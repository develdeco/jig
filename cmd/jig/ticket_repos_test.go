package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// read is a repo whose manifest was read from a clone.
func read(name string, workspaces, oracles []string) ready.Repo {
	return ready.Repo{Name: name, Workspaces: workspaces, Oracles: oracles, Dir: "/clones/" + name}
}

func sliceIn(id, workspace, oracle string) store.Slice {
	return store.Slice{ID: id, Workspace: workspace, Oracle: oracle}
}

// TestValidateWorkspacesOfAProjectOfOneRepo: the messages a project of one repo
// has always had, whatever the repo is called.
func TestValidateWorkspacesOfAProjectOfOneRepo(t *testing.T) {
	t.Parallel()
	all := []store.Slice{sliceIn("a", "alpha", "test"), sliceIn("b", "missing", "test"), sliceIn("c", "app/alpha", "test")}

	got := validateWorkspaces([]ready.Repo{read("app", []string{"alpha"}, []string{"test"})}, all)
	want := []string{`slice b: workspace "missing" not in manifest`, `slice c: workspace "app/alpha" not in manifest`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a repo that declares alpha: %q, want %q: the workspace is a bare id, never read as <repo>/<id>", got, want)
	}
	if got := validateWorkspaces([]ready.Repo{{Name: "app"}}, all); !reflect.DeepEqual(got, []string{`no machine-mapped clone for repo "app"`}) {
		t.Errorf("a repo with no clone: %q, want only that", got)
	}
	broken := ready.Repo{Name: "app", Dir: "/clones/app", Err: errors.New("bad yaml")}
	if got := validateWorkspaces([]ready.Repo{broken}, all); !reflect.DeepEqual(got, []string{"manifest resolve failed: bad yaml"}) {
		t.Errorf("a manifest that does not parse: %q, want only that", got)
	}
	if got := validateWorkspaces(nil, all); !reflect.DeepEqual(got, []string{"the project declares no repos"}) {
		t.Errorf("no repos: %q", got)
	}
}

// TestValidateWorkspacesOfAProjectOfSeveralRepos: each slice is checked against
// the repo it names or the one that declares its workspace; a repo with no
// clone is asked for only when a slice builds in it, and a manifest that does
// not parse is told whatever the slices say.
func TestValidateWorkspacesOfAProjectOfSeveralRepos(t *testing.T) {
	t.Parallel()
	api := read("api", []string{"server", "lib"}, []string{"test"})
	web := read("web", []string{"ui", "lib"}, []string{"test", "lint"})
	docs := ready.Repo{Name: "docs"}

	ok := []store.Slice{sliceIn("s", "api/server", "test"), sliceIn("u", "web/ui", "lint"), sliceIn("b", "ui", "test"), sliceIn("l", "api/lib", "test")}
	if got := validateWorkspaces([]ready.Repo{api, web, docs}, ok); got != nil {
		t.Errorf("slices that name their repo, or a workspace one repo declares, with a repo nothing builds in left unmapped: %q, want none", got)
	}

	bad := []store.Slice{
		sliceIn("amb", "lib", "test"),
		sliceIn("none", "stray", "test"),
		sliceIn("lack", "web/server", "test"),
		sliceIn("lint", "api/server", "lint"),
		sliceIn("doc", "docs/site", "test"),
		sliceIn("doc2", "docs/api", "test"),
	}
	got := validateWorkspaces([]ready.Repo{api, web, docs}, bad)
	for _, want := range []string{
		`slice amb: ambiguous workspace "lib": repos api and web declare it; name its repo, as api/lib or web/lib`,
		`slice none: workspace "stray" is declared by none of the repos (api, web, docs): name its repo, as <repo>/stray; the manifest of docs could not be read`,
		`slice lack: workspace "server" not in the manifest of repo web`,
		`slice lint: oracle "lint" is not in the manifest of repo api (repo web declares it)`,
		`no machine-mapped clone for repo "docs"`,
	} {
		if !containsLine(got, want) {
			t.Errorf("problems %q lack %q", got, want)
		}
	}
	if n := countLines(got, `no machine-mapped clone for repo "docs"`); n != 1 {
		t.Errorf("the missing clone of docs is told %d times, want once for its two slices", n)
	}

	broken := ready.Repo{Name: "web", Dir: "/clones/web", Err: errors.New("bad yaml")}
	got = validateWorkspaces([]ready.Repo{api, broken}, []store.Slice{sliceIn("s", "api/server", "test"), sliceIn("u", "web/ui", "test")})
	if !reflect.DeepEqual(got, []string{"repo web: manifest resolve failed: bad yaml"}) {
		t.Errorf("a repo whose manifest does not parse: %q, want it told once, by name, and its slice not blamed", got)
	}
}

// TestProjectTargets: the target of every repo, once each, in order; the
// default for a project of none.
func TestProjectTargets(t *testing.T) {
	t.Parallel()
	cfg := project.Config{Repos: []project.Repo{{Label: "api", Target: "develop"}, {Label: "web"}, {Label: "docs", Target: "develop"}}}
	if got, want := projectTargets(cfg), []string{"develop", "main"}; !reflect.DeepEqual(got, want) {
		t.Errorf("projectTargets = %v, want %v", got, want)
	}
	if got, want := projectTargets(project.Config{}), []string{"main"}; !reflect.DeepEqual(got, want) {
		t.Errorf("projectTargets of no repos = %v, want %v", got, want)
	}
}

func containsLine(lines []string, want string) bool { return countLines(lines, want) > 0 }

func countLines(lines []string, want string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, want) {
			n++
		}
	}
	return n
}
