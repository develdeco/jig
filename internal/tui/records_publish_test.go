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
	g.feed(jig2()...).press("enter")
	if g.m.Screen() != "publish" {
		t.Fatalf("on %s, want the publish screen", g.m.Screen())
	}
	return g
}

// TestPublishScreenFitsTheWindowWhateverTheTextsAre: the body and the changelog
// take the rows the checks and the activity leave, so the page is no taller
// than the window, however long the files are, and what was cut ends in an
// ellipsis.
func TestPublishScreenFitsTheWindowWhateverTheTextsAre(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ body, changelog, height int }{
		{3, 2, 24}, {40, 40, 24}, {40, 1, 24}, {1, 40, 24}, {40, 40, 30}, {40, 40, 50}, {0, 0, 24},
	} {
		g := publishRig(t, tc.body, tc.changelog, tc.height)
		pg := g.m.page()
		if rows := g.m.bodyRows(); len(pg.lines) > rows {
			t.Errorf("body %d, changelog %d, height %d: the page has %d lines for a window of %d:\n%s",
				tc.body, tc.changelog, tc.height, len(pg.lines), rows, pageText(g))
		}
		view := g.view()
		if n := strings.Count(view, "\n") + 1; n != tc.height {
			t.Errorf("height %d: the screen is %d lines", tc.height, n)
		}
		if !strings.Contains(view, "╰") {
			t.Errorf("height %d: the activity panel's bottom edge is not on the screen:\n%s", tc.height, view)
		}
		if text := pageText(g); tc.body == 40 && !strings.Contains(text, " …") {
			t.Errorf("height %d: the body was cut without an ellipsis:\n%s", tc.height, text)
		}
	}
	// A body that is there and a window with no room for it still shows its
	// first line.
	g := publishRig(t, 40, 40, 24)
	if text := pageText(g); !strings.Contains(text, "body line 1") || !strings.Contains(text, "changelog line 1") {
		t.Errorf("the first lines of the body and the changelog are not on the page:\n%s", text)
	}
}

// TestAPageWithNoRowToStandOnIsScrolledByTheArrowKeys: on a terminal too short
// for the publish screen, the arrow keys move the window down the page and
// back, and home and end go to its ends.
func TestAPageWithNoRowToStandOnIsScrolledByTheArrowKeys(t *testing.T) {
	t.Parallel()
	g := publishRig(t, 6, 6, 14)
	if view := g.view(); !strings.Contains(view, "✓ pull request opened ·") || strings.Contains(view, "╰") {
		t.Fatalf("the short screen does not begin at the top of the page:\n%s", view)
	}
	g.press("end")
	if view := g.view(); !strings.Contains(view, "╰") || strings.Contains(view, "✓ pull request opened ·") {
		t.Errorf("end did not go to the end of the page:\n%s", view)
	}
	g.press("home")
	if view := g.view(); !strings.Contains(view, "✓ pull request opened ·") {
		t.Errorf("home did not go back to the top:\n%s", view)
	}
	g.press("down", "down", "down")
	if view := g.view(); strings.Contains(view, "✓ pull request opened ·") || g.m.top().scroll != 3 {
		t.Errorf("three downs left the window at %d:\n%s", g.m.top().scroll, g.view())
	}
	g.press("up")
	if g.m.top().scroll != 2 {
		t.Errorf("up left the window at %d, want 2", g.m.top().scroll)
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
	lines := strings.Split(g.view(), "\n")
	if len(lines) != 24 || !strings.Contains(lines[2], "approved by Ada Lovelace") {
		t.Fatalf("the brief screen does not begin with its step:\n%s", g.view())
	}
	// Down through the sections to the slices: the window leaves the top, and
	// the cursor stays on the screen.
	g.press("end")
	view := g.view()
	if !strings.Contains(view, "▸") || !strings.Contains(view, "Ship the copy") {
		t.Errorf("the last row is not on the screen:\n%s", view)
	}
	g.press("home")
	if lines := strings.Split(g.view(), "\n"); !strings.Contains(lines[2], "approved") {
		t.Errorf("home did not bring back the top:\n%s", g.view())
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
