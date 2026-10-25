package correct

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
)

func TestOutsideKeepsTheFilesUnderNoWorkspace(t *testing.T) {
	t.Parallel()
	files := []string{"alpha/a.go", "alpha", "alphabet/x.go", "beta/deep/b.go", "go.mod", "docs/alpha/readme.md"}
	got := Outside(files, []string{"alpha", "beta"})
	want := []string{"alphabet/x.go", "go.mod", "docs/alpha/readme.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Outside = %v, want %v: a workspace is a directory, not a name prefix", got, want)
	}
	if got := Outside(files, []string{"."}); got != nil {
		t.Errorf("Outside the whole repo = %v, want nothing", got)
	}
	if got := Outside(files, nil); got != nil {
		t.Errorf("Outside no workspace at all = %v, want nothing: there is no scope to judge by", got)
	}
}

func TestWorkspacePathsPassOverWorkspacesTheManifestLacks(t *testing.T) {
	t.Parallel()
	m := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "./beta/"}, {ID: "win", Path: "sub\x5cwin"}}}
	all := []store.Slice{{ID: "a", Workspace: "alpha"}, {ID: "b", Workspace: "beta"}, {ID: "a2", Workspace: "alpha"},
		{ID: "g", Workspace: "gamma"}, {ID: "w", Workspace: "win"}}
	got := WorkspacePaths(m, "", all)
	want := []string{"alpha", "beta", "sub/win"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WorkspacePaths = %v, want %v: cleaned, once each, and nothing for gamma, which is another repo", got, want)
	}
}

// TestWorkspacePathsReadAWorkspaceNamedByItsRepo: in a project of several repos
// a slice writes "<repo>/<id>", and only the repo of that name takes it.
func TestWorkspacePathsReadAWorkspaceNamedByItsRepo(t *testing.T) {
	t.Parallel()
	m := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "server", Path: "srv"}}}
	all := []store.Slice{{ID: "s", Workspace: "api/server"}, {ID: "u", Workspace: "web/ui"}, {ID: "b", Workspace: "server"}}
	if got, want := WorkspacePaths(m, "api", all), []string{"srv"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorkspacePaths for api = %v, want %v: its own slice, once, and its bare one", got, want)
	}
	if got, want := WorkspacePaths(m, "web", all), []string{"srv"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorkspacePaths for web = %v, want %v: only the bare id, which this manifest declares as written", got, want)
	}
	if got := WorkspacePaths(m, "docs", all[:2]); got != nil {
		t.Errorf("WorkspacePaths for docs = %v, want nothing: both slices build in other repos", got)
	}
}

func TestScopeJudgesACommitAgainstTheManifestOfItsRepo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	all, err := w.st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}

	inside := w.commitFiles("inside", "alpha/a.go", "beta/b.go")
	if _, drifted, err := Scope(w.clone, "", inside, all); err != nil || drifted {
		t.Errorf("a commit inside the workspaces = drifted %v, %v, want neither", drifted, err)
	}

	out := w.commitFiles("outside", "alpha/a2.go", "docs/readme.md", "go.mod")
	d, drifted, err := Scope(w.clone, "", out, all)
	if err != nil || !drifted {
		t.Fatalf("a commit that reaches outside = drifted %v, %v, want a drift", drifted, err)
	}
	if want := []string{"docs/readme.md", "go.mod"}; !reflect.DeepEqual(d.Files, want) {
		t.Errorf("Files = %v, want %v", d.Files, want)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(d.Paths, want) {
		t.Errorf("Paths = %v, want %v", d.Paths, want)
	}
	if d.Commit != out || d.Ref() != "scope:"+out[:12] {
		t.Errorf("Commit = %s, Ref = %s, want the commit and its short form", d.Commit, d.Ref())
	}
	for _, want := range []string{"commit " + out[:12], "2 files", "JIG-1", "alpha, beta", "docs/readme.md, go.mod"} {
		if text := scopeText("JIG-1", d); !strings.Contains(text, want) {
			t.Errorf("scopeText = %q, want %q", text, want)
		}
	}

	// A deleted file counts too.
	w.git("rm", "-q", "go.mod")
	w.git("commit", "-m", "drop go.mod")
	if d, drifted, err := Scope(w.clone, "", w.git("rev-parse", "HEAD"), all); err != nil || !drifted || !reflect.DeepEqual(d.Files, []string{"go.mod"}) {
		t.Errorf("a commit that deletes an outside file = %+v, %v, %v, want a drift on go.mod", d, drifted, err)
	}
}

func TestScopeHasNothingToJudgeByWithoutAWorkspaceOfThisRepo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	sha := w.commitFiles("outside", "docs/readme.md")

	elsewhere := []store.Slice{{ID: "g", Workspace: "gamma"}}
	if _, drifted, err := Scope(w.clone, "", sha, elsewhere); err != nil || drifted {
		t.Errorf("slices that build in another repo = drifted %v, %v, want neither", drifted, err)
	}
	// The first commit has no parent to compare with.
	root := w.git("rev-list", "--max-parents=0", "HEAD")
	all, _ := w.st.ReadSlices("JIG-1")
	if _, drifted, err := Scope(w.clone, "", root, all); err != nil || drifted {
		t.Errorf("a root commit = drifted %v, %v, want neither", drifted, err)
	}
	// A workspace that is the whole repo leaves nothing outside.
	w.write(".claude/jig.yaml", "workspaces:\n  - id: alpha\n    path: .\n")
	w.commit("wide manifest", ".claude/jig.yaml")
	sha = w.commitFiles("anywhere", "docs/more.md")
	if _, drifted, err := Scope(w.clone, "", sha, all); err != nil || drifted {
		t.Errorf("a workspace that is the whole repo = drifted %v, %v, want neither", drifted, err)
	}
	// A manifest that does not parse is an error, not a verdict.
	w.write(".claude/jig.yaml", "workspaces: [not, a, list of maps\n")
	if _, _, err := Scope(w.clone, "", sha, all); err == nil {
		t.Error("a manifest that does not parse = no error")
	}
}
