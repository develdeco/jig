package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// tinyRepo creates a minimal git repo committed on main, with a bare
// remote, for tests that do not need the full fixture.
func tinyRepo(t *testing.T) (repoDir, remoteDir string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	run(t, dir, "config", "user.name", "jig-fixture")
	run(t, dir, "config", "user.email", "fixture@example.invalid")
	writeAndCommit(t, dir, "f.txt", "one", "init")

	remote := filepath.Join(t.TempDir(), "remote.git")
	run(t, dir, "clone", "--bare", dir, remote)
	run(t, dir, "remote", "add", "origin", remote)
	run(t, dir, "push", "origin", "main")
	return dir, remote
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v (in %s): %v", args, dir, err)
	}
	return out
}

func writeAndCommit(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", msg)
	return run(t, dir, "rev-parse", "HEAD")
}

// cloneFrom clones remote into a fresh temp dir, returning its path.
func cloneFrom(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	run(t, filepath.Dir(dir), "clone", remote, dir)
	run(t, dir, "config", "user.name", "jig-fixture")
	run(t, dir, "config", "user.email", "fixture@example.invalid")
	return dir
}
