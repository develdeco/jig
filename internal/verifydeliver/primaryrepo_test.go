package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// TestPrimaryRepoOfAProjectWithNoRepos: a v3 project whose jig.yaml is not on
// this machine has no repos; asking for its primary one is not a panic.
func TestPrimaryRepoOfAProjectWithNoRepos(t *testing.T) {
	t.Parallel()
	repo, name, target := primaryRepo(project.Config{})
	if repo != (project.Repo{}) || name != "" || target != "main" {
		t.Errorf("primaryRepo(no repos) = %+v, %q, %q; want the zero repo and the default target", repo, name, target)
	}
	_, name, target = primaryRepo(project.Config{Repos: []project.Repo{{Remote: "https://example.invalid/org/app.git", Target: "develop"}}})
	if name != "app" || target != "develop" {
		t.Errorf("primaryRepo(one repo) = %q, %q", name, target)
	}
}
