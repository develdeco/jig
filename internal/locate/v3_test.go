package locate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// storeV3 lays out a v3 store under the root: a store.yaml serving projects
// and no project.yaml, which carries no name and no repos.
func (w *world) storeV3(dir string, projects ...string) string {
	w.t.Helper()
	path := w.path(dir)
	if err := os.MkdirAll(filepath.Join(path, "platform"), 0o755); err != nil {
		w.t.Fatal(err)
	}
	yaml := "schema_version: 3\nkeys:\n  JIG: everything\ntrackers: []\nplatform: platform/\nprojects: [" + strings.Join(projects, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(path, "store.yaml"), []byte(yaml), 0o644); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "ledger.md"), nil, 0o644); err != nil {
		w.t.Fatal(err)
	}
	return path
}

func (w *world) marker(dir string, m project.Marker) {
	w.t.Helper()
	if err := project.WriteMarker(dir, m); err != nil {
		w.t.Fatal(err)
	}
}

// A v3 store has no project.yaml: the marker's directory finds it, as a sibling
// or through the machine map, and names the repo the marker's way.
func TestLocateV3StoreFromTheMarker(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	repo := w.repo("demo", "")
	w.marker(repo, project.Marker{Jig: "1", Project: "demo", Kind: project.KindProject})
	storeDir := w.storeV3("demo-tickets", "demo")

	got := w.locate(repo, Opts{})
	if got.How != HowMarker || got.Project != "demo" || got.Kind != project.KindProject || got.Repo != "demo" {
		t.Errorf("Locate = %+v, want marker, project demo, repo demo", got)
	}
	same(t, "store", got.StorePath, storeDir)
	same(t, "repo dir", got.RepoDir, repo)
	if !got.Mapped {
		t.Errorf("the recognized clone was not mapped")
	}
	if mp := w.machine()["demo"]; mp.Clones["demo"] != repo && !strings.EqualFold(mp.Clones["demo"], repo) {
		t.Errorf("machine entry = %+v, want the clone demo", mp)
	}
	openStore(t, storeDir) // store.Open takes the v3 store
}

// A repo the marker names keeps that name whatever its remote says.
func TestLocateV3OrchestratorNamesRepoByTheMarker(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	shop := w.repo("shop", "")
	api := w.repo("shop/api", "https://example.invalid/org/shop-api-service.git")
	w.marker(shop, project.Marker{Jig: "1", Project: "shop", Kind: project.KindOrchestrator, Repos: []project.MarkerRepo{{Name: "api", Path: "api"}}})
	w.storeV3("shop-tickets", "shop")

	got := w.locate(mkdir(t, api, "sub"), Opts{})
	if got.How != HowMarker || got.Kind != project.KindOrchestrator || got.Repo != "api" {
		t.Errorf("Locate in the nested repo = %+v, want orchestrator, repo api", got)
	}
	same(t, "repo dir", got.RepoDir, api)
	if clones := w.machine()["shop"].Clones; clones["api"] == "" {
		t.Errorf("machine clones = %v, want api", clones)
	}
}

// --store on a v3 store names the project the store serves; a store that
// serves several names none.
func TestLocateV3StoreFlag(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	one := w.storeV3("one-tickets", "one")
	two := w.storeV3("two-tickets", "a", "b")
	got := w.locate(w.root, Opts{StoreFlag: one})
	if got.How != HowFlag || got.Project != "one" {
		t.Errorf("--store on a v3 store = %+v, want flag, project one", got)
	}
	got = w.locate(w.root, Opts{StoreFlag: two})
	if got.How != HowFlag || got.Project != "" {
		t.Errorf("--store on a two-project store = %+v, want flag, no project name", got)
	}
}

// Standing in a v3 store, or in the repo it is the sibling of, finds it.
func TestLocateV3StoreDirectories(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	storeDir := w.storeV3("demo-tickets", "demo")
	got := w.locate(storeDir, Opts{})
	if got.How != HowStore || got.Project != "demo" {
		t.Errorf("Locate in a v3 store = %+v, want store, project demo", got)
	}
	same(t, "store", got.StorePath, storeDir)

	// The sibling of a repo that has no marker and no map entry.
	repo := w.repo("demo", "")
	got = w.locate(repo, Opts{})
	if got.How != HowStore || got.StorePath == "" {
		t.Errorf("Locate in a repo next to a v3 store = %+v, want the sibling store", got)
	}
}

// A fresh clone is recognized by its remote against the repos a v3 project's
// marker lists.
func TestLocateV3MapRemote(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	url := "https://example.invalid/org/demo.git"
	first := w.repo("first", url)
	w.marker(first, project.Marker{Jig: "1", Project: "demo", Kind: project.KindProject})
	storeDir := w.storeV3("demo-tickets", "demo")
	w.mapProject("demo", project.MachineProject{Store: storeDir, Root: first, Clones: map[string]string{"demo": first}})

	second := w.repo("second", url)
	got := w.locate(second, Opts{})
	if got.How != HowMapRemote || got.Project != "demo" || got.Repo != "demo" {
		t.Errorf("Locate in a second clone = %+v, want map-remote, project demo, repo demo", got)
	}
	same(t, "store", got.StorePath, storeDir)
}
