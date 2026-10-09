package migrate

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// newV1GitStore builds a non-bare, git-backed v1 store (no remote) with two
// tickets: T-1 has a chart entry, a blocked_by on T-2, and a committed
// tracker/ticket.md (publish never touched) that carries its title and
// description; T-2 has a tracker/github.yaml and no ticket.yaml of its own.
func newV1GitStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	if _, err := gitx.Run(root, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(
		"schema_version: 1\nname: demo\nticket_format: \"T-{n}\"\ntracker: local\nrepos: []\nplatform: platform/\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Root: root}

	if err := os.MkdirAll(filepath.Join(oldTicketDir(root, "T-1"), "tracker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldTicketDir(root, "T-1"), "tracker", "ticket.md"), []byte("# T-1 from ticket.md\n\nThe description.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldTicketDir(root, "T-1"), "tracker", "subtasks.yaml"), []byte("subtasks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(oldTicketDir(root, "T-1"), "tracker", "comments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldTicketDir(root, "T-1"), "tracker", "comments", "c1.md"), []byte("a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldTicketDir(root, "T-1"), "ticket.yaml"), []byte(
		"schema_version: 1\nblocked_by:\n  - ticket: T-2\n    kind: merged\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(oldTicketDir(root, "T-2"), "tracker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldTicketDir(root, "T-2"), "tracker", "github.yaml"), []byte("issue: 42\nnode_id: abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := "tickets:\n  - id: T-2\n    title: Chart title for T-2\n    blocked_by:\n      - ref: T-1\n"
	if err := os.WriteFile(filepath.Join(root, "charts", "demo", "tickets.yaml"), []byte(chartYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	runGit(t, root, "add", "-A")
	runGit(t, root, "-c", "user.name=tester", "-c", "user.email=tester@example.invalid", "commit", "-m", "init")
	return st
}

func TestApplyMigratesAStoreEndToEnd(t *testing.T) {
	t.Parallel()
	st := newV1GitStore(t)
	m := Map{
		Keys:    map[string]string{"STORE": "the store", "GRAPH": "the graph"},
		Tickets: map[string]string{"T-1": "STORE", "T-2": "GRAPH"},
	}
	plan, err := BuildPlan(st, m)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := Apply(st, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// T-1 -> STORE-1: title/description resolved from tracker/ticket.md,
	// tracker/ticket.md, subtasks.yaml and comments/ gone, aliases carries
	// T-1, blocked_by now names GRAPH-1.
	if _, err := os.Stat(oldTicketDir(st.Root, "T-1")); !os.IsNotExist(err) {
		t.Errorf("old folder T-1 still exists: %v", err)
	}
	rec1, err := st.ReadTicket("STORE-1")
	if err != nil {
		t.Fatalf("ReadTicket(STORE-1): %v", err)
	}
	if rec1.Title != "T-1 from ticket.md" || rec1.Body != "The description." {
		t.Errorf("STORE-1 title/body = %q/%q, want the ticket.md fallback", rec1.Title, rec1.Body)
	}
	if len(rec1.Aliases) != 1 || rec1.Aliases[0] != "T-1" {
		t.Errorf("STORE-1 aliases = %v, want [T-1]", rec1.Aliases)
	}
	if len(rec1.BlockedBy) != 1 || rec1.BlockedBy[0].Ticket != "GRAPH-1" {
		t.Errorf("STORE-1 blocked_by = %+v, want GRAPH-1", rec1.BlockedBy)
	}
	for _, p := range []string{"tracker/ticket.md", "tracker/subtasks.yaml", "tracker/comments/c1.md"} {
		if _, err := os.Stat(filepath.Join(st.TicketDir("STORE-1"), p)); !os.IsNotExist(err) {
			t.Errorf("STORE-1/%s should be gone, stat err = %v", p, err)
		}
	}

	// T-2 -> GRAPH-1: tracker/github.yaml kept, aliases carries T-2.
	if _, err := os.Stat(filepath.Join(st.TicketDir("GRAPH-1"), "tracker", "github.yaml")); err != nil {
		t.Errorf("GRAPH-1/tracker/github.yaml missing: %v", err)
	}
	rec2, err := st.ReadTicket("GRAPH-1")
	if err != nil {
		t.Fatalf("ReadTicket(GRAPH-1): %v", err)
	}
	if len(rec2.Aliases) != 1 || rec2.Aliases[0] != "T-2" {
		t.Errorf("GRAPH-1 aliases = %v, want [T-2]", rec2.Aliases)
	}

	// The chart's entry id and blocked_by ref both moved.
	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "GRAPH-1" {
		t.Fatalf("chart entries = %+v, want id GRAPH-1", entries)
	}
	if len(entries[0].BlockedBy) != 1 || entries[0].BlockedBy[0].Ref != "STORE-1" {
		t.Fatalf("chart blocked_by = %+v, want ref STORE-1", entries[0].BlockedBy)
	}

	// project.yaml is rewritten onto schema 2.
	data, err := os.ReadFile(filepath.Join(st.Root, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseProjectYAMLForTest(data)
	if err != nil {
		t.Fatalf("parse migrated project.yaml: %v\n%s", err, data)
	}
	if cfg.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want 2", cfg.SchemaVersion)
	}
	if cfg.Keys["STORE"] == "" || cfg.Keys["GRAPH"] == "" {
		t.Errorf("Keys = %+v, want STORE and GRAPH", cfg.Keys)
	}

	// Everything landed in one commit, nothing left dirty.
	status := runGit(t, st.Root, "status", "--porcelain")
	if status != "" {
		t.Errorf("git status --porcelain = %q, want a clean tree after the migration's commit", status)
	}
	log := runGit(t, st.Root, "log", "--oneline", "-1")
	if log == "" {
		t.Errorf("no commit landed")
	}
}

// parseProjectYAMLForTest decodes just enough of a project.yaml for this
// test's assertions.
func parseProjectYAMLForTest(data []byte) (struct {
	SchemaVersion int               `yaml:"schema_version"`
	Keys          map[string]string `yaml:"keys"`
}, error) {
	var out struct {
		SchemaVersion int               `yaml:"schema_version"`
		Keys          map[string]string `yaml:"keys"`
	}
	err := yaml.Unmarshal(data, &out)
	return out, err
}
