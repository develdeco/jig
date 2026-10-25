package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verify"
)

// The usage lines of `jig verify`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	verifySliceUsage   = "slice to verify (default: the commit's Jig-Slice trailer, then the slice your session claimed)"
	verifyWaitUsage    = "run the oracle here and wait for its outcome (or for the worker already running), instead of in a detached process"
	verifyRerunUsage   = "verify again a commit that already has a verification"
	verifySessionUsage = "harness session id recorded on the lines (default: $JIG_SESSION, else CLAUDE_CODE_SESSION_ID)"
	verifyStoreUsage   = "explicit store path"
	verifyProjectUsage = "project name, resolved via the machine mapping"
	verifyWorkerUsage  = "run as the detached worker that does the verification"
	verifyAgentUsage   = "agent id recorded on the lines"
	verifyCloneUsage   = "the repo clone to check the commit out from (set for the worker)"
)

// verifySliceTrailer is the commit trailer that names the slice a commit is
// for.
const verifySliceTrailer = "Jig-Slice"

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"verify", "run a slice's oracle on a commit in a detached worktree, recording the outcome", []flagSpec{
			{"slice", verifySliceUsage, false},
			{"wait", verifyWaitUsage, false},
			{"rerun", verifyRerunUsage, false},
			{"session", verifySessionUsage, false},
			{"store", verifyStoreUsage, false},
			{"project", verifyProjectUsage, false},
			{"worker", verifyWorkerUsage, true},
			{"agent-id", verifyAgentUsage, true},
			{"repo-dir", verifyCloneUsage, true},
		}, false},
		Order: 320,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdVerify(e, args, stdout)
		},
	})
}

// verifyOpts is what `jig verify` was asked, after its flags and positionals
// are parsed.
type verifyOpts struct {
	ticket, rev            string
	slice, session, agent  string
	storeFlag, projectFlag string
	cloneFlag              string
	wait, rerun, worker    bool
}

// cmdVerify implements `jig verify [T] [sha] [--slice S] [--wait] [--rerun]
// [--session ID]`: run slice S's oracle on commit sha of ticket T in a
// detached worktree and record the outcome in the ticket's journal. Without
// --wait the run is a detached worker (`--worker` is the worker itself, hidden)
// and the command returns once it is started; the ticket defaults to the one
// the clone's branch is for, the commit to HEAD, the slice to the commit's
// Jig-Slice trailer and then the slice the session claimed.
func cmdVerify(e env, args []string, stdout io.Writer) int {
	// The positionals come first (`jig verify T-1 <sha> --slice a`) or last
	// (a worker's command line puts its flags first); the flag package stops
	// at the first of them, so the leading ones are taken out here.
	var pos []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}

	fs := newFlagSet(e, "verify")
	var o verifyOpts
	fs.StringVar(&o.slice, "slice", "", verifySliceUsage)
	fs.BoolVar(&o.wait, "wait", false, verifyWaitUsage)
	fs.BoolVar(&o.rerun, "rerun", false, verifyRerunUsage)
	fs.StringVar(&o.session, "session", "", verifySessionUsage)
	fs.StringVar(&o.storeFlag, "store", "", verifyStoreUsage)
	fs.StringVar(&o.projectFlag, "project", "", verifyProjectUsage)
	fs.BoolVar(&o.worker, "worker", false, verifyWorkerUsage)
	fs.StringVar(&o.agent, "agent-id", "", verifyAgentUsage)
	fs.StringVar(&o.cloneFlag, "repo-dir", "", verifyCloneUsage)
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	pos = append(pos, fs.Args()...)

	var err error
	if o.ticket, o.rev, err = splitVerifyArgs(pos); err != nil {
		return renderErr(stdout, err)
	}
	if o.session == "" {
		o.session = envSession(e)
	}
	if o.storeFlag, err = e.abs(o.storeFlag); err != nil {
		return renderErr(stdout, err)
	}
	if o.cloneFlag, err = e.abs(o.cloneFlag); err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, o.projectFlag, o.storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	d, req, err := resolveVerify(e, o, st, cfg, mp, jigHome, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}

	switch {
	case o.worker:
		return runVerifyWorker(d, req, stdout)
	case o.wait:
		return runVerifyWait(e, d, req, stdout)
	}
	return runVerifySpawn(e, d, req, stdout)
}

// splitVerifyArgs tells the positionals apart: a ticket id (a key and a
// number, which is all a ticket's name is) and a revision (anything else a
// clone can resolve to a commit). Each is optional, and neither can be given
// twice.
func splitVerifyArgs(pos []string) (ticket, rev string, err error) {
	for _, p := range pos {
		switch {
		case store.BranchLeadID(p) == p:
			if ticket != "" {
				return "", "", &axi.Error{Msg: fmt.Sprintf("jig verify takes one ticket, got %s and %s", ticket, p), Code: "VALIDATION_ERROR"}
			}
			ticket = p
		default:
			if rev != "" {
				return "", "", &axi.Error{Msg: fmt.Sprintf("jig verify takes one commit, got %s and %s", rev, p), Code: "VALIDATION_ERROR"}
			}
			rev = p
		}
	}
	return ticket, rev, nil
}

// resolveVerify turns the parsed command into what verify runs on: the
// clone the commit is checked out from, the ticket, the commit, and the
// slice. Each has a default so the common call is bare `jig verify`. Where
// the work is comes from locate, as for `jig here`: the repo and its clone,
// the ticket the branch binds, the session and the slice it claimed.
func resolveVerify(e env, o verifyOpts, st *store.Store, cfg project.Config, mp project.MachineProject, jigHome string, stdout io.Writer) (verify.Deps, verify.Request, error) {
	none := func(err error) (verify.Deps, verify.Request, error) { return verify.Deps{}, verify.Request{}, err }
	cwd, err := e.getwd()
	if err != nil {
		return none(err)
	}
	ticket := o.ticket
	if ticket != "" {
		if ticket, err = resolveTicketArg(st, ticket, stdout); err != nil {
			return none(err)
		}
	}
	where, err := locate.Locate(jigHome, cwd, locate.Opts{
		StoreFlag: st.Root, Ticket: ticket, Slice: o.slice, Session: o.session, Getenv: e.getenv, NoMap: true,
	})
	if err != nil {
		return none(err)
	}
	if ticket == "" {
		if ticket = where.Ticket; ticket == "" {
			return none(&axi.Error{
				Msg:  fmt.Sprintf("cannot tell which ticket this is: the branch %q names none", where.Branch),
				Code: "VALIDATION_ERROR",
				Help: []string{"Name the ticket: `jig verify T-1`"},
			})
		}
	}
	if err := requireSlices(st, ticket); err != nil {
		return none(err)
	}
	o.session = where.Session

	repoName, target, err := verifyRepo(cfg, mp, where.Repo, o.cloneFlag)
	if err != nil {
		return none(err)
	}
	clone, workDir, err := verifyClone(cwd, o.cloneFlag, where.RepoDir, mp.Clones[repoName])
	if err != nil {
		return none(err)
	}

	rev := o.rev
	if rev == "" {
		rev = "HEAD"
	}
	if strings.HasPrefix(rev, "-") {
		return none(&axi.Error{Msg: fmt.Sprintf("%q is not a commit", rev), Code: "VALIDATION_ERROR"})
	}
	sha, err := gitx.RevParse(workDir, rev+"^{commit}")
	if err != nil {
		return none(&axi.Error{Msg: fmt.Sprintf("commit %s is not in %s", rev, workDir), Code: "VALIDATION_ERROR"})
	}
	slice, err := verifySlice(st, ticket, clone, sha, o, where.Slice)
	if err != nil {
		return none(err)
	}
	if err := verifySliceRepo(st, cfg, mp, ticket, slice, repoName); err != nil {
		return none(err)
	}

	d := verify.Deps{
		Store: st, JigHome: jigHome, Clone: clone, Repo: repoName, Target: target,
		Now: e.now, Start: e.spawn,
	}
	if exe, err := e.executable(); err == nil {
		d.Executable = exe
	}
	return d, verify.Request{Ticket: ticket, Slice: slice, SHA: sha, Session: o.session, Agent: o.agent, Rerun: o.rerun}, nil
}

// verifyRepo is the repo the commit is in, by its name in the project, and its
// target branch: the one locate found around the working directory, else the
// one whose mapped clone is the clone named (--repo-dir), else the project's
// only repo. A project of several repos with none to go by is refused: no
// repo is the one that owns what is not placed.
func verifyRepo(cfg project.Config, mp project.MachineProject, located, cloneFlag string) (name, target string, err error) {
	for _, r := range cfg.Repos {
		if located != "" && r.Name() == located {
			return r.Name(), r.TargetBranch(), nil
		}
	}
	if cloneFlag != "" {
		for _, r := range cfg.Repos {
			if dir := mp.Clones[r.Name()]; dir != "" && gitx.SameDir(dir, cloneFlag) {
				return r.Name(), r.TargetBranch(), nil
			}
		}
	}
	switch len(cfg.Repos) {
	case 0:
		return located, "", nil
	case 1:
		return cfg.Repos[0].Name(), cfg.Repos[0].TargetBranch(), nil
	}
	names := make([]string, len(cfg.Repos))
	for i, r := range cfg.Repos {
		names[i] = r.Name()
	}
	return "", "", &axi.Error{
		Msg:  fmt.Sprintf("cannot tell which repo of the project the commit is in: this directory is in none of %s", strings.Join(names, ", ")),
		Code: "VALIDATION_ERROR",
		Help: []string{"Run `jig verify` from inside the clone of the repo the commit is in, or pass --repo-dir"},
	}
}

// verifySliceRepo refuses a slice that builds in another repo of the project
// than the one the commit is in: its oracle would be run on code it is not
// about. A project of one repo, and a slice no repo owns yet (`jig validate`
// says so), have nothing to refuse.
func verifySliceRepo(st *store.Store, cfg project.Config, mp project.MachineProject, ticket, slice, repo string) error {
	if len(cfg.Repos) < 2 {
		return nil
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return err
	}
	place := ready.Place(ready.ProjectRepos(cfg, mp.Clones), slices)
	for _, s := range slices {
		if s.ID != slice {
			continue
		}
		owner := place.Repo[s.Workspace]
		if owner == ready.Unplaced || owner == repo {
			return nil
		}
		help := "Commit it in the clone of " + owner
		if dir := mp.Clones[owner]; dir != "" {
			help += ": " + dir
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("slice %s of %s builds in repo %s, but this commit is in repo %s", slice, ticket, owner, repo),
			Code: "VALIDATION_ERROR",
			Help: []string{help, "Or name the slice this commit is for: `Jig-Slice: <slice>` in its message, or --slice"},
		}
	}
	return nil
}

// verifyClone finds the operator's clone to check the commit out from and the
// directory to resolve the commit in. The clone is the one named (the worker's
// --repo-dir), else the one locate found the working directory in, else the
// project's mapped clone of the repo, else the repository around the working
// directory. The directory is where the command runs when that is a checkout
// of the same repository (a linked worktree has a HEAD of its own), else the
// clone itself.
func verifyClone(cwd, cloneFlag, located, mapped string) (clone, workDir string, err error) {
	switch {
	case cloneFlag != "":
		clone = cloneFlag
	case located != "":
		clone = located
	case mapped != "":
		clone = mapped
	default:
		if clone, err = gitx.TopLevel(cwd); err != nil {
			return "", "", &axi.Error{
				Msg:  "no clone of the project's repo is known on this machine, and this directory is not in a git repository",
				Code: "VALIDATION_ERROR",
				Help: []string{"Run `jig verify` from inside the repo's clone"},
			}
		}
	}
	workDir = clone
	if top, terr := gitx.TopLevel(cwd); terr == nil {
		theirs, e1 := gitx.CommonDir(top)
		ours, e2 := gitx.CommonDir(clone)
		if e1 == nil && e2 == nil && gitx.SameDir(theirs, ours) {
			workDir = top
		}
	}
	return clone, workDir, nil
}

// verifySlice picks the slice to verify: the --slice flag, else the commit's
// Jig-Slice trailer, else the one locate found for the session (JIG_SLICE, or
// the slice its latest claim holds). A trailer or a located slice that names a
// slice the ticket does not have is passed over; a flag that does is refused,
// since the person asked for it.
func verifySlice(st *store.Store, ticket, clone, sha string, o verifyOpts, located string) (string, error) {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	ids := make([]string, len(slices))
	has := map[string]bool{}
	for i, s := range slices {
		ids[i], has[s.ID] = s.ID, true
	}
	if o.slice != "" {
		if !has[o.slice] {
			return "", &axi.Error{
				Msg:  fmt.Sprintf("ticket %s has no slice %q", ticket, o.slice),
				Code: "VALIDATION_ERROR",
				Help: []string{"Its slices are: " + strings.Join(ids, ", ")},
			}
		}
		return o.slice, nil
	}
	if trailer, err := gitx.Trailer(clone, sha, verifySliceTrailer); err == nil && has[trailer] {
		return trailer, nil
	}
	if has[located] {
		return located, nil
	}
	return "", &axi.Error{
		Msg:  fmt.Sprintf("cannot tell which slice commit %s is for", shortSHA(sha)),
		Code: "VALIDATION_ERROR",
		Help: []string{
			"Add a `Jig-Slice: <slice>` trailer to the commit message, pass `--slice`, or claim a slice first",
			"The slices of " + ticket + " are: " + strings.Join(ids, ", "),
		},
	}
}

// verifyReport is what a `jig verify` call prints, whichever way it ran.
type verifyReport struct {
	ticket, slice, sha, state string
	attempt, seconds, pid     int
	existing                  bool
	command, env              string
	summary, signature        string
	log                       string
	pushErr, warning          string
}

// blocks renders the report as the verify block, a warning block when there
// is one, and the help line.
func (r verifyReport) blocks(help string) []string {
	state := r.state
	if state == "" {
		state = "unknown"
	}
	kv := [][2]string{{"ticket", r.ticket}, {"slice", r.slice}, {"commit", r.sha}, {"state", state}}
	if r.attempt > 0 {
		kv = append(kv, [2]string{"attempt", strconv.Itoa(r.attempt)})
	}
	if r.existing {
		kv = append(kv, [2]string{"existing", "true"})
	}
	if r.pid > 0 {
		kv = append(kv, [2]string{"pid", strconv.Itoa(r.pid)})
	}
	if r.seconds > 0 {
		kv = append(kv, [2]string{"seconds", strconv.Itoa(r.seconds)})
	}
	for _, f := range [][2]string{{"command", r.command}, {"env", r.env}, {"summary", r.summary}, {"signature", r.signature}, {"log", r.log}} {
		if f[1] != "" {
			kv = append(kv, f)
		}
	}
	blocks := []string{axi.KV("verify", kv)}
	var warn [][2]string
	if r.warning != "" {
		warn = append(warn, [2]string{"message", r.warning})
	}
	if r.pushErr != "" {
		warn = append(warn, [2]string{"message", "the verification is recorded here but could not be pushed to the store (the next checkpoint will): " + r.pushErr})
	}
	if len(warn) > 0 {
		blocks = append(blocks, axi.KV("warning", warn))
	}
	return append(blocks, axi.Help(help))
}

// exitCode is 1 for a verification that failed or could not run, else 0.
func (r verifyReport) exitCode() int {
	if r.state == journal.VerifyFail || r.state == journal.VerifyError {
		return 1
	}
	return 0
}

// verifyReportOf is the report of a verification that ran here.
func verifyReportOf(res verify.Result, log string) verifyReport {
	return verifyReport{
		ticket: res.Ticket, slice: res.Slice, sha: res.SHA, state: res.State, attempt: res.Attempt, seconds: res.Seconds,
		existing: res.Existing, command: res.Command, env: res.Env, summary: res.Summary, signature: res.Signature,
		log: log, pushErr: res.PushErr,
	}
}

// runVerifySpawn starts the detached worker, or says what is already there.
func runVerifySpawn(e env, d verify.Deps, req verify.Request, stdout io.Writer) int {
	got, err := verify.Spawn(d, req)
	if err != nil {
		return renderErr(stdout, err)
	}
	rep := verifyReport{
		ticket: got.Ticket, slice: got.Slice, sha: got.SHA, state: got.State, attempt: got.Attempt,
		existing: got.Existing, pid: got.PID, log: got.Log, warning: got.Warning,
	}
	call := fmt.Sprintf("jig verify %s %s --slice %s", got.Ticket, shortSHA(got.SHA), got.Slice)
	help := fmt.Sprintf("Run `%s --wait` to wait for the outcome, or `jig status %s` to see the slice states", call, got.Ticket)
	if got.Existing {
		help = fmt.Sprintf("Run `%s --rerun` to verify it again, or `%s --wait` to wait for one that is running", call, call)
	} else {
		rep.state = journal.VerifyPending
	}
	axi.Render(stdout, rep.blocks(help)...)
	return 0
}

// runVerifyWorker is the detached worker: it verifies in this process, with
// its stdout the machine-local log, and prints the report there. It ends by
// the exit code of the outcome the way --wait does, except that a failing
// oracle is a verification that worked.
func runVerifyWorker(d verify.Deps, req verify.Request, stdout io.Writer) int {
	// A git hook that started jig leaves the repo variables in its
	// environment; main cleared them for this process already, and this keeps
	// the worker honest if it is ever called in-process.
	gitx.ClearRepoEnv()
	d.Log = stdout
	res, err := verify.Run(d, req)
	if err != nil {
		return renderErr(stdout, err)
	}
	rep := verifyReportOf(res, "")
	axi.Render(stdout, rep.blocks(fmt.Sprintf("Run `jig status %s` to see the slice states", res.Ticket))...)
	if res.State == journal.VerifyError {
		return 1
	}
	return 0
}

// runVerifyWait verifies in this process and waits for the outcome: it runs
// the oracle itself, or, when a worker is already on the commit, waits for that
// worker's. The failed oracle's output tail is in the log it names.
func runVerifyWait(e env, d verify.Deps, req verify.Request, stdout io.Writer) int {
	log, logFile := openVerifyLog(d, req)
	if logFile != nil {
		defer logFile.Close()
		d.Log = logFile
	}
	res, err := verify.Run(d, req)
	if err != nil {
		return renderErr(stdout, err)
	}
	if res.Existing && res.State == journal.VerifyPending {
		res = waitForVerification(e, d, res)
	}
	rep := verifyReportOf(res, log)
	help := fmt.Sprintf("Run `jig status %s` to see the slice states", res.Ticket)
	switch {
	case res.State == journal.VerifyPending:
		help = fmt.Sprintf("Still pending: a worker is on it. Run `jig verify %s %s --slice %s --wait` to keep waiting", res.Ticket, shortSHA(res.SHA), res.Slice)
	case res.Existing:
		help = fmt.Sprintf("Run `jig verify %s %s --slice %s --rerun` to verify it again", res.Ticket, shortSHA(res.SHA), res.Slice)
	}
	axi.Render(stdout, rep.blocks(help)...)
	return rep.exitCode()
}

// openVerifyLog opens the verification's log for append and returns its path,
// or "" and nil when it cannot (the log is for the person; the verification
// does not depend on it).
func openVerifyLog(d verify.Deps, req verify.Request) (string, *os.File) {
	sha, err := gitx.RevParse(d.Clone, req.SHA+"^{commit}")
	if err != nil {
		return "", nil
	}
	path, err := verify.LogPath(d, req.Ticket, sha, req.Slice)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return "", nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return "", nil
	}
	return path, f
}

// verifyPoll is how often waitForVerification looks at the journal.
const verifyPoll = 500 * time.Millisecond

// waitForVerification waits for the worker that holds a verification to
// record its outcome: it reads the journal until the verification is no
// longer pending, the interrupt cancels e.ctx, or the longest an oracle may
// run (the clone's manifest timeout, else the default) and a grace have
// passed. The result is the outcome it found, or the pending result it was
// given.
func waitForVerification(e env, d verify.Deps, res verify.Result) verify.Result {
	limit := manifest.DefaultOracleLimit
	if m, err := manifest.Resolve(d.Clone); err == nil {
		limit = m.OracleLimit()
	}
	deadline := e.now().Add(limit + 5*time.Minute)
	for {
		if lines, err := journal.Read(d.Store, res.Ticket); err == nil {
			if cur := journal.VerificationState(lines, res.SHA, res.Slice); cur.State != journal.VerifyPending && cur.State != journal.VerifyNone {
				res.State, res.Attempt, res.Seconds, res.Summary = cur.State, cur.Attempt, cur.Seconds, cur.Summary
				return res
			}
		}
		if e.now().After(deadline) {
			return res
		}
		select {
		case <-e.ctx.Done():
			return res
		case <-time.After(verifyPoll):
		}
	}
}
