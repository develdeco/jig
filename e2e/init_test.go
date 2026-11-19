package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestInitStandaloneQuickstart drives the command sequence a new project goes
// through (init --standalone, ticket new, validate) against a plain repo
// directory, deliberately without fixture.Generate: Generate performs its
// own project.InitProject call to write the machine mapping, which would
// mask a regression in init --standalone's own wiring. brief.md and
// slices.yaml are written by hand here, the way the intake skill would
// leave them, so this exercises the real path `jig validate` takes.
func TestInitStandaloneQuickstart(t *testing.T) {
	t.Parallel()
	env := jigEnv{home: t.TempDir()}

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	r := runJig(t, env, repoDir, "init", "--standalone")
	if r.Code != 0 {
		t.Fatalf("jig init --standalone exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	r = runJig(t, env, repoDir, "ticket", "new", "--title", "Fix the thing")
	if r.Code != 0 {
		t.Fatalf("jig ticket new exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	const ticket = "MYREPO-1" // first ticket minted into a fresh store, under its derived key (from repoDir's base name "myrepo")
	if !strings.Contains(r.Stdout, ticket) {
		t.Fatalf("jig ticket new stdout missing %q:\n%s", ticket, r.Stdout)
	}

	storeDir := filepath.Join(parent, "myrepo-tickets")
	ticketDir := filepath.Join(storeDir, "tickets", ticket)
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

	r = runJig(t, env, repoDir, "validate", ticket)
	if r.Code != 0 {
		t.Fatalf("jig validate %s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", ticket, r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("jig validate stdout missing \"valid: yes\":\n%s", r.Stdout)
	}
}
