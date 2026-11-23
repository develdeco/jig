package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// gate is a runner that holds every command until the test lets it go: it
// records the commands in the order they began, and each runs to the end only
// when the test releases it.
type gate struct {
	mu      sync.Mutex
	begun   []string
	finish  chan struct{}
	started chan string
	outputs map[string]string
}

func newGate() *gate {
	return &gate{finish: make(chan struct{}), started: make(chan string, 64), outputs: map[string]string{}}
}

func (g *gate) run(args []string) (string, int) {
	line := strings.Join(args, " ")
	g.mu.Lock()
	g.begun = append(g.begun, line)
	out := g.outputs[line]
	g.mu.Unlock()
	g.started <- line
	<-g.finish
	return out, 0
}

// release lets the command that is running end.
func (g *gate) release() { g.finish <- struct{}{} }

// next waits for the next command to begin and says which.
func (g *gate) next(t *testing.T) string {
	t.Helper()
	select {
	case line := <-g.started:
		return line
	case <-time.After(5 * time.Second):
		t.Fatalf("no command began; begun so far: %v", g.began())
		return ""
	}
}

func (g *gate) began() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.begun...)
}

// none says no command began within a moment.
func (g *gate) none(t *testing.T, what string) {
	t.Helper()
	select {
	case line := <-g.started:
		t.Fatalf("%s: %q began", what, line)
	case <-time.After(100 * time.Millisecond):
	}
}

// waiting is how many commands wait their turn.
func waitingIn(q *queue) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiting)
}

// untilWaiting waits for n commands to be waiting in q.
func untilWaiting(t *testing.T, q *queue, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if waitingIn(q) == n {
			return
		}
	}
	t.Fatalf("%d commands wait, want %d", waitingIn(q), n)
}

// ask runs a command in the queue on a goroutine and gives what it printed.
func ask(q *queue, c class, line string) <-chan string {
	out := make(chan string, 1)
	go func() {
		got, _ := q.do(c, strings.Fields(line))
		out <- got
	}()
	return out
}

// TestAKeysCommandRunsBeforeBackgroundReads: of the commands waiting, a key's
// runs first, then the screen the person opened, then the reads that keep the
// rest warm, each class in the order it was asked.
func TestAKeysCommandRunsBeforeBackgroundReads(t *testing.T) {
	t.Parallel()
	g := newGate()
	q := &queue{run: g.run}
	first := ask(q, classBackground, "next --roster")
	if got := g.next(t); got != "next --roster" {
		t.Fatalf("began %q first", got)
	}
	// While the roster runs, a background read, a read of the opened screen
	// and two keys' commands wait, asked in that order.
	ask(q, classBackground, "status T-2")
	untilWaiting(t, q, 1)
	ask(q, classOpened, "status T-1")
	untilWaiting(t, q, 2)
	ask(q, classKey, "answer T-1 q-1 yes")
	untilWaiting(t, q, 3)
	ask(q, classKey, "triage T-1 r1-f1 keep")
	untilWaiting(t, q, 4)

	g.release()
	<-first
	var order []string
	for i := 0; i < 4; i++ {
		order = append(order, g.next(t))
		g.release()
	}
	want := []string{"answer T-1 q-1 yes", "triage T-1 r1-f1 keep", "status T-1", "status T-2"}
	if strings.Join(order, " | ") != strings.Join(want, " | ") {
		t.Errorf("the commands ran in the order\n  %s\nwant\n  %s", strings.Join(order, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestTheOpenedScreensReadRunsBeforeBackgroundReads: with a roster read in
// flight and other reads waiting, enter on a ticket draws its screen at once
// and its status read is the next command to run.
func TestTheOpenedScreensReadRunsBeforeBackgroundReads(t *testing.T) {
	t.Parallel()
	g := newGate()
	cfg := quietConfig()
	cfg.Run = g.run
	cfg.Scope = []string{"--store", "/s"}
	m := New(cfg)
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("T-1"), listed("T-2"), listed("T-3")}, seq: 1})
	m.rosterBusy = false

	// The roster is being read again, and the status of another ticket is kept
	// warm behind it.
	started := make(chan struct{})
	go func() { close(started); m.loadRoster()() }()
	<-started
	if got := g.next(t); got != "next --json --roster --store /s" {
		t.Fatalf("began %q, want the roster read", got)
	}
	q := queueOf(t, m)
	go func() { m.refreshStatus("T-3")() }()
	untilWaiting(t, q, 1)

	// Enter on the first ticket: its screen is drawn at once, with the roster
	// read still running.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.top(); got.screen != screenTicket || got.ticket != "T-1" {
		t.Fatalf("enter opened %+v, want the screen of T-1", got)
	}
	if frame := m.View(); !strings.Contains(frame, "T-1") || strings.Contains(frame, "reading the store") {
		t.Errorf("the ticket's screen is not drawn while the roster is read:\n%s", frame)
	}
	runCmds(cmd)
	untilWaiting(t, q, 2)

	g.release() // the roster read ends
	if got := g.next(t); got != "status T-1 --store /s" {
		t.Errorf("the next command was %q, want the status of the ticket that was opened", got)
	}
	g.release()
	if got := g.next(t); got != "status T-3 --store /s" {
		t.Errorf("then %q, want the status kept warm", got)
	}
	g.release()
}

// queueOf is the queue a model's runner is. It is found through the closure's
// receiver by asking the model's runner for a command it can only run through
// the queue, so a test needs no field for it.
func queueOf(t *testing.T, m *Model) *queue {
	t.Helper()
	if m.queue == nil {
		t.Fatal("the model keeps no queue")
	}
	return m.queue
}

// runCmds runs a command and the ones in its batch, each on a goroutine, and
// drops what they report.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				runCmds(c)
			}
		}
	}()
}

// TestABackgroundReadIsNeverQueuedTwice: a read asked for while the same line
// waits adds nothing and takes the answer of the one waiting; asked for while it
// runs it is asked again once, after it, so what changed meanwhile is read.
func TestABackgroundReadIsNeverQueuedTwice(t *testing.T) {
	t.Parallel()
	g := newGate()
	g.outputs["next --roster"] = "the roster"
	q := &queue{run: g.run}
	running := ask(q, classBackground, "next --roster")
	g.next(t)

	var asked []<-chan string
	for i := 0; i < 5; i++ {
		asked = append(asked, ask(q, classBackground, "next --roster"))
		if i == 0 {
			untilWaiting(t, q, 1)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if got := waitingIn(q); got != 1 {
		t.Errorf("%d reads wait for 5 asks of one line, want one", got)
	}
	// A key's command of the same words is not merged with them.
	ask(q, classKey, "next --roster")
	untilWaiting(t, q, 2)

	g.release()
	<-running
	// The key's command is the next, the one read that waits follows it.
	for i := 0; i < 2; i++ {
		g.next(t)
		g.release()
	}
	for i, ch := range asked {
		select {
		case got := <-ch:
			if got != "the roster" {
				t.Errorf("ask %d got %q", i, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("ask %d was never answered", i)
		}
	}
	g.none(t, "after the read that waited")
	if got := g.began(); len(got) != 3 {
		t.Errorf("%d commands ran, want the one running, the one that waited and the key's: %v", len(got), got)
	}
}

// TestARunningReadIsNotInterrupted: a key's command that comes in while a read
// runs waits for it, and the read ends with its own output.
func TestARunningReadIsNotInterrupted(t *testing.T) {
	t.Parallel()
	g := newGate()
	g.outputs["status T-1"] = "its status"
	q := &queue{run: g.run}
	read := ask(q, classOpened, "status T-1")
	g.next(t)
	key := ask(q, classKey, "gate T-1")
	untilWaiting(t, q, 1)
	g.none(t, "while the read runs")
	g.release()
	select {
	case got := <-read:
		if got != "its status" {
			t.Errorf("the read printed %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end")
	}
	if got := g.next(t); got != "gate T-1" {
		t.Errorf("then %q began", got)
	}
	g.release()
	<-key
}

// TestABurstOfStoreCommitsReadsTheRosterOnceMore: while a roster read runs, any
// number of store commits cost one more read once it ends, never one for each
// commit, and the rows of the new read replace the old where they stand.
func TestABurstOfStoreCommitsReadsTheRosterOnceMore(t *testing.T) {
	t.Parallel()
	g := newGate()
	g.outputs["next --json --roster --store /s"] = `{"tickets":[{"id":"T-1","title":"One","state":"ready"},{"id":"T-2","title":"Two","state":"ready"}]}` + "\n"
	cfg := quietConfig()
	cfg.Run = g.run
	cfg.Scope = []string{"--store", "/s"}
	cfg.IdleTick = -1
	head := "h0"
	cfg.StoreHead = func() (string, error) { return head, nil }
	now := rigNow
	m := New(cfg)
	m.now = func() time.Time { return now }

	msgs := make(chan tea.Msg, 16)
	var spawn func(cmd tea.Cmd)
	spawn = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() {
			switch msg := cmd().(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					spawn(c)
				}
			case nil:
			default:
				msgs <- msg
			}
		}()
	}
	take := func() {
		t.Helper()
		select {
		case msg := <-msgs:
			_, cmd := m.Update(msg)
			spawn(cmd)
		case <-time.After(5 * time.Second):
			t.Fatal("no message came back")
		}
	}

	// The first roster read, and the baseline head.
	spawn(m.loadRoster())
	g.next(t)
	g.release()
	take()
	if !m.w.rosterRead {
		t.Fatal("the roster was not taken")
	}
	_, cmd := m.Update(idleMsg{}) // the baseline head
	spawn(cmd)
	now = now.Add(10 * time.Second)

	// A commit moves the head: a read begins, and holds.
	head = "h1"
	_, cmd = m.Update(idleMsg{})
	spawn(cmd)
	if got := g.next(t); got != "next --json --roster --store /s" {
		t.Fatalf("a store commit began %q", got)
	}
	// A burst of commits while it runs: no read begins for any of them.
	for i := 2; i < 10; i++ {
		head = "h" + string(rune('0'+i))
		now = now.Add(time.Second)
		_, cmd = m.Update(idleMsg{})
		spawn(cmd)
	}
	g.none(t, "during the burst")

	g.release()
	take() // the read's rows
	now = now.Add(10 * time.Second)
	_, cmd = m.Update(idleMsg{})
	spawn(cmd)
	if got := g.next(t); got != "next --json --roster --store /s" {
		t.Fatalf("after the read, %q began, want one more roster read", got)
	}
	g.release()
	take()
	now = now.Add(10 * time.Second)
	_, cmd = m.Update(idleMsg{})
	spawn(cmd)
	g.none(t, "once the burst was read")
	if got := len(g.began()); got != 3 {
		t.Errorf("%d roster reads for a first read and a burst of commits, want 3: %v", got, g.began())
	}
}
