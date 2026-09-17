package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestInitStandalone runs `jig init --standalone` against a fixture repo
// clone and asserts it creates a sibling "<repo>-tickets" store: a fresh git
// repo on main with project.yaml, ledger.md, and platform/.
func TestInitStandalone(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{})

	r := runJig(t, fx.RepoDir, "init", "--standalone")
	if r.Code != 0 {
		t.Fatalf("jig init --standalone exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	storeDir := filepath.Join(filepath.Dir(fx.RepoDir), filepath.Base(fx.RepoDir)+"-tickets")
	if _, err := os.Stat(filepath.Join(storeDir, "project.yaml")); err != nil {
		t.Fatalf("project.yaml missing under standalone store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, "ledger.md")); err != nil {
		t.Fatalf("ledger.md missing under standalone store: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(storeDir, "platform")); err != nil || !fi.IsDir() {
		t.Fatalf("platform/ missing under standalone store: %v", err)
	}
	// project.InitStandalone writes project.yaml/ledger.md/platform/ without
	// committing them, so the new repo has no commits yet; rev-parse
	// --abbrev-ref HEAD requires a resolvable commit and fails on an unborn
	// branch, but symbolic-ref does not.
	branch, err := gitx.Run(storeDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("symbolic-ref HEAD in standalone store: %v", err)
	}
	if branch != "main" {
		t.Fatalf("standalone store branch = %q, want main", branch)
	}
}

// TestInitProject runs `jig init --store <path> --clone <name>=<path>`
// against a fresh JIG_HOME and asserts the machine mapping it writes there
// contains the expected entry, keyed by the project's name.
func TestInitProject(t *testing.T) {
	// Generate the fixture under its own JIG_HOME (fixture.Generate performs
	// its own InitProject call as part of materializing the fixture); then
	// point a second, fresh JIG_HOME at the `jig init` invocation under test,
	// so this test actually exercises the CLI's wiring rather than only
	// re-observing what Generate already did.
	fx, _ := newFixture(t, fixture.Opts{})
	targetHome := t.TempDir()
	t.Setenv("JIG_HOME", targetHome)

	r := runJig(t, t.TempDir(), "init", "--store", fx.StoreDir, "--clone", "fixture-repo="+fx.RepoDir)
	if r.Code != 0 {
		t.Fatalf("jig init --store --clone exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	data, err := os.ReadFile(filepath.Join(targetHome, "projects.yaml"))
	if err != nil {
		t.Fatalf("read machine mapping: %v", err)
	}
	var machine map[string]project.MachineProject
	if err := yaml.Unmarshal(data, &machine); err != nil {
		t.Fatalf("parse machine mapping: %v", err)
	}
	entry, ok := machine["fixture"]
	if !ok {
		t.Fatalf("machine mapping missing project %q; got %+v", "fixture", machine)
	}
	wantStore, _ := filepath.Abs(fx.StoreDir)
	if entry.Store != wantStore {
		t.Fatalf("machine mapping store = %q, want %q", entry.Store, wantStore)
	}
	wantClone, _ := filepath.Abs(fx.RepoDir)
	if entry.Clones["fixture-repo"] != wantClone {
		t.Fatalf("machine mapping clone fixture-repo = %q, want %q", entry.Clones["fixture-repo"], wantClone)
	}
}

// TestInitStandaloneQuickstart drives the README Quickstart's exact command
// sequence (init --standalone, ticket new, validate) against a plain repo
// directory, deliberately without fixture.Generate: Generate performs its
// own project.InitProject call to write the machine mapping, which would
// mask a regression in init --standalone's own wiring. brief.md and
// slices.yaml are written by hand here, the way the intake skill would
// leave them, so this exercises the real path `jig validate` takes.
func TestInitStandaloneQuickstart(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	r := runJig(t, repoDir, "init", "--standalone")
	if r.Code != 0 {
		t.Fatalf("jig init --standalone exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	r = runJig(t, repoDir, "ticket", "new", "--title", "Fix the thing")
	if r.Code != 0 {
		t.Fatalf("jig ticket new exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	const ticket = "T-1" // first ticket minted into a fresh store, per project.yaml's default ticket_format
	if !strings.Contains(r.Stdout, ticket) {
		t.Fatalf("jig ticket new stdout missing %q:\n%s", ticket, r.Stdout)
	}

	storeDir := filepath.Join(parent, "myrepo-tickets")
	ticketDir := filepath.Join(storeDir, ticket)
	brief := "# Fix the thing\n\n## Goal\n\nMake the thing work.\n"
	hashes := store.BriefSectionHashes([]byte(brief))
	slices := fmt.Sprintf(`slices:
  - id: a
    workspace: root
    goal: Make the thing work.
    oracle: "true"
    blocked_by: []
    from_brief: ["%s"]
`, hashes["Goal"])
	if err := os.WriteFile(filepath.Join(ticketDir, "brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ticketDir, "slices.yaml"), []byte(slices), 0o644); err != nil {
		t.Fatalf("write slices.yaml: %v", err)
	}

	r = runJig(t, repoDir, "validate", ticket)
	if r.Code != 0 {
		t.Fatalf("jig validate %s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", ticket, r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("jig validate stdout missing \"valid: yes\":\n%s", r.Stdout)
	}
}
