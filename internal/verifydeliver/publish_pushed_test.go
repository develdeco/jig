package verifydeliver

import (
	"os"
	"path/filepath"
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
//
// The branch already has an open pull request, so the one publish this test
// makes is also the update of it, and its steps read what that publish did:
//
//   - it replaces the open pull request's body and opens none, through the fake
//     gh: the lookup asks for the pull request from the ticket's branch into the
//     target, `gh pr edit` gets the body file publish wrote and the URL found,
//     and there is no `gh pr create`. The report and the journal say the pull
//     request was updated, not opened. The branch is on origin (pushed from the
//     build lease), so the push is a fast-forward and the body is the only thing
//     that changes on the pull request;
//   - it posts the review notes as a comment on the pull request it updates,
//     exactly once, with pr/review-notes.md as its --body-file;
//   - the publish lease's own copy of the branch is disposable - Publish
//     re-points it at the copy it ships - so a copy left by an earlier attempt
//     must not refuse the acquire. Here the lease kept a commit of its own from a
//     publish that stopped before its push, and the branch has since reached
//     origin another way: the two have diverged, and the sync rule would refuse
//     the lease over a commit nobody keeps.
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
	// An earlier publish's leftover: the publish lease holds the branch with a
	// commit of its own, and origin has no such branch, so nothing compares.
	stale, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Publish)
	if err != nil {
		t.Fatalf("pool.Acquire publish lease: %v", err)
	}
	writeAndCommit(t, stale.Dir, "leftover.txt", "an earlier attempt", "an earlier publish's leftover")
	// The branch reaches origin from the build lease.
	run(t, buildDir, "push", "origin", branch)
	pushed := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	logFile, _ := useGithubHost(t, &d, openPullOf(t, branch), "")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish of a branch already on origin: %v", err)
	}
	calls := loggedGh(t, logFile)

	step(t, "the branch is published as it stands", func(t *testing.T) {
		if tip := originRef(t, fx.RepoRemote, "refs/heads/"+branch); tip != pushed || report.Head["fixture-repo"] != pushed {
			t.Fatalf("origin's %s = %s (report head %q), want the head it had, %s, unchanged", branch, tip, report.Head["fixture-repo"], pushed)
		}
		if all := strings.Fields(run(t, fx.RepoRemote, "rev-list", "--reverse", "main.."+branch)); strings.Join(all, " ") != strings.Join(built, " ") {
			t.Fatalf("origin's %s holds %v, want the commits already pushed, unchanged: %v", branch, all, built)
		}
	})

	step(t, "the open pull request is updated instead of a second one being opened", func(t *testing.T) {
		if n := ghCalls(calls, lookupCall(branch)...); n != 1 {
			t.Errorf("the lookup of %s's open pull request into main called %d time(s), want once: %v", branch, n, calls)
		}
		body := filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"]))
		if n := ghCalls(calls, "pr", "edit", existingPR, "--repo", "owner/fixture-repo", "--body-file", body); n != 1 {
			t.Errorf("gh pr edit %s --body-file %s called %d time(s), want once: %v", existingPR, body, n, calls)
		}
		if n := ghCalls(calls, "pr", "create"); n != 0 {
			t.Errorf("gh pr create called %d time(s): a second pull request was opened beside the existing one: %v", n, calls)
		}
		if report.PRURL["fixture-repo"] != existingPR || !report.PRUpdated["fixture-repo"] {
			t.Errorf("report: PR %q updated=%v, want the existing %s updated", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"], existingPR)
		}
		if data, err := os.ReadFile(body); err != nil || strings.TrimSpace(string(data)) == "" {
			t.Errorf("the body file %s that pr edit was given is missing or empty (err %v)", body, err)
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
		if len(prLines) != 1 || prLines[0].Outcome != "updated" || prLines[0].Commit != pushed {
			t.Errorf("journal pr lines = %+v, want one recording updated at the head %s", prLines, pushed)
		}
	})

	step(t, "the review notes are posted as a comment on the pull request it updates", func(t *testing.T) {
		comments := reviewNotesCommentCalls(calls)
		if len(comments) != 1 {
			t.Fatalf("pr comment called %d time(s), want exactly once: %v", len(comments), calls)
		}
		wantReviewNotesBodyFile(t, comments[0])
	})

	step(t, "the publish lease's stale copy is dropped, not shipped", func(t *testing.T) {
		if out := run(t, fx.RepoRemote, "ls-tree", "-r", "--name-only", branch); strings.Contains(out, "leftover.txt") {
			t.Fatalf("origin's %s carries the stale lease's leftover.txt: publish shipped the stale copy", branch)
		}
	})
}

// TestPublishRepublishesAPublishedTicket: a ticket that was published can be
// published again. The branch publish pushed is the reviewed head, exactly the
// build lease's copy, so the second publish finds the two in step, pushes
// nothing new, and journals the pull request again at the same head. Nothing
// already on origin is rewritten.
//
// The second publish of a ticket leaves ledger.md with one entry of it and the
// contract index with one line, as the first left them (TestRepublishReplacesTheLedgerEntryInPlace
// is the rule of the entry).
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
	ledger, index := readStoreDoc(t, d.Store.LedgerPath()), readStoreDoc(t, d.Store.ContractIndexPath(""))
	if n := strings.Count(ledger, "## "+fx.Ticket+" - "); n != 1 {
		t.Fatalf("test setup: ledger.md has %d entries of %s after the first publish, want 1:\n%s", n, fx.Ticket, ledger)
	}

	second, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("re-Publish: %v", err)
	}

	step(t, "the branch on origin is not rewritten, and the pull request is journaled again at the same head", func(t *testing.T) {
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
	})

	step(t, "the ledger and the contract index keep one entry of the ticket", func(t *testing.T) {
		if got := readStoreDoc(t, d.Store.LedgerPath()); got != ledger {
			t.Errorf("ledger.md after the re-publish =\n%s\nwant it as the first publish left it:\n%s", got, ledger)
		}
		if got := readStoreDoc(t, d.Store.ContractIndexPath("")); got != index {
			t.Errorf("contract-index.md after the re-publish =\n%s\nwant it as the first publish left it:\n%s", got, index)
		}
	})
}
