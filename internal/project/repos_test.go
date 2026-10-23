package project

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSetMarkerReposKeepsWhatItDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "jig.yaml"), `# the shop project
jig: "1"
project: shop
kind: orchestrator
future_key: from a newer jig # kept
repos:
    - name: api
      path: api
`)
	if err := SetMarkerRepos(dir, []MarkerRepo{{Name: "api", Path: "api"}, {Name: "web", Path: "web", Remote: "https://example.invalid/web.git"}}); err != nil {
		t.Fatalf("SetMarkerRepos: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "jig.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# the shop project", "future_key: from a newer jig # kept", "name: web", "remote: https://example.invalid/web.git"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("jig.yaml lost or lacks %q:\n%s", want, data)
		}
	}
	m, err := LoadMarker(filepath.Join(dir, "jig.yaml"))
	if err != nil || len(m.Repos) != 2 || m.Project != "shop" || m.Kind != KindOrchestrator {
		t.Errorf("marker = %+v, %v", m, err)
	}

	// An empty list takes the key out.
	if err := SetMarkerRepos(dir, nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "jig.yaml"))
	if strings.Contains(string(data), "repos:") || !strings.Contains(string(data), "future_key") {
		t.Errorf("jig.yaml after an empty list:\n%s", data)
	}
}

func TestSetMarkerStoreSetsAndRemovesTheKey(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMarker(dir, Marker{Jig: "1", Project: "demo", Kind: KindProject}); err != nil {
		t.Fatal(err)
	}
	if err := SetMarkerStore(dir, "git@example.invalid:org/demo-tickets.git"); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadMarker(filepath.Join(dir, "jig.yaml"))
	if m.Store != "git@example.invalid:org/demo-tickets.git" || m.Project != "demo" {
		t.Errorf("marker = %+v", m)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "jig.yaml"))
	if strings.Index(string(data), "store:") < strings.Index(string(data), "project:") {
		t.Errorf("store: is not placed after project:\n%s", data)
	}
	if err := SetMarkerStore(dir, ""); err != nil {
		t.Fatal(err)
	}
	m, _ = LoadMarker(filepath.Join(dir, "jig.yaml"))
	if m.Store != "" {
		t.Errorf("store survived its removal: %+v", m)
	}
	writeFile(t, filepath.Join(dir, "jig.yaml"), "- a\n- list\n")
	if err := SetMarkerStore(dir, "x"); err == nil {
		t.Error("SetMarkerStore on a marker that is no mapping: nil error")
	}
}

func TestFindProjectMarkerPassesOverAMember(t *testing.T) {
	orch := t.TempDir()
	if err := WriteMarker(orch, Marker{Jig: "1", Project: "shop", Kind: KindOrchestrator}); err != nil {
		t.Fatal(err)
	}
	member := filepath.Join(orch, "api")
	if err := os.MkdirAll(filepath.Join(member, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(member, Marker{Jig: "1", Project: "shop", Kind: KindMember}); err != nil {
		t.Fatal(err)
	}
	dir, m, ok, err := FindProjectMarker(filepath.Join(member, "src"))
	if err != nil || !ok || m.Kind != KindOrchestrator || !isSameDir(dir, orch) {
		t.Errorf("FindProjectMarker = %q, %+v, %v, %v; want the orchestrator's", dir, m, ok, err)
	}
	// FindMarker, by contrast, stops at the member.
	if _, m, _, _ := FindMarker(filepath.Join(member, "src")); m.Kind != KindMember {
		t.Errorf("FindMarker kind = %q, want member", m.Kind)
	}
	// With nothing above the member, there is no project.
	lonely := t.TempDir()
	if err := WriteMarker(lonely, Marker{Jig: "1", Project: "x", Kind: KindMember}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := FindProjectMarker(lonely); ok || err != nil {
		t.Errorf("FindProjectMarker with only a member = %v, %v; want none", ok, err)
	}
}

func TestIsRepoURLAndURLRepoName(t *testing.T) {
	urls := map[string]string{
		"https://github.com/org/api.git":    "api",
		"https://github.com/org/api":        "api",
		"https://github.com/org/api/":       "api",
		"git@github.com:org/web-app.git":    "web-app",
		"ssh://git@host.invalid/org/db.git": "db",
		"file:///tmp/x/lib.git":             "lib",
		"file:///C:/Users/me/lib.git":       "lib",
	}
	for url, name := range urls {
		if !IsRepoURL(url) {
			t.Errorf("IsRepoURL(%q) = false", url)
		}
		if got := URLRepoName(url); got != name {
			t.Errorf("URLRepoName(%q) = %q, want %q", url, got, name)
		}
	}
	for _, path := range []string{"api", "./api", "group/deep", `C:\Users\me\api`, "C:/Users/me/api", "/tmp/api", "../api", "my@odd/path"} {
		if IsRepoURL(path) {
			t.Errorf("IsRepoURL(%q) = true, want a path", path)
		}
	}
}

func TestNestedReposAndStatuses(t *testing.T) {
	root := runGitDir(t, t.TempDir())
	runGitDir(t, filepath.Join(root, "api"))
	runGitDir(t, filepath.Join(root, "web"))
	runGitDir(t, filepath.Join(root, ".hidden"))
	runGitDir(t, filepath.Join(root, "group", "deep"))
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil { // no .git
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".gitmodules"), "[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.invalid/lib.git\n[submodule \"deep\"]\n\tpath = group/deep\n\turl = https://example.invalid/deep.git\n")

	var paths []string
	for _, n := range NestedRepos(root) {
		paths = append(paths, n.Path)
	}
	if want := []string{"api", "lib", "web"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("NestedRepos = %v, want %v: direct children with a .git, plus direct submodules, no hidden folder, nothing deeper", paths, want)
	}

	listed := []MarkerRepo{{Name: "api", Path: "api"}, {Name: "lib", Path: "lib"}, {Name: "gone", Path: "gone"}, {Name: "far", Path: "far", Remote: "https://example.invalid/far.git"}}
	got := map[string]string{}
	subs := map[string]bool{}
	for _, s := range Statuses(root, listed) {
		got[RepoName(s.Repo)] = s.State
		subs[RepoName(s.Repo)] = s.Submodule
	}
	want := map[string]string{"api": RepoCheckedOut, "lib": RepoNotHere, "gone": RepoMissing, "far": RepoNotHere}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Statuses = %v, want %v", got, want)
	}
	if !subs["lib"] || subs["api"] {
		t.Errorf("Submodule flags = %v, want lib only", subs)
	}
}

func TestResolveRepoArgs(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "shop")
	runGitDir(t, filepath.Join(root, "api"))
	runGitDir(t, filepath.Join(root, "group", "deep"))
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := runGitDir(t, filepath.Join(parent, "outside"))
	if err := os.MkdirAll(filepath.Join(root, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveRepoArgs(root, []string{"api", "https://example.invalid/org/web.git"}, true)
	if err != nil || len(got) != 2 {
		t.Fatalf("ResolveRepoArgs = %+v, %v", got, err)
	}
	if got[0].Repo.Name != "api" || got[0].URL != "" || got[1].Repo.Name != "web" || got[1].Repo.Path != "web" || got[1].URL == "" {
		t.Errorf("resolved = %+v, want api (here) and web (to fetch)", got)
	}
	if abs, err := ResolveRepoArgs(root, []string{filepath.Join(root, "api")}, true); err != nil || abs[0].Repo.Path != "api" {
		t.Errorf("an absolute path inside the root = %+v, %v; want api", abs, err)
	}
	if deep, err := ResolveRepoArgs(root, []string{"group/deep"}, false); err != nil || deep[0].Repo.Name != "deep" || deep[0].Repo.Path != "group/deep" {
		t.Errorf("a deeper path = %+v, %v; want deep at group/deep", deep, err)
	}

	for _, c := range []struct {
		name   string
		args   []string
		direct bool
		want   string
	}{
		{"deeper than a child at init", []string{"group/deep"}, true, "not a direct child"},
		{"outside the orchestrator", []string{outside}, false, "not inside the orchestrator"},
		{"the orchestrator itself", []string{"."}, false, "not inside the orchestrator"},
		{"a parent of it", []string{".."}, false, "not inside the orchestrator"},
		{"a folder that is no repo", []string{"docs"}, false, "is not a repository"},
		{"a path that is not there", []string{"nope"}, false, "is not a repository"},
		{"a URL whose folder exists", []string{"https://example.invalid/org/taken.git"}, false, "already exists"},
		{"two repos by one name", []string{"https://a.invalid/x/web.git", "https://b.invalid/y/web.git"}, false, "two repos would be named"},
	} {
		_, err := ResolveRepoArgs(root, c.args, c.direct)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to say %q", c.name, err, c.want)
		}
	}
}

func TestSyncAddsRemovesAndKeeps(t *testing.T) {
	root := runGitDir(t, t.TempDir())
	runGitDir(t, filepath.Join(root, "api"))
	runGitDir(t, filepath.Join(root, "docs"))
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil { // an unfetched submodule's folder
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".gitmodules"), "[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.invalid/lib.git\n")
	listed := []MarkerRepo{
		{Name: "api", Path: "api"},
		{Name: "web", Path: "web"},
		{Name: "lib", Path: "lib"},
		{Name: "far", Path: "far", Remote: "https://example.invalid/far.git"},
	}

	names := func(rs []MarkerRepo) string {
		var out []string
		for _, r := range rs {
			out = append(out, RepoName(r))
		}
		return strings.Join(out, ",")
	}

	list, rep := Sync(root, listed, nil)
	if names(list) != "api,lib,far,docs" {
		t.Errorf("list = %s, want api,lib,far then docs added (web's folder is gone)", names(list))
	}
	if names(rep.Added) != "docs" || names(rep.Removed) != "web" || names(rep.NotHere) != "lib,far" {
		t.Errorf("report = added %s, removed %s, not here %s", names(rep.Added), names(rep.Removed), names(rep.NotHere))
	}

	// The person says lib is not wanted: it goes; far, with its own remote,
	// stays (it is not a submodule to ask about).
	list, rep = Sync(root, listed, func(r MarkerRepo) bool { return r.Name == "lib" })
	if names(list) != "api,far,docs" || names(rep.Removed) != "web,lib" {
		t.Errorf("with lib dropped: list %s, removed %s", names(list), names(rep.Removed))
	}

	// While .gitmodules still declares lib, the next sync lists it again: a
	// submodule is dropped for good by taking it out of .gitmodules.
	back, rep := Sync(root, list, nil)
	if names(rep.Added) != "lib" || names(back) != "api,far,docs,lib" {
		t.Errorf("a sync with lib still in .gitmodules: list %s, added %s", names(back), names(rep.Added))
	}
	writeFile(t, filepath.Join(root, ".gitmodules"), "")
	again, rep := Sync(root, list, nil)
	if names(again) != names(list) || len(rep.Added)+len(rep.Removed) != 0 {
		t.Errorf("a sync of a list that matches: list %s, report %+v", names(again), rep)
	}
}

func TestRemoveAndMergeRepos(t *testing.T) {
	list := []MarkerRepo{{Name: "api", Path: "api"}, {Path: "group/deep"}, {Name: "web", Path: "web"}}
	rest, gone, err := RemoveRepo(list, "deep")
	if err != nil || RepoName(gone) != "deep" || len(rest) != 2 {
		t.Errorf("RemoveRepo(deep) = %+v, %+v, %v (a repo goes by its path's last element when unnamed)", rest, gone, err)
	}
	if _, _, err := RemoveRepo(list, "nope"); err == nil || !strings.Contains(err.Error(), "api, deep, web") {
		t.Errorf("RemoveRepo(nope) err = %v, want it to list the repos", err)
	}
	if len(list) != 3 {
		t.Errorf("RemoveRepo changed the list it was given: %+v", list)
	}

	merged, err := MergeRepos(list, []MarkerRepo{{Name: "docs", Path: "docs"}})
	if err != nil || len(merged) != 4 || len(list) != 3 {
		t.Errorf("MergeRepos = %+v, %v", merged, err)
	}
	for _, dup := range []MarkerRepo{{Name: "api", Path: "elsewhere"}, {Name: "other", Path: "web"}} {
		if _, err := MergeRepos(list, []MarkerRepo{dup}); err == nil {
			t.Errorf("MergeRepos accepted %+v, which the list already has by name or path", dup)
		}
	}
}

func TestMapAndUnmapRepos(t *testing.T) {
	jigHome := t.TempDir()
	root := t.TempDir()
	api := runGitDir(t, filepath.Join(root, "api"))
	statuses := Statuses(root, []MarkerRepo{{Name: "api", Path: "api"}, {Name: "gone", Path: "gone"}})
	if err := MapRepos(jigHome, "shop", "/stores/shop-tickets", root, statuses); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadMachine(jigHome)
	mp := m["shop"]
	if mp.Store != "/stores/shop-tickets" || mp.Root != root || len(mp.Clones) != 1 || mp.Clones["api"] != api {
		t.Errorf("entry = %+v, want store, root and the one clone that is there", mp)
	}
	// The store and root already there are kept.
	if err := MapRepos(jigHome, "shop", "", "/elsewhere", nil); err != nil {
		t.Fatal(err)
	}
	m, _ = LoadMachine(jigHome)
	if m["shop"].Store != "/stores/shop-tickets" || m["shop"].Root != root {
		t.Errorf("entry = %+v, want store and root kept", m["shop"])
	}
	if err := UnmapRepos(jigHome, "shop", []string{"api", "never-there"}); err != nil {
		t.Fatal(err)
	}
	if err := UnmapRepos(jigHome, "no-such-project", []string{"api"}); err != nil {
		t.Errorf("UnmapRepos of an unmapped project: %v", err)
	}
	m, _ = LoadMachine(jigHome)
	if len(m["shop"].Clones) != 0 {
		t.Errorf("clones after unmap = %v", m["shop"].Clones)
	}
}
