package tui

import (
	"fmt"
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
