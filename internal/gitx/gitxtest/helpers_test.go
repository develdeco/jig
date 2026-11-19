package gitxtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
)

func TestMain(m *testing.M) {
	os.Exit(gittest.Run(m))
}

// mustRun runs the program in dir and returns its output.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("%s: %v", strings.Join(args, " "), err)
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

// newStoreRepo is a repository in a fresh directory with one commit, as a
// store has.
func newStoreRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRun(t, dir, "init", "-q", "-b", "main")
	mustRun(t, dir, "config", "user.name", "tester")
	mustRun(t, dir, "config", "user.email", "tester@example.invalid")
	writeFileT(t, dir, ".gitattributes", gitx.StoreAttributes)
	writeFileT(t, dir, ".gitignore", "*.lock\n")
	writeFileT(t, dir, "project.yaml", "schema_version: 1\n")
	mustRun(t, dir, "add", "-A")
	mustRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}
