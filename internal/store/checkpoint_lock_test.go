package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// runCheckpointChild is the other process in
// TestLockCheckpointReentrantAndExclusive: it takes the checkpoint lock of
// the store at root, tells the parent by writing "held" next to it, keeps the
// lock until the parent writes "release", then lets go.
func runCheckpointChild(root string) int {
	st := &Store{Root: root}
	release, err := st.LockCheckpoint()
	if err != nil {
		return 1
	}
	signals := os.Getenv("JIG_CHECKPOINT_SIGNALS")
	if err := writeMarker(signals, "held"); err != nil {
		return 1
	}
	if !waitMarker(signals, "release", time.Now().Add(30*time.Second)) {
		return 1
	}
	release()
	return 0
}

// checkpointLockFile is the file LockCheckpoint's lock sits on for the store
// work, a plain clone: the sidecar of <git dir>/jig-checkpoint.
func checkpointLockFile(work string) string {
	return filepath.Join(work, ".git", checkpointLockName)
}

// heldElsewhere reports whether something else holds the lock on path,
// trying with a second file handle that never waits.
func heldElsewhere(t *testing.T, path string) bool {
	t.Helper()
	release, held, err := TryLock(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if held {
		release()
	}
	return !held
}

// TestLockCheckpointReentrantAndExclusive: within a process the lock nests
// (a second take does not wait, and the file lock lasts until the last
// release, however many times a release is called); across processes it
// excludes, a waiter getting it as soon as the holder lets go, and a waiter
// that times out proceeds and warns.
func TestLockCheckpointReentrantAndExclusive(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	lockFile := checkpointLockFile(work)

	t.Run("re-entrant per root", func(t *testing.T) {
		outer, err := st.LockCheckpoint()
		if err != nil {
			t.Fatal(err)
		}
		if !heldElsewhere(t, lockFile) {
			t.Fatal("the lock file is free while LockCheckpoint holds it")
		}
		took := make(chan time.Duration, 1)
		go func() {
			begin := time.Now()
			inner, err := st.LockCheckpoint()
			if err != nil {
				t.Error(err)
			}
			took <- time.Since(begin)
			inner()
		}()
		select {
		case d := <-took:
			if d > time.Second {
				t.Errorf("a nested take waited %s, want none", d)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a nested LockCheckpoint deadlocked against its own process")
		}
		// The nested hold is gone; the outer one is not, however often the
		// nested release is called again.
		if !heldElsewhere(t, lockFile) {
			t.Fatal("releasing a nested hold let go of the outer one")
		}
		again, err := (&Store{Root: work}).LockCheckpoint() // a second Store value, one root
		if err != nil {
			t.Fatal(err)
		}
		again()
		again()
		if !heldElsewhere(t, lockFile) {
			t.Fatal("releasing a hold twice let go of the outer one")
		}
		outer()
		if heldElsewhere(t, lockFile) {
			t.Fatal("the lock file is still held after every hold was released")
		}
	})

	t.Run("exclusive across processes", func(t *testing.T) {
		signals := t.TempDir()
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(self)
		cmd.Env = append(os.Environ(), "JIG_CHECKPOINT_CHILD="+work, "JIG_CHECKPOINT_SIGNALS="+signals)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		childDone := make(chan error, 1)
		go func() { childDone <- cmd.Wait() }()
		defer func() {
			_ = writeMarker(signals, "release")
			<-childDone
		}()
		if !waitMarker(signals, "held", time.Now().Add(30*time.Second)) {
			t.Fatal("the child never took the lock")
		}

		// A waiter with a short patience proceeds and says so.
		var mu sync.Mutex
		var warnings []string
		st.Warn = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			warnings = append(warnings, format)
		}
		begin := time.Now()
		release, err := st.lockCheckpoint(200 * time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(begin); waited < 150*time.Millisecond {
			t.Errorf("lockCheckpoint returned after %s while another process held the lock", waited)
		}
		mu.Lock()
		if len(warnings) != 1 || !strings.Contains(warnings[0], "without the checkpoint lock") {
			t.Errorf("warnings = %q, want one saying it proceeds without the checkpoint lock", warnings)
		}
		mu.Unlock()
		release()
		st.Warn = nil
		if !heldElsewhere(t, lockFile) {
			t.Fatal("a timed-out waiter's release let go of the other process's lock")
		}

		// A patient waiter gets it once the holder lets go, and not before.
		got := make(chan error, 1)
		go func() {
			release, err := st.LockCheckpoint()
			if err == nil {
				release()
			}
			got <- err
		}()
		select {
		case err := <-got:
			t.Fatalf("LockCheckpoint returned (%v) while another process held the lock", err)
		case <-time.After(300 * time.Millisecond):
		}
		if err := writeMarker(signals, "release"); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-got:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("LockCheckpoint never got the lock after the other process released it")
		}
	})
}

// TestLockCheckpointOnAStoreWithNoGit: a directory that is not a git clone has
// no common directory to lock in; the lock is the in-process one alone, still
// re-entrant, and takes nothing from the file system.
func TestLockCheckpointOnAStoreWithNoGit(t *testing.T) {
	root := t.TempDir()
	st := &Store{Root: root}
	a, err := st.LockCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.LockCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	b()
	a()
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("a store with no git holds %d entries after LockCheckpoint, want none", len(entries))
	}
}
