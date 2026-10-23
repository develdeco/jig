package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenTakesEitherConfigFile: a v2 store has project.yaml, a v3 one
// store.yaml; Open accepts both and refuses a directory that has neither.
func TestOpenTakesEitherConfigFile(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"project.yaml", "store.yaml"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("schema_version: 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		st, err := Open(dir)
		if err != nil || st.Root != dir {
			t.Errorf("Open on a store with %s = %+v, %v", name, st, err)
		}
	}
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("Open on a directory with neither file: nil error")
	}
}
