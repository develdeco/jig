package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// bigStore is a model whose roster lists n tickets, each with history journal
// lines the stream replayed; status says how many have had their status read.
func bigStore(t testing.TB, n, history, status int) *Model {
	t.Helper()
	cfg := Config{Run: func([]string) (string, int) { return "", 0 }, Zone: time.UTC, Width: 100, Height: 40, Project: "demo",
		Now: func() time.Time { return rigNow }}
	m := New(cfg)
	var rows []rosterRow
	var envs []events.Envelope
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("T-%04d", i)
		rows = append(rows, listed(id))
		for h := 0; h < history; h++ {
			ts := fmt.Sprintf("2026-10-23T09:%02d:%02dZ", (h/60)%60, h%60)
			switch h % 3 {
			case 0:
				envs = append(envs, jl(ts, id, "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "", "", "")))
			case 1:
				envs = append(envs, jl(ts, id, "a", journal.EventOracle, with("", sha('a'), "fail", "TestA", "", "")))
			default:
				envs = append(envs, jl(ts, id, "a", journal.EventQuestion, with("s-builder", "", "", "Which?", fmt.Sprintf("q-%d", h), "agent")))
			}
		}
	}
	m.w.apply(envs)
	m.w.setRoster(rows)
	for i := 0; i < status; i++ {
		m.w.tickets[fmt.Sprintf("T-%04d", i)].status = parseStatus(statusJIG1())
	}
	return m
}

// listed is a roster row of a ticket in its build, as the verb lists it.
func listed(id string) rosterRow {
	return rosterRow{ID: id, Title: "Ticket " + id, State: "ready", Digest: &digestInfo{
		Kind: "task", Current: "build", Ready: true,
		Phases: []digestPhase{
			{Name: "brief", State: "done", Text: "approved · 2 sections, 2 slices"},
			{Name: "build", State: "current", Text: "1 of 2 green"},
			{Name: "review", State: "pending", Text: "after the build"},
			{Name: "publish", State: "pending", Text: "after a clean review"},
			{Name: "land", State: "pending", Text: "after the publish"},
		},
		Slices: &digestSlices{Total: 2, Green: 1, Building: 1},
	}}
}

// quietConfig is a model's configuration with a runner that prints nothing.
func quietConfig() Config {
	return Config{Run: func([]string) (string, int) { return "", 0 }, Zone: time.UTC, Width: 100, Height: 24, Project: "demo",
		Now: func() time.Time { return rigNow }}
}

// TestMainDrawsNothingBeforeTheRoster: with a runner, main says it is reading
// the store from the first frame until the roster lands, whatever the stream's
// replay has named by then, and its header counts nothing; the first rows it
// draws are the roster's, in its order. A model with no runner draws the
// stream, as it always did.
func TestMainDrawsNothingBeforeTheRoster(t *testing.T) {
	t.Parallel()
	m := New(quietConfig())
	m.w.apply(append(jig1(), jig2()...))
	frame := m.View()
	if !strings.Contains(frame, "reading the store…") {
		t.Errorf("the first frame does not say the store is being read:\n%s", frame)
	}
	for _, not := range []string{"JIG-1", "JIG-2", "ticket", "no tickets yet"} {
		if strings.Contains(frame, not) {
			t.Errorf("main before the roster says %q:\n%s", not, frame)
		}
	}

	// The roster lands: the complete list, in the roster's order, not the
	// replay's.
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("JIG-2"), listed("JIG-9"), listed("JIG-1")}, seq: 1})
	frame = m.View()
	at1, at2, at9 := strings.Index(frame, "JIG-1 "), strings.Index(frame, "JIG-2 "), strings.Index(frame, "JIG-9 ")
	if at1 < 0 || at2 < 0 || at9 < 0 || !(at2 < at9 && at9 < at1) {
		t.Errorf("main after the roster is not its list in its order (JIG-2, JIG-9, JIG-1):\n%s", frame)
	}
	if !strings.Contains(frame, "3 tickets") {
		t.Errorf("the header does not count the roster's tickets:\n%s", frame)
	}

	// With no runner nothing reads the store: the stream is what there is.
	cfg := quietConfig()
	cfg.Run = nil
	bare := New(cfg)
	bare.w.apply(jig1())
	if got := bare.View(); !strings.Contains(got, "JIG-1") || strings.Contains(got, "reading the store") {
		t.Errorf("a model with no runner does not draw the stream:\n%s", got)
	}
}

// TestMainKeepsRosterOrderUnderTheCursor: a later roster read replaces the rows
// where they stand, in its order, and the cursor stays on the ticket it was on,
// not on the number of its row.
func TestMainKeepsRosterOrderUnderTheCursor(t *testing.T) {
	t.Parallel()
	m := New(quietConfig())
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("T-1"), listed("T-2"), listed("T-3")}, seq: 1})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if sel := m.selected(); sel == nil || sel.ticket != "T-2" {
		t.Fatalf("the cursor is on %+v, want T-2", sel)
	}
	// Two tickets come in ahead of it and another moves behind it.
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("T-9"), listed("T-8"), listed("T-3"), listed("T-2"), listed("T-1")}, seq: 2})
	if sel := m.selected(); sel == nil || sel.ticket != "T-2" {
		t.Errorf("the cursor is on %+v after the list changed, want it on T-2", sel)
	}
	var order []string
	for _, ref := range m.page().refs() {
		if ref.kind == rowTicket {
			order = append(order, ref.ticket)
		}
	}
	if got := strings.Join(order, " "); got != "T-9 T-8 T-3 T-2 T-1" {
		t.Errorf("the rows are %s, want the roster's order", got)
	}
	// A list that no longer has the ticket leaves the cursor on a row.
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("T-9")}, seq: 3})
	if sel := m.selected(); sel == nil {
		t.Errorf("the cursor is nowhere on a list of one ticket")
	}
}

// TestMainAndChartRowsComputeNoClocks: the project's list and a chart's rows
// show no time, so drawing them computes no work clock, however many tickets
// they list; the ticket screen, which shows times, does, once for each second.
func TestMainAndChartRowsComputeNoClocks(t *testing.T) {
	t.Parallel()
	m := bigStore(t, 40, 30, 5)
	m.w.setCharts([]chartListing{{Name: "run", Title: "A run", Tickets: []string{"T-0000", "T-0001", "T-0002", "T-0003"}}})
	m.View()
	if m.clockRuns != 0 {
		t.Errorf("drawing main computed %d work clocks, want none", m.clockRuns)
	}
	m.nav = append(m.nav, navEntry{screen: screenChart, chart: "run"})
	m.View()
	if m.clockRuns != 0 {
		t.Errorf("drawing a chart computed %d work clocks, want none", m.clockRuns)
	}
	m.nav = append(m.nav, navEntry{screen: screenTicket, ticket: "T-0000"})
	m.View()
	if m.clockRuns == 0 {
		t.Errorf("the ticket screen shows times and computed no work clock")
	}
	was := m.clockRuns
	m.View()
	if m.clockRuns != was {
		t.Errorf("a second frame in the same second computed %d more clocks", m.clockRuns-was)
	}
}

// TestAFramesWorkDoesNotGrowWithHistory: what a frame of main visits of the
// tickets' journals is nothing once a ticket's lines have been read, whether it
// has 10 lines or 1,000, and a line for one ticket costs the reading of that
// ticket alone: a key, a tick and a batch of the stream cost what they draw.
func TestAFramesWorkDoesNotGrowWithHistory(t *testing.T) {
	t.Parallel()
	scanned := func(m *Model) (n int) {
		for _, tk := range m.w.tickets {
			n += tk.scanned
		}
		return n
	}
	for _, history := range []int{10, 1000} {
		m := bigStore(t, 20, history, 20)
		m.View()
		before := scanned(m)
		if before == 0 {
			t.Fatalf("the first frame of tickets of %d lines read none", history)
		}
		for j := 0; j < 5; j++ {
			m.View()
			_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		}
		if got := scanned(m) - before; got != 0 {
			t.Errorf("tickets of %d lines: frames after the first visited %d lines, want none", history, got)
		}
	}

	m := bigStore(t, 20, 200, 20)
	m.View()
	before := scanned(m)
	m.w.apply([]events.Envelope{jl(at(30, 0), "T-0003", "a", journal.EventClaim, with("s-builder", "", "", "", "", ""))})
	m.View()
	if got := scanned(m) - before; got == 0 || got > 8*201 {
		t.Errorf("a line of one ticket made the frame visit %d lines, want the few readings of its 201", got)
	}
}
