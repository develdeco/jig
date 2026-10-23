package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// projGitRepo makes dir a git repository on main with one commit, the way a
// person's product repo is when they run `jig init` in it, and returns dir.
func projGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	gitLog(t, dir, "init", "-b", "main")
	writeFileOrFatal(t, filepath.Join(dir, "README.md"), []byte("# "+filepath.Base(dir)+"\n"))
	gitLog(t, dir, "add", "-A")
	commitPinned(t, dir, "init")
	return dir
}

// projBareRemote makes an empty bare repository called name under a fresh
// directory and returns its path, written with forward slashes: what a
// remote that costs nothing is in a test, a path git can push to.
func projBareRemote(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	gitLog(t, filepath.Dir(dir), "init", "--bare", "-b", "main", dir)
	return filepath.ToSlash(dir)
}

// projFileURL is the file:// URL of the directory p.
func projFileURL(p string) string {
	slash := filepath.ToSlash(p)
	if strings.HasPrefix(slash, "/") {
		return "file://" + slash
	}
	return "file:///" + slash
}

// projMarker reads the jig.yaml in dir.
func projMarker(t *testing.T, dir string) project.Marker {
	t.Helper()
	m, err := project.LoadMarker(filepath.Join(dir, project.MarkerFile))
	if err != nil {
		t.Fatalf("LoadMarker in %s: %v", dir, err)
	}
	return m
}

// projRun runs jig and fails the test unless it exits 0.
func projRun(t *testing.T, env jigEnv, cwd string, args ...string) jigResult {
	t.Helper()
	r := runJig(t, env, cwd, args...)
	if r.Code != 0 {
		t.Fatalf("jig %s (in %s): exit %d, want 0\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), cwd, r.Code, r.Stdout, r.Stderr)
	}
	return r
}

// projRefusal fails the test unless r is a refusal with exit code code whose
// stdout holds every want.
func projRefusal(t *testing.T, what string, r jigResult, code int, want ...string) {
	t.Helper()
	if r.Code != code {
		t.Errorf("%s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", what, r.Code, code, r.Stdout, r.Stderr)
	}
	for _, w := range want {
		if !strings.Contains(r.Stdout, w) {
			t.Errorf("%s: stdout lacks %q:\n%s", what, w, r.Stdout)
		}
	}
}

// projEnv is the environment a project test runs jig in: a jig home and
// a user home of its own.
func projEnv(t *testing.T) jigEnv {
	t.Helper()
	return jigEnv{home: t.TempDir(), userHome: t.TempDir()}
}
