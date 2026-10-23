package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/project"
)

// TestMapAndIgnore: `jig map` records a clone jig cannot recognize (a repo with
// no remote), and `jig ignore` makes jig leave a directory alone, a clone of a
// known remote included, until `--remove` takes it back.
func TestMapAndIgnore(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{SecondRepo: true})

	// The second repo lives on this machine as a plain directory: no origin,
	// and the machine map does not know it.
	scratch := filepath.Join(t.TempDir(), "gamma-scratch")
	gitLog(t, filepath.Dir(scratch), "init", "-b", "main", scratch)
	if err := project.UpdateMachine(env.home, func(m map[string]project.MachineProject) error {
		mp := m["fixture"]
		delete(mp.Clones, "second-repo")
		m["fixture"] = mp
		return nil
	}); err != nil {
		t.Fatalf("UpdateMachine: %v", err)
	}
	if r := runJig(t, env, scratch, "here"); r.Code != 0 || r.Stdout != "" {
		t.Fatalf("jig here in an unmapped directory with no remote: exit %d, stdout %q, want silence", r.Code, r.Stdout)
	}

	// map refuses what cannot be mapped.
	if r := runJig(t, env, fx.StoreDir, "map", "nope="+scratch); r.Code != 2 || !strings.Contains(r.Stdout, `declares no repo "nope"`) || !strings.Contains(r.Stdout, "second-repo") {
		t.Errorf("jig map nope=...: exit %d, want 2 naming the repos the project declares:\n%s", r.Code, r.Stdout)
	}
	if r := runJig(t, env, fx.StoreDir, "map", "second-repo="+filepath.Join(scratch, "missing")); r.Code != 2 || !strings.Contains(r.Stdout, "not a directory") {
		t.Errorf("jig map to a missing path: exit %d, want 2 saying it is not a directory:\n%s", r.Code, r.Stdout)
	}
	if r := runJig(t, env, fx.StoreDir, "map", "second-repo"); r.Code != 2 {
		t.Errorf("jig map second-repo (no path): exit %d, want 2:\n%s", r.Code, r.Stdout)
	}

	r := runJig(t, env, fx.StoreDir, "map", "second-repo="+scratch)
	if r.Code != 0 || !strings.Contains(r.Stdout, "map:\n  project: fixture\n  repo: second-repo\n") {
		t.Fatalf("jig map second-repo=...: exit %d\n%s", r.Code, r.Stdout)
	}
	machine, err := project.LoadMachine(env.home)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	if got := machine["fixture"].Clones; got["second-repo"] != scratch || got["fixture-repo"] != fx.RepoDir {
		t.Errorf("mapped clones = %v, want second-repo -> %s and the first repo untouched", got, scratch)
	}
	if got := hereJSON(t, env, scratch); got.How != "map-path" || got.Repo.Name != "second-repo" || got.Project != "fixture" {
		t.Errorf("jig here in the mapped directory: how = %q, repo = %+v, project = %q, want map-path, second-repo, fixture", got.How, got.Repo, got.Project)
	}

	// ignore: the mapped directory, and below it, goes quiet.
	r = runJig(t, env, fx.StoreDir, "ignore", scratch)
	if r.Code != 0 || !strings.Contains(r.Stdout, "status: ignored\n") {
		t.Fatalf("jig ignore: exit %d\n%s", r.Code, r.Stdout)
	}
	if r := runJig(t, env, fx.StoreDir, "ignore", scratch); r.Code != 0 || !strings.Contains(r.Stdout, "status: already ignored\n") {
		t.Errorf("jig ignore twice: exit %d\n%s", r.Code, r.Stdout)
	}
	if ignored, err := project.LoadIgnored(env.home); err != nil || len(ignored) != 1 {
		t.Errorf("ignored list = %v (%v), want the one directory", ignored, err)
	}
	if r := runJig(t, env, scratch, "here"); r.Code != 0 || r.Stdout != "" {
		t.Errorf("jig here in an ignored directory: exit %d, stdout %q, want silence", r.Code, r.Stdout)
	}
	if r := runJig(t, env, scratch, "here", "--explain"); !strings.Contains(r.Stdout, "ignored:") {
		t.Errorf("jig here --explain in an ignored directory lacks the ignored line:\n%s", r.Stdout)
	}

	// A clone of a known remote under an ignored directory is not tracked,
	// while the same clone elsewhere is recognized.
	ignoredParent := t.TempDir()
	gitLog(t, ignoredParent, "clone", fx.RepoRemote, "clone")
	cloneUnder := filepath.Join(ignoredParent, "clone")
	if got := hereJSON(t, env, cloneUnder); got.How != "map-remote" {
		t.Fatalf("a fresh clone before ignoring: how = %q, want map-remote", got.How)
	}
	if r := runJig(t, env, fx.StoreDir, "ignore", ignoredParent); r.Code != 0 {
		t.Fatalf("jig ignore %s: exit %d\n%s", ignoredParent, r.Code, r.Stdout)
	}
	if r := runJig(t, env, cloneUnder, "here"); r.Code != 0 || r.Stdout != "" {
		t.Errorf("jig here in a clone under an ignored directory: exit %d, stdout %q, want silence", r.Code, r.Stdout)
	}
	// A verb that needs a store finds none there, but --store still names one.
	if r := runJig(t, env, cloneUnder, "status", "JIG-1"); r.Code == 0 {
		t.Errorf("jig status in an ignored clone: exit 0, want a refusal:\n%s", r.Stdout)
	}
	if r := runJig(t, env, cloneUnder, "status", "JIG-1", "--store", fx.StoreDir); r.Code != 0 {
		t.Errorf("jig status --store in an ignored clone: exit %d, want 0:\n%s", r.Code, r.Stdout)
	}

	// --remove takes a directory back.
	r = runJig(t, env, fx.StoreDir, "ignore", scratch, "--remove")
	if r.Code != 0 || !strings.Contains(r.Stdout, "status: tracked again\n") {
		t.Fatalf("jig ignore --remove: exit %d\n%s", r.Code, r.Stdout)
	}
	if got := hereJSON(t, env, scratch); got.How != "map-path" || got.Repo.Name != "second-repo" {
		t.Errorf("after --remove: how = %q, repo = %+v, want map-path and second-repo", got.How, got.Repo)
	}
	if r := runJig(t, env, fx.StoreDir, "ignore", scratch, "--remove"); r.Code != 0 || !strings.Contains(r.Stdout, "status: not ignored\n") {
		t.Errorf("jig ignore --remove twice: exit %d\n%s", r.Code, r.Stdout)
	}
}
