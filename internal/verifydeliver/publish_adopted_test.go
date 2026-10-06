package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// TestPublishShipsAnAdoptedBranch is the hand-written-branch loop's last step:
// an author built add-retry outside jig and pushed it, jig adopted it, built
// a fix on top in its build lease, and the last round was clean. Publish ships
// that branch as it is - the author's commit keeps its sha, jig's fix and
// whatever publish adds (the merge of the target, which has moved, and the
// memorize commit) sit on top, and origin's branch is fast-forwarded to it,
// with no squash and no jig/<ticket> branch beside it. Nothing the author
// pushed is rewritten, and the commits jig built are on origin under the same
// shas, so the ticket goes on working: the build lease follows origin's copy,
// which now holds all of them.
func TestPublishShipsAnAdoptedBranch(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	built := jigBuildsOn(t, d, fx, ticket, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket}); err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	advanceTarget(t, fx)
	mainBefore := originRef(t, fx.RepoRemote, "refs/heads/main")
	// A stale branch under the ticket's default name is not the ticket's
	// branch, which is the recorded one.
	run(t, fx.RepoRemote, "branch", ticketBranch(ticket), mainBefore)

	report, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish of an adopted branch: %v", err)
	}
	if report.Squash("fixture-repo") != NotSquashed || len(report.Squashed) != 0 {
		t.Errorf("report: squashed %v (%q), want the branch pushed as it is", report.Squashed, report.Squash("fixture-repo"))
	}

	head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	if report.Head["fixture-repo"] != head || head == tip {
		t.Fatalf("origin's %s = %s (report head %q), want it advanced past the author's %s to the head publish pushed", branch, head, report.Head["fixture-repo"], tip)
	}
	// Fast-forwarded from the author's tip, jig's fix among what was added, all
	// under the shas they had.
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", tip, head)
	added := strings.Fields(run(t, fx.RepoRemote, "rev-list", "--first-parent", "--reverse", tip+".."+head))
	if len(added) != 3 || added[0] != built {
		t.Fatalf("publish added %v on top of the author's tip, want jig's fix %s, the merge of main, and the memorize commit", added, built)
	}
	if subject := run(t, fx.RepoRemote, "log", "-1", "--format=%s", added[1]); !strings.HasPrefix(subject, "Merge ") {
		t.Errorf("the commit after jig's fix is %q, want the merge of the moved target", subject)
	}
	if subject := run(t, fx.RepoRemote, "log", "-1", "--format=%s", head); subject != "docs: memorize "+ticket {
		t.Errorf("the tip of %s is %q, want the memorize commit", branch, subject)
	}
	if out := run(t, fx.RepoRemote, "show", head+":.claude/retrieval/"+ticket+".md"); strings.TrimSpace(out) == "" {
		t.Errorf("the memorize commit's retrieval notes are empty")
	}
	// The stale default-named branch was left alone, and main never moved.
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(ticket)); got != mainBefore {
		t.Errorf("origin's %s = %s, want the stale branch untouched at %s", ticketBranch(ticket), got, mainBefore)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
		t.Errorf("origin's main = %s, want %s: publish never moves the target", got, mainBefore)
	}

	// The store records the publish, and the squash that did not happen.
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	seen := map[string]journal.Line{}
	for _, l := range lines {
		seen[l.Event] = l
	}
	for _, event := range []string{"reconcile", "memorize", "changelog", "squash", "pr", "route", "publish-done"} {
		if _, ok := seen[event]; !ok {
			t.Errorf("journal has no %q line after the publish", event)
		}
	}
	if l := seen["squash"]; l.Outcome != "none:branch-on-origin" || l.Commit != "" {
		t.Errorf("journal squash line = %+v, want none:branch-on-origin and no commit", l)
	}
	if l := seen["pr"]; l.Commit != head {
		t.Errorf("journal pr line = %+v, want the pushed head %s", l, head)
	}
	if ledger, err := os.ReadFile(filepath.Join(d.Store.Root, "ledger.md")); err != nil || !strings.Contains(string(ledger), "## "+ticket+" - Add retry") {
		t.Errorf("ledger.md lacks the ticket's entry titled with its recorded title (err %v):\n%s", err, ledger)
	}

	// Everything jig built is on origin under the same shas: the build lease
	// is behind origin's copy now, and follows it.
	buildLease := buildLeaseOf(t, fx, ticket)
	if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build); err != nil {
		t.Fatalf("build Acquire after the publish: %v", err)
	}
	if got := run(t, buildLease, "rev-parse", "HEAD"); got != head {
		t.Errorf("the build lease is at %s after the publish, want origin's copy %s", got, head)
	}
}

// TestPublishShipsAnAdoptedBranchJigBuiltNothingOn: the author's branch, and
// nothing of jig's on it. There is no build lease on this machine and none is
// needed - the copy publish ships is origin's - and what publish adds (the
// memorize commit) is the only thing that moves the branch.
func TestPublishShipsAnAdoptedBranchJigBuiltNothingOn(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate --branch: %v", err)
	}

	report, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish of an adopted branch jig built nothing on: %v", err)
	}
	head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	if report.Head["fixture-repo"] != head || head == tip {
		t.Fatalf("origin's %s = %s (report head %q), want the memorize commit pushed on the author's %s", branch, head, report.Head["fixture-repo"], tip)
	}
	if got := run(t, fx.RepoRemote, "rev-parse", head+"~1"); got != tip {
		t.Errorf("the commit under the memorize commit is %s, want the author's tip %s", got, tip)
	}
	if subject := run(t, fx.RepoRemote, "log", "-1", "--format=%s", head); subject != "docs: memorize "+ticket {
		t.Errorf("the tip of %s is %q, want the memorize commit", branch, subject)
	}
	if _, err := os.Stat(buildLeaseOf(t, fx, ticket)); !os.IsNotExist(err) {
		t.Errorf("a build lease exists at %s (stat err %v): publishing a branch jig built nothing on needs none", buildLeaseOf(t, fx, ticket), err)
	}
}

// TestPublishOfAnAdoptedBranchHoldsTheHeadToTheReview: the reviewed-head rule
// on an adopted branch. The author pushes after the clean round, so origin's
// copy - the one publish would ship - is not the head the round reviewed: the
// publish is refused before it writes anything, and once a round has reviewed
// the branch as it is, it ships the author's new commit too.
func TestPublishOfAnAdoptedBranchHoldsTheHeadToTheReview(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	authorBranch(t, fx, branch)
	src := NewReviewerGateSource(reviewerBackend(t))
	first, err := Gate(d, src, GateOpts{Ticket: ticket, Branch: branch})
	if err != nil || first.Verdict != "clean" || first.ReviewedSHA["fixture-repo"] == "" {
		t.Fatalf("Gate --branch: verdict %q, reviewed %q, err %v, want a clean reviewer round", first.Verdict, first.ReviewedSHA["fixture-repo"], err)
	}
	pushed := authorPush(t, fx, branch, "more.txt")
	journalBefore, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}

	_, err = Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != pushed {
		t.Fatalf("origin's %s = %s after the refused publish, want the author's %s untouched", branch, got, pushed)
	}
	if after, _ := journal.Read(d.Store, ticket); len(after) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish", len(journalBefore), len(after))
	}

	second, err := Gate(d, src, GateOpts{Ticket: ticket})
	if err != nil || second.ReviewedSHA["fixture-repo"] != pushed {
		t.Fatalf("Gate round 2: reviewed %q, err %v, want a round over the author's new commit %s", second.ReviewedSHA["fixture-repo"], err, pushed)
	}
	if _, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true}); err != nil {
		t.Fatalf("Publish after a round over the branch as it is: %v", err)
	}
	head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", pushed, head)
}

// TestPublishOfAnAdoptedBranchShipsTheReviewedHeadAfterTheTargetMoves: the
// head held to the review is the one publish would ship before reconcile. The
// target moves after a clean reviewer round, and reconcile then merges it into
// the branch on origin, so the head publish pushes is not the reviewed one and
// is still not a refusal; reading the head after reconcile would refuse every
// publish once the target had moved.
func TestPublishOfAnAdoptedBranchShipsTheReviewedHeadAfterTheTargetMoves(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)
	first, err := Gate(d, NewReviewerGateSource(reviewerBackend(t)), GateOpts{Ticket: ticket, Branch: branch})
	if err != nil || first.Verdict != "clean" || first.ReviewedSHA["fixture-repo"] != tip {
		t.Fatalf("Gate --branch: verdict %q, reviewed %q, err %v, want a clean reviewer round over the author's %s", first.Verdict, first.ReviewedSHA["fixture-repo"], err, tip)
	}
	advanceTarget(t, fx)
	moved := originRef(t, fx.RepoRemote, "refs/heads/main")

	report, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish after a clean reviewer round and a moved target: %v", err)
	}
	if report.Tier != "oracles-only" {
		t.Errorf("revalidation tier = %q, want oracles-only: the target moved after the round", report.Tier)
	}
	head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	if head == tip {
		t.Fatalf("origin's %s did not move from the reviewed %s", branch, tip)
	}
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", tip, head)
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", moved, head)
}

// TestPointAtTicketBranchSaysWhoseCopyItChose: the function the gate and
// publish share reports which copy of the branch the lease was left at, the
// build lease's or origin's, because what publish's refusals tell the operator
// to do depends on it. The rule is the branch's, not the kind of ticket's: an
// adopted branch jig built nothing on is origin's, the author's; any other
// branch is the build lease's while origin has no copy of it (the ticket's own
// jig/<ticket> until a publish pushes it) or while its commits are unpushed,
// and origin's once it holds them, whether the human or a publish pushed them.
func TestPointAtTicketBranchSaysWhoseCopyItChose(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	pointErr := func(t *testing.T, d Deps, fx *fixture.Fixture, tkt, br string, adopted bool) (branchCopy, error) {
		t.Helper()
		lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", br, tkt, pool.Publish)
		if err != nil {
			t.Fatalf("acquire the publish lease: %v", err)
		}
		lines, err := journal.Read(d.Store, tkt)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		return pointAtTicketBranch(d, lease.Dir, "fixture-repo", tkt, br, adopted, journal.BuiltCommits(lines), "publish")
	}
	point := func(t *testing.T, d Deps, fx *fixture.Fixture, tkt, br string, adopted bool) branchCopy {
		t.Helper()
		got, err := pointErr(t, d, fx, tkt, br, adopted)
		if err != nil {
			t.Fatalf("pointAtTicketBranch: %v", err)
		}
		return got
	}
	adopt := func(t *testing.T) (Deps, *fixture.Fixture) {
		t.Helper()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d := newDeps(t, fx)
		newAdoptTicket(t, d, ticket)
		authorBranch(t, fx, branch)
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
			t.Fatalf("Gate --branch: %v", err)
		}
		return d, fx
	}

	// own is a ticket's own jig/<ticket> after the build and a clean round: the
	// state each of its subtests continues from.
	own := func(t *testing.T) (Deps, *fixture.Fixture, string, string) {
		t.Helper()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d := newDeps(t, fx)
		gateToClean(t, fx, d)
		return d, fx, ticketBranch(fx.Ticket), buildLeaseOf(t, fx, fx.Ticket)
	}
	// pushedOn adds a commit to origin's copy of br from another clone.
	pushedOn := func(t *testing.T, fx *fixture.Fixture, br string) {
		t.Helper()
		other := cloneFrom(t, fx.RepoRemote)
		run(t, other, "checkout", br)
		writeAndCommit(t, other, "more.txt", "added on origin", "a commit only origin has")
		run(t, other, "push", "origin", br)
	}
	t.Run("a ticket's own branch", func(t *testing.T) {
		t.Parallel()
		d, fx, br, _ := own(t)
		if got := point(t, d, fx, fx.Ticket, br, false); got != buildLeasesCopy {
			t.Errorf("copy = %v, want the build lease's: jig/<ticket> lives there until a publish pushes it", got)
		}
	})
	t.Run("a ticket's own branch pushed as it is", func(t *testing.T) {
		t.Parallel()
		d, fx, br, buildDir := own(t)
		run(t, buildDir, "push", "origin", br)
		if got := point(t, d, fx, fx.Ticket, br, false); got != originsCopy {
			t.Errorf("copy = %v, want origin's: it holds every commit the build lease does", got)
		}
	})
	t.Run("a ticket's own branch pushed and built on", func(t *testing.T) {
		t.Parallel()
		d, fx, br, buildDir := own(t)
		run(t, buildDir, "push", "origin", br)
		writeAndCommit(t, buildDir, "more.txt", "built after the push", "a commit only the build lease has")
		if got := point(t, d, fx, fx.Ticket, br, false); got != buildLeasesCopy {
			t.Errorf("copy = %v, want the build lease's: it is ahead of origin's", got)
		}
	})
	t.Run("a ticket's own branch pushed and pushed on", func(t *testing.T) {
		t.Parallel()
		d, fx, br, buildDir := own(t)
		run(t, buildDir, "push", "origin", br)
		pushedOn(t, fx, br)
		if got := point(t, d, fx, fx.Ticket, br, false); got != originsCopy {
			t.Errorf("copy = %v, want origin's: the build lease's is behind it, so it is not the branch's head", got)
		}
	})
	t.Run("a ticket's own branch a publish squashed", func(t *testing.T) {
		t.Parallel()
		d, fx, br, buildDir := own(t)
		if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		_, err := pointErr(t, d, fx, fx.Ticket, br, false)
		wantAxiCode(t, err, "BRANCH_DIVERGED")
		if !strings.Contains(err.Error(), "the build lease at "+buildDir) {
			t.Errorf("the refusal %q does not name the build lease to integrate in", err)
		}
	})
	t.Run("a ticket's own branch pushed on, beside a leftover of an attempt", func(t *testing.T) {
		t.Parallel()
		d, fx, br, buildDir := own(t)
		run(t, buildDir, "push", "origin", br)
		pushedOn(t, fx, br)
		commitLeftover(t, buildDir, fx.Ticket)
		if got := point(t, d, fx, fx.Ticket, br, false); got != originsCopy {
			t.Errorf("copy = %v, want origin's: the lease's own commit is an attempt's leftover, not jig's work", got)
		}
	})
	t.Run("a ticket's own branch pushed, with no build lease here", func(t *testing.T) {
		t.Parallel()
		d, fx, br, buildDir := own(t)
		run(t, buildDir, "push", "origin", br)
		if err := os.RemoveAll(buildDir); err != nil {
			t.Fatalf("remove the build lease: %v", err)
		}
		if got := point(t, d, fx, fx.Ticket, br, false); got != originsCopy {
			t.Errorf("copy = %v, want origin's: it holds every commit jig built", got)
		}
	})
	t.Run("a ticket's own branch a publish squashed, with no build lease here", func(t *testing.T) {
		t.Parallel()
		d, fx, br, buildDir := own(t)
		if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := os.RemoveAll(buildDir); err != nil {
			t.Fatalf("remove the build lease: %v", err)
		}
		_, err := pointErr(t, d, fx, fx.Ticket, br, false)
		wantAxiCode(t, err, "BUILD_LEASE_MISSING")
	})
	t.Run("an adopted branch jig built nothing on", func(t *testing.T) {
		t.Parallel()
		d, fx := adopt(t)
		if got := point(t, d, fx, ticket, branch, true); got != originsCopy {
			t.Errorf("copy = %v, want origin's: the branch is the author's", got)
		}
	})
	t.Run("an adopted branch jig built on and has not pushed", func(t *testing.T) {
		t.Parallel()
		d, fx := adopt(t)
		jigBuildsOn(t, d, fx, ticket, branch)
		if got := point(t, d, fx, ticket, branch, true); got != buildLeasesCopy {
			t.Errorf("copy = %v, want the build lease's: jig's commit is only there", got)
		}
	})
	t.Run("an adopted branch jig built on and pushed", func(t *testing.T) {
		t.Parallel()
		d, fx := adopt(t)
		jigBuildsOn(t, d, fx, ticket, branch)
		run(t, buildLeaseOf(t, fx, ticket), "push", "origin", branch)
		if got := point(t, d, fx, ticket, branch, true); got != originsCopy {
			t.Errorf("copy = %v, want origin's: it holds jig's commit now", got)
		}
	})
}

// TestPublishRefusesAnAdoptedBranchTheAuthorPushedToWhileJigBuilt: jig's
// commit is in the build lease and unpushed when the author pushes again, so
// neither copy holds both. Publish judges the two the way the gate does
// (chooseBuiltCopy) and refuses with BRANCH_DIVERGED and the merge to do in
// the build lease, before it writes anything or pushes: origin's branch keeps
// the author's commit, and jig's is not lost.
func TestPublishRefusesAnAdoptedBranchTheAuthorPushedToWhileJigBuilt(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	built := jigBuildsOn(t, d, fx, ticket, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket}); err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	pushed := authorPush(t, fx, branch, "more.txt")
	journalBefore, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}

	_, err = Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	wantAxiCode(t, err, "BRANCH_DIVERGED")
	if !strings.Contains(err.Error(), "the build lease at "+buildLeaseOf(t, fx, ticket)) {
		t.Errorf("the refusal %q does not name the build lease to integrate in", err)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != pushed {
		t.Fatalf("origin's %s = %s after the refused publish, want the author's %s untouched", branch, got, pushed)
	}
	if got := run(t, buildLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); got != built {
		t.Errorf("the build lease is at %s after the refusal, want jig's %s untouched", got, built)
	}
	if after, _ := journal.Read(d.Store, ticket); len(after) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish", len(journalBefore), len(after))
	}
}

// TestPublishUpdatesThePRTheAuthorOpened: the author opened a pull request for
// their branch before handing it to jig. Publish updates that one - its body,
// through `gh pr edit` - and opens no second, and the branch it pushes to is
// the one that pull request is from.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishUpdatesThePRTheAuthorOpened(t *testing.T) {
	t.Parallel()
	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate --branch: %v", err)
	}
	logFile, _ := useGithubPRs(t, &d, openPullOf(t, branch), "")

	report, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGh(t, logFile)
	if ghCalls(calls, lookupCall(branch)...) != 1 ||
		ghCalls(calls, "pr", "edit", existingPR) != 1 || ghCalls(calls, "pr", "create") != 0 {
		t.Errorf("gh calls = %v, want one lookup for %s, one edit of %s and no create", calls, branch, existingPR)
	}
	if !report.PRUpdated["fixture-repo"] || report.PRURL["fixture-repo"] != existingPR {
		t.Errorf("report: PR %q updated=%v, want the author's %s updated", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"], existingPR)
	}
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", tip, originRef(t, fx.RepoRemote, "refs/heads/"+branch))
}

// TestPublishTitleOfAnAdoptedTicket: an adopted ticket's slices are the fixes
// its gate rounds queued, so the goal of the first slice names one of them, not
// the work. A slice records the gate round that queued it (FromGate), and the
// headline of the pull request and the ledger entry is the goal of the first
// slice that did not come from a gate round, whatever kind of ticket it is;
// with none, it is the ticket's own title, the one recorded when it was
// minted, and then the id. There is no adopted case in that rule: the
// slices say what they are.
func TestPublishTitleOfAnAdoptedTicket(t *testing.T) {
	t.Parallel()

	fixes := []store.Slice{
		{ID: "fix-1", Goal: "Fix these gate findings:\n\nthe off-by-one in Retry", FromGate: 1},
		{ID: "fix-2", Goal: "Fix these gate findings:\n\nthe missing test", FromGate: 2},
	}
	work := store.Slice{ID: "s-1", Goal: "Add Retry to alpha"}
	if got := consolidatedTitle("Add retry", "JIG-2", fixes); got != "Add retry" {
		t.Errorf("title of a ticket with only gate fixes = %q, want its recorded title", got)
	}
	if got := consolidatedTitle("", "JIG-2", fixes); got != "JIG-2" {
		t.Errorf("title of a ticket with only gate fixes and no recorded title = %q, want the id, not a fix's goal", got)
	}
	if got := consolidatedTitle("Add retry", "JIG-2", nil); got != "Add retry" {
		t.Errorf("title of a ticket with no slices = %q, want its recorded title", got)
	}
	if got := consolidatedTitle("Add retry", "JIG-2", append([]store.Slice{work}, fixes...)); got != "Add Retry to alpha" {
		t.Errorf("title of a ticket whose work precedes its fixes = %q, want the work's goal", got)
	}
	if got := consolidatedTitle("Add retry", "JIG-2", append(append([]store.Slice{}, fixes...), work)); got != "Add Retry to alpha" {
		t.Errorf("title of a ticket whose fixes precede its work = %q, want the work's goal", got)
	}
}

// TestPublishRefusesAnAdoptedBranchDeletedOnOrigin: a branch a ticket adopted
// is on origin by definition. When it is gone, publish refuses
// (BRANCH_NOT_FOUND) rather than cut it from the target and push a branch that
// lacks the author's code under the author's name; nothing is written or
// pushed.
func TestPublishRefusesAnAdoptedBranchDeletedOnOrigin(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate --branch: %v", err)
	}
	run(t, fx.RepoRemote, "branch", "-D", branch)
	journalBefore, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}

	_, err = Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	wantAxiCode(t, err, "BRANCH_NOT_FOUND")
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
		t.Fatalf("origin has %s = %s after the refused publish, want the branch still gone", branch, got)
	}
	if after, _ := journal.Read(d.Store, ticket); len(after) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish", len(journalBefore), len(after))
	}
}

// TestPublishRefusesWhenJigBuiltAnAdoptedBranchOnAnotherMachine: the journal
// records a commit jig built on the adopted branch that is still in another
// machine's build lease. Publishing from this one would ship a branch that
// leaves it out, so publish refuses (BUILD_LEASE_MISSING), as the gate does
// for the same copy, and its help names publish as the command to run on the
// machine that built it.
func TestPublishRefusesWhenJigBuiltAnAdoptedBranchOnAnotherMachine(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	jigBuildsOnAt(t, d, fx, t.TempDir(), ticket, branch)

	_, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	wantAxiCode(t, err, "BUILD_LEASE_MISSING")
	if !strings.Contains(strings.Join(err.(*axi.Error).Help, "\n"), "Run `jig publish "+ticket+"` on the machine that built them") {
		t.Errorf("the refusal's help = %q, want it to name `jig publish %s`", err.(*axi.Error).Help, ticket)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != tip {
		t.Fatalf("origin's %s = %s after the refused publish, want the author's %s untouched", branch, got, tip)
	}
}
