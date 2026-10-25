package correct

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func TestCheckFindsNothingInAQuietTicket(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: "c1"})
	w.fail("b", "x", 1)
	notes, err := Check(w.input())
	if err != nil || len(notes) != 0 {
		t.Errorf("Check = %+v, %v, want no note and no error", notes, err)
	}
	if got := w.corrections(); len(got) != 0 {
		t.Errorf("a quiet ticket got correction lines: %+v", got)
	}
}

func TestCheckFiresAStallOnceAndOffersItAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fail("a", "sig", 1)
	w.fail("a", "sig", 2)

	notes, err := Check(w.input())
	if err != nil || len(notes) != 1 || notes[0].Kind != KindStall || !strings.HasPrefix(notes[0].Ref, "stall:a:") {
		t.Fatalf("Check = %+v, %v, want the one stall of slice a", notes, err)
	}
	for _, want := range []string{"slice a failed its oracle the same way 2 times", "attempt 2", "FAIL sig", "change the approach"} {
		if !strings.Contains(notes[0].Text, want) {
			t.Errorf("the stall text %q lacks %q", notes[0].Text, want)
		}
	}
	if strings.Contains(notes[0].Text, "jig requeue") {
		t.Errorf("the stall text %q offers a requeue to a session that is watched: the slice is not stalled", notes[0].Text)
	}
	lines := w.corrections()
	if len(lines) != 1 {
		t.Fatalf("correction lines = %+v, want one", lines)
	}
	l := lines[0]
	if l.Ref != notes[0].Ref || l.Outcome != KindStall || l.Slice != "a" || l.Session != "s-1" || l.Harness != "claude" || l.Provenance != "jig" ||
		l.Signature != "sig" || l.Commit == "" || l.Summary != notes[0].Text || l.Ticket != "JIG-1" {
		t.Errorf("correction line = %+v, want the stall of slice a by session s-1, as jig, with the note text", l)
	}
	if got := w.sliceState("a"); got.State != "queued" || got.Reason != "" {
		t.Errorf("slice a = %+v after a stall seen by a person at the keyboard, want it left alone", got)
	}

	again, err := Check(w.input())
	if err != nil || !reflect.DeepEqual(again, notes) {
		t.Errorf("Check again = %+v, %v, want the same note", again, err)
	}
	if got := w.corrections(); len(got) != 1 {
		t.Errorf("correction lines after the second Check = %d, want still one", len(got))
	}
}

func TestCheckMarksTheSliceStalledOnlyOnceAndOnlyWhenUnattended(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fail("a", "sig", 1)
	w.fail("a", "sig", 2)
	in := w.input()
	in.Unattended = true

	notes, err := Check(in)
	if err != nil || len(notes) != 1 {
		t.Fatalf("Check = %+v, %v, want the stall", notes, err)
	}
	ss := w.sliceState("a")
	if ss.State != "stalled" || ss.Reason != "stall" || ss.Signature != "sig" || ss.StallSummary != "FAIL sig" || ss.Attempts != 2 {
		t.Errorf("slice a = %+v, want stalled for a stall, with the signature, the summary and 2 attempts", ss)
	}
	if want := "jig requeue JIG-1 --slice a"; !strings.Contains(notes[0].Text, want) {
		t.Errorf("the stall text %q lacks the remedy %q", notes[0].Text, want)
	}

	// The session claims the slice again: the fact has not changed, and the
	// state is not taken from it a second time.
	if err := w.st.WriteSliceState("JIG-1", "a", store.SliceState{State: "building", Attempts: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(in); err != nil {
		t.Fatal(err)
	}
	if got := w.sliceState("a"); got.State != "building" {
		t.Errorf("slice a = %+v after a second Check, want the claim left alone", got)
	}

	// A slice that waits on an answer keeps waiting.
	w2 := newWorld(t)
	w2.fail("a", "sig", 1)
	w2.fail("a", "sig", 2)
	if err := w2.st.WriteSliceState("JIG-1", "a", store.SliceState{State: "needs-input", Question: "q-001"}); err != nil {
		t.Fatal(err)
	}
	in2 := w2.input()
	in2.Unattended = true
	if _, err := Check(in2); err != nil {
		t.Fatal(err)
	}
	if got := w2.sliceState("a"); got.State != "needs-input" || got.Question != "q-001" {
		t.Errorf("slice a = %+v, want it still waiting on q-001", got)
	}
}

func TestCheckLeavesAStallThatIsNotOfThisSession(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fail("a", "sig", 1)
	w.fail("a", "sig", 2)

	in := w.input()
	in.Since = time.Now().Add(time.Hour)
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Errorf("a session that began after the failures: %+v, %v, want nothing", notes, err)
	}
	in.Since = time.Time{}
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Errorf("a session whose start is unknown: %+v, %v, want nothing: nothing is known to be its own", notes, err)
	}
	if got := w.corrections(); len(got) != 0 {
		t.Errorf("correction lines = %+v, want none: the fact is no fact of this session", got)
	}

	// Its own next failure of the same kind is the third, and is its own.
	w.fail("a", "sig", 3)
	in.Since = w.start.Add(time.Duration(w.n) * time.Second)
	notes, err := Check(in)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0].Text, "3 times") {
		t.Errorf("Check = %+v, %v, want the stall, counting the failures before the session too", notes, err)
	}
}

func TestCheckTellsTheSessionAgainWhenItGoesOnFailing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fail("a", "sig", 1)
	w.fail("a", "sig", 2)
	first, err := Check(w.input())
	if err != nil || len(first) != 1 {
		t.Fatalf("Check = %+v, %v", first, err)
	}
	w.fail("a", "sig", 3)
	second, err := Check(w.input())
	if err != nil || len(second) != 1 || second[0].Ref == first[0].Ref || !strings.Contains(second[0].Text, "3 times") {
		t.Errorf("after a third failure Check = %+v, %v, want a new note", second, err)
	}
	if got := w.corrections(); len(got) != 2 {
		t.Errorf("correction lines = %d, want one for each repeat", len(got))
	}
}

func TestCheckFindsABriefDriftWhoeverAsks(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: "c1"})
	in := w.input()
	in.Since = time.Time{} // a drift is a state of the ticket, not an event of a session

	w.writeBrief(strings.Replace(testBrief, "Add a caller.", "Add two callers.", 1))
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Fatalf("the brief section of a slice nobody verified changed: %+v, %v, want nothing", notes, err)
	}

	w.writeBrief(strings.Replace(testBrief, "Fix Clamp.", "Fix Clamp and its callers.", 1))
	notes, err := Check(in)
	if err != nil || len(notes) != 1 || notes[0].Kind != KindBrief || !strings.HasPrefix(notes[0].Ref, "brief:") ||
		!strings.Contains(notes[0].Text, "slice a cites 1 section") || !strings.Contains(notes[0].Text, "jig requeue JIG-1 --from-brief-diff") {
		t.Fatalf("Check = %+v, %v, want the drift of slice a", notes, err)
	}
	lines := w.corrections()
	if len(lines) != 1 || lines[0].Outcome != KindBrief || lines[0].Ref != notes[0].Ref || lines[0].Slice != "" {
		t.Errorf("correction lines = %+v, want the one drift, on the ticket", lines)
	}
	if again, err := Check(in); err != nil || !reflect.DeepEqual(again, notes) || len(w.corrections()) != 1 {
		t.Errorf("Check again = %+v, %v with %d lines, want the same note and no new line", again, err, len(w.corrections()))
	}

	// Requeued, the slice is built again: the drift is the new work to do.
	w.line(journal.Line{Slice: "a", Event: journal.EventRequeue})
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Errorf("after the requeue Check = %+v, %v, want nothing", notes, err)
	}
	// A ticket with no brief has nothing to drift from.
	if err := os.Remove(filepath.Join(w.st.TicketDir("JIG-1"), "brief.md")); err != nil {
		t.Fatal(err)
	}
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: "c2"})
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Errorf("with no brief Check = %+v, %v, want nothing", notes, err)
	}
}

func TestCheckScopeDriftIsOfferedAgainToTheSessionThatMadeIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	in := w.input()
	// The session works on slice a, which names it for the commits it makes.
	in.Slice = "a"

	in.Commit = w.commitFiles("inside", "alpha/a.go")
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Fatalf("a commit inside the workspaces: %+v, %v, want nothing", notes, err)
	}

	in.Commit = w.commitFiles("outside", "alpha/b.go", "docs/readme.md")
	notes, err := Check(in)
	if err != nil || len(notes) != 1 || notes[0].Kind != KindScope || notes[0].Ref != "scope:"+in.Commit[:12] || !strings.Contains(notes[0].Text, "docs/readme.md") {
		t.Fatalf("Check = %+v, %v, want the scope drift of the commit", notes, err)
	}
	lines := w.corrections()
	if len(lines) != 1 || lines[0].Outcome != KindScope || lines[0].Commit != in.Commit || lines[0].Slice != "a" || lines[0].Session != "s-1" ||
		!reflect.DeepEqual(lines[0].Files, []string{"docs/readme.md"}) || lines[0].Summary != notes[0].Text {
		t.Errorf("correction lines = %+v, want the one drift, with the commit, the file and the text", lines)
	}

	// At a later event, with no commit of its own, the session is offered it again.
	later := w.input()
	again, err := Check(later)
	if err != nil || !reflect.DeepEqual(again, notes) {
		t.Errorf("a later event: %+v, %v, want the same note", again, err)
	}
	if len(w.corrections()) != 1 {
		t.Errorf("correction lines = %d, want still one", len(w.corrections()))
	}
	// Another session was not the one that made the commit.
	other := w.input()
	other.Session = "s-2"
	if got, err := Check(other); err != nil || len(got) != 0 {
		t.Errorf("another session: %+v, %v, want nothing", got, err)
	}
	// Nor is a session that began after it.
	late := w.input()
	late.Since = time.Now().Add(time.Hour)
	if got, err := Check(late); err != nil || len(got) != 0 {
		t.Errorf("a session that began later: %+v, %v, want nothing", got, err)
	}
}

func TestCheckKeepsTheOtherNotesWhenADetectorFails(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fail("a", "sig", 1)
	w.fail("a", "sig", 2)
	w.write(".claude/jig.yaml", "workspaces: [not, a, list of maps\n")
	in := w.input()
	in.Commit = w.commit("break the manifest", ".claude/jig.yaml")

	notes, err := Check(in)
	if err == nil {
		t.Error("Check = no error, want the manifest that does not parse")
	}
	if len(notes) != 1 || notes[0].Kind != KindStall {
		t.Errorf("Check = %+v, want the stall all the same", notes)
	}
}

func TestCheckFailsWhenTheJournalCannotBeRead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	in := w.input()
	boom := errors.New("boom")
	in.Journal = func() ([]journal.Line, error) { return nil, boom }
	if notes, err := Check(in); !errors.Is(err, boom) || notes != nil {
		t.Errorf("Check = %+v, %v, want the error and no note", notes, err)
	}
}

func TestCheckDoesNotReturnAFactItCouldNotRecord(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	lines := []journal.Line{
		{Slice: "a", Event: journal.EventOracle, Outcome: "fail", Signature: "sig", Attempt: 1, TS: w.start.Add(time.Second).Format(time.RFC3339Nano)},
		{Slice: "a", Event: journal.EventOracle, Outcome: "fail", Signature: "sig", Attempt: 2, TS: w.start.Add(2 * time.Second).Format(time.RFC3339Nano)},
	}
	in := w.input()
	in.Journal = func() ([]journal.Line, error) { return lines, nil }

	// A directory where the journal file goes: the line cannot be written.
	path := filepath.Join(w.st.TicketDir("JIG-1"), "journal.ndjson")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if notes, err := Check(in); err == nil || len(notes) != 0 {
		t.Errorf("Check = %+v, %v, want the error and no note: the next call finds the fact again", notes, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	notes, err := Check(in)
	if err != nil || len(notes) != 1 || len(w.corrections()) != 1 {
		t.Errorf("Check once the journal can be written = %+v, %v with %d lines, want the note and its line", notes, err, len(w.corrections()))
	}
}
