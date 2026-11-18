package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
	verifySliceUsage    = "slice to verify (default: HEAD's Jig-Slice trailer, then the slice your session claimed)"
	verifySessionUsage  = "harness session id recorded on the lines (default: $JIG_SESSION, else CLAUDE_CODE_SESSION_ID)"
	verifyStoreUsage    = "explicit store path"
	verifyProjectUsage  = "project name, resolved via the machine mapping"
	verifyFinalUsage    = "run the union of the ticket's slice commands at HEAD, the ticket's last run (no --slice, no -- <cmd>)"
	verifyDirUsage      = "where the command runs (default: the worktree's root); a run in another work tree covers nothing"
	verifyTimeoutUsage  = "end the command after this long (default: none; the harness owns time)"
	verifyDryRunUsage   = "print the header and the record that would be written; run and write nothing"
	verifyNoRecordUsage = "run and print, and write no line, state or push (an exploratory run)"
	verifyJSONUsage     = "print the closing report as one JSON object on stdout, and the command's output on stderr"
)

// verifySliceTrailer is the commit trailer that names the slice a commit is
// for.
const verifySliceTrailer = "Jig-Slice"

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"verify", "run a slice's oracle in this worktree, in the foreground, and record the run; `-- <cmd>` runs another command, which covers nothing; `--final` runs the union of the ticket's slice commands, the ticket's last run (exit code: the command's own, 125 when jig failed before it started)", []flagSpec{
			{"slice", verifySliceUsage, false},
			{"final", verifyFinalUsage, false},
			{"dir", verifyDirUsage, false},
			{"timeout", verifyTimeoutUsage, false},
			{"dry-run", verifyDryRunUsage, false},
			{"no-record", verifyNoRecordUsage, false},
			{"json", verifyJSONUsage, false},
			{"session", verifySessionUsage, false},
			{"store", verifyStoreUsage, false},
			{"project", verifyProjectUsage, false},
		}, false},
		Order: 320,
		Note:  "jig verify [<ticket>] [flags] [-- <cmd>]: the ticket defaults to the one the branch is for; `-- <cmd>` (default: the slice's oracle, verbatim) runs another command, recorded as differing and covering nothing; exit code: the command's own, 125 when jig failed before the command started",
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
	dir                    string
	timeout                time.Duration
	dryRun, noRecord, json bool
	// final is --final: the run is of the union of the ticket's slice
	// commands, not of one slice's oracle.
	final bool
	// command is what followed `--`, joined: another command to run in the
	// place of the slice's oracle, "" for none. args is the command line as
	// given, which the oracle line records.
	command, args string
}

// cmdVerify implements `jig verify [T] [--slice S] [--dir D] [--timeout T]
// [--dry-run] [--no-record] [--json] [-- cmd...]`: run slice S's oracle in
// this worktree, in the foreground and the caller's environment, stream its
// output between a header and a footer, and record the run in the ticket's
// journal. jig starts nothing and waits for nothing: the exit code is the
// command's own, and 125 when jig failed before the command started. The
// ticket defaults to the one the branch is for, the slice to HEAD's
// Jig-Slice trailer and then the slice the session claimed. Every flag has a
// default, and the header prints it.
func cmdVerify(e env, args []string, stdout io.Writer) int {
	// What follows `--` is a command, not a positional or a flag.
	var o verifyOpts
	o.args = strings.Join(args, " ")
	for i, a := range args {
		if a == "--" {
			o.command = joinCommand(args[i+1:])
			args = args[:i]
			break
		}
	}
	// The positional comes first (`jig verify T-1 --slice a`) or last; the flag
	// package stops at the first flag, so the leading ones are taken out here.
	var pos []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}

	fs := newFlagSet(e, "verify")
	fs.StringVar(&o.slice, "slice", "", verifySliceUsage)
	fs.BoolVar(&o.final, "final", false, verifyFinalUsage)
	fs.StringVar(&o.dir, "dir", "", verifyDirUsage)
	fs.DurationVar(&o.timeout, "timeout", 0, verifyTimeoutUsage)
	fs.BoolVar(&o.dryRun, "dry-run", false, verifyDryRunUsage)
	fs.BoolVar(&o.noRecord, "no-record", false, verifyNoRecordUsage)
	fs.BoolVar(&o.json, "json", false, verifyJSONUsage)
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
	if o.dir, err = e.abs(o.dir); err != nil {
		return unrun(stdout, err)
	}
	if o.timeout < 0 {
		return unrun(stdout, &axi.Error{Msg: "--timeout is not a negative time", Code: "VALIDATION_ERROR"})
	}
	if o.final {
		// The union is the ticket's own commands: another command or one
		// slice is a different run, so the two are refused before anything runs.
		if o.slice != "" {
			return unrun(stdout, &axi.Error{Msg: "--final and --slice cannot be given together: --final runs every slice's command", Code: "VALIDATION_ERROR"})
		}
		if o.command != "" {
			return unrun(stdout, &axi.Error{Msg: "--final and -- <cmd> cannot be given together: --final runs the slices' own commands", Code: "VALIDATION_ERROR"})
		}
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, o.projectFlag, o.storeFlag, stdout)
	if err != nil {
		return unrun(stdout, err)
	}
	d, req, source, err := resolveVerify(e, o, st, cfg, mp, jigHome, stdout)
	if err != nil {
		return unrun(stdout, err)
	}
	return runVerify(e, d, req, o, source, stdout)
}

// joinCommand is the words after `--` as the one command line sh runs: a
// single word is the command as it stands, several are joined, each quoted
// for sh when it holds anything but plain characters.
func joinCommand(words []string) string {
	if len(words) == 1 {
		return words[0]
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = shQuote(w)
	}
	return strings.Join(quoted, " ")
}

// shQuote is w as one word of an sh command line.
func shQuote(w string) string {
	if w != "" && strings.Trim(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
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
func resolveVerify(e env, o verifyOpts, st *store.Store, cfg project.Config, mp project.MachineProject, jigHome string, stdout io.Writer) (verify.Deps, verify.Request, string, error) {
	none := func(err error) (verify.Deps, verify.Request, string, error) {
		return verify.Deps{}, verify.Request{}, "", err
	}
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
	d := verify.Deps{
		Store: st, JigHome: jigHome, Project: cfg.Name, Slots: cfg.PinnedSlots(), Clone: dir, Repo: repoName, Target: target,
		Now: e.now,
	}
	if o.final {
		// A union is of every slice that builds in this repo: no slice is
		// picked, and a project of several repos says which are this one's.
		if len(cfg.Repos) > 1 {
			d.Repos = ready.ProjectRepos(cfg, mp.Clones)
		}
		req := verify.Request{Ticket: ticket, Session: o.session, Flags: o.args, Timeout: o.timeout, Dir: o.dir, NoRecord: o.noRecord}
		return d, req, "--final", nil
	}
	slice, source, err := verifySlice(st, ticket, dir, sha, o, where.Slice)
	if err != nil {
		return none(err)
	}
	if err := verifySliceRepo(st, cfg, mp, ticket, slice, repoName); err != nil {
		return none(err)
	}

	req := verify.Request{
		Ticket: ticket, Slice: slice, Session: o.session, Command: o.command, Flags: o.args, Timeout: o.timeout,
		Dir: o.dir, NoRecord: o.noRecord,
	}
	return d, req, source, nil
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
func verifySlice(st *store.Store, ticket, clone, sha string, o verifyOpts, located string) (slice, source string, err error) {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", "", err
	}
	ids := make([]string, len(slices))
	has := map[string]bool{}
	for i, s := range slices {
		ids[i], has[s.ID] = s.ID, true
	}
	if o.slice != "" {
		if !has[o.slice] {
			return "", "", &axi.Error{
				Msg:  fmt.Sprintf("ticket %s has no slice %q", ticket, o.slice),
				Code: "VALIDATION_ERROR",
				Help: []string{"Its slices are: " + strings.Join(ids, ", ")},
			}
		}
		return o.slice, "--slice", nil
	}
	if trailer, err := gitx.Trailer(clone, sha, verifySliceTrailer); err == nil && has[trailer] {
		return trailer, "the Jig-Slice trailer of HEAD", nil
	}
	if has[located] {
		return located, "the slice your session claimed ($JIG_SLICE or its claim)", nil
	}
	return "", "", &axi.Error{
		Msg:  fmt.Sprintf("cannot tell which slice commit %s is for", shortSHA(sha)),
		Code: "VALIDATION_ERROR",
		Help: []string{
			"Add a `Jig-Slice: <slice>` trailer to the commit message, pass `--slice`, or claim a slice first",
			"The slices of " + ticket + " are: " + strings.Join(ids, ", "),
		},
	}
}

// runVerify runs the oracle in this process, in the foreground, between a
// header and a footer. The header, before the command's output, names what is
// about to run: the command, the directory, the commit, the slice and where it
// came from, the tree, and every flag with the default it has. The footer,
// after it, says how it ended, whether HEAD is covered or why not, and what
// was recorded where; a run whose command never started has none. The exit
// code is the command's own (125 when it never started, 1 for a run jig
// stopped). With --json the footer is one JSON object on stdout and the
// header and the command's output go to stderr; --dry-run prints the header
// and the record that would be written, and runs and writes nothing.
func runVerify(e env, d verify.Deps, req verify.Request, o verifyOpts, source string, stdout io.Writer) int {
	// A git hook that started jig leaves the repo variables in its
	// environment; main cleared them for this process already, and this keeps
	// the run honest if it is ever called in-process.
	gitx.ClearRepoEnv()
	stream := stdout
	if o.json && e.stderr != nil {
		stream = e.stderr
	}
	if o.final {
		return runFinal(d, req, o, stream, stdout)
	}
	plan, err := verify.Plan(d, req)
	if err != nil {
		return unrun(stdout, err)
	}
	axi.Render(stream, verifyHeader(plan, req, o, source)...)
	if o.dryRun {
		axi.Render(stream, verifyWouldRecord(plan, req, o)...)
		return 0
	}

	d.Out = stream
	res, err := verify.Run(d, req)
	if err != nil {
		return unrun(stdout, err)
	}
	if res.State == journal.VerifyError {
		// The command never started: no footer, and the reason is the report.
		axi.Render(stdout, axi.KV("error", [][2]string{{"message", res.Summary}}), axi.Help("Fix what the message says, then run `jig verify` again"))
		return verify.ExitUnrun
	}
	code := res.Code
	if res.State == "" {
		code = 1
	}
	if o.json {
		out, err := json.Marshal(verifyJSONOf(res, req, code))
		if err != nil {
			return unrun(stdout, err)
		}
		fmt.Fprintln(stdout, string(out))
		return code
	}
	axi.Render(stream, verifyFooter(res, req, code)...)
	return code
}

// flagLine is one flag as the header prints it: its value, and the default
// when it is the one in force.
func flagLine(name, value string, isDefault bool) [2]string {
	if isDefault {
		value += " (default)"
	}
	return [2]string{name, value}
}

// verifyHeader is what jig prints before the command's output.
func verifyHeader(plan verify.Result, req verify.Request, o verifyOpts, source string) []string {
	command := plan.Command
	if req.Command != "" {
		command += " (given after --, not the slice's oracle)"
	}
	on := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	timeout, timeoutDefault := "none", true
	if req.Timeout > 0 {
		timeout, timeoutDefault = req.Timeout.String(), false
	}
	dir, dirDefault := plan.Dir, o.dir == ""
	kv := [][2]string{
		{"command", command}, {"dir", plan.Dir}, {"commit", plan.SHA}, {"slice", plan.Slice + " (" + source + ")"},
		{"ticket", plan.Ticket}, {"attempt", strconv.Itoa(plan.Attempt)}, {"tree", plan.TreeBefore},
	}
	if plan.Env != "" {
		kv = append(kv, [2]string{"env", plan.Env})
	}
	if req.Command == "" {
		kv[0][1] = command + " (the slice's oracle)"
	}
	if !req.NoRecord {
		// The one variable jig adds to the command's environment.
		kv = append(kv, [2]string{"env_added", "JIG_RECORD_DIR (where the scenarios' recordings go)"})
	}
	if o.json {
		kv = append(kv, [2]string{"output", "stderr (--json)"})
	}
	flags := [][2]string{
		flagLine("slice", plan.Slice, source != "--slice"),
		flagLine("--", map[bool]string{true: "the slice's oracle", false: req.Command}[req.Command == ""], req.Command == ""),
		flagLine("dir", dir, dirDefault),
		flagLine("timeout", timeout, timeoutDefault),
		flagLine("dry-run", on(o.dryRun), !o.dryRun),
		flagLine("no-record", on(o.noRecord), !o.noRecord),
		flagLine("json", on(o.json), !o.json),
	}
	return []string{axi.KV("verify", kv), axi.KV("flags", flags)}
}

// verifyWouldRecord is what --dry-run prints after the header: the lines a run
// of the command would write, and what it would change.
func verifyWouldRecord(plan verify.Result, req verify.Request, o verifyOpts) []string {
	record := [][2]string{
		{"event", "oracle"}, {"slice", plan.Slice}, {"attempt", strconv.Itoa(plan.Attempt)}, {"commit", plan.SHA},
		{"command", plan.Command}, {"dir", plan.Dir}, {"flags", req.Flags},
		{"tree_before", plan.TreeBefore},
	}
	covers := "yes"
	if plan.Uncovered != "" {
		covers = "no"
		record = append(record, [2]string{"uncovered", plan.Uncovered})
	}
	record = append(record, [2]string{"covers", covers})
	changes := "a covering pass writes the verified line and marks slice " + plan.Slice + " green; a covering failure leaves it building"
	if !plan.Covered {
		changes = "nothing else: a run that covers nothing changes no slice state"
	}
	if o.noRecord {
		changes = "nothing: --no-record writes no line, state or push"
	}
	return []string{
		axi.KV("would_record", record),
		axi.KV("would_change", [][2]string{{"effect", changes}}),
		axi.Help("Nothing ran and nothing was written; run `jig verify` without --dry-run to run it"),
	}
}

// verifyFooter is what jig prints after the command's output.
func verifyFooter(res verify.Result, req verify.Request, code int) []string {
	outcome := res.State
	if outcome == "" {
		outcome = "stopped"
	}
	kv := [][2]string{{"outcome", outcome}, {"exit", strconv.Itoa(code)}, {"seconds", strconv.Itoa(res.Seconds)}}
	covers := "yes"
	if !res.Covered {
		covers = "no"
	}
	kv = append(kv, [2]string{"covers", covers}, [2]string{"head", shortSHA(res.HeadEnd)})
	if res.Uncovered != "" {
		kv = append(kv, [2]string{"uncovered", res.Uncovered})
	}
	for _, f := range [][2]string{{"summary", res.Summary}, {"signature", res.Signature}} {
		if f[1] != "" {
			kv = append(kv, f)
		}
	}
	kv = append(kv, verifyRecorded(res, req)...)
	blocks := []string{axi.KV("result", append([][2]string{{"ticket", res.Ticket}, {"slice", res.Slice}, {"commit", res.SHA}, {"state", res.State}, {"attempt", strconv.Itoa(res.Attempt)}}, kv...))}
	if res.PushErr != "" {
		blocks = append(blocks, axi.KV("warning", [][2]string{{"message", "the run is recorded here but could not be pushed to the store (the next checkpoint will): " + res.PushErr}}))
	}
	help := "Run `jig status " + res.Ticket + "` to see the slice states"
	if !res.Covered {
		help = "This run covers no commit: commit or discard the changes, then run `jig verify` again"
	}
	return append(blocks, axi.Help(help))
}

// verifyRecorded says what the run recorded and where: the lines' kinds, the
// ticket's journal they are in, and whether they went to the store's remote.
func verifyRecorded(res verify.Result, req verify.Request) [][2]string {
	if req.NoRecord {
		return [][2]string{{"recorded", "nothing (--no-record)"}}
	}
	pushed := "no"
	if res.Pushed && res.PushErr == "" {
		pushed = "yes"
	}
	return [][2]string{
		{"recorded", strings.Join(res.Recorded, " ")},
		{"in", "the journal of " + res.Ticket},
		{"pushed", pushed},
	}
}

// verifyJSON is the closing report of a run as --json prints it.
type verifyJSON struct {
	Ticket     string   `json:"ticket"`
	Slice      string   `json:"slice"`
	Commit     string   `json:"commit"`
	HeadEnd    string   `json:"head_end"`
	Attempt    int      `json:"attempt"`
	Command    string   `json:"command"`
	Dir        string   `json:"dir"`
	Outcome    string   `json:"outcome"`
	Exit       int      `json:"exit"`
	Seconds    int      `json:"seconds"`
	Covered    bool     `json:"covered"`
	Uncovered  string   `json:"uncovered,omitempty"`
	TreeBefore string   `json:"tree_before"`
	TreeAfter  string   `json:"tree_after"`
	Recorded   []string `json:"recorded"`
	NoRecord   bool     `json:"no_record,omitempty"`
	Pushed     bool     `json:"pushed"`
	PushError  string   `json:"push_error,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Signature  string   `json:"signature,omitempty"`
}

func verifyJSONOf(res verify.Result, req verify.Request, code int) verifyJSON {
	outcome := res.State
	if outcome == "" {
		outcome = "stopped"
	}
	recorded := res.Recorded
	if recorded == nil {
		recorded = []string{}
	}
	return verifyJSON{
		Ticket: res.Ticket, Slice: res.Slice, Commit: res.SHA, HeadEnd: res.HeadEnd, Attempt: res.Attempt, Command: res.Command,
		Dir: res.Dir, Outcome: outcome, Exit: code, Seconds: res.Seconds, Covered: res.Covered, Uncovered: res.Uncovered,
		TreeBefore: res.TreeBefore, TreeAfter: res.TreeAfter, Recorded: recorded, NoRecord: req.NoRecord,
		Pushed: res.Pushed && res.PushErr == "", PushError: res.PushErr, Summary: res.Summary, Signature: res.Signature,
	}
}
