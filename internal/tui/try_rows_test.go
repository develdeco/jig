package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// longSubject is a commit subject that wraps in the narrowest panel.
const longSubject = "close every handle on every path before the error goes back to the caller"

// fiveTries is JIG-1 with its slice a checked five times, each commit with a
// subject: the build screen lists the last three and counts the two before them.
func fiveTries(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "5", "-", "-", "Greet", "app", "unit", "-"}), 0)
	var lines []events.Envelope
	for i, d := range []byte("12345") {
		lines = append(lines,
			jl(at(i, 30), "JIG-1", "a", journal.EventOracle, with("", sha(d), "fail", "boom "+string(d), "", ""), func(l *journal.Line) {
				l.Command, l.Seconds, l.Subject = "go test ./...", 30, fmt.Sprintf("attempt %d: %s", i+1, longSubject)
			}))
	}
	g.feed(lines...).press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want the build screen", g.m.Screen())
	}
	return g.sized(w, h)
}

// TestATryIsARowOnTheSliceScreenWithItsSubjectWhole: each commit of the slice is a
// row of its Commits panel, newest first, and its subject is wrapped, never cut,
// at the floor and at the common size; enter on it opens nothing and says why.
func TestATryIsARowOnTheSliceScreenWithItsSubjectWhole(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {100, 30}} {
		g := fiveTries(t, size[0], size[1])
		g.cursorOn(rowSlice, "a")
		g.press("enter")
		if g.m.Screen() != "slice" {
			t.Fatalf("%dx%d: on %s, want the slice", size[0], size[1], g.m.Screen())
		}
		var tries []string
		for _, r := range g.m.page().refs() {
			if r.kind == rowTry {
				tries = append(tries, r.id)
			}
		}
		if len(tries) != 5 || tries[0] != sha('5') || tries[4] != sha('1') {
			t.Fatalf("%dx%d: the try rows are %v, want the five commits newest first", size[0], size[1], tries)
		}
		g.cursorOn(rowTry, sha('5'))
		commits := readWords(between(g.view(), "╭─ Commits", "╰"))
		whole := true
		for _, w := range strings.Fields("attempt 5: " + longSubject) {
			whole = whole && strings.Contains(commits, w)
		}
		if !whole {
			t.Errorf("%dx%d: the newest try's subject is cut:\n%s", size[0], size[1], g.view())
		}
		g.press("enter")
		if g.m.Screen() != "slice" || !strings.Contains(g.m.notice, "whole here") {
			t.Errorf("%dx%d: enter on a try opened %s, notice %q, want nothing and the reason", size[0], size[1], g.m.Screen(), g.m.notice)
		}
	}
}

// TestATryInTheBuildDetailOpensTheSliceScreenOnIt: a commit the build screen's
// Detail lists is a row; enter opens the slice screen with the Commits pane
// focused and its cursor on that commit.
func TestATryInTheBuildDetailOpensTheSliceScreenOnIt(t *testing.T) {
	t.Parallel()
	g := fiveTries(t, 100, 50)
	g.cursorOn(rowSlice, "a")
	g.tabTo("Detail · a")
	if ref := g.m.selected(); ref == nil || ref.kind != rowTry || ref.id != sha('5') {
		t.Fatalf("tab into the Detail puts the cursor on %+v, want the newest try", ref)
	}
	g.press("down", "down")
	if ref := g.m.selected(); ref == nil || ref.kind != rowTry {
		t.Fatalf("two downs put the cursor on %+v, want a try", ref)
	}
	want := g.m.selected().id
	g.press("enter")
	if g.m.Screen() != "slice" || g.m.top().slice != "a" {
		t.Fatalf("enter opened %s %q, want the slice a", g.m.Screen(), g.m.top().slice)
	}
	if got := g.m.focusedPane(); got != "Commits" {
		t.Errorf("the focused pane is %q, want Commits", got)
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowTry || ref.id != want {
		t.Errorf("the cursor is on %+v, want the try %s", ref, want)
	}
	g.press("esc")
	if g.m.Screen() != "build" {
		t.Errorf("esc went to %s, want the build", g.m.Screen())
	}
}

// TestEarlierCommitsIsARowThatOpensTheSliceOnTheNewestOfThem: the count of the
// commits the Detail left out is a row, and enter opens the slice screen with
// the cursor on the newest of them.
func TestEarlierCommitsIsARowThatOpensTheSliceOnTheNewestOfThem(t *testing.T) {
	t.Parallel()
	g := fiveTries(t, 100, 50)
	g.cursorOn(rowSlice, "a")
	if !strings.Contains(readWords(between(g.view(), "╭─ Detail · a", "╰")), "+2 earlier commits") {
		t.Fatalf("the Detail does not count the earlier commits:\n%s", g.view())
	}
	g.tabTo("Detail · a")
	g.press("end")
	ref := g.m.selected()
	if ref == nil || ref.kind != rowTry || ref.container != "earlier" || ref.id != sha('2') {
		t.Fatalf("the last row of the Detail is %+v, want the earlier-commits row naming the newest of them", ref)
	}
	g.press("enter")
	if g.m.Screen() != "slice" || g.m.focusedPane() != "Commits" {
		t.Fatalf("enter opened %s with %q focused", g.m.Screen(), g.m.focusedPane())
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowTry || ref.id != sha('2') {
		t.Errorf("the cursor is on %+v, want the commit 2222222", ref)
	}
}

// TestAnAnsweredQuestionIsARowShownWholeWithItsAnswer: a question that was answered
// is a row of the slice screen's Questions pane and of the build Detail, with its
// text and its answer whole and who gave it.
func TestAnAnsweredQuestionIsARowShownWholeWithItsAnswer(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {100, 30}} {
		g := newRig(t, "JIG-1")
		g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
		g.feed(jig1Records()...).press("enter").sized(size[0], size[1])
		g.cursorOn(rowSlice, "a")
		detail := readWords(between(g.view(), "╭─ Detail · a", "╰"))
		for _, want := range []string{"q-000 Trim the name?", "Yes, trim it. · answered by you"} {
			if size[0] > 60 && !strings.Contains(detail, want) {
				t.Errorf("%dx%d: the build Detail lacks %q:\n%s", size[0], size[1], want, g.view())
			}
		}
		g.press("enter")
		if g.m.Screen() != "slice" {
			t.Fatalf("%dx%d: on %s, want the slice", size[0], size[1], g.m.Screen())
		}
		g.cursorOn(rowQuestion, "q-000")
		questions := readWords(between(g.view(), "╭─ Questions", "╰"))
		if !strings.Contains(questions, "q-000") || !strings.Contains(questions, "answered by you") ||
			!strings.Contains(questions, "Trim the name?") || !strings.Contains(questions, "answer: Yes, trim it.") {
			t.Errorf("%dx%d: the answered question is not whole in its row:\n%s", size[0], size[1], g.view())
		}
	}
}

// TestAQuestionInTheBuildDetailOpensWhatTheOpenFunctionSaysOfIt: enter on an open
// question of the build Detail is the prompt that answers it, and on an answered
// one the slice screen, as the one function that says what a row opens has it.
func TestAQuestionInTheBuildDetailOpensWhatTheOpenFunctionSaysOfIt(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.feed(jig1Records()...).press("enter").sized(100, 50)

	g.cursorOn(rowSlice, "c")
	g.tabTo("Detail · c")
	if ref := g.m.selected(); ref == nil || ref.kind != rowQuestion || ref.id != "q-001" {
		t.Fatalf("the Detail of slice c puts the cursor on %+v, want its open question", ref)
	}
	g.press("enter")
	if g.m.input == nil || g.m.input.qid != "q-001" {
		t.Fatalf("enter on an open question opened %+v, want the prompt that answers it", g.m.input)
	}
	g.press("esc")

	g.cursorOn(rowSlice, "a")
	g.tabTo("Detail · a")
	if ref := g.m.selected(); ref == nil || ref.kind != rowQuestion || ref.id != "q-000" {
		t.Fatalf("the Detail of slice a puts the cursor on %+v, want its answered question", ref)
	}
	g.press("enter")
	if g.m.Screen() != "slice" || g.m.top().slice != "a" {
		t.Errorf("enter on an answered question opened %s %q, want the slice a", g.m.Screen(), g.m.top().slice)
	}
}
