package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// Texts as long as a person writes them, made of words no other line uses.
const (
	layoutSubject  = "Greeting rewrites the welcome copy so every visitor is named by first name and falls back to friend only when nothing is on file"
	layoutQuestion = "Should the farewell copy trim whitespace around the name before it greets the visitor or leave the name exactly as typed"
	layoutFinding  = "Unchecked write error silently drops the receipt line whenever the ledger file is locked by another process"
)

// wantNothingCut checks that a screen that has the width for it cuts none of
// the texts: each is in the view whole, however its words wrap, and no row is
// wider than the terminal. A narrow terminal wraps them, and a text that goes
// past three rows ends in an ellipsis.
func wantNothingCut(t *testing.T, view string, w int, texts ...string) {
	t.Helper()
	wantNoRowWider(t, strings.Split(view, "\n"), w)
	flat := squeeze(view)
	for _, text := range texts {
		whole := strings.Contains(flat, squeeze(text))
		switch {
		case whole:
		case w >= 100:
			t.Errorf("at %d wide %q is cut:\n%s", w, text, view)
		case !strings.Contains(flat, squeeze(text[:30])):
			t.Errorf("at %d wide %q is cut early:\n%s", w, text, view)
		}
	}
}

// TestBuildReviewSliceScreensCutNothingWhileWidthAllows: the Build, Review,
// Slice and Brief screens, with long subjects, questions and findings, wrap
// them at a narrow width and cut none where the width allows.
func TestBuildReviewSliceScreensCutNothingWhileWidthAllows(t *testing.T) {
	t.Parallel()
	build := func() *rig {
		g := newRig(t, "JIG-1")
		g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
		lines := jig1Records()
		for i := range lines {
			switch lines[i].Event {
			case journal.EventOracle:
				lines[i].Subject = layoutSubject
			case journal.EventQuestion:
				lines[i].Summary = layoutQuestion
			}
		}
		g.feed(lines...)
		return g
	}
	screens := map[string]func() (*rig, []string){
		"build": func() (*rig, []string) {
			g := build()
			g.press("enter")
			return g, []string{layoutSubject, layoutQuestion}
		},
		"slice": func() (*rig, []string) {
			g := build()
			// The slice screen words a commit's subject through the line that asked
			// for its verification, until it lists the commits itself.
			g.feed(jl(at(0, 6), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", layoutSubject, "", "")))
			g.press("enter", "up", "enter")
			return g, []string{layoutSubject}
		},
		"review": func() (*rig, []string) {
			g := newRig(t, "JIG-2")
			g.r.answer("status JIG-2 --store /s", strings.ReplaceAll(manyProbesTwoAsks(), "Unchecked error", layoutFinding), 0)
			g.feed(jig2Mid()...).press("enter") // the cursor opens on the first ask, whose detail is its finding whole
			return g, []string{layoutFinding}
		},
	}
	for name, open := range screens {
		for _, size := range [][2]int{{60, 90}, {80, 90}, {230, 90}} {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s %dx%d", name, w, h), func(t *testing.T) {
				t.Parallel()
				g, texts := open()
				g.send(tea.WindowSizeMsg{Width: w, Height: h})
				wantNothingCut(t, g.view(), w, texts...)
			})
		}
	}
}
