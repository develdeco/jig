package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAMarkerWithASlotsKeyOfAnyValueStillLoads: slots: no longer means
// anything, so a marker that carries it loads whatever its value, including
// the zero and negative counts that were once an error.
func TestAMarkerWithASlotsKeyOfAnyValueStillLoads(t *testing.T) {
	for _, value := range []string{"3", "0", "-1"} {
		path := filepath.Join(t.TempDir(), MarkerFile)
		if err := os.WriteFile(path, []byte("jig: \"1\"\nproject: demo\nkind: project\nslots: "+value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := LoadMarker(path)
		if err != nil {
			t.Errorf("slots: %s: LoadMarker: %v", value, err)
			continue
		}
		if m.Project != "demo" || m.Kind != KindProject {
			t.Errorf("slots: %s: marker = %+v, want project demo of kind project", value, m)
		}
	}
}
