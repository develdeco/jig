package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAManifestWithAHeavyKeyStillLoadsItsOracles: a declaration written when
// heavy: named the oracles that took a suite slot still loads, and its oracles
// resolve, whatever happens to that key.
func TestAManifestWithAHeavyKeyStillLoadsItsOracles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	decl := "oracles:\n  test: go test ./...\n  lint: golint\nheavy: [test]\n"
	if err := os.WriteFile(filepath.Join(dir, ".claude", "jig.yaml"), []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m.Oracles["test"] != "go test ./..." || m.Oracles["lint"] != "golint" {
		t.Errorf("oracles = %v, want test and lint resolved", m.Oracles)
	}
}
