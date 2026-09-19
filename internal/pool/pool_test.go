package pool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// newSourceAndRemote creates a tiny source repo committed on main, then a
// bare clone of it to act as the fixture remote. Returns the remote path.
func newSourceAndRemote(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	run(t, src, "init", "-b", "main")
	run(t, src, "config", "user.email", "fixture@example.invalid")
	run(t, src, "config", "user.name", "jig-fixture")
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("one"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run(t, src, "add", "-A")
	run(t, src, "commit", "-m", "init")

	remote := t.TempDir()
	remote = filepath.Join(remote, "remote.git")
	run(t, filepath.Dir(remote), "clone", "--bare", src, remote)
	return remote
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// commit runs "git commit" in dir with an explicit local identity, so the
// test passes on a machine with no global git config (every CI runner):
// unlike newSourceAndRemote's source repo, a lease dir is a fresh clone and
// never inherits the source's local .git/config.
func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	return run(t, dir, "-c", "user.name=jig-fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", msg)
}

// writeFile writes content at path, creating its parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireCloneAndResume(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	remote := newSourceAndRemote(t)

	lease1, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lease1.Branch != "jig/T-1" {
		t.Fatalf("Branch = %q, want jig/T-1", lease1.Branch)
	}
	if _, err := os.Stat(lease1.Dir); err != nil {
		t.Fatalf("lease dir missing: %v", err)
	}
	originMain := run(t, lease1.Dir, "rev-parse", "origin/main")
	head := run(t, lease1.Dir, "rev-parse", "HEAD")
	if head != originMain {
		t.Fatalf("fresh lease HEAD = %s, want origin/main %s", head, originMain)
	}

	// Commit into the lease, then Return and re-Acquire the same key: the
	// resume case should keep the lease's local branch, not reset it.
	if err := os.WriteFile(filepath.Join(lease1.Dir, "g.txt"), []byte("two"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run(t, lease1.Dir, "add", "-A")
	commit(t, lease1.Dir, "work")
	resumeSHA := run(t, lease1.Dir, "rev-parse", "HEAD")

	if err := lease1.Return(); err != nil {
		t.Fatalf("Return: %v", err)
	}

	lease2, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if lease2.Dir != lease1.Dir {
		t.Fatalf("second Acquire dir = %q, want reuse of %q", lease2.Dir, lease1.Dir)
	}
	head2 := run(t, lease2.Dir, "rev-parse", "HEAD")
	if head2 != resumeSHA {
		t.Fatalf("resumed lease HEAD = %s, want kept commit %s", head2, resumeSHA)
	}

	// A gate-key Acquire for the same ticket gets a separate directory,
	// checked out on the same branch name.
	lease3, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Gate)
	if err != nil {
		t.Fatalf("gate Acquire: %v", err)
	}
	if lease3.Dir == lease1.Dir {
		t.Fatalf("gate lease dir should differ from build lease dir: %q", lease3.Dir)
	}
	if lease3.Branch != "jig/T-1" {
		t.Fatalf("gate lease branch = %q, want jig/T-1", lease3.Branch)
	}
	if filepath.Dir(lease3.Dir) != filepath.Dir(lease1.Dir) {
		t.Fatalf("gate lease should live under the same repo dir: %q vs %q", lease3.Dir, lease1.Dir)
	}
}

// TestAcquireNeverResetsExistingLocalBranch covers the case
// TestAcquireCloneAndResume's resume step cannot: once the branch has also
// reached origin (a prior push), the old "checkout -B branch origin/branch"
// path re-pointed the local branch to the remote tip on every later
// Acquire, discarding any commit landed locally since. An existing local
// branch must always be kept as-is.
func TestAcquireNeverResetsExistingLocalBranch(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	remote := newSourceAndRemote(t)

	lease1, err := Acquire("fixture2", remote, "main", "jig/T-2", "T-2", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := os.WriteFile(filepath.Join(lease1.Dir, "h.txt"), []byte("pushed work"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run(t, lease1.Dir, "add", "-A")
	commit(t, lease1.Dir, "pushed work")
	run(t, lease1.Dir, "push", "origin", "jig/T-2")

	// A second slice in the same run commits again without pushing: origin
	// now lags one commit behind the local branch.
	if err := os.WriteFile(filepath.Join(lease1.Dir, "i.txt"), []byte("unpushed work"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run(t, lease1.Dir, "add", "-A")
	commit(t, lease1.Dir, "unpushed work")
	localSHA := run(t, lease1.Dir, "rev-parse", "HEAD")

	lease2, err := Acquire("fixture2", remote, "main", "jig/T-2", "T-2", Build)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if lease2.Dir != lease1.Dir {
		t.Fatalf("second Acquire dir = %q, want reuse of %q", lease2.Dir, lease1.Dir)
	}
	head := run(t, lease2.Dir, "rev-parse", "HEAD")
	if head != localSHA {
		t.Fatalf("re-acquired branch HEAD = %s, want the local commit %s still on the branch tip (must not reset to origin)", head, localSHA)
	}
}
