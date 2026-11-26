package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// bigMain is main over n tickets, the first of which has a line written at 09:00.
func bigMain(t *testing.T, n int) *rig {
	t.Helper()
	g := newRig(t, "")
	atClock(g, "2026-10-23T09:10:00Z")
	g.feed(jl(at(0, 0), "JIG-1", "", journal.EventGateOpen))
	rows := make([]rosterRow, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, rosterRow{ID: fmt.Sprintf("JIG-%d", i), Title: fmt.Sprintf("Ticket number %d", i),
			Digest: task("build", true, "done|approved", "current|ready to build", "pending|after the build", "pending|after a clean review", "pending|after the publish")})
	}
	g.m.w.setRoster(rows)
	g.m.w.gen++
	g.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return g
}

// TestAStepOfTheClockBuildsNoRow: main over 1,000 tickets, a step of the clock
// draws the ages again and builds neither the queue nor a row; a step past the
// moment a session's sign leaves its window builds them, since a row may move.
func TestAStepOfTheClockBuildsNoRow(t *testing.T) {
	t.Parallel()
	g := bigMain(t, 1000)
	first := g.view()
	if !strings.Contains(first, "10m") {
		t.Fatalf("the first frame has no age:\n%s", first)
	}
	builds, rows := g.m.queueBuilds, g.m.rowBuilds
	if builds == 0 || rows < 1000 {
		t.Fatalf("the first frame built %d queues and %d rows", builds, rows)
	}
	atClock(g, "2026-10-23T09:11:00Z")
	started := time.Now()
	step := g.view()
	took := time.Since(started)
	if !strings.Contains(step, "11m") || strings.Contains(step, "10m") {
		t.Errorf("the clock's step did not draw the age again:\n%s", step)
	}
	if g.m.queueBuilds != builds || g.m.rowBuilds != rows {
		t.Errorf("a step of the clock built %d queues and %d rows", g.m.queueBuilds-builds, g.m.rowBuilds-rows)
	}
	// What the step cost before: the whole list built again.
	g.m.main = nil
	started = time.Now()
	g.view()
	t.Logf("a step of the clock on 1,000 tickets took %v; the list built whole takes %v", took, time.Since(started))
}

// agesOf are the ages that end the rows of a frame, top to bottom.
func agesOf(frame string) []string {
	var out []string
	re := regexp.MustCompile(` (\d+[smhd]) *│$`)
	for _, l := range strings.Split(frame, "\n") {
		if m := re.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// TestAClockStepMovesWhatTheClockMoves: a step of the clock that crosses the
// moment a session's sign leaves its window builds the list again, and the row
// leaves Running; one that does not draws the ages again of every row, of an
// issue's tasks, of a view by chart and of a search open across it, and a folded
// group stays one fold row.
func TestAClockStepMovesWhatTheClockMoves(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	atClock(g, "2026-10-23T09:01:00Z")
	g.m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Greeting copy",
		Digest: task("build", true, "done|approved", "current|ready to build", "pending|after the build", "pending|after a clean review", "pending|after the publish")}})
	g.feed(ll(at(0, 30), "JIG-1", journal.EventTool, with("s-builder", "", "", "", "", "")))
	// A later line of the journal: the moment the row stops running is the
	// session's sign leaving its window, not the ticket's last line doing so.
	g.feed(jl(at(2, 0), "JIG-1", "", journal.EventMemorize))
	g.m.w.gen++
	g.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if v := g.view(); !strings.Contains(v, "╭─ Running ") {
		t.Fatalf("a ticket with a session at work is not running:\n%s", v)
	}
	builds, rows := g.m.queueBuilds, g.m.rowBuilds
	atClock(g, "2026-10-23T09:03:00Z")
	if v := g.view(); !strings.Contains(v, "╭─ Running ") || g.m.queueBuilds != builds || g.m.rowBuilds != rows {
		t.Errorf("a step inside the window built the list (%d queues, %d rows) or moved the row:\n%s", g.m.queueBuilds-builds, g.m.rowBuilds-rows, v)
	}
	atClock(g, "2026-10-23T09:06:00Z")
	if v := g.view(); strings.Contains(v, "╭─ Running ") || g.m.queueBuilds != builds+1 {
		t.Errorf("a step past the window left the row running or built the list %d times:\n%s", g.m.queueBuilds-builds, v)
	}
}

// TestAClockStepAgesEveryRowWhereverItIsDrawn: see TestAClockStepMovesWhatTheClockMoves.
func TestAClockStepAgesEveryRowWhereverItIsDrawn(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{})
	g.m.height, g.m.width = 40, 110
	atClock(g, "2026-10-23T09:50:00Z")
	g.unfold()
	check := func(what string) {
		before := agesOf(stripANSI(g.view()))
		if len(before) < 3 {
			t.Fatalf("%s: %d ages drawn", what, len(before))
		}
		builds, rows := g.m.queueBuilds, g.m.rowBuilds
		atClock(g, "2026-10-23T09:55:00Z")
		after := agesOf(stripANSI(g.view()))
		atClock(g, "2026-10-23T09:50:00Z")
		g.view() // the clock goes back: the next step is a step again
		if g.m.queueBuilds != builds || g.m.rowBuilds != rows {
			t.Errorf("%s: a step built %d queues and %d rows", what, g.m.queueBuilds-builds, g.m.rowBuilds-rows)
		}
		if len(after) != len(before) {
			t.Fatalf("%s: %d ages before, %d after", what, len(before), len(after))
		}
		for i := range before {
			if before[i] == after[i] {
				t.Errorf("%s: row %d reads %s before the step and after it", what, i, before[i])
			}
		}
	}
	check("the groups, the done fold open")
	g.press("s")
	check("by chart")
	g.press("s")
	// A search open across the step draws the new ages.
	typed(g.m, "/shop")
	check("a search")
	press(g.m, tea.KeyEsc)
	// A fold stays one row across a step.
	g.m.nav[0].open = nil
	g.view()
	atClock(g, "2026-10-23T09:55:00Z")
	if v := stripANSI(g.view()); !strings.Contains(v, "▸ enter shows them") || strings.Contains(v, "SHOP-1 ") {
		t.Errorf("a folded group is not one fold row after a step:\n%s", v)
	}
}

// TestASearchDrawsThePanelsOfMain: the rows a search keeps are drawn in main's
// panels, each list under its names, with the cursor's row inside one.
func TestASearchDrawsThePanelsOfMain(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	m.View()
	typed(m, "/for")
	frame := stripANSI(m.View())
	if !strings.Contains(frame, "  ╭─ ") || !strings.Contains(frame, "│    ticket") || !strings.Contains(frame, "│▸") {
		t.Errorf("a search is not drawn as panels under their names:\n%s", frame)
	}
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "▸") {
			t.Errorf("the cursor's row is outside a panel: %q", l)
		}
	}
}
