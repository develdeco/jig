package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
)

// TestShareThenHereFromFreshClone: a project is local until it is shared, and
// sharing is the push. share asks first (a push is not undone), then sets the
// remotes it is given, writes the store's remote into jig.yaml and commits it,
// and pushes the store and the repo. A fresh clone of the repo, on a machine
// with an empty machine map, then finds the project from jig.yaml alone: here
// names the store's remote and the command to get it, and once the store is
// cloned beside the repo every verb works.
func TestShareThenHereFromFreshClone(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	parent := t.TempDir()
	repo := projGitRepo(t, filepath.Join(parent, "myrepo"))
	projRun(t, env, repo, "init")
	projRun(t, env, repo, "ticket", "new", "--title", "Fix the thing")
	storeRemote := projBareRemote(t, "myrepo-tickets.git")
	repoRemote := projBareRemote(t, "myrepo.git")

	// Nothing to push to, and nothing guessed.
	projRefusal(t, "share with no remote", runJig(t, env, repo, "share"), 2, "nothing to push to", "--store-remote <url>", "--remote myrepo=<url>", "--create")
	// Asked, not told: without --yes (and no terminal) nothing is pushed.
	r := runJig(t, env, repo, "share", "--store-remote", storeRemote, "--remote", "myrepo="+repoRemote)
	projRefusal(t, "share without --yes", r, 2, "NEEDS_INPUT", "--yes")
	if refs := gitLog(t, repoRemote, "for-each-ref"); refs != "" {
		t.Errorf("share pushed the repo before it was told to:\n%s", refs)
	}
	if got := gitLog(t, repo, "remote"); got != "" {
		t.Errorf("share set remotes %q before it was told to", got)
	}
	// A remote name the project does not have.
	projRefusal(t, "share with an unknown repo", runJig(t, env, repo, "share", "--store-remote", storeRemote, "--remote", "nope="+repoRemote, "--yes"), 2, "names no repo of the project: nope")

	r = projRun(t, env, repo, "share", "--store-remote", storeRemote, "--remote", "myrepo="+repoRemote, "--yes")
	for _, want := range []string{"share:\n  project: myrepo\n", "store_remote: " + axi.Quote(storeRemote), "marker_committed: yes", "pushed[2]{repo,branch}:\n  store,main\n  myrepo,main\n", "remotes_set[2]{repo,remote}:"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig share stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if m := projMarker(t, repo); m.Store != storeRemote {
		t.Errorf("jig.yaml store = %q, want %q", m.Store, storeRemote)
	}
	if got := gitLog(t, repo, "log", "-1", "--format=%s"); got != "jig: record the project's store in jig.yaml" {
		t.Errorf("the repo's last commit = %q, want jig's marker commit", got)
	}
	if got := gitLog(t, repo, "log", "-1", "--format=%an|%cn"); got != "jig-fixture|jig-fixture" {
		t.Errorf("the marker commit is authored %q, want the person (the pinned identity here)", got)
	}
	if status := gitLog(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("the repo is dirty after share:\n%s", status)
	}
	if got := gitLog(t, repo, "rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
		t.Errorf("main's upstream = %q, want origin/main", got)
	}
	if got := gitLog(t, repoRemote, "show", "main:jig.yaml"); !strings.Contains(got, "store: "+storeRemote) {
		t.Errorf("the pushed jig.yaml lacks the store's remote:\n%s", got)
	}
	storeFiles := gitLog(t, storeRemote, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"store.yaml", "tickets/MYREPO-1/ticket.yaml"} {
		if !strings.Contains(storeFiles, want) {
			t.Errorf("the pushed store lacks %s:\n%s", want, storeFiles)
		}
	}
	// Sharing again changes nothing.
	r = projRun(t, env, repo, "share", "--yes")
	if !strings.Contains(r.Stdout, "marker_committed: no") {
		t.Errorf("a second share committed again:\n%s", r.Stdout)
	}

	// A fresh clone, an empty machine map.
	env2 := projEnv(t)
	fresh := filepath.Join(t.TempDir(), "fresh")
	gitLog(t, filepath.Dir(fresh), "clone", repoRemote, fresh)
	got := hereJSON(t, env2, fresh)
	if got.How != "marker" || got.Project != "myrepo" || got.Kind != "project" || got.Store.Path != "" || got.Store.Remote != storeRemote || got.Mapped {
		t.Errorf("here in the fresh clone: how %q, project %q, kind %q, store %+v, mapped %v; want marker, myrepo, project, only the store's remote, not mapped", got.How, got.Project, got.Kind, got.Store, got.Mapped)
	}
	sibling := filepath.Join(filepath.Dir(fresh), "myrepo-tickets")
	if r := runJig(t, env2, fresh, "here"); !strings.Contains(r.Stdout, "git clone "+storeRemote+" ") || !strings.Contains(r.Stdout, "myrepo-tickets") {
		t.Errorf("jig here in the fresh clone lacks the command that gets the store:\n%s", r.Stdout)
	}
	projRefusal(t, "ticket new before the store is cloned", runJig(t, env2, fresh, "ticket", "new", "--title", "x"), 2, "not cloned on this machine")

	gitLog(t, filepath.Dir(fresh), "clone", storeRemote, sibling)
	got = hereJSON(t, env2, fresh)
	if got.How != "marker" || got.Project != "myrepo" || got.Repo.Name != "myrepo" || !got.Mapped {
		t.Errorf("here with the store cloned: how %q, project %q, repo %+v, mapped %v; want marker, myrepo, myrepo, mapped", got.How, got.Project, got.Repo, got.Mapped)
	}
	sameDir(t, "store.path", got.Store.Path, sibling)
	r = projRun(t, env2, fresh, "ticket", "new", "--title", "Second thing")
	if !strings.Contains(r.Stdout, "MYREPO-2") {
		t.Errorf("jig ticket new in the fresh clone: want MYREPO-2 after the shared MYREPO-1:\n%s", r.Stdout)
	}
	if got := hereJSON(t, env2, fresh, "MYREPO-1"); got.Ticket.ID != "MYREPO-1" {
		t.Errorf("the shared ticket is not there: %+v", got.Ticket)
	}
}

// TestShareOrchestratorReportsReposWithoutARemote: share sets the remote of each
// repo it is told of, pushes the store and the orchestrator (and a nested repo
// it gave a remote), writes into jig.yaml the remote of a nested clone that no
// .gitmodules carries (a collaborator's checkout has no other way to learn it),
// and reports the nested repos that still have no remote, which collaborators
// cannot get.
func TestShareOrchestratorReportsReposWithoutARemote(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	shop := filepath.Join(t.TempDir(), "shop")
	api := projGitRepo(t, filepath.Join(shop, "api"))
	web := projGitRepo(t, filepath.Join(shop, "web"))
	projRun(t, env, shop, "init", "--orchestrator", "--yes")
	storeRemote := projBareRemote(t, "shop-tickets.git")
	shopRemote := projBareRemote(t, "shop.git")
	apiRemote := projBareRemote(t, "api.git")

	r := projRun(t, env, shop, "share", "--store-remote", storeRemote, "--remote", "shop="+shopRemote, "--remote", "api="+apiRemote, "--yes")
	for _, want := range []string{"without_remote[1]{repo}:\n  web\n", "pushed[3]{repo,branch}:\n  store,main\n  api,main\n  shop,main\n", "pass --remote <name>=<url>"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig share stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	m := projMarker(t, shop)
	if m.Store != storeRemote {
		t.Errorf("jig.yaml store = %q, want %q", m.Store, storeRemote)
	}
	if len(m.Repos) != 2 || m.Repos[0].Remote != apiRemote || m.Repos[1].Remote != "" {
		t.Errorf("jig.yaml repos = %+v, want api with its new remote and web with none", m.Repos)
	}
	if got := gitLog(t, api, "remote", "get-url", "origin"); got != apiRemote {
		t.Errorf("api's origin = %q, want %q", got, apiRemote)
	}
	if got := gitLog(t, web, "remote"); got != "" {
		t.Errorf("web got remotes %q it was not given", got)
	}
	if refs := gitLog(t, apiRemote, "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main" {
		t.Errorf("api's remote refs = %q, want main pushed", refs)
	}
	if got := gitLog(t, shopRemote, "show", "main:jig.yaml"); !strings.Contains(got, "remote: "+apiRemote) || !strings.Contains(got, "store: "+storeRemote) {
		t.Errorf("the pushed jig.yaml lacks the store or api's remote:\n%s", got)
	}

	// A fresh clone of the orchestrator learns api's URL from jig.yaml.
	if got := repoNames(projMarker(t, shop).Repos); got != "api,web" {
		t.Errorf("repos = %s", got)
	}
	r = projRun(t, env, shop, "repos")
	if !strings.Contains(r.Stdout, "api,api,"+axi.Quote(apiRemote)+",checked out") {
		t.Errorf("jig repos lacks api's remote:\n%s", r.Stdout)
	}
}

// TestShareCreatesThroughTheHostCLI: --create makes each repository that has no
// remote through gh, private, with the remote gh adds; the fake gh here adds
// the remote of a bare repository the test made, and the push reaches it.
func TestShareCreatesThroughTheHostCLI(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	parent := t.TempDir()
	repo := projGitRepo(t, filepath.Join(parent, "myrepo"))
	projRun(t, env, repo, "init")
	storeRemote := projBareRemote(t, "myrepo-tickets.git")
	repoRemote := projBareRemote(t, "myrepo.git")

	stubDir := fixture.GhStub(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	hosted := env.with(
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_LOG="+log,
		fmt.Sprintf(`GH_STUB_REPO_URLS={"myrepo-tickets":%q,"myrepo":%q}`, storeRemote, repoRemote),
	)

	projRun(t, hosted, repo, "share", "--create", "--yes")
	if got := gitLog(t, repo, "remote", "get-url", "origin"); got != repoRemote {
		t.Errorf("the repo's origin = %q, want the one gh made, %q", got, repoRemote)
	}
	if got := gitLog(t, repo+"-tickets", "remote", "get-url", "origin"); got != storeRemote {
		t.Errorf("the store's origin = %q, want the one gh made, %q", got, storeRemote)
	}
	for _, bare := range []string{storeRemote, repoRemote} {
		if refs := gitLog(t, bare, "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main" {
			t.Errorf("%s has refs %q, want main pushed", bare, refs)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read the gh log: %v", err)
	}
	for _, want := range []string{`"repo","create","myrepo-tickets","--private"`, `"repo","create","myrepo","--private"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("gh was not run as %s:\n%s", want, data)
		}
	}
	if m := projMarker(t, repo); m.Store != storeRemote {
		t.Errorf("jig.yaml store = %q, want %q", m.Store, storeRemote)
	}
}
