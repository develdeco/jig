package ready

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// TestProjectRepos: each repo comes with the workspaces its clone's manifest
// declares; a repo with no clone declares none and has no Dir, and one whose
// manifest does not parse says why.
func TestProjectRepos(t *testing.T) {
	t.Parallel()
	write := func(dir, yaml string) string {
		t.Helper()
		path := filepath.Join(dir, ".claude", "jig.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	api := write(t.TempDir(), "workspaces:\n  - id: server\n    path: server\n  - id: worker\n    path: worker\noracles:\n  test: go test ./{path}/...\n  lint: golangci-lint run ./{path}/...\n")
	web := write(t.TempDir(), "workspaces: [unterminated\n")
	cfg := project.Config{Repos: []project.Repo{{Label: "api"}, {Label: "web"}, {Label: "docs"}}}

	got := ProjectRepos(cfg, map[string]string{"api": api, "web": web})
	if len(got) != 3 || got[0].Name != "api" || got[1].Name != "web" || got[2].Name != "docs" {
		t.Fatalf("ProjectRepos = %+v, want api, web and docs in the project's order", got)
	}
	if !reflect.DeepEqual(got[0].Workspaces, []string{"server", "worker"}) || !reflect.DeepEqual(got[0].Oracles, []string{"lint", "test"}) || got[0].Dir != api || !got[0].Read() {
		t.Errorf("api = %+v, want its two workspaces and two oracles read from %s", got[0], api)
	}
	if got[1].Err == nil || got[1].Dir != web || len(got[1].Workspaces) != 0 || got[1].Read() {
		t.Errorf("web = %+v, want a manifest that did not parse, unread", got[1])
	}
	if got[2].Dir != "" || got[2].Err != nil || len(got[2].Workspaces) != 0 || got[2].Read() {
		t.Errorf("docs = %+v, want no clone and so nothing declared", got[2])
	}
}
