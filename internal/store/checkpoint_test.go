package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingPreCommit writes a pre-commit hook that always fails into dir and
// returns dir.
func failingPreCommit(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// hostHooks points every git program this test starts at a hooks directory
// whose pre-commit always fails, through config from the environment, as a
// machine-wide core.hooksPath would. In-process store writes never read
// the machine's git config, so whether a store commit survives it tells
// the in-process path from the git program's.
func hostHooks(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", failingPreCommit(t, t.TempDir()))
}

// TestPushRunsInProcessOnJigStores pins where a store write runs: a store
// shaped as jig creates one (its .gitattributes rules out line-ending
// conversion, and it declares nothing else go-git would not honor) is
// written in process, where the machine's git config does not apply. One
// that says nothing about line endings, or has hooks of its own, stays with
// the git program, which runs the hooks.
func TestPushRunsInProcessOnJigStores(t *testing.T) {
	addNote := func(t *testing.T, work string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a jig store", func(t *testing.T) {
		st, work, remote := newTestRemoteStore(t)
		hostHooks(t)
		addNote(t, work)
		if err := st.Push("add note"); err != nil {
			t.Fatalf("Push on a jig store: %v", err)
		}
		if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "log", "-1", "--pretty=%s")); got != "add note" {
			t.Fatalf("origin's last commit = %q, want the Push's", got)
		}
	})

	t.Run("a store that says nothing about line endings", func(t *testing.T) {
		st, work, _ := newTestRemoteStore(t)
		runGit(t, work, "rm", "-q", ".gitattributes")
		runGit(t, work, "commit", "-q", "-m", "no attributes")
		hostHooks(t)
		addNote(t, work)
		if err := st.Push("add note"); err == nil {
			t.Fatal("Push = nil, want the git program's failing pre-commit hook to stop it")
		}
	})

	t.Run("a store with a hook of its own", func(t *testing.T) {
		st, work, _ := newTestRemoteStore(t)
		failingPreCommit(t, filepath.Join(work, ".git", "hooks"))
		addNote(t, work)
		if err := st.Push("add note"); err == nil {
			t.Fatal("Push = nil, want the store's own failing pre-commit hook to stop it")
		}
	})
}
