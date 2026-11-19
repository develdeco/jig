package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
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
