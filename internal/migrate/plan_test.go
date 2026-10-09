package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// newV1Store builds a bare, non-git store (no remote, no commits) with a v1
// project.yaml and the ticket folders named, enough for BuildPlan, which
// reads only the filesystem and (best-effort) git history.
func newV1Store(t *testing.T, tickets ...string) *store.Store {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\nname: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Root: root}
	for _, id := range tickets {
		if err := os.MkdirAll(oldTicketDir(root, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func wantValidationError(t *testing.T, err error) {
	t.Helper()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("err = %v, want *axi.Error VALIDATION_ERROR", err)
	}
}

func TestBuildPlanRefusesTicketLeftOut(t *testing.T) {
	t.Parallel()
	st := newV1Store(t, "T-1", "T-2")
	m := Map{Keys: map[string]string{"STORE": "the store"}, Tickets: map[string]string{"T-1": "STORE"}}

	_, err := BuildPlan(st, m)
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), "T-2") {
		t.Fatalf("err = %v, want it to name T-2", err)
	}
}

func TestBuildPlanRefusesTicketTheStoreLacks(t *testing.T) {
	t.Parallel()
	st := newV1Store(t, "T-1")
	m := Map{Keys: map[string]string{"STORE": "the store"}, Tickets: map[string]string{"T-1": "STORE", "T-9": "STORE"}}

	_, err := BuildPlan(st, m)
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), "T-9") {
		t.Fatalf("err = %v, want it to name T-9", err)
	}
}

func TestBuildPlanRefusesUndeclaredKey(t *testing.T) {
	t.Parallel()
	st := newV1Store(t, "T-1", "T-2")
	m := Map{Keys: map[string]string{"STORE": "the store"}, Tickets: map[string]string{"T-1": "STORE", "T-2": "GRAPH"}}

	_, err := BuildPlan(st, m)
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), "T-2") || !strings.Contains(err.Error(), "GRAPH") {
		t.Fatalf("err = %v, want it to name T-2 and GRAPH", err)
	}
}

func TestBuildPlanRefusesAMapThatDeclaresTheOldIDsKey(t *testing.T) {
	t.Parallel()
	st := newV1Store(t, "T-1", "T-2")
	m := Map{Keys: map[string]string{"T": "clashes with the old ids"}, Tickets: map[string]string{"T-1": "T", "T-2": "T"}}

	_, err := BuildPlan(st, m)
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), "T") {
		t.Fatalf("err = %v, want it to name the clashing key", err)
	}
}

// TestBuildPlanNumbersInOldIDOrderPerKey pins brief.md#The rename map: each
// key's tickets are numbered in old-id order, by the old id's own number,
// not by string or map iteration order.
func TestBuildPlanNumbersInOldIDOrderPerKey(t *testing.T) {
	t.Parallel()
	st := newV1Store(t, "T-10", "T-2", "T-7")
	m := Map{
		Keys: map[string]string{"STORE": "the store", "GRAPH": "the graph"},
		Tickets: map[string]string{
			"T-10": "STORE",
			"T-2":  "STORE",
			"T-7":  "GRAPH",
		},
	}

	plan, err := BuildPlan(st, m)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	want := map[string]string{"T-2": "STORE-1", "T-10": "STORE-2", "T-7": "GRAPH-1"}
	got := map[string]string{}
	for _, r := range plan.Renames {
		got[r.OldID] = r.NewID
	}
	for old, newID := range want {
		if got[old] != newID {
			t.Errorf("rename of %s = %s, want %s (got map %+v)", old, got[old], newID, got)
		}
	}
}

// TestBuildPlanFilesMovesDeletesAndRewrites pins the file-op plan for one
// renamed ticket: tracker/github.yaml moves, tracker/ticket.md and
// tracker/comments/ delete, ticket.yaml rewrites at its new path, and
// project.yaml always rewrites.
func TestBuildPlanFilesMovesDeletesAndRewrites(t *testing.T) {
	t.Parallel()
	st := newV1Store(t, "T-1")
	write := func(rel, content string) {
		p := filepath.Join(oldTicketDir(st.Root, "T-1"), rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ticket.yaml", "schema_version: 1\ntitle: hi\n")
	write("brief.md", "# Brief\n")
	write("tracker/github.yaml", "issue: 1\n")
	write("tracker/ticket.md", "# Old title\n")
	write("tracker/subtasks.yaml", "subtasks: []\n")
	write("tracker/comments/c1.md", "a comment\n")

	m := Map{Keys: map[string]string{"STORE": "the store"}, Tickets: map[string]string{"T-1": "STORE"}}
	plan, err := BuildPlan(st, m)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	byPath := map[string]FileOp{}
	for _, f := range plan.Files {
		byPath[f.Path] = f
	}
	if op, ok := byPath["tickets/STORE-1/ticket.yaml"]; !ok || op.Action != "rewrite" {
		t.Errorf("ticket.yaml op = %+v, want a rewrite at its new path", op)
	}
	if op, ok := byPath["tickets/STORE-1/brief.md"]; !ok || op.Action != "move" || op.From != "T-1/brief.md" {
		t.Errorf("brief.md op = %+v, want a move from T-1/brief.md", op)
	}
	if op, ok := byPath["tickets/STORE-1/tracker/github.yaml"]; !ok || op.Action != "move" {
		t.Errorf("tracker/github.yaml op = %+v, want a move", op)
	}
	if op, ok := byPath["T-1/tracker/ticket.md"]; !ok || op.Action != "delete" {
		t.Errorf("tracker/ticket.md op = %+v, want a delete at its old path", op)
	}
	if op, ok := byPath["T-1/tracker/subtasks.yaml"]; !ok || op.Action != "delete" {
		t.Errorf("tracker/subtasks.yaml op = %+v, want a delete", op)
	}
	if op, ok := byPath["T-1/tracker/comments/c1.md"]; !ok || op.Action != "delete" {
		t.Errorf("tracker/comments/c1.md op = %+v, want a delete", op)
	}
	if op, ok := byPath["project.yaml"]; !ok || op.Action != "rewrite" {
		t.Errorf("project.yaml op = %+v, want a rewrite", op)
	}
}
