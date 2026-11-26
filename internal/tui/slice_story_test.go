package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// openSlice opens the slice screen of slice id on a ticket whose status and lines
// are given, through the build screen as a person does.
func openSlice(t *testing.T, status string, lines []events.Envelope, id string, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", status, 0)
	g.feed(lines...).press("enter").sized(w, h)
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	g.focus(id).press("enter")
	if g.m.Screen() != "slice" || g.m.top().slice != id {
		t.Fatalf("on %s of slice %q, want the screen of slice %s:\n%s", g.m.Screen(), g.m.top().slice, id, g.view())
	}
	return g
}

// TestSliceScreenDrawsGoalOracleAndFactsWhole: a long goal and a long oracle are
// each drawn whole and wrapped at 60x20 and at 100x30, no row is wider than the
// screen, and a fact with nothing to say has no row.
func TestSliceScreenDrawsGoalOracleAndFactsWhole(t *testing.T) {
	t.Parallel()
	goal := "Greeting rewrites the welcome copy so every visitor is named by first name and falls back to friend only when nothing is on file, then trims the name"
	oracle := "go test -count=1 ./internal/greeting -run 'TestNamesTheVisitor|TestFallsBackToFriend|TestTrimsTheName'"
	status := strings.Join([]string{
		"ticket: JIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate", "findings"}, [][]string{
			{"a", "building", "2", "-", "-", goal, "-", oracle, "-", "-"},
		}),
		"questions: none",
		axi.Help("Run `jig next JIG-1`"),
	}, "\n") + "\n"
	for _, size := range [][2]int{{60, 20}, {100, 30}} {
		w, h := size[0], size[1]
		g := openSlice(t, status, nil, "a", w, h)
		// A word wider than the room is cut where the line ends, so the words are
		// compared without the spaces that wrapping may put inside one.
		page := strings.ReplaceAll(readWords(pageText(g)), " ", "")
		for _, want := range []string{goal, oracle} {
			if !strings.Contains(page, strings.ReplaceAll(want, " ", "")) {
				t.Errorf("at %dx%d the slice screen lacks %q whole:\n%s", w, h, want, g.view())
			}
		}
		view := g.view()
		for _, l := range strings.Split(view, "\n") {
			if width(l) > w {
				t.Errorf("at %dx%d a row is %d cells: %q", w, h, width(l), l)
			}
		}
		// This slice has no workspace, waits for none, and none waits for it, and
		// nobody has claimed it: those facts are not drawn.
		for _, label := range []string{"workspace", "after", "neededby", "builtby", "heldby"} {
			if strings.Contains(page, label) {
				t.Errorf("at %dx%d the slice screen draws %q, a fact with no value:\n%s", w, h, label, view)
			}
		}
		for _, line := range strings.Split(pageText(g), "\n") {
			switch strings.TrimSpace(line) {
			case "goal", "oracle", "state", "tries", "workspace", "after":
				t.Errorf("at %dx%d a label stands with no value: %q", w, h, line)
			}
		}
	}
}

// TestSliceScreenNamesItsBuilderOnce: a session that drafted the brief and then
// claimed the slice is the builder on the slice screen, in the facts and in the
// timeline, and is not the planner.
func TestSliceScreenNamesItsBuilderOnce(t *testing.T) {
	t.Parallel()
	lines := []events.Envelope{
		jl(at(0, 1), "JIG-1", "", journal.EventApproval, with("s-both", "", "", "", "", "")),
		jl(at(0, 5), "JIG-1", "a", journal.EventClaim, with("s-both", "", "", "", "", "")),
	}
	status := slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"})
	g := openSlice(t, status, lines, "a", 100, 30)
	page := readWords(pageText(g))
	if !strings.Contains(page, "held by builder") {
		t.Errorf("the facts do not name the holder the builder:\n%s", page)
	}
	if !strings.Contains(page, "builder took it") {
		t.Errorf("the timeline does not name the builder:\n%s", page)
	}
	if strings.Contains(page, "planner") {
		t.Errorf("the screen names the slice's builder by its first role:\n%s", page)
	}
}

// TestSliceScreenOpensTheSlicesItWaitsOnAndTheOnesThatWaitOnIt: "after" and
// "needed by" name their slices as rows, enter on one opens that slice's screen
// and esc comes back.
func TestSliceScreenOpensTheSlicesItWaitsOnAndTheOnesThatWaitOnIt(t *testing.T) {
	t.Parallel()
	g := openSlice(t, fixRoundStatus(), jig1Records(), "d", 100, 40)
	page := pageText(g)
	if !strings.Contains(page, "after") || !strings.Contains(page, "b") {
		t.Fatalf("the facts of d do not say what it waits for:\n%s", page)
	}
	for _, wait := range []string{"b", "c"} {
		g.focus(wait).press("enter")
		if g.m.Screen() != "slice" || g.m.top().slice != wait {
			t.Fatalf("enter on %q opened %s of slice %q:\n%s", wait, g.m.Screen(), g.m.top().slice, g.view())
		}
		if !strings.Contains(pageText(g), "needed by") {
			t.Errorf("the screen of %s does not say d needs it:\n%s", wait, pageText(g))
		}
		g.press("esc")
		if g.m.top().slice != "d" {
			t.Fatalf("esc from %s is on slice %q, want d", wait, g.m.top().slice)
		}
	}
	// And the other way: from b, enter on the slice that needs it.
	g.focus("b").press("enter").focus("d").press("enter")
	if g.m.top().slice != "d" {
		t.Errorf("enter on the slice that waits for b opened %q:\n%s", g.m.top().slice, g.view())
	}
	g.press("esc")
	if g.m.top().slice != "b" {
		t.Errorf("esc from d is on slice %q, want b", g.m.top().slice)
	}
}

// TestAFixSliceScreenOpensWithItsFinding: the first panel of a fix slice is its
// finding, with the round, the finding ids and the goal jig wrote whole, with no
// heading line; the slice's own panel does not repeat the goal.
func TestAFixSliceScreenOpensWithItsFinding(t *testing.T) {
	t.Parallel()
	g := openSlice(t, fixRoundStatus(), jig1Records(), "fix-2-app", 100, 40)
	view := g.view()
	page := pageText(g)
	finding := strings.Index(page, "Finding")
	slice := strings.Index(page, "Slice fix-2-app")
	if finding < 0 || slice < 0 || finding > slice {
		t.Fatalf("the finding is not the panel above the slice's own:\n%s", view)
	}
	head := page[finding:slice]
	if rowHolding(view, "Finding", "round 2") == "" {
		t.Errorf("the finding panel does not say its round:\n%s", view)
	}
	for _, want := range []string{"r1-f1", "publish.go:1105 The stacked parent's fallback is no longer reached", "A stale parent would merge.", "Risk (high): the fallback is the only guard."} {
		if !strings.Contains(head, want) {
			t.Errorf("the finding panel lacks %q:\n%s", want, head)
		}
	}
	if strings.Contains(page, "Fix these gate findings") {
		t.Errorf("the goal's heading is drawn:\n%s", page)
	}
	if n := strings.Count(page, "A stale parent would merge."); n != 1 {
		t.Errorf("the goal is drawn %d times, want once:\n%s", n, page)
	}
	if !strings.Contains(page[slice:], "oracle") {
		t.Errorf("the slice's own panel has no oracle:\n%s", page[slice:])
	}
}

// TestAnOpenQuestionOnTheSliceScreenIsStillAnsweredWithA: a slice with an open
// question has its row on the screen and `a` opens the answer to it.
func TestAnOpenQuestionOnTheSliceScreenIsStillAnsweredWithA(t *testing.T) {
	t.Parallel()
	g := openSlice(t, fixRoundStatus(), jig1Records(), "c", 100, 40)
	if page := pageText(g); !strings.Contains(page, "q-001") || !strings.Contains(page, "Formal or casual?") {
		t.Fatalf("the open question is not on the screen:\n%s", page)
	}
	g.press("a")
	if g.m.input == nil || g.m.input.qid != "q-001" {
		t.Fatalf("a did not open the answer to q-001:\n%s", g.view())
	}
}
