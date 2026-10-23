package locate

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// world is a scratch machine for one test: a jig home, and a root directory to
// lay stores and clones out under.
type world struct {
	t    *testing.T
	home string
	root string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	return &world{t: t, home: t.TempDir(), root: t.TempDir()}
}

func (w *world) path(parts ...string) string {
	return filepath.Join(append([]string{w.root}, parts...)...)
}

// store lays out a store named name under the root, declaring a repo for each
// remote, and returns its directory. It is not a git repo.
func (w *world) store(dir, name string, remotes ...string) string {
	w.t.Helper()
	path := w.path(dir)
	if err := os.MkdirAll(filepath.Join(path, "platform"), 0o755); err != nil {
		w.t.Fatal(err)
	}
	yaml := fmt.Sprintf("schema_version: 2\nname: %s\nkeys:\n  JIG: everything\ntrackers: []\nplatform: platform/\nrepos:\n", name)
	for _, r := range remotes {
		yaml += fmt.Sprintf("  - remote: %q\n", filepath.ToSlash(r))
	}
	if len(remotes) == 0 {
		yaml = fmt.Sprintf("schema_version: 2\nname: %s\nkeys:\n  JIG: everything\ntrackers: []\nplatform: platform/\nrepos: []\n", name)
	}
	if err := os.WriteFile(filepath.Join(path, "project.yaml"), []byte(yaml), 0o644); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "ledger.md"), nil, 0o644); err != nil {
		w.t.Fatal(err)
	}
	return path
}

// gitStore is store as a git repo, so the directories under it have a top
// level.
func (w *world) gitStore(dir, name string, remotes ...string) string {
	w.t.Helper()
	path := w.store(dir, name, remotes...)
	git(w.t, path, "init", "-b", "main")
	return path
}

// repo makes a git repo with one commit under the root, with an origin remote
// when origin is not "".
func (w *world) repo(dir, origin string) string {
	w.t.Helper()
	path := w.path(dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		w.t.Fatal(err)
	}
	git(w.t, path, "init", "-b", "main")
	commit(w.t, path)
	if origin != "" {
		git(w.t, path, "remote", "add", "origin", origin)
	}
	return path
}

func (w *world) mapProject(name string, mp project.MachineProject) {
	w.t.Helper()
	if err := project.UpdateMachine(w.home, func(m map[string]project.MachineProject) error {
		m[name] = mp
		return nil
	}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) machine() map[string]project.MachineProject {
	w.t.Helper()
	m, err := project.LoadMachine(w.home)
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

func (w *world) locate(cwd string, o Opts) Where {
	w.t.Helper()
	got, err := Locate(w.home, cwd, o)
	if err != nil {
		w.t.Fatalf("Locate(%s): %v", cwd, err)
	}
	return got
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

func commit(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "--allow-empty", "-m", "init")
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(parts...)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func same(t *testing.T, what, got, want string) {
	t.Helper()
	if !gitx.SameDir(got, want) {
		t.Errorf("%s = %q, want the directory %q", what, got, want)
	}
}
