package observe

import (
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func TestPostCommitRecordsTheCommitAndStartsNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.claimed()
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
	if got := r.started(); len(got) != 0 {
		t.Fatalf("a post-commit left %+v, want nothing started", got)
	}
	// Seen again, as git's hook and the tool call that made the commit both
	// do, it still starts nothing.
	if _, err := Handle(r.d, Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir}); err != nil || len(r.started()) != 0 {
		t.Errorf("a second post-commit for the same commit: %v, left %+v", err, r.started())
	}
}

func TestPostCommitStartsNothingWhateverTheCommitNames(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.claimed()
	r.commit("", "plain.txt") // no trailer: the session's claim is the slice
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	r.commit("zzz", "stray.txt") // a slice the ticket lacks
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	if err := r.st.WriteSliceState(r.fx.Ticket, "c", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	r.commit("c", "named.txt") // a green slice, named by the trailer
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	if got := r.started(); len(got) != 0 {
		t.Errorf("left %+v, want nothing started for any of the commits", got)
	}
	if got := len(r.ledgerOf(journal.EventGitCommit)); got != 3 {
		t.Errorf("git-commit lines = %d, want every commit recorded", got)
	}
	if st, err := r.st.ReadSliceState(r.fx.Ticket, "c"); err != nil || st.State != "green" {
		t.Errorf("slice state = %+v, %v, want still green", st, err)
	}

	// Off the ticket branch the commit binds no ticket at all.
	if _, err := gitx.Run(r.fx.RepoDir, "checkout", "-q", "main"); err != nil {
		t.Fatal(err)
	}
	r.commit("a", "off.txt")
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	if got := r.started(); len(got) != 0 {
		t.Errorf("left %+v for a commit on main, want nothing", got)
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
		t.Errorf("left %+v outside a project", r.started())
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

// claimed makes the session s9 the holder of slice c and the commit's session.
func (r *rig) claimed() {
	r.t.Helper()
	r.add(journal.Line{Event: journal.EventClaim, Slice: "c", Session: "s9"})
	r.d.Getenv = func(k string) string {
		if k == "JIG_SESSION" {
			return "s9"
		}
		return ""
	}
}
