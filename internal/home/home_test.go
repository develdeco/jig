package home

import (
	"path/filepath"
	"testing"
)

func TestRootHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JIG_HOME", dir)
	r, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if r != dir {
		t.Fatalf("Root() = %q, want %q", r, dir)
	}
}

// TestPathsDeriveFromTheRootGiven covers the home-anchored paths: they are
// derived from the root a caller passes, never from the environment.
func TestPathsDeriveFromTheRootGiven(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JIG_HOME", t.TempDir())
	if p := PoolDir(root); p != filepath.Join(root, "pool") {
		t.Fatalf("PoolDir(%q) = %q", root, p)
	}
	if m := MachinePath(root); m != filepath.Join(root, "projects.yaml") {
		t.Fatalf("MachinePath(%q) = %q", root, m)
	}
	if e := IntentExcerptDir(root); e != filepath.Join(root, "intent-excerpts") {
		t.Fatalf("IntentExcerptDir(%q) = %q", root, e)
	}
}
