package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/fixture"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/project"
)

// TestInitStandalone runs `jig init --standalone` against a fixture repo
// clone and asserts it creates a sibling "<repo>-tickets" store: a fresh git
// repo on main with project.yaml, ledger.md, and platform/.
func TestInitStandalone(t *testing.T) {
	requireBinary(t)

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
	requireBinary(t)

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
