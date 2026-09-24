// Command claudestub is a fake `claude` binary used only by session's
// headless backend tests. It never runs a session: it records its argv and
// working directory to $CLAUDE_STUB_LOG (one JSON object per line), writes
// $CLAUDE_STUB_WRITE_BODY to $CLAUDE_STUB_WRITE_PATH when the path is set
// (a session honoring its disk contract), prints $CLAUDE_STUB_STDOUT and
// $CLAUDE_STUB_STDERR, and exits with $CLAUDE_STUB_EXIT (default 0).
//
// $CLAUDE_STUB_HANG makes it sleep for that Go duration instead of exiting,
// standing in for a session that has stopped making progress.
// $CLAUDE_STUB_CHILD_HANG first starts a copy of itself that sleeps for
// that duration and outlives it, standing in for the shells and test
// runners a real session leaves behind holding jig's pipes.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	data, err := json.Marshal(map[string]any{"argv": os.Args, "cwd": cwd})
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
