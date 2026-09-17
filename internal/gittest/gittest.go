// Package gittest runs a test binary's git hermetically: no background
// maintenance outlives a test, and no host git config leaks into one. It is
// a leaf package, importing nothing else from this module.
package gittest

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	mu       sync.Mutex
	cleanups []func()
)

// Run writes a git config with maintenance.auto=false, receive.autogc=false
// and gc.autoDetach=false, points GIT_CONFIG_GLOBAL at it, sets
// GIT_CONFIG_NOSYSTEM=1, and runs m. Every git process the binary spawns,
// including git-receive-pack on the far side of a local push, then runs
// without detached maintenance or host config. After m.Run() it runs the
// AtExit funcs in LIFO order and returns the exit code:
//
//	func TestMain(m *testing.M) { os.Exit(gittest.Run(m)) }
func Run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "jig-gittest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gittest: create temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	cfg := filepath.Join(dir, "gitconfig")
	const body = "[maintenance]\n\tauto = false\n[receive]\n\tautogc = false\n[gc]\n\tautoDetach = false\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gittest: write %s: %v\n", cfg, err)
		return 1
	}
	if err := os.Setenv("GIT_CONFIG_GLOBAL", cfg); err != nil {
		fmt.Fprintf(os.Stderr, "gittest: set GIT_CONFIG_GLOBAL: %v\n", err)
		return 1
	}
	if err := os.Setenv("GIT_CONFIG_NOSYSTEM", "1"); err != nil {
		fmt.Fprintf(os.Stderr, "gittest: set GIT_CONFIG_NOSYSTEM: %v\n", err)
		return 1
	}

	code := m.Run()
	runCleanups()
	return code
}

// pinnedIdentity is the fixture identity and date PinIdentity sets, matching
// internal/fixture and the fake session backend so shas stay comparable
// across all three.
var pinnedIdentity = [][2]string{
	{"GIT_AUTHOR_NAME", "jig-fixture"},
	{"GIT_AUTHOR_EMAIL", "fixture@example.invalid"},
	{"GIT_AUTHOR_DATE", "2026-01-01T00:00:00Z"},
	{"GIT_COMMITTER_NAME", "jig-fixture"},
	{"GIT_COMMITTER_EMAIL", "fixture@example.invalid"},
	{"GIT_COMMITTER_DATE", "2026-01-01T00:00:00Z"},
}

// PinIdentity sets the six GIT_AUTHOR_* and GIT_COMMITTER_* name, email and
// date variables for the whole test binary, so commits hash the same on
// every run. The environment beats git config, so call it only from a
// package whose tests do not rely on their own configured identity:
//
//	func TestMain(m *testing.M) {
//	    gittest.PinIdentity()
//	    os.Exit(gittest.Run(m))
//	}
func PinIdentity() {
	for _, kv := range pinnedIdentity {
		_ = os.Setenv(kv[0], kv[1])
	}
}

// AtExit registers f to run after m.Run() returns, in LIFO order, before
// Run removes its own temp directory. It is safe to call concurrently.
func AtExit(f func()) {
	mu.Lock()
	defer mu.Unlock()
	cleanups = append(cleanups, f)
}

// runCleanups runs every func registered with AtExit, in LIFO order, and
// clears the registry.
func runCleanups() {
	mu.Lock()
	fs := cleanups
	cleanups = nil
	mu.Unlock()
	for i := len(fs) - 1; i >= 0; i-- {
		fs[i]()
	}
}
