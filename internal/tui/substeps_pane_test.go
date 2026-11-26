package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// slicesRig is a screen on BIG-1, a build of n slices of which the first
// building-1 are green and the building-th is being built, at w by h.
func slicesRig(t *testing.T, n, building, w, h int) *rig {
	t.Helper()
	var rows [][]string
	var lines []events.Envelope
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s%d", i)
		state := "green"
		switch {
		case i == building:
			state = "building"
		case i > building:
			state = "queued"
		}
		rows = append(rows, []string{id, state, "1", "-", "-", "Slice " + id})
		if i <= building {
			lines = append(lines,
				jl(at(i, 0), "BIG-1", id, journal.EventClaim, with("s-builder", "", "", "", "", "")),
				jl(at(i, 30), "BIG-1", id, journal.EventVerifyPending, with("s-builder", sha(byte('0'+i)), "", "", "", "")))
		}
		if i < building {
			lines = append(lines, jl(at(i, 40), "BIG-1", id, journal.EventVerified, with("", sha(byte('0'+i)), "", "", "", "")))
		}
	}
	status := strings.Join([]string{"ticket: BIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, rows), "questions: none"}, "\n") + "\n"
	g := newRig(t, "BIG-1")
	g.r.answer("status BIG-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(lines...)
	g.view()
	return g
}

// TestTabReachesTheSubstepsPaneAndEnterOpensASlice: the sub-steps of the step
// under the Steps cursor are a pane; tab reaches it with its cursor on the slice
// being built, enter on a slice opens its slice screen, and esc comes back to the
// ticket with the cursor, and the focus, where they were left.
func TestTabReachesTheSubstepsPaneAndEnterOpensASlice(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 28}, {100, 30}} {
		g := slicesRig(t, 5, 5, size[0], size[1])
		g.tabTo("Build")
		if got := g.m.selected(); got == nil || got.kind != rowSub || got.slice != "s5" {
			t.Fatalf("%v: tab reaches the Build pane with the cursor on %+v, want the slice being built, s5", size, got)
		}
		g.press("up", "up")
		if got := g.m.selected(); got == nil || got.slice != "s3" {
			t.Fatalf("%v: up twice from s5 goes to %+v, want s3", size, got)
		}
		g.press("enter")
		if g.m.Screen() != "slice" || g.m.top().slice != "s3" {
			t.Fatalf("%v: enter on the slice sub-step opens %s %q, want the slice screen of s3", size, g.m.Screen(), g.m.top().slice)
		}
		g.press("esc")
		g.view()
		if g.m.Screen() != "ticket" || g.m.focusedPane() != "Build" {
			t.Fatalf("%v: esc returns to %s with the focus on %q, want the ticket and Build", size, g.m.Screen(), g.m.focusedPane())
		}
		if got := g.m.selected(); got == nil || got.slice != "s3" {
			t.Errorf("%v: back on the ticket the cursor is on %+v, want s3 where it was left", size, got)
		}
		g.tabTo("Steps")
		if got := g.m.selected(); got == nil || got.kind != rowBuild {
			t.Errorf("%v: the Steps cursor is on %+v, want the build step", size, got)
		}
	}
}

// TestASubstepPaneListsEverySubstepAndOpensOnTheOneThatMatters: the pane has a
// row for each sub-step, the cursor goes through all of them and the window
// follows it with the count of what it leaves out; it opens on the sub-step
// that matters now, which stays open.
func TestASubstepPaneListsEverySubstepAndOpensOnTheOneThatMatters(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 9, 6, 80, 34)
	v := g.view()
	if !strings.Contains(v, "slice s6") || !strings.Contains(v, "now ") {
		t.Errorf("the pane does not open the slice under way:\n%s", v)
	}
	g.tabTo("Build")
	if got := g.m.selected(); got == nil || got.slice != "s6" {
		t.Fatalf("the pane's cursor begins on %+v, want s6, the sub-step that matters now", got)
	}
	g.press("home")
	if got := g.m.selected(); got == nil || got.slice != "s1" {
		t.Fatalf("home goes to %+v, want s1", got)
	}
	seen := map[string]bool{"s1": true}
	for i := 0; i < 12; i++ {
		g.press("down")
		if got := g.m.selected(); got != nil && got.kind == rowSub {
			seen[got.slice] = true
		} else {
			t.Fatalf("the cursor left the pane's rows: %+v", got)
		}
	}
	if len(seen) != 9 {
		t.Errorf("the cursor went through %d of the 9 sub-steps: %v", len(seen), seen)
	}
	if got := g.m.selected(); got == nil || got.slice != "s9" {
		t.Errorf("down past the last goes to %+v, want it to stay on s9", got)
	}
	if v = g.view(); !strings.Contains(v, "▸ ○ slice s9") {
		t.Errorf("the window at the end does not show the cursor on s9:\n%s", v)
	}
	// A pane with less room draws the window around the cursor and counts what
	// it leaves out on each side.
	short := slicesRig(t, 9, 6, 80, 26)
	short.tabTo("Build").press("end")
	sv := short.view()
	block := strings.Join(panelLines(strings.Split(sv, "\n"), "Build ─"), "\n")
	if !strings.Contains(block, "▸ ○ slice s9") || !strings.Contains(block, "↑ ") || strings.Contains(block, "↓ ") {
		t.Errorf("the short pane at its end does not show s9 and count what is above:\n%s", sv)
	}
	short.press("home")
	sv = short.view()
	block = strings.Join(panelLines(strings.Split(sv, "\n"), "Build ─"), "\n")
	if !strings.Contains(block, "▸ ✓ slice s1") || !strings.Contains(block, "↓ ") || strings.Contains(block, "↑ ") {
		t.Errorf("the short pane at its start does not show s1 and count what is below:\n%s", sv)
	}
	g.press("end")
	if got := g.m.selected(); got == nil || got.slice != "s9" {
		t.Errorf("end goes to %+v, want s9", got)
	}
	// The sub-step that matters now stays open while the cursor is elsewhere
	// in a window that holds it.
	g.press("home")
	g.press("down", "down", "down", "down", "down")
	if got := g.m.selected(); got == nil || got.slice != "s6" {
		t.Fatalf("five downs from s1 reach %+v, want s6", got)
	}
	g.press("up")
	if v := g.view(); !strings.Contains(v, "now ") {
		t.Errorf("the sub-step that matters now closed when the cursor left it:\n%s", v)
	}
}

// TestASubstepOfAnotherStepOpensItsStepsScreen: a sub-step that names no slice
// opens the screen of its step.
func TestASubstepOfAnotherStepOpensItsStepsScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 44})
	g.feed(jig2Mid()...)
	g.tabTo("Review")
	if got := g.m.selected(); got == nil || got.kind != rowSub || got.slice != "" {
		t.Fatalf("the Review pane's cursor is on %+v, want a sub-step of the round", got)
	}
	g.press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("enter on a round's sub-step opens %s, want the review screen", g.m.Screen())
	}
	g.press("esc")
	g.tabTo("Steps").press("down")
	if got := g.m.selected(); got == nil || got.kind != rowPublish {
		t.Fatalf("the Steps cursor is on %+v, want the publish step", got)
	}
	g.tabTo("Publish")
	g.press("enter")
	if g.m.Screen() != "publish" {
		t.Errorf("enter on a publish sub-step opens %s, want the publish screen", g.m.Screen())
	}
}

// TestThePanesFollowTheStepUnderTheStepsCursorWhateverHasTheFocus: the sub-steps
// pane and the Activity pane are about the step the Steps cursor is on, and a
// move of the focus or of another pane's cursor does not change which.
func TestThePanesFollowTheStepUnderTheStepsCursorWhateverHasTheFocus(t *testing.T) {
	t.Parallel()
	g := shop1Rig(t)
	g.send(tea.WindowSizeMsg{Width: 80, Height: 40})
	g.view()
	g.press("up") // Steps: the brief
	v := g.view()
	if !strings.Contains(v, "╭─ Brief") {
		t.Fatalf("the Steps cursor on the brief does not show its sub-steps:\n%s", v)
	}
	g.tabTo("Brief").press("down")
	v = g.view()
	if !strings.Contains(v, "╭─ Brief") || strings.Contains(v, "╭─ Build") {
		t.Errorf("a move in the sub-steps pane changed the step the panes are about:\n%s", v)
	}
	g.tabTo("Activity")
	v = g.view()
	if !strings.Contains(v, "╭─ Brief") {
		t.Errorf("the focus on Activity changed the step the panes are about:\n%s", v)
	}
	g.tabTo("Steps").press("down")
	if v = g.view(); !strings.Contains(v, "╭─ Build") || strings.Contains(v, "╭─ Brief") {
		t.Errorf("the Steps cursor on the build does not show its sub-steps:\n%s", v)
	}
}
