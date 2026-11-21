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

// TestAnEndAfterReleaseKillsNothing: once the run is released its pid and
// handle are no longer the run's, so a late end (a signal arriving as the run
// finishes) must not take down whatever now holds them. With no job the end
// would otherwise be a taskkill of the shell's tree.
func TestAnEndAfterReleaseKillsNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pid")
	c, err := shCommand(context.Background(), sleeperCommand(file), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(c)
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
		c.SysProcAttr = nil
	}
	if err := g.start(); err != nil {
		t.Fatal(err)
	}
	pid := waitForPID(t, file)
	t.Cleanup(func() {
		_ = KillTree(c)
		_ = c.Wait()
	})
	g.release()
	if err := g.end(); err != nil {
		t.Fatalf("end after release = %v, want nil", err)
	}
	time.Sleep(time.Second)
	if !pidAlive(c.Process.Pid) || !pidAlive(pid) {
		t.Fatal("an end after release killed the run's tree")
	}
}
