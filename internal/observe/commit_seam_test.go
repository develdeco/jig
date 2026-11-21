package observe

import (
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// TestSeamAPostCommitOnADetachedHeadRecordsTheCommitWithNoBranch: the ledger
// line of a commit made on a detached HEAD names the commit and the
// directory, and no branch.
func TestSeamAPostCommitOnADetachedHeadRecordsTheCommitWithNoBranch(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	repo := t.TempDir()
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "one"}} {
		if _, err := gitx.Run(repo, a...); err != nil {
			t.Fatal(err)
		}
	}
	sha, err := gitx.RevParse(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Handle(r.d, Event{Harness: "git", Kind: KindPostCommit, Cwd: repo}); err != nil {
		t.Fatalf("Handle on a branch: %v", err)
	}
	if _, err := gitx.Run(repo, "checkout", "-q", "--detach"); err != nil {
		t.Fatal(err)
	}
	if _, err := Handle(r.d, Event{Harness: "git", Kind: KindPostCommit, Cwd: repo}); err != nil {
		t.Fatalf("Handle detached: %v", err)
	}
	got := r.ledgerOf(journal.EventGitCommit)
	if len(got) != 2 || got[0].Branch != "main" || got[1].Branch != "" || got[1].Commit != sha || got[1].Cwd != repo {
		t.Errorf("git-commit lines = %+v, want main, then a detached commit of %s with no branch", got, sha)
	}
}
