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
	p, err := PoolDir()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "pool") {
		t.Fatalf("PoolDir() = %q", p)
	}
	m, err := MachinePath()
	if err != nil {
		t.Fatal(err)
	}
	if m != filepath.Join(dir, "projects.yaml") {
		t.Fatalf("MachinePath() = %q", m)
	}
}
