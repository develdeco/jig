package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// occupy starts a command in q that holds the queue until the test releases it,
// so what is asked after it waits.
func occupy(t *testing.T, g *gate, q *queue) {
	t.Helper()
	ask(q, classBackground, "occupy")
	if got := g.next(t); got != "occupy" {
		t.Fatalf("began %q, want the command that holds the queue", got)
	}
}

// order releases what waits and says the commands in the order they ran.
func order(t *testing.T, g *gate, n int) string {
	t.Helper()
	g.release() // the one that held the queue
	var ran []string
	for i := 0; i < n; i++ {
		ran = append(ran, strings.TrimSuffix(g.next(t), " --store /s"))
		g.release()
	}
	return strings.Join(ran, " | ")
}

// TestAReadOfAScreenLeftWaitsBehindTheOneOpened: a read is of the screen on show
// when the queue picks the next, not when it was asked for. The chart's read and
// each of its tickets' statuses waiting for a chart the person has left run
// after the status of the ticket they have opened, whatever order they were
// asked in.
func TestAReadOfAScreenLeftWaitsBehindTheOneOpened(t *testing.T) {
	t.Parallel()
	g := newGate()
	q := &queue{run: g.run}
	occupy(t, g, q)

	q.show(onShow{chart: "run", chartTickets: []string{"T-1", "T-2", "T-3", "T-4"}})
	askAbout(q, classOpened, about{chart: "run"}, "next run --json")
	untilWaiting(t, q, 1)
	for i, id := range []string{"T-1", "T-2", "T-3", "T-4"} {
		askAbout(q, classOpened, about{ticket: id}, "status "+id)
		untilWaiting(t, q, 2+i)
	}
	// The person goes back and opens another ticket, whose status is asked for.
	q.show(onShow{ticket: "T-5"})
	askAbout(q, classOpened, about{ticket: "T-5"}, "status T-5")
	untilWaiting(t, q, 6)

	want := "status T-5 | next run --json | status T-1 | status T-2 | status T-3 | status T-4"
	if got := order(t, g, 6); got != want {
		t.Errorf("the commands ran in the order\n  %s\nwant\n  %s", got, want)
	}
}

// TestAQueuedReadOfTheOpenedTicketRunsNext: a status already waiting, kept warm
// behind others, runs next once the person opens its ticket: it is raised, not
// only marked to be read again.
func TestAQueuedReadOfTheOpenedTicketRunsNext(t *testing.T) {
	t.Parallel()
	g := newGate()
	q := &queue{run: g.run}
	occupy(t, g, q)

	for i, id := range []string{"T-1", "T-2", "T-3", "T-4"} {
		askAbout(q, classOpened, about{ticket: id}, "status "+id)
		untilWaiting(t, q, 1+i)
	}
	q.show(onShow{ticket: "T-4"})
	if got, want := order(t, g, 4), "status T-4 | status T-1 | status T-2 | status T-3"; got != want {
		t.Errorf("the commands ran in the order\n  %s\nwant\n  %s", got, want)
	}
}

// classModel is a model of a store of five tickets and the chart "run" of the
// first two, over the gate.
func classModel(t *testing.T, g *gate) *Model {
	t.Helper()
	cfg := quietConfig()
	cfg.Run = g.run
	cfg.Scope = []string{"--store", "/s"}
	m := New(cfg)
	rows := []rosterRow{listed("T-1"), listed("T-2"), listed("T-3"), listed("T-9")}
	_, _ = m.Update(rosterMsg{rows: rows, charts: []chartListing{{Name: "run", Title: "A run", Tickets: []string{"T-1", "T-2"}}}, seq: 1})
	m.rosterBusy = false
	return m
}

// start runs what a model asks for on goroutines, a batch's commands as well.
func start(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				start(c)
			}
		}
	}()
}

// TestTheScreensCommandsAreQueuedInTheirClass: with a roster read waiting, the
// commands the model asks for run in their class: a key's command first, then
// the reads of the ticket on show (its status, its brief) or of the chart on
// show (the chart and its tickets' statuses), then the roster, then the reads of
// anything else.
func TestTheScreensCommandsAreQueuedInTheirClass(t *testing.T) {
	t.Parallel()

	g := newGate()
	m := classModel(t, g)
	occupy(t, g, m.queue)
	m.nav = append(m.nav, navEntry{screen: screenTicket, ticket: "T-1"})
	m.syncScreen()
	for i, cmd := range []tea.Cmd{
		m.loadRoster(),
		m.refreshStatus("T-2"),
		m.refreshStatus("T-1"),
		m.loadBrief("T-1"),
		m.exec("answer", "T-1", []string{"answer", "T-1", "q-1", "yes", "--store", "/s"}),
	} {
		start(cmd)
		untilWaiting(t, m.queue, 1+i)
	}
	want := "answer T-1 q-1 yes | status T-1 | brief show T-1 --text | next --json --roster | status T-2"
	if got := order(t, g, 5); got != want {
		t.Errorf("on a ticket's screen the commands ran in the order\n  %s\nwant\n  %s", got, want)
	}

	g = newGate()
	m = classModel(t, g)
	occupy(t, g, m.queue)
	m.nav = append(m.nav, navEntry{screen: screenChart, chart: "run"})
	m.syncScreen()
	for i, cmd := range []tea.Cmd{
		m.loadRoster(),
		m.refreshStatus("T-9"),
		m.loadChart("run"),
		m.refreshStatus("T-2"),
	} {
		start(cmd)
		untilWaiting(t, m.queue, 1+i)
	}
	want = "next run --json | status T-2 | next --json --roster | status T-9"
	if got := order(t, g, 4); got != want {
		t.Errorf("on a chart's screen the commands ran in the order\n  %s\nwant\n  %s", got, want)
	}
}
