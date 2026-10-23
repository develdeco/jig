package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
)

// TestPublishPushesABranchAlreadyOnOriginWithoutChangingIt: a branch that is
// already on origin - here jig/<ticket> pushed from the build lease before
// publish - is published as it stands. The target has moved since the gate,
// and publish merges nothing and adds no commit: origin's branch keeps every
// sha it had, and the report and the journal name that head.
func TestPublishPushesABranchAlreadyOnOriginWithoutChangingIt(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	advanceTarget(t, fx)

	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	built := strings.Fields(run(t, buildDir, "rev-list", "--reverse", "origin/main..HEAD"))
	if len(built) < 2 {
		t.Fatalf("test setup: the build lease holds %d commit(s) beyond main, want several", len(built))
	}
	run(t, buildDir, "push", "origin", branch)
	pushed := originRef(t, fx.RepoRemote, "refs/heads/"+branch)

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish of a branch already on origin: %v", err)
	}
	if tip := originRef(t, fx.RepoRemote, "refs/heads/"+branch); tip != pushed || report.Head["fixture-repo"] != pushed {
		t.Fatalf("origin's %s = %s (report head %q), want the head it had, %s, unchanged", branch, tip, report.Head["fixture-repo"], pushed)
	}
	if all := strings.Fields(run(t, fx.RepoRemote, "rev-list", "--reverse", "main.."+branch)); strings.Join(all, " ") != strings.Join(built, " ") {
		t.Fatalf("origin's %s holds %v, want the commits already pushed, unchanged: %v", branch, all, built)
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	for _, l := range lines {
		if l.Event == "pr" && l.Commit != pushed {
			t.Errorf("journal pr line = %+v, want the head %s", l, pushed)
		}
	}
}

// TestPublishRepublishesAPublishedTicket: a ticket that was published can be
// published again. The branch publish pushed is the reviewed head, exactly the
// build lease's copy, so the second publish finds the two in step, pushes
// nothing new, and journals the pull request again at the same head. Nothing
// already on origin is rewritten.
func TestPublishRepublishesAPublishedTicket(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)

	first, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	head := first.Head["fixture-repo"]
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); head == "" || got != head {
		t.Fatalf("test setup: the first publish left origin's %s at %s, want its head %q", branch, got, head)
	}

	second, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("re-Publish: %v", err)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != head || second.Head["fixture-repo"] != head {
		t.Fatalf("origin's %s = %s (report head %q) after the re-publish, want %s unchanged", branch, got, second.Head["fixture-repo"], head)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	var prLines []journal.Line
	for _, l := range lines {
		if l.Event == "pr" {
			prLines = append(prLines, l)
		}
	}
	if len(prLines) != 2 || prLines[0].Commit != head || prLines[1].Commit != head {
		t.Fatalf("journal pr lines = %+v, want two, both at %s", prLines, head)
	}
}

// TestPublishDropsThePublishLeasesStaleCopy: the publish lease's own copy of
// the branch is disposable - Publish re-points it at the copy it ships - so a
// copy left by an earlier attempt must not refuse the acquire. Here the lease
// kept a commit of its own from a publish that stopped before its push, and
// the branch has since reached origin another way: the two have diverged, and
// the sync rule would refuse the lease over a commit nobody keeps.
func TestPublishDropsThePublishLeasesStaleCopy(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)

	// An earlier publish's leftover: the publish lease holds the branch with a
	// commit of its own, and origin has no such branch, so nothing compares.
	stale, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Publish)
	if err != nil {
		t.Fatalf("pool.Acquire publish lease: %v", err)
	}
	writeAndCommit(t, stale.Dir, "leftover.txt", "an earlier attempt", "an earlier publish's leftover")
	// The branch reaches origin from the build lease.
	run(t, buildLeaseDir(t, fx), "push", "origin", branch)

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish over a stale publish lease copy: %v", err)
	}
	if out := run(t, fx.RepoRemote, "ls-tree", "-r", "--name-only", branch); strings.Contains(out, "leftover.txt") {
		t.Fatalf("origin's %s carries the stale lease's leftover.txt: publish shipped the stale copy", branch)
	}
}
