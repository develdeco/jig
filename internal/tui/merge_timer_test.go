package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
)

// The merge timer: a screen that waits on a merge is read again every
// Config.MergeEvery, because a merge on the host writes no line to any journal.
// The rigs set MergeEvery to -1, so no tick is scheduled and the tests send
// the mergeMsg themselves.

const (
	mergeChartVerb  = "next checkout --json --store /s"
	mergeRosterVerb = "next --json --store /s"
	mergeWaitText   = "waits for JIG-4 merged"
)

// total is how many commands the runner has been asked for, of any kind.
func (f *fakeRunner) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// withoutEnds is checkoutJSON once JIG-4 has merged: no run ends at a merge and
// JIG-7 waits for nothing.
func withoutEnds() string {
	return checkoutJSON(func(v map[string]any) {
		delete(v, "ends")
		delete(v["tickets"].([]map[string]any)[4], "waits_for")
	})
}

// TestAChartThatEndsAtAMergeIsReadAgainOnTheTimer: each tick of a chart screen
// whose run ends at a merge reads the ticket list and the chart; once a read
// lists no end, the screen shows the merge and the next tick reads nothing.
func TestAChartThatEndsAtAMergeIsReadAgainOnTheTimer(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	if v := g.view(); !strings.Contains(v, mergeWaitText) {
		t.Fatalf("the chart screen does not show the wait before the merge:\n%s", v)
	}
	chart, roster := g.r.ran(mergeChartVerb), g.r.ran(mergeRosterVerb)
	for i := 1; i <= 2; i++ {
		g.send(mergeMsg{})
		if n := g.r.ran(mergeChartVerb); n != chart+i {
			t.Fatalf("after tick %d the chart was read %d times more, want %d", i, n-chart, i)
		}
		if n := g.r.ran(mergeRosterVerb); n != roster+i {
			t.Fatalf("after tick %d the ticket list was read %d times more, want %d", i, n-roster, i)
		}
	}
	_, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", withoutEnds(), status)
	g.send(mergeMsg{})
	if v := g.view(); strings.Contains(v, mergeWaitText) {
		t.Errorf("the screen still shows the wait after a read that lists no end:\n%s", v)
	}
	calls := g.r.total()
	g.send(mergeMsg{})
	g.send(mergeMsg{})
	if n := g.r.total() - calls; n != 0 {
		t.Errorf("a chart whose run ends at no merge was read %d times on the timer, want 0", n)
	}
}

// TestAChartThatWaitsOnNothingIsNotReadOnTheTimer: a chart with no end, and the
// project's list over a chart that has one, read nothing on a tick.
func TestAChartThatWaitsOnNothingIsNotReadOnTheTimer(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	_, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", withoutEnds(), status)
	g.press("end", "enter")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want chart", g.m.Screen())
	}
	calls := g.r.total()
	g.send(mergeMsg{})
	if n := g.r.total() - calls; n != 0 {
		t.Errorf("a chart with no end was read %d times on the timer, want 0", n)
	}

	g = chartScreenRig(t)
	g.press("esc")
	if g.m.Screen() != "main" {
		t.Fatalf("on %s, want main", g.m.Screen())
	}
	calls = g.r.total()
	g.send(mergeMsg{})
	if n := g.r.total() - calls; n != 0 {
		t.Errorf("the project's list was read %d times on the timer, want 0", n)
	}
}

// TestAReadThatPrintsNoChartKeepsTheTimerGoing: one blank read (the store file
// replaced at that instant) does not stop the timer: the chart is read on the
// next tick, and on the one after, until a read lists no end.
func TestAReadThatPrintsNoChartKeepsTheTimerGoing(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.r.answer(mergeChartVerb, "", 0)
	before := g.r.ran(mergeChartVerb)
	g.send(mergeMsg{})
	g.send(mergeMsg{})
	if n := g.r.ran(mergeChartVerb); n != before+2 {
		t.Fatalf("after a read that printed no chart the chart was read %d more times in two ticks, want 2", n-before)
	}
	g.r.answer(mergeChartVerb, withoutEnds(), 0)
	g.send(mergeMsg{})
	if n := g.r.ran(mergeChartVerb); n != before+3 {
		t.Fatalf("the chart was read %d more times, want 3", n-before)
	}
	calls := g.r.total()
	g.send(mergeMsg{})
	if n := g.r.total() - calls; n != 0 {
		t.Errorf("the chart was read %d times after a read that listed no end, want 0", n)
	}
}

// TestAPublishedTicketsScreenIsReadAgainOnTheTimer: a ticket published with a
// pull request on record reads its status on each tick, on every screen that
// names it, and stops once the status says it is closed. A ticket that is not
// published, one published with no pull request on record, a closed one and the
// project's list read nothing.
func TestAPublishedTicketsScreenIsReadAgainOnTheTimer(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.feed(jig2()...)
	if g.m.Screen() != "ticket" {
		t.Fatalf("on %s, want ticket", g.m.Screen())
	}
	reads := g.r.statusReads("JIG-2")
	g.send(mergeMsg{})
	if n := g.r.statusReads("JIG-2"); n != reads+1 {
		t.Fatalf("the ticket screen read the status %d more times on a tick, want 1", n-reads)
	}
	g.press("enter")
	if g.m.Screen() != "publish" {
		t.Fatalf("on %s, want publish", g.m.Screen())
	}
	reads = g.r.statusReads("JIG-2")
	g.send(mergeMsg{})
	if n := g.r.statusReads("JIG-2"); n != reads+1 {
		t.Errorf("the publish screen read the status %d more times on a tick, want 1", n-reads)
	}
	g.press("esc", "up", "up", "up", "enter")
	if g.m.Screen() != "brief" {
		t.Fatalf("on %s, want brief", g.m.Screen())
	}
	reads = g.r.statusReads("JIG-2")
	g.send(mergeMsg{})
	if n := g.r.statusReads("JIG-2"); n != reads+1 {
		t.Errorf("the brief screen read the status %d more times on a tick, want 1", n-reads)
	}

	// Closed in another session: the read shows it, and the next tick reads nothing.
	g.r.answer("status JIG-2 --store /s", closedStatus(statusJIG2Done()), 0)
	g.send(mergeMsg{})
	reads = g.r.statusReads("JIG-2")
	g.send(mergeMsg{})
	g.send(mergeMsg{})
	if n := g.r.statusReads("JIG-2"); n != reads {
		t.Errorf("a closed ticket was read %d times on the timer, want 0", n-reads)
	}
}

// TestATicketThatWaitsOnNoMergeIsNotReadOnTheTimer: an unpublished ticket, one
// published with no pull request on record, one the status says is closed, and
// the main screen read nothing on a tick.
func TestATicketThatWaitsOnNoMergeIsNotReadOnTheTimer(t *testing.T) {
	t.Parallel()
	published := jig2()
	noPR := append(append([]events.Envelope{}, published[:10]...), published[11:]...)
	for _, tc := range []struct {
		name   string
		ticket string
		lines  []events.Envelope
		status string
	}{
		{"unpublished", "JIG-1", jig1(), statusJIG1()},
		{"no pull request on record", "JIG-2", noPR, statusJIG2Done()},
		{"closed", "JIG-2", published, closedStatus(statusJIG2Done())},
		{"the project's list", "", published, statusJIG2Done()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newRig(t, tc.ticket)
			if tc.ticket != "" {
				g.r.answer("status "+tc.ticket+" --store /s", tc.status, 0)
			}
			g.feed(tc.lines...)
			calls := g.r.total()
			g.send(mergeMsg{})
			g.send(mergeMsg{})
			if n := g.r.total() - calls; n != 0 {
				t.Errorf("%d reads on two ticks, want 0", n)
			}
		})
	}
}

// closedStatus is a status with the closed block of a ticket covered by a pull
// request.
func closedStatus(status string) string {
	return status + axi.KV("closed", [][2]string{{"reason", "covered"}, {"by", "develdeco/jig#82"}, {"at", "2026-10-25"}}) + "\n"
}

// TestTheMergeTimerSchedulesItself: zero is two minutes, a negative interval
// schedules nothing, a positive one schedules a tick, and a tick schedules the
// next. The commands are not run: a tick would block for its whole duration.
func TestTheMergeTimerSchedulesItself(t *testing.T) {
	t.Parallel()
	if got := New(Config{}).mergeEvery; got != 2*time.Minute {
		t.Errorf("the default interval is %v, want 2m", got)
	}
	if got := New(Config{MergeEvery: 30 * time.Second}).mergeEvery; got != 30*time.Second {
		t.Errorf("the interval given is %v, want 30s", got)
	}
	if cmd := New(Config{MergeEvery: -1}).mergeAfterTick(); cmd != nil {
		t.Errorf("a negative interval scheduled a tick")
	}
	m := New(Config{MergeEvery: time.Hour})
	if m.mergeAfterTick() == nil {
		t.Errorf("a positive interval scheduled no tick")
	}
	if _, cmd := m.Update(mergeMsg{}); cmd == nil {
		t.Errorf("a tick did not schedule the next")
	}
	if _, cmd := New(Config{MergeEvery: -1}).Update(mergeMsg{}); cmd != nil {
		t.Errorf("a tick of a model with the timer off scheduled the next")
	}
}
