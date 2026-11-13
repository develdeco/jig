package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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

// namesPath reports whether text names path as given or in its resolved
// spelling. A Windows temp dir can reach a test in its 8.3 short form
// (RUNNER~1) while jig names the file by its long one.
func namesPath(text, path string) bool {
	if strings.Contains(text, path) {
		return true
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	return err == nil && strings.Contains(text, filepath.Join(dir, filepath.Base(path)))
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
// excludes, a waiter getting it as soon as the holder lets go.
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

// startCheckpointChild starts the other process that takes the checkpoint lock
// of the store at work and keeps it until release is called (the returned
// func, also run at cleanup). It returns the child's pid.
func startCheckpointChild(t *testing.T, work string) (pid int, release func()) {
	t.Helper()
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
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	release = func() {
		once.Do(func() {
			_ = writeMarker(signals, "release")
			<-done
		})
	}
	t.Cleanup(release)
	if !waitMarker(signals, "held", time.Now().Add(30*time.Second)) {
		t.Fatal("the child never took the lock")
	}
	return cmd.Process.Pid, func() {
		_ = writeMarker(signals, "release")
	}
}

func setCheckpointBounds(st *Store, notice, bound time.Duration) {
	st.checkpointNotice, st.checkpointBound = notice, bound
}

// TestLockCheckpointKeepsWaitingPastTheNoticeAndNamesTheHolder: past the
// notice bound a waiter has not given up; it says once who holds the lock,
// and takes the lock when the holder lets go, never having proceeded without.
func TestLockCheckpointKeepsWaitingPastTheNoticeAndNamesTheHolder(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	pid, letGo := startCheckpointChild(t, work)
	setCheckpointBounds(st, 200*time.Millisecond, 20*time.Second)

	var mu sync.Mutex
	var warnings []string
	st.Warn = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}

	got := make(chan error, 1)
	go func() {
		release, err := st.LockCheckpoint()
		if err == nil {
			defer release()
			if !heldElsewhere(t, checkpointLockFile(work)) {
				err = fmt.Errorf("the waiter returned without holding the file lock")
			}
		}
		got <- err
	}()
	// Wait for the notice rather than a fixed window, so a slow runner that
	// reaches the notice late still sees it; the waiter must not return
	// meanwhile.
	warned := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(warnings)
	}
	deadline := time.After(10 * time.Second)
	for warned() == 0 {
		select {
		case err := <-got:
			t.Fatalf("LockCheckpoint returned (%v) past the notice while another process held the lock", err)
		case <-deadline:
			t.Fatal("no notice 10s past the notice bound while another process held the lock")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// One more stretch past the notice: still waiting, still one notice.
	select {
	case err := <-got:
		t.Fatalf("LockCheckpoint returned (%v) past the notice while another process held the lock", err)
	case <-time.After(300 * time.Millisecond):
	}
	mu.Lock()
	if len(warnings) != 1 || !strings.Contains(warnings[0], fmt.Sprint(pid)) {
		t.Errorf("warnings = %q, want one naming holder pid %d", warnings, pid)
	}
	mu.Unlock()

	letGo()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("LockCheckpoint never got the lock after the other process released it")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, w := range warnings {
		if strings.Contains(w, "without the checkpoint lock") {
			t.Errorf("a warning said the work proceeds without the lock: %q", w)
		}
	}
}

// TestLockCheckpointFailsAtTheLongerBoundNamingTheHolder: a holder that never
// lets go ends the wait in an error that names it and the lock file; the
// lock is not held and the other process keeps it.
func TestLockCheckpointFailsAtTheLongerBoundNamingTheHolder(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	pid, _ := startCheckpointChild(t, work)
	setCheckpointBounds(st, 100*time.Millisecond, 400*time.Millisecond)
	st.Warn = func(string, ...any) {}

	begin := time.Now()
	release, err := st.LockCheckpoint()
	if err == nil {
		release()
		t.Fatal("LockCheckpoint returned no error while another process held the lock past the bound")
	}
	if waited := time.Since(begin); waited < 350*time.Millisecond {
		t.Errorf("failed after %s, before the bound", waited)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(pid)) {
		t.Errorf("error %q does not name holder pid %d", err, pid)
	}
	if lockFile := checkpointLockFile(work) + ".lock"; !namesPath(err.Error(), lockFile) {
		t.Errorf("error %q does not name the lock file %s", err, lockFile)
	}
	if release == nil {
		t.Fatal("release is nil")
	}
	release()
	if !heldElsewhere(t, checkpointLockFile(work)) {
		t.Fatal("the failed waiter's release let go of the other process's lock")
	}
}

// holderRE matches a "pid N (name) since T" holder description: its pid, name
// and start time.
var holderRE = regexp.MustCompile(`pid (\d+) \(([^)]+)\) since (\S+)`)

// TestLockCheckpointNoticeAndFailureNameTheHoldersCommandAndStart: the notice
// and the failure say which command holds the lock (the base name of its
// executable) and when it started, as RFC 3339 within this test's run.
func TestLockCheckpointNoticeAndFailureNameTheHoldersCommandAndStart(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second).Truncate(time.Second)
	pid, _ := startCheckpointChild(t, work)
	after := time.Now().Add(time.Second)
	setCheckpointBounds(st, 100*time.Millisecond, 400*time.Millisecond)
	var notice string
	st.Warn = func(format string, args ...any) { notice = fmt.Sprintf(format, args...) }

	release, err := st.LockCheckpoint()
	if err == nil {
		release()
		t.Fatal("LockCheckpoint returned no error while another process held the lock past the bound")
	}
	for what, text := range map[string]string{"notice": notice, "failure": err.Error()} {
		m := holderRE.FindStringSubmatch(text)
		if m == nil {
			t.Errorf("%s %q does not describe the holder as pid, command and start", what, text)
			continue
		}
		if m[1] != fmt.Sprint(pid) {
			t.Errorf("%s names pid %s, want %d", what, m[1], pid)
		}
		if m[2] != filepath.Base(self) {
			t.Errorf("%s names command %q, want %q", what, m[2], filepath.Base(self))
		}
		started, perr := time.Parse(time.RFC3339, m[3])
		if perr != nil {
			t.Errorf("%s start %q is not RFC 3339: %v", what, m[3], perr)
		} else if started.Before(before) || started.After(after) {
			t.Errorf("%s start %s is outside this test's run (%s to %s)", what, started, before, after)
		}
	}
}

// TestLockCheckpointReadsAMissingHolderFileAsUnknown: a holder that left no
// file is still named in the notice and the failure, as unknown.
func TestLockCheckpointReadsAMissingHolderFileAsUnknown(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	startCheckpointChild(t, work)
	if err := os.Remove(filepath.Join(work, ".git", checkpointHolderName)); err != nil {
		t.Fatal(err)
	}
	setCheckpointBounds(st, 100*time.Millisecond, 400*time.Millisecond)
	var notice string
	st.Warn = func(format string, args ...any) { notice = fmt.Sprintf(format, args...) }

	release, err := st.LockCheckpoint()
	if err == nil {
		release()
		t.Fatal("LockCheckpoint returned no error while another process held the lock past the bound")
	}
	for what, text := range map[string]string{"notice": notice, "failure": err.Error()} {
		if !strings.Contains(text, "holder unknown") {
			t.Errorf("%s %q does not say the holder is unknown", what, text)
		}
	}
}

// TestLockCheckpointKeepsAHolderFileWhileHeld: <git dir>/jig-checkpoint.holder
// names this process while the lock is held, a nested hold included, and is
// gone after the last release.
func TestLockCheckpointKeepsAHolderFileWhileHeld(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	holder := filepath.Join(work, ".git", "jig-checkpoint.holder")
	named := func(when string) {
		t.Helper()
		data, err := os.ReadFile(holder)
		if err != nil {
			t.Fatalf("%s: no holder file: %v", when, err)
		}
		if f := strings.Fields(string(data)); len(f) == 0 || f[0] != fmt.Sprint(os.Getpid()) {
			t.Errorf("%s: holder file %q does not name pid %d", when, data, os.Getpid())
		}
	}
	outer, err := st.LockCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	named("held")
	inner, err := st.LockCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	named("nested")
	inner()
	named("after the nested release")
	outer()
	if _, err := os.Stat(holder); !os.IsNotExist(err) {
		t.Errorf("holder file after the last release: stat error %v, want it gone", err)
	}
}

// TestLockCheckpointFailsAtTheBoundNotLater: with a notice of 1s and a bound
// of 1.2s the wait asks for the notice and then only the rest of the bound,
// so it ends at the bound, not a notice and a bound apart. It reads the
// timeouts the wait is split into rather than timing a wall clock, which a
// loaded machine stretches.
func TestLockCheckpointFailsAtTheBoundNotLater(t *testing.T) {
	st, _ := newTestStandaloneStore(t)
	setCheckpointBounds(st, time.Second, 1200*time.Millisecond)
	st.Warn = func(string, ...any) {}
	var asked []time.Duration
	st.checkpointTryLock = func(_ string, timeout time.Duration) (func(), bool, error) {
		asked = append(asked, timeout)
		return func() {}, false, nil
	}

	release, err := st.LockCheckpoint()
	if err == nil {
		release()
		t.Fatal("LockCheckpoint returned no error while the lock stayed held past the bound")
	}
	want := []time.Duration{time.Second, 200 * time.Millisecond}
	if !slices.Equal(asked, want) {
		t.Errorf("the wait asked for %v, want %v: the notice, then the rest of the bound", asked, want)
	}
	if !strings.Contains(err.Error(), "after 1.2s") {
		t.Errorf("error = %q, want it to name the 1.2s bound", err)
	}
}
