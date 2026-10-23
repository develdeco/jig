package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sharedProject makes a single-repo project in a git repo with one commit, and
// returns its options for share with no remote asked for.
func sharedProject(t *testing.T, name string) (ShareOpts, InitResult) {
	t.Helper()
	_, root := newRepoDir(t, name)
	runGitInProject(t, root, "init", "-b", "main")
	writeFile(t, filepath.Join(root, "README.md"), "# "+name+"\n")
	runGitInProject(t, root, "add", "-A")
	runGitInProject(t, root, "commit", "-m", "init")
	res, err := Init(InitOpts{Dir: root, JigHome: t.TempDir(), Kind: KindProject})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, err := LoadMarker(filepath.Join(root, MarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	return ShareOpts{Root: root, Marker: m, StoreDir: res.Store}, res
}

// bareRemote makes a bare repository and returns its path with forward slashes.
func bareRemote(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	runGitInProject(t, filepath.Dir(dir), "init", "--bare", "-b", "main", dir)
	return filepath.ToSlash(dir)
}

func TestPlanShareRefusesWhatCannotBeShared(t *testing.T) {
	t.Run("no remote anywhere", func(t *testing.T) {
		o, _ := sharedProject(t, "demo")
		_, err := PlanShare(o)
		wantValidationError(t, err)
		for _, want := range []string{"the store has no remote: pass --store-remote <url>", "the project's repo demo has no remote: pass --remote demo=<url>"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want it to say %q", err, want)
			}
		}
		if h := strings.Join(helpOf(err), "\n"); !strings.Contains(h, "--create") {
			t.Errorf("help = %q, want it to offer --create", h)
		}
	})

	t.Run("a different origin than the one given", func(t *testing.T) {
		o, _ := sharedProject(t, "demo")
		runGitInProject(t, o.StoreDir, "remote", "add", "origin", "https://example.invalid/org/demo-tickets.git")
		o.StoreRemote = "https://example.invalid/org/other.git"
		o.Remotes = map[string]string{"demo": bareRemote(t, "demo.git")}
		_, err := PlanShare(o)
		wantValidationError(t, err)
		if !strings.Contains(err.Error(), "already has the origin https://example.invalid/org/demo-tickets.git") {
			t.Errorf("err = %v", err)
		}
		// The same origin, spelled with .git or without, is the same.
		o.StoreRemote = "https://example.invalid/org/demo-tickets"
		if _, err := PlanShare(o); err != nil {
			t.Errorf("PlanShare with the origin it has, spelled another way: %v", err)
		}
	})

	t.Run("a remote for a repo the project does not have", func(t *testing.T) {
		o, _ := sharedProject(t, "demo")
		o.StoreRemote = bareRemote(t, "s.git")
		o.Remotes = map[string]string{"demo": bareRemote(t, "d.git"), "ghost": "x"}
		_, err := PlanShare(o)
		wantValidationError(t, err)
		if !strings.Contains(err.Error(), "names no repo of the project: ghost") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a folder that is no repository", func(t *testing.T) {
		_, root := newRepoDir(t, "plain")
		res, err := Init(InitOpts{Dir: root, JigHome: t.TempDir(), Kind: KindProject})
		if err != nil {
			t.Fatal(err)
		}
		m, _ := LoadMarker(filepath.Join(root, MarkerFile))
		_, err = PlanShare(ShareOpts{Root: root, Marker: m, StoreDir: res.Store, StoreRemote: bareRemote(t, "s.git"), Remotes: map[string]string{"plain": "x"}})
		wantValidationError(t, err)
		if !strings.Contains(err.Error(), "is not a git repository") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestPlanShareSaysWhatItWillDo(t *testing.T) {
	o, _ := sharedProject(t, "demo")
	o.StoreRemote = bareRemote(t, "demo-tickets.git")
	o.Create = true // the project's own repo has no remote and was given none
	plan, err := PlanShare(o)
	if err != nil {
		t.Fatalf("PlanShare: %v", err)
	}
	if len(plan.Targets) != 2 || plan.Targets[0].Name != "store" || plan.Targets[1].Name != "demo" {
		t.Fatalf("targets = %+v, want the store first, the project's repo last", plan.Targets)
	}
	if plan.Targets[0].Set != o.StoreRemote || plan.Targets[0].Create || !plan.Targets[0].Push {
		t.Errorf("store target = %+v, want its remote set, pushed", plan.Targets[0])
	}
	if !plan.Targets[1].Create || plan.Targets[1].Set != "" || !plan.Targets[1].Push {
		t.Errorf("repo target = %+v, want it created, pushed", plan.Targets[1])
	}
	summary := strings.Join(plan.Summary(), "\n")
	for _, want := range []string{"make " + o.StoreRemote + " the origin of the store", "create a private repository \"demo\"", "push main of the store", "push main of the demo", "write the store's remote into jig.yaml"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary lacks %q:\n%s", want, summary)
		}
	}
}

func TestRunShareSetsRemotesPushesAndRecordsTheStore(t *testing.T) {
	o, res := sharedProject(t, "demo")
	storeRemote, repoRemote := bareRemote(t, "demo-tickets.git"), bareRemote(t, "demo.git")
	o.StoreRemote = storeRemote
	o.Remotes = map[string]string{"demo": repoRemote}
	plan, err := PlanShare(o)
	if err != nil {
		t.Fatalf("PlanShare: %v", err)
	}
	got, err := RunShare(plan, nil)
	if err != nil {
		t.Fatalf("RunShare: %v", err)
	}
	if got.StoreRemote != storeRemote || !got.Committed || len(got.Pushed) != 2 || got.Pushed[0] != "store main" || got.Pushed[1] != "demo main" {
		t.Errorf("result = %+v, want the store remote, jig.yaml committed, store then repo pushed", got)
	}
	if got.Remotes["store"] != storeRemote || got.Remotes["demo"] != repoRemote {
		t.Errorf("remotes = %v", got.Remotes)
	}
	m, _ := LoadMarker(filepath.Join(o.Root, MarkerFile))
	if m.Store != storeRemote {
		t.Errorf("marker store = %q", m.Store)
	}
	if shown := runGitInProject(t, repoRemote, "show", "main:jig.yaml"); !strings.Contains(shown, "store: "+storeRemote) {
		t.Errorf("the pushed jig.yaml:\n%s", shown)
	}
	if files := runGitInProject(t, storeRemote, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(files, "store.yaml") {
		t.Errorf("the pushed store:\n%s", files)
	}
	if got := runGitInProject(t, o.Root, "log", "-1", "--format=%s"); got != "jig: record the project's store in jig.yaml" {
		t.Errorf("commit message = %q", got)
	}
	if got := runGitInProject(t, res.Store, "rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
		t.Errorf("the store's upstream = %q", got)
	}

	// Run again from the project as it is: nothing new to commit, the same push.
	plan, err = PlanShare(ShareOpts{Root: o.Root, Marker: m, StoreDir: res.Store})
	if err != nil {
		t.Fatalf("second PlanShare: %v", err)
	}
	again, err := RunShare(plan, nil)
	if err != nil || again.Committed {
		t.Errorf("second RunShare = %+v, %v; want no new commit", again, err)
	}
}

func TestRunShareCreatesThroughTheHost(t *testing.T) {
	o, _ := sharedProject(t, "demo")
	hosted := map[string]string{"demo-tickets": bareRemote(t, "ht.git"), "demo": bareRemote(t, "hd.git")}
	o.Create = true
	plan, err := PlanShare(o)
	if err != nil {
		t.Fatalf("PlanShare: %v", err)
	}
	var made []string
	got, err := RunShare(plan, func(name, dir string) error {
		made = append(made, name)
		runGitInProject(t, dir, "remote", "add", "origin", hosted[name])
		return nil
	})
	if err != nil {
		t.Fatalf("RunShare: %v", err)
	}
	if strings.Join(made, ",") != "demo-tickets,demo" {
		t.Errorf("repositories created = %v, want the store's, then the project's", made)
	}
	if got.StoreRemote != hosted["demo-tickets"] || got.Remotes["demo"] != hosted["demo"] {
		t.Errorf("result = %+v", got)
	}

	// Without a host to ask, --create is refused instead of ignored.
	o2, _ := sharedProject(t, "other")
	o2.Create = true
	plan2, err := PlanShare(o2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunShare(plan2, nil); err == nil {
		t.Error("RunShare with --create and no host: nil error")
	}
}

// A nested repo that is a submodule travels with the orchestrator; one that is
// a plain clone needs its remote in jig.yaml.
func TestRunShareRecordsTheRemoteOfANestedCloneOnly(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "shop")
	runGitDir(t, filepath.Join(root, "api"))
	runGitDir(t, filepath.Join(root, "web"))
	res, err := Init(InitOpts{Dir: root, JigHome: t.TempDir(), Kind: KindOrchestrator, MakeRepo: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, d := range []string{filepath.Join(root, "api"), filepath.Join(root, "web")} {
		writeFile(t, filepath.Join(d, "f.txt"), "x\n")
		runGitInProject(t, d, "add", "-A")
		runGitInProject(t, d, "commit", "-m", "first")
	}
	// web is declared as a submodule (as `jig init <url>` would leave it).
	writeFile(t, filepath.Join(root, ".gitmodules"), "[submodule \"web\"]\n\tpath = web\n\turl = placeholder\n")
	m, _ := LoadMarker(filepath.Join(root, MarkerFile))
	apiRemote, webRemote := bareRemote(t, "api.git"), bareRemote(t, "web.git")
	plan, err := PlanShare(ShareOpts{
		Root: root, Marker: m, StoreDir: res.Store,
		StoreRemote: bareRemote(t, "shop-tickets.git"),
		Remotes:     map[string]string{"shop": bareRemote(t, "shop.git"), "api": apiRemote, "web": webRemote},
	})
	if err != nil {
		t.Fatalf("PlanShare: %v", err)
	}
	if _, err := RunShare(plan, nil); err != nil {
		t.Fatalf("RunShare: %v", err)
	}
	m, _ = LoadMarker(filepath.Join(root, MarkerFile))
	if len(m.Repos) != 2 || m.Repos[0].Remote != apiRemote || m.Repos[1].Remote != "" {
		t.Errorf("marker repos = %+v, want api's remote recorded and the submodule web's left to .gitmodules", m.Repos)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitmodules")); err != nil {
		t.Errorf(".gitmodules: %v", err)
	}
}

// A submodule is checked out detached: share reads it, it does not push it, so
// it asks for no branch; a repo it is to push needs one.
func TestPlanShareAsksNoBranchOfANestedRepoItDoesNotPush(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "shop")
	api := runGitDir(t, filepath.Join(root, "api"))
	writeFile(t, filepath.Join(api, "f.txt"), "x\n")
	runGitInProject(t, api, "add", "-A")
	runGitInProject(t, api, "commit", "-m", "first")
	runGitInProject(t, api, "remote", "add", "origin", "https://example.invalid/org/api.git")
	runGitInProject(t, api, "checkout", "--detach")
	res, err := Init(InitOpts{Dir: root, JigHome: t.TempDir(), Kind: KindOrchestrator, MakeRepo: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, _ := LoadMarker(filepath.Join(root, MarkerFile))
	o := ShareOpts{Root: root, Marker: m, StoreDir: res.Store, StoreRemote: bareRemote(t, "s.git"), Remotes: map[string]string{"shop": bareRemote(t, "r.git")}}

	plan, err := PlanShare(o)
	if err != nil {
		t.Fatalf("PlanShare with a detached nested repo that has its remote: %v", err)
	}
	for _, tg := range plan.Targets {
		if tg.Name == "api" && tg.Push {
			t.Errorf("api is to be pushed: %+v", tg)
		}
	}

	// A nested repo that has no remote yet, given one, is pushed, so it needs a
	// branch.
	runGitInProject(t, api, "remote", "remove", "origin")
	o.Remotes["api"] = bareRemote(t, "a.git")
	_, err = PlanShare(o)
	wantValidationError(t, err)
	if !strings.Contains(err.Error(), "has no branch checked out") {
		t.Errorf("err = %v", err)
	}
}
