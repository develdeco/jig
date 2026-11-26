package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// onLog tabs to the Activity pane and puts its cursor on the first line whose
// row says ok; it fails when no line does.
func (g *rig) onLog(ok func(rowRef) bool) rowRef {
	g.t.Helper()
	g.tabTo("Activity")
	full := g.m.page().windowed["Activity"]
	for i, r := range full {
		if ok(r) {
			e := g.m.top()
			e.paneCurs["Activity"] = paneCursor{id: r.identity(), idx: i, set: true}
			got := g.m.selected()
			if got == nil || *got != r {
				g.t.Fatalf("the cursor is on %+v, want the line %+v", got, r)
			}
			return r
		}
	}
	g.t.Fatalf("no line of the activity matches; its %d rows are %+v", len(full), full)
	return rowRef{}
}

// TestTheActivityPaneHoldsEveryLineAndKeepsItsCursor: the pane has a row for each
// line of the log, the keys reach the oldest, a line that comes in above the
// cursor does not move it, and a cursor whose line went away stays at its place.
func TestTheActivityPaneHoldsEveryLineAndKeepsItsCursor(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 9, 6, 80, 26)
	g.tabTo("Activity")
	full := g.m.page().windowed["Activity"]
	if len(full) < 15 {
		t.Fatalf("the pane holds %d rows, want a line for every one of the log's", len(full))
	}
	if v := g.view(); !strings.Contains(v, fmt.Sprintf("latest %d of %d", strings.Count(strings.Join(panelLines(strings.Split(v, "\n"), "Activity"), "\n"), "\n")-3, len(full))) &&
		!strings.Contains(v, "of "+fmt.Sprint(len(full))) {
		t.Errorf("the title does not count the lines:\n%s", v)
	}
	first := g.m.selected()
	if first == nil || first.kind != rowLog || first.id != full[0].id {
		t.Fatalf("the pane's cursor begins on %+v, want the newest line %+v", first, full[0])
	}
	g.press("down", "down", "down")
	mid := g.m.selected()
	if mid == nil || mid.id != full[3].id {
		t.Fatalf("three downs reach %+v, want the fourth line %+v", mid, full[3])
	}
	// A line comes in above the cursor: the cursor stays on its line.
	g.feed(jl(at(20, 0), "BIG-1", "s6", journal.EventVerifyPending, with("s-builder", sha('9'), "", "", "", "")))
	if got := g.m.selected(); got == nil || got.id != mid.id {
		t.Errorf("a new line moved the cursor to %+v, want it on %+v", got, mid)
	}
	if now := g.m.page().windowed["Activity"]; len(now) != len(full)+1 || now[0].id == full[0].id {
		t.Errorf("the new line is not the pane's first row: %d rows", len(now))
	}
	// The oldest line is reached, and the window follows with the count of the
	// newer ones.
	sendKey(g, "pgdown")
	g.press("end")
	last := g.m.selected()
	now := g.m.page().windowed["Activity"]
	if last == nil || last.id != now[len(now)-1].id {
		t.Fatalf("end reaches %+v, want the oldest line %+v", last, now[len(now)-1])
	}
	if v := g.view(); !strings.Contains(v, "↑ ") || !strings.Contains(v, "newer") {
		t.Errorf("the window at the oldest line does not count the newer ones:\n%s", v)
	}
	// A cursor on a line that went away stays at its place, clamped.
	e := g.m.top()
	e.paneCurs["Activity"] = paneCursor{id: rowIdentity{kind: rowLog, id: "gone"}, idx: 5, set: true}
	if got := g.m.selected(); got == nil || got.id != now[5].id {
		t.Errorf("the cursor of a line that went away is on %+v, want the line at its place, %+v", got, now[5])
	}
	e.paneCurs["Activity"] = paneCursor{id: rowIdentity{kind: rowLog, id: "gone"}, idx: 1 << 20, set: true}
	if got := g.m.selected(); got == nil || got.id != now[len(now)-1].id {
		t.Errorf("a place past the end clamps to %+v, want the oldest line", got)
	}
}

// TestAnActivityLineOpensWhatItIsAbout: a line of the log opens the most
// specific thing it names: its slice, its round, its step; a line that names
// none opens nothing and says so.
func TestAnActivityLineOpensWhatItIsAbout(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 44})
	g.feed(jig2()...)
	g.feed(jl(at(15, 0), "JIG-2", "", journal.EventSpend))

	g.onLog(func(r rowRef) bool { return r.round != "" })
	g.press("enter")
	if g.m.Screen() != "review" {
		t.Errorf("a line of a round opens %s, want the review screen", g.m.Screen())
	}
	g.press("esc")
	if got := g.m.focusedPane(); got != "Activity" {
		t.Errorf("back from the review the focus is on %q, want Activity", got)
	}

	g.onLog(func(r rowRef) bool { return r.slice == "a" })
	g.press("enter")
	if g.m.Screen() != "slice" || g.m.top().slice != "a" {
		t.Errorf("a line of a slice opens %s %q, want the slice screen of a", g.m.Screen(), g.m.top().slice)
	}
	g.press("esc")

	g.onLog(func(r rowRef) bool { return r.slice == "" && r.round == "" && r.container == "publish" })
	g.press("enter")
	if g.m.Screen() != "publish" {
		t.Errorf("a line of the publish step that names no round opens %s, want the publish screen", g.m.Screen())
	}
	g.press("esc")

	line := g.onLog(func(r rowRef) bool { return r.slice == "" && r.round == "" && r.container == "" })
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.notice != logNamesNothing {
		t.Errorf("a line that names nothing (%+v) opens %s with the notice %q", line, g.m.Screen(), g.m.notice)
	}
}

// TestAnOpenQuestionOpensTheAnswerPrompt: a question opens its answer prompt
// while it is open, in Needs you and as a line of the log alike, and its slice
// once it is answered.
func TestAnOpenQuestionOpensTheAnswerPrompt(t *testing.T) {
	t.Parallel()
	g := newQuietRig(t, "JIG-1")
	g.feed(jig1()...)
	g.tabTo("Needs you").press("enter")
	if g.m.input == nil || g.m.input.qid != "q-001" || g.m.Screen() != "ticket" {
		t.Fatalf("enter on the open question: input %+v on %s, want the prompt for q-001 on the ticket", g.m.input, g.m.Screen())
	}
	g.press("esc")
	if g.m.input != nil {
		t.Fatalf("esc did not close the prompt")
	}
	g.onLog(func(r rowRef) bool { return r.question == "q-001" })
	g.press("enter")
	if g.m.input == nil || g.m.input.qid != "q-001" {
		t.Fatalf("enter on the question's line: input %+v, want the prompt for q-001", g.m.input)
	}
	g.press("esc")

	// Answered, it opens its slice.
	a := newQuietRig(t, "JIG-1")
	a.r.answer("status JIG-1 --store /s", strings.Replace(statusJIG1(), "q-001,c,open", "q-001,c,answered", 1), 0)
	a.feed(append(jig1(), jl(at(3, 0), "JIG-1", "c", journal.EventAnswer, with("s-person", "", "", "Casual", "q-001", "person")))...)
	g = a
	g.onLog(func(r rowRef) bool { return r.question == "q-001" })
	g.press("enter")
	if g.m.input != nil {
		t.Fatalf("enter on an answered question opened a prompt: %+v", g.m.input)
	}
	if g.m.Screen() != "slice" || g.m.top().slice != "c" {
		t.Errorf("enter on an answered question opens %s %q, want the slice screen of c", g.m.Screen(), g.m.top().slice)
	}
}
