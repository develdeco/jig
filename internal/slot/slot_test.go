package slot

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func take(t *testing.T, root string, n int) func() {
	t.Helper()
	rel, err := Take(context.Background(), root, "p", n, nil)
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
		rel, err := Take(context.Background(), root, "p", 1, func() { waits <- struct{}{} })
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
	if _, err := Take(ctx, root, "p", 2, nil); err == nil {
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
	other, err := Take(context.Background(), root, "q", 1, nil)
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
	go func() { _, err := Take(ctx, root, "p", 1, nil); done <- err }()
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
	if _, err := Take(context.Background(), root, "p", 0, nil); err == nil {
		t.Error("zero slots accepted")
	}
	for _, name := range []string{"", "..", "a/b", `a\b`} {
		if _, err := Take(context.Background(), root, name, 1, nil); err == nil {
			t.Errorf("project name %q accepted", name)
		}
	}
}

// TestAKilledHolderFreesTheSlot: a process that holds the slot and is killed
// outright leaves it free for the next taker.
func TestAKilledHolderFreesTheSlot(t *testing.T) {
	if root := os.Getenv("SLOT_TEST_HOLD"); root != "" {
		if _, err := Take(context.Background(), root, "p", 1, nil); err != nil {
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
	if _, err := Take(ctx, root, "p", 1, nil); err == nil {
		t.Fatal("took the slot while a live process held it")
	}
	cancel()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rel, err := Take(ctx, root, "p", 1, nil)
	if err != nil {
		t.Fatalf("the killed holder's slot stayed taken: %v", err)
	}
	rel()
}
