package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// TestReposSyncReportsAddedAndRemoved: sync makes the project's list match its
// folder. A repo that appeared nested in the orchestrator is added, one whose
// folder is gone is removed, both are reported, jig.yaml and the machine map
// follow, and a second sync has nothing to say.
func TestReposSyncReportsAddedAndRemoved(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	shop := filepath.Join(t.TempDir(), "shop")
	projGitRepo(t, filepath.Join(shop, "api"))
	web := projGitRepo(t, filepath.Join(shop, "web"))
	projRun(t, env, shop, "init", "--orchestrator", "--yes")

	docs := projGitRepo(t, filepath.Join(shop, "docs"))
	if err := os.RemoveAll(web); err != nil {
		t.Fatalf("remove web: %v", err)
	}

	// Before sync, repos shows what is unlisted.
	r := projRun(t, env, shop, "repos")
	if !strings.Contains(r.Stdout, "unlisted[1]{name,path}:\n  docs,docs\n") || !strings.Contains(r.Stdout, "web,web,,missing") {
		t.Errorf("jig repos before the sync does not show docs unlisted and web missing:\n%s", r.Stdout)
	}

	r = projRun(t, env, shop, "repos", "sync")
	for _, want := range []string{"added: 1\n", "removed: 1\n", "not_here: 0\n", "added[1]{name,path}:\n  docs,docs\n", "removed[1]{name,path}:\n  web,web\n", "Commit jig.yaml"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig repos sync stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	m := projMarker(t, shop)
	if got := repoNames(m.Repos); got != "api,docs" {
		t.Errorf("jig.yaml repos = %s, want api,docs (web removed, docs added after)", got)
	}
	machine, err := project.LoadMachine(env.home)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	clones := machine["shop"].Clones
	if _, ok := clones["web"]; ok || len(clones) != 2 {
		t.Errorf("machine map clones = %v, want api and docs only", clones)
	}
	sameDir(t, "machine map clone docs", clones["docs"], docs)

	r = projRun(t, env, shop, "repos")
	if strings.Contains(r.Stdout, "unlisted") || !strings.Contains(r.Stdout, "docs,docs,,checked out") {
		t.Errorf("jig repos after the sync:\n%s", r.Stdout)
	}
	r = projRun(t, env, shop, "repos", "sync")
	if !strings.Contains(r.Stdout, "added: 0\n") || !strings.Contains(r.Stdout, "removed: 0\n") || !strings.Contains(r.Stdout, "already matches") {
		t.Errorf("a second jig repos sync has something to report:\n%s", r.Stdout)
	}
	// And the list is the single source of what the orchestrator spans.
	if got := hereJSON(t, env, docs); got.Repo.Name != "docs" || got.Project != "shop" {
		t.Errorf("jig here in docs: project %q, repo %+v", got.Project, got.Repo)
	}
}

// TestReposSyncKeepsASubmoduleNotCheckedOutHere: a listed submodule whose
// folder is not fetched is part of the project, merely not checked out here:
// sync reports it and keeps it, with --yes and without (no terminal to ask on).
func TestReposSyncKeepsASubmoduleNotCheckedOutHere(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	src := projGitRepo(t, filepath.Join(t.TempDir(), "lib"))
	libRemote := filepath.Join(t.TempDir(), "lib.git")
	gitLog(t, filepath.Dir(libRemote), "clone", "--bare", src, libRemote)
	hub := filepath.Join(t.TempDir(), "hub")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	projRun(t, env, hub, "init", "--orchestrator", "--yes", projFileURL(libRemote))

	// A checkout that has not fetched the submodule: its folder is empty.
	lib := filepath.Join(hub, "lib")
	if err := os.RemoveAll(lib); err != nil {
		t.Fatalf("remove lib: %v", err)
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}

	if r := projRun(t, env, hub, "repos"); !strings.Contains(r.Stdout, "lib,lib,,not checked out here") {
		t.Errorf("jig repos does not say lib is not checked out here:\n%s", r.Stdout)
	}
	for _, args := range [][]string{{"repos", "sync", "--yes"}, {"repos", "sync"}} {
		r := projRun(t, env, hub, args...)
		for _, want := range []string{"removed: 0\n", "not_here: 1\n", "not_here[1]{name,path}:\n  lib,lib\n", "git submodule update --init"} {
			if !strings.Contains(r.Stdout, want) {
				t.Errorf("jig %s stdout lacks %q:\n%s", strings.Join(args, " "), want, r.Stdout)
			}
		}
		if got := repoNames(projMarker(t, hub).Repos); got != "lib" {
			t.Errorf("after jig %s, jig.yaml repos = %s, want lib kept", strings.Join(args, " "), got)
		}
	}
}

// TestReposAddAndRemove: add lists a repo nested in the orchestrator (a path of
// any depth, or a URL to fetch), remove takes one out of the list and leaves its
// folder, and both refuse what cannot be: a repo the list has, one outside the
// orchestrator, a name it lacks, and a single-repo project.
func TestReposAddAndRemove(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	parent := t.TempDir()
	shop := filepath.Join(parent, "shop")
	projGitRepo(t, filepath.Join(shop, "api"))
	projRun(t, env, shop, "init", "--orchestrator", "--yes")
	deep := projGitRepo(t, filepath.Join(shop, "group", "deep"))
	web := projGitRepo(t, filepath.Join(shop, "web"))

	r := projRun(t, env, shop, "repos", "add", "web")
	if !strings.Contains(r.Stdout, "repos_add:\n  project: shop\n  name: web\n  path: web\n  source: nested\n") {
		t.Errorf("jig repos add web stdout:\n%s", r.Stdout)
	}
	projRun(t, env, shop, "repos", "add", "group/deep")
	if got := repoNames(projMarker(t, shop).Repos); got != "api,web,deep" {
		t.Errorf("jig.yaml repos = %s, want api,web,deep", got)
	}
	if m := projMarker(t, shop); m.Repos[2].Path != "group/deep" {
		t.Errorf("deep's path = %q, want group/deep", m.Repos[2].Path)
	}
	machine, _ := project.LoadMachine(env.home)
	sameDir(t, "machine map clone deep", machine["shop"].Clones["deep"], deep)
	if got := hereJSON(t, env, deep); got.Repo.Name != "deep" {
		t.Errorf("jig here in group/deep: repo %+v, want deep", got.Repo)
	}

	projRefusal(t, "add of a listed repo", runJig(t, env, shop, "repos", "add", "web"), 2, `already lists a repo "web"`)
	projRefusal(t, "add of a repo outside", runJig(t, env, shop, "repos", "add", filepath.Join(parent, "elsewhere")), 2, "not inside the orchestrator")
	projRefusal(t, "add of the orchestrator itself", runJig(t, env, shop, "repos", "add", "."), 2, "not inside the orchestrator")
	projRefusal(t, "add of a folder that is no repo", runJig(t, env, shop, "repos", "add", "group"), 2, "is not a repository")
	projRefusal(t, "add --clone of a path", runJig(t, env, shop, "repos", "add", "--clone", "group/deep"), 2, "--clone is for a URL")
	projRefusal(t, "remove of an unlisted name", runJig(t, env, shop, "repos", "remove", "nope"), 2, `lists no repo "nope"`, "api, web, deep")

	r = projRun(t, env, shop, "repos", "remove", "web")
	if !strings.Contains(r.Stdout, "repos_remove:") || !strings.Contains(r.Stdout, "Its folder was left alone") {
		t.Errorf("jig repos remove web stdout:\n%s", r.Stdout)
	}
	if _, err := os.Stat(web); err != nil {
		t.Errorf("remove took the folder with it: %v", err)
	}
	if got := repoNames(projMarker(t, shop).Repos); got != "api,deep" {
		t.Errorf("jig.yaml repos = %s, want api,deep", got)
	}
	machine, _ = project.LoadMachine(env.home)
	if _, ok := machine["shop"].Clones["web"]; ok {
		t.Errorf("machine map still has a clone for web: %v", machine["shop"].Clones)
	}

	// A single-repo project keeps no list.
	solo := projGitRepo(t, filepath.Join(parent, "solo"))
	projRun(t, env, solo, "init")
	projRefusal(t, "repos add in a single-repo project", runJig(t, env, solo, "repos", "add", "x"), 2, "is for an orchestrator")
	projRefusal(t, "repos sync in a single-repo project", runJig(t, env, solo, "repos", "sync"), 2, "is for an orchestrator")
	projRefusal(t, "repos outside any project", runJig(t, env, parent, "repos"), 2, "not in a jig project", "jig init")
}

// repoNames is the names of repos, comma-joined, in order.
func repoNames(repos []project.MarkerRepo) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = project.RepoName(r)
	}
	return strings.Join(names, ",")
}
