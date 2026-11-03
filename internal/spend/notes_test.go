package spend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// rig is a session on ticket T-1, event by event, over a store in a temp dir.
type rig struct {
	t          *testing.T
	st         *store.Store
	home       string
	transcript string
	where      locate.Where
	now        time.Time
	seq        int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{
		t:          t,
		st:         newStore(t),
		home:       t.TempDir(),
		transcript: filepath.Join(t.TempDir(), "s-1.jsonl"),
		where:      locate.Where{Ticket: "T-1", Slice: "a"},
		now:        time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
	}
}

// say adds usage lines to the transcript, each a message of its own.
func (r *rig) say(session string, tokens ...int64) {
	r.t.Helper()
	r.sayTo(r.transcript, session, tokens...)
}

// sayTo is say into the transcript at path.
func (r *rig) sayTo(path, session string, tokens ...int64) {
	r.t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	for _, n := range tokens {
		r.seq++
		id := fmt.Sprintf("m%d", r.seq)
		if _, err := f.WriteString(asst(session, id, "claude-sonnet-4-5", n, 0, 0, 0, 0) + "\n"); err != nil {
			r.t.Fatal(err)
		}
	}
}

// event runs the spend source for an event of kind, as session s-1 unless
// session says otherwise.
func (r *rig) event(kind string) []observe.Note {
	r.t.Helper()
	return r.eventAs(kind, "s-1", r.transcript)
}

func (r *rig) eventAs(kind, session, transcript string) []observe.Note {
	r.t.Helper()
	r.now = r.now.Add(time.Second)
	notes, err := Notes(observe.NoteCtx{
		Event:   observe.Event{Harness: "claude", Kind: kind, Session: session, Transcript: transcript},
		Where:   r.where,
		Store:   r.st,
		JigHome: r.home,
		Now:     r.now,
		Journal: func() ([]journal.Line, error) { return journal.Read(r.st, "T-1") },
	})
	if err != nil {
		r.t.Fatalf("%s event: %v", kind, err)
	}
	return notes
}

func (r *rig) spendLines() []journal.Line {
	r.t.Helper()
	all, err := journal.Read(r.st, "T-1")
	if err != nil {
		r.t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range all {
		if l.Event == journal.EventSpend {
			out = append(out, l)
		}
	}
	return out
}

// TestNotesRecordSpendAtStopAndSessionEndWhenItChanged: a stop and a session
// end write the cumulative spend line of the session when its total moved, and
// no other event writes anything.
func TestNotesRecordSpendAtStopAndSessionEndWhenItChanged(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.say("s-1", 100, 200)

	for _, kind := range []string{observe.KindSessionStart, observe.KindPrompt, observe.KindTool, observe.KindPostCommit} {
		r.event(kind)
	}
	if n := len(r.spendLines()); n != 0 {
		t.Fatalf("%d spend lines after a prompt and a tool call, want none: only a stop and a session end record", n)
	}

	r.event(observe.KindStop)
	r.event(observe.KindStop) // nothing new since
	lines := r.spendLines()
	if len(lines) != 1 {
		t.Fatalf("%d spend lines after two stops over one total, want 1", len(lines))
	}
	l := lines[0]
	if l.Session != "s-1" || l.Harness != "claude" || l.Slice != "a" || l.Ticket != "T-1" || l.Provenance != "jig" ||
		l.Model != "claude-sonnet-4-5" || l.Spend == nil || l.Spend.Input != 300 {
		t.Errorf("spend line = %+v, want session s-1 of claude on slice a, 300 input tokens", l)
	}

	r.say("s-1", 50)
	r.event(observe.KindStop)
	r.event(observe.KindSessionEnd) // nothing new since the stop
	if lines = r.spendLines(); len(lines) != 2 || lines[1].Spend.Input != 350 {
		t.Fatalf("spend lines = %+v, want a second, cumulative one at 350", lines)
	}

	r.say("s-1", 25)
	r.event(observe.KindSessionEnd)
	if lines = r.spendLines(); len(lines) != 3 || lines[2].Spend.Input != 375 {
		t.Fatalf("spend lines = %+v, want the session end to write the last 25 tokens", lines)
	}
	if got := FromLines(journalLines(t, r)); got.Sessions != 1 || got.Input != 375 {
		t.Errorf("ticket spend = %+v, want the session once, at its latest line", got)
	}
}

// sayAgent adds usage lines to the transcript of agent, which Claude Code keeps
// beside the main one: <main without .jsonl>/subagents/agent-<agent>.jsonl.
func (r *rig) sayAgent(session, agent string, tokens ...int64) {
	r.t.Helper()
	path := AgentTranscript(r.transcript, agent)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	r.sayTo(path, session, tokens...)
}

// agentStop runs the spend source for the stop of subagent agent of session
// s-1: the event names the session's main transcript, as the hook payload of a
// subagent does.
func (r *rig) agentStop(agent string) []observe.Note {
	r.t.Helper()
	r.now = r.now.Add(time.Second)
	notes, err := Notes(observe.NoteCtx{
		Event:   observe.Event{Harness: "claude", Kind: observe.KindStop, Session: "s-1", Agent: agent, Transcript: r.transcript},
		Where:   r.where,
		Store:   r.st,
		JigHome: r.home,
		Now:     r.now,
		Journal: func() ([]journal.Line, error) { return journal.Read(r.st, "T-1") },
	})
	if err != nil {
		r.t.Fatalf("subagent stop: %v", err)
	}
	return notes
}

// stopped records in the machine ledger that the stop of agent was placed at
// ticket, as the core does for every stop it handles. ticket "" is an agent
// placed at none.
func (r *rig) stopped(session, agent, ticket string) {
	r.t.Helper()
	l := journal.Line{TS: ledger.Stamp(r.now), Event: journal.EventHook, Outcome: observe.KindStop, Session: session, Agent: agent, Ticket: ticket}
	if err := ledger.Append(r.home, l); err != nil {
		r.t.Fatal(err)
	}
}

// TestNotesASubagentsStopNeverWritesTheSessionsLine: a subagent's stop writes
// the agent's line and not the session's, whatever the main transcript holds:
// the session's own stop and session end record that.
func TestNotesASubagentsStopNeverWritesTheSessionsLine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.say("s-1", 100)
	if notes := r.agentStop("a-1"); len(notes) != 0 {
		t.Errorf("notes at a subagent stop = %+v, want none", notes)
	}
	if n := len(r.spendLines()); n != 0 {
		t.Fatalf("%d spend lines after the stop of an agent with no transcript, want none: the main transcript's 100 tokens are the session's", n)
	}
	r.sayAgent("s-1", "a-1", 7)
	r.agentStop("a-1")
	lines := r.spendLines()
	if len(lines) != 1 || lines[0].Agent != "a-1" || lines[0].Spend.Input != 7 {
		t.Fatalf("spend lines = %+v, want one, the agent's, at 7 (not the session's 100)", lines)
	}
	r.event(observe.KindStop)
	if lines = r.spendLines(); len(lines) != 2 || lines[1].Agent != "" || lines[1].Spend.Input != 107 {
		t.Errorf("spend lines = %+v, want the session's own stop to write its line: the agent has no ledger line yet, so its 7 stay in the session's 107", lines)
	}
}

// TestNotesRecordASubagentsOwnLine: a subagent's stop appends a cumulative
// line for the agent on the ticket it is placed at: its parent's session id,
// its own id, and the totals of its transcript alone; an agent placed at no
// ticket, one with no transcript and one with nothing new since its last line
// write none.
func TestNotesRecordASubagentsOwnLine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.say("s-1", 1000)
	r.sayAgent("s-1", "a-1", 100, 200)
	r.sayAgent("s-1", "a-2", 5)

	r.agentStop("a-1")
	r.agentStop("a-1") // nothing new since
	lines := r.spendLines()
	if len(lines) != 1 {
		t.Fatalf("%d spend lines after two stops over one total, want 1", len(lines))
	}
	l := lines[0]
	if l.Session != "s-1" || l.Agent != "a-1" || l.Harness != "claude" || l.Slice != "a" || l.Ticket != "T-1" || l.Provenance != "jig" ||
		l.Model != "claude-sonnet-4-5" || l.Spend == nil || l.Spend.Input != 300 {
		t.Errorf("spend line = %+v, want session s-1, agent a-1 of claude on slice a, 300 input tokens (its own transcript alone)", l)
	}

	r.sayAgent("s-1", "a-1", 50)
	r.agentStop("a-1")
	r.agentStop("a-2")
	lines = r.spendLines()
	if len(lines) != 3 || lines[1].Agent != "a-1" || lines[1].Spend.Input != 350 || lines[2].Agent != "a-2" || lines[2].Spend.Input != 5 {
		t.Fatalf("spend lines = %+v, want a-1 again, cumulative at 350, and a-2 at 5", lines)
	}
	if got := FromLines(journalLines(t, r)); got.Sessions != 1 || got.Agents != 2 || got.Input != 355 {
		t.Errorf("ticket spend = %+v, want 1 session, 2 agents, 355 tokens (the session itself has no line)", got)
	}

	r.agentStop("a-9") // no transcript on disk
	if n := len(r.spendLines()); n != 3 {
		t.Errorf("%d spend lines after the stop of an agent with no transcript, want still 3", n)
	}
	r.where.Ticket = ""
	r.sayAgent("s-1", "a-2", 5)
	if got := r.agentStop("a-2"); got != nil {
		t.Errorf("notes for an agent placed at no ticket = %+v", got)
	}
	r.where.Ticket = "T-1"
	if n := len(r.spendLines()); n != 3 {
		t.Errorf("%d spend lines after the stop of an agent placed at no ticket, want still 3", n)
	}
	// A subagent's stop that names no session and no transcript has nothing to read.
	if _, err := Notes(observe.NoteCtx{
		Event: observe.Event{Kind: observe.KindStop, Agent: "a-1"}, Where: r.where, Store: r.st, JigHome: r.home, Now: r.now,
		Journal: func() ([]journal.Line, error) { return journal.Read(r.st, "T-1") },
	}); err != nil || len(r.spendLines()) != 3 {
		t.Errorf("a subagent stop with no transcript: err = %v, %d lines", err, len(r.spendLines()))
	}
}

// TestNotesLeaveOutTheAgentsWithALineOfTheirOwn: the session's line sums the
// main transcript and the subagent transcripts of the agents the machine
// ledger has no placed stop for. An agent with a hook line of the session, at
// a stop, naming a ticket, has a line of its own and is left out; one with no
// such line (still running, crashed, placed at no ticket) stays in, so its
// tokens are not lost.
func TestNotesLeaveOutTheAgentsWithALineOfTheirOwn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.say("s-1", 1000)
	r.sayAgent("s-1", "a-done", 100)
	r.sayAgent("s-1", "a-running", 10)
	r.sayAgent("s-1", "a-unplaced", 1)
	r.sayAgent("s-1", "a-other", 20000)

	// The ledger is the machine's: lines of other sessions and of other kinds
	// do not make an agent of this session own.
	r.stopped("s-1", "a-done", "T-2")
	r.stopped("s-1", "a-unplaced", "")
	r.stopped("s-2", "a-running", "T-1")
	r.stopped("s-2", "a-other", "T-1")
	if err := ledger.Append(r.home, journal.Line{Event: journal.EventTool, Session: "s-1", Agent: "a-running", Ticket: "T-1"}); err != nil {
		t.Fatal(err)
	}
	r.stopped("s-1", "", "T-1") // the session's own stop names no agent

	r.event(observe.KindStop)
	lines := r.spendLines()
	if len(lines) != 1 || lines[0].Agent != "" || lines[0].Spend.Input != 1000+10+1+20000 {
		t.Fatalf("spend lines = %+v, want the session's line at 21011: a-done has a line of its own (on T-2), the rest stay in", lines)
	}

	// a-running stops, placed at T-1: the session's next line takes it out.
	r.stopped("s-1", "a-running", "T-1")
	r.event(observe.KindSessionEnd)
	if lines = r.spendLines(); len(lines) != 2 || lines[1].Spend.Input != 1000+1+20000 {
		t.Errorf("spend lines = %+v, want the session end to write a second line at 21001, without a-running", lines)
	}
}

// TestNotesLeaveOutAnAgentFoundBeyondTheSessionStart: a session that was
// resumed or compacted starts again under the same id, and the lines its
// agents wrote earlier are still theirs, so the walk back does not stop at a
// session-start line.
func TestNotesLeaveOutAnAgentFoundBeyondTheSessionStart(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.say("s-1", 1000)
	r.sayAgent("s-1", "a-1", 100)
	r.stopped("s-1", "a-1", "T-1")
	if err := ledger.Append(r.home, journal.Line{Event: journal.EventSessionStart, Session: "s-1"}); err != nil {
		t.Fatal(err)
	}
	r.event(observe.KindStop)
	if lines := r.spendLines(); len(lines) != 1 || lines[0].Spend.Input != 1000 {
		t.Errorf("spend lines = %+v, want the session's line at 1000: a-1 stopped before the session started again", lines)
	}
}

// TestNotesNameSubagentsInTheBudgetNote: a subagent's stop hears the ticket's
// budget like any event, the sum includes the agents, and the note counts
// them: "(1 session, 2 subagents)", and "(1 session)" when there are none.
func TestNotesNameSubagentsInTheBudgetNote(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if err := WriteBudget(r.st, "T-1", project.Budget{Tokens: 1000}); err != nil {
		t.Fatal(err)
	}
	r.sayAgent("s-1", "a-1", 600)
	if got := r.agentStop("a-1"); len(got) != 0 {
		t.Fatalf("notes at 60%% of the budget = %+v, want none", got)
	}

	r.sayAgent("s-1", "a-1", 300)
	near := r.agentStop("a-1")
	if len(near) != 1 || !strings.Contains(near[0].Text, "T-1 has used 90% of its spend budget: 900 of 1k tokens (1 session, 1 subagent).") {
		t.Fatalf("notes after the agent stopped at 90%% = %+v, want the note to name 1 session and 1 subagent", near)
	}
	r.sayAgent("s-1", "a-2", 400)
	over := r.agentStop("a-2")
	if len(over) != 1 || !strings.Contains(over[0].Text, "T-1 is over its spend budget: 1.3k of 1k tokens (1 session, 2 subagents).") {
		t.Fatalf("notes after a second agent stopped = %+v, want an over note naming 1 session and 2 subagents", over)
	}
	// Any other event hears the same note.
	if again := r.event(observe.KindPrompt); len(again) != 1 || again[0].Ref != over[0].Ref {
		t.Errorf("a prompt heard %+v, want the same over note", again)
	}
}

func journalLines(t *testing.T, r *rig) []journal.Line {
	t.Helper()
	all, err := journal.Read(r.st, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// TestNotesWriteNothingWithoutSpendToSee: no transcript, a transcript not
// there yet, no session, no ticket and no store each leave the journal alone.
func TestNotesWriteNothingWithoutSpendToSee(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if got := r.eventAs(observe.KindStop, "s-1", ""); got != nil {
		t.Errorf("notes with no transcript = %+v", got)
	}
	if got := r.eventAs(observe.KindStop, "s-1", filepath.Join(t.TempDir(), "gone.jsonl")); got != nil {
		t.Errorf("notes with a transcript not written yet = %+v", got)
	}
	r.say("s-1", 10)
	r.where.Ticket = ""
	r.event(observe.KindStop)
	r.where.Ticket = "T-1"
	if _, err := Notes(observe.NoteCtx{Event: observe.Event{Kind: observe.KindStop, Session: "s-1", Transcript: r.transcript}, Where: r.where}); err != nil {
		t.Errorf("a source with no store: %v", err)
	}
	if n := len(r.spendLines()); n != 0 {
		t.Errorf("%d spend lines, want none", n)
	}
	// A session id missing from the event is the name of the transcript.
	r.eventAs(observe.KindStop, "", r.transcript)
	if lines := r.spendLines(); len(lines) != 1 || lines[0].Session != "s-1" {
		t.Errorf("spend lines = %+v, want one for session s-1 (named by the transcript)", lines)
	}
}

// TestNotesTellOfABudgetNearOrOver: a ticket at 80% of a limit is told so, and
// one past a limit is told so; neither is enforced, and the note is the same
// one at every kind of event until the state changes.
func TestNotesTellOfABudgetNearOrOver(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if err := WriteBudget(r.st, "T-1", project.Budget{Tokens: 1000}); err != nil {
		t.Fatal(err)
	}

	r.say("s-1", 500)
	if got := r.event(observe.KindStop); got != nil {
		t.Fatalf("notes at 50%% of the budget = %+v, want none", got)
	}

	r.say("s-1", 350)
	near := r.event(observe.KindStop)
	if len(near) != 1 || near[0].Enforce || !strings.Contains(near[0].Text, "T-1 has used 85% of its spend budget: 850 of 1k tokens (1 session).") ||
		!strings.Contains(near[0].Text, "Jig only reports spend; it stops nothing.") {
		t.Fatalf("notes at 85%% = %+v, want one advisory note saying so", near)
	}
	for _, kind := range []string{observe.KindPrompt, observe.KindTool} {
		again := r.event(kind)
		if len(again) != 1 || again[0].Ref != near[0].Ref {
			t.Errorf("%s event: notes = %+v, want the same note (ref %s)", kind, again, near[0].Ref)
		}
	}

	r.say("s-1", 650)
	over := r.event(observe.KindStop)
	if len(over) != 1 || over[0].Enforce || over[0].Ref == near[0].Ref ||
		!strings.Contains(over[0].Text, "T-1 is over its spend budget: 1.5k of 1k tokens (1 session).") {
		t.Fatalf("notes at 150%% = %+v, want an advisory note that is a new fact", over)
	}
	if got := r.event(observe.KindSessionEnd); got != nil {
		t.Errorf("a session end returned %+v: what it returns is never delivered, so it returns nothing", got)
	}

	// Raising the budget past the spend ends the note; passing the new limit
	// is news again.
	if err := WriteBudget(r.st, "T-1", project.Budget{Tokens: 1_000_000}); err != nil {
		t.Fatal(err)
	}
	if got := r.event(observe.KindPrompt); got != nil {
		t.Errorf("notes under a raised budget = %+v, want none", got)
	}
	if err := WriteBudget(r.st, "T-1", project.Budget{Tokens: 1000}); err != nil {
		t.Fatal(err)
	}
	if again := r.event(observe.KindPrompt); len(again) != 1 || again[0].Ref != over[0].Ref {
		t.Errorf("notes under the old budget again = %+v, want the same over note", again)
	}
	if err := WriteBudget(r.st, "T-1", project.Budget{Tokens: 1200}); err != nil {
		t.Fatal(err)
	}
	if again := r.event(observe.KindPrompt); len(again) != 1 || again[0].Ref == over[0].Ref {
		t.Errorf("notes under a budget of 1200 = %+v, want an over note with a ref of its own", again)
	}
}

// TestNotesHoldTheWholeTicketAgainstItsBudget: the spend of a ticket is every
// session on it, so a session that has spent nothing yet is told that the
// others took the budget, and a ticket with no budget record is told nothing.
func TestNotesHoldTheWholeTicketAgainstItsBudget(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.say("s-1", 700)
	r.event(observe.KindStop)
	if got := r.event(observe.KindPrompt); got != nil {
		t.Fatalf("notes for a ticket with no budget record = %+v, want none", got)
	}

	if err := WriteBudget(r.st, "T-1", project.Budget{Tokens: 1000}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "s-2.jsonl")
	r.sayTo(other, "s-2", 600)

	// s-2 has nothing in the journal yet and hears nothing it caused: at a
	// stop it writes its own line, and the two are over together.
	over := r.eventAs(observe.KindStop, "s-2", other)
	if len(over) != 1 || !strings.Contains(over[0].Text, "is over its spend budget: 1.3k of 1k tokens (2 sessions).") {
		t.Fatalf("notes after s-2 stopped = %+v, want over the budget with two sessions", over)
	}
	if got := FromLines(journalLines(t, r)); got.Sessions != 2 || got.Input != 1300 {
		t.Errorf("ticket spend = %+v, want 2 sessions and 1300 tokens", got)
	}
	// A third session that has a prompt before it spends anything hears it.
	if heard := r.eventAs(observe.KindPrompt, "s-3", ""); len(heard) != 1 || heard[0].Ref != over[0].Ref {
		t.Errorf("a session that spent nothing heard %+v, want the over note", heard)
	}
}

// TestNotesNameWhatCannotBePriced: a limit in dollars is held against the
// dollars that could be counted, and the note says which models were left out.
func TestNotesNameWhatCannotBePriced(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if err := WriteBudget(r.st, "T-1", project.Budget{USD: 0.01}); err != nil {
		t.Fatal(err)
	}
	// 10000 input tokens of sonnet 4.5 cost $0.03; fable has no price.
	writeLines(t, r.transcript,
		asst("s-1", "a", "claude-sonnet-4-5", 10000, 0, 0, 0, 0),
		asst("s-1", "b", "claude-fable-5", 500, 0, 0, 0, 0),
	)
	over := r.event(observe.KindStop)
	if len(over) != 1 || !strings.Contains(over[0].Text, "T-1 is over its spend budget: $0.03 of $0.01 (1 session).") ||
		!strings.Contains(over[0].Text, "No price for claude-fable-5: its dollars are not counted.") {
		t.Fatalf("notes = %+v, want an over note that names claude-fable-5 as unpriced", over)
	}
	if lines := r.spendLines(); len(lines) != 1 || lines[0].Summary != "unpriced: claude-fable-5" || lines[0].Spend.Input != 10500 {
		t.Errorf("spend lines = %+v, want the unpriced model named in the summary and its tokens counted", lines)
	}
}

// TestNotesRecordTheProjectDefaultBudgetOnTheTicket: the first spend of a
// ticket with no budget record records the default of the project marker as
// the ticket budget; a ticket that was given none on purpose keeps none.
func TestNotesRecordTheProjectDefaultBudgetOnTheTicket(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	root := t.TempDir()
	if err := project.WriteMarker(root, project.Marker{Project: "p", Budget: &project.Budget{Tokens: 1000, USD: 5}}); err != nil {
		t.Fatal(err)
	}
	r.where.Root = root

	r.event(observe.KindPrompt)
	if _, recorded, _ := ReadBudget(r.st, "T-1"); recorded {
		t.Fatal("a prompt recorded the budget: only the first recorded spend does")
	}
	r.say("s-1", 400)
	r.event(observe.KindStop)
	if b, recorded, err := ReadBudget(r.st, "T-1"); err != nil || !recorded || b != (project.Budget{Tokens: 1000, USD: 5}) {
		t.Fatalf("ticket budget = %+v, recorded = %v, err = %v, want the project default", b, recorded, err)
	}

	// Cleared on purpose: an empty record stays empty.
	if err := WriteBudget(r.st, "T-1", project.Budget{}); err != nil {
		t.Fatal(err)
	}
	r.say("s-1", 400)
	if got := r.event(observe.KindStop); got != nil {
		t.Errorf("notes = %+v, want none for a ticket given no budget on purpose", got)
	}
	if b, _, _ := ReadBudget(r.st, "T-1"); Limited(b) {
		t.Errorf("ticket budget = %+v, want the empty record kept", b)
	}

	// No marker, no default.
	r2 := newRig(t)
	r2.say("s-1", 400)
	r2.event(observe.KindStop)
	if _, recorded, _ := ReadBudget(r2.st, "T-1"); recorded {
		t.Error("a budget was recorded for a project with no default")
	}
}
