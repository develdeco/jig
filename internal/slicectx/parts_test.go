package slicectx

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestFailedAttemptsKeepTheLatestEight: a long history lists the latest
// MaxFailedAttempts, oldest of them first.
func TestFailedAttemptsKeepTheLatestEight(t *testing.T) {
	var lines []journal.Line
	for i := 1; i <= 11; i++ {
		lines = append(lines,
			journal.Line{Slice: "b", Event: journal.EventOracle, Outcome: "fail", Attempt: i},
			journal.Line{Slice: "other", Event: journal.EventOracle, Outcome: "fail", Attempt: i},
			journal.Line{Slice: "b", Event: journal.EventOracle, Outcome: "pass", Attempt: i})
	}
	got := failedAttempts(lines, "b")
	if len(got) != MaxFailedAttempts || got[0].Attempt != 4 || got[len(got)-1].Attempt != 11 {
		t.Errorf("failed attempts = %+v, want %d from 4 to 11", got, MaxFailedAttempts)
	}
}

// TestBuildListsTheQuestionsAndNotesOfTheSlice: the questions about the
// slice, with the answer and who it speaks for, and the notes on the slice
// and on the whole ticket - not those of another slice.
func TestBuildListsTheQuestionsAndNotesOfTheSlice(t *testing.T) {
	w := newWorld(t)
	if _, err := w.st.AddQuestion("JIG-1", "b", "Percent or ratio?"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.st.AddQuestion("JIG-1", "a", "Other slice question"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.st.AnswerAs("JIG-1", "q-001", "Percent.", "person"); err != nil {
		t.Fatal(err)
	}
	w.line(journal.Line{Slice: "b", Event: journal.EventNote, Summary: "use the helper in util", Session: "s-1"})
	w.line(journal.Line{Slice: "a", Event: journal.EventNote, Summary: "belongs to a"})
	w.line(journal.Line{Event: journal.EventNote, Summary: "whole ticket note"})

	c, err := Build(w.input("b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Questions) != 1 || c.Questions[0] != (Question{ID: "q-001", Status: "answered", Body: "Percent or ratio?", Answer: "Percent.", Provenance: "person"}) {
		t.Errorf("questions = %+v, want the one answered question of b", c.Questions)
	}
	if len(c.Notes) != 2 || c.Notes[0].Text != "use the helper in util" || c.Notes[0].Session != "s-1" || c.Notes[1].Text != "whole ticket note" {
		t.Errorf("notes = %+v, want the note of b then the ticket note", c.Notes)
	}
	text := strings.Join(c.Blocks(), "\n")
	for _, want := range []string{
		"questions[1]{id,status,question,answer,provenance}:\n  q-001,answered,Percent or ratio?,Percent.,person\n",
		"notes[2]{ts,slice,session,note}:\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("blocks lack %q:\n%s", want, text)
		}
	}
}

// TestBlocksRenderEveryPartAndLeaveOutTheEmptyOnes pins the text register:
// the context block first, the brief sections as indented markdown, a table
// for each part that has rows, and nothing for one that has none.
func TestBlocksRenderEveryPartAndLeaveOutTheEmptyOnes(t *testing.T) {
	w := newWorld(t)
	c, err := Build(w.input("b"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(c.Blocks(), "\n")
	for _, want := range []string{
		"context:\n  ticket: JIG-1\n  slice: b\n  goal: Add a caller.\n  workspace: alpha\n  oracle: test\n",
		"  env: rig\n  state: queued\n  blocked_by: a\n  attempt: 1\n  oracle_seconds: 0\n",
		"  stale_brief_sections: 1\n",
		"brief_sections[1]:\n  ## Slice B - callers\n\n  Add a caller.\n\n  With detail.\n",
		"seams:\n  ## Seams\n\n  - the verb",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("blocks lack %q:\n%s", want, text)
		}
	}
	for _, absent := range []string{"earlier_slices", "failed_attempts", "related", "questions", "notes"} {
		if strings.Contains(text, absent+"[") {
			t.Errorf("blocks carry an empty %s part:\n%s", absent, text)
		}
	}
}
