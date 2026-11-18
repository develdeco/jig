package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verify"
)

// The usage lines of `jig verify`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	verifySliceUsage   = "slice to verify (default: HEAD's Jig-Slice trailer, then the slice your session claimed)"
	verifySessionUsage = "harness session id recorded on the lines (default: $JIG_SESSION, else CLAUDE_CODE_SESSION_ID)"
	verifyStoreUsage   = "explicit store path"
	verifyProjectUsage = "project name, resolved via the machine mapping"
)

// verifySliceTrailer is the commit trailer that names the slice a commit is
// for.
const verifySliceTrailer = "Jig-Slice"

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"verify", "run a slice's oracle in this worktree, in the foreground, and record the run (exit code: the command's own, 125 when jig failed before it started)", []flagSpec{
			{"slice", verifySliceUsage, false},
			{"session", verifySessionUsage, false},
			{"store", verifyStoreUsage, false},
			{"project", verifyProjectUsage, false},
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
	ticket                 string
	slice, session         string
	storeFlag, projectFlag string
}

// cmdVerify implements `jig verify [T] [--slice S] [--session ID]`: run slice
// S's oracle in this worktree, in the foreground and the caller's
// environment, stream its output, and record the run in the ticket's journal.
// jig starts nothing and waits for nothing: the exit code is the command's
// own, and 125 when jig failed before the command started. The ticket
// defaults to the one the branch is for, the slice to HEAD's Jig-Slice
// trailer and then the slice the session claimed.
func cmdVerify(e env, args []string, stdout io.Writer) int {
	// The positional comes first (`jig verify T-1 --slice a`) or last; the flag
	// package stops at the first flag, so the leading ones are taken out here.
	var pos []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}

	fs := newFlagSet(e, "verify")
	var o verifyOpts
	fs.StringVar(&o.slice, "slice", "", verifySliceUsage)
	fs.StringVar(&o.session, "session", "", verifySessionUsage)
	fs.StringVar(&o.storeFlag, "store", "", verifyStoreUsage)
	fs.StringVar(&o.projectFlag, "project", "", verifyProjectUsage)
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return unrun(stdout, err)
	}
	pos = append(pos, fs.Args()...)

	var err error
	if o.ticket, err = splitVerifyArgs(pos); err != nil {
		return unrun(stdout, err)
	}
	if o.session == "" {
		o.session = envSession(e)
	}
	if o.storeFlag, err = e.abs(o.storeFlag); err != nil {
		return unrun(stdout, err)
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, o.projectFlag, o.storeFlag, stdout)
	if err != nil {
		return unrun(stdout, err)
	}
	d, req, err := resolveVerify(e, o, st, cfg, mp, jigHome, stdout)
	if err != nil {
		return unrun(stdout, err)
	}
	return runVerify(d, req, stdout)
}

// unrun reports an error of jig's own, before the command started, and is the
// exit code of such a run.
func unrun(stdout io.Writer, err error) int {
	renderErr(stdout, err)
	return verify.ExitUnrun
}

// splitVerifyArgs reads the positionals: the ticket id (a key and a number,
// which is all a ticket's name is), optional and given once. A run is of the
// worktree's HEAD, so a commit is not a positional.
func splitVerifyArgs(pos []string) (ticket string, err error) {
	for _, p := range pos {
		switch {
		case strings.HasPrefix(p, "-"):
			return "", &axi.Error{Msg: fmt.Sprintf("%q is not a ticket", p), Code: "VALIDATION_ERROR"}
		case store.BranchLeadID(p) != p:
			return "", &axi.Error{
				Msg:  fmt.Sprintf("%q is not a ticket: jig verify runs the worktree's HEAD, so it takes no commit", p),
				Code: "VALIDATION_ERROR",
				Help: []string{"Check the commit out and run `jig verify` in it"},
			}
		case ticket != "":
			return "", &axi.Error{Msg: fmt.Sprintf("jig verify takes one ticket, got %s and %s", ticket, p), Code: "VALIDATION_ERROR"}
		}
		ticket = p
	}
	return ticket, nil
}

// resolveVerify turns the parsed command into what verify runs on: the
// worktree the command runs in, the ticket and the slice. Each has a default
// so the common call is bare `jig verify`. Where the work is comes from
// locate, as for `jig here`: the repo, the ticket the branch binds, the
// session and the slice it claimed.
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
		StoreFlag: st.Root, Ticket: ticket, Slice: o.slice, Session: o.session, Getenv: e.getenv, NoMap: true, Memo: e.memo,
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

	repoName, target, err := verifyRepo(cfg, where.Repo)
	if err != nil {
		return none(err)
	}
	dir, err := verifyDir(e.memo, cwd)
	if err != nil {
		return none(err)
	}
	sha, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		return none(&axi.Error{Msg: fmt.Sprintf("%s has no commit to verify", dir), Code: "VALIDATION_ERROR"})
	}
	slice, err := verifySlice(st, ticket, dir, sha, o, where.Slice)
	if err != nil {
		return none(err)
	}
	if err := verifySliceRepo(st, cfg, mp, ticket, slice, repoName); err != nil {
		return none(err)
	}

	d := verify.Deps{
		Store: st, JigHome: jigHome, Project: cfg.Name, Slots: cfg.PinnedSlots(), Clone: dir, Repo: repoName, Target: target,
		Now: e.now,
	}
	return d, verify.Request{Ticket: ticket, Slice: slice, Session: o.session}, nil
}

// verifyRepo is the repo the commit is in, by its name in the project, and its
// target branch: the one locate found around the working directory, else the
// project's only repo. A project of several repos with none to go by is refused: no
// repo is the one that owns what is not placed.
func verifyRepo(cfg project.Config, located string) (name, target string, err error) {
	for _, r := range cfg.Repos {
		if located != "" && r.Name() == located {
			return r.Name(), r.TargetBranch(), nil
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
		Help: []string{"Run `jig verify` from inside the clone of the repo the commit is in"},
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

// verifyDir is the worktree the run is of: the root of the checkout the
// command is run from. The builder's own worktree is where its commits are,
// so it is where the oracle runs.
func verifyDir(m *gitx.Memo, cwd string) (string, error) {
	top, err := m.TopLevel(cwd)
	if err != nil {
		return "", &axi.Error{
			Msg:  "this directory is not in a git worktree, so there is no commit to verify",
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `jig verify` from inside the worktree the slice's work is committed in"},
		}
	}
	return top, nil
}

// verifySlice picks the slice to verify: the --slice flag, else HEAD's
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

// verifyReport is what a `jig verify` call prints after the command's output.
type verifyReport struct {
	ticket, slice, sha, state string
	attempt, seconds          int
	covered                   bool
	uncovered                 string
	command, env              string
	summary, signature        string
	pushErr                   string
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
	if r.seconds > 0 {
		kv = append(kv, [2]string{"seconds", strconv.Itoa(r.seconds)})
	}
	covers := "no"
	if r.covered {
		covers = "yes"
	}
	kv = append(kv, [2]string{"covers", covers})
	for _, f := range [][2]string{{"uncovered", r.uncovered}, {"command", r.command}, {"env", r.env}, {"summary", r.summary}, {"signature", r.signature}} {
		if f[1] != "" {
			kv = append(kv, f)
		}
	}
	blocks := []string{axi.KV("verify", kv)}
	if r.pushErr != "" {
		blocks = append(blocks, axi.KV("warning", [][2]string{{"message", "the run is recorded here but could not be pushed to the store (the next checkpoint will): " + r.pushErr}}))
	}
	return append(blocks, axi.Help(help))
}

// verifyReportOf is the report of a run.
func verifyReportOf(res verify.Result) verifyReport {
	return verifyReport{
		ticket: res.Ticket, slice: res.Slice, sha: res.SHA, state: res.State, attempt: res.Attempt, seconds: res.Seconds,
		covered: res.Covered, uncovered: res.Uncovered, command: res.Command, env: res.Env, summary: res.Summary, signature: res.Signature,
		pushErr: res.PushErr,
	}
}

// runVerify runs the oracle in this process, in the foreground: the command's
// output goes to stdout as it is produced, the report follows it, and the
// exit code is the command's own (125 when it never started, 1 for a run jig
// stopped).
func runVerify(d verify.Deps, req verify.Request, stdout io.Writer) int {
	// A git hook that started jig leaves the repo variables in its
	// environment; main cleared them for this process already, and this keeps
	// the run honest if it is ever called in-process.
	gitx.ClearRepoEnv()
	d.Out = stdout
	res, err := verify.Run(d, req)
	if err != nil {
		return unrun(stdout, err)
	}
	rep := verifyReportOf(res)
	help := fmt.Sprintf("Run `jig status %s` to see the slice states", res.Ticket)
	if !res.Covered && res.Ran {
		help = "This run covers no commit (" + res.Uncovered + "): commit or discard the changes, then run `jig verify` again"
	}
	axi.Render(stdout, rep.blocks(help)...)
	switch {
	case res.State == journal.VerifyError:
		return verify.ExitUnrun
	case !res.Ran || res.State == "":
		return 1
	}
	return res.Code
}
