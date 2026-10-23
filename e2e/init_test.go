package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestInitStandaloneLocal runs a bare `jig init` in a git repo that holds no
// nested repos: jig reads the case off the folder, says it picked a
// single-repo project before it writes, and leaves a project that is usable
// with no remote anywhere (the digester chart's decision 30, a project is
// local until shared): a v3 store beside the repo, a jig.yaml in it with no
// store: key, and the machine map entry. `ticket new` and `here` work at once.
func TestInitStandaloneLocal(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	parent := t.TempDir()
	repo := projGitRepo(t, filepath.Join(parent, "myrepo"))
	storeDir := filepath.Join(parent, "myrepo-tickets")

	r := projRun(t, env, repo, "init")
	for _, want := range []string{"picked:\n  mode: standalone\n", "init:\n  mode: standalone\n  project: myrepo\n", "jig share"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig init stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if strings.Index(r.Stdout, "picked:") > strings.Index(r.Stdout, "init:") {
		t.Errorf("jig init says what it picked after it wrote:\n%s", r.Stdout)
	}

	// A v3 store: store.yaml, no project.yaml, clean, one commit.
	if _, err := os.Stat(filepath.Join(storeDir, "store.yaml")); err != nil {
		t.Fatalf("store.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, "project.yaml")); !os.IsNotExist(err) {
		t.Errorf("a v3 store has no project.yaml: %v", err)
	}
	if status := gitLog(t, storeDir, "status", "--porcelain"); status != "" {
		t.Errorf("the new store is dirty:\n%s", status)
	}
	if n := gitLog(t, storeDir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("the new store has %s commits, want 1", n)
	}
	if _, err := store.Open(storeDir); err != nil {
		t.Errorf("store.Open on the v3 store: %v", err)
	}

	// The repo has the marker, uncommitted: init commits nothing in it.
	m := projMarker(t, repo)
	if m.Project != "myrepo" || m.Kind != project.KindProject || m.Store != "" || len(m.Repos) != 0 {
		t.Errorf("jig.yaml = %+v, want project myrepo, kind project, no store, no repos", m)
	}
	if status := gitLog(t, repo, "status", "--porcelain"); !strings.Contains(status, "?? jig.yaml") {
		t.Errorf("git status in the repo = %q, want jig.yaml untracked and nothing else of init's", status)
	}

	machine, err := project.LoadMachine(env.home)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	mp := machine["myrepo"]
	sameDir(t, "machine map store", mp.Store, storeDir)
	sameDir(t, "machine map root", mp.Root, repo)
	sameDir(t, "machine map clone", mp.Clones["myrepo"], repo)

	// Usable now, with no remote: here names it, ticket new mints under the
	// key derived from the name.
	got := hereJSON(t, env, repo)
	if got.How != "marker" || got.Project != "myrepo" || got.Kind != "project" || got.Repo.Name != "myrepo" || got.Mapped {
		t.Errorf("jig here: how %q, project %q, kind %q, repo %+v, mapped %v; want marker, myrepo, project, myrepo, already mapped", got.How, got.Project, got.Kind, got.Repo, got.Mapped)
	}
	sameDir(t, "here store.path", got.Store.Path, storeDir)
	if got.Store.Remote != "" {
		t.Errorf("here store.remote = %q, want none: nothing is published", got.Store.Remote)
	}
	r = projRun(t, env, repo, "ticket", "new", "--title", "Fix the thing")
	if !strings.Contains(r.Stdout, "MYREPO-1") {
		t.Errorf("jig ticket new stdout lacks MYREPO-1:\n%s", r.Stdout)
	}
	if got := hereJSON(t, env, repo, "MYREPO-1"); got.Ticket.ID != "MYREPO-1" {
		t.Errorf("jig here MYREPO-1: ticket %+v", got.Ticket)
	}
	if r := projRun(t, env, repo, "repos"); !strings.Contains(r.Stdout, "myrepo,.,,checked out") {
		t.Errorf("jig repos lacks the one repo, checked out:\n%s", r.Stdout)
	}

	// A second init is refused and changes nothing.
	r = runJig(t, env, repo, "init")
	projRefusal(t, "second jig init", r, 2, "already initialized")
	if n := gitLog(t, storeDir, "rev-list", "--count", "HEAD"); n == "1" {
		// (the ticket's claim is the second commit)
		t.Errorf("the store has no commit for the ticket minted before the second init")
	}
}

// TestInitStandaloneFlags: --standalone forces the single-repo case in a folder
// inference would read as an orchestrator (a repo that vendors one), --key
// names the area key, and a plain folder that is no git repository is a project
// too.
func TestInitStandaloneFlags(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	parent := t.TempDir()
	repo := projGitRepo(t, filepath.Join(parent, "vendor-host"))
	projGitRepo(t, filepath.Join(repo, "third_party"))

	r := projRun(t, env, repo, "init", "--standalone", "--key", "VH")
	if strings.Contains(r.Stdout, "picked:") {
		t.Errorf("jig init --standalone says it picked; the flag was the pick:\n%s", r.Stdout)
	}
	if m := projMarker(t, repo); m.Kind != project.KindProject || len(m.Repos) != 0 {
		t.Errorf("jig.yaml = %+v, want a single-repo project listing no repos", m)
	}
	cfg, err := project.LoadStoreConfig(filepath.Join(parent, "vendor-host-tickets"))
	if err != nil {
		t.Fatalf("LoadStoreConfig: %v", err)
	}
	if _, ok := cfg.Keys["VH"]; !ok || len(cfg.Keys) != 1 {
		t.Errorf("keys = %v, want exactly VH", cfg.Keys)
	}

	// Both flags are two answers to one question; a repo is not a single-repo
	// project's business.
	other := projGitRepo(t, filepath.Join(parent, "other"))
	projRefusal(t, "init with both flags", runJig(t, env, other, "init", "--standalone", "--orchestrator"), 2, "pick one")
	projRefusal(t, "init --standalone with a repo", runJig(t, env, other, "init", "--standalone", "somewhere"), 2, "takes no repos")
	projRefusal(t, "init with a malformed key", runJig(t, env, other, "init", "--key", "lowercase"), 2, "--key")
	if _, err := os.Stat(filepath.Join(parent, "other-tickets")); !os.IsNotExist(err) {
		t.Errorf("a refused init left a store behind: %v", err)
	}
}

// TestInitOrchestratorTwoNested: a plain folder holding two repos is an
// orchestrator. Without --yes init will not run git init there (stdin is no
// terminal) and writes nothing; with it, the folder becomes a repository, the
// project lists both repos, and the store, the marker and the machine map are
// as for any project. Local, and usable: ticket new works from the orchestrator
// and from a nested repo, and here names the repo.
func TestInitOrchestratorTwoNested(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	parent := t.TempDir()
	shop := filepath.Join(parent, "shop")
	api := projGitRepo(t, filepath.Join(shop, "api"))
	web := projGitRepo(t, filepath.Join(shop, "web"))
	storeDir := filepath.Join(parent, "shop-tickets")

	// Inferred, and not yet a git repository: needs the person's yes.
	r := runJig(t, env, shop, "init")
	projRefusal(t, "jig init in a plain folder", r, 2, "picked:\n  mode: orchestrator\n", "nested repo(s): api, web", "NEEDS_INPUT", "--yes")
	for _, path := range []string{filepath.Join(shop, ".git"), filepath.Join(shop, "jig.yaml"), storeDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the declined init left %s behind: %v", path, err)
		}
	}

	r = projRun(t, env, shop, "init", "--yes")
	for _, want := range []string{"mode: orchestrator", "project: shop", "git: initialized", "api,api,nested,checked out", "web,web,nested,checked out"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig init --yes stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(shop, ".git")); err != nil {
		t.Errorf("the orchestrator is not a git repository: %v", err)
	}
	m := projMarker(t, shop)
	wantRepos := []project.MarkerRepo{{Name: "api", Path: "api"}, {Name: "web", Path: "web"}}
	if m.Project != "shop" || m.Kind != project.KindOrchestrator || m.Store != "" || fmt.Sprint(m.Repos) != fmt.Sprint(wantRepos) {
		t.Errorf("jig.yaml = %+v, want project shop, kind orchestrator, no store, repos %+v", m, wantRepos)
	}
	if _, err := os.Stat(filepath.Join(storeDir, "store.yaml")); err != nil {
		t.Fatalf("store.yaml missing: %v", err)
	}
	machine, err := project.LoadMachine(env.home)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	mp := machine["shop"]
	sameDir(t, "machine map store", mp.Store, storeDir)
	sameDir(t, "machine map root", mp.Root, shop)
	sameDir(t, "machine map clone api", mp.Clones["api"], api)
	sameDir(t, "machine map clone web", mp.Clones["web"], web)

	// here: from the orchestrator itself, and from inside each nested repo.
	top := hereJSON(t, env, shop)
	if top.How != "marker" || top.Project != "shop" || top.Kind != "orchestrator" || top.Repo.Name != "" {
		t.Errorf("jig here in the orchestrator: how %q, project %q, kind %q, repo %+v; want marker, shop, orchestrator, in none of its repos", top.How, top.Project, top.Kind, top.Repo)
	}
	inApi := hereJSON(t, env, filepath.Join(api))
	if inApi.Project != "shop" || inApi.Kind != "orchestrator" || inApi.Repo.Name != "api" {
		t.Errorf("jig here in api: project %q, kind %q, repo %+v; want shop, orchestrator, api", inApi.Project, inApi.Kind, inApi.Repo)
	}
	sameDir(t, "here in web: store.path", hereJSON(t, env, web).Store.Path, storeDir)

	// Usable with no remote: tickets mint from either place, in the one store.
	r = projRun(t, env, shop, "ticket", "new", "--title", "Cross-repo thing")
	if !strings.Contains(r.Stdout, "SHOP-1") {
		t.Errorf("jig ticket new stdout lacks SHOP-1:\n%s", r.Stdout)
	}
	r = projRun(t, env, web, "ticket", "new", "--title", "Web thing")
	if !strings.Contains(r.Stdout, "SHOP-2") {
		t.Errorf("jig ticket new from web: stdout lacks SHOP-2:\n%s", r.Stdout)
	}

	r = projRun(t, env, shop, "repos")
	for _, want := range []string{"kind: orchestrator", "api,api,", "web,web,", "checked out"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig repos stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if strings.Contains(r.Stdout, "unlisted") {
		t.Errorf("jig repos lists unlisted repos, but both are listed:\n%s", r.Stdout)
	}
}

// TestInitOrchestratorFetchesURLs: a URL given to init is fetched into the
// orchestrator, as a submodule written with ignore = all by default, and as a
// gitignored clone with --clone (whose remote then goes in jig.yaml, since a
// clone git ignores is not something another checkout would have). A repo
// outside the orchestrator and one deeper than a direct child are refused.
func TestInitOrchestratorFetchesURLs(t *testing.T) {
	t.Parallel()
	src := projGitRepo(t, filepath.Join(t.TempDir(), "lib"))
	libRemote := filepath.Join(t.TempDir(), "lib.git")
	gitLog(t, filepath.Dir(libRemote), "clone", "--bare", src, libRemote)
	url := projFileURL(libRemote)

	t.Run("submodule", func(t *testing.T) {
		t.Parallel()
		env := projEnv(t)
		parent := t.TempDir()
		hub := filepath.Join(parent, "hub")
		if err := os.MkdirAll(hub, 0o755); err != nil {
			t.Fatal(err)
		}
		r := projRun(t, env, hub, "init", "--orchestrator", "--yes", url)
		if !strings.Contains(r.Stdout, "lib,lib,submodule,checked out") {
			t.Errorf("jig init stdout lacks the submodule row:\n%s", r.Stdout)
		}
		gitmodules := string(readFileOrFatal(t, filepath.Join(hub, ".gitmodules")))
		if !strings.Contains(gitmodules, "path = lib") || !strings.Contains(gitmodules, "ignore = all") {
			t.Errorf(".gitmodules = %q, want the lib submodule written with ignore = all", gitmodules)
		}
		if m := projMarker(t, hub); len(m.Repos) != 1 || m.Repos[0].Name != "lib" || m.Repos[0].Remote != "" {
			t.Errorf("jig.yaml repos = %+v, want lib with no remote: .gitmodules carries it", m.Repos)
		}
		// Committing nothing in the orchestrator: the submodule and
		// .gitmodules are staged for the person, jig.yaml untracked.
		if status := gitLog(t, hub, "status", "--porcelain"); !strings.Contains(status, "A  .gitmodules") || !strings.Contains(status, "?? jig.yaml") {
			t.Errorf("git status in the orchestrator:\n%s", status)
		}
		if got := hereJSON(t, env, filepath.Join(hub, "lib")); got.Repo.Name != "lib" || got.Project != "hub" {
			t.Errorf("jig here in lib: project %q, repo %+v", got.Project, got.Repo)
		}
	})

	t.Run("clone", func(t *testing.T) {
		t.Parallel()
		env := projEnv(t)
		hub := filepath.Join(t.TempDir(), "hub")
		if err := os.MkdirAll(hub, 0o755); err != nil {
			t.Fatal(err)
		}
		r := projRun(t, env, hub, "init", "--orchestrator", "--yes", "--clone", url)
		if !strings.Contains(r.Stdout, "lib,lib,fetched,checked out") {
			t.Errorf("jig init --clone stdout lacks the fetched row:\n%s", r.Stdout)
		}
		if _, err := os.Stat(filepath.Join(hub, ".gitmodules")); !os.IsNotExist(err) {
			t.Errorf("--clone wrote a .gitmodules: %v", err)
		}
		if ignore := string(readFileOrFatal(t, filepath.Join(hub, ".gitignore"))); !strings.Contains(ignore, "/lib/") {
			t.Errorf(".gitignore = %q, want /lib/", ignore)
		}
		if m := projMarker(t, hub); len(m.Repos) != 1 || m.Repos[0].Remote != url {
			t.Errorf("jig.yaml repos = %+v, want lib with the URL as its remote", m.Repos)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		env := projEnv(t)
		parent := t.TempDir()
		hub := filepath.Join(parent, "hub")
		projGitRepo(t, filepath.Join(hub, "group", "deep"))
		outside := projGitRepo(t, filepath.Join(parent, "outside"))
		projRefusal(t, "init with a repo outside the orchestrator", runJig(t, env, hub, "init", "--orchestrator", "--yes", outside), 2, "not inside the orchestrator")
		projRefusal(t, "init with a repo too deep", runJig(t, env, hub, "init", "--orchestrator", "--yes", "group/deep"), 2, "not a direct child", "jig repos add group/deep")
		projRefusal(t, "init with a folder that is no repo", runJig(t, env, hub, "init", "--orchestrator", "--yes", "group"), 2, "is not a repository")
		for _, path := range []string{filepath.Join(hub, "jig.yaml"), filepath.Join(parent, "hub-tickets")} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("a refused init left %s behind: %v", path, err)
			}
		}
	})
}

// TestInitRefusesOutside: init defines a project and runs only at a project's
// root. It refuses in a repo nested in a project (pointing at jig repos add),
// in a folder below the top of its repo, in a project already, and in a store,
// and each time writes nothing.
func TestInitRefusesOutside(t *testing.T) {
	t.Parallel()

	t.Run("a repo nested in a project", func(t *testing.T) {
		t.Parallel()
		env := projEnv(t)
		parent := t.TempDir()
		shop := filepath.Join(parent, "shop")
		api := projGitRepo(t, filepath.Join(shop, "api"))
		projRun(t, env, shop, "init", "--orchestrator", "--yes")
		r := runJig(t, env, api, "init", "--standalone")
		projRefusal(t, "init in a nested repo", r, 2, "inside the jig project at", "jig repos add", "attach --tracked --members")
		if _, err := os.Stat(filepath.Join(shop, "api-tickets")); !os.IsNotExist(err) {
			t.Errorf("the refused init made a store: %v", err)
		}
		if _, err := os.Stat(filepath.Join(api, "jig.yaml")); !os.IsNotExist(err) {
			t.Errorf("the refused init wrote a marker in the nested repo: %v", err)
		}
	})

	t.Run("below the top of a repo", func(t *testing.T) {
		t.Parallel()
		env := projEnv(t)
		parent := t.TempDir()
		repo := projGitRepo(t, filepath.Join(parent, "repo"))
		sub := filepath.Join(repo, "pkg")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		r := runJig(t, env, sub, "init")
		projRefusal(t, "init in a subfolder of a repo", r, 2, "below its top", "Run `jig init` in ")
		if _, err := os.Stat(filepath.Join(sub, "jig.yaml")); !os.IsNotExist(err) {
			t.Errorf("the refused init wrote a marker: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "pkg-tickets")); !os.IsNotExist(err) {
			t.Errorf("the refused init made a store: %v", err)
		}
	})

	t.Run("a store", func(t *testing.T) {
		t.Parallel()
		env := projEnv(t)
		parent := t.TempDir()
		repo := projGitRepo(t, filepath.Join(parent, "repo"))
		projRun(t, env, repo, "init")
		r := runJig(t, env, filepath.Join(parent, "repo-tickets"), "init")
		projRefusal(t, "init in a store", r, 2, "is a jig store")
	})

	t.Run("the project's root again", func(t *testing.T) {
		t.Parallel()
		env := projEnv(t)
		repo := projGitRepo(t, filepath.Join(t.TempDir(), "repo"))
		projRun(t, env, repo, "init", "--standalone")
		projRefusal(t, "init twice", runJig(t, env, repo, "init", "--standalone"), 2, "already initialized")
	})
}

// TestInitStandaloneQuickstart drives the README Quickstart's exact command
// sequence (init --standalone, ticket new, validate) against a plain repo
// directory, deliberately without fixture.Generate: Generate performs its
// own project.InitProject call to write the machine mapping, which would
// mask a regression in init --standalone's own wiring. brief.md and
// slices.yaml are written by hand here, the way the intake skill would
// leave them, so this exercises the real path `jig validate` takes.
func TestInitStandaloneQuickstart(t *testing.T) {
	t.Parallel()
	env := jigEnv{home: t.TempDir()}

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	r := runJig(t, env, repoDir, "init", "--standalone")
	if r.Code != 0 {
		t.Fatalf("jig init --standalone exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	r = runJig(t, env, repoDir, "ticket", "new", "--title", "Fix the thing")
	if r.Code != 0 {
		t.Fatalf("jig ticket new exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	const ticket = "MYREPO-1" // first ticket minted into a fresh store, under its derived key (from repoDir's base name "myrepo")
	if !strings.Contains(r.Stdout, ticket) {
		t.Fatalf("jig ticket new stdout missing %q:\n%s", ticket, r.Stdout)
	}

	storeDir := filepath.Join(parent, "myrepo-tickets")
	ticketDir := filepath.Join(storeDir, "tickets", ticket)
	brief := "# Fix the thing\n\n## Goal\n\nMake the thing work.\n"
	hashes := store.BriefSectionHashes([]byte(brief))
	slices := fmt.Sprintf(`slices:
  - id: a
    workspace: root
    goal: Make the thing work.
    oracle: "true"
    blocked_by: []
    from_brief: ["%s"]
`, hashes["Goal"])
	if err := os.WriteFile(filepath.Join(ticketDir, "brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ticketDir, "slices.yaml"), []byte(slices), 0o644); err != nil {
		t.Fatalf("write slices.yaml: %v", err)
	}

	r = runJig(t, env, repoDir, "validate", ticket)
	if r.Code != 0 {
		t.Fatalf("jig validate %s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", ticket, r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("jig validate stdout missing \"valid: yes\":\n%s", r.Stdout)
	}
}
