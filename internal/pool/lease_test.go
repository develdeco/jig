package pool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
)

// newEnclosingRepo creates a committed git working copy with its own origin,
// standing in for a dotfiles checkout that holds ~/.config/jig. notes.txt is
// its one tracked file.
func newEnclosingRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	run(t, dir, "add", "-A")
	commit(t, dir, "dotfiles")
	bare := filepath.Join(t.TempDir(), "dotfiles.git")
	run(t, filepath.Dir(bare), "clone", "--bare", dir, bare)
	run(t, dir, "remote", "add", "origin", bare)
	run(t, dir, "fetch", "origin")
	return dir
}

// assertEnclosingUntouched fails the test if any lease operation reached the
// enclosing repository: its branch, its branch list, or its uncommitted edit.
func assertEnclosingUntouched(t *testing.T, enclosing string) {
	t.Helper()
	if got := run(t, enclosing, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("enclosing repo HEAD = %q, want main: a lease checkout ran there", got)
	}
	if got := run(t, enclosing, "for-each-ref", "--format=%(refname)", "refs/heads"); got != "refs/heads/main" {
		t.Errorf("enclosing repo branches = %q, want only refs/heads/main", got)
	}
	if got, err := os.ReadFile(filepath.Join(enclosing, "notes.txt")); err != nil || string(got) != "uncommitted work\n" {
		t.Errorf("enclosing repo's uncommitted edit = %q, %v; want it untouched", got, err)
	}
}

// assertOwnClone fails the test unless dir is itself the top level of a git
// working copy checked out on branch at remote's main.
func assertOwnClone(t *testing.T, dir, remote, branch string) {
	t.Helper()
	top := run(t, dir, "rev-parse", "--show-toplevel")
	topInfo, err := os.Stat(top)
	if err != nil {
		t.Fatalf("stat lease toplevel %q: %v", top, err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat lease: %v", err)
	}
	if !os.SameFile(topInfo, dirInfo) {
		t.Fatalf("lease toplevel = %q, want the lease itself (%s)", top, dir)
	}
	if got := run(t, dir, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("lease HEAD = %q, want %s", got, branch)
	}
	if got, want := run(t, dir, "rev-parse", "HEAD"), run(t, remote, "rev-parse", "main"); got != want {
		t.Fatalf("lease HEAD = %s, want the remote's main %s", got, want)
	}
}

// TestAcquireRecoversBrokenLease covers every shape of lease directory git
// shows is not a repository of its own, with JIG_HOME inside an enclosing
// working copy that has its own origin. A `.git` entry alone used to mark a
// lease reusable, so for a .git directory git cannot open, or a .git file
// naming another repository, Acquire fetched and ran `checkout -B` in the
// enclosing repository; leftover files and a file at the lease path made
// every later clone fail. Acquire must never touch the enclosing
// repository, must move a broken lease aside (never delete it), and must
// clone afresh.
func TestAcquireRecoversBrokenLease(t *testing.T) {
	cases := []struct {
		name string
		// lay puts the broken lease at dir and returns the path, relative to
		// the lease, of a file that must survive in the moved-aside copy ("."
		// when the lease itself is that file).
		lay func(t *testing.T, dir, enclosing string) string
	}{
		{
			name: "a .git directory git cannot open",
			lay: func(t *testing.T, dir, _ string) string {
				rel := filepath.Join(".git", "objects", "pack", "pack-leftover.pack")
				writeFile(t, filepath.Join(dir, rel), "left behind by a locked file\n")
				return rel
			},
		},
		{
			name: "a .git file naming the enclosing repository",
			lay: func(t *testing.T, dir, enclosing string) string {
				writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.Join(enclosing, ".git")+"\n")
				return ".git"
			},
		},
		{
			name: "files left without a .git",
			lay: func(t *testing.T, dir, _ string) string {
				writeFile(t, filepath.Join(dir, "stray.txt"), "left behind by a locked file\n")
				return "stray.txt"
			},
		},
		{
			name: "a file at the lease path",
			lay: func(t *testing.T, dir, _ string) string {
				writeFile(t, dir, "not a directory\n")
				return "."
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enclosing := newEnclosingRepo(t)
			home := filepath.Join(enclosing, "jig-home")
			t.Setenv("JIG_HOME", home)
			remote := newSourceAndRemote(t)
			dir := filepath.Join(home, "pool", "fixture", "T-1")
			marker := c.lay(t, dir, enclosing)
			want, err := os.ReadFile(filepath.Join(dir, marker))
			if err != nil {
				t.Fatalf("read marker: %v", err)
			}
			writeFile(t, filepath.Join(enclosing, "notes.txt"), "uncommitted work\n")

			lease, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)

			assertEnclosingUntouched(t, enclosing)
			if err != nil {
				t.Fatalf("Acquire over a broken lease should recover, got: %v", err)
			}
			if lease.Dir != dir {
				t.Fatalf("lease dir = %q, want %q", lease.Dir, dir)
			}
			assertOwnClone(t, dir, remote, "jig/T-1")

			asides, err := filepath.Glob(dir + ".broken-*")
			if err != nil || len(asides) != 1 {
				t.Fatalf("moved-aside leases = %v (%v), want exactly one T-1.broken-<time>", asides, err)
			}
			if got, err := os.ReadFile(filepath.Join(asides[0], marker)); err != nil || string(got) != string(want) {
				t.Fatalf("moved-aside lease lost %s: %q, %v", marker, got, err)
			}
		})
	}
}

// TestAcquireClonesIntoEmptyDir covers a clone killed right after creating
// its target directory: an empty directory holds nothing worth keeping, so
// Acquire clones into it in place instead of moving it aside.
func TestAcquireClonesIntoEmptyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JIG_HOME", home)
	remote := newSourceAndRemote(t)
	dir := filepath.Join(home, "pool", "fixture", "T-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build); err != nil {
		t.Fatalf("Acquire into an empty dir: %v", err)
	}
	assertOwnClone(t, dir, remote, "jig/T-1")
	if asides, _ := filepath.Glob(dir + ".broken-*"); len(asides) != 0 {
		t.Fatalf("an empty lease dir was moved aside: %v", asides)
	}
}

// TestAcquireRelativeJIGHome covers a relative JIG_HOME: git runs the clone
// from the lease's parent directory, so a relative lease path would be
// resolved twice and every later git call would miss the lease.
func TestAcquireRelativeJIGHome(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("JIG_HOME", "jig-home")
	remote := newSourceAndRemote(t)

	lease, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire with a relative JIG_HOME: %v", err)
	}
	if !filepath.IsAbs(lease.Dir) {
		t.Fatalf("lease dir = %q, want an absolute path", lease.Dir)
	}
	want, err := filepath.Abs(filepath.Join("jig-home", "pool", "fixture", "T-1"))
	if err != nil {
		t.Fatal(err)
	}
	if lease.Dir != want {
		t.Fatalf("lease dir = %q, want %q", lease.Dir, want)
	}
	assertOwnClone(t, lease.Dir, remote, "jig/T-1")
}

// TestUsable pins the check in front of the gate's pre-Acquire reset: only a
// directory git opens as its own repository, from its own .git directory,
// with a commit checked out, qualifies.
func TestUsable(t *testing.T) {
	enclosing := newEnclosingRepo(t)
	if !Usable(enclosing) {
		t.Fatalf("a committed repo at its own top level must qualify")
	}

	cases := map[string]func(t *testing.T) string{
		"a .git directory git cannot open, inside a working copy": func(t *testing.T) string {
			dir := filepath.Join(enclosing, "pool", "repo", "T-1")
			writeFile(t, filepath.Join(dir, ".git", "objects", "pack", "pack-leftover.pack"), "x")
			return dir
		},
		"a .git directory git cannot open, inside a bare repository": func(t *testing.T) string {
			bare := filepath.Join(t.TempDir(), "home.git")
			run(t, filepath.Dir(bare), "clone", "--bare", enclosing, bare)
			dir := filepath.Join(bare, "pool", "repo", "T-1")
			writeFile(t, filepath.Join(dir, ".git", "objects", "pack", "pack-leftover.pack"), "x")
			return dir
		},
		"a .git file naming another repository": func(t *testing.T) string {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.Join(enclosing, ".git")+"\n")
			return dir
		},
		"an unborn HEAD": func(t *testing.T) string {
			dir := t.TempDir()
			run(t, dir, "init", "-b", "main")
			return dir
		},
		"an extension this git does not know": func(t *testing.T) string {
			dir := t.TempDir()
			run(t, dir, "init", "-b", "main")
			run(t, dir, "-c", "user.name=jig-fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "seed")
			run(t, dir, "config", "core.repositoryformatversion", "1")
			run(t, dir, "config", "extensions.jigTestUnknown", "true")
			return dir
		},
		"a plain directory": func(t *testing.T) string { return t.TempDir() },
		"a missing directory": func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "missing")
		},
	}
	for name, lay := range cases {
		t.Run(name, func(t *testing.T) {
			if Usable(lay(t)) {
				t.Fatalf("must not qualify")
			}
		})
	}
}

// TestAcquireNeverOverwritesAnAside covers the rename's one conflict: when
// the aside name is already taken, Acquire leaves both directories exactly
// as they are and says so, instead of replacing or merging either.
func TestAcquireNeverOverwritesAnAside(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JIG_HOME", home)
	remote := newSourceAndRemote(t)
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })

	dir := filepath.Join(home, "pool", "fixture", "T-1")
	writeFile(t, filepath.Join(dir, "stray.txt"), "broken lease\n")
	taken := dir + ".broken-20260102T030405Z"
	writeFile(t, filepath.Join(taken, "stray.txt"), "earlier aside\n")

	_, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err == nil || !strings.Contains(err.Error(), taken) {
		t.Fatalf("Acquire = %v, want an error naming the taken aside %s", err, taken)
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "stray.txt"):   "broken lease\n",
		filepath.Join(taken, "stray.txt"): "earlier aside\n",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q untouched", path, got, err, want)
		}
	}
}

// TestAcquireKeepsLeaseGitRefusesByOwner covers a healthy lease git refuses
// to work in because another user owns it (a JIG_HOME on exFAT or a network
// share, or one left behind by sudo), simulated with git's own
// GIT_TEST_ASSUME_DIFFERENT_OWNER. The lease is not broken, so Acquire must
// not move it aside and clone over its unpushed commit; it fails with git's
// own ownership error instead, as it did before leases were checked.
func TestAcquireKeepsLeaseGitRefusesByOwner(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	remote := newSourceAndRemote(t)
	lease, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	writeFile(t, filepath.Join(lease.Dir, "g.txt"), "unpushed work\n")
	run(t, lease.Dir, "add", "-A")
	commit(t, lease.Dir, "unpushed work")
	unpushed := run(t, lease.Dir, "rev-parse", "HEAD")

	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
	if _, err := gitx.Run(lease.Dir, "-c", "safe.directory=*", "rev-parse", "HEAD"); err != nil {
		t.Skipf("this git ignores safe.directory given with -c: %v", err)
	}
	_, err = Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err == nil || !strings.Contains(err.Error(), "pool: fetch") {
		t.Fatalf("Acquire over a lease git refuses by owner = %v, want the reuse fetch's ownership error", err)
	}
	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "0")

	if asides, _ := filepath.Glob(lease.Dir + ".broken-*"); len(asides) != 0 {
		t.Fatalf("a healthy lease git refuses by owner was moved aside: %v", asides)
	}
	if got := run(t, lease.Dir, "rev-parse", "HEAD"); got != unpushed {
		t.Fatalf("lease HEAD = %s, want the unpushed commit %s", got, unpushed)
	}
}

// TestAcquireReusesLeaseWithUnbornHEAD covers a lease git opens as its own
// repository whose HEAD does not resolve: a clone killed before its first
// checkout, or an orphan checkout left behind in a build lease whose ticket
// branch still holds unpushed commits. Neither is broken, so Acquire reuses
// it in place and its checkout repairs HEAD; moving it aside would drop the
// branch's commits from the run.
func TestAcquireReusesLeaseWithUnbornHEAD(t *testing.T) {
	t.Run("a clone killed before its first checkout", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("JIG_HOME", home)
		remote := newSourceAndRemote(t)
		dir := filepath.Join(home, "pool", "fixture", "T-1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run(t, dir, "init", "-b", "main")
		run(t, dir, "remote", "add", "origin", remote)

		if _, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		assertOwnClone(t, dir, remote, "jig/T-1")
		if asides, _ := filepath.Glob(dir + ".broken-*"); len(asides) != 0 {
			t.Fatalf("a lease with an unborn HEAD was moved aside: %v", asides)
		}
	})

	t.Run("an orphan checkout over unpushed work", func(t *testing.T) {
		t.Setenv("JIG_HOME", t.TempDir())
		remote := newSourceAndRemote(t)
		lease, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		writeFile(t, filepath.Join(lease.Dir, "g.txt"), "unpushed work\n")
		run(t, lease.Dir, "add", "-A")
		commit(t, lease.Dir, "unpushed work")
		unpushed := run(t, lease.Dir, "rev-parse", "HEAD")
		run(t, lease.Dir, "checkout", "--orphan", "scratch")

		if _, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build); err != nil {
			t.Fatalf("second Acquire: %v", err)
		}
		if asides, _ := filepath.Glob(lease.Dir + ".broken-*"); len(asides) != 0 {
			t.Fatalf("a lease with an unborn HEAD was moved aside: %v", asides)
		}
		if got := run(t, lease.Dir, "rev-parse", "HEAD"); got != unpushed {
			t.Fatalf("lease HEAD = %s, want the unpushed commit %s", got, unpushed)
		}
	})
}

// TestAcquireRefusesLeaseGitCannotOpen covers a lease whose .git has what
// git requires of a repository but that git still refuses (here an
// extension this git does not know, as a newer git can leave behind). The
// lease may hold unpushed work, so Acquire must neither move it aside nor
// clone over it: it fails with git's own error and leaves the lease as it
// was.
func TestAcquireRefusesLeaseGitCannotOpen(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	remote := newSourceAndRemote(t)
	lease, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	head := run(t, lease.Dir, "rev-parse", "HEAD")
	run(t, lease.Dir, "config", "core.repositoryformatversion", "1")
	run(t, lease.Dir, "config", "extensions.jigTestUnknown", "true")

	_, err = Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err == nil || !strings.Contains(err.Error(), "jigtestunknown") {
		t.Fatalf("Acquire over a lease git refuses = %v, want git's unknown-extension error", err)
	}
	if asides, _ := filepath.Glob(lease.Dir + ".broken-*"); len(asides) != 0 {
		t.Fatalf("a lease git refuses was moved aside: %v", asides)
	}
	if Usable(lease.Dir) {
		t.Fatalf("Usable = true for a lease git refuses")
	}
	run(t, lease.Dir, "config", "--file", filepath.Join(lease.Dir, ".git", "config"), "--unset", "extensions.jigTestUnknown")
	if got := run(t, lease.Dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("lease HEAD = %s, want %s untouched", got, head)
	}
}

// TestAcquireIgnoresInheritedGitDir covers a jig started with GIT_DIR set (a
// hook in a bare repository exports it, a user can export it): every git
// call Acquire makes must still act on the lease, never on the repository
// GIT_DIR names.
func TestAcquireIgnoresInheritedGitDir(t *testing.T) {
	enclosing := newEnclosingRepo(t)
	writeFile(t, filepath.Join(enclosing, "notes.txt"), "uncommitted work\n")
	t.Setenv("JIG_HOME", t.TempDir())
	remote := newSourceAndRemote(t)
	t.Setenv("GIT_DIR", filepath.Join(enclosing, ".git"))

	for i := 0; i < 2; i++ { // a fresh clone, then a reuse
		lease, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
		if err != nil {
			t.Fatalf("Acquire %d: %v", i+1, err)
		}
		assertOwnClone(t, lease.Dir, remote, "jig/T-1")
	}
	assertEnclosingUntouched(t, enclosing)
}
