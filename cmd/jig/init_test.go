package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmdInitStandaloneRefusesReinit checks that a second `jig init
// --standalone` against an already-initialized store is refused rather than
// silently overwriting its ledger.md and project.yaml back to their fresh
// defaults (project.InitStandalone always overwrites both).
func TestCmdInitStandaloneRefusesReinit(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	t.Chdir(repoDir)

	var buf bytes.Buffer
	if code := cmdInit([]string{"--standalone"}, &buf); code != 0 {
		t.Fatalf("first init exit code = %d, output:\n%s", code, buf.String())
	}

	storeDir := filepath.Join(parent, "myrepo-tickets")
	ledger := filepath.Join(storeDir, "ledger.md")
	if err := os.WriteFile(ledger, []byte("real ledger content\n"), 0o644); err != nil {
		t.Fatalf("write ledger marker: %v", err)
	}

	buf.Reset()
	code := cmdInit([]string{"--standalone"}, &buf)
	if code == 0 {
		t.Fatalf("second init exit code = 0, want a refusal; output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "already initialized") {
		t.Errorf("expected an \"already initialized\" refusal, got:\n%s", buf.String())
	}

	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if string(data) != "real ledger content\n" {
		t.Fatalf("ledger.md was overwritten: %q", data)
	}
}
