// Package make is the frontier loop: it dispatches queued, unblocked slices
// to a build session backend, routes their results, and drives a ticket's
// slices from queued to green (or to a paused/stalled stop) one attempt at
// a time. make and verifydeliver share no in-memory state; the store on
// disk is their only interface.
package make

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/develdeco/jig/envrun"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/journal"
	"github.com/develdeco/jig/manifest"
	"github.com/develdeco/jig/outcome"
	"github.com/develdeco/jig/pool"
	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/session"
	"github.com/develdeco/jig/staircase"
	"github.com/develdeco/jig/store"
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
}

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
// NOTE: v0.1 supports a single repo per project, per the contract's own
// "v0.1 single repo" note on step 3; every slice's workspace is mapped to
// d.Cfg.Repos[0].
func Run(d Deps, o RunOpts) (RunReport, error) {
	ticket := o.Ticket
	maxAttempts := o.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}

	if err := d.Store.Sync(); err != nil {
		return RunReport{}, fmt.Errorf("make: sync: %w", err)
	}

	if o.AnswerQID != "" {
		if err := answerAndRequeue(d, ticket, o.AnswerQID, o.AnswerText); err != nil {
			return RunReport{}, err
		}
	}

	if len(d.Cfg.Repos) == 0 {
		return RunReport{}, errors.New("make: project has no repos")
	}
	repo := d.Cfg.Repos[0]
	repoName := repo.Name()
	target := repo.Target
	if target == "" {
		target = "main"
	}

	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return RunReport{}, fmt.Errorf("make: read slices: %w", err)
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
		return fmt.Errorf("make: answer %s: %w", qid, err)
	}
	st, err := d.Store.ReadSliceState(ticket, slice)
	if err != nil {
		return fmt.Errorf("make: read slice state %s: %w", slice, err)
	}
	st.State = "queued"
	st.Question = ""
	st.Reason = ""
	if err := d.Store.WriteSliceState(ticket, slice, st); err != nil {
		return fmt.Errorf("make: write slice state %s: %w", slice, err)
	}
	if err := d.Journal(journal.Line{Slice: slice, Event: "answer"}); err != nil {
		return fmt.Errorf("make: journal answer: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: slice %s queued", ticket, slice)); err != nil {
		return fmt.Errorf("make: push: %w", err)
	}
	return nil
}

func readStates(st *store.Store, ticket string, slices []store.Slice) (map[string]store.SliceState, error) {
	states := make(map[string]store.SliceState, len(slices))
	for _, s := range slices {
		state, err := st.ReadSliceState(ticket, s.ID)
		if err != nil {
			return nil, fmt.Errorf("make: read slice state %s: %w", s.ID, err)
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
			return RunReport{}, fmt.Errorf("make: read slice state %s: %w", s.ID, err)
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
		return RunReport{}, fmt.Errorf("make: read questions: %w", err)
	}
	for _, q := range questions {
		if q.Status == "open" {
			report.PendingQuestion = q.ID
			break
		}
	}
	return report, nil
}

// runCtx carries the state one Run call shares across its concurrent repo
// groups: the stall counter (run-scoped, per the contract), the halt flag a
// stall raises, and the first infrastructure error seen. Every field below
// mu is guarded by it; the store/journal/session calls each do their own
// locking and need no additional guard here.
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
	if err := rc.d.Journal(l); err != nil {
		rc.fail(fmt.Errorf("make: journal %s: %w", l.Event, err))
	}
}

func (rc *runCtx) push(sliceID, state string) {
	if err := rc.d.Store.Push(fmt.Sprintf("%s: slice %s %s", rc.ticket, sliceID, state)); err != nil {
		rc.fail(fmt.Errorf("make: push: %w", err))
	}
}

func (rc *runCtx) writeState(sliceID string, st store.SliceState) bool {
	if err := rc.d.Store.WriteSliceState(rc.ticket, sliceID, st); err != nil {
		rc.fail(fmt.Errorf("make: write slice state %s: %w", sliceID, err))
		return false
	}
	return true
}

// processSlice runs exactly one attempt of sl: acquire the lease, bring up
// its env class if any, select a model, write slice.json, dispatch, and
// route the result.
func (rc *runCtx) processSlice(sl store.Slice) {
	d := rc.d
	ticket := rc.ticket

	lease, err := pool.Acquire(rc.repoName, rc.remote, rc.target, "jig/"+ticket, ticket)
	if err != nil {
		rc.fail(fmt.Errorf("make: acquire lease for %s: %w", sl.ID, err))
		return
	}

	startSHA, ok := rc.ensureStartSHA(lease)
	if !ok {
		return
	}

	m, err := manifest.Resolve(lease.Dir)
	if err != nil {
		rc.fail(fmt.Errorf("make: resolve manifest for %s: %w", sl.ID, err))
		return
	}
	ws, _ := m.Workspace(sl.Workspace)

	if sl.Env != "" {
		if !rc.bringUpEnv(sl, m, lease) {
			return
		}
		defer rc.tearDownEnv(sl, m, lease)
	}

	sig := measureSignals(lease.Dir, startSHA)
	model := staircase.Select(d.Rungs, sig)

	st, err := d.Store.ReadSliceState(ticket, sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, err))
		return
	}
	attempt := st.Attempts + 1
	st.State = "building"
	st.Attempts = attempt
	if !rc.writeState(sl.ID, st) {
		return
	}

	sjPath := sliceJSONPath(d.Store, ticket, sl.ID, attempt)
	rjPath := resultJSONPath(d.Store, ticket, sl.ID, attempt)

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
	}
	if err := writeSliceJSON(sjPath, body); err != nil {
		rc.fail(fmt.Errorf("make: write slice.json for %s: %w", sl.ID, err))
		return
	}

	oracleCmd := m.OracleCmd(sl.Oracle, ws)
	prompt := renderDispatchPrompt(sl.ID, ticket, sl.Goal, oracleCmd, sjPath, rjPath)

	rc.journal(journal.Line{Slice: sl.ID, Event: "dispatch", Model: model, Attempt: attempt})
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

	var res outcome.Result
	if err := d.Backend.Run(dispatch); err != nil {
		res = outcome.Result{Outcome: outcome.Failed, Summary: fmt.Sprintf("backend run failed: %v", err)}
	} else if data, rerr := os.ReadFile(rjPath); rerr != nil {
		res = outcome.Result{Outcome: outcome.Failed, Summary: "session produced no result.json"}
	} else {
		res = outcome.ParseJSON("slice", data)
	}

	rc.journal(journal.Line{Slice: sl.ID, Event: "result", Outcome: res.Outcome, Commit: res.Commit, Attempt: attempt})
	if rc.isHalted() {
		return
	}

	rc.route(sl, lease, attempt, res)
}

// ensureStartSHA writes <ticket>/start.<repoName>.sha the first time this
// repo is dispatched into for ticket, recording origin/<target>'s sha as
// the fork point squash and reconcile measure against later. It returns the
// recorded sha and whether the caller may proceed.
func (rc *runCtx) ensureStartSHA(lease pool.Lease) (string, bool) {
	path := filepath.Join(rc.d.Store.TicketDir(rc.ticket), fmt.Sprintf("start.%s.sha", rc.repoName))
	if data, err := os.ReadFile(path); err == nil {
		return trimSHA(data), true
	} else if !os.IsNotExist(err) {
		rc.fail(fmt.Errorf("make: read %s: %w", path, err))
		return "", false
	}

	sha, err := gitx.RevParse(lease.Dir, "origin/"+rc.target)
	if err != nil {
		rc.fail(fmt.Errorf("make: resolve origin/%s: %w", rc.target, err))
		return "", false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		rc.fail(fmt.Errorf("make: create ticket dir: %w", err))
		return "", false
	}
	if err := os.WriteFile(path, []byte(sha), 0o644); err != nil {
		rc.fail(fmt.Errorf("make: write %s: %w", path, err))
		return "", false
	}
	return sha, true
}

func trimSHA(data []byte) string {
	s := string(data)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// bringUpEnv brings sl's env class up in lease, routing an Unavailable
// failure to the env-blocked state. It reports whether dispatch may
// proceed.
func (rc *runCtx) bringUpEnv(sl store.Slice, m manifest.Manifest, lease pool.Lease) bool {
	ec, ok := m.Envs[sl.Env]
	if !ok {
		// NOTE: a slice naming an env class the manifest never declares is a
		// brief/slices.yaml authoring error, not a runtime outcome; treat it
		// as an infrastructure failure rather than guessing a policy.
		rc.fail(fmt.Errorf("make: slice %s names undeclared env class %q", sl.ID, sl.Env))
		return false
	}

	_, err := envrun.Up(ec, rc.ticket, lease.Dir)
	if err == nil {
		rc.journal(journal.Line{Slice: sl.ID, Event: "env-up"})
		return !rc.isHalted()
	}

	var unavail *envrun.Unavailable
	if !errors.As(err, &unavail) {
		rc.fail(fmt.Errorf("make: env up for %s: %w", sl.ID, err))
		return false
	}

	policyOutcome := unavail.Policy
	if policyOutcome == "" {
		policyOutcome = "pause"
	}
	rc.journal(journal.Line{Slice: sl.ID, Event: "env-unavailable", Outcome: policyOutcome})

	st, rerr := rc.d.Store.ReadSliceState(rc.ticket, sl.ID)
	if rerr != nil {
		rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, rerr))
		return false
	}
	st.State = "env-blocked"
	st.Reason = "env-up-failed"
	if !rc.writeState(sl.ID, st) {
		return false
	}
	rc.push(sl.ID, "env-blocked")
	return false
}

// tearDownEnv brings sl's already-up env class back down; this is a
// deferred cleanup so it runs whatever the dispatch's outcome was.
func (rc *runCtx) tearDownEnv(sl store.Slice, m manifest.Manifest, lease pool.Lease) {
	ec := m.Envs[sl.Env]
	h := &envrun.Handle{Class: ec, Ticket: rc.ticket, Dir: lease.Dir}
	_ = h.Down()
	rc.journal(journal.Line{Slice: sl.ID, Event: "env-down"})
}

// route applies step 9 of the algorithm: it turns one dispatch's parsed
// result into the slice's next state, journaling and pushing as it goes.
func (rc *runCtx) route(sl store.Slice, lease pool.Lease, attempt int, res outcome.Result) {
	d := rc.d
	ticket := rc.ticket

	if res.Outcome == outcome.Green {
		if reason, ok := verifyGreen(lease.Dir, res); ok {
			st, err := d.Store.ReadSliceState(ticket, sl.ID)
			if err != nil {
				rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, err))
				return
			}
			st.State = "green"
			st.Attempts = attempt
			st.Question = ""
			st.Reason = ""
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

// verifyGreen checks a claimed-green result against the lease on disk: its
// commit must exist there, and every declared artifact must exist under it.
func verifyGreen(leaseDir string, res outcome.Result) (reason string, ok bool) {
	if res.Commit == "" {
		return "commit missing", false
	}
	if _, err := gitx.Run(leaseDir, "cat-file", "-e", res.Commit+"^{commit}"); err != nil {
		return fmt.Sprintf("commit %s not found in lease", res.Commit), false
	}
	for _, a := range res.Artifacts {
		if _, err := os.Stat(filepath.Join(leaseDir, a)); err != nil {
			return fmt.Sprintf("artifact %s missing", a), false
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
		headings := briefHeadingsFor(d.Store, ticket, sl)
		body = flawedBriefQuestionBody(ticket, res.Summary, headings)
		reason = "flawed-brief"
	}

	qid := d.Store.NextQuestionID(ticket)
	if err := d.Store.WriteQuestion(ticket, store.Question{ID: qid, Slice: sl.ID, Status: "open", Body: body}); err != nil {
		rc.fail(fmt.Errorf("make: write question for %s: %w", sl.ID, err))
		return
	}

	st, err := d.Store.ReadSliceState(ticket, sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, err))
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
	d := rc.d
	ticket := rc.ticket

	st, err := d.Store.ReadSliceState(ticket, sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, err))
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
	d := rc.d
	ticket := rc.ticket

	sig := outcome.Signature("slice", res.Outcome, res.Summary)
	rc.mu.Lock()
	_, stalled := rc.stall.Observe(sig, false)
	rc.mu.Unlock()

	if stalled {
		st, err := d.Store.ReadSliceState(ticket, sl.ID)
		if err != nil {
			rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, err))
			return
		}
		st.State = "stalled"
		st.Attempts = attempt
		st.Reason = "stall"
		if !rc.writeState(sl.ID, st) {
			return
		}
		rc.journal(journal.Line{Slice: sl.ID, Event: "stall", Outcome: res.Outcome, Attempt: attempt})
		rc.push(sl.ID, "stalled")

		reason := fmt.Sprintf(
			"stall: slice %s returned %s twice with no progress: %s. A repeat is patching — likely a misconception; amend the brief (`jig requeue --from-brief-diff`) or answer with direction (`jig run --answer`)",
			sl.ID, res.Outcome, res.Summary,
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
		st, err := d.Store.ReadSliceState(ticket, sl.ID)
		if err != nil {
			rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, err))
			return
		}
		st.State = "stalled"
		st.Attempts = attempt
		st.Reason = "attempt-cap"
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
	st, err := d.Store.ReadSliceState(ticket, sl.ID)
	if err != nil {
		rc.fail(fmt.Errorf("make: read slice state %s: %w", sl.ID, err))
		return
	}
	st.State = "queued"
	st.Attempts = attempt
	if !rc.writeState(sl.ID, st) {
		return
	}
	rc.push(sl.ID, "queued")
}
