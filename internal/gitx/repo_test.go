package gitx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// mustRun runs the git program in dir and fails t on error.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Run(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// writeFileT writes body to dir/name, creating parents.
func writeFileT(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readFileT reads dir/name.
func readFileT(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// commitFile writes body to file in dir and commits it with the git
// program, returning the new commit.
func commitFile(t *testing.T, dir, file, body string) string {
	t.Helper()
	writeFileT(t, dir, file, body)
	mustRun(t, dir, "add", "-A")
	mustRun(t, dir, "-c", "user.name=o", "-c", "user.email=o@example.invalid", "commit", "-q", "-m", file)
	return mustRun(t, dir, "rev-parse", "HEAD")
}

// newStoreRepo creates a repository shaped like a jig store (StoreAttributes
// at its root, one commit on main) and returns its directory.
func newStoreRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRun(t, dir, "init", "-q", "-b", "main")
	mustRun(t, dir, "config", "user.name", "tester")
	mustRun(t, dir, "config", "user.email", "tester@example.invalid")
	writeFileT(t, dir, ".gitattributes", StoreAttributes)
	writeFileT(t, dir, ".gitignore", "*.lock\n")
	writeFileT(t, dir, "project.yaml", "schema_version: 1\n")
	mustRun(t, dir, "add", "-A")
	mustRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// newRemotePair returns a store repository with a bare origin it has pushed
// main to, and that origin's path.
func newRemotePair(t *testing.T) (work, remote string) {
	t.Helper()
	work = newStoreRepo(t)
	remote = filepath.Join(t.TempDir(), "origin.git")
	mustRun(t, "", "init", "-q", "--bare", "-b", "main", remote)
	mustRun(t, work, "remote", "add", "origin", remote)
	mustRun(t, work, "push", "-q", "origin", "main")
	return work, remote
}

// openRepo opens dir in process and fails t if it cannot.
func openRepo(t *testing.T, dir string) *Repo {
	t.Helper()
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo(%s): %v", dir, err)
	}
	return r
}

// TestOpenRepoOnlyWhenNothingConvertsFileContents: gitx converts nothing
// and runs no filters, so a repository is opened in process only when its
// own root .gitattributes rules every conversion out. A config setting
// alone is not enough, since a machine's attributes file could still turn
// conversion on.
func TestOpenRepoOnlyWhenNothingConvertsFileContents(t *testing.T) {
	cases := []struct {
		name string
		lay  func(t *testing.T, dir string)
		ok   bool
	}{
		{"says nothing", func(*testing.T, string) {}, false},
		{"only its own core.autocrlf=false", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.autocrlf", "false")
		}, false},
		{"jig's store attributes", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", StoreAttributes)
		}, true},
		{"jig's store attributes with CRLF", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", strings.ReplaceAll(StoreAttributes, "\n", "\r\n"))
		}, true},
		{"line endings alone", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", "* -text\n")
		}, false},
		{"another text rule", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", "*.md text eol=crlf\n")
		}, false},
		{"a filter as well", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", StoreAttributes+"*.bin filter=lfs\n")
		}, false},
		{"info/attributes as well", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", StoreAttributes)
			writeFileT(t, dir, ".git/info/attributes", "*.md text\n")
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			mustRun(t, dir, "init", "-q", "-b", "main")
			c.lay(t, dir)
			_, err := OpenRepo(dir)
			if c.ok && err != nil {
				t.Fatalf("OpenRepo = %v, want it opened", err)
			}
			if !c.ok && !errors.Is(err, ErrUseCLI) {
				t.Fatalf("OpenRepo = %v, want ErrUseCLI", err)
			}
		})
	}

	t.Run("a linked worktree", func(t *testing.T) {
		main := newStoreRepo(t)
		linked := filepath.Join(t.TempDir(), "linked")
		mustRun(t, main, "worktree", "add", "-q", "-b", "side", linked)
		if _, err := OpenRepo(linked); !errors.Is(err, ErrUseCLI) {
			t.Fatalf("OpenRepo(linked worktree) = %v, want ErrUseCLI", err)
		}
	})
}

// TestOpenRepoLeavesWhatTheRepositoryDeclaresToTheGitProgram: a repository
// whose own files or config ask for something gitx does not do in process
// is left to the git program, while git's hook samples alone change
// nothing.
func TestOpenRepoLeavesWhatTheRepositoryDeclaresToTheGitProgram(t *testing.T) {
	config := func(key, value string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) { mustRun(t, dir, "config", key, value) }
	}
	cases := []struct {
		name string
		lay  func(t *testing.T, dir string)
		ok   bool
	}{
		{"nothing declared", func(*testing.T, string) {}, true},
		{"only git's hook samples", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/hooks/pre-commit.sample", "#!/bin/sh\n")
		}, true},
		{"its own hook", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/hooks/post-commit", "#!/bin/sh\n")
		}, false},
		{"its own core.hooksPath", config("core.hooksPath", "hooks"), false},
		{"a shallow history", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/shallow", mustRun(t, dir, "rev-parse", "HEAD")+"\n")
		}, false},
		{"grafts", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/info/grafts", "")
		}, false},
		{"alternates", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/objects/info/alternates", t.TempDir()+"\n")
		}, false},
		{"a format extension", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.repositoryFormatVersion", "1")
			mustRun(t, dir, "config", "extensions.worktreeConfig", "true")
		}, false},
		{"group-shared", config("core.sharedRepository", "group"), false},
		{"an included config file", config("include.path", "extra.config"), false},
		{"a conditionally included config file", config("includeIf.onbranch:main.path", "extra.config"), false},
		{"its own excludes file", config("core.excludesFile", "ignored"), false},
		{"its own attributes file", config("core.attributesFile", "attributes"), false},
		{"a separate work tree", config("core.worktree", "elsewhere"), false},
		{"a split index", config("core.splitIndex", "true"), false},
		{"a sparse checkout", config("core.sparseCheckout", "true"), false},
		{"a sparse index", config("index.sparse", "true"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newStoreRepo(t)
			c.lay(t, dir)
			_, err := OpenRepo(dir)
			if c.ok && err != nil {
				t.Fatalf("OpenRepo = %v, want it opened", err)
			}
			if !c.ok && !errors.Is(err, ErrUseCLI) {
				t.Fatalf("OpenRepo = %v, want ErrUseCLI", err)
			}
		})
	}
}

// TestRepoState pins what one in-process look reports: the branch (an
// unborn one included, none when HEAD is detached) and unmerged entries.
func TestRepoState(t *testing.T) {
	cases := []struct {
		name string
		lay  func(t *testing.T, dir string)
		want RepoState
	}{
		{"clean", func(*testing.T, string) {}, RepoState{Branch: "main"}},
		{"changes", func(t *testing.T, dir string) {
			writeFileT(t, dir, "T-1/journal.ndjson", "{}\n")
			writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
		}, RepoState{Branch: "main"}},
		{"a detached HEAD", func(t *testing.T, dir string) {
			mustRun(t, dir, "checkout", "-q", "--detach")
		}, RepoState{}},
		{"unmerged entries", func(t *testing.T, dir string) {
			writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
			mustRun(t, dir, "stash", "-q")
			writeFileT(t, dir, "project.yaml", "schema_version: 3\n")
			mustRun(t, dir, "commit", "-q", "-am", "three")
			if _, err := Run(dir, "stash", "pop"); err == nil {
				t.Fatal("setup: stash pop did not conflict")
			}
		}, RepoState{Branch: "main", Unmerged: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newStoreRepo(t)
			c.lay(t, dir)
			got, err := openRepo(t, dir).State()
			if err != nil {
				t.Fatalf("State: %v", err)
			}
			if got != c.want {
				t.Fatalf("State = %+v, want %+v", got, c.want)
			}
		})
	}

	t.Run("an unborn branch", func(t *testing.T) {
		dir := t.TempDir()
		mustRun(t, dir, "init", "-q", "-b", "trunk")
		writeFileT(t, dir, ".gitattributes", StoreAttributes)
		got, err := openRepo(t, dir).State()
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		if want := (RepoState{Branch: "trunk"}); got != want {
			t.Fatalf("State = %+v, want %+v", got, want)
		}
	})
}

// TestAnIndexGoGitCannotReadIsTheGitPrograms: the git program writes an
// index go-git cannot read when a machine's config asks for it (skipHash,
// which feature.manyFiles turns on, or a split index). State and CommitAll
// then hand the store to the git program rather than failing, and change
// nothing.
func TestAnIndexGoGitCannotReadIsTheGitPrograms(t *testing.T) {
	for _, opt := range []string{"index.skipHash=true", "core.splitIndex=true"} {
		t.Run(opt, func(t *testing.T) {
			dir := newStoreRepo(t)
			writeFileT(t, dir, "a.txt", "a\n")
			mustRun(t, dir, "-c", opt, "add", "-A")
			r := openRepo(t, dir)
			if _, err := r.State(); !errors.Is(err, ErrUseCLI) {
				t.Fatalf("State = %v, want ErrUseCLI", err)
			}
			if _, err := r.CommitAll("x", "jig", "jig@invalid"); !errors.Is(err, ErrUseCLI) {
				t.Fatalf("CommitAll = %v, want ErrUseCLI", err)
			}
			if got := mustRun(t, dir, "log", "-1", "--format=%s"); got != "init" {
				t.Fatalf("HEAD = %q, want the commit before", got)
			}
			if _, err := os.Stat(filepath.Join(dir, ".git", "index.lock")); !os.IsNotExist(err) {
				t.Fatalf("index.lock left behind: %v", err)
			}
		})
	}
}

// TestOpenRepoAnythingGoGitCannotOpen: a directory go-git cannot open as a
// repository is the git program's to report on.
func TestOpenRepoAnythingGoGitCannotOpen(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, dir, ".git/HEAD", "not a ref\n")
	if _, err := OpenRepo(dir); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("OpenRepo(broken) = %v, want ErrUseCLI", err)
	}
	if _, err := OpenRepo(t.TempDir()); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("OpenRepo(no repository) = %v, want ErrUseCLI", err)
	}
}

// TestWriteRefComparesUnderTheLock: a ref moves only from the value the
// caller read; when it holds another, nothing is written and the lock is
// released.
func TestWriteRefComparesUnderTheLock(t *testing.T) {
	dir := newStoreRepo(t)
	head := mustRun(t, dir, "rev-parse", "HEAD")
	next := commitFile(t, dir, "a.txt", "a\n")
	repo, done, err := openRepo(t, dir).open()
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	gitDir := filepath.Join(dir, ".git")
	name := plumbing.NewBranchReferenceName("main")
	stale := plumbing.NewHash(head)
	if err := writeRef(repo, gitDir, name, stale, &stale); !errors.Is(err, errRefMoved) {
		t.Fatalf("writeRef from a stale value = %v, want errRefMoved", err)
	}
	if got := mustRun(t, dir, "rev-parse", "main"); got != next {
		t.Fatalf("main = %s after a refused write, want %s", got, next)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "refs", "heads", "main.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock after a refused write: %v", err)
	}
	cur := plumbing.NewHash(next)
	if err := writeRef(repo, gitDir, name, stale, &cur); err != nil {
		t.Fatalf("writeRef from the current value: %v", err)
	}
	if got := mustRun(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main = %s, want %s", got, head)
	}
	mustRun(t, dir, "pack-refs", "--all")
	if err := writeRef(repo, gitDir, name, cur, &cur); !errors.Is(err, errRefMoved) {
		t.Fatalf("writeRef against a packed ref from a stale value = %v, want errRefMoved", err)
	}
	if err := writeRef(repo, gitDir, name, cur, &stale); err != nil {
		t.Fatalf("writeRef against a packed ref: %v", err)
	}
	if got := mustRun(t, dir, "rev-parse", "main"); got != next {
		t.Fatalf("main = %s, want %s", got, next)
	}
}

// TestLooseObjectsPastAutoGC: git's own estimate of work for automatic gc,
// the loose objects in objects/17 above gc.auto/256 or more packs without a
// .keep than gc.autoPackLimit, from the repository's own config (6700 and
// 50 by default; gc.auto=0 turns both off).
func TestLooseObjectsPastAutoGC(t *testing.T) {
	// loose leaves exactly n loose objects in objects/17, counting any the
	// repository's own commits happened to put there.
	loose := func(t *testing.T, dir string, n int) {
		t.Helper()
		obj := filepath.Join(dir, ".git", "objects", "17")
		if err := os.MkdirAll(obj, 0o755); err != nil {
			t.Fatal(err)
		}
		existing, err := os.ReadDir(obj)
		if err != nil {
			t.Fatal(err)
		}
		for i := len(existing); i < n; i++ {
			name := strings.Repeat("0", 36) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
			if err := os.WriteFile(filepath.Join(obj, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// Not a loose object's name: never counted.
		if err := os.WriteFile(filepath.Join(obj, "tmp_obj_x"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// packs adds n pack files, each with a .keep when keep is set.
	packs := func(t *testing.T, dir string, n int, keep bool) {
		t.Helper()
		for i := 0; i < n; i++ {
			base := fmt.Sprintf(".git/objects/pack/pack-%040d", i)
			writeFileT(t, dir, base+".pack", "")
			if keep {
				writeFileT(t, dir, base+".keep", "")
			}
		}
	}
	cases := []struct {
		name   string
		config [][2]string
		loose  int
		packs  int
		keep   bool
		want   bool
	}{
		{"default limit, at it", nil, 27, 0, false, false},
		{"default limit, past it", nil, 28, 0, false, true},
		{"its own lower limit", [][2]string{{"gc.auto", "512"}}, 3, 0, false, true},
		{"default pack limit, at it", nil, 0, 50, false, false},
		{"default pack limit, past it", nil, 0, 51, false, true},
		{"its own lower pack limit", [][2]string{{"gc.autoPackLimit", "2"}}, 0, 3, false, true},
		{"kept packs", [][2]string{{"gc.autoPackLimit", "2"}}, 0, 3, true, false},
		{"pack limit off", [][2]string{{"gc.autoPackLimit", "0"}}, 0, 60, false, false},
		{"automatic gc off", [][2]string{{"gc.auto", "0"}}, 60, 60, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newStoreRepo(t)
			for _, kv := range c.config {
				mustRun(t, dir, "config", kv[0], kv[1])
			}
			loose(t, dir, c.loose)
			packs(t, dir, c.packs, c.keep)
			if got := openRepo(t, dir).LooseObjectsPastAutoGC(); got != c.want {
				t.Fatalf("LooseObjectsPastAutoGC = %v, want %v", got, c.want)
			}
		})
	}
}
