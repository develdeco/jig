package spawn

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain turns this test binary into the detached child when
// JIG_SPAWN_CHILD names a directory: the tests start the binary itself
// through Detached, handing the variable over in Spec.Env (the test process's
// own environment is never edited).
func TestMain(m *testing.M) {
	if dir := os.Getenv("JIG_SPAWN_CHILD"); dir != "" {
		os.Exit(runChild(dir))
	}
	os.Exit(m.Run())
}

// runChild reports what its own standard streams are to the log it was given,
// waits a moment so the parent has long since returned from Detached, then
// writes done into dir: the file the test waits for.
func runChild(dir string) int {
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	os.Stdout.WriteString("child stdout: stdin held " + strconv.Itoa(len(stdin)) + " bytes\n")
	os.Stderr.WriteString("child stderr: cwd " + mustGetwd() + "\n")
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "done"), []byte("done"), 0o644); err != nil {
		return 1
	}
	return 0
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return wd
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestDetachedOutlivesParent: Detached returns at once, well before the child
// is done, and the child goes on to finish its work: it writes its file after
// Detached has returned.
func TestDetachedOutlivesParent(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "logs", "deep", "child.log")

	start := time.Now()
	pid, err := Detached(Spec{
		Path:    self,
		Dir:     dir,
		Env:     append(os.Environ(), "JIG_SPAWN_CHILD="+dir),
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("Detached: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Detached took %s, want under 1s: it must not wait for the child", took)
	}
	if pid <= 0 {
		t.Errorf("pid = %d, want a process id", pid)
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
		t.Fatal("the child had already finished when Detached returned; the test proves nothing")
	}

	waitForFile(t, filepath.Join(dir, "done"))

	// The log took both streams, in a file Detached made with its directories,
	// and the child's stdin was the null device: it read to EOF at once.
	var log []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		log, _ = os.ReadFile(logPath)
		if strings.Contains(string(log), "child stdout: stdin held 0 bytes") && strings.Contains(string(log), "child stderr: cwd ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q, want both the child's stdout and stderr lines", log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(log), filepath.Base(dir)) {
		t.Errorf("log = %q, want the child to have run in Spec.Dir (%s)", log, dir)
	}
}

// TestDetachedAppendsToTheLog: a second child's output follows the first's
// rather than replacing it.
func TestDetachedAppendsToTheLog(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "child.log")
	if err := os.WriteFile(logPath, []byte("earlier run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detached(Spec{Path: self, Dir: dir, Env: append(os.Environ(), "JIG_SPAWN_CHILD="+dir), LogPath: logPath}); err != nil {
		t.Fatalf("Detached: %v", err)
	}
	waitForFile(t, filepath.Join(dir, "done"))

	deadline := time.Now().Add(10 * time.Second)
	for {
		log, _ := os.ReadFile(logPath)
		if strings.HasPrefix(string(log), "earlier run\n") && strings.Contains(string(log), "child stderr: cwd ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q, want the earlier run's line kept and the child's after it", log)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestDetachedRefusesWhatCannotStart: no program, or a program that is not
// there, is an error and starts nothing.
func TestDetachedRefusesWhatCannotStart(t *testing.T) {
	if _, err := Detached(Spec{}); err == nil {
		t.Error("Detached with no Path = nil error, want a refusal")
	}
	missing := filepath.Join(t.TempDir(), "no-such-program")
	if pid, err := Detached(Spec{Path: missing}); err == nil {
		t.Errorf("Detached of a missing program = pid %d, nil error, want an error", pid)
	}
}
