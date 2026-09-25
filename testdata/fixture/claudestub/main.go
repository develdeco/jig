// Command claudestub is a fake `claude` binary used only by session's
// headless backend tests. It never runs a session: it records its argv,
// working directory, own environment and the names of its working
// directory's parent's own entries to $CLAUDE_STUB_LOG (one JSON object per
// line - what a real live child could see, for a test that drives it
// through the real headless backend), writes $CLAUDE_STUB_WRITE_BODY to
// $CLAUDE_STUB_WRITE_PATH when the path is set (a session honoring its disk
// contract), prints $CLAUDE_STUB_STDOUT and $CLAUDE_STUB_STDERR, and exits
// with $CLAUDE_STUB_EXIT (default 0).
//
// $CLAUDE_STUB_HANG makes it sleep for that Go duration instead of exiting,
// standing in for a session that has stopped making progress.
// $CLAUDE_STUB_CHILD_HANG first starts a copy of itself that sleeps for
// that duration and outlives it, standing in for the shells and test
// runners a real session leaves behind holding jig's pipes.
// $CLAUDE_STUB_CHILD_PID_FILE, with $CLAUDE_STUB_CHILD_HANG set, writes that
// child's pid to the named file once it has started, so a test can check
// afterward whether the tree kill actually reached it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func main() {
	logCall(os.Getenv("CLAUDE_STUB_LOG"))

	if path := os.Getenv("CLAUDE_STUB_WRITE_PATH"); path != "" {
		if err := os.WriteFile(path, []byte(os.Getenv("CLAUDE_STUB_WRITE_BODY")), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	}
	if d := os.Getenv("CLAUDE_STUB_CHILD_HANG"); d != "" {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(),
			"CLAUDE_STUB_CHILD_HANG=", "CLAUDE_STUB_LOG=", "CLAUDE_STUB_WRITE_PATH=",
			"CLAUDE_STUB_HANG="+d)
		// The child inherits this process's stdout and stderr, which are
		// jig's pipes: that is the point of the case.
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
		if pidFile := os.Getenv("CLAUDE_STUB_CHILD_PID_FILE"); pidFile != "" {
			pid := strconv.Itoa(child.Process.Pid)
			if err := os.WriteFile(pidFile, []byte(pid), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(5)
			}
		}
	}
	if d := os.Getenv("CLAUDE_STUB_HANG"); d != "" {
		if dur, err := time.ParseDuration(d); err == nil {
			time.Sleep(dur)
		}
	}
	fmt.Fprint(os.Stdout, os.Getenv("CLAUDE_STUB_STDOUT"))
	fmt.Fprint(os.Stderr, os.Getenv("CLAUDE_STUB_STDERR"))
	code, _ := strconv.Atoi(os.Getenv("CLAUDE_STUB_EXIT"))
	os.Exit(code)
}

func logCall(path string) {
	if path == "" {
		return
	}
	cwd, _ := os.Getwd()
	// parentEntries is exactly what a real child could see with a plain
	// directory listing of its own cwd's parent - the surface a "judge"
	// directory sitting beside a reviewer's own worktree would show up on.
	var parentEntries []string
	if entries, err := os.ReadDir(filepath.Dir(cwd)); err == nil {
		for _, e := range entries {
			parentEntries = append(parentEntries, e.Name())
		}
	}
	data, err := json.Marshal(map[string]any{
		"argv": os.Args, "cwd": cwd, "env": os.Environ(), "parent_entries": parentEntries,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, string(data))
}
