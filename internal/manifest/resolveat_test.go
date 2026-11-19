package manifest

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// gitIn runs git in dir for a test.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

// TestResolveAtMatchesAChecksOut: the manifest read at a commit from git's
// objects is the manifest of a checkout of that commit, whatever the working
// tree of the repository it is read in holds, and a commit that declares
// nothing resolves to the default manifest.
func TestResolveAtMatchesAChecksOut(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-b", "main")
	gitIn(t, repo, "config", "user.name", "jig-test")
	gitIn(t, repo, "config", "user.email", "jig@example.invalid")

	writeFile(t, filepath.Join(repo, "a.txt"), "no manifest yet\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-m", "no manifest")
	bare := gitIn(t, repo, "rev-parse", "HEAD")

	writeFile(t, filepath.Join(repo, ".claude", "jig.yaml"), `
workspaces:
  - id: alpha
    path: alpha
oracles:
  test: go test ./...
invariants:
  - docs/
timeout: 90s
`)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-m", "declare the build surface")
	declared := gitIn(t, repo, "rev-parse", "HEAD")

	// The working tree and the index say something else: not committed, so
	// not the manifest at the commit.
	writeFile(t, filepath.Join(repo, ".claude", "jig.yaml"), "oracles:\n  lint: golangci-lint run\n")

	checkout := filepath.Join(t.TempDir(), "checkout")
	gitIn(t, filepath.Dir(checkout), "clone", repo, checkout)
	for name, rev := range map[string]string{"declared": declared, "undeclared": bare} {
		gitIn(t, checkout, "checkout", "--detach", rev)
		want, err := Resolve(checkout)
		if err != nil {
			t.Fatalf("%s: Resolve of the checkout: %v", name, err)
		}
		got, err := ResolveAt(repo, rev)
		if err != nil {
			t.Fatalf("%s: ResolveAt: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: ResolveAt = %+v, want the checkout's %+v", name, got, want)
		}
	}
	got, _ := ResolveAt(repo, declared)
	if got.Oracles["test"] != "go test ./..." || len(got.Workspaces) != 1 || got.Workspaces[0].ID != "alpha" || got.Timeout.Seconds() != 90 {
		t.Errorf("ResolveAt(declared) = %+v, want the committed declaration", got)
	}

	if _, err := ResolveAt(repo, "0000000000000000000000000000000000000000"); err == nil {
		t.Error("ResolveAt of a commit the repository lacks resolved without an error")
	}
}
