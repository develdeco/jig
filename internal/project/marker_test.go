package project

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// samePath compares two directory paths the way the platform's file system
// does, so a temp dir reached through a symlinked or differently cased
// spelling still matches.
func samePath(t *testing.T, got, want string) {
	t.Helper()
	g, gerr := filepath.EvalSymlinks(got)
	w, werr := filepath.EvalSymlinks(want)
	if gerr != nil || werr != nil {
		t.Fatalf("resolve %q / %q: %v / %v", got, want, gerr, werr)
	}
	if !strings.EqualFold(g, w) {
		t.Fatalf("dir = %q, want %q", got, want)
	}
}

// TestFindMarkerWalksUp: the nearest jig.yaml above (or at) the start
// directory wins - an orchestrator's marker from anywhere under it, a member
// repo's own marker from inside the member, and none from outside any project.
func TestFindMarkerWalksUp(t *testing.T) {
	base := t.TempDir()
	orch := filepath.Join(base, "orch")
	member := filepath.Join(orch, "services", "api")
	deep := filepath.Join(member, "internal", "deep")
	loose := filepath.Join(orch, "docs")
	for _, d := range []string{deep, loose} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteMarker(orch, Marker{Jig: "1", Project: "demo", Store: "git@example.invalid:org/demo-tickets.git", Kind: KindOrchestrator}); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(member, Marker{Project: "demo", Kind: KindMember}); err != nil {
		t.Fatal(err)
	}

	t.Run("nested: a directory under the orchestrator finds the orchestrator", func(t *testing.T) {
		dir, m, ok, err := FindMarker(loose)
		if err != nil || !ok {
			t.Fatalf("FindMarker = %q, %+v, %v, %v; want the orchestrator's marker", dir, m, ok, err)
		}
		samePath(t, dir, orch)
		if m.Kind != KindOrchestrator || m.Project != "demo" || m.Store == "" {
			t.Errorf("marker = %+v, want the orchestrator's", m)
		}
	})

	t.Run("member: the member's own marker is found first", func(t *testing.T) {
		for _, start := range []string{member, deep} {
			dir, m, ok, err := FindMarker(start)
			if err != nil || !ok {
				t.Fatalf("FindMarker(%s) = %q, %+v, %v, %v; want the member's marker", start, dir, m, ok, err)
			}
			samePath(t, dir, member)
			if m.Kind != KindMember {
				t.Errorf("FindMarker(%s) kind = %q, want %q", start, m.Kind, KindMember)
			}
		}
	})

	t.Run("a file as the start is judged by its directory", func(t *testing.T) {
		file := filepath.Join(deep, "x.go")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		dir, _, ok, err := FindMarker(file)
		if err != nil || !ok {
			t.Fatalf("FindMarker(file) = %q, %v, %v", dir, ok, err)
		}
		samePath(t, dir, member)
	})

	t.Run("none: outside any project", func(t *testing.T) {
		dir, m, ok, err := FindMarker(base)
		if err != nil || ok {
			t.Fatalf("FindMarker(%s) = %q, %+v, %v, %v; want no marker", base, dir, m, ok, err)
		}
		if dir != "" || !reflect.DeepEqual(m, Marker{}) {
			t.Errorf("FindMarker with none = %q, %+v; want the zero values", dir, m)
		}
	})
}

// TestFindMarkerIgnoresTheManifest: <repo>/.claude/jig.yaml is the per-repo
// manifest, a different file that happens to share the name; a walk that
// starts in .claude must not take it for a marker.
func TestFindMarkerIgnoresTheManifest(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".claude", MarkerFile), "workspaces:\n  - name: api\n")

	if dir, _, ok, err := FindMarker(filepath.Join(repo, ".claude")); err != nil || ok {
		t.Fatalf("FindMarker from .claude = %q, %v, %v; want none, the manifest is not a marker", dir, ok, err)
	}
	if err := WriteMarker(repo, Marker{Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	dir, m, ok, err := FindMarker(filepath.Join(repo, ".claude"))
	if err != nil || !ok || m.Project != "demo" {
		t.Fatalf("FindMarker from .claude with a marker at the root = %q, %+v, %v, %v", dir, m, ok, err)
	}
	samePath(t, dir, repo)
}

// TestMarkerRoundTripAndLenientRead: every field survives a write and a read,
// a key this jig does not know is ignored, a marker that is not YAML is an
// error, and the file is replaced whole.
func TestMarkerRoundTripAndLenientRead(t *testing.T) {
	dir := t.TempDir()
	in := Marker{
		Jig:          "1",
		Project:      "demo",
		Store:        "git@example.invalid:org/demo-tickets.git",
		Kind:         KindProject,
		Expects:      &Expects{Harness: []string{"claude", "codex"}, Hooks: true},
		Repos:        []MarkerRepo{{Name: "api", Path: "services/api", Remote: "git@example.invalid:org/api.git", Target: "develop"}},
		Branches:     &BranchRule{From: "github"},
		Budget:       &Budget{Tokens: 5_000_000, USD: 12.5},
		Distribution: "shared",
		Unattended:   true,
	}
	mdir := filepath.Join(dir, "new", "dir")
	if err := WriteMarker(mdir, in); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	path := filepath.Join(mdir, MarkerFile)
	out, err := LoadMarker(path)
	if err != nil {
		t.Fatalf("LoadMarker: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip: got %+v, want %+v", out, in)
	}
	raw, _ := os.ReadFile(path)
	for _, key := range []string{"jig:", "project:", "store:", "kind:", "expects:", "repos:", "branches:", "budget:", "distribution:", "unattended:"} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("marker file lacks the key %q:\n%s", key, raw)
		}
	}

	writeFile(t, path, "project: demo\nfuture_key: whatever\nbudget:\n  tokens: 7\n  currency: eur\n")
	lenient, err := LoadMarker(path)
	if err != nil || lenient.Project != "demo" || lenient.Budget == nil || lenient.Budget.Tokens != 7 {
		t.Fatalf("LoadMarker with unknown keys = %+v, %v; want the known ones read", lenient, err)
	}

	writeFile(t, path, "jig: 1\nproject: demo\n")
	if m, err := LoadMarker(path); err != nil || m.Jig != "1" {
		t.Fatalf("LoadMarker with a numeric jig: = %+v, %v; want \"1\"", m, err)
	}

	writeFile(t, path, "")
	if m, err := LoadMarker(path); err != nil || !reflect.DeepEqual(m, Marker{}) {
		t.Fatalf("LoadMarker of an empty file = %+v, %v; want the zero Marker", m, err)
	}

	writeFile(t, path, "project: [unclosed\n")
	if _, err := LoadMarker(path); err == nil {
		t.Error("LoadMarker of a file that is not YAML = nil error, want one")
	}
	if _, _, _, err := FindMarker(mdir); err == nil {
		t.Error("FindMarker over a broken marker = nil error, want one")
	}

	if err := WriteMarker(mdir, Marker{Project: "other"}); err != nil {
		t.Fatal(err)
	}
	replaced, _ := LoadMarker(path)
	if !reflect.DeepEqual(replaced, Marker{Project: "other"}) {
		t.Errorf("after WriteMarker over it the marker = %+v, want only project: other", replaced)
	}
	if entries, _ := os.ReadDir(mdir); len(entries) != 1 {
		t.Errorf("marker directory holds %d entries after the writes, want only jig.yaml (no lock or temp file left in a person's repo)", len(entries))
	}
}
