package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// readDown is the words of the screen from its top to its end: every frame the
// page takes as the keys page down, joined.
func readDown(g *rig, frames int) string {
	seen := readWords(g.view())
	for i := 0; i < frames; i++ {
		g.send(tea.KeyMsg{Type: tea.KeyPgDown})
		seen += " " + readWords(g.view())
	}
	return seen
}

// TestEveryRecordOpensAndEscComesBack: a ticket that went through three review
// rounds (four findings in round 1, one of them a fix; a fix in round 2; a clean
// round 3), whose first fix slice failed a try and then passed, and an issue
// whose suite failed and added a fix task, at five sizes. Every round is a row; a
// round 1 finding opens whole; its fix slice opens the slice whose Finding panel
// names it; esc returns twice to round 1 with the cursor where it was left; the
// failed try opens the slice screen on it with its run and failure whole; and the
// failed suite row opens the fix task and esc returns to the plan.
func TestEveryRecordOpensAndEscComesBack(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {160, 40}, {230, 50}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := threeRounds(t, w, h, 0)
			if got := strings.Join(g.refsOfKind(rowRound), " "); got != "3 2 1" {
				t.Fatalf("the review has round rows %q, want a row for each round", got)
			}

			// tab and a move reach a round 1 finding.
			g.tabTo("Rounds")
			g.press("down", "down")
			g.tabTo("Detail · round 1")
			if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f1" {
				t.Fatalf("the cursor is on %+v, want the first finding of round 1", ref)
			}

			// enter opens it whole, no field cut.
			g.press("enter")
			if g.m.Screen() != "finding" {
				t.Fatalf("enter opened %s, want the finding", g.m.Screen())
			}
			seen := readDown(g, 8)
			for _, want := range []string{
				"alpha/a.go:12", "Close the file", "a leaked handle fails the next open",
				"The file is opened and never closed.", "Close it on every path.", "fix-1-alpha", "green",
			} {
				if !strings.Contains(seen, want) {
					t.Errorf("the finding screen lacks %q:\n%s", want, seen)
				}
			}
			if strings.Contains(g.view(), "…") && !strings.Contains(strings.Split(g.view(), "\n")[0], "…") {
				t.Errorf("a field of the finding is cut with an ellipsis:\n%s", g.view())
			}

			// enter on its fix slice opens the slice whose Finding panel names it.
			g.cursorOn(rowSlice, "fix-1-alpha")
			g.press("enter")
			if g.m.Screen() != "slice" || g.m.top().slice != "fix-1-alpha" {
				t.Fatalf("enter opened %s %q, want the fix slice", g.m.Screen(), g.m.top().slice)
			}
			g.tabTo("Finding")
			if got := strings.Join(g.refsOfKind(rowFinding), " "); got != "r1-f1" {
				t.Errorf("the Finding panel has finding rows %q, want r1-f1", got)
			}
			if panel := readWords(between(g.view(), "╭─ Finding", "╰")); !strings.Contains(panel, "round 1") || !strings.Contains(panel, "r1-f1") {
				t.Errorf("the Finding panel does not name its round and finding:\n%s", g.view())
			}

			// esc returns twice to round 1, the cursor where it was left.
			g.press("esc", "esc")
			if g.m.Screen() != "review" {
				t.Fatalf("two esc went to %s, want the review", g.m.Screen())
			}
			if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f1" {
				t.Errorf("the cursor is on %+v, want where it was left, the finding", ref)
			}
			if !strings.Contains(g.view(), "╭─ Detail · round 1") {
				t.Errorf("the Detail is not round 1's:\n%s", g.view())
			}

			// On the build screen the failed try opens the slice screen on it.
			g.press("2")
			g.cursorOn(rowSlice, "fix-1-alpha")
			g.tabTo("Detail · fix-1-alpha")
			g.cursorOn(rowTry, sha('b'))
			g.press("enter")
			if g.m.Screen() != "slice" {
				t.Fatalf("enter on the failed try opened %s, want the slice", g.m.Screen())
			}
			if ref := g.m.selected(); ref == nil || ref.kind != rowTry || ref.id != sha('b') {
				t.Errorf("the slice opens with the cursor on %+v, want the failed try", ref)
			}
			run := readDown(g, 6)
			for _, want := range []string{"bbbbbbb", "Close the file on the happy path", "failed in 7s", "TestClose: the handle leaked", "verify log"} {
				if !strings.Contains(run, want) {
					t.Errorf("the failed try lacks %q on the slice screen:\n%s", want, run)
				}
			}

			// On the plan the failed suite row opens the fix task, and esc returns.
			p := planRig(t, planSpec{suite: []string{`demo,fail,go test ./...,4d577e2aaaaaa,9,"final:boom",-,SHOP-4,-`}}).sized(w, h)
			p.tabTo("Issue")
			p.press("enter")
			if p.m.Screen() != "ticket" || p.m.top().ticket != "SHOP-4" {
				t.Fatalf("enter on the failed suite opened %s %q, want the fix task SHOP-4", p.m.Screen(), p.m.top().ticket)
			}
			p.press("esc")
			if p.m.Screen() != "chart" {
				t.Errorf("esc went to %s, want the plan", p.m.Screen())
			}
		})
	}
}
