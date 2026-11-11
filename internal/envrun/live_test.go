package envrun

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sleeperCommand starts a long-running child, records its pid (the Windows
// one under Git's sh, which numbers processes of its own) in file, and waits
// on it.
func sleeperCommand(file string) string {
	return `sleep 600 & p=$!; (cat /proc/$p/winpid 2>/dev/null || echo $p) > '` + filepath.ToSlash(file) + `'; wait`
}

// waitForPID waits for the file sleeperCommand writes and returns the pid.
func waitForPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s was never written", file)
	return 0
}

// waitGone fails the test unless pid stops running soon.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for pidAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestEndLiveEndsARunningOracle: ending the live oracles (what jig's stop
// signal handler does) ends a running oracle and the child it started, and
// the run returns at once instead of waiting on its limit.
func TestEndLiveEndsARunningOracle(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pid")
	done := make(chan error, 1)
	go func() {
		_, err := ShellOutputEnv(sleeperCommand(file), t.TempDir(), time.Hour, nil)
		done <- err
	}()
	pid := waitForPID(t, file)
	t.Cleanup(func() {
		if pidAlive(pid) {
			p, _ := os.FindProcess(pid)
			_ = p.Kill()
		}
	})
	endLive(false)
	select {
	case err := <-done:
		if !errors.Is(err, ErrStopped) {
			t.Errorf("an oracle that was ended reported %v, want ErrStopped", err)
		}
		if errors.Is(err, ErrTimedOut) {
			t.Error("an oracle that was ended reported a timeout")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not return after its oracle was ended")
	}
	waitGone(t, pid)
}

// TestFinishedFailedAndTimedOutRunsReportAsBefore: a run that finishes, fails or times out
// reports as it did.
func TestFinishedFailedAndTimedOutRunsReportAsBefore(t *testing.T) {
	dir := t.TempDir()
	before := liveCount()
	if out, err := ShellOutputEnv("echo done", dir, time.Minute, nil); err != nil || trimEOL(out) != "done" {
		t.Errorf("finished run = %q, %v, want done", out, err)
	}
	// A failed oracle hands back both of its streams: a compile error or a
	// panic is on stderr.
	out, err := ShellOutputEnv("echo out; echo err >&2; exit 3", dir, time.Minute, nil)
	if err == nil {
		t.Error("a failing run reported success")
	}
	if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
		t.Errorf("failing run output = %q, want both stdout and stderr", out)
	}
	_, err = ShellOutputEnv("sleep 600", dir, 300*time.Millisecond, nil)
	var to *TimedOutError
	if !errors.As(err, &to) {
		t.Errorf("timed out run error = %v, want a TimedOutError", err)
	}
	// Every finished, failed and timed out run is forgotten: a stale entry
	// would have a later EndAll signal a dead pid or group.
	if after := liveCount(); after != before {
		t.Errorf("%d oracles registered live after the runs, %d before: a finished run was not forgotten", after, before)
	}
}

// liveCount is how many oracles are registered live.
func liveCount() int {
	live.mu.Lock()
	defer live.mu.Unlock()
	return len(live.ends)
}

// TestFinishedRunLeavesWhatItStartedRunning: a run that finishes does not end
// a background process it left, as before (Windows: the job stops killing on
// close when the run is released).
func TestFinishedRunLeavesWhatItStartedRunning(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pid")
	cmd := `sleep 600 >/dev/null 2>&1 & p=$!; (cat /proc/$p/winpid 2>/dev/null || echo $p) > '` + filepath.ToSlash(file) + `'`
	if _, err := ShellOutputEnv(cmd, t.TempDir(), time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	pid := waitForPID(t, file)
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	time.Sleep(500 * time.Millisecond)
	if !pidAlive(pid) {
		t.Error("a background process a finished oracle left was ended with the run")
	}
}

// TestRegisterForgetsAndEndsLateOracles: an oracle that finished is forgotten
// and not ended by a later EndAll; once jig is stopping, a new one is ended as
// it registers.
func TestRegisterForgetsAndEndsLateOracles(t *testing.T) {
	var ended []string
	forgetA := register(func() { ended = append(ended, "a") })
	register(func() { ended = append(ended, "b") })
	forgetA()
	endLive(false)
	if len(ended) != 1 || ended[0] != "b" {
		t.Fatalf("ended = %v, want only b", ended)
	}
	live.mu.Lock()
	live.ends = nil
	live.ended = true
	live.mu.Unlock()
	t.Cleanup(func() {
		live.mu.Lock()
		live.ended = false
		live.mu.Unlock()
	})
	register(func() { ended = append(ended, "late") })
	if len(ended) != 2 || ended[1] != "late" {
		t.Errorf("ended = %v, want the late one ended as it registered", ended)
	}
}

// TestEndAllEndsOraclesThatStartAfterIt: EndAll is final, so an oracle that
// registers after the stop is ended as it starts (a gate between two oracle
// runs when the signal lands), where endLive(false) would leave it running.
func TestEndAllEndsOraclesThatStartAfterIt(t *testing.T) {
	t.Cleanup(func() {
		live.mu.Lock()
		live.ended = false
		live.mu.Unlock()
	})
	EndAll()
	ended := false
	register(func() { ended = true })
	if !ended {
		t.Error("an oracle registered after EndAll was not ended")
	}
}
