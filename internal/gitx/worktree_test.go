package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// worktreeListed reports whether repo's worktree list names dir, by file
// identity when the directory is there and by spelling when it is not.
func worktreeListed(t *testing.T, repo, dir string) bool {
	t.Helper()
	out, err := Run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	// SameDir needs the directory to exist, and one test deletes it. git
	// lists the long, symlink-free spelling of a path, so resolve the parent
	// that still exists: on a Windows runner the temp directory is an 8.3
	// short name (RUNNER~1), and on macOS /var is a link to /private/var.
	want := filepath.ToSlash(dir)
	if parent, err := filepath.EvalSymlinks(filepath.Dir(dir)); err == nil {
		want = filepath.ToSlash(filepath.Join(parent, filepath.Base(dir)))
	}
	for _, line := range strings.Split(out, "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		if SameDir(path, dir) || strings.EqualFold(filepath.ToSlash(path), want) || strings.EqualFold(filepath.ToSlash(path), filepath.ToSlash(dir)) {
			return true
		}
	}
	return false
}

// TestWorktreeAddDetachedChecksTheCommitOutWithoutMovingTheRepo pins the
// worktree jig verify runs an oracle in: the named commit's files, detached
// at it, in a directory of its own, while the repo it came from keeps its
// branch and its working tree.
func TestWorktreeAddDetachedChecksTheCommitOutWithoutMovingTheRepo(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	first, err := RevParse(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "f.txt", "second")
	dir := filepath.Join(t.TempDir(), "nested", "wt")

	if err := WorktreeAddDetached(repo, dir, first); err != nil {
		t.Fatalf("WorktreeAddDetached: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "f.txt")); err != nil || string(got) != "x" {
		t.Errorf("f.txt in the worktree = %q, %v, want the first commit's content", got, err)
	}
	if head, err := RevParse(dir, "HEAD"); err != nil || head != first {
		t.Errorf("worktree HEAD = %q, %v, want %s", head, err, first)
	}
	if branch, _ := Run(dir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "HEAD" {
		t.Errorf("worktree is on %q, want a detached HEAD", branch)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "f.txt")); string(got) != "second" {
		t.Errorf("the repo's own working tree changed: f.txt = %q", got)
	}
	if branch, _ := Run(repo, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Errorf("the repo moved to %q, want to stay on main", branch)
	}
	if !worktreeListed(t, repo, dir) {
		t.Errorf("git does not list %s as a worktree of the repo", dir)
	}
}

// TestWorktreeAddDetachedRefusesWhatItCannotCheckOut keeps a failure an error:
// an unknown revision, a directory that already holds files, and a revision
// spelled like an option.
func TestWorktreeAddDetachedRefusesWhatItCannotCheckOut(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	if err := WorktreeAddDetached(repo, filepath.Join(t.TempDir(), "a"), "0123456789012345678901234567890123456789"); err == nil {
		t.Error("an unknown commit was checked out")
	}
	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "keep.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WorktreeAddDetached(repo, occupied, "HEAD"); err == nil {
		t.Error("a directory with files in it was used")
	}
	if got, _ := os.ReadFile(filepath.Join(occupied, "keep.txt")); string(got) != "mine" {
		t.Errorf("the occupied directory lost its file: %q", got)
	}
	if err := WorktreeAddDetached(repo, filepath.Join(t.TempDir(), "b"), "--orphan"); err == nil {
		t.Error("a revision spelled like an option was passed to git")
	}
}

// TestWorktreeRemove covers the cleanup of a run: a worktree the oracle left
// dirty goes (directory and git's record of it), and the call is safe to
// repeat, to make for a directory git never registered, and for one that is
// not there.
func TestWorktreeRemove(t *testing.T) {
	t.Parallel()
	// Each case has a repo of its own: pruning in one must not meet a worktree
	// another is still adding.
	newRepo := func(t *testing.T) string {
		t.Helper()
		return initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	}

	t.Run("a dirty worktree is removed with git's record of it", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		dir := filepath.Join(t.TempDir(), "wt")
		if err := WorktreeAddDetached(repo, dir, "HEAD"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("edited by the oracle"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("left behind"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := WorktreeRemove(repo, dir); err != nil {
			t.Fatalf("WorktreeRemove: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("the directory is still there: %v", err)
		}
		if worktreeListed(t, repo, dir) {
			t.Error("git still lists the removed worktree")
		}
		if err := WorktreeRemove(repo, dir); err != nil {
			t.Errorf("a second WorktreeRemove: %v", err)
		}
	})
	t.Run("a directory git never registered is deleted", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		dir := filepath.Join(t.TempDir(), "leftover")
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sub", "x"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := WorktreeRemove(repo, dir); err != nil {
			t.Fatalf("WorktreeRemove: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("the directory is still there: %v", err)
		}
	})
	t.Run("a worktree whose directory was deleted by hand is pruned", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		dir := filepath.Join(t.TempDir(), "wt")
		if err := WorktreeAddDetached(repo, dir, "HEAD"); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if !worktreeListed(t, repo, dir) {
			t.Fatal("test setup: git no longer lists the worktree whose directory was deleted, so there is nothing to prune")
		}
		if err := WorktreeRemove(repo, dir); err != nil {
			t.Fatalf("WorktreeRemove: %v", err)
		}
		if worktreeListed(t, repo, dir) {
			t.Error("git still lists the worktree whose directory was deleted")
		}
		// The same directory can be added again.
		if err := WorktreeAddDetached(repo, dir, "HEAD"); err != nil {
			t.Errorf("adding at the pruned directory again: %v", err)
		}
	})
}

// TestTrailer reads a commit trailer the way git interprets them: from the
// last paragraph when it is all trailers, by key without regard to case, the
// last of several, and nothing from a body line that only looks like one.
func TestTrailer(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	commit := func(name string, paragraphs ...string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(repo, "add", "-A"); err != nil {
			t.Fatal(err)
		}
		args := []string{"commit"}
		for _, p := range paragraphs {
			args = append(args, "-m", p)
		}
		if _, err := Run(repo, args...); err != nil {
			t.Fatal(err)
		}
		sha, err := RevParse(repo, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}

	withTrailer := commit("a", "subject", "Jig-Slice: a")
	for _, key := range []string{"Jig-Slice", "jig-slice", "JIG-SLICE"} {
		if got, err := Trailer(repo, withTrailer, key); err != nil || got != "a" {
			t.Errorf("Trailer(%q) = %q, %v, want a", key, got, err)
		}
	}
	if got, err := Trailer(repo, withTrailer, "Reviewed-By"); err != nil || got != "" {
		t.Errorf("Trailer of an absent key = %q, %v, want empty", got, err)
	}

	several := commit("b", "subject", "body text", "Jig-Slice: first\nSigned-off-by: Someone\nJig-Slice: second")
	if got, err := Trailer(repo, several, "Jig-Slice"); err != nil || got != "second" {
		t.Errorf("Trailer with two = %q, %v, want the last, second", got, err)
	}
	if got, err := Trailer(repo, several, "Signed-off-by"); err != nil || got != "Someone" {
		t.Errorf("Trailer(Signed-off-by) = %q, %v", got, err)
	}

	inBody := commit("c", "subject\n\nJig-Slice: not-a-trailer\n\nmore body text after it")
	if got, err := Trailer(repo, inBody, "Jig-Slice"); err != nil || got != "" {
		t.Errorf("Trailer of a body line = %q, %v, want empty: it is not in the trailer block", got, err)
	}

	none := commit("d", "just a subject")
	if got, err := Trailer(repo, none, "Jig-Slice"); err != nil || got != "" {
		t.Errorf("Trailer of a commit with none = %q, %v, want empty", got, err)
	}

	if _, err := Trailer(repo, withTrailer, "bad key,x"); err == nil {
		t.Error("a key with a comma was accepted")
	}
	if _, err := Trailer(repo, "--all", "Jig-Slice"); err == nil {
		t.Error("a revision spelled like an option was accepted")
	}
	if _, err := Trailer(repo, "0123456789012345678901234567890123456789", "Jig-Slice"); err == nil {
		t.Error("an unknown commit was read")
	}
}
