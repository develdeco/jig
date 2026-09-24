package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// gitRun runs git in dir for a test, failing it on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// initLeaseRepo turns dir, which must already exist, into a git repo with a
// pinned local identity, standing in for a pool lease's worktree.
func initLeaseRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.name", "jig-fixture")
	gitRun(t, dir, "config", "user.email", "fixture@example.invalid")
}

// commitLeaseFile writes content to name inside dir and commits it as an
// ordinary file (mode 100644), so it becomes part of dir's HEAD tree.
func commitLeaseFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gitRun(t, dir, "add", "--", name)
	gitRun(t, dir, "commit", "-m", "add "+name)
}

// commitLeaseSymlink commits name inside dir's HEAD tree as a git symlink
// entry (mode 120000) pointing at target, via plumbing rather than an OS
// symlink: a committed symlink entry must exist for a test regardless of
// whether the host filesystem or the invoking user can create a real one.
func commitLeaseSymlink(t *testing.T, dir, name, target string) {
	t.Helper()
	targetFile := filepath.Join(t.TempDir(), "symlink-target")
	if err := os.WriteFile(targetFile, []byte(target), 0o644); err != nil {
		t.Fatalf("write symlink target content: %v", err)
	}
	hash := gitRun(t, dir, "hash-object", "-w", targetFile)
	gitRun(t, dir, "update-index", "--add", "--cacheinfo", "120000,"+hash+","+name)
	gitRun(t, dir, "commit", "-m", "make "+name+" a symlink")
}

// TestLeaseMemoryAppendsTheCommittedFile pins the ordinary case: a lease
// with a plain, committed CLAUDE.md gets it carried as a jig-owned 0600 temp
// file whose content matches the commit, and the cleanup func removes that
// file.
func TestLeaseMemoryAppendsTheCommittedFile(t *testing.T) {
	dir := t.TempDir()
	initLeaseRepo(t, dir)
	commitLeaseFile(t, dir, "CLAUDE.md", "# conventions\ndo the thing\n")

	path, cleanup, err := leaseMemory(dir)
	if err != nil {
		t.Fatalf("leaseMemory: %v", err)
	}
	if path == "" {
		t.Fatal("leaseMemory returned no path for a lease with a committed CLAUDE.md")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the carried memory file: %v", err)
	}
	if string(got) != "# conventions\ndo the thing\n" {
		t.Errorf("carried memory file = %q, want the committed content", got)
	}
	if fi, err := os.Stat(path); err != nil {
		t.Fatalf("stat the carried memory file: %v", err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("carried memory file mode = %v, want 0600", fi.Mode().Perm())
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("carried memory file still exists after cleanup (stat err: %v)", err)
	}
}

// TestLeaseMemorySkipsANonRepoWorktree proves leaseMemory never falls back
// to reading CLAUDE.md off disk: a plain directory (not a git repository)
// with a real CLAUDE.md file sitting in it still yields nothing to append.
func TestLeaseMemorySkipsANonRepoWorktree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("not carried\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := leaseMemory(dir)
	defer cleanup()
	if err != nil {
		t.Fatalf("leaseMemory: %v", err)
	}
	if path != "" {
		t.Errorf("leaseMemory(%q) = %q, want \"\" for a lease that is not a git repository", dir, path)
	}
}

// TestLeaseMemorySkipsAMissingEntry proves a lease with commits but no
// CLAUDE.md at HEAD carries nothing, the same as a lease with none at all.
func TestLeaseMemorySkipsAMissingEntry(t *testing.T) {
	dir := t.TempDir()
	initLeaseRepo(t, dir)
	commitLeaseFile(t, dir, "seed.txt", "seed\n")

	path, cleanup, err := leaseMemory(dir)
	defer cleanup()
	if err != nil {
		t.Fatalf("leaseMemory: %v", err)
	}
	if path != "" {
		t.Errorf("leaseMemory with no CLAUDE.md committed = %q, want \"\"", path)
	}
}

// TestLeaseMemorySkipsACommittedSymlink proves a committed CLAUDE.md that is
// itself a symlink (mode 120000) is not read: the blob exists and has
// content, but that content is a link target string, not memory to append,
// and following it would let a ticket branch name any file on the machine.
func TestLeaseMemorySkipsACommittedSymlink(t *testing.T) {
	dir := t.TempDir()
	initLeaseRepo(t, dir)
	commitLeaseFile(t, dir, "seed.txt", "seed\n")
	commitLeaseSymlink(t, dir, "CLAUDE.md", "../outside/secret.txt")

	path, cleanup, err := leaseMemory(dir)
	defer cleanup()
	if err != nil {
		t.Fatalf("leaseMemory: %v", err)
	}
	if path != "" {
		t.Errorf("leaseMemory with a committed symlink CLAUDE.md = %q, want \"\"", path)
	}
}

// TestLeaseMemoryIgnoresWorkingTreeOverride is the exfiltration case the fix
// closes: a lease commits an ordinary CLAUDE.md, then something (a previous
// slice's own Write/Edit, or anything else with access to the worktree)
// replaces the working-tree file with a symlink or a hard link to a file
// outside the lease, without committing that change. leaseMemory must still
// carry only the committed content, because it never reads the working tree
// at all.
func TestLeaseMemoryIgnoresWorkingTreeOverride(t *testing.T) {
	const committed = "# committed memory\n"

	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("REFUTE-FAKE-SECRET-BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		initLeaseRepo(t, dir)
		commitLeaseFile(t, dir, "CLAUDE.md", committed)

		claudeMD := filepath.Join(dir, "CLAUDE.md")
		if err := os.Remove(claudeMD); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secret, claudeMD); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		path, cleanup, err := leaseMemory(dir)
		defer cleanup()
		if err != nil {
			t.Fatalf("leaseMemory: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read the carried memory file: %v", err)
		}
		if string(got) != committed {
			t.Errorf("carried memory = %q, want the committed content %q (an untracked symlink must not override it)", got, committed)
		}
	})

	t.Run("hard link", func(t *testing.T) {
		dir := t.TempDir()
		initLeaseRepo(t, dir)
		commitLeaseFile(t, dir, "CLAUDE.md", committed)

		claudeMD := filepath.Join(dir, "CLAUDE.md")
		if err := os.Remove(claudeMD); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(secret, claudeMD); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}

		path, cleanup, err := leaseMemory(dir)
		defer cleanup()
		if err != nil {
			t.Fatalf("leaseMemory: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read the carried memory file: %v", err)
		}
		if string(got) != committed {
			t.Errorf("carried memory = %q, want the committed content %q (an untracked hard link must not override it)", got, committed)
		}
	})
}

// TestLeaseMemoryRefusesAnOversizedBlob proves the cap is enforced before
// any content is read, and the refusal names both the file's size and the
// cap rather than silently truncating or dropping it.
func TestLeaseMemoryRefusesAnOversizedBlob(t *testing.T) {
	dir := t.TempDir()
	initLeaseRepo(t, dir)
	over := leaseMemoryCap + 1
	commitLeaseFile(t, dir, "CLAUDE.md", strings.Repeat("x", over))

	path, cleanup, err := leaseMemory(dir)
	defer cleanup()
	if path != "" {
		t.Errorf("leaseMemory returned a path %q for an oversized blob, want none", path)
	}
	var ax *axi.Error
	if !errors.As(err, &ax) || ax.Code != "LEASE_MEMORY_TOO_LARGE" {
		t.Fatalf("leaseMemory error = %v, want an axi.Error with code LEASE_MEMORY_TOO_LARGE", err)
	}
	for _, want := range []string{strconv.Itoa(over), strconv.Itoa(leaseMemoryCap)} {
		if !strings.Contains(ax.Msg, want) {
			t.Errorf("LEASE_MEMORY_TOO_LARGE message %q does not name %q", ax.Msg, want)
		}
	}
}

// TestLeaseMemoryAcceptsExactlyTheCap proves the cap is inclusive: a blob of
// exactly leaseMemoryCap bytes is carried, not refused.
func TestLeaseMemoryAcceptsExactlyTheCap(t *testing.T) {
	dir := t.TempDir()
	initLeaseRepo(t, dir)
	commitLeaseFile(t, dir, "CLAUDE.md", strings.Repeat("x", leaseMemoryCap))

	path, cleanup, err := leaseMemory(dir)
	defer cleanup()
	if err != nil {
		t.Fatalf("leaseMemory at exactly the cap: %v", err)
	}
	if path == "" {
		t.Error("leaseMemory at exactly the cap carried nothing, want the file")
	}
}
