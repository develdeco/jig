//go:build windows

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestTaskkillPathUsesSystemRoot pins the ordinary case: the absolute path
// is built from %SystemRoot%, not a bare "taskkill" resolved through
// whatever the process's PATH happens to hold.
func TestTaskkillPathUsesSystemRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CustomRoot")
	t.Setenv("SystemRoot", root)
	want := filepath.Join(root, "System32", "taskkill.exe")
	if got := taskkillPath(); got != want {
		t.Errorf("taskkillPath() = %q, want %q", got, want)
	}
}

// TestTaskkillPathFallsBackWhenSystemRootIsEmpty pins the fallback for a
// process that has cleared its own environment: C:\Windows, not a relative
// or PATH-resolved name.
func TestTaskkillPathFallsBackWhenSystemRootIsEmpty(t *testing.T) {
	t.Setenv("SystemRoot", "")
	want := filepath.Join(`C:\Windows`, "System32", "taskkill.exe")
	if got := taskkillPath(); got != want {
		t.Errorf("taskkillPath() with no SystemRoot = %q, want %q", got, want)
	}
}

// TestKillTreeFallsBackWhenTaskkillIsUnusable proves killTree still ends the
// process when taskkill.exe cannot be run at all (SystemRoot points
// somewhere with no taskkill.exe there): it must fall back to
// cmd.Process.Kill rather than leaving the process running because the
// preferred way to end it failed.
func TestKillTreeFallsBackWhenTaskkillIsUnusable(t *testing.T) {
	t.Setenv("SystemRoot", filepath.Join(t.TempDir(), "no-such-windows"))

	stubDir := buildBinary(t, filepath.Join("testdata", "fixture", "claudestub"), "claude")
	cmd := exec.Command(filepath.Join(stubDir, "claude.exe"))
	cmd.Env = append(os.Environ(), "CLAUDE_STUB_HANG=30s", "CLAUDE_STUB_CHILD_HANG=", "CLAUDE_STUB_LOG=", "CLAUDE_STUB_WRITE_PATH=")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the stub: %v", err)
	}

	if err := killTree(cmd); err != nil {
		t.Fatalf("killTree: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the stub is still running 15s after killTree fell back to Process.Kill")
	}
}
