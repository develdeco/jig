package gittest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// GitArgv runs fn with git tracing on and returns the command line of every
// git process it started (every process git itself starts counts, as in CI's
// budget), in the order they began. It points GIT_TRACE2_EVENT at a file of
// its own for the call, so the process environment is edited and the test
// must not be parallel: t.Setenv refuses a test that called t.Parallel, and
// a serial test runs before any parallel one resumes.
func GitArgv(t testing.TB, fn func()) [][]string {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	fn()
	f, err := os.Open(trace)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("gittest: read the git trace: %v", err)
	}
	defer f.Close()
	var started [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var ev struct {
			Event string
			Argv  []string
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Event == "start" {
			started = append(started, ev.Argv)
		}
	}
	return started
}

// CountGit is how many git processes fn started (see GitArgv).
func CountGit(t testing.TB, fn func()) int {
	t.Helper()
	return len(GitArgv(t, fn))
}
