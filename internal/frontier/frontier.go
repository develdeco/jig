// Package frontier is the frontier loop: it dispatches queued, unblocked
// slices to a build session backend, routes their results, and drives a
// ticket's slices from queued to green (or to a parked/stalled stop) one
// attempt at a time. frontier and verifydeliver share no in-memory state;
// the store on disk is their only interface.
package frontier

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// defaultMaxAttempts is RunOpts.MaxAttempts' default when left at zero.
const defaultMaxAttempts = 3

// Deps wires the frontier loop to its collaborators. Journal is bound by
// the caller to a specific store and ticket (e.g. func(l journal.Line)
// error { return journal.Append(st, ticket, l) }); Run never sets l.Ticket
// itself.
type Deps struct {
	Store   *store.Store
	Cfg     project.Config
	Machine project.MachineProject
	Backend session.Backend
	Rungs   staircase.Config
	Journal func(l journal.Line) error
	// Home is the jig home root whose pool holds the build leases:
	// home.Root() for the binary, a test's own directory in tests.
	Home string
	// Oracle runs an oracle command in a lease and returns its combined
	// output (oracleAtGreen). nil means the real run, envrun.ShellOutput
	// with the Windows short-path workaround; a test that is not about the
	// oracle hands one that passes.
	Oracle func(cmd, dir string) (string, error)
}

// runOracle runs cmd in dir through d.Oracle, or for real when it is nil.
func (d Deps) runOracle(cmd, dir string) (string, error) {
	if d.Oracle != nil {
		return d.Oracle(cmd, dir)
	}
	return envrun.ShellOutput(envrun.ShortenQuotedPath(cmd), dir, oracleLimit)
}

// oracleLimit bounds jig's own oracle run: the same bound a headless session's
// shell gives one command (ADR 0018), so an oracle that fits a builder's shell
// fits here too, and a hung one cannot hold the repo group.
const oracleLimit = 30 * time.Minute

// RunOpts configures one Run call.
type RunOpts struct {
	Ticket      string
	AnswerQID   string
	AnswerText  string
	MaxAttempts int // default 3
}

// RunReport summarizes a Run call's outcome across every slice of the
// ticket, as of when the run stopped: not just the slices this call
// touched, but every slice's current state.
type RunReport struct {
	Green, Stalled, NeedsInput, EnvBlocked []string // slice ids
	PendingQuestion                        string   // first open question id
	Stopped                                bool
	StopReason                             string // "stall: <framed msg>" | "attempt-cap: ..."
}

// Run drives o.Ticket's frontier: queued slices whose dependencies are all
// green get dispatched, in repo-grouped, per-repo-serial batches, until no
// slice is left eligible or a stall halts the run.
//
// NOTE: v0.1 supports a single repo per project; every slice's workspace
// is mapped to d.Cfg.Repos[0].
func Run(d Deps, o RunOpts) (RunReport, error) {
	ticket := o.Ticket
	maxAttempts := o.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}

	if err := d.Store.Sync(); err != nil {
		return RunReport{}, fmt.Errorf("frontier: sync: %w", err)
	}

	if o.AnswerQID != "" {
		if err := answerAndRequeue(d, ticket, o.AnswerQID, o.AnswerText); err != nil {
			return RunReport{}, err
		}
	}

	if len(d.Cfg.Repos) == 0 {
		return RunReport{}, errors.New("frontier: project has no repos")
	}
	repo := d.Cfg.Repos[0]
	repoName := repo.Name()
	target := repo.TargetBranch()

	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return RunReport{}, fmt.Errorf("frontier: read slices: %w", err)
	}
	sliceByID := make(map[string]store.Slice, len(slices))
	wsRepo := make(map[string]string, len(slices))
	for _, s := range slices {
		sliceByID[s.ID] = s
		wsRepo[s.Workspace] = repoName
	}

	rc := &runCtx{
		d:           d,
		ticket:      ticket,
		maxAttempts: maxAttempts,
		repoName:    repoName,
		remote:      repo.Remote,
		target:      target,
	}

	for {
		states, err := readStates(d.Store, ticket, slices)
		if err != nil {
			return RunReport{}, err
		}
		frontier := computeFrontier(slices, states)
		if len(frontier) == 0 {
			break
		}

		groups := Schedule(frontier, wsRepo)
		var wg sync.WaitGroup
		for _, group := range groups {
			group := group
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, id := range group {
					if rc.isHalted() {
						return
					}
					rc.processSlice(sliceByID[id])
					if rc.isHalted() {
						return
					}
				}
			}()
		}
		wg.Wait()

		if rc.isHalted() {
			break
		}
	}

	if err := rc.firstErr(); err != nil {
		return RunReport{}, err
	}

	stopped, stopReason := rc.stopState()
	return buildReport(d.Store, ticket, slices, stopped, stopReason)
}

// answerAndRequeue records the answer to qid and re-queues the slice it
// belongs to, keeping its attempt count.
func answerAndRequeue(d Deps, ticket, qid, text string) error {
	slice, err := d.Store.Answer(ticket, qid, text)
	if err != nil {
		return fmt.Errorf("frontier: answer %s: %w", qid, err)
	}
	st, err := d.Store.ReadSliceState(ticket, slice)
	if err != nil {
		return fmt.Errorf("frontier: read slice state %s: %w", slice, err)
	}
	st.State = "queued"
	st.Question = ""
	st.Reason = ""
	st.Signature = ""
	st.StallSummary = ""
	if err := d.Store.WriteSliceState(ticket, slice, st); err != nil {
		return fmt.Errorf("frontier: write slice state %s: %w", slice, err)
	}
	if err := d.Journal(journal.Line{Slice: slice, Event: "answer"}); err != nil {
		return fmt.Errorf("frontier: journal answer: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: slice %s queued", ticket, slice)); err != nil {
		return fmt.Errorf("frontier: push: %w", err)
	}
	return nil
}

func readStates(st *store.Store, ticket string, slices []store.Slice) (map[string]store.SliceState, error) {
	states := make(map[string]store.SliceState, len(slices))
	for _, s := range slices {
		state, err := st.ReadSliceState(ticket, s.ID)
		if err != nil {
			return nil, fmt.Errorf("frontier: read slice state %s: %w", s.ID, err)
		}
		states[s.ID] = state
	}
	return states, nil
}

// computeFrontier returns, in slices.yaml order, every slice that is queued
// and whose every blocking slice is green.
func computeFrontier(slices []store.Slice, states map[string]store.SliceState) []store.Slice {
	var out []store.Slice
	for _, s := range slices {
		if states[s.ID].State != "queued" {
			continue
		}
		ready := true
		for _, dep := range s.BlockedBy {
			if states[dep].State != "green" {
				ready = false
				break
			}
		}
		if ready {
			out = append(out, s)
		}
	}
	return out
}

// buildReport reads every slice's current, final state and every open
// question to assemble the run's report.
func buildReport(st *store.Store, ticket string, slices []store.Slice, stopped bool, stopReason string) (RunReport, error) {
	report := RunReport{Stopped: stopped, StopReason: stopReason}
	for _, s := range slices {
		state, err := st.ReadSliceState(ticket, s.ID)
		if err != nil {
			return RunReport{}, fmt.Errorf("frontier: read slice state %s: %w", s.ID, err)
		}
		switch state.State {
		case "green":
			report.Green = append(report.Green, s.ID)
		case "stalled":
			report.Stalled = append(report.Stalled, s.ID)
		case "needs-input":
			report.NeedsInput = append(report.NeedsInput, s.ID)
		case "env-blocked":
			report.EnvBlocked = append(report.EnvBlocked, s.ID)
		}
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return RunReport{}, fmt.Errorf("frontier: read questions: %w", err)
	}
	for _, q := range questions {
		if q.Status == "open" {
			report.PendingQuestion = q.ID
			break
		}
	}

	// A ticket that ends this call with any stalled slice is not green,
	// whether or not the stall fired during this call: a re-run that finds
	// an already-stalled slice sitting outside the frontier (so this call's
	// own rc.stopped never gets set) must still report Stopped, or a script
	// chaining `jig run && jig gate && jig publish` proceeds on it.
	if len(report.Stalled) > 0 && !report.Stopped {
		report.Stopped = true
		report.StopReason = fmt.Sprintf("stall: slice(s) %s already stalled", strings.Join(report.Stalled, ", "))
	}
	return report, nil
}

// runCtx carries the state one Run call shares across its concurrent repo
// groups: the stall counter (scoped to this Run call), the halt flag a
// stall raises, the first infrastructure error seen, and the ticket's branch,
// resolved once. The fields from stall through err are guarded by mu.
type runCtx struct {
	d           Deps
	ticket      string
	maxAttempts int
	repoName    string
	remote      string
	target      string

	mu         sync.Mutex
	stall      outcome.StallCounter
	halted     bool // a stall fired: stop dispatching entirely
	stopped    bool // report as Stopped (stall or attempt-cap)
	stopReason string
	err        error

	// storeMu serializes every Store/Journal/Push/question call this run
	// makes: the store itself takes no lock of its own for these, and
	// concurrent repo-group goroutines (see Run's per-group fan-out) would
	// otherwise race on it - e.g. two slices' NextQuestionID+WriteQuestion
	// sequences interleaving into the same question id. It guards call
	// duration only, never held across an rc.mu-guarded section.
	storeMu sync.Mutex

	// branchOnce guards the one resolution of the ticket's branch this Run
	// makes (see ticketBranch); branch, adopted and branchErr are its result.
	branchOnce sync.Once
	branch     string
	adopted    bool
	branchErr  error
}

// ticketBranch is the ticket's working branch, resolved on first use and
// never again: every slice attempt of one Run, across every concurrent repo
// group, builds on the branch the first of them resolved, even if
// ticket.yaml changes mid-run (Store.TicketBranch's own rule for a command
// that names the branch more than once). It resolves on the first slice
// rather than up front, so a Run with nothing on its frontier never reads
// the record. adopted says whether the ticket recorded the branch: one built
// outside jig, on origin by definition, where the ordinary jig/<ticket> is
// not until a publish pushes it.
func (rc *runCtx) ticketBranch() (branch string, adopted bool, err error) {
	rc.branchOnce.Do(func() {
		rec, err := rc.d.Store.ReadTicket(rc.ticket)
		if err != nil {
			rc.branchErr = err
			return
		}
		rc.branch, rc.branchErr = rc.d.Store.ResolveTicketBranch(rc.ticket, rec, rc.target)
		rc.adopted = rec.Adopted()
	})
	return rc.branch, rc.adopted, rc.branchErr
}

func (rc *runCtx) isHalted() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.halted
}

func (rc *runCtx) firstErr() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.err
}

func (rc *runCtx) stopState() (bool, string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.stopped, rc.stopReason
}

// fail records an infrastructure error and halts the run: these are bugs or
// environment problems (a lease that won't clone, a journal write that
// fails), not scenario outcomes, so they abort rather than route like a
// slice result.
func (rc *runCtx) fail(err error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.err == nil {
		rc.err = err
	}
	rc.halted = true
}

func (rc *runCtx) journal(l journal.Line) {
	rc.storeMu.Lock()
	err := rc.d.Journal(l)
	rc.storeMu.Unlock()
	if err != nil {
		rc.fail(fmt.Errorf("frontier: journal %s: %w", l.Event, err))
	}
}

func (rc *runCtx) push(sliceID, state string) {
	rc.storeMu.Lock()
	err := rc.d.Store.Push(fmt.Sprintf("%s: slice %s %s", rc.ticket, sliceID, state))
	rc.storeMu.Unlock()
	if err != nil {
		rc.fail(fmt.Errorf("frontier: push: %w", err))
	}
}

func (rc *runCtx) writeState(sliceID string, st store.SliceState) bool {
	rc.storeMu.Lock()
	err := rc.d.Store.WriteSliceState(rc.ticket, sliceID, st)
	rc.storeMu.Unlock()
	if err != nil {
		rc.fail(fmt.Errorf("frontier: write slice state %s: %w", sliceID, err))
		return false
	}
	return true
}

// readSliceState reads sliceID's state, serialized against every other
// store/journal/question write and Push this run makes (see storeMu).
func (rc *runCtx) readSliceState(sliceID string) (store.SliceState, error) {
	rc.storeMu.Lock()
	defer rc.storeMu.Unlock()
	return rc.d.Store.ReadSliceState(rc.ticket, sliceID)
}

// maxEarlierFiles caps one earlier slice's file list in slice.json; the
// rest are counted in its files_omitted.
const maxEarlierFiles = 50

// earlierSlices lists the ticket's slices other than sliceID whose green
// jig verified, in the order they first verified (journal.VerifiedSlices):
// the summary from the latest verified attempt's result.json, and the
// files the slice's attempts changed, the union of each attempt's own range
// in leaseDir. A range git cannot resolve (an older journal without
// dispatch bases, a commit no longer in the lease) adds no files.
func (rc *runCtx) earlierSlices(sliceID, leaseDir string) ([]earlierSlice, error) {
	rc.storeMu.Lock()
	lines, err := journal.Read(rc.d.Store, rc.ticket)
	rc.storeMu.Unlock()
	if err != nil {
		return nil, err
	}
	out := []earlierSlice{}
	for _, v := range journal.VerifiedSlices(lines, sliceID) {
		e := earlierSlice{ID: v.Slice, Files: []string{}}
		if data, err := os.ReadFile(resultJSONPath(rc.d.Store, rc.ticket, v.Slice, v.Attempt)); err == nil {
			e.Summary = outcome.ParseJSON("slice", data).Summary
		}
		seen := map[string]bool{}
		for _, r := range v.Ranges {
			diff, err := gitx.Run(leaseDir, "-c", "core.quotePath=false", "diff", "--name-only", r[0]+".."+r[1])
			if err != nil {
				continue
			}
			for _, f := range strings.Split(strings.TrimSpace(diff), "\n") {
				if f = strings.TrimSpace(f); f == "" || seen[f] {
					continue
				}
				seen[f] = true
				if len(e.Files) == maxEarlierFiles {
					e.FilesOmitted++
					continue
				}
				e.Files = append(e.Files, f)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// leaseHead is dir's HEAD, or "" when git cannot say: the end of an
// attempt's turn, for its result line.
func leaseHead(dir string) string {
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		return ""
	}
	return head
}

// lastOracleSeconds is the wall time of jig's latest run of command with
// env class env up on this ticket (journal.LastOracleSeconds), reading the
// journal under storeMu.
func (rc *runCtx) lastOracleSeconds(command, env string) (int, error) {
	rc.storeMu.Lock()
	defer rc.storeMu.Unlock()
	lines, err := journal.Read(rc.d.Store, rc.ticket)
	if err != nil {
		return 0, err
	}
	return journal.LastOracleSeconds(lines, command, env), nil
}

// failedAttempts counts sliceID's attempts that failed at the work
// (journal.FailedAttempts), reading the journal under storeMu.
func (rc *runCtx) failedAttempts(sliceID string) (int, error) {
	rc.storeMu.Lock()
	defer rc.storeMu.Unlock()
	lines, err := journal.Read(rc.d.Store, rc.ticket)
	if err != nil {
		return 0, err
	}
	return journal.FailedAttempts(lines, sliceID), nil
}

// writeNewQuestion allocates the next question id and writes q under it,
// holding storeMu across both steps so two concurrent repo groups can never
// allocate and write the same id.
func (rc *runCtx) writeNewQuestion(sliceID, body string) (string, error) {
	rc.storeMu.Lock()
	defer rc.storeMu.Unlock()
	qid := rc.d.Store.NextQuestionID(rc.ticket)
	if err := rc.d.Store.WriteQuestion(rc.ticket, store.Question{ID: qid, Slice: sliceID, Status: "open", Body: body}); err != nil {
		return "", err
	}
	return qid, nil
}

// processSlice runs exactly one attempt of sl: acquire the lease, bring up
// its env class if any, select a model, write slice.json, dispatch, and
// route the result.
func (rc *runCtx) processSlice(sl store.Slice) {
	d := rc.d
	ticket := rc.ticket

	branch, adopted, err := rc.ticketBranch()
	if err != nil {
		rc.fail(fmt.Errorf("frontier: resolve branch for %s: %w", ticket, err))
		return
	}
	// An adopted branch is on origin by definition, so one that is not is
	// refused (BRANCH_NOT_FOUND) instead of being cut from the target: the
	// fixes would be built on a branch that lacks the author's code. The
	// commits jig built on it and verified are what make the lease's copy
	// jig's to keep: a lease that holds none that origin lacks follows
	// origin's copy of the branch even where the two have diverged, since what
	// it holds of its own is not jig's work (pool.RecutUnlessBuilt), and one
	// that does hold one is refused as diverged, as always.
	var (
		acquireOpts []pool.Option
		built       []string
	)
	if adopted {
		if built, err = rc.builtCommits(); err != nil {
			rc.fail(fmt.Errorf("frontier: read journal: %w", err))
			return
		}
		acquireOpts = append(acquireOpts, pool.MustExistOnOrigin(), pool.RecutUnlessBuilt(built))
	}
	lease, err := pool.Acquire(d.Home, rc.repoName, rc.remote, rc.target, branch, ticket, pool.Build, acquireOpts...)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: acquire lease for %s: %w", sl.ID, err))
		return
	}
	// The lease's copy must hold every commit jig built on the branch, or the
	// next commits would go on a branch that lacks the earlier ones: they
	// are on another machine, or lost (the same rule the gate applies to the
	// copy it reviews).
	if adopted {
		if err := pool.RequireBuilt(lease.Dir, "HEAD", lease.Dir, ticket, branch, "jig run "+ticket, built); err != nil {
			rc.fail(err)
			return
		}
	}

	startSHA, ok := rc.ensureStartSHA(lease, adopted && len(built) == 0)
	if !ok {
		return
	}

	m, err := manifest.Resolve(lease.Dir)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: resolve manifest for %s: %w", sl.ID, err))
		return
	}
	ws, _ := m.Workspace(sl.Workspace)

	if sl.Env != "" {
		h, ok := rc.bringUpEnv(sl, m, lease)
		if !ok {
			return
		}
		defer rc.tearDownEnv(sl, h)
	}

	st, err := rc.readSliceState(sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, err))
		return
	}

	// Each earlier attempt of this slice that failed at the work climbs the
	// staircase a rung (ADR 0019).
	sig := measureSignals(lease.Dir, startSHA, m)
	failed, err := rc.failedAttempts(sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: count failed attempts of %s: %w", sl.ID, err))
		return
	}
	sig.FailedAttempts = failed
	model := staircase.Select(d.Rungs, sig)

	attempt := st.Attempts + 1
	st.State = "building"
	st.Attempts = attempt
	if !rc.writeState(sl.ID, st) {
		return
	}

	sjPath := sliceJSONPath(d.Store, ticket, sl.ID, attempt)
	rjPath := resultJSONPath(d.Store, ticket, sl.ID, attempt)

	oracleCmd := m.OracleCmd(sl.Oracle, ws)
	oracleSeconds, err := rc.lastOracleSeconds(oracleCmd, sl.Env)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: read the oracle's last run time for %s: %w", sl.ID, err))
		return
	}
	earlier, err := rc.earlierSlices(sl.ID, lease.Dir)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: read earlier slices for %s: %w", sl.ID, err))
		return
	}
	// The attempt's base: what its changed files are measured from, once it
	// verifies (earlierSlices).
	base, err := gitx.RevParse(lease.Dir, "HEAD")
	if err != nil {
		rc.fail(fmt.Errorf("frontier: resolve the lease head for %s: %w", sl.ID, err))
		return
	}

	body := sliceJSONBody{
		ID:            sl.ID,
		Goal:          sl.Goal,
		Oracle:        sl.Oracle,
		Workspace:     sl.Workspace,
		Env:           sl.Env,
		Attempt:       attempt,
		BriefSections: resolveBriefSections(d.Store, ticket, sl),
		AttemptLog:    buildAttemptLog(d.Store, ticket, sl.ID, attempt),
		Answer:        answerFor(d.Store, ticket, sl.ID),
		OracleSeconds: oracleSeconds,
		EarlierSlices: earlier,
	}
	if err := writeSliceJSON(sjPath, body); err != nil {
		rc.fail(fmt.Errorf("frontier: write slice.json for %s: %w", sl.ID, err))
		return
	}

	_, fixTurns := d.Backend.(session.Resumer)
	prompt := renderDispatchPrompt(sl.ID, ticket, sl.Goal, oracleCmd, sjPath, rjPath, fixTurns)

	rc.journal(journal.Line{Slice: sl.ID, Event: "dispatch", Model: model, Commit: base, Attempt: attempt})
	if rc.isHalted() {
		return
	}

	dispatch := session.Dispatch{
		Ticket:     ticket,
		Slice:      sl.ID,
		Attempt:    attempt,
		Worktree:   lease.Dir,
		SliceJSON:  sjPath,
		ResultJSON: rjPath,
		Model:      model,
		Prompt:     prompt,
		Screen:     true,
	}

	// A backend that can resume its session reports the session's id, so a
	// red oracle run can go back to that same session (oracleAtGreen).
	var sessionID string
	var runErr error
	if r, ok := d.Backend.(session.Resumer); ok {
		sessionID, runErr = r.RunResumable(dispatch)
	} else {
		runErr = d.Backend.Run(dispatch)
	}
	res := readResult(runErr, rjPath)

	rc.journal(journal.Line{Slice: sl.ID, Event: "result", Outcome: res.Outcome, Commit: res.Commit, Head: leaseHead(lease.Dir), Attempt: attempt})
	if rc.isHalted() {
		return
	}

	res = rc.oracleAtGreen(sl, lease, attempt, dispatch, sessionID, oracleCmd, startSHA, res)
	if rc.isHalted() {
		return
	}

	rc.route(sl, lease, attempt, res, startSHA)
}

// maxOracleFixes bounds how many times a red oracle run goes back to the
// builder's own session before the attempt counts as failed (ADR 0020).
const maxOracleFixes = 2

// readResult turns one dispatch's backend error and the result.json it left
// into the attempt's result.
func readResult(runErr error, rjPath string) outcome.Result {
	if runErr != nil {
		return outcome.Result{Outcome: outcome.Failed, Summary: fmt.Sprintf("backend run failed: %v", runErr)}
	}
	data, err := os.ReadFile(rjPath)
	if err != nil {
		return outcome.Result{Outcome: outcome.Failed, Summary: "session produced no result.json"}
	}
	return outcome.ParseJSON("slice", data)
}

// oracleAtGreen runs sl's oracle in the lease when res claims a green that
// verifies, and journals each run (event "oracle", pass or fail, with the
// lease's HEAD when its tree is clean). A red run goes back to the builder's
// own session, with the oracle's output, up to maxOracleFixes times, where
// the backend can resume it. After that, or where it cannot, the attempt
// fails with the output in its summary, written over result.json so the next
// attempt's log carries it. Any other result passes through for route to
// handle (ADR 0020).
func (rc *runCtx) oracleAtGreen(sl store.Slice, lease pool.Lease, attempt int, dispatch session.Dispatch, sessionID, oracleCmd, startSHA string, res outcome.Result) outcome.Result {
	for fixes := 0; ; fixes++ {
		if res.Outcome != outcome.Green {
			return res
		}
		if _, ok := verifyGreen(lease.Dir, startSHA, res); !ok {
			return res
		}

		// An oracle run vouches for the reported commit only when the tracked
		// files are that commit: uncommitted edits go back to the session
		// like a red run, without running the oracle.
		var tail, prompt string
		if dirty := trackedChanges(lease.Dir); dirty != "" {
			tail = "uncommitted changes to tracked files:\n" + dirty
			prompt = renderDirtyTreePrompt(oracleCmd, dirty, dispatch.ResultJSON)
		} else {
			evidence := cleanHead(lease.Dir)
			started := time.Now()
			out, err := rc.d.runOracle(oracleCmd, lease.Dir)
			// Rounded up, so every run records at least a second and 0
			// keeps meaning "never ran".
			seconds := int((time.Since(started) + time.Second - 1) / time.Second)
			result := "pass"
			if err != nil {
				result = "fail"
			}
			rc.journal(journal.Line{Slice: sl.ID, Event: "oracle", Outcome: result, Commit: evidence, Attempt: attempt, Command: oracleCmd, Env: sl.Env, Seconds: seconds})
			if err == nil {
				return res
			}
			tail = outputTail(out)
			prompt = renderOracleFixPrompt(oracleCmd, seconds, tail, dispatch.ResultJSON)
		}

		resumer, ok := rc.d.Backend.(session.Resumer)
		if !ok || sessionID == "" || fixes == maxOracleFixes || rc.isHalted() {
			failed := outcome.Result{
				Outcome: outcome.CodeBug,
				Summary: fmt.Sprintf("jig's oracle run failed after %d fix turn(s): %s", fixes, oneLine(tail)),
				Commit:  res.Commit,
			}
			if data, merr := json.Marshal(failed); merr == nil {
				_ = os.WriteFile(dispatch.ResultJSON, data, 0o644)
			}
			return failed
		}

		// The session's last result is spent: a turn that writes no new one
		// must not be read back as green.
		_ = os.Remove(dispatch.ResultJSON)
		fix := dispatch
		fix.Prompt = prompt
		res = readResult(resumer.Resume(fix, sessionID), dispatch.ResultJSON)
		rc.journal(journal.Line{Slice: sl.ID, Event: "result", Outcome: res.Outcome, Commit: res.Commit, Head: leaseHead(lease.Dir), Attempt: attempt})
		if rc.isHalted() {
			return res
		}
	}
}

// trackedChanges returns dir's uncommitted changes to tracked files (git
// status, untracked files left out), or "" when there are none or git
// cannot say.
func trackedChanges(dir string) string {
	status, err := gitx.Run(dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(status)
}

// cleanHead returns dir's HEAD sha when its working tree is clean (nothing
// modified, staged or untracked), else "": an oracle run is evidence about a
// commit only when the tree it ran on was exactly that commit.
func cleanHead(dir string) string {
	status, err := gitx.Run(dir, "status", "--porcelain")
	if err != nil || strings.TrimSpace(status) != "" {
		return ""
	}
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		return ""
	}
	return head
}

// outputTail keeps the last 4,000 bytes of an oracle's output: enough to
// show a failure, small enough for a prompt and a summary.
func outputTail(out string) string {
	out = strings.TrimSpace(out)
	if len(out) > 4000 {
		out = out[len(out)-4000:]
	}
	return out
}

// oneLine flattens the end of s to one line of at most about 600 bytes, for
// a result summary. The kept part starts at a line boundary, so two identical
// failures summarize identically and the stall counter can match them.
func oneLine(s string) string {
	if len(s) > 600 {
		s = s[len(s)-600:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// ensureStartSHA writes <ticket>/start.<repoName>.sha the first time this
// repo is dispatched into for ticket, recording the sha the lease's branch
// started from: what a build's commits must descend from (verifyGreen), and
// where a gate round's scope falls back to when it has no merge base with the
// target (resolveScopeBase). Squash and reconcile do not read it. That is
// where pool.Acquire cut the branch: origin/<branch> when origin already has
// the branch - one built outside jig and adopted, whose start sha `jig gate
// --branch` normally records first - else origin/<target>. It returns the
// recorded sha and whether the caller may proceed.
//
// follow is true for an adopted branch jig has built nothing on: it is the
// author's until jig builds on it, so its start sha follows it, and every
// dispatch records origin's tip again, where the lease was just cut or
// fast-forwarded to. An author who pushed to the branch, or rewrote it, after
// the adoption is built on as the branch is when jig starts, where a start sha
// fixed at adoption would fail every commit for a rewritten history and stall
// the slices with no stated cause. Once jig has built (the journal records a
// commit that verified), the start sha stays: those commits descend from it.
func (rc *runCtx) ensureStartSHA(lease pool.Lease, follow bool) (string, bool) {
	path := rc.d.Store.StartSHAPath(rc.ticket, rc.repoName)
	if !follow {
		if data, err := os.ReadFile(path); err == nil {
			return trimSHA(data), true
		} else if !os.IsNotExist(err) {
			rc.fail(fmt.Errorf("frontier: read %s: %w", path, err))
			return "", false
		}
	}

	startRef := "origin/" + rc.target
	if _, err := gitx.RevParse(lease.Dir, "refs/remotes/origin/"+lease.Branch); err == nil {
		startRef = "origin/" + lease.Branch
	}
	sha, err := gitx.RevParse(lease.Dir, startRef)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: resolve %s: %w", startRef, err))
		return "", false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		rc.fail(fmt.Errorf("frontier: create ticket dir: %w", err))
		return "", false
	}
	if err := os.WriteFile(path, []byte(sha), 0o644); err != nil {
		rc.fail(fmt.Errorf("frontier: write %s: %w", path, err))
		return "", false
	}
	return sha, true
}

// builtCommits is the commits the journal records jig built on the ticket's
// branch and verified (journal.BuiltCommits).
func (rc *runCtx) builtCommits() ([]string, error) {
	rc.storeMu.Lock()
	defer rc.storeMu.Unlock()
	lines, err := journal.Read(rc.d.Store, rc.ticket)
	if err != nil {
		return nil, err
	}
	return journal.BuiltCommits(lines), nil
}

func trimSHA(data []byte) string {
	s := string(data)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// bringUpEnv brings sl's env class up in lease, routing an Unavailable
// failure to the env-blocked state. It returns the *envrun.Handle Up
// allocated (nil when dispatch may not proceed) so the caller's deferred
// tearDownEnv tears down the exact instance that came up - in particular
// the exact port Up allocated, not a guessed or zero one - and whether
// dispatch may proceed.
func (rc *runCtx) bringUpEnv(sl store.Slice, m manifest.Manifest, lease pool.Lease) (*envrun.Handle, bool) {
	ec, ok := m.Envs[sl.Env]
	if !ok {
		// NOTE: a slice naming an env class the manifest never declares is a
		// brief/slices.yaml authoring error, not a runtime outcome; treat it
		// as an infrastructure failure rather than guessing a policy.
		rc.fail(fmt.Errorf("frontier: slice %s names undeclared env class %q", sl.ID, sl.Env))
		return nil, false
	}

	h, err := envrun.Up(ec, rc.ticket, lease.Dir)
	if err == nil {
		rc.journal(journal.Line{Slice: sl.ID, Event: "env-up"})
		if rc.isHalted() {
			return nil, false
		}
		return h, true
	}

	var unavail *envrun.Unavailable
	if !errors.As(err, &unavail) {
		rc.fail(fmt.Errorf("frontier: env up for %s: %w", sl.ID, err))
		return nil, false
	}

	policyOutcome := unavail.Policy
	if policyOutcome == "" {
		policyOutcome = "pause"
	}
	rc.journal(journal.Line{Slice: sl.ID, Event: "env-unavailable", Outcome: policyOutcome})

	st, rerr := rc.readSliceState(sl.ID)
	if rerr != nil {
		rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, rerr))
		return nil, false
	}
	st.State = "env-blocked"
	st.Reason = "env-up-failed"
	if !rc.writeState(sl.ID, st) {
		return nil, false
	}
	rc.push(sl.ID, "env-blocked")
	return nil, false
}

// tearDownEnv brings h, sl's already-up env class instance, back down; this
// is a deferred cleanup so it runs whatever the dispatch's outcome was. h
// must be the *envrun.Handle bringUpEnv returned, so Down substitutes the
// same port Up allocated rather than a fabricated zero value.
func (rc *runCtx) tearDownEnv(sl store.Slice, h *envrun.Handle) {
	_ = h.Down()
	rc.journal(journal.Line{Slice: sl.ID, Event: "env-down"})
}

// route applies step 9 of the algorithm: it turns one dispatch's parsed
// result into the slice's next state, journaling and pushing as it goes.
// startSHA is the ticket/repo's recorded fork point, passed through to
// verifyGreen so a claimed-green commit can be checked against it.
func (rc *runCtx) route(sl store.Slice, lease pool.Lease, attempt int, res outcome.Result, startSHA string) {
	if res.Outcome == outcome.Green {
		if reason, ok := verifyGreen(lease.Dir, startSHA, res); ok {
			// The commit is the ticket's now: the result line records what the
			// builder claimed, this one that it verified, and only these are
			// the commits jig built (journal.BuiltCommits). It goes in before
			// the slice is marked green, so a run that stops in between still
			// has the commit recorded.
			rc.journal(journal.Line{Slice: sl.ID, Event: "verified", Commit: res.Commit, Attempt: attempt})
			if rc.isHalted() {
				return
			}
			st, err := rc.readSliceState(sl.ID)
			if err != nil {
				rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, err))
				return
			}
			st.State = "green"
			st.Attempts = attempt
			st.Question = ""
			st.Reason = ""
			st.Signature = ""
			st.StallSummary = ""
			if !rc.writeState(sl.ID, st) {
				return
			}
			rc.push(sl.ID, "green")
			return
		} else {
			res = outcome.Result{Outcome: outcome.Failed, Summary: "green did not verify: " + reason}
			// falls through to the code-bug/oracle-wrong/failed handling below.
		}
	}

	switch res.Outcome {
	case outcome.NeedsInput, outcome.FlawedBrief:
		rc.routeQuestion(sl, attempt, res)
	case outcome.BlockedByEnv:
		rc.routeBlockedByEnv(sl, attempt)
	default: // code-bug, oracle-wrong, failed (including a failed verify-green)
		rc.routeFailure(sl, attempt, res)
	}
}

// verifyGreen checks a claimed-green result against the lease on disk: the
// commit must exist there, be new (a descendant of startSHA, the run's
// recorded fork point - not startSHA itself) and reachable from HEAD, and
// every declared artifact must exist inside the commit's tree (checked with
// `git cat-file -e <commit>:<path>`, never the worktree, since an
// uncommitted scratch file must not satisfy it).
func verifyGreen(leaseDir, startSHA string, res outcome.Result) (reason string, ok bool) {
	if res.Commit == "" {
		return "commit missing", false
	}
	if res.Commit == startSHA {
		return "commit is the run's start sha: no new work", false
	}
	if _, err := gitx.Run(leaseDir, "cat-file", "-e", res.Commit+"^{commit}"); err != nil {
		return fmt.Sprintf("commit %s not found in lease", res.Commit), false
	}
	if _, err := gitx.Run(leaseDir, "merge-base", "--is-ancestor", startSHA, res.Commit); err != nil {
		return fmt.Sprintf("commit %s is not a descendant of start sha %s", res.Commit, startSHA), false
	}
	if _, err := gitx.Run(leaseDir, "merge-base", "--is-ancestor", res.Commit, "HEAD"); err != nil {
		return fmt.Sprintf("commit %s is not reachable from HEAD", res.Commit), false
	}
	for _, a := range res.Artifacts {
		path := filepath.ToSlash(a)
		if _, err := gitx.Run(leaseDir, "cat-file", "-e", res.Commit+":"+path); err != nil {
			return fmt.Sprintf("artifact %s missing at commit %s", a, res.Commit), false
		}
	}
	return "", true
}

func (rc *runCtx) routeQuestion(sl store.Slice, attempt int, res outcome.Result) {
	d := rc.d
	ticket := rc.ticket

	body := res.Question
	reason := ""
	if res.Outcome == outcome.FlawedBrief {
		if headings := briefHeadingsFor(d.Store, ticket, sl); len(headings) > 0 {
			body = flawedBriefQuestionBody(ticket, res.Summary, headings)
			reason = "flawed-brief"
		} else {
			// A flawed brief is a finding about brief sections to amend, and
			// the amend-then-requeue remedy needs some: `jig requeue
			// --from-brief-diff` requeues the slices whose sections
			// changed. A slice with none - on a ticket with no brief (one
			// that adopted a branch, or was never given one), a gate fix
			// slice, whose work no brief section describes - has nothing to
			// amend, so the session's finding is a plain question for the
			// human to answer, like any other. `jig status` decides the same
			// way (resumeCommand). The journal still records what the
			// builder reported.
			body = res.Summary
		}
	}

	qid, err := rc.writeNewQuestion(sl.ID, body)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: write question for %s: %w", sl.ID, err))
		return
	}

	st, err := rc.readSliceState(sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, err))
		return
	}
	st.State = "needs-input"
	st.Attempts = attempt
	st.Question = qid
	st.Reason = reason
	if !rc.writeState(sl.ID, st) {
		return
	}

	rc.journal(journal.Line{Slice: sl.ID, Event: "question", Outcome: res.Outcome, Attempt: attempt})
	rc.push(sl.ID, "needs-input")
}

func (rc *runCtx) routeBlockedByEnv(sl store.Slice, attempt int) {
	st, err := rc.readSliceState(sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, err))
		return
	}
	st.State = "env-blocked"
	st.Attempts = attempt
	st.Reason = "blocked-by-env"
	if !rc.writeState(sl.ID, st) {
		return
	}
	rc.push(sl.ID, "env-blocked")
}

func (rc *runCtx) routeFailure(sl store.Slice, attempt int, res outcome.Result) {
	// The signature's unit is the slice, not the fixed literal "slice": two
	// different slices failing once each with the same outcome/summary must
	// not collide into a shared stall count.
	sig := outcome.Signature(sl.ID, res.Outcome, res.Summary)
	rc.mu.Lock()
	_, stalled := rc.stall.Observe(sig, false)
	rc.mu.Unlock()

	if stalled {
		st, err := rc.readSliceState(sl.ID)
		if err != nil {
			rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, err))
			return
		}
		st.State = "stalled"
		st.Attempts = attempt
		st.Reason = "stall"
		st.Signature = sig
		st.StallSummary = res.Summary
		if !rc.writeState(sl.ID, st) {
			return
		}
		rc.journal(journal.Line{Slice: sl.ID, Event: "stall", Outcome: res.Outcome, Attempt: attempt})
		rc.push(sl.ID, "stalled")

		// The remedy names one complete, working command, chosen from the
		// slice's own structure the same way status.go's stalled hint
		// does: a slice with a brief section to amend (FromBrief non-empty)
		// is remediated by amending it and requeuing with
		// --from-brief-diff; one with none (a gate fix slice) has no
		// pending question to answer at this point either, so its only
		// working command is `jig requeue --slice`. res.Summary is
		// parenthesized rather than appended with a leading period, since
		// it usually ends in one itself (avoiding a run of two).
		remedy := fmt.Sprintf("run `jig requeue %s --slice %s`", rc.ticket, sl.ID)
		if len(sl.FromBrief) > 0 {
			remedy = fmt.Sprintf("amend the brief, then run `jig requeue %s --from-brief-diff`", rc.ticket)
		}
		reason := fmt.Sprintf(
			"stall: slice %s returned %s twice with no progress (%s) - a repeat is likely a misconception: %s",
			sl.ID, res.Outcome, res.Summary, remedy,
		)
		rc.mu.Lock()
		rc.stopped = true
		rc.halted = true
		if rc.stopReason == "" {
			rc.stopReason = reason
		}
		rc.mu.Unlock()
		return
	}

	if attempt >= rc.maxAttempts {
		st, err := rc.readSliceState(sl.ID)
		if err != nil {
			rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, err))
			return
		}
		st.State = "stalled"
		st.Attempts = attempt
		st.Reason = "attempt-cap"
		st.Signature = sig
		st.StallSummary = res.Summary
		if !rc.writeState(sl.ID, st) {
			return
		}
		rc.push(sl.ID, "stalled")

		reason := fmt.Sprintf("attempt-cap: slice %s exhausted %d attempts: %s", sl.ID, attempt, res.Summary)
		rc.mu.Lock()
		rc.stopped = true
		if rc.stopReason == "" {
			rc.stopReason = reason
		}
		rc.mu.Unlock()
		return
	}

	// Retry: back to queued, attempts kept.
	st, err := rc.readSliceState(sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("frontier: read slice state %s: %w", sl.ID, err))
		return
	}
	st.State = "queued"
	st.Attempts = attempt
	if !rc.writeState(sl.ID, st) {
		return
	}
	rc.push(sl.ID, "queued")
}
