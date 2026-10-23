package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmdInitStandaloneRefusesReinit checks that a second `jig init
// --standalone` in an already-initialized folder is refused rather than
// silently overwriting the store's ledger.md and store.yaml back to their
// fresh defaults.
func TestCmdInitStandaloneRefusesReinit(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	e := testEnv(t.TempDir()).inDir(repoDir)

	var buf bytes.Buffer
	if code := cmdInit(e, []string{"--standalone"}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("first init exit code = %d, output:\n%s", code, buf.String())
	}

	storeDir := filepath.Join(parent, "myrepo-tickets")
	ledger := filepath.Join(storeDir, "ledger.md")
	if err := os.WriteFile(ledger, []byte("real ledger content\n"), 0o644); err != nil {
		t.Fatalf("write ledger marker: %v", err)
	}

	buf.Reset()
	code := cmdInit(e, []string{"--standalone"}, &buf, strings.NewReader(""))
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
