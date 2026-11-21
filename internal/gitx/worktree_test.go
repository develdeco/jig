package gitx

import (
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

// TestWorktreesOfANonRepository is an error, not an empty list.
func TestWorktreesOfANonRepository(t *testing.T) {
	t.Parallel()
	if trees, err := Worktrees(t.TempDir()); err == nil {
		t.Errorf("Worktrees of a directory that is no repository = %+v, want an error", trees)
	}
}
