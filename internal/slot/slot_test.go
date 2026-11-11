package slot

import (
	"bufio"
	"context"
	"fmt"
	"github.com/develdeco/jig/internal/journal"
	"os"
	"os/exec"
	"testing"
	"time"
)

func take(t *testing.T, root string, n int) func() {
	t.Helper()
	rel, err := Take(context.Background(), root, "p", n, "", nil)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	return rel
}

// TestTwoHoldersOfOneSlotAreOrdered: the second taker waits, says so once,
// and gets the slot when the first lets go.
func TestTwoHoldersOfOneSlotAreOrdered(t *testing.T) {
	root := t.TempDir()
	first := take(t, root, 1)

	waits := make(chan struct{}, 4)
	got := make(chan func(), 1)
	go func() {
		rel, err := Take(context.Background(), root, "p", 1, "", func() { waits <- struct{}{} })
		if err != nil {
			t.Error(err)
		}
		got <- rel
	}()
	select {
	case <-waits:
	case <-time.After(5 * time.Second):
		t.Fatal("the second taker never said it waits")
	}
	select {
	case <-got:
		t.Fatal("the second taker got the slot while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	first()
	select {
	case rel := <-got:
		rel()
	case <-time.After(5 * time.Second):
		t.Fatal("the second taker never got the slot after the release")
	}
	if len(waits) != 0 {
		t.Errorf("onWait ran %d more times, want once", len(waits))
	}
}

// TestTwoSlotsLetTwoHold: with N=2 two hold at once and a third waits.
func TestTwoSlotsLetTwoHold(t *testing.T) {
	root := t.TempDir()
	a := take(t, root, 2)
	b := take(t, root, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Take(ctx, root, "p", 2, "", nil); err == nil {
		t.Fatal("a third holder took a slot of two")
	}
	a()
	c := take(t, root, 2)
	b()
	c()
}

// TestAReleasedSlotIsFree: a release (also repeated) frees the slot, and
// another project's slot is separate.
func TestAReleasedSlotIsFree(t *testing.T) {
	root := t.TempDir()
	rel := take(t, root, 1)
	other, err := Take(context.Background(), root, "q", 1, "", nil)
	if err != nil {
		t.Fatalf("another project's slot: %v", err)
	}
	other()
	rel()
	rel()
	take(t, root, 1)()
}

// TestCancelEndsTheWait: a cancelled context returns the wait.
func TestCancelEndsTheWait(t *testing.T) {
	root := t.TempDir()
	defer take(t, root, 1)()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := Take(ctx, root, "p", 1, "", nil); done <- err }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled wait returned a slot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the wait")
	}
}

// TestBadArgumentsAreRefused: no slots, or a project name that is a path.
func TestBadArgumentsAreRefused(t *testing.T) {
	root := t.TempDir()
	if _, err := Take(context.Background(), root, "p", 0, "", nil); err == nil {
		t.Error("zero slots accepted")
	}
	for _, name := range []string{"", "..", "a/b", `a\b`} {
		if _, err := Take(context.Background(), root, name, 1, "", nil); err == nil {
			t.Errorf("project name %q accepted", name)
		}
	}
}

// TestAKilledHolderFreesTheSlot: a process that holds the slot and is killed
// outright leaves it free for the next taker.
func TestAKilledHolderFreesTheSlot(t *testing.T) {
	if root := os.Getenv("SLOT_TEST_HOLD"); root != "" {
		if _, err := Take(context.Background(), root, "p", 1, "", nil); err != nil {
			os.Exit(2)
		}
		fmt.Println("held")
		time.Sleep(time.Hour)
		return
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAKilledHolderFreesTheSlot$")
	cmd.Env = append(os.Environ(), "SLOT_TEST_HOLD="+root)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	if line != "held\n" {
		_ = cmd.Process.Kill()
		t.Fatalf("holder said %q, want held", line)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	if _, err := Take(ctx, root, "p", 1, "", nil); err == nil {
		t.Fatal("took the slot while a live process held it")
	}
	cancel()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rel, err := Take(ctx, root, "p", 1, "", nil)
	if err != nil {
		t.Fatalf("the killed holder's slot stayed taken: %v", err)
	}
	rel()
}

// TestReadSaysWhoHoldsAndWhoWaits: a held slot shows its holder's name, a
// live waiter shows too, and both vanish when the holder lets go and the
// waiter takes the slot.
func TestReadSaysWhoHoldsAndWhoWaits(t *testing.T) {
	root := t.TempDir()
	if s, err := Read(root, "p"); err != nil || len(s.Holders)+len(s.Waiters) != 0 {
		t.Fatalf("Read of an unused project = %+v, %v", s, err)
	}
	rel, err := Take(context.Background(), root, "p", 1, Who("T-1", "a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	got := make(chan func(), 1)
	go func() {
		r, err := Take(context.Background(), root, "p", 1, Who("T-2", ""), func() { close(waiting) })
		if err != nil {
			t.Error(err)
		}
		got <- r
	}()
	<-waiting
	s, err := Read(root, "p")
	if err != nil || len(s.Holders) != 1 || s.Holders[0] != "T-1/a" || len(s.Waiters) != 1 || s.Waiters[0] != "T-2/gate" {
		t.Fatalf("Read = %+v, %v; want T-1/a holding and T-2/gate waiting", s, err)
	}
	rel()
	r2 := <-got
	s, err = Read(root, "p")
	if err != nil || len(s.Holders) != 1 || s.Holders[0] != "T-2/gate" || len(s.Waiters) != 0 {
		t.Fatalf("Read after the hand-over = %+v, %v", s, err)
	}
	r2()
	if s, _ := Read(root, "p"); len(s.Holders)+len(s.Waiters) != 0 {
		t.Fatalf("Read after every release = %+v", s)
	}
}

// TestWaitsAreTheJournalsOpenWaitsThatStillWait: a slot-wait line with a live
// waiter behind it is a wait, said in the one line; once slot-taken follows,
// or when the waiter is gone, it is not.
func TestWaitsAreTheJournalsOpenWaitsThatStillWait(t *testing.T) {
	root := t.TempDir()
	holder, err := Take(context.Background(), root, "p", 1, Who("T-1", "x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder()
	waiting := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Take(ctx, root, "p", 1, Who("T-2", "a"), func() { close(waiting) })
	}()
	<-waiting
	wait := []journal.Line{{Event: journal.EventSlotWait, Slice: "a"}}
	got, err := Waits(root, "p", "T-2", wait)
	want := "waiting for the suite slot: T-1/x is running its suite (1 waiting)"
	if err != nil || len(got) != 1 || got[0].Slice != "a" || got[0].Line != want {
		t.Fatalf("Waits = %+v, %v; want slice a with %q", got, err, want)
	}
	if got, _ := Waits(root, "p", "T-2", append(wait, journal.Line{Event: journal.EventSlotTaken, Slice: "a"})); len(got) != 0 {
		t.Errorf("a wait followed by slot-taken still shows: %+v", got)
	}
	if got, _ := Waits(root, "p", "T-2", []journal.Line{{Event: journal.EventSlotWait, Slice: "b"}}); len(got) != 0 {
		t.Errorf("a wait nobody is in shows: %+v", got)
	}
	cancel()
	<-done
	if got, _ := Waits(root, "p", "T-2", wait); len(got) != 0 {
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
