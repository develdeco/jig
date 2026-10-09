package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

func TestCheckSchemaVersion(t *testing.T) {
	t.Parallel()
	if err := CheckSchemaVersion(project.Config{SchemaVersion: 1}); err != nil {
		t.Errorf("schema 1: got %v, want nil", err)
	}
	if err := CheckSchemaVersion(project.Config{SchemaVersion: 2}); err == nil {
		t.Errorf("schema 2: got nil, want a refusal")
	}
}

func TestCheckClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Root: root}
	runGit(t, root, "add", "-A")
	runGit(t, root, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-m", "init")

	if err := CheckClean(st); err != nil {
		t.Errorf("clean store: got %v, want nil", err)
	}

	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\nextra: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckClean(st); err == nil {
		t.Errorf("dirty store: got nil, want a refusal")
	}
}

// newLevelTestStore creates a bare remote and a clone pushed level with it.
func newLevelTestStore(t *testing.T) (st *store.Store, work string) {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work = filepath.Join(dir, "work")
	runGit(t, "", "init", "--bare", "-b", "main", remote)
	runGit(t, "", "clone", remote, work)
	runGit(t, work, "config", "user.name", "t")
	runGit(t, work, "config", "user.email", "t@example.invalid")
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "push", "origin", "main")
	return &store.Store{Root: work}, work
}

func TestCheckLevelWithOrigin(t *testing.T) {
	t.Parallel()
	st, work := newLevelTestStore(t)
	if err := CheckLevelWithOrigin(st); err != nil {
		t.Errorf("level store: got %v, want nil", err)
	}

	// Ahead of origin: an uncommitted push.
	if err := os.WriteFile(filepath.Join(work, "ledger.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "ahead")
	if err := CheckLevelWithOrigin(st); err == nil {
		t.Errorf("ahead of origin: got nil, want a refusal")
	}
}

func TestCheckLevelWithOriginPassesStandalone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-m", "init")

	st := &store.Store{Root: root}
	if err := CheckLevelWithOrigin(st); err != nil {
		t.Errorf("standalone store (no origin): got %v, want nil", err)
	}
}

func TestCheckNoLeases(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	if err := CheckNoLeases(jigHome, []string{"demo-repo"}, []string{"T-1", "T-2"}); err != nil {
		t.Errorf("no leases: got %v, want nil", err)
	}

	dir, err := pool.Dir(jigHome, "demo-repo", "T-1", pool.Gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckNoLeases(jigHome, []string{"demo-repo"}, []string{"T-1", "T-2"}); err == nil {
		t.Errorf("a gate lease for T-1 exists: got nil, want a refusal")
	}
}

func TestBranchWarningsNamesABranchStillOnOrigin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")
	runGit(t, "", "init", "--bare", "-b", "main", remote)
	runGit(t, "", "clone", remote, work)
	runGit(t, work, "config", "user.name", "t")
	runGit(t, work, "config", "user.email", "t@example.invalid")
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "push", "origin", "main")
	runGit(t, work, "checkout", "-b", "jig/T-1")
	runGit(t, work, "push", "origin", "jig/T-1")

	renames := []Rename{{OldID: "T-1", NewID: "STORE-1"}, {OldID: "T-2", NewID: "STORE-2"}}
	warnings := BranchWarnings(work, []project.Repo{{Remote: remote}}, renames)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one (for T-1's branch)", warnings)
	}
	if !containsAll(warnings[0], "T-1", "jig/T-1", "STORE-1") {
		t.Errorf("warning = %q, want it to name T-1, jig/T-1 and STORE-1", warnings[0])
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
