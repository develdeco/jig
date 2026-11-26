package tui

import (
	"fmt"
	"strings"
	"testing"
)

// numbered is n lines of text, "<what> line 1" and on.
func numbered(what string, n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s line %d", what, i+1)
	}
	return strings.Join(lines, "\n")
}

// publishRig is a published JIG-2 whose status carries a body and a changelog
// of the given lengths, on a terminal of height rows.
func publishRig(t *testing.T, body, changelog, height int) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2DoneWith(numbered("body", body), numbered("changelog", changelog)), 0)
	g.m.height = height
	g.feed(jig2()...).press("up", "enter")
	if g.m.Screen() != "publish" {
		t.Fatalf("on %s, want the publish screen", g.m.Screen())
	}
	return g
}

// TestPublishScreenFitsTheWindowWhateverTheTextsAre: the first screenful of the
// publish screen holds the checklist and the detail of its first check whatever
// the files are, a body longer than the room the screen leaves is windowed inside
// its box with the mark of what lies below, and the screen is as tall as the
// terminal.
func TestPublishScreenFitsTheWindowWhateverTheTextsAre(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ body, changelog, height int }{
		{3, 2, 24}, {40, 40, 24}, {40, 1, 24}, {1, 40, 24}, {40, 40, 30}, {40, 40, 50}, {0, 0, 24},
	} {
		g := publishRig(t, tc.body, tc.changelog, tc.height)
		view := g.view()
		if n := strings.Count(view, "\n") + 1; n != tc.height {
			t.Errorf("height %d: the screen is %d lines", tc.height, n)
		}
		if !strings.Contains(between(view, "Checklist", "╰"), "published") || !strings.Contains(view, "╭─ Detail · body") {
			t.Errorf("body %d, changelog %d, height %d: the first screenful lacks the whole checklist or the detail of its first check:\n%s", tc.body, tc.changelog, tc.height, view)
		}
		detail := between(view, "╭─ Detail · body", "╰")
		// Whatever of the body the screen has no room for is cut inside the box and
		// the box says so; nothing is cut without the mark.
		if whole := strings.Contains(detail, fmt.Sprintf("body line %d", tc.body)); tc.body > 0 && !whole && !strings.Contains(detail, "↓ ") {
			t.Errorf("body %d, height %d: the body is cut with no mark:\n%s", tc.body, tc.height, view)
		}
		if tc.body > 12 && tc.height <= 30 && strings.Contains(detail, fmt.Sprintf("body line %d", tc.body)) {
			t.Errorf("body %d, height %d: a long body is not windowed in its box:\n%s", tc.body, tc.height, view)
		}
	}
	// With the room the whole of a short body is the detail's.
	if view := publishScreen(t, 100, 60).view(); !strings.Contains(between(view, "╭─ Detail · body", "╰"), "body line 12") {
		t.Errorf("a body of 12 lines is not whole on a tall screen:\n%s", view)
	}
	// A body that is there and a window with room for little still shows its
	// first line.
	g := publishRig(t, 40, 40, 27)
	if view := g.view(); !strings.Contains(view, "body line 1") {
		t.Errorf("the first line of the body is not on the screen:\n%s", view)
	}
}

// TestAPageWithNoRowToStandOnIsScrolledByTheArrowKeys: on a terminal too short
// for the review screen of a ticket with no round and no ask, which has no row to
// stand on, the arrow keys move the window down the page and back, and home and
// end go to its ends.
func TestAPageWithNoRowToStandOnIsScrolledByTheArrowKeys(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.m.height = 12
	g.feed(jig1()...).press("down", "enter")
	if g.m.Screen() != "review" || len(g.m.page().refs()) != 0 {
		t.Fatalf("on %s with %d rows, want the review screen with none", g.m.Screen(), len(g.m.page().refs()))
	}
	// The page ends with the activity's bottom edge, the second the page has.
	if view := g.view(); !strings.Contains(view, "no round yet") || strings.Count(view, "╰") > 1 {
		t.Fatalf("the short screen does not begin at the top of the page:\n%s", view)
	}
	g.press("end")
	if view := g.view(); !strings.Contains(view, "╰") || strings.Contains(view, "no round yet") {
		t.Errorf("end did not go to the end of the page:\n%s", view)
	}
	g.press("home")
	if view := g.view(); !strings.Contains(view, "no round yet") {
		t.Errorf("home did not go back to the top:\n%s", view)
	}
	g.press("down", "down", "down")
	// A window that starts inside a list keeps the list's names above it, so a
	// move over them goes on to the next line there is to show.
	three := g.m.top().scroll
	if view := g.view(); !strings.Contains(view, "↑ ") || three < 3 {
		t.Errorf("three downs left the window at %d:\n%s", g.m.top().scroll, g.view())
	}
	g.press("up")
	if g.m.top().scroll >= three {
		t.Errorf("up left the window at %d, want it above %d", g.m.top().scroll, three)
	}
	g.press("home", "up")
	if g.m.top().scroll != 0 {
		t.Errorf("up at the top left the window at %d", g.m.top().scroll)
	}
	g.press("esc", "enter")
	if g.m.top().scroll != 0 {
		t.Errorf("a screen opened again begins at %d, want the top", g.m.top().scroll)
	}
}

// TestOpeningTheReviewPutsTheCursorOnTheAskThatWasChosen: from the ticket screen
// a person who picks an ask lands on that ask, and one who opens the step lands
// on the first, so k and d decide what they came for.
func TestOpeningTheReviewPutsTheCursorOnTheAskThatWasChosen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	g.press("enter")
	if ref := g.m.selected(); ref == nil || ref.kind != rowAsk || ref.id != "r1-f1" {
		t.Fatalf("opening the review step puts the cursor on %+v, want ask r1-f1", ref)
	}
	g.press("esc", "end", "enter") // the ticket screen's last row: ask r1-f2
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want the review screen", g.m.Screen())
	}
	if ref := g.m.selected(); ref == nil || ref.id != "r1-f2" {
		t.Errorf("opening ask r1-f2 puts the cursor on %+v", ref)
	}
	// The window follows: both asks and their reasons are in the first screenful.
	if view := g.view(); !strings.Contains(view, "Unchecked error") || !strings.Contains(view, "Name shadows import") {
		t.Errorf("the review screen does not show the asks:\n%s", view)
	}

	published := newRig(t, "JIG-2")
	published.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	published.feed(jig2()...).press("up", "enter")
	if ref := published.m.selected(); ref != nil && ref.kind == rowAsk {
		t.Errorf("a review with no ask puts the cursor on an ask: %+v", ref)
	}
}

// TestDetailScreensBeginAtTheirTop: the first screenful of each detail screen
// opens on the step it is about, whatever the page holds below it, and the
// window follows the cursor down and back.
func TestDetailScreensBeginAtTheirTop(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.r.answer("brief show JIG-1 --text --store /s", briefTextJIG1(), 0)
	g.feed(jig1Records()...).press("up", "enter")
	// On a screen that has the room for it the page begins at its top, and the
	// detail of the cursor's row is whole under the lists.
	g.sized(100, 40)
	lines := strings.Split(g.view(), "\n")
	if len(lines) != 40 || !strings.Contains(lines[2], "approved by an agent, as Ada Lovelace") || !strings.Contains(g.view(), "╭─ Detail · Goal") {
		t.Fatalf("the brief screen does not begin with its step and hold the detail of its first row:\n%s", g.view())
	}
	// On the default screen the lists and the detail are what the window shows,
	// and the cursor stays on it down to the last row.
	g.sized(80, 24)
	if view := g.view(); len(strings.Split(view, "\n")) != 24 || !strings.Contains(view, "╭─ Detail · Goal") || !strings.Contains(view, "▸") {
		t.Errorf("the first screenful of the brief has no detail of its first row:\n%s", view)
	}
	g.press("end")
	view := g.view()
	if !strings.Contains(view, "▸") || !strings.Contains(view, "Ship the copy") || !strings.Contains(view, "╭─ Detail · d") {
		t.Errorf("the last row is not on the screen with its detail:\n%s", view)
	}
	g.press("home")
	if view := g.view(); !strings.Contains(view, "▸ Goal") && !strings.Contains(view, "│▸ Goal") {
		t.Errorf("home did not bring back the first row:\n%s", view)
	}
}

// TestTheTailAndTheCommandPanelLeaveTheDetailScreensScrollable: with the tail
// open, a detail screen has half the rows and still reaches its end.
func TestTheTailAndTheCommandPanelLeaveTheDetailScreensScrollable(t *testing.T) {
	t.Parallel()
	g := publishRig(t, 6, 6, 24)
	g.press("t", "end")
	if view := g.view(); !strings.Contains(view, "tail") || !strings.Contains(view, "╰") {
		t.Errorf("with the tail open the end of the page is out of reach:\n%s", view)
	}
}
