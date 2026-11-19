package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
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
//
// What the publish renders for an adopted ticket is the last step: What changed
// lists the author's own pre-adoption commit first, ahead of jig's (there are
// none registered as slices here, only a journaled build the test helper never
// appends to slices.yaml, so there is nothing of jig's to list after it - the
// author's commit alone is still the rule this ticket exists to pin). With no
// bullet of jig's to separate it from, the author-commit block gets no
// separator of its own: the section ends in one blank line before
// ## Verification, not two. The revalidation tier is none whether the target
// moved or not, as publish revalidates nothing.
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
	head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)

	step(t, "origin's branch is fast-forwarded by jig's fix and nothing else is touched", func(t *testing.T) {
		if report.Head["fixture-repo"] != head || head == tip {
			t.Fatalf("origin's %s = %s (report head %q), want it advanced past the author's %s to the head publish pushed", branch, head, report.Head["fixture-repo"], tip)
		}
		// Fast-forwarded from the author's tip by exactly jig's fix, under the sha
		// it had: the target moved after the round, and publish merged nothing,
		// squashed nothing and added no commit of its own.
		run(t, fx.RepoRemote, "merge-base", "--is-ancestor", tip, head)
		added := strings.Fields(run(t, fx.RepoRemote, "rev-list", "--first-parent", "--reverse", tip+".."+head))
		if len(added) != 1 || added[0] != built || head != built {
			t.Fatalf("publish added %v on top of the author's tip, want only jig's fix %s", added, built)
		}
		// The stale default-named branch was left alone, and main never moved.
		if got := originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(ticket)); got != mainBefore {
			t.Errorf("origin's %s = %s, want the stale branch untouched at %s", ticketBranch(ticket), got, mainBefore)
		}
		if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
			t.Errorf("origin's main = %s, want %s: publish never moves the target", got, mainBefore)
		}
	})

	step(t, "the store records the publish, and nothing that reshaped the branch", func(t *testing.T) {
		lines, err := journal.Read(d.Store, ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		seen := map[string]journal.Line{}
		for _, l := range lines {
			seen[l.Event] = l
		}
		for _, event := range []string{"changelog", "pr", "publish-done"} {
			if _, ok := seen[event]; !ok {
				t.Errorf("journal has no %q line after the publish", event)
			}
		}
		for _, event := range []string{"reconcile", "memorize", "squash"} {
			if l, ok := seen[event]; ok {
				t.Errorf("journal has %+v: publish reshapes nothing", l)
			}
		}
		if l := seen["pr"]; l.Commit != head || l.Branch != branch {
			t.Errorf("journal pr line = %+v, want the pushed head %s of %s", l, head, branch)
		}
		if ledger, err := os.ReadFile(filepath.Join(d.Store.Root, "ledger.md")); err != nil || !strings.Contains(string(ledger), "## "+ticket+" - Add retry") {
			t.Errorf("ledger.md lacks the ticket's entry titled with its recorded title (err %v):\n%s", err, ledger)
		}
	})

	step(t, "the build lease follows origin's copy, which holds everything jig built", func(t *testing.T) {
		buildLease := buildLeaseOf(t, fx, ticket)
		if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build); err != nil {
			t.Fatalf("build Acquire after the publish: %v", err)
		}
		if got := run(t, buildLease, "rev-parse", "HEAD"); got != head {
			t.Errorf("the build lease is at %s after the publish, want origin's copy %s", got, head)
		}
	})

	step(t, "the pull request body and review notes of an adopted ticket", func(t *testing.T) {
		body, notes := readPRFiles(t, d, ticket, "fixture-repo")

		wantBody := `## What

- Add retry

## Verification

Oracles green:

- test

Gate rounds:

- Round 1: clean
- Round 2: clean
`
		wantBody = withNoFinalRun(t, shortSHA(head), wantBody)
		if body != wantBody {
			t.Fatalf("pr/fixture-repo.md =\n%s\nwant\n%s", body, wantBody)
		}

		wantNotes := fmt.Sprintf(`# Review Notes

## Slices

- author a: attempt 1 (%s)

## Coverage

Touched files:

- alpha/alpha.go
- fix.txt

`, shortSHA(tip))
		if notes != wantNotes {
			t.Fatalf("pr/review-notes.md =\n%s\nwant\n%s", notes, wantNotes)
		}
	})
}

// TestPublishOfAnAdoptedBranchHoldsTheHeadToTheReview: the reviewed-head rule
// on an adopted branch, in two steps of one ticket.
//
// The head held to the review is the head publish ships. The target moves after
// a clean reviewer round, and publish merges nothing, so the branch on origin
// stays exactly the reviewed head: the move of the target is no refusal and no
// reason to rewrite anything.
//
// The author then pushes after a clean round, so origin's copy - the one
// publish would ship - is not the head the round reviewed: the publish is
// refused before it writes anything, and once a round has reviewed the branch
// as it is, it ships the author's new commit too.
func TestPublishOfAnAdoptedBranchHoldsTheHeadToTheReview(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)

	step(t, "a target that moved after the clean round changes nothing a publish ships", func(t *testing.T) {
		first := recordedRound(t, d, GateOpts{Ticket: ticket, Branch: branch}, cleanReport)
		if first.Verdict != "clean" || first.ReviewedSHA["fixture-repo"] != tip {
			t.Fatalf("Gate --branch: verdict %q, reviewed %q, want a clean reviewer round over the author's %s", first.Verdict, first.ReviewedSHA["fixture-repo"], tip)
		}
		advanceTarget(t, fx)

		report, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true})
		if err != nil {
			t.Fatalf("Publish after a clean reviewer round and a moved target: %v", err)
		}
		if report.Tier != "none" {
			t.Errorf("revalidation tier = %q, want none: publish revalidates nothing", report.Tier)
		}
		if head := originRef(t, fx.RepoRemote, "refs/heads/"+branch); head != tip || report.Head["fixture-repo"] != tip {
			t.Fatalf("origin's %s = %s (report head %q), want the reviewed %s unchanged", branch, head, report.Head["fixture-repo"], tip)
		}
	})

	step(t, "the author's push after the round is refused until a round has reviewed it", func(t *testing.T) {
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

		second := recordedRound(t, d, GateOpts{Ticket: ticket}, cleanReport)
		if second.ReviewedSHA["fixture-repo"] != pushed {
			t.Fatalf("Gate round 2: reviewed %q, want a round over the author's new commit %s", second.ReviewedSHA["fixture-repo"], pushed)
		}
		if _, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true}); err != nil {
			t.Fatalf("Publish after a round over the branch as it is: %v", err)
		}
		head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
		run(t, fx.RepoRemote, "merge-base", "--is-ancestor", pushed, head)
	})
}

// pointAt points a publish lease at the copy of branch the way publish does, after
// dropping the lease's own copy (publish re-points it at the copy it ships, so
// what an earlier call left there must not stand in a later one's way), and
// returns the copy pointAtTicketBranch chose, or its refusal.
func pointAt(t *testing.T, d Deps, fx *fixture.Fixture, ticket, branch string, adopted bool) (branchCopy, error) {
	t.Helper()
	if dir, err := pool.Dir(fx.Home, "fixture-repo", ticket, pool.Publish); err != nil {
		t.Fatalf("resolve the publish lease: %v", err)
	} else if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove the publish lease: %v", err)
	}
	lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Publish)
	if err != nil {
		t.Fatalf("acquire the publish lease: %v", err)
	}
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	return pointAtTicketBranch(d, lease.Dir, "fixture-repo", ticket, branch, adopted, journal.BuiltCommits(lines), "publish")
}

// wantCopy requires pointAtTicketBranch to choose want.
func wantCopy(t *testing.T, d Deps, fx *fixture.Fixture, ticket, branch string, adopted bool, want branchCopy, why string) {
	t.Helper()
	got, err := pointAt(t, d, fx, ticket, branch, adopted)
	if err != nil {
		t.Fatalf("pointAtTicketBranch: %v", err)
	}
	if got != want {
		t.Errorf("copy = %v, want %v: %s", got, want, why)
	}
}

// TestPublishUpdatesThePRTheAuthorOpened: the author opened a pull request for
// their branch before handing it to jig. Publish updates that one - its body,
// through `gh pr edit` - and opens no second, and the branch it pushes to is
// the one that pull request is from.
//
// jig built nothing on the branch: there is no build lease on this machine and
// none is needed - the copy publish ships is origin's - and nothing moves the
// branch: it is on origin already, as the author left it, and publish adds no
// commit.
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
	logFile, _ := useGithubHost(t, &d, openPullOf(t, branch), "")

	report, err := Publish(d, PublishOpts{Ticket: ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	step(t, "the author's pull request is updated and no second is opened", func(t *testing.T) {
		calls := loggedGh(t, logFile)
		if ghCalls(calls, lookupCall(branch)...) != 1 ||
			ghCalls(calls, "pr", "edit", existingPR) != 1 || ghCalls(calls, "pr", "create") != 0 {
			t.Errorf("gh calls = %v, want one lookup for %s, one edit of %s and no create", calls, branch, existingPR)
		}
		if !report.PRUpdated["fixture-repo"] || report.PRURL["fixture-repo"] != existingPR {
			t.Errorf("report: PR %q updated=%v, want the author's %s updated", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"], existingPR)
		}
		run(t, fx.RepoRemote, "merge-base", "--is-ancestor", tip, originRef(t, fx.RepoRemote, "refs/heads/"+branch))
	})

	step(t, "a branch jig built nothing on needs no build lease and is not moved", func(t *testing.T) {
		head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
		if report.Head["fixture-repo"] != head || head != tip {
			t.Fatalf("origin's %s = %s (report head %q), want the author's %s, unchanged", branch, head, report.Head["fixture-repo"], tip)
		}
		buildLease, perr := pool.Dir(fx.Home, "fixture-repo", ticket, pool.Build)
		if perr != nil {
			t.Fatal(perr)
		}
		if _, err := os.Stat(buildLease); !os.IsNotExist(err) {
			t.Errorf("a build lease exists at %s (stat err %v): publishing a branch jig built nothing on needs none", buildLease, err)
		}
	})
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
