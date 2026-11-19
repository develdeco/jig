package mirror

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// addDemoChart commits a chart demo whose tickets.yaml is ticketsYAML.
func addDemoChart(t *testing.T, st *store.Store, ticketsYAML string) {
	t.Helper()
	dir := filepath.Join(st.Root, "charts", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tickets.yaml"), []byte(ticketsYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", "add chart")
}

// treeLinks reads every symlink under trackers/ as link path (inside an
// entry's folder, named by the store id) to the folder it leads to, both
// relative to the store root, in slash form.
func treeLinks(t *testing.T, st *store.Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(st.Root, "trackers")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		to, err := filepath.EvalSymlinks(p)
		if err != nil {
			return err
		}
		want, err := filepath.EvalSymlinks(st.Root)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(want, to)
		if err != nil {
			return err
		}
		from, _ := filepath.Rel(st.Root, p)
		out[filepath.ToSlash(from)] = filepath.ToSlash(rel)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}
