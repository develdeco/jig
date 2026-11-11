//go:build windows

package envrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestClosingTheJobEndsTheOracleTree: the oracle's job object kills on close,
// the shell in it before it ran, so the tree ends when the job's handle closes
// - which is what happens to the handle of a jig that is terminated.
func TestClosingTheJobEndsTheOracleTree(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pid")
	c, err := shCommand(context.Background(), sleeperCommand(file), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(c)
	if g.job == 0 {
		t.Fatal("no job object was made")
	}
	if err := g.start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	pid := waitForPID(t, file)
	t.Cleanup(func() {
		if pidAlive(pid) {
			_ = windows.TerminateJobObject(g.job, 1)
		}
	})
	if !pidAlive(pid) {
		t.Fatal("the oracle's child is not running")
	}
	if err := windows.CloseHandle(g.job); err != nil {
		t.Fatal(err)
	}
	waitGone(t, pid)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the shell outlived its job")
	}
}

// TestTimeoutEndsTheOracleTree: a run that outlives its limit has its whole
// tree ended by the job (the shell's child included), and returns well inside
// the wait delay that would otherwise end only the shell.
func TestTimeoutEndsTheOracleTree(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pid")
	done := make(chan error, 1)
	go func() {
		_, err := ShellOutputEnv(sleeperCommand(file), t.TempDir(), 5*time.Second, nil)
		done <- err
	}()
	pid := waitForPID(t, file)
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	select {
	case err := <-done:
		if !errors.Is(err, ErrTimedOut) {
			t.Errorf("error = %v, want a timeout", err)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("the run did not return soon after its limit")
	}
	waitGone(t, pid)
}
