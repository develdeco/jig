package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// migrateFixture is a v1 store, with an origin, carrying two tickets (T-1
// with a chart entry, a blocked_by on T-2 and a tracker/github.yaml record;
// T-2 plain) - the seam brief.md#Seams names: "a v1 test store that has a
// chart, a published ticket and a tracker/github.yaml".
type migrateFixture struct {
	work, remote, repoRemote string
}

func newMigrateFixture(t *testing.T) migrateFixture {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "store-remote.git")
	work := filepath.Join(dir, "store-work")
	run := func(d string, args ...string) string {
		t.Helper()
		out, err := gitx.Run(d, args...)
		if err != nil {
			t.Fatalf("git %v (in %s): %v", args, d, err)
		}
		return out
	}
	run("", "init", "--bare", "-b", "main", remote)
	run("", "clone", remote, work)
	run(work, "config", "user.name", "tester")
	run(work, "config", "user.email", "tester@example.invalid")

	repoRemote := filepath.Join(dir, "product-repo.git")
	run("", "init", "--bare", "-b", "main", repoRemote)

	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte(
		"schema_version: 1\nname: demo\nticket_format: \"T-{n}\"\ntracker: local\nrepos:\n  - remote: "+filepath.ToSlash(repoRemote)+"\nplatform: platform/\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitattributes"), []byte(gitx.StoreAttributes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("*.lock\n.*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "T-1", "ticket.yaml"), []byte(
		"schema_version: 1\ntitle: First ticket\nblocked_by:\n  - ticket: T-2\n    kind: merged\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "T-1", "brief.md"), []byte("# T-1\n\n## Context\n\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "T-2", "tracker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "T-2", "tracker", "github.yaml"), []byte("issue: 7\nnode_id: xyz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "T-2", "brief.md"), []byte("# T-2\n\n## Context\n\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := "tickets:\n  - id: T-1\n    title: Chart title for T-1\n  - id: T-2\n    title: Chart title for T-2\n    blocked_by:\n      - ref: T-1\n"
	if err := os.WriteFile(filepath.Join(work, "charts", "demo", "tickets.yaml"), []byte(chartYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	run(work, "add", "-A")
	run(work, "commit", "-m", "init")
	run(work, "push", "origin", "main")
	return migrateFixture{work: work, remote: remote, repoRemote: repoRemote}
}

func writeMigrateMap(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "map.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const migrateMapYAML = `
keys:
  STORE: everything in the store
  GRAPH: everything in the graph
tickets:
  T-1: GRAPH
  T-2: STORE
`

func TestStoreMigrateDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	fx := newMigrateFixture(t)
	mapPath := writeMigrateMap(t, migrateMapYAML)
	jigHome := t.TempDir()

	out, code := runMain(t, testEnv(jigHome), "", "store", "migrate", "--map", mapPath, "--dry-run", "--store", fx.work)
	if code != 0 {
		t.Fatalf("dry run: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "T-1,GRAPH-1,Chart title for T-1") && !strings.Contains(out, "T-1,GRAPH-1,First ticket") {
		t.Errorf("dry run output does not show T-1's rename:\n%s", out)
	}
	if !strings.Contains(out, "T-2,STORE-1") {
		t.Errorf("dry run output does not show T-2's rename:\n%s", out)
	}
	if !strings.Contains(out, "project.yaml") {
		t.Errorf("dry run output does not list project.yaml:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(fx.work, "T-1")); err != nil {
		t.Errorf("dry run moved T-1 off the store root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(fx.work, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "schema_version: 1") {
		t.Errorf("dry run rewrote project.yaml:\n%s", data)
	}
}

func TestStoreMigrateApplyEndToEnd(t *testing.T) {
	t.Parallel()
	fx := newMigrateFixture(t)
	mapPath := writeMigrateMap(t, migrateMapYAML)
	jigHome := t.TempDir()
	env := testEnv(jigHome)

	out, code := runMain(t, env, "", "store", "migrate", "--map", mapPath, "--store", fx.work)
	if code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "GRAPH-1") || !strings.Contains(out, "STORE-1") {
		t.Fatalf("apply output does not show the rename map:\n%s", out)
	}
	if !strings.Contains(out, "help") {
		t.Errorf("apply output has no next-steps help block:\n%s", out)
	}

	st := &store.Store{Root: fx.work}
	if _, err := os.Stat(st.TicketDir("GRAPH-1")); err != nil {
		t.Fatalf("tickets/GRAPH-1 missing after apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir("STORE-1"), "tracker", "github.yaml")); err != nil {
		t.Errorf("STORE-1's tracker/github.yaml should survive the migration: %v", err)
	}

	// jig status and jig validate both work the migrated tickets, by their
	// new ids and by their old ids (through the alias). validate also wants
	// a machine-mapped clone of the product repo, unrelated to the
	// migration itself.
	productClone := t.TempDir()
	if _, err := project.InitProject(jigHome, fx.work, map[string]string{"product-repo": productClone}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	for _, id := range []string{"GRAPH-1", "T-1"} {
		out, code := runMain(t, env, "", "status", id, "--store", fx.work)
		if code != 0 {
			t.Errorf("status %s: exit %d\n%s", id, code, out)
		}
	}
	if out, code := runMain(t, env, "", "validate", "GRAPH-1", "--store", fx.work); code != 0 {
		t.Errorf("validate GRAPH-1: exit %d\n%s", code, out)
	}

	// A second migrate is refused: the store is at schema 2 now.
	out, code = runMain(t, env, "", "store", "migrate", "--map", mapPath, "--store", fx.work)
	if code == 0 {
		t.Fatalf("second migrate: exit 0, want a refusal\n%s", out)
	}
}

func TestStoreMigrateRefusesALeftOutTicket(t *testing.T) {
	t.Parallel()
	fx := newMigrateFixture(t)
	mapPath := writeMigrateMap(t, "keys:\n  STORE: everything\ntickets:\n  T-1: STORE\n")
	jigHome := t.TempDir()

	out, code := runMain(t, testEnv(jigHome), "", "store", "migrate", "--map", mapPath, "--dry-run", "--store", fx.work)
	if code == 0 {
		t.Fatalf("exit 0, want a refusal naming T-2\n%s", out)
	}
	if !strings.Contains(out, "T-2") {
		t.Errorf("output does not name T-2:\n%s", out)
	}
}

func TestStoreMigrateRefusesALeaseStillHeld(t *testing.T) {
	t.Parallel()
	fx := newMigrateFixture(t)
	mapPath := writeMigrateMap(t, migrateMapYAML)
	jigHome := t.TempDir()

	leaseDir, err := pool.Dir(jigHome, "product-repo", "T-1", pool.Build)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(leaseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, testEnv(jigHome), "", "store", "migrate", "--map", mapPath, "--store", fx.work)
	if code == 0 {
		t.Fatalf("exit 0, want a refusal naming the held lease\n%s", out)
	}
	if !strings.Contains(out, "T-1") {
		t.Errorf("output does not name the leased ticket T-1:\n%s", out)
	}
}

func TestStoreMigrateWarnsAboutABranchStillOnOrigin(t *testing.T) {
	t.Parallel()
	fx := newMigrateFixture(t)
	mapPath := writeMigrateMap(t, migrateMapYAML)
	jigHome := t.TempDir()

	run := func(d string, args ...string) string {
		t.Helper()
		out, err := gitx.Run(d, args...)
		if err != nil {
			t.Fatalf("git %v (in %s): %v", args, d, err)
		}
		return out
	}
	productWork := filepath.Join(t.TempDir(), "product-work")
	run("", "clone", fx.repoRemote, productWork)
	run(productWork, "config", "user.name", "tester")
	run(productWork, "config", "user.email", "tester@example.invalid")
	if err := os.WriteFile(filepath.Join(productWork, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(productWork, "add", "-A")
	run(productWork, "commit", "-m", "init")
	run(productWork, "push", "origin", "main")
	run(productWork, "checkout", "-b", "jig/T-1")
	run(productWork, "push", "origin", "jig/T-1")

	out, code := runMain(t, testEnv(jigHome), "", "store", "migrate", "--map", mapPath, "--store", fx.work)
	if code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "jig/T-1") {
		t.Errorf("output has no warning about jig/T-1 still on origin:\n%s", out)
	}
}
