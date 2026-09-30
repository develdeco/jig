package verifydeliver

import (
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
)

// TestPublishPushesABranchAlreadyOnOriginAsItIs: only unpushed history is
// squashed. A branch that is already on origin - here jig/<ticket> pushed from
// the build lease before publish, the case the squash used to refuse with
// PUSHED_RANGE - is pushed as it is: every commit already there keeps its sha,
// what publish adds (the merge of the moved target, the memorize commit) sits
// on top, origin's branch is fast-forwarded to it, and the report and the
// journal say the branch was not squashed.
func TestPublishPushesABranchAlreadyOnOriginAsItIs(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	advanceTarget(t, fx)

	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	built := strings.Fields(run(t, buildDir, "rev-list", "--reverse", "origin/main..HEAD"))
	if len(built) < 2 {
		t.Fatalf("test setup: the build lease holds %d commit(s) beyond main, want several to squash", len(built))
	}
	run(t, buildDir, "push", "origin", branch)
	pushed := originRef(t, fx.RepoRemote, "refs/heads/"+branch)

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish of a branch already on origin: %v", err)
	}
	if len(report.Squashed) != 0 || report.Squash("fixture-repo") != NotSquashed {
		t.Errorf("report: squashed %v, Squash() = %q, want nothing squashed and %q", report.Squashed, report.Squash("fixture-repo"), NotSquashed)
	}
	if NotSquashed != "not squashed (branch already on origin)" {
		t.Errorf("NotSquashed = %q, want the words the publish report is documented to say", NotSquashed)
	}

	tip := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	if tip == pushed || report.Head["fixture-repo"] != tip {
		t.Fatalf("origin's %s = %s (report head %q), want it advanced past %s to the head publish pushed", branch, tip, report.Head["fixture-repo"], pushed)
	}
	// Fast-forwarded: what was there is still there, every sha unchanged.
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", pushed, tip)
	all := strings.Fields(run(t, fx.RepoRemote, "rev-list", "--reverse", "main.."+branch))
	if len(all) < len(built) || !slices.Equal(all[:len(built)], built) {
		t.Fatalf("origin's %s starts with %v, want the commits already pushed, unchanged: %v", branch, all, built)
	}
	// What publish added sits on top: the merge of the moved target, then the
	// memorize commit.
	if subject := run(t, fx.RepoRemote, "log", "-1", "--format=%s", tip); subject != "docs: memorize "+fx.Ticket {
		t.Errorf("the tip of %s is %q, want the memorize commit", branch, subject)
	}
	if parents := strings.Fields(run(t, fx.RepoRemote, "rev-list", "--parents", "-n", "1", tip+"~1")); len(parents) != 3 {
		t.Errorf("the commit under the memorize commit has %d parent(s), want the merge of the moved target (2)", len(parents)-1)
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	var squashLines []journal.Line
	for _, l := range lines {
		if l.Event == "squash" {
			squashLines = append(squashLines, l)
		}
	}
	if len(squashLines) != 1 || squashLines[0].Outcome != "none:branch-on-origin" || squashLines[0].Commit != "" {
		t.Errorf("journal squash lines = %+v, want one recording none:branch-on-origin and no squash commit", squashLines)
	}
}

// TestPublishRepublishesAPublishedTicket: a ticket whose publish squashed and
// pushed jig/<ticket> can be published again once its build lease is
// integrated with the squash, the way BRANCH_DIVERGED says to: the second
// publish pushes what is now a fast-forward as it is, the squash stays where
// it was pushed, and nothing already on origin is rewritten.
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
	squash := first.Squashed["fixture-repo"]
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); squash == "" || got != squash {
		t.Fatalf("test setup: the first publish left origin's %s at %s, want its squash %q", branch, got, squash)
	}

	// The build lease still holds the unsquashed commits, so it has diverged
	// from origin's squash: merging origin's branch in there is what the sync
	// rule's refusal says to do, and a round over the result is clean.
	buildDir, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	if _, aerr := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Build); aerr == nil {
		t.Fatal("test setup: a build acquire after the squash publish must be refused as diverged")
	}
	run(t, buildDir, "fetch", "origin")
	run(t, buildDir, "merge", "--no-edit", "origin/"+branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate over the integrated branch: %v", err)
	}

	second, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("re-Publish: %v", err)
	}
	if len(second.Squashed) != 0 || second.Squash("fixture-repo") != NotSquashed {
		t.Errorf("re-publish squashed %v (%q), want the branch pushed as it is", second.Squashed, second.Squash("fixture-repo"))
	}
	tip := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	if tip == squash || second.Head["fixture-repo"] != tip {
		t.Fatalf("origin's %s = %s after the re-publish, want it advanced past the first squash %s", branch, tip, squash)
	}
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", squash, tip)
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
	writeAndCommit(t, stale.Dir, "leftover.txt", "an earlier attempt", "an earlier publish's squash")
	// The branch reaches origin from the build lease.
	run(t, buildLeaseDir(t, fx), "push", "origin", branch)

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish over a stale publish lease copy: %v", err)
	}
	if out := run(t, fx.RepoRemote, "ls-tree", "-r", "--name-only", branch); strings.Contains(out, "leftover.txt") {
		t.Fatalf("origin's %s carries the stale lease's leftover.txt: publish shipped the stale copy", branch)
	}
}
