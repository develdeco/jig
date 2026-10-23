package observe

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

func TestPostCommitRecordsTheCommitAndStartsItsVerification(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	sha := r.commit("b", "b.txt")

	res, err := Handle(r.d, Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	if err != nil || !res.Empty() {
		t.Fatalf("Handle = %+v, %v, want nothing to say", res, err)
	}
	commits := r.ledgerOf(journal.EventGitCommit)
	if len(commits) != 1 || commits[0].Commit != sha || commits[0].Branch != "jig/"+r.fx.Ticket || commits[0].Cwd != r.fx.RepoDir {
		t.Errorf("git-commit lines = %+v, want one for %s", commits, sha)
	}
	if commits[0].Session != "" || commits[0].Ticket != "" || commits[0].Harness != "" {
		t.Errorf("git-commit line carries more than commit, cwd and branch: %+v", commits[0])
	}
	got := r.started()
	if len(got) != 1 || !strings.Contains(strings.Join(got[0].Args, " "), "--slice b") {
		t.Fatalf("started %+v, want one worker for slice b", got)
	}
	if strings.Contains(strings.Join(got[0].Args, " "), "--session") {
		t.Errorf("worker args %v name a session; git's hook has none", got[0].Args)
	}
	// Seen again (a tool call of the commit), it is not started again.
	if _, err := Handle(r.d, Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir}); err != nil || len(r.started()) != 1 {
		t.Errorf("a second post-commit for the same commit: %v, %d workers", err, len(r.started()))
	}
}

func TestPostCommitTakesTheSliceFromTheSessionWhenTheCommitNamesNone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.add(journal.Line{Event: journal.EventClaim, Slice: "c", Session: "s9"})
	r.commit("", "c.txt")
	r.d.Getenv = func(k string) string {
		if k == "JIG_SESSION" {
			return "s9"
		}
		return ""
	}
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	got := r.started()
	if len(got) != 1 || !strings.Contains(strings.Join(got[0].Args, " "), "--slice c") || !strings.Contains(strings.Join(got[0].Args, " "), "--session s9") {
		t.Errorf("started %+v, want one worker for slice c as session s9", got)
	}
}

func TestPostCommitVerifiesNothingItCannotPlace(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()

	r.commit("", "plain.txt") // no trailer and no claim
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	r.commit("zzz", "stray.txt") // a slice the ticket lacks
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	if len(r.started()) != 0 {
		t.Errorf("started %+v, want nothing: neither commit names a slice of the ticket", r.started())
	}
	if got := len(r.ledgerOf(journal.EventGitCommit)); got != 2 {
		t.Errorf("git-commit lines = %d, want both commits recorded", got)
	}

	// Off the ticket branch the commit binds no ticket at all.
	if _, err := gitx.Run(r.fx.RepoDir, "checkout", "-q", "main"); err != nil {
		t.Fatal(err)
	}
	r.commit("a", "off.txt")
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	if len(r.started()) != 0 {
		t.Errorf("started %+v for a commit on main, want nothing", r.started())
	}
}

func TestPostCommitOutsideAnyProjectStillRecordsTheCommit(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	repo := t.TempDir()
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "one"}} {
		if _, err := gitx.Run(repo, a...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Handle(r.d, Event{Harness: "git", Kind: KindPostCommit, Cwd: repo}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := r.ledgerOf(journal.EventGitCommit); len(got) != 1 || got[0].Cwd != repo {
		t.Errorf("git-commit lines = %+v, want one in %s", got, repo)
	}
	if len(r.started()) != 0 {
		t.Errorf("started %+v outside a project", r.started())
	}
}

func TestPostCommitInADirectoryThatIsNoRepositoryIsAnError(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if _, err := Handle(r.d, Event{Kind: KindPostCommit, Cwd: t.TempDir()}); err == nil {
		t.Error("Handle in a plain directory succeeded, want the git error")
	}
	if _, err := Handle(r.d, Event{Kind: KindPostCommit}); err == nil {
		t.Error("Handle with no directory succeeded, want an error")
	}
}
