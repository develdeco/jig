package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
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

		wantBody := fmt.Sprintf(`## What changed

- author a: attempt 1 (%s)

## Verification

Oracles:

- test

Revalidation tier: none

Findings: 0 fixed, 0 dismissed, 0 noted, 0 asked. See the pull request's first comment for detail.
`, shortSHA(tip))
		if body != wantBody {
			t.Fatalf("pr/fixture-repo.md =\n%s\nwant\n%s", body, wantBody)
		}

		wantNotes := `# Review Notes

## Coverage

Touched files:

- alpha/alpha.go
- fix.txt

`
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
	src := newReviewerGateSource(reviewerBackend(t))

	step(t, "a target that moved after the clean round changes nothing a publish ships", func(t *testing.T) {
		first, err := Gate(d, src, GateOpts{Ticket: ticket, Branch: branch})
		if err != nil || first.Verdict != "clean" || first.ReviewedSHA["fixture-repo"] != tip {
			t.Fatalf("Gate --branch: verdict %q, reviewed %q, err %v, want a clean reviewer round over the author's %s", first.Verdict, first.ReviewedSHA["fixture-repo"], err, tip)
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

		second, err := Gate(d, src, GateOpts{Ticket: ticket})
		if err != nil || second.ReviewedSHA["fixture-repo"] != pushed {
			t.Fatalf("Gate round 2: reviewed %q, err %v, want a round over the author's new commit %s", second.ReviewedSHA["fixture-repo"], err, pushed)
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

// TestPointAtTicketBranchSaysWhoseCopyItChose: the function the gate and
// publish share reports which copy of the branch the lease was left at, the
// build lease's or origin's, because what publish's refusals tell the operator
// to do depends on it. The rule is the branch's, not the kind of ticket's: an
// adopted branch jig built nothing on is origin's, the author's; any other
// branch is the build lease's while origin has no copy of it (the ticket's own
// jig/<ticket> until a publish pushes it) or while its commits are unpushed,
// and origin's once it holds them, whether the human or a publish pushed them.
// (The cases a publish pushed are steps of TestPublishFullChain, which has one.)
//
// What it reads is the build lease, origin and the journal's built commits, so
// no round is needed: a ticket's own branch is one build and its states are
// steps of the one ticket, in an order in which each leaves the pool as the next
// expects it; an adopted branch is the author's push and the states jig's own
// commit and the author's put it in.
func TestPointAtTicketBranchSaysWhoseCopyItChose(t *testing.T) {
	t.Parallel()

	t.Run("a ticket's own branch", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d := newDeps(t, fx)
		driveBuild(t, fx, "rung-a")
		br, buildDir := ticketBranch(fx.Ticket), buildLeaseDir(t, fx)
		// pushedOn adds a commit to origin's copy of br from another clone.
		pushedOn := func(t *testing.T) {
			t.Helper()
			other := cloneFrom(t, fx.RepoRemote)
			run(t, other, "checkout", br)
			writeAndCommit(t, other, "more.txt", "added on origin", "a commit only origin has")
			run(t, other, "push", "origin", br)
		}

		step(t, "built and not pushed", func(t *testing.T) {
			wantCopy(t, d, fx, fx.Ticket, br, false, buildLeasesCopy, "the ticket's own branch lives in the build lease until a publish pushes it")
		})
		step(t, "pushed as it is", func(t *testing.T) {
			run(t, buildDir, "push", "origin", br)
			wantCopy(t, d, fx, fx.Ticket, br, false, originsCopy, "origin holds every commit the build lease does")
		})
		step(t, "pushed and built on", func(t *testing.T) {
			writeAndCommit(t, buildDir, "built.txt", "built after the push", "a commit only the build lease has")
			wantCopy(t, d, fx, fx.Ticket, br, false, buildLeasesCopy, "the build lease is ahead of origin's")
		})
		step(t, "pushed and pushed on", func(t *testing.T) {
			run(t, buildDir, "push", "origin", br)
			pushedOn(t)
			wantCopy(t, d, fx, fx.Ticket, br, false, originsCopy, "the build lease's is behind it, so it is not the branch's head")
		})
		step(t, "pushed on, beside a leftover of an attempt", func(t *testing.T) {
			commitLeftover(t, buildDir, fx.Ticket)
			wantCopy(t, d, fx, fx.Ticket, br, false, originsCopy, "the lease's own commit is an attempt's leftover, not jig's work")
		})
		step(t, "pushed, with no build lease here", func(t *testing.T) {
			if err := os.RemoveAll(buildDir); err != nil {
				t.Fatalf("remove the build lease: %v", err)
			}
			wantCopy(t, d, fx, fx.Ticket, br, false, originsCopy, "it holds every commit jig built")
		})
	})

	t.Run("an adopted branch", func(t *testing.T) {
		t.Parallel()
		const ticket, branch = "JIG-2", "add-retry"
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d := newDeps(t, fx)
		newAdoptTicket(t, d, ticket)
		authorBranch(t, fx, branch)
		var built, pushed string

		step(t, "jig built nothing on it", func(t *testing.T) {
			wantCopy(t, d, fx, ticket, branch, true, originsCopy, "the branch is the author's")
		})
		step(t, "jig built on it and has not pushed", func(t *testing.T) {
			built = jigBuildsOn(t, d, fx, ticket, branch)
			wantCopy(t, d, fx, ticket, branch, true, buildLeasesCopy, "jig's commit is only there")
		})
		// jig's commit is in the build lease and unpushed when the author pushes
		// again, so neither copy holds both. Publish judges the two the way the
		// gate does (chooseBuiltCopy) and refuses with BRANCH_DIVERGED and the
		// merge to do in the build lease, before it writes anything or pushes:
		// origin's branch keeps the author's commit, and jig's is not lost.
		step(t, "the author pushed on while jig's commit is unpushed", func(t *testing.T) {
			pushed = authorPush(t, fx, branch, "more.txt")
			_, err := pointAt(t, d, fx, ticket, branch, true)
			wantAxiCode(t, err, "BRANCH_DIVERGED")
			if !strings.Contains(err.Error(), "the build lease at "+buildLeaseOf(t, fx, ticket)) {
				t.Errorf("the refusal %q does not name the build lease to integrate in", err)
			}
			if got := run(t, buildLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); got != built {
				t.Errorf("the build lease is at %s after the refusal, want jig's %s untouched", got, built)
			}
			if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != pushed {
				t.Fatalf("origin's %s = %s after the refusal, want the author's %s untouched", branch, got, pushed)
			}
		})
		step(t, "jig built on it and pushed", func(t *testing.T) {
			buildLease := buildLeaseOf(t, fx, ticket)
			run(t, buildLease, "fetch", "origin")
			run(t, buildLease, "merge", "--no-edit", "origin/"+branch)
			run(t, buildLease, "push", "origin", branch)
			wantCopy(t, d, fx, ticket, branch, true, originsCopy, "it holds jig's commit now")
		})
		// The journal records a commit jig built that is still in another
		// machine's build lease. Publishing from this one would ship a branch that
		// leaves it out, so it is refused (BUILD_LEASE_MISSING), as the gate does
		// for the same copy, and the help names publish as the command to run on
		// the machine that built it.
		step(t, "jig built on it on another machine and has not pushed", func(t *testing.T) {
			lease, err := pool.Acquire(t.TempDir(), "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build)
			if err != nil {
				t.Fatalf("acquire the other machine's build lease: %v", err)
			}
			if err := os.WriteFile(filepath.Join(lease.Dir, "fix2.txt"), []byte("the other machine's fix\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			run(t, lease.Dir, "add", "-A")
			if _, err := gitx.RunEnv(lease.Dir, buildGitEnv, "commit", "-m", ticket+" fix-2: the other machine's fix"); err != nil {
				t.Fatalf("commit the other machine's fix: %v", err)
			}
			journalBuilt(t, d.Store, ticket, "fix-2", run(t, lease.Dir, "rev-parse", "HEAD"), 1)

			_, err = pointAt(t, d, fx, ticket, branch, true)
			wantAxiCode(t, err, "BUILD_LEASE_MISSING")
			if !strings.Contains(strings.Join(err.(*axi.Error).Help, "\n"), "Run `jig publish "+ticket+"` on the machine that built them") {
				t.Errorf("the refusal's help = %q, want it to name `jig publish %s`", err.(*axi.Error).Help, ticket)
			}
		})
	})
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
		if _, err := os.Stat(buildLeaseOf(t, fx, ticket)); !os.IsNotExist(err) {
			t.Errorf("a build lease exists at %s (stat err %v): publishing a branch jig built nothing on needs none", buildLeaseOf(t, fx, ticket), err)
		}
	})
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
