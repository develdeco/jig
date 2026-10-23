// Package spawn starts a process that outlives the jig command that started
// it: the detached `jig verify` worker a hook or a foreground command leaves
// running after it returns. The platform differences (a new session on Unix,
// a detached process in a new group on Windows) are in spawn_unix.go and
// spawn_windows.go.
package spawn

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Spec describes the process to start.
type Spec struct {
	// Path is the program to run: a path, or a name looked up on PATH.
	Path string
	// Args are the arguments after the program name.
	Args []string
	// Dir is the working directory; empty is the caller's.
	Dir string
	// Env is the child's whole environment as KEY=VALUE entries; nil gives
	// it the caller's own. A caller that wants the caller's environment plus
	// a few entries passes os.Environ() with them appended.
	Env []string
	// LogPath is the file the child's standard output and standard error are
	// appended to, created with its directories when absent. Empty discards
	// both.
	LogPath string
}

// Detached starts the process s describes and returns its pid without
// waiting for it. The child gets the null device for standard input, so it
// never reads from, and never holds, the caller's terminal; its standard
// output and error append to s.LogPath; and it is detached from the caller's
// session or console, so it keeps running after the caller exits and is not
// stopped by the terminal closing. Detached releases the process: the caller
// does not Wait for it and cannot get its exit status.
//
// The caller's own copies of the log and null-device handles are closed
// before it returns. An error means no process was started.
func Detached(s Spec) (pid int, err error) {
	if s.Path == "" {
		return 0, errors.New("spawn: no program to run")
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("spawn: open %s: %w", os.DevNull, err)
	}
	defer null.Close()
	out := null
	if s.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(s.LogPath), 0o755); err != nil {
			return 0, fmt.Errorf("spawn: create log directory: %w", err)
		}
		log, err := os.OpenFile(s.LogPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return 0, fmt.Errorf("spawn: open log %s: %w", s.LogPath, err)
		}
		defer log.Close()
		out = log
	}

	start := func(attempt int) (*exec.Cmd, error) {
		cmd := exec.Command(s.Path, s.Args...)
		cmd.Dir = s.Dir
		cmd.Env = s.Env
		cmd.Stdin = null
		cmd.Stdout = out
		cmd.Stderr = out
		cmd.SysProcAttr = detachAttr(attempt)
		return cmd, cmd.Start()
	}
	cmd, err := start(0)
	for attempt := 1; err != nil && retryDetach(err, attempt); attempt++ {
		cmd, err = start(attempt)
	}
	if err != nil {
		return 0, fmt.Errorf("spawn: start %s: %w", s.Path, err)
	}
	pid = cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("spawn: release %s (pid %d): %w", s.Path, pid, err)
	}
	return pid, nil
}
