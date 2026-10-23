package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

const v3StoreYAML = `schema_version: 3
keys:
    DEMO: everything in demo
trackers: []
platform: platform/
projects: [demo]
`

const v2ProjectYAML = `schema_version: 2
name: demo
keys:
    DEMO: everything in demo
trackers: []
repos:
    - remote: https://example.invalid/org/demo.git
      target: develop
platform: platform/
`

func TestStoreConfigPathPicksTheFileTheStoreHas(t *testing.T) {
	v3 := t.TempDir()
	writeFile(t, filepath.Join(v3, "store.yaml"), v3StoreYAML)
	v2 := t.TempDir()
	writeFile(t, filepath.Join(v2, "project.yaml"), v2ProjectYAML)
	both := t.TempDir()
	writeFile(t, filepath.Join(both, "store.yaml"), v3StoreYAML)
	writeFile(t, filepath.Join(both, "project.yaml"), v2ProjectYAML)
	neither := t.TempDir()

	if got, err := StoreConfigPath(v3); err != nil || got != filepath.Join(v3, "store.yaml") {
		t.Errorf("v3 store: %q, %v", got, err)
	}
	if got, err := StoreConfigPath(v2); err != nil || got != filepath.Join(v2, "project.yaml") {
		t.Errorf("v2 store: %q, %v", got, err)
	}
	_, err := StoreConfigPath(both)
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), "has both store.yaml and project.yaml") {
		t.Errorf("a store with both files: %v", err)
	}
	_, err = StoreConfigPath(neither)
	wantValidationError(t, err)

	for dir, want := range map[string]bool{v3: true, v2: true, both: true, neither: false, "": false} {
		if got := IsStoreDir(dir); got != want {
			t.Errorf("IsStoreDir(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestLoadStoreConfigReadsEitherLayout(t *testing.T) {
	v3 := t.TempDir()
	writeFile(t, filepath.Join(v3, "store.yaml"), v3StoreYAML)
	cfg, err := LoadStoreConfig(v3)
	if err != nil {
		t.Fatalf("LoadStoreConfig v3: %v", err)
	}
	if cfg.SchemaVersion != 3 || cfg.Name != "demo" || len(cfg.Projects) != 1 || len(cfg.Repos) != 0 || cfg.Keys["DEMO"] == "" {
		t.Errorf("v3 config = %+v, want schema 3, the one project demo as its name, no repos", cfg)
	}

	// A store that serves two projects names neither: the marker does.
	two := t.TempDir()
	writeFile(t, filepath.Join(two, "store.yaml"), strings.Replace(v3StoreYAML, "[demo]", "[demo, other]", 1))
	if cfg, err := LoadStoreConfig(two); err != nil || cfg.Name != "" || len(cfg.Projects) != 2 {
		t.Errorf("a two-project store: name %q, projects %v, err %v; want no name", cfg.Name, cfg.Projects, err)
	}

	v2 := t.TempDir()
	writeFile(t, filepath.Join(v2, "project.yaml"), v2ProjectYAML)
	cfg, err = LoadStoreConfig(v2)
	if err != nil {
		t.Fatalf("LoadStoreConfig v2: %v", err)
	}
	if cfg.SchemaVersion != 2 || cfg.Name != "demo" || len(cfg.Repos) != 1 || cfg.Repos[0].TargetBranch() != "develop" {
		t.Errorf("v2 config = %+v, want its own name and repos", cfg)
	}
}

// A config refusal on a store.yaml names store.yaml, not project.yaml.
func TestLoadRefusalsNameTheFileTheStoreHas(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "store.yaml"), "schema_version: 3\nkeys:\n  lower: no\ntrackers: []\n")
	_, err := LoadStoreConfig(dir)
	wantValidationError(t, err)
	var ae *axi.Error
	errors.As(err, &ae)
	if strings.Contains(ae.Msg, "project.yaml") || !strings.Contains(ae.Msg, "store.yaml") {
		t.Errorf("the refusal names the wrong file: %q", ae.Msg)
	}
	for _, h := range ae.Help {
		if strings.Contains(h, "project.yaml") {
			t.Errorf("help names the wrong file: %q", h)
		}
	}

	v2 := t.TempDir()
	writeFile(t, filepath.Join(v2, "project.yaml"), "schema_version: 2\nname: x\nkeys:\n  lower: no\ntrackers: []\n")
	_, err = LoadStoreConfig(v2)
	wantValidationError(t, err)
	errors.As(err, &ae)
	if !strings.Contains(ae.Msg, "project.yaml") {
		t.Errorf("a v2 refusal lost its file name: %q", ae.Msg)
	}
}

// LoadConfig completes a v3 store from the project's marker, found at the
// roots given, then through the machine map.
func TestLoadConfigCompletesAV3StoreFromTheMarker(t *testing.T) {
	jigHome := t.TempDir()
	storeDir := t.TempDir()
	writeFile(t, filepath.Join(storeDir, "store.yaml"), v3StoreYAML)

	t.Run("a single-repo project", func(t *testing.T) {
		root := t.TempDir()
		if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject}); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(jigHome, storeDir, "", root)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Name != "demo" || len(cfg.Repos) != 1 || cfg.Repos[0].Name() != "demo" || cfg.Repos[0].Remote != root || cfg.Repos[0].TargetBranch() != "main" {
			t.Errorf("config = name %q, repos %+v; want demo with the one repo demo at %s", cfg.Name, cfg.Repos, root)
		}
	})

	t.Run("the repo's origin, name and target", func(t *testing.T) {
		root := runGitDir(t, t.TempDir())
		runGitInProject(t, root, "remote", "add", "origin", "https://example.invalid/org/demo-app.git")
		if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject, Repos: []MarkerRepo{{Name: "app", Target: "develop"}}}); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(jigHome, storeDir, "", root)
		if err != nil {
			t.Fatal(err)
		}
		r := cfg.Repos[0]
		if r.Name() != "app" || r.Remote != "https://example.invalid/org/demo-app.git" || r.TargetBranch() != "develop" {
			t.Errorf("repo = %+v (name %s, target %s), want app on origin, target develop", r, r.Name(), r.TargetBranch())
		}
		// A remote the marker names beats the origin.
		if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject, Repos: []MarkerRepo{{Remote: "https://example.invalid/other.git"}}}); err != nil {
			t.Fatal(err)
		}
		cfg, _ = LoadConfig(jigHome, storeDir, "", root)
		if got := cfg.Repos[0].Remote; got != "https://example.invalid/other.git" {
			t.Errorf("remote = %q, want the marker's", got)
		}
	})

	t.Run("an orchestrator's nested repos", func(t *testing.T) {
		root := runGitDir(t, t.TempDir())
		api := runGitDir(t, filepath.Join(root, "api"))
		runGitInProject(t, api, "remote", "add", "origin", "https://example.invalid/org/api.git")
		runGitDir(t, filepath.Join(root, "web")) // no origin
		// A submodule that is not fetched: its URL is .gitmodules'.
		writeFile(t, filepath.Join(root, ".gitmodules"), "[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.invalid/org/lib.git\n")
		m := Marker{Jig: "1", Project: "demo", Kind: KindOrchestrator, Repos: []MarkerRepo{
			{Name: "api", Path: "api"}, {Name: "web", Path: "web"}, {Name: "lib", Path: "lib"},
			{Name: "far", Path: "deeper/far", Remote: "https://example.invalid/org/far.git", Target: "develop"},
		}}
		if err := WriteMarker(root, m); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(jigHome, storeDir, "", root)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]Repo{}
		for _, r := range cfg.Repos {
			got[r.Name()] = r
		}
		if len(cfg.Repos) != 4 {
			t.Fatalf("repos = %+v, want four", cfg.Repos)
		}
		if got["api"].Remote != "https://example.invalid/org/api.git" {
			t.Errorf("api's remote = %q, want its clone's origin", got["api"].Remote)
		}
		if want := filepath.Join(root, "web"); got["web"].Remote != want {
			t.Errorf("web's remote = %q, want its directory %q (no origin, no submodule)", got["web"].Remote, want)
		}
		if got["lib"].Remote != "https://example.invalid/org/lib.git" {
			t.Errorf("lib's remote = %q, want .gitmodules' URL", got["lib"].Remote)
		}
		if got["far"].Remote != "https://example.invalid/org/far.git" || got["far"].TargetBranch() != "develop" {
			t.Errorf("far = %+v, want the marker's remote and target", got["far"])
		}
	})

	t.Run("the machine map when no root has the marker", func(t *testing.T) {
		root := t.TempDir()
		if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject}); err != nil {
			t.Fatal(err)
		}
		if err := UpdateMachine(jigHome, func(m map[string]MachineProject) error {
			m["demo"] = MachineProject{Store: storeDir, Root: root, Clones: map[string]string{"demo": root}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(jigHome, storeDir, "demo", t.TempDir())
		if err != nil || len(cfg.Repos) != 1 || cfg.Repos[0].Name() != "demo" {
			t.Errorf("config = %+v, %v; want the repo from the mapped root's marker", cfg.Repos, err)
		}
	})

	t.Run("a member's marker describes nothing, another project's is not ours", func(t *testing.T) {
		member := t.TempDir()
		if err := WriteMarker(member, Marker{Jig: "1", Project: "demo", Kind: KindMember}); err != nil {
			t.Fatal(err)
		}
		foreign := t.TempDir()
		if err := WriteMarker(foreign, Marker{Jig: "1", Project: "elsewhere", Kind: KindProject}); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(t.TempDir(), storeDir, "demo", member, foreign)
		if err != nil || len(cfg.Repos) != 0 || cfg.Name != "demo" {
			t.Errorf("config = name %q repos %+v, %v; want the store's own, no repos", cfg.Name, cfg.Repos, err)
		}
	})
}

// A v2 store carries its repos itself; a marker beside it does not change them.
func TestLoadConfigLeavesAV2StoreAlone(t *testing.T) {
	storeDir := t.TempDir()
	writeFile(t, filepath.Join(storeDir, "project.yaml"), v2ProjectYAML)
	root := t.TempDir()
	if err := WriteMarker(root, Marker{Jig: "1", Project: "demo", Kind: KindProject, Repos: []MarkerRepo{{Name: "other"}}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(t.TempDir(), storeDir, "demo", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Name() != "demo" || cfg.Repos[0].TargetBranch() != "develop" {
		t.Errorf("repos = %+v, want project.yaml's own", cfg.Repos)
	}
}

// Repo.Label names a repo whatever its remote says, and a v2 project.yaml may
// not need it.
func TestRepoNameHonorsALabel(t *testing.T) {
	if got := (Repo{Remote: "https://example.invalid/org/shop-api.git", Label: "api"}).Name(); got != "api" {
		t.Errorf("Name() = %q, want the label", got)
	}
	if got := (Repo{Remote: "https://example.invalid/org/shop-api.git"}).Name(); got != "shop-api" {
		t.Errorf("Name() = %q, want the remote's basename", got)
	}
}

func TestSubmodulesAndGitmodulesURL(t *testing.T) {
	root := t.TempDir()
	if got := Submodules(root); len(got) != 0 {
		t.Errorf("Submodules without .gitmodules = %v", got)
	}
	runGitDir(t, root)
	writeFile(t, filepath.Join(root, ".gitmodules"), "[submodule \"first\"]\n\tpath = libs/first\n\turl = ../first.git\n[submodule \"second\"]\n\tpath = second\n\turl = https://example.invalid/second.git\n")
	got := Submodules(root)
	if len(got) != 2 || got[0] != "libs/first" || got[1] != "second" {
		t.Errorf("Submodules = %v, want libs/first and second", got)
	}
	if url := GitmodulesURL(root, "libs/first"); url != "../first.git" {
		t.Errorf("GitmodulesURL(libs/first) = %q", url)
	}
	if url := GitmodulesURL(root, "nope"); url != "" {
		t.Errorf("GitmodulesURL(nope) = %q, want none", url)
	}
}

// runGitDir makes dir a git repository (creating it) and returns it.
func runGitDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitInProject(t, dir, "init", "-b", "main")
	return dir
}
