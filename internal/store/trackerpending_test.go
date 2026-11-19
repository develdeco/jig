package store

import (
	"os"
	"path/filepath"
	"testing"
)

// cleanDuring fails the test when the store's working tree holds anything
// uncommitted: called from inside a hook, it is what a claim on the same clone
// would meet.
func cleanDuring(t *testing.T, work, when string) {
	t.Helper()
	if got := porcelain(t, work); got != "" {
		t.Errorf("%s: the working tree holds the sync's writes before the landing:\n%s", when, got)
	}
}

// commitLegacy leaves a record in its ticket's own folder, the old place, and
// commits it.
func commitLegacy(t *testing.T, st *Store, o TrackerOwner, issue string) {
	t.Helper()
	abs := filepath.Join(st.Root, st.legacyRecordRel(o))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("repo: acme/tracking\nissue: "+issue+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("JIG-1: old place"); err != nil {
		t.Fatal(err)
	}
	st.takeTrackerWrites()
}
