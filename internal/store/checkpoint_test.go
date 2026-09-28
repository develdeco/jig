package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingHooks writes hooks that always fail into dir, one per name, and
// returns dir.
func failingHooks(t *testing.T, dir string, names ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// hostConfig gives every git program this test starts, through config
// from the environment as a machine's own config would, a pre-commit and a
// pre-push hook that always fail and no local transport at all
// (protocol.file.allow=never): a commit, push or fetch by the git program
// fails. In-process store writes never read the machine's git config, so
// whether a store write survives it tells the in-process path from the git
// program's, step by step.
func hostConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", failingHooks(t, t.TempDir(), "pre-commit", "pre-push"))
	t.Setenv("GIT_CONFIG_KEY_1", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_1", "never")
}

// TestStoreWritesRunInProcessOnJigStores pins where a store write runs: on
// a store shaped as jig creates one (its .gitattributes rules out every
// conversion, and it declares nothing else gitx would not honor) the
// commit, the push and the fetch run in process, where the machine's git
// config does not apply; a store behind its remote still fast-forwards
// with the git program's merge, which needs no transport. A store that
// says nothing about conversion, or has hooks of its own, stays with the
// git program, which runs the hooks.
func TestStoreWritesRunInProcessOnJigStores(t *testing.T) {
	addNote := func(t *testing.T, work string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a jig store's push", func(t *testing.T) {
		st, work, remote := newTestRemoteStore(t)
		hostConfig(t)
		addNote(t, work)
		if err := st.Push("add note"); err != nil {
			t.Fatalf("Push on a jig store: %v", err)
		}
		if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "log", "-1", "--pretty=%s")); got != "add note" {
			t.Fatalf("origin's last commit = %q, want the Push's", got)
		}
	})

	t.Run("a jig store's sync from behind", func(t *testing.T) {
		st, work, remote := newTestRemoteStore(t)
		other := filepath.Join(t.TempDir(), "other")
		runGit(t, "", "clone", "-q", remote, other)
		if err := os.WriteFile(filepath.Join(other, "theirs.txt"), []byte("theirs\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, other, "add", "-A")
		runGit(t, other, "-c", "user.name=o", "-c", "user.email=o@example.invalid", "commit", "-q", "-m", "theirs")
		runGit(t, other, "push", "-q", "origin", "main")
		hostConfig(t)
		if err := st.Sync(); err != nil {
			t.Fatalf("Sync on a jig store behind its remote: %v", err)
		}
		if b, err := os.ReadFile(filepath.Join(work, "theirs.txt")); err != nil || string(b) != "theirs\n" {
			t.Fatalf("theirs.txt after Sync = %q, %v; want it fast-forwarded in", b, err)
		}
	})

	t.Run("a store that says nothing about conversion", func(t *testing.T) {
		st, work, _ := newTestRemoteStore(t)
		runGit(t, work, "rm", "-q", ".gitattributes")
		runGit(t, work, "commit", "-q", "-m", "no attributes")
		hostConfig(t)
		addNote(t, work)
		if err := st.Push("add note"); err == nil {
			t.Fatal("Push = nil, want the git program's failing pre-commit hook to stop it")
		}
	})

	t.Run("a store with a hook of its own", func(t *testing.T) {
		st, work, _ := newTestRemoteStore(t)
		failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
		addNote(t, work)
		if err := st.Push("add note"); err == nil {
			t.Fatal("Push = nil, want the store's own failing pre-commit hook to stop it")
		}
	})
}

// TestStoreWritesFallBackWhenGoGitCannotReadTheIndex: a machine whose git
// config makes the git program write an index go-git cannot read
// (index.skipHash, which feature.manyFiles turns on) keeps working: the
// store hands every step to the git program.
func TestStoreWritesFallBackWhenGoGitCannotReadTheIndex(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "index.skipHash")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	runGit(t, work, "add", "-A") // rewrites the index as the machine's config says
	for i, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := st.Push("write " + name); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "log", "-1", "--pretty=%s")); got != "write b.txt" {
		t.Fatalf("origin's last commit = %q", got)
	}
}
