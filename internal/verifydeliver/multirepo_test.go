package verifydeliver

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestCheckOneRepo: a publish is refused for a ticket whose slices build in
// more than one repo, with the repos named; a ticket that builds in one repo, in
// a project of one or of several, goes through, and so does one whose workspaces
// no repo owns yet. (The gate has no such refusal: TestGateRepos.)
func TestCheckOneRepo(t *testing.T) {
	t.Parallel()
	two := project.Config{Repos: []project.Repo{{Label: "api"}, {Label: "web"}}}
	three := project.Config{Repos: []project.Repo{{Label: "api"}, {Label: "web"}, {Label: "docs"}}}
	slice := func(id, workspace string) store.Slice { return store.Slice{ID: id, Workspace: workspace} }

	for _, c := range []struct {
		name    string
		cfg     project.Config
		slices  []store.Slice
		refused string
	}{
		{"two repos", two, []store.Slice{slice("s", "api/server"), slice("u", "web/ui")}, "repos api and web;"},
		{"three, two touched", three, []store.Slice{slice("d", "docs/site"), slice("s", "api/server"), slice("t", "api/lib")}, "repos api and docs;"},
		{"three touched", three, []store.Slice{slice("d", "docs/site"), slice("s", "api/server"), slice("u", "web/ui")}, "repos api, web and docs;"},
		{"one repo of two", two, []store.Slice{slice("s", "api/server"), slice("t", "api/lib")}, ""},
		{"a bare workspace nobody owns", two, []store.Slice{slice("s", "api/server"), slice("x", "stray")}, ""},
		{"a project of one", project.Config{Repos: []project.Repo{{Label: "app"}}}, []store.Slice{slice("a", "alpha"), slice("b", "beta")}, ""},
		{"no repos", project.Config{}, []store.Slice{slice("a", "alpha")}, ""},
	} {
		err := checkOneRepo(Deps{Cfg: c.cfg}, "JIG-1", c.slices)
		if c.refused == "" {
			if err != nil {
				t.Errorf("%s: refused: %v", c.name, err)
			}
			continue
		}
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "MULTI_REPO_NOT_YET" ||
			!strings.Contains(ae.Msg, "this ticket's slices span "+c.refused) || !strings.Contains(ae.Msg, "publish ships one repo until multi-repo publishing lands") {
			t.Errorf("%s: err = %v, want MULTI_REPO_NOT_YET naming %q", c.name, err, c.refused)
		}
	}
}
