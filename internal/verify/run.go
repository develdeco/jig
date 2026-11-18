package verify

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/scopebase"
	"github.com/develdeco/jig/internal/store"
)

// Deps is what a verification works with.
type Deps struct {
	// Store is the ticket's store. It is the checkpoint-wired store of the
	// command that runs the verification, so the pushes the run makes sync
	// the mirror as any other checkpoint does.
	Store *store.Store
	// JigHome is the jig home root: the recordings live under it, and the
	// ledger is its.
	JigHome string
	// Clone is the worktree the run is of: the directory whose HEAD is the
	// commit that is verified, and where the oracle runs. It is the builder's
	// own checkout, in the builder's own shell; jig makes no checkout of its
	// own.
	Clone string
	// Repos are the project's repos with the workspaces each declares, in a
	// project of several: they say which slices of a ticket build in Repo
	// (ready.Place), which a final run is the union of. nil, or one repo,
	// means every slice builds here.
	Repos []ready.Repo
	// Repo is the repo's name in the project and Target its target branch.
	// They find the sha the commit must descend from: the ticket's
	// start.<Repo>.sha, else the merge base with origin/<Target>. Either may
	// be empty, and a commit with no known fork point is not held to one.
	Repo   string
	Target string
	// Oracle runs an oracle command in dir with env (each "NAME=value") added
	// to its environment, bounded by limit (0 for none), and returns its
	// combined output. nil means the real run: envrun.ShellStream, through sh.
	Oracle func(cmd, dir string, env []string, limit time.Duration) (string, error)
	// OracleUsage is Oracle that also returns what the run's process tree
	// used; it is used when Oracle is nil, and nil means the real run. An
	// injected Oracle measures nothing.
	OracleUsage func(cmd, dir string, env []string, limit time.Duration) (string, envrun.Usage, error)
	// Sh finds the sh the oracle and the env class's commands run through;
	// nil is envrun.Sh.
	Sh func() (string, error)
	// Now is the clock the run's lines and its timing read; nil is time.Now.
	Now func() time.Time
	// Out, when set, is where the command's combined output goes as it is
	// produced, unfiltered. nil discards it; the run keeps only a bounded tail
	// of it either way, for a failure's summary and signature.
	Out io.Writer
	// Log, when set, is where the run writes what jig did besides the command
	// (a push that did not go through). nil discards.
	Log io.Writer
	// Sleep waits between the tries of a push; nil is time.Sleep.
	Sleep func(time.Duration)
}

// Request names one verification: the slice's oracle, run in the worktree
// Deps.Clone and recorded against its HEAD.
type Request struct {
	Ticket string
	Slice  string
	// Session and Agent are who asked, written on the lines the run records.
	Session string
	Agent   string
	// Command, when set, is run in the place of the slice's oracle. The run is
	// recorded as another command and covers nothing.
	Command string
	// Flags is the command line's flags and `--` command, as given, recorded
	// on the oracle line so the run can be made again by hand.
	Flags string
	// Timeout bounds the command when it is above 0; the run is unbounded
	// otherwise.
	Timeout time.Duration
	// Dir is where the command runs; the worktree's root when empty.
	Dir string
	// NoRecord runs the command and writes nothing: no line, no state, no
	// recording directory, no push. An exploratory run.
	NoRecord bool
}

// Result is what a verification found.
type Result struct {
	Ticket string
	Slice  string
	// SHA is the full sha of HEAD when the run started.
	SHA string
	// Attempt is the slice attempt the run took.
	Attempt int
	// State is one of journal's Verify constants: pass, fail or error for a
	// run that could not reach its command.
	State string
	// Code is the command's own exit code: 0 for a pass, the code the command
	// ended with, 124 for a command ended at the --timeout. 0 when the command
	// never ran.
	Code int
	// Ran is whether the command started.
	Ran bool
	// Covered is whether the run covers SHA: it changed the slice's state and
	// wrote the pass. Uncovered says why not, in the journal's words.
	Covered   bool
	Uncovered string
	// HeadEnd is HEAD when the run ended; TreeBefore and TreeAfter whether the
	// working tree was clean (journal.TreeClean) then.
	HeadEnd    string
	TreeBefore string
	TreeAfter  string
	// Dir is where the command ran.
	Dir string
	// Command and Env are the command that ran and the env class that was up
	// for it.
	Command string
	Env     string
	// Seconds is the command's wall time, rounded up.
	Seconds int
	// Summary is a failure's flattened tail (at most 600 bytes) or the reason
	// a run could not reach its command, and Signature the failure's stall
	// signature; Tail is the last 4,000 bytes of a failed command's output.
	Summary   string
	Signature string
	Tail      string
	// Pushed is whether the run's lines went to the store's remote; PushErr is
	// why not, "" when they did: the lines are recorded and stay local until
	// the next checkpoint pushes them. Recorded names the lines the run wrote.
	PushErr  string
	Pushed   bool
	Recorded []string
}

// pushTries is how many times the run tries a push before it leaves the
// commit local for the next checkpoint, and pushWait the pause after the
// first failure (the second waits twice as long).
const (
	pushTries = 3
	pushWait  = 500 * time.Millisecond
)

// ExitUnrun is the exit code `jig verify` ends with when jig's own work
// failed before the command started: the code git bisect run uses for "could
// not test".
const ExitUnrun = 125

// exitTimedOut is the exit code of a command ended at its --timeout, the one
// timeout(1) uses.
const exitTimedOut = 124

// run is one verification in flight.
type run struct {
	d   Deps
	r   Request
	sha string
	sl  store.Slice

	storeID string
	dir     string // the worktree
	cmdDir  string // where the command runs
	subject string // HEAD's subject
	pre     string // why the run cannot cover, known before it starts

	descent     string // why HEAD does not descend from the start sha, once asked
	descentDone bool
	attempt     int
	res         Result
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

// oracle runs cmd in dir, or for real when Deps.Oracle is nil, streaming its
// output to w, and returns what its process tree used (zero when nothing
// measured it).
func (d Deps) oracle(cmd, dir string, env []string, limit time.Duration, w io.Writer) (envrun.Usage, error) {
	switch {
	case d.Oracle != nil:
		out, err := d.Oracle(cmd, dir, env, limit)
		_, _ = io.WriteString(w, out)
		return envrun.Usage{}, err
	case d.OracleUsage != nil:
		out, use, err := d.OracleUsage(cmd, dir, env, limit)
		_, _ = io.WriteString(w, out)
		return use, err
	}
	return envrun.ShellStream(cmd, dir, limit, env, w)
}

// sh finds the sh commands run through, with envrun.Sh when Deps.Sh is nil.
func (d Deps) sh() (string, error) {
	if d.Sh != nil {
		return d.Sh()
	}
	return envrun.Sh()
}

// tailWriter keeps the last tailBytes bytes written to it.
type tailWriter struct{ buf []byte }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*tailBytes {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailBytes:]...)
	}
	return len(p), nil
}

func (t *tailWriter) String() string { return string(t.buf) }

// prepare checks the request against the store and the worktree and resolves
// what the run needs before it touches the journal: HEAD's full sha and the
// slice. It records nothing.
func (d Deps) prepare(r Request) (*run, error) {
	x := &run{d: d, r: r, res: Result{Ticket: r.Ticket, Slice: r.Slice}}
	switch {
	case d.Store == nil:
		return x, errors.New("verify: no store")
	case d.JigHome == "":
		return x, errors.New("verify: no jig home")
	case d.Clone == "":
		return x, errors.New("verify: no worktree to verify")
	case r.Ticket == "" || r.Slice == "":
		return x, errors.New("verify: a request names a ticket and a slice")
	}

	if err := x.prepareHead(); err != nil {
		return x, err
	}

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
	return x, nil
}

// prepareHead resolves the commit a run is of and where its command runs: HEAD
// of the worktree, its subject, and the directory the request names (the
// worktree's root when none).
func (x *run) prepareHead() error {
	d, r := x.d, x.r
	sha, err := gitx.RevParse(d.Clone, "HEAD")
	if err != nil {
		return fmt.Errorf("verify: HEAD of %s: %w", d.Clone, err)
	}
	x.sha, x.res.SHA, x.dir = sha, sha, d.Clone
	x.cmdDir = d.Clone
	if r.Dir != "" {
		x.cmdDir = r.Dir
		if fi, err := os.Stat(r.Dir); err != nil || !fi.IsDir() {
			return fmt.Errorf("verify: %s is not a directory to run the command in", r.Dir)
		}
	}
	x.res.Dir = x.cmdDir
	if subject, err := gitx.Subject(d.Clone, sha); err == nil {
		x.subject = oneLine(strings.TrimSpace(subject))
	}
	return nil
}

// Run verifies HEAD of the worktree Deps.Clone in this process: it runs the
// slice's oracle (or Request.Command) there, in the caller's environment, in
// the foreground, streams its output, and records the run in one oracle line
// that holds every input and fact needed to run it again by hand.
//
// A run covers HEAD when the tree was clean before and after, HEAD did not
// move, the command was the slice's own oracle, it ran in the worktree (not
// another work tree) and HEAD descends from the ticket's start sha. A covering pass marks the slice green and writes the
// verified line after the oracle line (and the recorded line before them
// when the scenarios recorded), a covering failure leaves the slice
// building; any other run is recorded with the reason it covers nothing
// (journal.Line.Uncovered) and changes no slice state. The run writes no
// verify-pending line, takes no lock and refuses nothing: a second run of
// the same commit runs again. The lines are then pushed, tried three times;
// if it fails the lines stay local for the next checkpoint (Result.PushErr).
//
// A run that cannot reach its command (there is no sh to run it with, the
// manifest does not resolve, the slice names an env class that is undeclared
// or will not come up) records a verify-error line with the reason and
// returns it as State error: that is an outcome, not an error. Run returns
// an error only when the run could not be recorded or was not set up.
func Run(d Deps, r Request) (Result, error) {
	x, err := d.prepare(r)
	if err != nil {
		return x.res, err
	}
	lines, err := journal.Read(d.Store, r.Ticket)
	if err != nil {
		return x.res, fmt.Errorf("verify: read the journal of %s: %w", r.Ticket, err)
	}
	x.attempt = journal.NextAttempt(lines, r.Slice)
	x.res.Attempt = x.attempt

	why, err := x.execute()
	if err != nil {
		return x.res, err
	}
	if why != "" {
		x.res.State, x.res.Summary, x.res.Code = journal.VerifyError, oneLine(why), ExitUnrun
		// A run that could not have covered anything leaves no line a reader
		// of verification state would take for the commit's.
		if x.pre == "" {
			if err := x.journal(journal.Line{Event: journal.EventVerifyError, Outcome: "error", Summary: x.res.Summary}); err != nil {
				return x.res, err
			}
			x.recordedLine(journal.EventVerifyError)
			x.push("could not verify")
		}
	}
	return x.res, nil
}

// Plan says what Run would do for r, and runs and writes nothing: the commit,
// the command and the directory it would run in, the env class, the attempt
// it would take, the tree as it is now, and the reason it would cover nothing
// when that is already so (Result.Uncovered; one that only the run can bring
// about, a tree left dirty or a HEAD moved, is not foretold).
func Plan(d Deps, r Request) (Result, error) {
	x, err := d.prepare(r)
	if err != nil {
		return x.res, err
	}
	lines, err := journal.Read(d.Store, r.Ticket)
	if err != nil {
		return x.res, fmt.Errorf("verify: read the journal of %s: %w", r.Ticket, err)
	}
	x.attempt = journal.NextAttempt(lines, r.Slice)
	x.res.Attempt = x.attempt
	m, err := manifest.Resolve(x.dir)
	if err != nil {
		return x.res, fmt.Errorf("verify: the manifest at %s does not resolve: %w", short(x.sha), err)
	}
	ws, _ := m.WorkspaceIn(x.d.Repo, x.sl.Workspace)
	oracleCmd := m.OracleCmd(x.sl.Oracle, ws)
	if strings.TrimSpace(oracleCmd) == "" {
		return x.res, fmt.Errorf("verify: slice %s has no oracle to run", r.Slice)
	}
	x.res.Command, x.res.Env = oracleCmd, x.sl.Env
	if r.Command != "" {
		x.res.Command = r.Command
	}
	x.res.TreeBefore = treeState(x.dir)
	x.res.HeadEnd = x.sha
	x.res.TreeAfter = x.res.TreeBefore
	x.res.Uncovered = x.uncoveredBecause(r.Command == "", x.res.TreeBefore, x.sha)
	x.res.Covered = x.res.Uncovered == ""
	return x.res, nil
}

// failureTail is the tail of a failed oracle run kept for the summary: the end
// of its output, with the reason added when the run was killed for taking too
// long (the output says nothing of that) or printed nothing at all.
func failureTail(out string, err error) string {
	tail := outputTail(out)
	switch {
	case tail == "":
		return err.Error()
	case errors.Is(err, envrun.ErrTimedOut):
		return outputTail(tail + "\n" + err.Error())
	}
	return tail
}

// exitCodeOf is the code a command's error stands for: 0 for none, the exit
// status of the process for a non-zero exit, 124 for a command ended at its
// timeout and 1 for anything else that ended it.
func exitCodeOf(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, envrun.ErrTimedOut):
		return exitTimedOut
	case errors.As(err, &ee) && ee.ExitCode() > 0:
		return ee.ExitCode()
	}
	return 1
}

// setState reads the slice's state under the store's checkpoint lock, lets
// change edit it (and say whether to write it), and writes it back, so a
// claim or an answer made meanwhile by another process is not overwritten by
// a stale copy.
func (x *run) setState(change func(*store.SliceState) bool) error {
	if x.r.NoRecord {
		return nil
	}
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
	if x.r.NoRecord {
		return nil
	}
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
	if x.r.NoRecord {
		return
	}
	x.res.Pushed = true
	slice := x.r.Slice
	if slice == "" {
		slice = "final"
	}
	msg := fmt.Sprintf("%s: verify %s %s: %s", x.r.Ticket, slice, short(x.sha), what)
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
	x.res.Pushed = false
	x.logf("push failed after %d tries; the lines stay local for the next checkpoint: %v", pushTries, err)
}

// logf writes one line to the log, if there is one.
func (x *run) logf(format string, args ...any) {
	if x.d.Log == nil {
		return
	}
	fmt.Fprintf(x.d.Log, "verify: "+format+"\n", args...)
}

// short is the first twelve characters of a sha.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// treeState is whether dir's working tree is clean: nothing modified, staged
// or untracked, ignored files excluded.
// A tree git cannot read is dirty: the rule is evidence only when git could
// say the tree is clean.
func treeState(dir string) string {
	status, err := gitx.Run(dir, "status", "--porcelain")
	if err != nil || strings.TrimSpace(status) != "" {
		return journal.TreeDirty
	}
	return journal.TreeClean
}

// sameWorktree reports whether dir is in the work tree whose root is root: git
// says dir's top level is that checkout, so a checkout nested in the worktree
// (an ignored directory holding another worktree), its parent and an unrelated
// directory are not.
func sameWorktree(root, dir string) bool {
	rootTop, err := gitx.TopLevel(root)
	if err != nil {
		return false
	}
	top, err := gitx.TopLevel(dir)
	return err == nil && gitx.SameDir(rootTop, top)
}

// execute does the run: the env class up, the command, the record. It returns
// why the command could not be reached ("" when it ran), or an error when a
// line could not be written.
func (x *run) execute() (why string, err error) {
	// The command and the env class's commands run through sh: without one the
	// command cannot be reached, which is no failure of the commit.
	if _, err := x.d.sh(); err != nil {
		return err.Error(), nil
	}
	m, err := manifest.Resolve(x.dir)
	if err != nil {
		return fmt.Sprintf("the manifest at %s does not resolve: %v", short(x.sha), err), nil
	}
	ws, _ := m.WorkspaceIn(x.d.Repo, x.sl.Workspace)
	oracleCmd := m.OracleCmd(x.sl.Oracle, ws)
	if strings.TrimSpace(oracleCmd) == "" {
		return fmt.Sprintf("slice %s has no oracle to run", x.r.Slice), nil
	}
	cmd := oracleCmd
	if x.r.Command != "" {
		cmd = x.r.Command
	}
	x.res.Command, x.res.Env = cmd, x.sl.Env
	// What already says the run cannot cover is settled before the env class
	// is brought up, so a class that will not come up leaves the slice alone.
	x.res.TreeBefore = treeState(x.dir)
	x.pre = x.uncoveredBecause(x.r.Command == "", x.res.TreeBefore, x.sha)

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
	if err := x.runOracle(m, cmd, cmd == oracleCmd && x.r.Command == ""); errors.Is(err, envrun.ErrStopped) {
		// jig was stopped mid-run: no verdict, and never the commit's failure.
		return "", nil
	} else if err != nil {
		return "", err
	}
	return "", nil
}

// descendsFromStart holds HEAD to the ticket's start sha, as the frontier's
// verifyGreen did: HEAD must be new work on the ticket's branch. It returns
// why not, "" when it does or when no start is known.
func (x *run) descendsFromStart() string {
	if x.descentDone {
		return x.descent
	}
	x.descent, x.descentDone = x.descentFromStart(), true
	return x.descent
}

// descentFromStart asks git, once per run: it is fixed before the command
// starts, since HEAD at the start and the start sha do not move under it.
func (x *run) descentFromStart() string {
	start := x.startSHA()
	if start == "" {
		return ""
	}
	if !gitx.HasCommit(x.d.Clone, start) {
		return fmt.Sprintf("the start sha %s is not in this clone", short(start))
	}
	if reason, ok := verifyGreen(x.dir, start, outcome.Result{Outcome: outcome.Green, Commit: x.sha}); !ok {
		return reason
	}
	return ""
}

// startSHA is the base the commit is held to descend from: the ticket's
// recorded start sha, else the merge base with origin's target (startSHA), ""
// when neither is known.
func (x *run) startSHA() string {
	d := x.d
	var recorded string
	if d.Repo != "" {
		if data, err := fileshare.ReadFile(d.Store.StartSHAPath(x.r.Ticket, d.Repo)); err == nil {
			recorded = string(data)
		}
	}
	return startSHA(d.Clone, x.sha, recorded, d.Target)
}

// changedFiles lists the files the commit changed against the base the gate's
// full scope uses (merge-base of origin/<target> and the commit, the start sha
// only when that fails: scopebase.Resolve), as the scope diff reads
// them (added, modified or type-changed). The start sha alone would list, after
// an integration merge, every file the target changed since it too. nil when the
// base is unknown or the diff fails, which ranks every recording alike.
func (x *run) changedFiles() []string {
	start := x.changedBase()
	if start == "" {
		return nil
	}
	files, err := gitx.DiffNameOnly(x.dir, start, x.sha, "AMT")
	if err != nil {
		return nil
	}
	return files
}

// changedBase is that base: the gate scope's when the repo and target are known,
// else startSHA's.
func (x *run) changedBase() string {
	d := x.d
	if d.Repo == "" || d.Target == "" {
		return x.startSHA()
	}
	base, err := scopebase.Resolve(d.Store, x.r.Ticket, x.dir, d.Repo, d.Target, x.sha)
	if err != nil {
		return x.startSHA()
	}
	return base
}

// bringUpEnv brings the slice's env class up in the worktree when it names
// one, as the frontier does: an env-up line, and down is what brings it back
// down (with its env-down line). An env class the manifest does not declare,
// or one that will not come up, is a reason the command cannot be reached; the
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
	h, uerr := envrun.Up(ec, x.r.Ticket, x.cmdDir)
	if uerr != nil {
		var unavail *envrun.Unavailable
		if !errors.As(uerr, &unavail) {
			return nil, fmt.Sprintf("bring up the env class %q: %v", x.sl.Env, uerr), nil
		}
		policy := unavail.Policy
		if policy == "" {
			policy = "pause"
		}
		if x.pre == "" {
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
		}
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

// uncoveredBecause says why a run that left the tree as treeAfter and HEAD at
// headEnd covers nothing, in the journal's words, or "" when it covers HEAD:
// the first of the six reasons that holds.
func (x *run) uncoveredBecause(ownOracle bool, treeAfter, headEnd string) string {
	switch {
	case x.res.TreeBefore != journal.TreeClean:
		return journal.UncoveredDirtyBefore
	case treeAfter != journal.TreeClean:
		return journal.UncoveredDirtyAfter
	case headEnd != x.sha:
		return journal.UncoveredHeadMoved
	case !ownOracle:
		return journal.UncoveredOtherCmd
	case !sameWorktree(x.dir, x.cmdDir):
		return journal.UncoveredOtherDir
	case x.descendsFromStart() != "":
		return journal.UncoveredNotDescend
	}
	return ""
}

// runOracle runs the command in the worktree and records what came of it: the
// oracle line, and for a run that covers HEAD the recorded line of a pass that
// recorded, then the green state and the verified line, in that order (see the
// pass below); a covering failure leaves the slice building, and a run that
// covers nothing changes no state.
func (x *run) runOracle(m manifest.Manifest, cmd string, ownOracle bool) (err error) {
	d := x.d
	rec := recorder{home: d.JigHome, storeID: x.storeID, ticket: x.r.Ticket, journal: x.journal, changed: x.changedFiles}
	var target recordTarget
	if !x.r.NoRecord {
		target = rec.start(x.r.Slice, x.attempt, x.sha)
	}
	// A run that jig's stop ended (in the command)
	// has nothing to record: its directory goes as a failed run's does.
	defer func() {
		if errors.Is(err, envrun.ErrStopped) {
			_ = rec.finish(x.r.Slice, x.attempt, x.sha, target, false)
		}
	}()

	// The oracle line carries everything needed to run this again by hand.
	base := func() journal.Line {
		return journal.Line{Event: journal.EventOracle, Commit: x.sha, Command: cmd, Env: x.sl.Env, Source: "jig",
			Dir: x.cmdDir, Subject: x.subject, Flags: x.r.Flags, HeadEnd: x.headNow(), TreeBefore: x.res.TreeBefore, TreeAfter: treeState(x.dir)}
	}
	tail := &tailWriter{}
	w := io.Writer(tail)
	if d.Out != nil {
		w = io.MultiWriter(d.Out, tail)
	}
	started := d.now()
	x.res.Ran = true
	use, oerr := d.oracle(cmd, x.cmdDir, target.env(), x.r.Timeout, w)
	if errors.Is(oerr, envrun.ErrStopped) {
		// No verdict: the line says the run ended without judging the code,
		// and the slice's state stays as it was.
		line := base()
		line.Outcome, line.Cause, line.Summary = "fail", journal.CauseStopped, oneLine(oerr.Error())
		if jerr := x.journal(line); jerr != nil {
			return jerr
		}
		x.recordedLine(journal.EventOracle)
		return oerr
	}
	// Rounded up, so every run records at least a second and 0 keeps meaning
	// "never ran".
	seconds := int((d.now().Sub(started) + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	x.res.Seconds = seconds
	x.res.Code = exitCodeOf(oerr)

	line := base()
	line.Seconds = seconds
	x.res.HeadEnd, x.res.TreeAfter = line.HeadEnd, line.TreeAfter
	line.SetUsage(use.CPU, use.PeakBytes, use.Basis)
	line.Uncovered = x.uncoveredBecause(ownOracle, line.TreeAfter, line.HeadEnd)
	x.res.Uncovered, x.res.Covered = line.Uncovered, line.Uncovered == ""
	if oerr == nil {
		line.Outcome = "pass"
		x.res.State = journal.VerifyPass
	} else {
		x.res.Tail, x.res.Summary = failureOutput(tail.String(), oerr)
		x.res.Signature = failureSignature(x.r.Slice, x.res.Summary, oerr)
		x.res.State = journal.VerifyFail
		line.Outcome, line.Summary, line.Signature = "fail", x.res.Summary, x.res.Signature
		line.Cause = failureCause(oerr)
	}
	if err := x.journal(line); err != nil {
		return err
	}
	x.recordedLine(journal.EventOracle)
	if err := rec.finish(x.r.Slice, x.attempt, x.sha, target, oerr == nil && x.res.Covered); err != nil {
		return err
	}
	if !x.res.Covered {
		// Recorded, and nothing more: no state, no verified line.
		x.push("run recorded, covers nothing: " + x.res.Uncovered)
		return nil
	}

	if oerr != nil {
		err := x.setState(func(st *store.SliceState) bool {
			st.Attempts = max(st.Attempts, x.attempt)
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
	err = x.setState(func(st *store.SliceState) bool {
		st.Attempts = max(st.Attempts, x.attempt)
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
	x.recordedLine(journal.EventVerified)
	x.push("oracle passed")
	return nil
}

// headNow is the worktree's HEAD now, "" when git cannot say.
func (x *run) headNow() string {
	head, err := gitx.RevParse(x.dir, "HEAD")
	if err != nil {
		return ""
	}
	return head
}

// recordedLine notes that the run wrote a line of kind, unless it writes none.
func (x *run) recordedLine(kind string) {
	if !x.r.NoRecord {
		x.res.Recorded = append(x.res.Recorded, kind)
	}
}
