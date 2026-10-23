package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"here", "name where the work is: project, store, repo, branch, ticket and slices", []flagSpec{
			{"store", "explicit store path", false},
			{"slice", "slice the work is on (default: JIG_SLICE, else the session's latest claim)", false},
			{"session", "harness session id (default: JIG_SESSION)", false},
			{"json", "print one JSON object instead of blocks", false},
			{"explain", "also print how each part was found", false},
			{"no-map", "do not record a recognized clone in the machine map", false},
		}, false},
		Order: 300,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdHere(e, args, stdout)
		},
	})
}

// cmdHere implements `jig here [<ticket>]`: it names where the work is from
// the working directory alone, and prints nothing at all outside a jig
// project. It reads the store and the repo and writes nothing but the machine
// map entry of a clone it recognizes; it never syncs, pushes or mirrors.
func cmdHere(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "here")
	storeFlag := fs.String("store", "", "explicit store path")
	sliceFlag := fs.String("slice", "", "slice the work is on (default: JIG_SLICE, else the session's latest claim)")
	sessionFlag := fs.String("session", "", "harness session id (default: JIG_SESSION)")
	jsonOut := fs.Bool("json", false, "print one JSON object instead of blocks")
	explain := fs.Bool("explain", false, "also print how each part was found")
	noMap := fs.Bool("no-map", false, "do not record a recognized clone in the machine map")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := atMostOne(positionals, "jig here", "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	jigHome, err := e.jigHome()
	if err != nil {
		return renderErr(stdout, err)
	}
	cwd, err := e.getwd()
	if err != nil {
		return renderErr(stdout, err)
	}
	where, err := locate.Locate(jigHome, cwd, locate.Opts{
		StoreFlag: absFrom(cwd, *storeFlag),
		Ticket:    ticket,
		Slice:     *sliceFlag,
		Session:   *sessionFlag,
		Getenv:    e.getenv,
		NoMap:     *noMap,
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	if where.How == locate.HowNone {
		// Outside a project there is nothing to say, and a hook that runs
		// this everywhere must hear nothing: --explain is the one way to ask.
		if *explain {
			axi.Render(stdout, explainBlock(where.Explain))
		}
		return 0
	}

	hc := hereCtx{e: e, Where: where, JigHome: jigHome}
	if where.StorePath != "" {
		cfg, err := project.Load(filepath.Join(where.StorePath, "project.yaml"))
		if err != nil {
			return renderErr(stdout, err)
		}
		if err := cfg.CheckSchemaVersion(false); err != nil {
			return renderErr(stdout, err)
		}
		if hc.Store, err = store.Open(where.StorePath); err != nil {
			return renderErr(stdout, err)
		}
		if where.TicketHow == locate.HowArg || where.TicketHow == locate.HowArgAlias {
			if err := requireTicket(hc.Store, where.Ticket); err != nil {
				return renderErr(stdout, err)
			}
		}
	}

	view, err := buildHereView(hc)
	if err != nil {
		return renderErr(stdout, err)
	}
	if *jsonOut {
		out, err := view.json(hc, hereSections())
		if err != nil {
			return renderErr(stdout, err)
		}
		if !*explain {
			out.Explain = nil
		}
		data, err := json.Marshal(out)
		if err != nil {
			return renderErr(stdout, err)
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	blocks, err := view.blocks(hc, hereSections())
	if err != nil {
		return renderErr(stdout, err)
	}
	if *explain {
		blocks = append(blocks, explainBlock(where.Explain))
	}
	blocks = append(blocks, axi.Help(view.help(hc)...))
	axi.Render(stdout, blocks...)
	return 0
}

// parseWithPositionals parses args against fs and returns the positional
// arguments wherever they sit among the flags, so `jig next JIG-1 --json` and
// `jig next --json JIG-1` mean the same (the flag package alone stops at the
// first positional). -h and a parse failure are parseFlags'.
func parseWithPositionals(stdout io.Writer, fs *flag.FlagSet, args []string) (positionals []string, handled bool, err error) {
	for {
		if handled, err := parseFlags(stdout, fs, args); handled || err != nil {
			return nil, handled, err
		}
		if fs.NArg() == 0 {
			return positionals, false, nil
		}
		positionals = append(positionals, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// atMostOne returns the one positional of a verb that takes one at most, "" if
// there is none, and a VALIDATION_ERROR if there are more.
func atMostOne(positionals []string, verb, what string) (string, error) {
	switch len(positionals) {
	case 0:
		return "", nil
	case 1:
		return positionals[0], nil
	}
	return "", &axi.Error{Msg: fmt.Sprintf("%s takes at most one %s, got %d", verb, what, len(positionals)), Code: "VALIDATION_ERROR"}
}

// exactlyOne is atMostOne for a verb that needs its positional.
func exactlyOne(positionals []string, verb, what string) (string, error) {
	one, err := atMostOne(positionals, verb, what)
	if err == nil && one == "" {
		return "", &axi.Error{Msg: fmt.Sprintf("missing required %s", what), Code: "VALIDATION_ERROR"}
	}
	return one, err
}

// explainBlock renders the steps a resolution took, one line each.
func explainBlock(lines []string) string {
	b := axi.Block{Header: fmt.Sprintf("explain[%d]:", len(lines))}
	for _, l := range lines {
		b.Lines = append(b.Lines, "  "+l)
	}
	return b.String()
}

// hereView is what `jig here` knows beyond where it is: the state of the
// ticket the work is on.
type hereView struct {
	title     string
	slices    []hereSlice
	verified  []hereVerified
	questions []hereQuestion
	pending   []herePending
}

type hereSlice struct {
	ID        string   `json:"id"`
	State     string   `json:"state"`
	Attempts  int      `json:"attempts"`
	BlockedBy []string `json:"blocked_by"`
	Question  string   `json:"question,omitempty"`
}

type hereVerified struct {
	Slice   string `json:"slice"`
	Commit  string `json:"commit"`
	Attempt int    `json:"attempt,omitempty"`
}

type hereQuestion struct {
	ID    string `json:"id"`
	Slice string `json:"slice"`
	Body  string `json:"body"`
}

type herePending struct {
	Slice  string `json:"slice"`
	Commit string `json:"commit"`
	Since  string `json:"since,omitempty"`
}

// buildHereView reads the ticket Where names, if it names one and the store
// is here.
func buildHereView(hc hereCtx) (hereView, error) {
	var v hereView
	st, ticket := hc.Store, hc.Where.Ticket
	if st == nil || ticket == "" {
		return v, nil
	}
	if rec, err := st.ReadTicket(ticket); err == nil {
		v.title = rec.Title
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return v, err
	}
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return v, err
		}
		blocked := sl.BlockedBy
		if blocked == nil {
			blocked = []string{}
		}
		v.slices = append(v.slices, hereSlice{ID: sl.ID, State: ss.State, Attempts: ss.Attempts, BlockedBy: blocked, Question: ss.Question})
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return v, err
	}
	for _, q := range questions {
		if q.Status == "open" {
			v.questions = append(v.questions, hereQuestion{ID: q.ID, Slice: q.Slice, Body: q.Body})
		}
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return v, err
	}
	v.verified, v.pending = verificationsIn(lines)
	return v, nil
}

// verificationsIn reads a journal for the commits that verified (a verified
// line, once per commit and slice) and for those still being verified: a
// verify-pending line no oracle or verify-error line of the same commit and
// slice has followed.
func verificationsIn(lines []journal.Line) (verified []hereVerified, pending []herePending) {
	type key struct{ commit, slice string }
	seen := map[key]bool{}
	for _, l := range lines {
		if l.Event == journal.EventVerified && l.Commit != "" && !seen[key{l.Commit, l.Slice}] {
			seen[key{l.Commit, l.Slice}] = true
			verified = append(verified, hereVerified{Slice: l.Slice, Commit: l.Commit, Attempt: l.Attempt})
		}
	}
	for i, l := range lines {
		if l.Event != journal.EventVerifyPending || l.Commit == "" {
			continue
		}
		done := false
		for _, later := range lines[i+1:] {
			if later.Commit == l.Commit && later.Slice == l.Slice && (later.Event == journal.EventOracle || later.Event == journal.EventVerifyError) {
				done = true
				break
			}
		}
		if !done {
			pending = append(pending, herePending{Slice: l.Slice, Commit: l.Commit, Since: l.TS})
		}
	}
	return verified, pending
}

// blocks renders the identity block, the ticket's blocks and then each of
// sections' (the registered ones, for the verb).
func (v hereView) blocks(hc hereCtx, sections []hereSection) ([]string, error) {
	w := hc.Where
	pairs := [][2]string{{"project", w.Project}, {"how", w.How}}
	add := func(k, val string) {
		if val != "" {
			pairs = append(pairs, [2]string{k, val})
		}
	}
	add("kind", w.Kind)
	if w.Root != w.RepoDir {
		add("root", w.Root)
	}
	add("store", w.StorePath)
	if w.StorePath == "" {
		add("store_remote", w.StoreRemote)
	}
	add("repo", w.Repo)
	add("repo_dir", w.RepoDir)
	add("branch", w.Branch)
	add("ticket", w.Ticket)
	add("ticket_how", w.TicketHow)
	add("title", v.title)
	add("slice", w.Slice)
	add("slice_how", w.SliceHow)
	add("session", w.Session)
	add("session_how", w.SessionHow)
	if w.Mapped {
		add("mapped", "yes")
	}
	blocks := []string{axi.KV("here", pairs)}
	if len(w.Nested) > 0 {
		blocks = append(blocks, axi.Table("nested", []string{"dir"}, idRows(w.Nested)))
	}

	if hc.Store != nil && w.Ticket != "" {
		var rows [][]string
		for _, s := range v.slices {
			blocked, question := "-", "-"
			if len(s.BlockedBy) > 0 {
				blocked = joinPlus(s.BlockedBy)
			}
			if s.Question != "" {
				question = s.Question
			}
			rows = append(rows, []string{s.ID, s.State, fmt.Sprint(s.Attempts), blocked, question})
		}
		blocks = append(blocks, axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question"}, rows))
		if len(v.verified) > 0 {
			var vrows [][]string
			for _, x := range v.verified {
				vrows = append(vrows, []string{x.Slice, shortSHA(x.Commit)})
			}
			blocks = append(blocks, axi.Table("verified", []string{"slice", "commit"}, vrows))
		}
		if len(v.questions) == 0 {
			blocks = append(blocks, "questions: none")
		} else {
			var qrows [][]string
			for _, q := range v.questions {
				qrows = append(qrows, []string{q.ID, q.Slice, firstLineOf(q.Body, 100)})
			}
			blocks = append(blocks, axi.Table("questions", []string{"id", "slice", "question"}, qrows))
		}
		if len(v.pending) > 0 {
			var prows [][]string
			for _, p := range v.pending {
				prows = append(prows, []string{p.Slice, shortSHA(p.Commit)})
			}
			blocks = append(blocks, axi.Table("pending_verifications", []string{"slice", "commit"}, prows))
		}
	}

	for _, sec := range sections {
		if sec.Blocks == nil {
			continue
		}
		extra, err := sec.Blocks(hc)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, extra...)
	}
	return blocks, nil
}

// help is the one contextual next step.
func (v hereView) help(hc hereCtx) []string {
	w := hc.Where
	switch {
	case w.StorePath == "" && w.StoreRemote != "":
		return []string{fmt.Sprintf("The store is not cloned on this machine: git clone %s %s, then run `jig here` again",
			w.StoreRemote, filepath.Join(filepath.Dir(w.Root), w.Project+"-tickets"))}
	case w.StorePath == "":
		return []string{"The store of this project is not on this machine; pass --store, or `jig map <repo>=<path>` once it is"}
	case w.Ticket != "":
		return []string{fmt.Sprintf("Run `jig next %s` to see what is ready, `jig status %s` for the full state", w.Ticket, w.Ticket)}
	case w.Branch != "":
		return []string{fmt.Sprintf("Branch %s binds no ticket: run `jig next` to see which ticket to take, or name one: `jig here <ticket>`", w.Branch)}
	default:
		return []string{"Run `jig next` to see which ticket to take"}
	}
}

// hereJSON is `jig here --json`: one object, every located part carrying the
// how it was found.
type hereJSON struct {
	How      string         `json:"how"`
	JigHome  string         `json:"jig_home"`
	Cwd      string         `json:"cwd"`
	Project  string         `json:"project,omitempty"`
	Kind     string         `json:"kind,omitempty"`
	Root     string         `json:"root,omitempty"`
	Store    *hereStoreJSON `json:"store,omitempty"`
	Repo     *hereRepoJSON  `json:"repo,omitempty"`
	Ticket   *hereRefJSON   `json:"ticket,omitempty"`
	Slice    *hereRefJSON   `json:"slice,omitempty"`
	Session  *hereRefJSON   `json:"session,omitempty"`
	Nested   []string       `json:"nested,omitempty"`
	Mapped   bool           `json:"mapped,omitempty"`
	State    *hereStateJSON `json:"state,omitempty"`
	Sections map[string]any `json:"sections,omitempty"`
	Explain  []string       `json:"explain,omitempty"`
}

type hereStoreJSON struct {
	Path   string `json:"path,omitempty"`
	Remote string `json:"remote,omitempty"`
}

type hereRepoJSON struct {
	Name   string `json:"name,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Branch string `json:"branch,omitempty"`
}

type hereRefJSON struct {
	ID    string `json:"id"`
	How   string `json:"how"`
	Title string `json:"title,omitempty"`
}

type hereStateJSON struct {
	Slices               []hereSlice    `json:"slices"`
	Verified             []hereVerified `json:"verified"`
	Questions            []hereQuestion `json:"questions"`
	PendingVerifications []herePending  `json:"pending_verifications"`
}

// json builds the --json object: the located parts, the ticket's state, and
// each of sections' JSON under its name.
func (v hereView) json(hc hereCtx, sections []hereSection) (hereJSON, error) {
	w := hc.Where
	out := hereJSON{
		How: w.How, JigHome: w.JigHome, Cwd: w.Cwd,
		Project: w.Project, Kind: w.Kind, Root: w.Root,
		Nested: w.Nested, Mapped: w.Mapped, Explain: w.Explain,
	}
	if w.StorePath != "" || w.StoreRemote != "" {
		out.Store = &hereStoreJSON{Path: w.StorePath, Remote: w.StoreRemote}
	}
	if w.Repo != "" || w.RepoDir != "" || w.Branch != "" {
		out.Repo = &hereRepoJSON{Name: w.Repo, Dir: w.RepoDir, Branch: w.Branch}
	}
	if w.Ticket != "" {
		out.Ticket = &hereRefJSON{ID: w.Ticket, How: w.TicketHow, Title: v.title}
	}
	if w.Slice != "" {
		out.Slice = &hereRefJSON{ID: w.Slice, How: w.SliceHow}
	}
	if w.Session != "" {
		out.Session = &hereRefJSON{ID: w.Session, How: w.SessionHow}
	}
	if hc.Store != nil && w.Ticket != "" {
		st := &hereStateJSON{Slices: v.slices, Verified: v.verified, Questions: v.questions, PendingVerifications: v.pending}
		if st.Slices == nil {
			st.Slices = []hereSlice{}
		}
		if st.Verified == nil {
			st.Verified = []hereVerified{}
		}
		if st.Questions == nil {
			st.Questions = []hereQuestion{}
		}
		if st.PendingVerifications == nil {
			st.PendingVerifications = []herePending{}
		}
		out.State = st
	}
	for _, sec := range sections {
		if sec.JSON == nil {
			continue
		}
		extra, err := sec.JSON(hc)
		if err != nil {
			return hereJSON{}, err
		}
		if out.Sections == nil {
			out.Sections = map[string]any{}
		}
		out.Sections[sec.Name] = extra
	}
	return out, nil
}

// shortSHA is the first twelve characters of a commit id.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// firstLineOf is s up to its first line break, cut to max runes.
func firstLineOf(s string, max int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
