package project

import (
	"path/filepath"
	"testing"
)

// TestAMarkersPinnedCountReachesTheConfigAndNoneStaysNone: a marker that sets
// no slots gives a config that pins none (PinnedSlots 0, so the project is
// measured), and slots: N gives N.
func TestAMarkersPinnedCountReachesTheConfigAndNoneStaysNone(t *testing.T) {
	jigHome := t.TempDir()
	storeDir := t.TempDir()
	writeFile(t, filepath.Join(storeDir, "store.yaml"), v3StoreYAML)
	three, one := 3, 1
	for name, tc := range map[string]struct {
		slots *int
		want  int
	}{
		"unset":   {nil, 0},
		"pinned3": {&three, 3},
		"pinned1": {&one, 1},
	} {
		root := t.TempDir()
		if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject, Slots: tc.slots}); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(jigHome, storeDir, "", root)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.PinnedSlots(); got != tc.want {
			t.Errorf("%s: PinnedSlots = %d, want %d", name, got, tc.want)
		}
	}
}
