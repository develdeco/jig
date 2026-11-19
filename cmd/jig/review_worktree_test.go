package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestReviewContextAdvisesTheReviewersWorktree: the round names the worktree
// the reviewer makes at the head, for each repo with a clone here: its
// directory beside the builder's, and the git command that makes it detached
// at the head, or moves the one an earlier round left there. A repo with no
// clone has no row, and a round with none has no table. The advice carries no
// directory of jig's.
func TestReviewContextAdvisesTheReviewersWorktree(t *testing.T) {
	t.Parallel()
	never := func(string) bool { return false }
	always := func(string) bool { return true }

	single := verifydeliver.Prepared{Ticket: "JIG-7", Repo: "app", Head: "abc123"}
	clones := map[string]string{"app": "/src/app"}

	out := strings.Join(reviewerWorktreeBlocks(single, clones, never), "\n")
	for _, want := range []string{
		"worktree[1]{repo,clone,dir,command}:",
		"app,/src/app,/src/app.worktrees/JIG-7-review,git -C /src/app worktree add --detach /src/app.worktrees/JIG-7-review abc123",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("advice lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pool") {
		t.Errorf("advice names the pool:\n%s", out)
	}

	again := strings.Join(reviewerWorktreeBlocks(single, clones, always), "\n")
	if want := "git -C /src/app.worktrees/JIG-7-review checkout --detach abc123"; !strings.Contains(again, want) {
		t.Errorf("advice with the directory there lacks %q:\n%s", want, again)
	}
	if strings.Contains(again, "worktree add") {
		t.Errorf("advice with the directory there adds it again:\n%s", again)
	}

	multi := verifydeliver.Prepared{Ticket: "JIG-7", Repos: []verifydeliver.PreparedRepo{
		{Repo: "api", Head: "a1"}, {Repo: "web", Head: "b2"}, {Repo: "docs", Head: "c3"},
	}}
	got := reviewerWorktreeBlocks(multi, map[string]string{"api": "/src/api", "web": "/src/my web"}, never)
	if len(got) != 1 {
		t.Fatalf("blocks = %v, want one table", got)
	}
	for _, want := range []string{
		"worktree[2]{repo,clone,dir,command}:",
		"git -C /src/api worktree add --detach /src/api.worktrees/JIG-7-review a1",
		`"git -C \"/src/my web\" worktree add --detach \"/src/my web.worktrees/JIG-7-review\" b2"`,
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("multi-repo advice lacks %q:\n%s", want, got[0])
		}
	}
	if strings.Contains(got[0], "docs") {
		t.Errorf("a repo with no clone has a row:\n%s", got[0])
	}

	if none := reviewerWorktreeBlocks(single, nil, never); none != nil {
		t.Errorf("no clone: blocks = %v, want none", none)
	}
}
