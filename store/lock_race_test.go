package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMain intercepts the re-exec used by TestLockRaceTwoProcesses: when
// JIG_LOCK_CHILD is set, the process runs one rendezvous role instead of the
// normal test suite.
func TestMain(m *testing.M) {
	if role := os.Getenv("JIG_LOCK_CHILD"); role != "" {
		os.Exit(runLockChild(role))
	}
	os.Exit(m.Run())
}

func runLockChild(role string) int {
	target := os.Getenv("JIG_LOCK_TARGET")
	markerDir := os.Getenv("JIG_LOCK_MARKERS")
	logFile := os.Getenv("JIG_LOCK_LOG")
	deadline := time.Now().Add(30 * time.Second)

	switch role {
	case "A":
		release, held, err := Lock(target, 30*time.Second)
		if err != nil || !held {
			return 1
		}
		if err := writeMarker(markerDir, "a-locked"); err != nil {
			return 1
		}
		if !waitMarker(markerDir, "b-tried", deadline) {
			return 1
		}
		if err := appendLog(logFile, "A"); err != nil {
			return 1
		}
		release()
		return 0
	case "B":
		if !waitMarker(markerDir, "a-locked", deadline) {
			return 1
		}
		if _, held, err := Lock(target, 0); err != nil || held {
			// Must NOT be able to acquire the lock while A holds it.
			return 1
		}
		if err := writeMarker(markerDir, "b-tried"); err != nil {
			return 1
		}
		release, held, err := Lock(target, 30*time.Second)
		if err != nil || !held {
			return 1
		}
		defer release()
		if err := appendLog(logFile, "B"); err != nil {
			return 1
		}
		return 0
	default:
		return 1
	}
}

func writeMarker(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte("1"), 0o644)
}

func waitMarker(dir, name string, deadline time.Time) bool {
	path := filepath.Join(dir, name)
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func appendLog(path, s string) error {
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	data = append(data, []byte(s)...)
	return AtomicWrite(path, data)
}

// TestLockRaceTwoProcesses proves Lock provides real mutual exclusion across
// process boundaries: it re-execs this test binary as two roles (A and B)
// that rendezvous over marker files, and asserts A's critical section
// finishes strictly before B's.
func TestLockRaceTwoProcesses(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	markerDir := filepath.Join(dir, "markers")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "log.txt")
	if err := os.WriteFile(logFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	env := append(os.Environ(),
		"JIG_LOCK_TARGET="+target,
		"JIG_LOCK_MARKERS="+markerDir,
		"JIG_LOCK_LOG="+logFile,
	)

	spawn := func(role string) *exec.Cmd {
		cmd := exec.Command(self)
		cmd.Env = append(append([]string{}, env...), "JIG_LOCK_CHILD="+role)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd
	}

	cmdA := spawn("A")
	cmdB := spawn("B")

	if err := cmdA.Start(); err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := cmdB.Start(); err != nil {
		t.Fatalf("start B: %v", err)
	}

	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	go func() { doneA <- cmdA.Wait() }()
	go func() { doneB <- cmdB.Wait() }()

	timeout := time.After(30 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case err := <-doneA:
			if err != nil {
				t.Fatalf("process A failed: %v", err)
			}
			doneA = nil
		case err := <-doneB:
			if err != nil {
				t.Fatalf("process B failed: %v", err)
			}
			doneB = nil
		case <-timeout:
			t.Fatal("timed out waiting for child processes")
		}
	}

	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(log) != "AB" {
		t.Fatalf("log order = %q, want %q", log, "AB")
	}
}
