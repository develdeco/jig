package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// backwards is envs in the reverse of the order they were written in, so a
// screen has to put them in time order itself.
func backwards(envs []events.Envelope) []events.Envelope {
	out := make([]events.Envelope, len(envs))
	for i, e := range envs {
		out[len(envs)-1-i] = e
	}
	return out
}

// panelRows are the rows inside the panel of a frame titled title, without the
// box, or nil when the frame has no such panel.
func panelRows(frame, title string) []string {
	var rows []string
	in := false
	for _, l := range strings.Split(frame, "\n") {
		switch {
		case strings.Contains(l, "╭─ "+title+" "):
			in = true
		case in && strings.Contains(l, "╰"):
			return rows
		case in:
			row := strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│"))
			if strings.HasPrefix(row, "time ") {
				// The names of the columns are no row of the list.
				continue
			}
			rows = append(rows, row)
		}
	}
	return rows
}

var clockAtStart = regexp.MustCompile(`^(\d\d:\d\d:\d\d) `)

// clocksOf are the times that start the rows, in the order the rows stand.
func clocksOf(rows []string) []string {
	var out []string
	for _, r := range rows {
		if m := clockAtStart.FindStringSubmatch(r); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// newestOnTop fails the test unless clocks never rise from a row to the next
// and there are at least min of them.
func newestOnTop(t *testing.T, what string, clocks []string, min int) {
	t.Helper()
	if len(clocks) < min {
		t.Fatalf("%s: %d timed rows, want at least %d: %v", what, len(clocks), min, clocks)
	}
	for i := 1; i < len(clocks); i++ {
		if clocks[i] > clocks[i-1] {
			t.Errorf("%s: %s is below %s, so the rows do not read newest first: %v", what, clocks[i], clocks[i-1], clocks)
			return
		}
	}
}

// tailRows are the rows of the tail pane of a frame.
func tailRows(frame string) []string {
	var rows []string
	in := false
	for _, l := range strings.Split(frame, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(l), "tail ─"):
			in = true
		case in && strings.HasPrefix(l, "──"):
			return rows
		case in:
			rows = append(rows, strings.TrimSpace(l))
		}
	}
	return rows
}

// TestAStepsActivityReadsNewestFirst: lines delivered out of order are drawn
// newest first on the review and the publish screens, whatever order they came
// in, and the ticket's own Activity agrees.
func TestAStepsActivityReadsNewestFirst(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		keys   []string
		screen string
		title  string
		first  string
	}{
		"review":  {[]string{"up", "up", "enter"}, "review", "Review activity", "09:13:30"},
		"publish": {[]string{"up", "enter"}, "publish", "Publish activity", "09:14:21"},
	} {
		g := newRig(t, "JIG-2")
		g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
		g.send(tea.WindowSizeMsg{Width: 100, Height: 50})
		lines := jig2()
		// The later half first, then the earlier: every line arrives before one
		// that was written earlier.
		g.feed(append(backwards(lines[6:]), backwards(lines[:6])...)...)
		g.press(tc.keys...)
		if g.m.Screen() != tc.screen {
			t.Fatalf("%s: on %s", name, g.m.Screen())
		}
		rows := panelRows(g.view(), tc.title)
		newestOnTop(t, name, clocksOf(rows), 3)
		if got := clocksOf(rows)[0]; got != tc.first {
			t.Errorf("%s: the first row is %s, want %s:\n%s", name, got, tc.first, g.view())
		}
	}
}

// TestTheTailReadsNewestFirst: the tail of a ticket, of a chart and of the
// project lists the line written last on top, though the lines came in the
// other way round.
func TestTheTailReadsNewestFirst(t *testing.T) {
	t.Parallel()
	ticket := newRig(t, "JIG-1")
	ticket.feed(backwards(jig1())...).press("t")
	newestOnTop(t, "ticket", clocksOf(tailRows(ticket.view())), 5)
	if first := clocksOf(tailRows(ticket.view()))[0]; first != "09:02:01" {
		t.Errorf("ticket: the first row of the tail is %s, want 09:02:01", first)
	}

	chart := newRig(t, "")
	lines, status := chartTickets()
	answerChart(chart, chartRoster(checkoutListing()), "checkout", checkoutJSON(), status)
	chart.feed(backwards(lines)...)
	chart.openChart("checkout").press("t")
	newestOnTop(t, "chart", clocksOf(tailRows(chart.view())), 5)

	main := newRig(t, "")
	main.feed(jig2()...).feed(jig1()...).press("t") // JIG-1's lines come after JIG-2's, and are older
	rows := tailRows(main.view())
	newestOnTop(t, "main", clocksOf(rows), 5)
	if first := clocksOf(rows)[0]; first != "09:14:21" {
		t.Errorf("main: the first row of the tail is %s, want 09:14:21", first)
	}
}

// TestACutTailEndsWithTheCountOfTheLinesItDoesNotShow: a tail with more lines
// than its pane keeps the newest and ends with the number of the others.
func TestACutTailEndsWithTheCountOfTheLinesItDoesNotShow(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	for i := 0; i < 40; i++ {
		g.feed(jl(at(i, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	}
	g.press("t")
	rows := tailRows(g.view())
	timed := len(clocksOf(rows))
	if timed == 0 || timed == 40 || rows[len(rows)-1] != fmt.Sprintf("↓ %d older", 40-timed) {
		t.Errorf("the tail shows %d of 40 lines and ends with %q:\n%s", timed, rows[len(rows)-1], strings.Join(rows, "\n"))
	}
	newestOnTop(t, "tail", clocksOf(rows), 3)
	if first := clocksOf(rows)[0]; first != "09:39:00" {
		t.Errorf("the tail starts at %s, want 09:39:00", first)
	}
}

// TestACutActivityCountsTheOlderRowsBelow: a panel too short for its lines
// keeps the newest, says "latest N of M" and ends with "↓ M-N older", and its
// rows and that one stay within the room the panel had; one that fits has none.
func TestACutActivityCountsTheOlderRowsBelow(t *testing.T) {
	t.Parallel()
	frames := map[string]string{}
	frames["ticket"] = func() string {
		g := newRig(t, "JIG-1")
		return g.feed(jig1()...).view()
	}()
	frames["step"] = func() string {
		g := newRig(t, "JIG-2")
		g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
		return g.feed(jig2()...).press("up", "enter").view()
	}()
	frames["chart"] = chartScreenOfHeight(t, 30).view()
	titles := map[string]string{"ticket": "Activity", "step": "Publish activity", "chart": "Activity"}
	for name, frame := range frames {
		var latest, of int
		cut := regexp.MustCompile(`latest (\d+) of (\d+)`).FindStringSubmatch(frame)
		if cut == nil {
			t.Errorf("%s: the panel is not cut:\n%s", name, frame)
			continue
		}
		fmt.Sscanf(cut[1], "%d", &latest)
		fmt.Sscanf(cut[2], "%d", &of)
		rows := panelRows(frame, titles[name])
		if len(rows) != latest+1 {
			t.Errorf("%s: %d rows, want the %d lines and the older row:\n%s", name, len(rows), latest, frame)
			continue
		}
		if want := fmt.Sprintf("↓ %d older", of-latest); rows[len(rows)-1] != want {
			t.Errorf("%s: the last row is %q, want %q:\n%s", name, rows[len(rows)-1], want, frame)
		}
	}

	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 50})
	g.feed(jig2()...).press("up", "enter")
	if v := g.view(); strings.Contains(v, "older") || strings.Contains(v, "latest ") {
		t.Errorf("a panel that fits is cut:\n%s", v)
	}
}
