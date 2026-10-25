package slicectx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/graphify"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
)

// Caps on what one context lists, so a long-running ticket does not bury the
// slice it is about.
const (
	// MaxFailedAttempts is how many of the latest failed attempts are listed.
	MaxFailedAttempts = 8
	// MaxNotes is how many of the latest notes are listed.
	MaxNotes = 20
	// MaxEarlierFiles caps one earlier slice's file list; the rest are counted
	// in FilesOmitted.
	MaxEarlierFiles = 50
)

// seamsHeading is the brief section that names the public interfaces and
// critical paths a builder tests at (ADR 0016).
const seamsHeading = "Seams"

// Input is what Build reads. Everything but Store, Ticket and Slice is
// optional: a context built without a clone has no oracle command, no
// earlier commits and no graph, and says so by leaving them out.
type Input struct {
	Store  *store.Store
	Ticket string
	Slice  string
	// Repo is the name of the repo the slice builds in, and RepoDir its clone
	// on this machine: where the manifest is read, the commits of the earlier
	// slices are found by their Jig-Slice trailer, and the code graph runs.
	Repo    string
	RepoDir string
	// Graph is the code-graph plane. nil, or a plane that is not enabled,
	// leaves Related empty and records nothing.
	Graph graphify.Plane
	// RecordGraph receives the graph line (Event graph) after each run of the
	// plane, for the machine ledger. nil records none.
	RecordGraph func(journal.Line) error
	// Session is the harness session the line is recorded for.
	Session string
}

// Context is what a builder needs to start on one slice: the slice itself, the
// brief sections it builds from, what the ticket verified slices already
// did, the failed attempts so far, the code a graph links to its goal, and
// the questions and notes about it. It is the pulled form of what
// slice.json carried.
type Context struct {
	Ticket string `json:"ticket"`
	Title  string `json:"title,omitempty"`
	Slice  Slice  `json:"slice"`
	// Repo is the repo the slice builds in and Clone its clone on this
	// machine, "" when the command that built the context left them out (a
	// project of one repo has nothing to tell apart).
	Repo  string `json:"repo,omitempty"`
	Clone string `json:"clone,omitempty"`
	// Brief and Intent are the absolute paths of the ticket brief.md and
	// intent.md, "" when it has none.
	Brief  string `json:"brief"`
	Intent string `json:"intent"`
	// BriefSections are the sections the from_brief of the slice names, with
	// their text. StaleBriefSections counts the names that match no section
	// of the brief as it is now: the brief was edited after the slice was cut.
	BriefSections      []BriefSection `json:"brief_sections"`
	StaleBriefSections int            `json:"stale_brief_sections,omitempty"`
	// Seams is the Seams section of the brief when the slice does not list it
	// among its own sections.
	Seams *BriefSection `json:"seams,omitempty"`
	// OracleSeconds is how long the latest run by jig of the oracle command
	// of the slice took on this ticket, in seconds; 0 before the first.
	OracleSeconds int `json:"oracle_seconds"`
	// EarlierSlices are the slices of the ticket that verified, in the order
	// they did, with their commits and the files those changed.
	EarlierSlices []EarlierSlice `json:"earlier_slices"`
	// FailedAttempts are the latest failed verifications of the slice, oldest
	// first, each with the summary of why.
	FailedAttempts []FailedAttempt `json:"failed_attempts"`
	// Related is the code a graph links to the slice goal, by file.
	Related []RelatedFile `json:"related"`
	// Questions are the questions about the slice, open and answered, in id
	// order.
	Questions []Question `json:"questions"`
	// Notes are the notes left on the slice or on the whole ticket.
	Notes []Note `json:"notes"`
}

// Slice is the slice a context is about.
type Slice struct {
	ID        string `json:"id"`
	Goal      string `json:"goal"`
	Workspace string `json:"workspace"`
	Oracle    string `json:"oracle"`
	// OracleCommand is the oracle resolved to the command jig runs, "" when
	// the manifest of the clone does not declare the workspace of the slice.
	OracleCommand string   `json:"oracle_command,omitempty"`
	Env           string   `json:"env,omitempty"`
	State         string   `json:"state"`
	BlockedBy     []string `json:"blocked_by"`
	// Attempt is the number the next verification of the slice takes
	// (journal.NextAttempt): one past the highest attempt any of its lines
	// carries.
	Attempt int `json:"attempt"`
}

// BriefSection is one section of the brief: its heading and its text.
type BriefSection struct {
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

// EarlierSlice is a slice of the ticket that verified, with the commits that
// carry its Jig-Slice trailer and the files they changed.
type EarlierSlice struct {
	ID      string   `json:"id"`
	Commits []Commit `json:"commits"`
	Files   []string `json:"files"`
	// FilesOmitted counts the files past MaxEarlierFiles left out of Files.
	FilesOmitted int `json:"files_omitted,omitempty"`
}

// Commit is a commit of an earlier slice: its sha and subject line (empty when
// the clone does not hold it).
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject,omitempty"`
}

// FailedAttempt is one failed verification of the slice.
type FailedAttempt struct {
	Attempt int    `json:"attempt"`
	Commit  string `json:"commit,omitempty"`
	Summary string `json:"summary,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
}

// RelatedFile is a file the code graph links to the goal of the slice, with
// the symbols in it that matched, as "<label> L<line>".
type RelatedFile struct {
	File    string   `json:"file"`
	Symbols []string `json:"symbols"`
}

// Question is a question about the slice and, once answered, its answer and
// who the answer speaks for.
type Question struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Body       string `json:"body"`
	Answer     string `json:"answer,omitempty"`
	Provenance string `json:"provenance,omitempty"`
}

// Note is a note someone left on the slice, or on the ticket when Slice is
// empty.
type Note struct {
	TS      string `json:"ts"`
	Slice   string `json:"slice,omitempty"`
	Session string `json:"session,omitempty"`
	Text    string `json:"text"`
}

// Build reads everything a context holds. A part that cannot be had for lack
// of a clone, a manifest or a graph is left empty; a part that is there and
// cannot be read is an error.
func Build(in Input) (Context, error) {
	st := in.Store
	rec, err := st.ReadTicket(in.Ticket)
	if err != nil {
		return Context{}, err
	}
	slices, err := st.ReadSlices(in.Ticket)
	if err != nil {
		return Context{}, err
	}
	var sl *store.Slice
	for i := range slices {
		if slices[i].ID == in.Slice {
			sl = &slices[i]
		}
	}
	if sl == nil {
		return Context{}, fmt.Errorf("slicectx: ticket %s has no slice %s", in.Ticket, in.Slice)
	}
	state, err := st.ReadSliceState(in.Ticket, sl.ID)
	if err != nil {
		return Context{}, err
	}
	lines, err := journal.Read(st, in.Ticket)
	if err != nil {
		return Context{}, err
	}

	c := Context{
		Ticket: in.Ticket,
		Title:  rec.Title,
		Slice: Slice{
			ID: sl.ID, Goal: sl.Goal, Workspace: sl.Workspace, Oracle: sl.Oracle, Env: sl.Env,
			State: state.State, BlockedBy: nonNil(sl.BlockedBy), Attempt: journal.NextAttempt(lines, sl.ID),
		},
		BriefSections:  []BriefSection{},
		EarlierSlices:  []EarlierSlice{},
		FailedAttempts: []FailedAttempt{},
		Related:        []RelatedFile{},
		Questions:      []Question{},
		Notes:          []Note{},
	}

	if in.RepoDir != "" {
		if m, err := manifest.Resolve(in.RepoDir); err == nil {
			if ws, ok := m.WorkspaceIn(in.Repo, sl.Workspace); ok {
				c.Slice.OracleCommand = m.OracleCmd(sl.Oracle, ws)
			}
		}
	}
	if c.Slice.OracleCommand != "" {
		c.OracleSeconds = journal.LastOracleSeconds(lines, c.Slice.OracleCommand, sl.Env)
	}

	if err := c.readBrief(st, in.Ticket, *sl); err != nil {
		return Context{}, err
	}
	c.FailedAttempts = failedAttempts(lines, sl.ID)
	c.Notes = notes(lines, sl.ID)

	c.EarlierSlices = earlierSlices(in, lines)
	c.Related = related(in, *sl, c.EarlierSlices)

	questions, err := st.ReadQuestions(in.Ticket)
	if err != nil {
		return Context{}, err
	}
	for _, q := range questions {
		if q.Slice == sl.ID {
			c.Questions = append(c.Questions, Question{ID: q.ID, Status: q.Status, Body: q.Body, Answer: q.Answer, Provenance: q.Provenance})
		}
	}
	return c, nil
}

// readBrief fills the brief and intent paths, the brief sections of the slice
// and the Seams section from the brief.md of the ticket.
func (c *Context) readBrief(st *store.Store, ticket string, sl store.Slice) error {
	if path := filepath.Join(st.TicketDir(ticket), "brief.md"); exists(path) {
		c.Brief = absolute(path)
	}
	if path := st.IntentPath(ticket); exists(path) {
		c.Intent = absolute(path)
	}
	if c.Brief == "" {
		return nil
	}
	data, err := os.ReadFile(c.Brief)
	if err != nil {
		return err
	}
	byHash := map[string]store.BriefSection{}
	var seams *store.BriefSection
	for _, sec := range store.BriefSections(data) {
		sec := sec
		byHash[sec.Hash] = sec
		if sec.Heading == seamsHeading {
			seams = &sec
		}
	}
	listed := map[string]bool{}
	for _, h := range sl.FromBrief {
		sec, ok := byHash[h]
		if !ok {
			c.StaleBriefSections++
			continue
		}
		c.BriefSections = append(c.BriefSections, BriefSection{Heading: sec.Heading, Text: sec.Body})
		listed[sec.Heading] = true
	}
	if seams != nil && !listed[seams.Heading] {
		c.Seams = &BriefSection{Heading: seams.Heading, Text: seams.Body}
	}
	return nil
}

// failedAttempts lists the failed oracle runs of slice, oldest first, the
// latest MaxFailedAttempts of them.
func failedAttempts(lines []journal.Line, slice string) []FailedAttempt {
	out := []FailedAttempt{}
	for _, l := range lines {
		if l.Slice == slice && l.Event == journal.EventOracle && l.Outcome == "fail" {
			out = append(out, FailedAttempt{Attempt: l.Attempt, Commit: l.Commit, Summary: l.Summary, Seconds: l.Seconds})
		}
	}
	if len(out) > MaxFailedAttempts {
		out = out[len(out)-MaxFailedAttempts:]
	}
	return out
}

// notes lists the notes left on slice or on the whole ticket, oldest first,
// the latest MaxNotes of them.
func notes(lines []journal.Line, slice string) []Note {
	out := []Note{}
	for _, l := range lines {
		if l.Event == journal.EventNote && (l.Slice == slice || l.Slice == "") {
			out = append(out, Note{TS: l.TS, Slice: l.Slice, Session: l.Session, Text: l.Summary})
		}
	}
	if len(out) > MaxNotes {
		out = out[len(out)-MaxNotes:]
	}
	return out
}

// Blocks renders the context in the text register of jig: one block per
// part, the parts that are empty left out.
func (c Context) Blocks() []string {
	s := c.Slice
	pairs := [][2]string{{"ticket", c.Ticket}}
	add := func(k, v string) {
		if v != "" {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	add("title", c.Title)
	pairs = append(pairs, [2]string{"slice", s.ID}, [2]string{"goal", s.Goal})
	add("workspace", s.Workspace)
	add("repo", c.Repo)
	add("clone", c.Clone)
	add("oracle", s.Oracle)
	add("oracle_command", s.OracleCommand)
	add("env", s.Env)
	add("state", s.State)
	if len(s.BlockedBy) > 0 {
		add("blocked_by", strings.Join(s.BlockedBy, "+"))
	}
	pairs = append(pairs, [2]string{"attempt", fmt.Sprint(s.Attempt)}, [2]string{"oracle_seconds", fmt.Sprint(c.OracleSeconds)})
	add("brief", c.Brief)
	add("intent", c.Intent)
	if c.StaleBriefSections > 0 {
		add("stale_brief_sections", fmt.Sprint(c.StaleBriefSections))
	}
	blocks := []string{axi.KV("context", pairs)}

	if len(c.BriefSections) > 0 {
		blocks = append(blocks, sectionsBlock(fmt.Sprintf("brief_sections[%d]:", len(c.BriefSections)), c.BriefSections))
	}
	if c.Seams != nil {
		blocks = append(blocks, sectionsBlock("seams:", []BriefSection{*c.Seams}))
	}
	if len(c.EarlierSlices) > 0 {
		rows := make([][]string, 0, len(c.EarlierSlices))
		for _, e := range c.EarlierSlices {
			rows = append(rows, []string{e.ID, e.commitsCell(), e.filesCell()})
		}
		blocks = append(blocks, axi.Table("earlier_slices", []string{"slice", "commits", "files"}, rows))
	}
	if len(c.FailedAttempts) > 0 {
		rows := make([][]string, 0, len(c.FailedAttempts))
		for _, a := range c.FailedAttempts {
			rows = append(rows, []string{fmt.Sprint(a.Attempt), short(a.Commit), dash(a.Summary)})
		}
		blocks = append(blocks, axi.Table("failed_attempts", []string{"attempt", "commit", "summary"}, rows))
	}
	if len(c.Related) > 0 {
		rows := make([][]string, 0, len(c.Related))
		for _, r := range c.Related {
			rows = append(rows, []string{r.File, strings.Join(r.Symbols, "+")})
		}
		blocks = append(blocks, axi.Table("related", []string{"file", "symbols"}, rows))
	}
	if len(c.Questions) > 0 {
		rows := make([][]string, 0, len(c.Questions))
		for _, q := range c.Questions {
			rows = append(rows, []string{q.ID, q.Status, q.Body, dash(q.Answer), dash(q.Provenance)})
		}
		blocks = append(blocks, axi.Table("questions", []string{"id", "status", "question", "answer", "provenance"}, rows))
	}
	if len(c.Notes) > 0 {
		rows := make([][]string, 0, len(c.Notes))
		for _, n := range c.Notes {
			rows = append(rows, []string{n.TS, dash(n.Slice), dash(n.Session), n.Text})
		}
		blocks = append(blocks, axi.Table("notes", []string{"ts", "slice", "session", "note"}, rows))
	}
	return blocks
}

// sectionsBlock renders brief sections as the markdown they are, indented
// under header.
func sectionsBlock(header string, sections []BriefSection) string {
	b := axi.Block{Header: header}
	for i, sec := range sections {
		if i > 0 {
			b.Lines = append(b.Lines, "")
		}
		b.Lines = append(b.Lines, "  ## "+sec.Heading)
		for _, line := range strings.Split(sec.Text, "\n") {
			if strings.TrimSpace(line) == "" {
				b.Lines = append(b.Lines, "")
				continue
			}
			b.Lines = append(b.Lines, "  "+line)
		}
	}
	return b.String()
}

// commitsCell is the commits of an earlier slice as one table cell: each
// "<sha12> <subject>", joined with " | ".
func (e EarlierSlice) commitsCell() string {
	parts := make([]string, 0, len(e.Commits))
	for _, c := range e.Commits {
		parts = append(parts, strings.TrimSpace(short(c.SHA)+" "+c.Subject))
	}
	return strings.Join(parts, " | ")
}

// filesCell is the files of an earlier slice as one table cell, joined with
// "+", and how many more there were.
func (e EarlierSlice) filesCell() string {
	if len(e.Files) == 0 {
		return "-"
	}
	cell := strings.Join(e.Files, "+")
	if e.FilesOmitted > 0 {
		cell += fmt.Sprintf(" (+%d more)", e.FilesOmitted)
	}
	return cell
}

// short is the first twelve characters of a commit id.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// dash is s, or "-" when it is empty.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func absolute(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
