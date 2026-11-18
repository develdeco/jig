package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAMarkerWithASlotsKeyStillLoads: a marker written when slots: pinned a
// count of suite runs still loads with its project and the rest.
func TestAMarkerWithASlotsKeyStillLoads(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, MarkerFile)
	if err := os.WriteFile(path, []byte("jig: \"1\"\nproject: demo\nkind: project\nslots: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadMarker(path)
	if err != nil {
		t.Fatalf("LoadMarker: %v", err)
	}
	if m.Project != "demo" || m.Kind != KindProject {
		t.Errorf("marker = %+v, want project demo of kind project", m)
	}
}
