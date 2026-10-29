package verify

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/spawn"
	"github.com/develdeco/jig/internal/store"
)

// Deps is what a verification works with.
type Deps struct {
	// Store is the ticket's store. It is the checkpoint-wired store of the
	// command that runs the verification, so the pushes the run makes sync
	// the mirror as any other checkpoint does.
	Store *store.Store
	// JigHome is the jig home root: the worktree, the log and the recordings
	// live under it, and the ledger is its.
	JigHome string
	// Clone is the operator's clone of the repo the commit is in: the
	// worktree is added from it, so the commit is guaranteed present and no
	// fetch is made.
	Clone string
	// Repo is the repo's name in the project and Target its target branch.
	// They find the sha the commit must descend from: the ticket's
	// start.<Repo>.sha, else the merge base with origin/<Target>. Either may
	// be empty, and a commit with no known fork point is not held to one.
	Repo   string
	Target string
	// Oracle runs an oracle command in dir with env (each "NAME=value") added
	// to its environment, bounded by limit, and returns its combined output.
	// nil means the real run: envrun.ShellOutputEnv with the Windows
	// short-path workaround, as the frontier runs it.
	Oracle func(cmd, dir string, env []string, limit time.Duration) (string, error)
	// Now is the clock the run's lines and its timing read; nil is time.Now.
	Now func() time.Time
	// Log, when set, is where the run writes what it did and the tail of a
	// failed oracle's output; a detached worker's is its own stdout, which is
	// the machine-local log. nil discards.
	Log io.Writer
	// Sleep waits between the tries of a push; nil is time.Sleep.
	Sleep func(time.Duration)

	// Executable is the jig binary Spawn starts as the worker, and Start the
	// function that starts a detached process (spawn.Detached in production).
	// Only Spawn reads them.
	Executable string
	Start      func(spawn.Spec) (int, error)
}

// Request names one verification: the slice's oracle, run on the commit.
type Request struct {
	Ticket string
	Slice  string
	// SHA is the commit; any revision the clone resolves to one, which the
	// run turns into the full sha before it records anything.
	SHA string
	// Session and Agent are who asked, written on the lines the run records.
	Session string
	Agent   string
	// Rerun verifies again a commit the journal already has a verification
	// of; without it the existing one stands and nothing runs.
	Rerun bool
}

// Result is what a verification found, or what was already there.
type Result struct {
	Ticket string
	Slice  string
	// SHA is the full sha of the commit.
	SHA string
	// Attempt is the slice attempt the verification took (the existing one's
	// when Existing).
	Attempt int
	// State is one of journal's Verify constants: pass, fail or error for a
	// verification that finished, pending for one still running, and for an
	// Existing result the state the journal already holds.
	State string
	// Existing is true when the journal already held a verification of the
	// commit for the slice (or a worker is on it) and Rerun was not set:
	// nothing ran and nothing was recorded.
	Existing bool
	// Command and Env are the oracle command that ran and the env class that
	// was up for it.
	Command string
	Env     string
	// Seconds is the oracle's wall time, rounded up.
	Seconds int
	// Summary is a failure's flattened tail (at most 600 bytes) or the reason
	// a run could not reach its oracle, and Signature the failure's stall
	// signature; Tail is the last 4,000 bytes of a failed oracle's output.
	Summary   string
	Signature string
	Tail      string
	// PushErr is why the last push failed, "" when it did not: the lines are
	// recorded and stay local until the next checkpoint pushes them.
	PushErr string
}

// pushTries is how many times the run tries a push before it leaves the
// commit local for the next checkpoint, and pushWait the pause after the
// first failure (the second waits twice as long).
const (
	pushTries = 3
	pushWait  = 500 * time.Millisecond
)

// run is one verification in flight.
type run struct {
	d   Deps
	r   Request
	sha string
	sl  store.Slice

	storeID string
	dir     string // the worktree
	attempt int
	res     Result
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) sleep(t time.Duration) {
	if d.Sleep != nil {
		d.Sleep(t)
		return
	}
	time.Sleep(t)
}

// oracle runs cmd in dir, or for real when Deps.Oracle is nil.
func (d Deps) oracle(cmd, dir string, env []string, limit time.Duration) (string, error) {
	if d.Oracle != nil {
		return d.Oracle(cmd, dir, env, limit)
	}
	return envrun.ShellOutputEnv(envrun.ShortenQuotedPath(cmd), dir, limit, env)
}

// prepare checks the request against the store and the clone and resolves
// what the run needs before it touches the journal: the commit's full sha,
// the slice, and the paths the verification lives at. It records nothing.
func (d Deps) prepare(r Request) (*run, error) {
	x := &run{d: d, r: r, res: Result{Ticket: r.Ticket, Slice: r.Slice, SHA: r.SHA}}
	switch {
	case d.Store == nil:
		return x, errors.New("verify: no store")
	case d.JigHome == "":
		return x, errors.New("verify: no jig home")
	case d.Clone == "":
		return x, errors.New("verify: no clone to verify the commit from")
	case r.Ticket == "" || r.Slice == "" || r.SHA == "":
		return x, errors.New("verify: a request names a ticket, a slice and a commit")
	case strings.HasPrefix(r.SHA, "-"):
		return x, fmt.Errorf("verify: %q is not a commit", r.SHA)
	}

	sha, err := gitx.RevParse(d.Clone, r.SHA+"^{commit}")
	if err != nil {
		return x, fmt.Errorf("verify: commit %s is not in %s: %w", r.SHA, d.Clone, err)
	}
	x.sha, x.res.SHA = sha, sha

	slices, err := d.Store.ReadSlices(r.Ticket)
	if err != nil {
		return x, fmt.Errorf("verify: read the slices of %s: %w", r.Ticket, err)
	}
	found := false
	for _, s := range slices {
		if s.ID == r.Slice {
			x.sl, found = s, true
			break
		}
	}
	if !found {
		return x, fmt.Errorf("verify: ticket %s has no slice %q", r.Ticket, r.Slice)
	}

	if x.storeID, err = d.Store.ID(); err != nil {
		return x, fmt.Errorf("verify: %w", err)
	}
	if x.dir, err = home.VerifyWorktreeDir(d.JigHome, x.storeID, r.Ticket, sha, r.Slice); err != nil {
		return x, err
	}
	return x, nil
}

// existing fills res from the journal's record of the verification when there
// is one, and reports whether the request stops there: it does unless Rerun.
func (x *run) existing(lines []journal.Line) bool {
	cur := journal.VerificationState(lines, x.sha, x.r.Slice)
	if cur.State == journal.VerifyNone || x.r.Rerun {
		return false
	}
	x.res.Existing = true
	x.res.State = cur.State
	x.res.Attempt = cur.Attempt
	x.res.Seconds = cur.Seconds
	x.res.Summary = cur.Summary
	return true
}

// subject is the first line of the commit's message, as the clone has it: the
// pending line carries it so a reader of the journal can say which change is
// being verified without a repository at hand. A commit git cannot show
// leaves it empty.
func (x *run) subject() string {
	out, err := gitx.Subject(x.d.Clone, x.sha)
	if err != nil {
		return ""
	}
	return oneLine(strings.TrimSpace(out))
}

// finishPass completes a pass that a run stopped recording halfway: the slice
// was marked green, and the verified line that comes next was never written.
// The commit's latest record is then a pass with no verified line for it, and
// the slice is green. The line is written now, with the attempt, session and
// agent of that pass, and pushed, so every reader of verified lines
// (journal.BuiltCommits, the pull request body, the notes a session is told)
// counts the commit. Anything else is left as it is.
func (x *run) finishPass(lines []journal.Line) error {
	if x.res.State != journal.VerifyPass {
		return nil
	}
	for _, l := range lines {
		if l.Event == journal.EventVerified && l.Commit == x.sha && l.Slice == x.r.Slice {
			return nil
		}
	}
	st, err := x.d.Store.ReadSliceState(x.r.Ticket, x.r.Slice)
	if err != nil {
		return fmt.Errorf("verify: read the state of slice %s: %w", x.r.Slice, err)
	}
	if st.State != "green" {
		return nil
	}
	cur := journal.VerificationState(lines, x.sha, x.r.Slice)
	l := journal.Line{
		TS:      x.d.now().UTC().Format(time.RFC3339Nano),
		Event:   journal.EventVerified,
		Slice:   x.r.Slice,
		Commit:  x.sha,
		Attempt: cur.Attempt,
		Session: cur.Session,
		Agent:   cur.Agent,
	}
	if err := journal.Append(x.d.Store, x.r.Ticket, l); err != nil {
		return fmt.Errorf("verify: journal %s: %w", l.Event, err)
	}
	x.logf("finished the pass of %s for slice %s: its verified line was missing", short(x.sha), x.r.Slice)
	x.push("verified")
	return nil
}

// Run verifies the request in this process.
//
// It takes the verification's lock (a lock file beside its worktree, held to
// the end), so two workers for one (commit, slice) never run together: the
// second finds the first's lock and reports the verification as pending.
// Under the lock it reads the journal; a verification already there stands
// unless Rerun is set (Result.Existing), and a pass a run stopped recording
// halfway is finished (finishPass). Otherwise the order is the contract:
//
//  1. a verify-pending line, pushed, so the work is visible before it is
//     done;
//  2. a detached worktree of the commit, the start-sha descent check, the
//     env class up if the slice names one;
//  3. the slice's oracle, as the manifest at that commit resolves it, run
//     with a recording directory in JIG_RECORD_DIR;
//  4. the oracle line (Source jig, Commit the sha, Command, Env, Seconds,
//     and on a failure Summary and Signature), then on a pass the recorded
//     line if the scenarios recorded, the slice's state green, and the
//     verified line, so a reader the verified line wakes finds the slice
//     green; on a failure the slice's state building;
//  5. a push, tried three times; if it fails the lines stay local for the
//     next checkpoint (Result.PushErr).
//
// A run that cannot reach the oracle (the descent check fails, the worktree
// cannot be made, the slice names an env class that is undeclared or will not
// come up) records a verify-error line with the reason and returns it as
// State error: that is an outcome, not an error. Run returns an error only
// when the run could not be recorded or was not set up.
func Run(d Deps, r Request) (Result, error) {
	x, err := d.prepare(r)
	if err != nil {
		return x.res, err
	}
	release, held, err := store.TryLock(x.dir, 0)
	if err != nil {
		return x.res, fmt.Errorf("verify: lock %s: %w", x.dir, err)
	}
	if !held {
		x.res.Existing, x.res.State = true, journal.VerifyPending
		return x.res, nil
	}
	defer release()

	lines, err := journal.Read(d.Store, r.Ticket)
	if err != nil {
		return x.res, fmt.Errorf("verify: read the journal of %s: %w", r.Ticket, err)
	}
	if x.existing(lines) {
		return x.res, x.finishPass(lines)
	}
	x.attempt = journal.NextAttempt(lines, r.Slice)
	x.res.Attempt = x.attempt

	if err := x.journal(journal.Line{Event: journal.EventVerifyPending, Summary: x.subject()}); err != nil {
		return x.res, err
	}
	x.res.State = journal.VerifyPending
	x.logf("verifying %s for slice %s of %s (attempt %d)", short(x.sha), r.Slice, r.Ticket, x.attempt)
	x.push("verifying")

	why, err := x.execute()
	if err != nil {
		return x.res, err
	}
	if why != "" {
		x.logf("could not verify: %s", why)
		x.res.State, x.res.Summary = journal.VerifyError, oneLine(why)
		if err := x.journal(journal.Line{Event: journal.EventVerifyError, Outcome: "error", Summary: x.res.Summary}); err != nil {
			return x.res, err
		}
		x.push("could not verify")
	}
	return x.res, nil
}

// failureTail is the tail of a failed oracle run kept for the log and the
// summary: the end of its output, with the reason added when the run was
// killed for taking too long (the output says nothing of that) or printed
// nothing at all.
func failureTail(out string, err error) string {
	tail := outputTail(out)
	switch {
	case tail == "":
		return err.Error()
	case strings.Contains(err.Error(), "did not finish within"):
		return outputTail(tail + "\n" + err.Error())
	}
	return tail
}

// setState reads the slice's state under the store's checkpoint lock, lets
// change edit it (and say whether to write it), and writes it back, so a
// claim or an answer made meanwhile by another process is not overwritten by
// a stale copy.
func (x *run) setState(change func(*store.SliceState) bool) error {
	release, err := x.d.Store.LockCheckpoint()
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	defer release()
	st, err := x.d.Store.ReadSliceState(x.r.Ticket, x.r.Slice)
	if err != nil {
		return fmt.Errorf("verify: read the state of slice %s: %w", x.r.Slice, err)
	}
	if !change(&st) {
		return nil
	}
	if err := x.d.Store.WriteSliceState(x.r.Ticket, x.r.Slice, st); err != nil {
		return fmt.Errorf("verify: write the state of slice %s: %w", x.r.Slice, err)
	}
	return nil
}

// journal appends l to the ticket's journal as this verification's line: its
// slice, the clock's time and, except on the env lines (which keep the shape
// the frontier gave them), its attempt and commit. The lines that say who
// asked carry the session and agent of the request.
func (x *run) journal(l journal.Line) error {
	l.Slice = x.r.Slice
	l.TS = x.d.now().UTC().Format(time.RFC3339Nano)
	switch l.Event {
	case journal.EventEnvUp, journal.EventEnvDown, journal.EventEnvUnavailable:
	default:
		l.Attempt = x.attempt
		if l.Commit == "" {
			l.Commit = x.sha
		}
		if l.Event != journal.EventRecorded {
			l.Session, l.Agent = x.r.Session, x.r.Agent
		}
	}
	if err := journal.Append(x.d.Store, x.r.Ticket, l); err != nil {
		return fmt.Errorf("verify: journal %s: %w", l.Event, err)
	}
	return nil
}

// push checkpoints the store: pushTries tries, a pause between. A push that
// never goes through is not a failure of the verification: the lines are
// written and the next checkpoint carries them.
func (x *run) push(what string) {
	msg := fmt.Sprintf("%s: verify %s %s: %s", x.r.Ticket, x.r.Slice, short(x.sha), what)
	var err error
	for i := 0; i < pushTries; i++ {
		if err = x.d.Store.Push(msg); err == nil {
			x.res.PushErr = ""
			return
		}
		if i < pushTries-1 {
			x.d.sleep(time.Duration(i+1) * pushWait)
		}
	}
	x.res.PushErr = err.Error()
	x.logf("push failed after %d tries; the lines stay local for the next checkpoint: %v", pushTries, err)
}

// logf writes one line to the log, if there is one.
func (x *run) logf(format string, args ...any) {
	if x.d.Log == nil {
		return
	}
	fmt.Fprintf(x.d.Log, "verify: "+format+"\n", args...)
}

// short is the first twelve characters of a sha, as the verification's
// directory and log name it.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// execute does steps 2 to 5 of Run's order. It returns why the oracle could
// not be reached ("" when it ran), or an error when a line could not be
// written.
func (x *run) execute() (why string, err error) {
	// Removed on every way out, a checkout that failed half-way included.
	defer x.removeWorktree()
	if err := x.addWorktree(); err != nil {
		return err.Error(), nil
	}

	if why := x.checkCheckout(); why != "" {
		return why, nil
	}
	m, err := manifest.Resolve(x.dir)
	if err != nil {
		return fmt.Sprintf("the manifest at %s does not resolve: %v", short(x.sha), err), nil
	}
	ws, _ := m.WorkspaceIn(x.d.Repo, x.sl.Workspace)
	cmd := m.OracleCmd(x.sl.Oracle, ws)
	if strings.TrimSpace(cmd) == "" {
		return fmt.Sprintf("slice %s has no oracle to run", x.r.Slice), nil
	}
	x.res.Command, x.res.Env = cmd, x.sl.Env

	down, why, err := x.bringUpEnv(m)
	if why != "" || err != nil {
		return why, err
	}
	if down != nil {
		defer func() {
			if derr := down(); derr != nil && err == nil {
				err = derr
			}
		}()
	}
	return "", x.runOracle(m, cmd)
}

// checkCheckout holds the fresh checkout to what an oracle run needs of it to
// be evidence: a clean tree, so the run is of exactly the commit, and a
// descent from the ticket's start sha, so the commit is new work on the
// ticket's branch (the frontier's verifyGreen). It returns why the commit
// cannot be verified, or "".
func (x *run) checkCheckout() string {
	d := x.d
	if dirty := dirtyTree(x.dir); dirty != "" {
		return "the checkout of " + short(x.sha) + " is not clean, so an oracle run on it would say nothing of the commit: " + oneLine(dirty)
	}
	var recorded string
	if d.Repo != "" {
		if data, err := os.ReadFile(d.Store.StartSHAPath(x.r.Ticket, d.Repo)); err == nil {
			recorded = string(data)
		}
	}
	start := startSHA(d.Clone, x.sha, recorded, d.Target)
	if start == "" {
		return ""
	}
	if !gitx.HasCommit(d.Clone, start) {
		return fmt.Sprintf("the start sha %s is not in this clone (fetch it, then run again)", short(start))
	}
	if reason, ok := verifyGreen(x.dir, start, outcome.Result{Outcome: outcome.Green, Commit: x.sha}); !ok {
		return "the commit does not verify: " + reason
	}
	return ""
}

// bringUpEnv brings the slice's env class up in the worktree when it names
// one, as the frontier does: an env-up line, and down is what brings it back
// down (with its env-down line). An env class the manifest does not declare,
// or one that will not come up, is a reason the oracle cannot be reached; the
// latter also journals env-unavailable with the class's policy and parks the
// slice as env-blocked, the frontier's own handling.
func (x *run) bringUpEnv(m manifest.Manifest) (down func() error, why string, err error) {
	if x.sl.Env == "" {
		return nil, "", nil
	}
	ec, ok := m.Envs[x.sl.Env]
	if !ok {
		return nil, fmt.Sprintf("slice %s names the env class %q, which the manifest at %s does not declare", x.r.Slice, x.sl.Env, short(x.sha)), nil
	}
	h, uerr := envrun.Up(ec, x.r.Ticket, x.dir)
	if uerr != nil {
		var unavail *envrun.Unavailable
		if !errors.As(uerr, &unavail) {
			return nil, fmt.Sprintf("bring up the env class %q: %v", x.sl.Env, uerr), nil
		}
		policy := unavail.Policy
		if policy == "" {
			policy = "pause"
		}
		if err := x.journal(journal.Line{Event: journal.EventEnvUnavailable, Outcome: policy}); err != nil {
			return nil, "", err
		}
		if err := x.setState(func(st *store.SliceState) bool {
			st.State, st.Reason = "env-blocked", "env-up-failed"
			st.Attempts = max(st.Attempts, x.attempt)
			return true
		}); err != nil {
			return nil, "", err
		}
		x.push("env-blocked")
		return nil, fmt.Sprintf("the env class %q is unavailable: %v", x.sl.Env, unavail.Err), nil
	}
	if err := x.journal(journal.Line{Event: journal.EventEnvUp}); err != nil {
		_ = h.Down()
		return nil, "", err
	}
	return func() error {
		_ = h.Down()
		return x.journal(journal.Line{Event: journal.EventEnvDown})
	}, "", nil
}

// runOracle runs the oracle in the worktree and records what came of it: the
// oracle line, the recorded line of a pass that recorded, then the green
// state and the verified line, in that order (see the pass below); a failure
// leaves the slice building.
func (x *run) runOracle(m manifest.Manifest, cmd string) error {
	d := x.d
	rec := recorder{home: d.JigHome, storeID: x.storeID, ticket: x.r.Ticket, journal: x.journal}
	target := rec.start(x.r.Slice, x.attempt, x.sha)

	limit := m.OracleLimit()
	started := d.now()
	out, oerr := d.oracle(cmd, x.dir, target.env(), limit)
	// Rounded up, so every run records at least a second and 0 keeps meaning
	// "never ran".
	seconds := int((d.now().Sub(started) + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	x.res.Seconds = seconds

	line := journal.Line{Event: journal.EventOracle, Commit: x.sha, Command: cmd, Env: x.sl.Env, Seconds: seconds, Source: "jig"}
	if oerr == nil {
		line.Outcome = "pass"
		x.res.State = journal.VerifyPass
	} else {
		x.res.Tail = failureTail(out, oerr)
		x.res.Summary = oneLine(x.res.Tail)
		x.res.Signature = outcome.Signature(x.r.Slice, outcome.CodeBug, x.res.Summary)
		x.res.State = journal.VerifyFail
		line.Outcome, line.Summary, line.Signature = "fail", x.res.Summary, x.res.Signature
		x.logf("the oracle failed after %ds (limit %s); the last %d bytes of its output:\n%s", seconds, limit, tailBytes, x.res.Tail)
	}
	if err := x.journal(line); err != nil {
		return err
	}
	if err := rec.finish(x.r.Slice, x.attempt, x.sha, target, oerr == nil); err != nil {
		return err
	}

	// A newer commit of the slice may already have an outcome: workers finish
	// in any order. This one is history then, and the slice's state stays
	// what the newer commit made it.
	newer := x.superseded()
	if newer {
		x.logf("a newer commit of slice %s already has an outcome; its state stays as that one left it", x.r.Slice)
	}

	if oerr != nil {
		err := x.setState(func(st *store.SliceState) bool {
			st.Attempts = max(st.Attempts, x.attempt)
			if newer {
				return true
			}
			// An open question outranks a failed run: the person's answer is
			// what a slice that needs input waits for.
			if st.State != "needs-input" {
				st.State, st.Reason, st.Question, st.Signature, st.StallSummary = "building", "", "", "", ""
			}
			return true
		})
		if err != nil {
			return err
		}
		x.push("oracle failed")
		return nil
	}

	// The commit is the ticket's now: the oracle line records the run, the
	// verified line that it verified, and only verified lines are the commits
	// jig built (journal.BuiltCommits). The slice is marked green first: the
	// verified line is what wakes a reader of the journal (jig events, the
	// TUI, the hooks), and a reader it wakes must find the slice green, not
	// the state from before. A run that stops in between leaves the oracle
	// line, which already stands as the commit's pass (VerificationState).
	err := x.setState(func(st *store.SliceState) bool {
		st.Attempts = max(st.Attempts, x.attempt)
		if newer {
			return true
		}
		st.State = "green"
		st.Question, st.Reason, st.Signature, st.StallSummary = "", "", "", ""
		return true
	})
	if err != nil {
		return err
	}
	if err := x.journal(journal.Line{Event: journal.EventVerified, Commit: x.sha}); err != nil {
		return err
	}
	x.push("oracle passed")
	return nil
}

// superseded reports whether a newer commit of this slice already has an
// outcome in the journal (see Superseded). A journal that cannot be read
// leaves the state to this run, as before.
func (x *run) superseded() bool {
	lines, err := journal.Read(x.d.Store, x.r.Ticket)
	if err != nil {
		return false
	}
	return Superseded(lines, x.r.Slice, x.sha, GitAncestry(x.d.Clone))
}
