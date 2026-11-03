package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/filelock"
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
	// The checkout is the whole commit, staged as it is: nothing modified,
	// deleted or untracked, which is what verify's clean-tree check holds it to.
	if status, err := Run(dir, "status", "--porcelain"); err != nil || status != "" {
		t.Errorf("git status in the new worktree = %q, %v, want a clean tree", status, err)
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
	t.Run("a worktree git left locked is removed with its record", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		dir := filepath.Join(t.TempDir(), "wt")
		if err := WorktreeAddDetached(repo, dir, "HEAD"); err != nil {
			t.Fatal(err)
		}
		// An add that was killed half-way leaves its worktree locked.
		if _, err := Run(repo, "worktree", "lock", "--reason", "initializing", dir); err != nil {
			t.Fatal(err)
		}
		if err := WorktreeRemove(repo, dir); err != nil {
			t.Fatalf("WorktreeRemove: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("the directory is still there: %v", err)
		}
		if worktreeListed(t, repo, dir) {
			t.Error("git still lists the locked worktree")
		}
	})
}

// holdWorktreesLock takes repo's worktrees lock as another process would, and
// lets go of it when the test ends unless the test let go of it first.
func holdWorktreesLock(t *testing.T, repo string) (release func()) {
	t.Helper()
	unlock, held, err := filelock.TryLock(filepath.Join(repo, ".git", worktreesLockName), 0)
	if err != nil || !held {
		t.Fatalf("taking the worktrees lock: held %v, err %v", held, err)
	}
	var once sync.Once
	t.Cleanup(func() { once.Do(unlock) })
	return func() { once.Do(unlock) }
}

// halfAdded writes into repo the records git's worktree add has written of
// a worktree named id when it is part-way through: its directory under
// worktrees/, and, when files is set, a gitdir and a commondir that is still
// empty. Another add that reads them then dies ("failed to read
// .../commondir"); a prune deletes a directory with no gitdir in it.
func halfAdded(t *testing.T, repo, id string, files bool) string {
	t.Helper()
	records := filepath.Join(repo, ".git", "worktrees", id)
	if err := os.MkdirAll(records, 0o755); err != nil {
		t.Fatal(err)
	}
	if files {
		gitdir := filepath.ToSlash(filepath.Join(t.TempDir(), id, ".git"))
		if err := os.WriteFile(filepath.Join(records, "gitdir"), []byte(gitdir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(records, "commondir"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return records
}

// TestWorktreeAddDetachedWaitsForTheWorktreesLock: an add runs no git while
// another process holds the repository's worktrees lock, whose own add may
// have its records half written, as the verify workers of one clone do when
// they start together. It waits, and when the wait runs out it says the lock
// was busy and has made nothing. It is the same lock from a linked worktree
// of the clone, and once the holder lets go the add goes through.
func TestWorktreeAddDetachedWaitsForTheWorktreesLock(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	linked := filepath.Join(t.TempDir(), "linked")
	if err := WorktreeAddDetached(repo, linked, "HEAD"); err != nil {
		t.Fatal(err)
	}
	release := holdWorktreesLock(t, repo)
	records := halfAdded(t, repo, "other", true)

	for _, from := range []string{repo, linked} {
		dir := filepath.Join(t.TempDir(), "wt")
		err := worktreeAddDetached(from, dir, "HEAD", 0)
		if !errors.Is(err, errWorktreesBusy) {
			t.Fatalf("an add from %s while another process holds the lock = %v, want it to wait for the lock (%v)", from, err, errWorktreesBusy)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("the add that waited made %s: %v", dir, err)
		}
	}

	// The other add finishes (here, its records go) and lets go of the lock.
	if err := os.RemoveAll(records); err != nil {
		t.Fatal(err)
	}
	release()
	dir := filepath.Join(t.TempDir(), "wt")
	if err := worktreeAddDetached(repo, dir, "HEAD", 0); err != nil {
		t.Fatalf("the add once the lock is free: %v", err)
	}
	if !worktreeListed(t, repo, dir) {
		t.Errorf("git does not list %s as a worktree of the repo", dir)
	}
}

// TestWorktreeRemoveWaitsForTheWorktreesLock: a removal deletes its own
// directory but prunes nothing while another process holds the repository's
// worktrees lock, so the records of the holder's add, which a prune would
// take for a stale worktree's, stay. Once the holder lets go the removal goes
// through.
func TestWorktreeRemoveWaitsForTheWorktreesLock(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	dir := filepath.Join(t.TempDir(), "wt")
	if err := WorktreeAddDetached(repo, dir, "HEAD"); err != nil {
		t.Fatal(err)
	}
	release := holdWorktreesLock(t, repo)
	records := halfAdded(t, repo, "other", false)

	err := worktreeRemove(repo, dir, 0)
	if !errors.Is(err, errWorktreesBusy) {
		t.Fatalf("a removal while another process holds the lock = %v, want it to wait for the lock (%v)", err, errWorktreesBusy)
	}
	if _, err := os.Stat(records); err != nil {
		t.Errorf("the records of the other process's add were pruned: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the removal that waited left its directory: %v", err)
	}

	release()
	if err := worktreeRemove(repo, dir, 0); err != nil {
		t.Fatalf("the removal once the lock is free: %v", err)
	}
	if worktreeListed(t, repo, dir) {
		t.Error("git still lists the removed worktree")
	}
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

// TestParseWorktrees reads the porcelain list the way git writes it: the
// first block is the repository's own work tree, a detached or bare entry has
// no branch, a prunable one says so, and a path with spaces is kept whole.
func TestParseWorktrees(t *testing.T) {
	t.Parallel()
	const sha = "0123456789012345678901234567890123456789"
	cases := []struct {
		name string
		out  string
		want []Worktree
	}{
		{"nothing listed", "", nil},
		{"the repository's own work tree", "worktree /src/repo\nHEAD " + sha + "\nbranch refs/heads/main\n",
			[]Worktree{{Dir: "/src/repo", Branch: "main", Main: true}}},
		{"a linked one on a branch with a slash", "worktree /src/repo\nHEAD " + sha + "\nbranch refs/heads/main\n\nworktree /src/repo.worktrees/T-1\nHEAD " + sha + "\nbranch refs/heads/feat/t-1\n",
			[]Worktree{{Dir: "/src/repo", Branch: "main", Main: true}, {Dir: "/src/repo.worktrees/T-1", Branch: "feat/t-1"}}},
		{"detached has no branch", "worktree /src/repo\nHEAD " + sha + "\nbranch refs/heads/main\n\nworktree /tmp/verify\nHEAD " + sha + "\ndetached\n",
			[]Worktree{{Dir: "/src/repo", Branch: "main", Main: true}, {Dir: "/tmp/verify"}}},
		{"a bare repository", "worktree /src/repo.git\nbare\n\nworktree /src/wt\nHEAD " + sha + "\nbranch refs/heads/main\n",
			[]Worktree{{Dir: "/src/repo.git", Main: true}, {Dir: "/src/wt", Branch: "main"}}},
		{"prunable and locked", "worktree /src/repo\nHEAD " + sha + "\nbranch refs/heads/main\n\nworktree /gone/wt\nHEAD " + sha + "\nbranch refs/heads/old\nlocked\nprunable gitdir file points to non-existent location\n",
			[]Worktree{{Dir: "/src/repo", Branch: "main", Main: true}, {Dir: "/gone/wt", Branch: "old", Prunable: true}}},
		{"a path with spaces and windows line ends", "worktree C:/My Work/repo one\r\nHEAD " + sha + "\r\nbranch refs/heads/main\r\n\r\nworktree C:/My Work/repo one.worktrees/T-1\r\nHEAD " + sha + "\r\nbranch refs/heads/t-1\r\n",
			[]Worktree{{Dir: "C:/My Work/repo one", Branch: "main", Main: true}, {Dir: "C:/My Work/repo one.worktrees/T-1", Branch: "t-1"}}},
		{"blank lines between blocks are only separators", "\n\nworktree /src/repo\nbranch refs/heads/main\n\n\n\nworktree /src/wt\nbranch refs/heads/b\n\n",
			[]Worktree{{Dir: "/src/repo", Branch: "main", Main: true}, {Dir: "/src/wt", Branch: "b"}}},
		{"a block without a worktree line is skipped", "HEAD " + sha + "\nbranch refs/heads/x\n\nworktree /src/repo\nbranch refs/heads/main\n",
			[]Worktree{{Dir: "/src/repo", Branch: "main", Main: true}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := parseWorktrees(c.out)
			if len(got) != len(c.want) {
				t.Fatalf("parseWorktrees = %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("worktree %d = %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestWorktrees lists what the git program says about a real repository: its
// own work tree first, a linked worktree with the branch it holds, a detached
// one with none, and one whose directory is gone as prunable.
func TestWorktrees(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	outside := t.TempDir()
	linked := filepath.Join(outside, "repo with space.worktrees", "T-1")
	detached := filepath.Join(outside, "detached")
	gone := filepath.Join(outside, "gone")
	mustRun(t, repo, "worktree", "add", "-q", linked, "-b", "feat/t-1")
	if err := WorktreeAddDetached(repo, detached, "HEAD"); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "worktree", "add", "-q", gone, "-b", "gone-branch")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	trees, err := Worktrees(repo)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	byBranch := map[string]Worktree{}
	var detachedTrees []Worktree
	for _, w := range trees {
		if w.Branch == "" {
			detachedTrees = append(detachedTrees, w)
			continue
		}
		byBranch[w.Branch] = w
	}
	if len(trees) != 4 || !trees[0].Main || !SameDir(trees[0].Dir, repo) || trees[0].Branch != "main" || trees[0].Prunable {
		t.Fatalf("Worktrees = %+v, want the repo's own work tree first on main among four", trees)
	}
	if w, ok := byBranch["feat/t-1"]; !ok || w.Main || w.Prunable || !SameDir(w.Dir, linked) {
		t.Errorf("the linked worktree = %+v, %v, want %s on feat/t-1, not main, not prunable", w, ok, linked)
	}
	if w, ok := byBranch["gone-branch"]; !ok || !w.Prunable || w.Main {
		t.Errorf("the worktree whose directory is gone = %+v, %v, want it prunable", w, ok)
	}
	if len(detachedTrees) != 1 || detachedTrees[0].Main || detachedTrees[0].Prunable || !SameDir(detachedTrees[0].Dir, detached) {
		t.Errorf("detached worktrees = %+v, want %s alone", detachedTrees, detached)
	}
}

// TestWorktreesOfANonRepository is an error, not an empty list.
func TestWorktreesOfANonRepository(t *testing.T) {
	t.Parallel()
	if trees, err := Worktrees(t.TempDir()); err == nil {
		t.Errorf("Worktrees of a directory that is no repository = %+v, want an error", trees)
	}
}
