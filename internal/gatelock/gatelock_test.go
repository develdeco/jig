package gatelock

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/home"
)

// TestGateLockSecondTakeWaitsAndReleaseFreesIt: a second take of one ticket
// says it waits, takes the lock once the first lets go, and reads the holder.
func TestGateLockSecondTakeWaitsAndReleaseFreesIt(t *testing.T) {
	root := t.TempDir()
	rel1, err := Take(context.Background(), root, "p", "T-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := Holder(root, "p", "T-1")
	if !ok || info.PID != os.Getpid() {
		t.Fatalf("Holder = %+v, %v; want this process", info, ok)
	}

	waits := make(chan struct{}, 4)
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		rel, err := Take(context.Background(), root, "p", "T-1", func() { calls.Add(1); waits <- struct{}{} })
		if err == nil {
			rel()
		}
		done <- err
	}()
	select {
	case <-waits:
	case <-time.After(5 * time.Second):
		t.Fatal("the second take never said it waits")
	}
	select {
	case <-done:
		t.Fatal("the second take returned while the first held the lock")
	case <-time.After(300 * time.Millisecond):
	}
	rel1()
	rel1() // safe twice
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second take did not get the lock after release")
	}
	if calls.Load() != 1 {
		t.Errorf("onWait called %d times, want once", calls.Load())
	}
	if _, ok := Holder(root, "p", "T-1"); ok {
		t.Error("holder file left after release")
	}
}

// TestGateLockOtherTicketsDoNotWait: the lock is per ticket.
func TestGateLockOtherTicketsDoNotWait(t *testing.T) {
	root := t.TempDir()
	rel, err := Take(context.Background(), root, "p", "T-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	rel2, err := Take(context.Background(), root, "p", "T-2", func() { t.Error("T-2 waited") })
	if err != nil {
		t.Fatal(err)
	}
	rel2()
}

// TestGateLockWaitEndsOnCancel: Ctrl-C ends the wait with the context's error.
func TestGateLockWaitEndsOnCancel(t *testing.T) {
	root := t.TempDir()
	rel, err := Take(context.Background(), root, "p", "T-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	if _, err := Take(ctx, root, "p", "T-1", nil); err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestGateLockOfADeadHolderIsFree: a lock file and holder file left by a gate
// that died hold nothing; the next take gets the lock without waiting.
func TestGateLockOfADeadHolderIsFree(t *testing.T) {
	root := t.TempDir()
	dir := home.GateDir(root, "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "T-1.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "T-1.holder"), []byte("99999999 2026-01-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := Take(context.Background(), root, "p", "T-1", func() { t.Error("waited on a dead holder") })
	if err != nil {
		t.Fatal(err)
	}
	info, ok := Holder(root, "p", "T-1")
	if !ok || info.PID != os.Getpid() {
		t.Errorf("Holder = %+v, %v; want this process", info, ok)
	}
	rel()
}

// TestGateLockRefusesANameThatLeavesTheDirectory: a ticket is one path element.
func TestGateLockRefusesANameThatLeavesTheDirectory(t *testing.T) {
	if _, err := Take(context.Background(), t.TempDir(), "p", "../x", nil); err == nil {
		t.Fatal("want an error")
	}
}
