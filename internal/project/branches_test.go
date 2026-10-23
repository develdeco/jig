package project

import (
	"path/filepath"
	"testing"
)

// TestLoadConfigCarriesWhichTrackerLeadsBranches: the marker's branches: from:
// is the config's BranchesFrom, which the store names every ticket's branch by.
// A marker that says none, and a v2 store (which has no marker), leave it empty.
func TestLoadConfigCarriesWhichTrackerLeadsBranches(t *testing.T) {
	jigHome := t.TempDir()
	storeDir := t.TempDir()
	writeFile(t, filepath.Join(storeDir, "store.yaml"), v3StoreYAML)

	for name, tc := range map[string]struct {
		branches *BranchRule
		want     string
	}{
		"named":       {&BranchRule{From: "jira"}, "jira"},
		"github":      {&BranchRule{From: "github"}, "github"},
		"none named":  {&BranchRule{}, ""},
		"no branches": {nil, ""},
	} {
		root := t.TempDir()
		if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject, Branches: tc.branches}); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(jigHome, storeDir, "", root)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BranchesFrom != tc.want {
			t.Errorf("%s: BranchesFrom = %q, want %q", name, cfg.BranchesFrom, tc.want)
		}
	}

	v2 := t.TempDir()
	writeFile(t, filepath.Join(v2, "project.yaml"), v2ProjectYAML)
	root := t.TempDir()
	if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject, Branches: &BranchRule{From: "jira"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(jigHome, v2, "demo", root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BranchesFrom != "" {
		t.Errorf("a v2 store: BranchesFrom = %q, want empty: a v2 store takes nothing from a marker", cfg.BranchesFrom)
	}
}
