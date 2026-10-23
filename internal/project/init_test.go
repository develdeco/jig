package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

func TestInferKind(t *testing.T) {
	plain := t.TempDir()
	if inf := InferKind(plain); inf.Kind != KindProject || len(inf.Nested) != 0 {
		t.Errorf("an empty folder = %+v, want a single-repo project", inf)
	}
	root := runGitDir(t, t.TempDir())
	runGitDir(t, filepath.Join(root, "api"))
	runGitDir(t, filepath.Join(root, "web"))
	inf := InferKind(root)
	if inf.Kind != KindOrchestrator || len(inf.Nested) != 2 || !strings.Contains(inf.Reason, "api, web") {
		t.Errorf("a repo holding two repos = %+v, want an orchestrator naming both", inf)
	}
	sub := runGitDir(t, t.TempDir())
	writeFile(t, filepath.Join(sub, ".gitmodules"), "[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.invalid/lib.git\n")
	if inf := InferKind(sub); inf.Kind != KindOrchestrator {
		t.Errorf("a repo with a .gitmodules entry = %+v, want an orchestrator", inf)
	}
}

func TestCheckInitDir(t *testing.T) {
	t.Run("a folder outside every project", func(t *testing.T) {
		if err := CheckInitDir(t.TempDir()); err != nil {
			t.Errorf("CheckInitDir: %v", err)
		}
		if err := CheckInitDir(runGitDir(t, t.TempDir())); err != nil {
			t.Errorf("CheckInitDir at the top of a repo: %v", err)
		}
	})
	t.Run("inside a project", func(t *testing.T) {
		root := t.TempDir()
		if err := WriteMarker(root, Marker{Jig: "1", Project: "p", Kind: KindOrchestrator}); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(root, "api")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		err := CheckInitDir(nested)
		wantValidationError(t, err)
		for _, want := range []string{"inside the jig project at", "jig repos add", "attach --tracked --members"} {
			if !strings.Contains(err.Error()+strings.Join(helpOf(err), "\n"), want) {
				t.Errorf("refusal lacks %q: %v / %v", want, err, helpOf(err))
			}
		}
		err = CheckInitDir(root)
		wantValidationError(t, err)
		if !strings.Contains(err.Error(), "already initialized") {
			t.Errorf("at the root again: %v", err)
		}
	})
	t.Run("the per-repo manifest is no marker", func(t *testing.T) {
		repo := runGitDir(t, t.TempDir())
		writeFile(t, filepath.Join(repo, ".claude", "jig.yaml"), "workspaces: []\n")
		if err := CheckInitDir(repo); err != nil {
			t.Errorf("CheckInitDir in a repo whose .claude/jig.yaml is the manifest: %v", err)
		}
	})
	t.Run("below the top of a repo", func(t *testing.T) {
		repo := runGitDir(t, t.TempDir())
		sub := filepath.Join(repo, "pkg")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		err := CheckInitDir(sub)
		wantValidationError(t, err)
		if !strings.Contains(err.Error(), "below its top") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a store", func(t *testing.T) {
		store := t.TempDir()
		writeFile(t, filepath.Join(store, "store.yaml"), v3StoreYAML)
		err := CheckInitDir(store)
		wantValidationError(t, err)
		if !strings.Contains(err.Error(), "is a jig store") {
			t.Errorf("err = %v", err)
		}
	})
}

// helpOf is an *axi.Error's help lines.
func helpOf(err error) []string {
	var ae *axi.Error
	if errors.As(err, &ae) {
		return ae.Help
	}
	return nil
}

func TestInitOrchestratorListsTheNestedRepos(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "shop")
	api := runGitDir(t, filepath.Join(root, "api"))
	web := runGitDir(t, filepath.Join(root, "web"))
	jigHome := t.TempDir()

	res, err := Init(InitOpts{Dir: root, JigHome: jigHome, Kind: KindOrchestrator, MakeRepo: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !res.MadeGit || !IsRepo(root) {
		t.Errorf("the orchestrator was not made a repository: %+v", res)
	}
	m, err := LoadMarker(filepath.Join(root, MarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindOrchestrator || m.Project != "shop" || len(m.Repos) != 2 || m.Repos[0].Path != "api" || m.Repos[1].Path != "web" {
		t.Errorf("marker = %+v, want orchestrator shop over api and web", m)
	}
	machine, _ := LoadMachine(jigHome)
	mp := machine["shop"]
	if mp.Root != root || mp.Store != filepath.Join(parent, "shop-tickets") || mp.Clones["api"] != api || mp.Clones["web"] != web || len(mp.Clones) != 2 {
		t.Errorf("machine entry = %+v", mp)
	}
	cfg, err := LoadConfig(jigHome, res.Store, "", root)
	if err != nil || len(cfg.Repos) != 2 || cfg.Repos[0].Name() != "api" || cfg.Repos[1].Name() != "web" {
		t.Errorf("config repos = %+v, %v", cfg.Repos, err)
	}
	// The orchestrator's own repo is left with nothing committed.
	if status := runGitInProject(t, root, "status", "--porcelain"); !strings.Contains(status, "?? jig.yaml") {
		t.Errorf("git status = %q, want jig.yaml untracked", status)
	}
}

func TestInitOrchestratorNeedsLeaveToMakeARepo(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "shop")
	runGitDir(t, filepath.Join(root, "api"))
	_, err := Init(InitOpts{Dir: root, JigHome: t.TempDir(), Kind: KindOrchestrator})
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), "is not a git repository") {
		t.Errorf("err = %v", err)
	}
	for _, p := range []string{filepath.Join(root, ".git"), filepath.Join(root, "jig.yaml"), filepath.Join(parent, "shop-tickets")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the refused init left %s behind: %v", p, err)
		}
	}
}

func TestInitSingleRepoTakesNoRepos(t *testing.T) {
	_, root := newRepoDir(t, "solo")
	_, err := Init(InitOpts{Dir: root, JigHome: t.TempDir(), Kind: KindProject, Repos: []string{"x"}})
	wantValidationError(t, err)
}

func TestInitRefusesAProjectNameMappedElsewhere(t *testing.T) {
	jigHome := t.TempDir()
	_, first := newRepoDir(t, "twin")
	if _, err := Init(InitOpts{Dir: first, JigHome: jigHome, Kind: KindProject}); err != nil {
		t.Fatal(err)
	}
	parent, second := newRepoDir(t, "twin")
	_, err := Init(InitOpts{Dir: second, JigHome: jigHome, Kind: KindProject})
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), `a project named "twin" is already mapped`) {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "twin-tickets")); !os.IsNotExist(err) {
		t.Errorf("the refused init made a store: %v", err)
	}
	// A mapping whose store is gone does not hold the name.
	machine, _ := LoadMachine(jigHome)
	if err := os.RemoveAll(machine["twin"].Store); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(InitOpts{Dir: second, JigHome: jigHome, Kind: KindProject}); err != nil {
		t.Errorf("Init over a stale mapping: %v", err)
	}
}

func TestInitTakesAwayWhatItMadeWhenARepoCannotBeFetched(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "hub")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.ToSlash(filepath.Join(parent, "no-such-repo.git"))
	url := "file:///" + strings.TrimPrefix(missing, "/")
	jigHome := t.TempDir()
	if _, err := Init(InitOpts{Dir: root, JigHome: jigHome, Kind: KindOrchestrator, MakeRepo: true, Repos: []string{url}}); err == nil {
		t.Fatal("Init fetched a repository that is not there")
	}
	for _, p := range []string{filepath.Join(parent, "hub-tickets"), filepath.Join(root, MarkerFile)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the failed init left %s behind: %v", p, err)
		}
	}
	if m, _ := LoadMachine(jigHome); len(m) != 0 {
		t.Errorf("the failed init mapped a project: %v", m)
	}
}
