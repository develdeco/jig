package slot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// patience bounds how long a test waits for something that must happen. It is
// not asserted on: a loaded machine only makes a test slower.
const patience = 60 * time.Second

func req(project, who string) Request { return Request{Project: project, Who: who} }

func take(t *testing.T, root, project, who string) func() {
	t.Helper()
	rel, err := Take(context.Background(), root, req(project, who), nil)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	return rel
}

// ask starts a Take in the background. waiting closes when the run says it
// waits; got delivers the release once it is admitted.
func ask(t *testing.T, root string, r Request) (waiting <-chan struct{}, got <-chan func()) {
	t.Helper()
	w := make(chan struct{})
	g := make(chan func(), 1)
	go func() {
		rel, err := Take(context.Background(), root, r, func() { close(w) })
		if err != nil {
			t.Error(err)
		}
		g <- rel
	}()
	return w, g
}

func recv[T any](t *testing.T, c <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(patience):
		t.Fatalf("never happened: %s", what)
		panic("unreachable")
	}
}

func notYet[T any](t *testing.T, c <-chan T, what string) {
	t.Helper()
	select {
	case <-c:
		t.Fatalf("happened too early: %s", what)
	default:
	}
}

// TestTwoRunsOfOneProjectAreOrdered: the second asker waits, says so once, and
// is admitted when the first lets go.
func TestTwoRunsOfOneProjectAreOrdered(t *testing.T) {
	root := t.TempDir()
	first := take(t, root, "p", "")
	waiting, got := ask(t, root, req("p", ""))
	recv(t, waiting, "the second asker says it waits")
	notYet(t, got, "the second asker has the share while the first holds it")
	first()
	recv(t, got, "the second asker is admitted after the release")()
}

// TestTwoProjectsShareOneBudget: a run of another project waits for a heavy
// run of the first, since the budget is the machine's.
func TestTwoProjectsShareOneBudget(t *testing.T) {
	root := t.TempDir()
	first := take(t, root, "p", "T-1/a")
	waiting, got := ask(t, root, req("q", "T-2/gate"))
	recv(t, waiting, "the other project's run says it waits")
	notYet(t, got, "the other project's run has a share while the first holds the machine")
	first()
	recv(t, got, "the other project's run is admitted after the release")()
}

// TestAPinnedProjectIsCountedNotBudgeted: a project that pins 2 holds two at
// once whatever another project holds, and a third waits for its own count.
func TestAPinnedProjectIsCountedNotBudgeted(t *testing.T) {
	root := t.TempDir()
	other := take(t, root, "q", "Q-1/a")
	pin := func(who string) Request { return Request{Project: "p", Who: who, Pinned: 2} }
	a, err := Take(context.Background(), root, pin("A"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Take(context.Background(), root, pin("B"), nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting, got := ask(t, root, pin("C"))
	recv(t, waiting, "the third pinned run says it waits")
	notYet(t, got, "the third pinned run has a share of two")
	a()
	recv(t, got, "the third pinned run is admitted when one of two lets go")()
	b()
	other()
}

// TestAPinnedWaiterDoesNotHoldUpTheBudget: a pinned run that waits for its
// count is not ahead of anyone on the budget, so a run that waits on the
// budget is admitted past it as soon as the holder leaves.
func TestAPinnedWaiterDoesNotHoldUpTheBudget(t *testing.T) {
	root := t.TempDir()
	holder := take(t, root, "q", "Q-1/a")
	p1, err := Take(context.Background(), root, Request{Project: "p", Who: "P-1/a", Pinned: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pw, pgot := ask(t, root, Request{Project: "p", Who: "P-2/a", Pinned: 1})
	recv(t, pw, "the pinned run waits for its count")
	bw, bgot := ask(t, root, req("r", "R-1/a"))
	recv(t, bw, "the budget run waits behind the holder")
	notYet(t, bgot, "the budget run has the machine while it is held")
	holder()
	recv(t, bgot, "the budget run is admitted past the pinned waiter")()
	notYet(t, pgot, "the pinned waiter has a share of one while its project holds it")
	p1()
	recv(t, pgot, "the pinned waiter is admitted when its project's run ends")()
}

// TestAReleasedShareIsFree: a release (also repeated) frees the share.
func TestAReleasedShareIsFree(t *testing.T) {
	root := t.TempDir()
	rel := take(t, root, "p", "")
	rel()
	rel()
	take(t, root, "p", "")()
}

// TestCancelEndsTheWait: a cancelled context returns the wait, and the run
// leaves the queue.
func TestCancelEndsTheWait(t *testing.T) {
	root := t.TempDir()
	defer take(t, root, "p", "")()
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{})
	done := make(chan error, 1)
	go func() { _, err := Take(ctx, root, req("p", "T-2/gate"), func() { close(waiting) }); done <- err }()
	recv(t, waiting, "the run waits")
	cancel()
	if err := recv(t, done, "the cancelled wait returns"); err == nil {
		t.Fatal("a cancelled wait returned a share")
	}
	if s, _ := Read(root); len(s.Waiters) != 0 {
		t.Errorf("a cancelled run still waits: %+v", s)
	}
}

// TestBadArgumentsAreRefused: a project name that is a path, or a negative
// pinned count.
func TestBadArgumentsAreRefused(t *testing.T) {
	root := t.TempDir()
	if _, err := Take(context.Background(), root, Request{Project: "p", Pinned: -1}, nil); err == nil {
		t.Error("a negative pinned count accepted")
	}
	for _, name := range []string{"", "..", "a/b", `a\b`} {
		if _, err := Take(context.Background(), root, req(name, ""), nil); err == nil {
			t.Errorf("project name %q accepted", name)
		}
	}
}

// TestMain turns the test binary into the asker of the cross-process tests
// when SLOT_TEST_ROOT is set: it asks for a share of the queue under that
// root, prints "waiting" when it has to wait and "admitted" when it has the
// share, and gives the share back when its stdin closes.
func TestMain(m *testing.M) {
	if root := os.Getenv("SLOT_TEST_ROOT"); root != "" {
		who := os.Getenv("SLOT_TEST_WHO")
		rel, err := Take(context.Background(), root, req("p"+who, who), func() { fmt.Println("waiting") })
		if err != nil {
			os.Exit(2)
		}
		fmt.Println("admitted")
		_, _ = io.Copy(io.Discard, os.Stdin)
		rel()
		return
	}
	os.Exit(m.Run())
}

type child struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines *bufio.Reader
}

func startAsker(t *testing.T, root, who string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "SLOT_TEST_ROOT="+root, "SLOT_TEST_WHO="+who)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, stdin: in, lines: bufio.NewReader(out)}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return c
}

// line reads the child's next line, failing the test when it does not come.
func (c *child) line(t *testing.T) string {
	t.Helper()
	got := make(chan string, 1)
	go func() { s, _ := c.lines.ReadString('\n'); got <- strings.TrimSpace(s) }()
	return recv(t, got, "a line from the asker process")
}

func (c *child) kill() { _ = c.cmd.Process.Kill(); _ = c.cmd.Wait() }

// waitersAre waits until the queue says exactly these runs wait, in order.
func waitersAre(t *testing.T, root string, want ...string) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for {
		s, err := Read(root)
		if err == nil && strings.Join(s.Waiters, ",") == strings.Join(want, ",") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %+v, %v; want %v", s.Waiters, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRunsAskedInOrderAreAdmittedInOrder: three real processes ask in order
// while the machine is full and are admitted in that order, whatever order
// they poll in. Each is read before the next may be, so a run admitted out of
// order leaves the test waiting on a line that never comes.
func TestRunsAskedInOrderAreAdmittedInOrder(t *testing.T) {
	root := t.TempDir()
	holder := take(t, root, "h", "H/a")
	var kids []*child
	var who []string
	for _, w := range []string{"A", "B", "C"} {
		c := startAsker(t, root, w)
		if got := c.line(t); got != "waiting" {
			t.Fatalf("asker %s said %q, want waiting", w, got)
		}
		kids = append(kids, c)
		who = append(who, w)
		waitersAre(t, root, who...)
	}
	holder()
	for i, c := range kids {
		if got := c.line(t); got != "admitted" {
			t.Fatalf("asker %s said %q, want admitted", who[i], got)
		}
		s, err := Read(root)
		if err != nil || len(s.Holders) != 1 || s.Holders[0] != who[i] {
			t.Fatalf("after admitting %s: %+v, %v; want it alone holding", who[i], s, err)
		}
		_ = c.stdin.Close()
		_ = c.cmd.Wait()
	}
}

// TestAKilledHolderFreesItsShare: a process that holds the machine and is
// killed outright leaves it free for the next asker.
func TestAKilledHolderFreesItsShare(t *testing.T) {
	root := t.TempDir()
	c := startAsker(t, root, "K")
	if got := c.line(t); got != "admitted" {
		t.Fatalf("holder said %q, want admitted", got)
	}
	waiting, got := ask(t, root, req("p", "T/gate"))
	recv(t, waiting, "the next asker waits on a live holder")
	notYet(t, got, "the next asker has the machine while a live process holds it")
	c.kill()
	recv(t, got, "the next asker is admitted after the holder is killed")()
}

// TestAKilledWaiterIsSkipped: a waiter killed outright is passed over, and the
// run behind it is admitted when the holder leaves.
func TestAKilledWaiterIsSkipped(t *testing.T) {
	root := t.TempDir()
	holder := take(t, root, "h", "H/a")
	c := startAsker(t, root, "W")
	if got := c.line(t); got != "waiting" {
		t.Fatalf("waiter said %q, want waiting", got)
	}
	waiting, got := ask(t, root, req("p", "T/gate"))
	recv(t, waiting, "the run behind it waits")
	c.kill()
	holder()
	recv(t, got, "the run behind a killed waiter is admitted")()
}

// TestReadSaysWhoHoldsAndWhoWaits: a held share shows its holder's name, a live
// waiter shows too, and both vanish when the holder lets go and the waiter
// takes it.
func TestReadSaysWhoHoldsAndWhoWaits(t *testing.T) {
	root := t.TempDir()
	if s, err := Read(root); err != nil || len(s.Holders)+len(s.Waiters) != 0 {
		t.Fatalf("Read of an unused machine = %+v, %v", s, err)
	}
	rel := take(t, root, "p", Who("T-1", "a"))
	waiting, got := ask(t, root, req("q", Who("T-2", "")))
	recv(t, waiting, "the waiter says it waits")
	s, err := Read(root)
	if err != nil || len(s.Holders) != 1 || s.Holders[0] != "T-1/a" || len(s.Waiters) != 1 || s.Waiters[0] != "T-2/gate" {
		t.Fatalf("Read = %+v, %v; want T-1/a holding and T-2/gate waiting", s, err)
	}
	rel()
	r2 := recv(t, got, "the waiter is admitted")
	s, err = Read(root)
	if err != nil || len(s.Holders) != 1 || s.Holders[0] != "T-2/gate" || len(s.Waiters) != 0 {
		t.Fatalf("Read after the hand-over = %+v, %v", s, err)
	}
	r2()
	if s, _ := Read(root); len(s.Holders)+len(s.Waiters) != 0 {
		t.Fatalf("Read after every release = %+v", s)
	}
}

// TestWaitsAreTheJournalsOpenWaitsThatStillWait: a slot-wait line with a live
// waiter behind it is a wait, said in the one line; once slot-taken follows,
// or when the waiter is gone, it is not.
func TestWaitsAreTheJournalsOpenWaitsThatStillWait(t *testing.T) {
	root := t.TempDir()
	defer take(t, root, "p", Who("T-1", "x"))()
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Take(ctx, root, req("p", Who("T-2", "a")), func() { close(waiting) })
	}()
	recv(t, waiting, "the waiter says it waits")
	wait := []journal.Line{{Event: journal.EventSlotWait, Slice: "a"}}
	got, err := Waits(root, "T-2", wait)
	want := "waiting for the suite slot: T-1/x is running its suite (1 waiting)"
	if err != nil || len(got) != 1 || got[0].Slice != "a" || got[0].Line != want {
		t.Fatalf("Waits = %+v, %v; want slice a with %q", got, err, want)
	}
	if got, _ := Waits(root, "T-2", append(wait, journal.Line{Event: journal.EventSlotTaken, Slice: "a"})); len(got) != 0 {
		t.Errorf("a wait followed by slot-taken still shows: %+v", got)
	}
	if got, _ := Waits(root, "T-2", []journal.Line{{Event: journal.EventSlotWait, Slice: "b"}}); len(got) != 0 {
		t.Errorf("a wait nobody is in shows: %+v", got)
	}
	cancel()
	<-done
	if got, _ := Waits(root, "T-2", wait); len(got) != 0 {
		t.Errorf("a waiter that gave up still shows: %+v", got)
	}
}

// TestLineSaysOnlyThatARunWaitsWhenNoHolderIsKnown: the holder and the count
// are named when a holder left its name, and the line is just "waiting for the
// suite slot" when none did.
func TestLineSaysOnlyThatARunWaitsWhenNoHolderIsKnown(t *testing.T) {
	if got, want := Line(Status{Holders: []string{"A/x"}, Waiters: []string{"B/y", "C/z"}}), "waiting for the suite slot: A/x is running its suite (2 waiting)"; got != want {
		t.Errorf("Line = %q, want %q", got, want)
	}
	for _, s := range []Status{{}, {Holders: []string{""}, Waiters: []string{"B/y"}}} {
		if got := Line(s); got != "waiting for the suite slot" {
			t.Errorf("Line(%+v) = %q, want the bare line", s, got)
		}
	}
}
