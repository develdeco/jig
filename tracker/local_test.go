package tracker_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/store"
	"github.com/develdeco/jig/tracker"
)

// newTestStore creates a minimal store at t.TempDir() (just enough for
// store.Open, which only requires project.yaml to exist) and returns it
// along with the config tracker adapters need.
func newTestStore(t *testing.T, cfg project.Config) (*store.Store, project.Config) {
	t.Helper()
	t.Setenv("JIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st, cfg
}

func localCfg() project.Config {
	return project.Config{
		SchemaVersion: 1,
		Name:          "fixture",
		TicketFormat:  "JIG-{n}",
		Tracker:       "local",
	}
}

func TestLocalMint(t *testing.T) {
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	id1, err := a.Mint(tracker.Draft{Title: "First", Body: "First body"})
	if err != nil {
		t.Fatalf("Mint 1: %v", err)
	}
	if id1 != "JIG-1" {
		t.Fatalf("id1 = %q, want JIG-1", id1)
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir(id1), "tracker", "ticket.md")); err != nil {
		t.Fatalf("ticket.md not created for %s: %v", id1, err)
	}

	id2, err := a.Mint(tracker.Draft{Title: "Second", Body: "Second body"})
	if err != nil {
		t.Fatalf("Mint 2: %v", err)
	}
	if id2 != "JIG-2" {
		t.Fatalf("id2 = %q, want JIG-2", id2)
	}
	if _, err := os.Stat(st.TicketDir(id2)); err != nil {
		t.Fatalf("folder not created for %s: %v", id2, err)
	}
}

func TestLocalProjection(t *testing.T) {
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	id, err := a.Mint(tracker.Draft{Title: "Epic slice", Body: "Do the thing"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	proj := tracker.Projection{
		Description: "Consolidated description",
		Subtasks: []tracker.Subtask{
			{ID: "a", Title: "slice a", State: "green", BlockedBy: nil},
			{ID: "b", Title: "slice b", State: "queued", BlockedBy: []string{"a"}},
		},
	}
	if err := a.Project(id, proj); err != nil {
		t.Fatalf("Project: %v", err)
	}
	if err := a.Comment(id, "first comment"); err != nil {
		t.Fatalf("Comment 1: %v", err)
	}
	if err := a.Comment(id, "second comment"); err != nil {
		t.Fatalf("Comment 2: %v", err)
	}

	trackerDir := filepath.Join(st.TicketDir(id), "tracker")

	desc, err := os.ReadFile(filepath.Join(trackerDir, "ticket.md"))
	if err != nil {
		t.Fatalf("read ticket.md: %v", err)
	}
	if got := string(desc); got != "Consolidated description\n" {
		t.Fatalf("ticket.md = %q", got)
	}

	subData, err := os.ReadFile(filepath.Join(trackerDir, "subtasks.yaml"))
	if err != nil {
		t.Fatalf("read subtasks.yaml: %v", err)
	}
	var sf struct {
		Subtasks []tracker.Subtask `yaml:"subtasks"`
	}
	if err := yaml.Unmarshal(subData, &sf); err != nil {
		t.Fatalf("parse subtasks.yaml: %v", err)
	}
	if len(sf.Subtasks) != 2 {
		t.Fatalf("subtasks = %d, want 2", len(sf.Subtasks))
	}
	if sf.Subtasks[1].ID != "b" || len(sf.Subtasks[1].BlockedBy) != 1 || sf.Subtasks[1].BlockedBy[0] != "a" {
		t.Fatalf("subtasks[1] = %+v", sf.Subtasks[1])
	}

	c1, err := os.ReadFile(filepath.Join(trackerDir, "comments", "001.md"))
	if err != nil {
		t.Fatalf("read comments/001.md: %v", err)
	}
	if string(c1) != "first comment\n" {
		t.Fatalf("comments/001.md = %q", string(c1))
	}
	c2, err := os.ReadFile(filepath.Join(trackerDir, "comments", "002.md"))
	if err != nil {
		t.Fatalf("read comments/002.md: %v", err)
	}
	if string(c2) != "second comment\n" {
		t.Fatalf("comments/002.md = %q", string(c2))
	}

	// Re-projecting overwrites ticket.md/subtasks.yaml instead of
	// accumulating: idempotent full projection.
	proj2 := tracker.Projection{
		Description: "Updated description",
		Subtasks: []tracker.Subtask{
			{ID: "a", Title: "slice a", State: "green"},
		},
	}
	if err := a.Project(id, proj2); err != nil {
		t.Fatalf("Project 2: %v", err)
	}
	desc2, err := os.ReadFile(filepath.Join(trackerDir, "ticket.md"))
	if err != nil {
		t.Fatalf("read ticket.md after re-project: %v", err)
	}
	if string(desc2) != "Updated description\n" {
		t.Fatalf("ticket.md after re-project = %q", string(desc2))
	}
	subData2, err := os.ReadFile(filepath.Join(trackerDir, "subtasks.yaml"))
	if err != nil {
		t.Fatalf("read subtasks.yaml after re-project: %v", err)
	}
	var sf2 struct {
		Subtasks []tracker.Subtask `yaml:"subtasks"`
	}
	if err := yaml.Unmarshal(subData2, &sf2); err != nil {
		t.Fatalf("parse subtasks.yaml after re-project: %v", err)
	}
	if len(sf2.Subtasks) != 1 {
		t.Fatalf("subtasks after re-project = %d, want 1 (overwritten)", len(sf2.Subtasks))
	}

	// Comment files from before the re-project must survive untouched.
	if _, err := os.Stat(filepath.Join(trackerDir, "comments", "001.md")); err != nil {
		t.Fatalf("comments/001.md missing after re-project: %v", err)
	}
}
