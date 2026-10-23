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

// reviewedInTheClone is cloneBuilt taken through one clean, independent round:
// the ticket is ready to publish, its branch lives in the operator's clone
// only, and the head the round reviewed is returned.
func reviewedInTheClone(t *testing.T) (*fixture.Fixture, Deps, string) {
	t.Helper()
	fx, d, head := cloneBuilt(t)
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if _, err := GateFinish(d, p, cleanReport(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	return fx, d, head
}

// publishPrCalls counts the calls the fake gh logged that open or edit a pull
// request.
func publishPrCalls(t *testing.T, logFile string) int {
	t.Helper()
	n := 0
	for _, argv := range loggedGh(t, logFile) {
		if len(argv) >= 2 && argv[0] == "pr" && (argv[1] == "create" || argv[1] == "edit") {
			n++
		}
	}
	return n
}

// TestPublishBodyRendersAndChangesNothingElse: --body writes the pull request
// body and the review notes for the reviewed head, journals a pr-body line, and
// that is all. The branch exists only in the operator's clone, and no lease is
// made, nothing is pushed, and the host is not called.
func TestPublishBodyRendersAndChangesNothingElse(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	logFile, _ := useGithubHost(t, &d, "", "")
	gateLease := gateLeaseOf(t, fx, fx.Ticket)
	gateHead := run(t, gateLease, "rev-parse", "HEAD")

	report, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if report.Head != head || report.Branch != ticketBranch(fx.Ticket) || report.Target != "main" || report.Repo != "fixture-repo" {
		t.Fatalf("report = %+v, want the reviewed head %s of %s into main", report, head, ticketBranch(fx.Ticket))
	}
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody)))
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	for _, want := range []string{"## What changed", "## Verification", "Reviewed head:", head[:7]} {
		if !strings.Contains(string(body), want) {
			t.Errorf("pr body lacks %q:\n%s", want, body)
		}
	}
	if _, err := os.Stat(filepath.Join(d.Store.Root, filepath.FromSlash(report.ReviewNotes))); err != nil {
		t.Errorf("no review notes: %v", err)
	}
	if _, err := os.Stat(publishLeaseDir(t, fx)); err == nil {
		t.Error("--body made a publish lease")
	}
	if got := run(t, gateLease, "rev-parse", "HEAD"); got != gateHead {
		t.Errorf("the gate lease moved from %s to %s", gateHead, got)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+report.Branch); got != "" {
		t.Errorf("origin has %s at %s after --body", report.Branch, got)
	}
	if calls := loggedGh(t, logFile); len(calls) != 0 {
		t.Errorf("--body called gh: %v", calls)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	var bodyLine journal.Line
	for _, l := range lines {
		switch l.Event {
		case journal.EventPrBody:
			bodyLine = l
		case journal.EventPr, journal.EventPublishDone, journal.EventChangelog:
			t.Errorf("--body journaled %+v", l)
		}
	}
	if bodyLine.Commit != head || bodyLine.Branch != report.Branch || bodyLine.Ref != report.PRBody {
		t.Errorf("pr-body line = %+v, want the head, the branch and the body's path", bodyLine)
	}
}

// TestPublishBodyHoldsTheHeadToWhereTheBranchIs: a body for a head that is
// neither the tip of the clone's branch nor origin's describes a pull request
// nobody will open; it is refused before anything is written.
func TestPublishBodyHoldsTheHeadToWhereTheBranchIs(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	writeAndCommit(t, fx.RepoDir, "late.txt", "after the round", "a commit after the round")
	linesBefore, _ := journal.Read(d.Store, fx.Ticket)

	_, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	if linesAfter, _ := journal.Read(d.Store, fx.Ticket); len(linesAfter) != len(linesBefore) {
		t.Fatalf("the refused body journaled %d line(s)", len(linesAfter)-len(linesBefore))
	}

	// Pushed to origin and reviewed there, the head is origin's tip: a body for
	// it is fine even though the clone has moved on since.
	run(t, fx.RepoDir, "push", "origin", ticketBranch(fx.Ticket)+"~1:refs/heads/"+ticketBranch(fx.Ticket))
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("PublishBody for the head origin has: %v", err)
	}
}

// TestPublishRecordPR: a pull request an agent opened is recorded once the
// host says it is from the ticket's branch and at the reviewed head; the pr
// line carries its URL, branch and commit, and the publish-done line follows.
// A pull request from another branch or at another commit is refused with
// nothing journaled, and a URL that is no pull request's is refused before the
// host is asked.
func TestPublishRecordPR(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	logFile, addEnv := useGithubHost(t, &d, "", "")
	branch := ticketBranch(fx.Ticket)
	journaledPR := func() bool {
		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if l.Event == journal.EventPr || l.Event == journal.EventPublishDone {
				return true
			}
		}
		return false
	}

	_, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, "https://example.com/not/a/pull", nil)
	wantAxiCode(t, err, "PR_URL_INVALID")

	addEnv("GH_STUB_HEAD_REF=some-other-branch", "GH_STUB_HEAD_OID="+head)
	_, err = PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	wantAxiCode(t, err, "PR_HEAD_MISMATCH")

	addEnv("GH_STUB_HEAD_REF="+branch, "GH_STUB_HEAD_OID="+strings.Repeat("0", 40))
	_, err = PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	if journaledPR() {
		t.Fatal("a refused pull request was journaled")
	}

	addEnv("GH_STUB_HEAD_OID=" + head)
	report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	if !report.Verified || report.URL != prURL || report.Branch != branch || report.Commit != head {
		t.Fatalf("report = %+v, want %s verified, from %s at %s", report, prURL, branch, head)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	var prLine journal.Line
	events := map[string]bool{}
	for _, l := range lines {
		events[l.Event] = true
		if l.Event == journal.EventPr {
			prLine = l
		}
	}
	if prLine.URL != prURL || prLine.Branch != branch || prLine.Commit != head || prLine.Outcome != "recorded" || !events[journal.EventPublishDone] || !events[journal.EventChangelog] {
		t.Errorf("pr line = %+v, events = %v, want the pr line, the changelog line and publish-done", prLine, events)
	}
	if n := publishPrCalls(t, logFile); n != 0 {
		t.Errorf("--pr opened or edited a pull request %d time(s)", n)
	}
}

// TestPublishRecordPRWithNoHostRecordsItUnverified: a repo whose remote is no
// pull-request host has nothing to ask, so the pull request is recorded as the
// agent gave it, said so in the outcome and a warning.
func TestPublishRecordPRWithNoHostRecordsItUnverified(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	var warnings []string
	report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, "https://git.example/o/r/pull/3", func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	if report.Verified || report.Commit != head || len(warnings) != 1 || !strings.Contains(warnings[0], "without checking") {
		t.Fatalf("report = %+v, warnings = %q, want it unverified at the reviewed head with one warning", report, warnings)
	}
	lines, _ := journal.Read(d.Store, fx.Ticket)
	for _, l := range lines {
		if l.Event == journal.EventPr && l.Outcome != "recorded:unverified" {
			t.Errorf("pr line = %+v, want recorded:unverified", l)
		}
	}
}

// TestPublishApproveAndTheConfirmation: a person's approval of the reviewed
// head stands in for the confirmation a bare publish asks; an agent's is
// recorded as an agent's and does not; one for another head does not either.
func TestPublishApproveAndTheConfirmation(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	asked := 0
	d.Confirm = func(string, string, string, bool) bool { asked++; return false }
	d.GuardedPush = func(string, string, string, bool) error { return nil }

	if report, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "agent"); err != nil || report.Head != head || report.Provenance != "agent" {
		t.Fatalf("PublishApprove(agent) = %+v (%v)", report, err)
	}
	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "PUBLISH_DECLINED")
	if asked != 1 {
		t.Fatalf("an agent's approval replaced the question: asked %d time(s), want 1", asked)
	}

	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "person"); err != nil {
		t.Fatalf("PublishApprove(person): %v", err)
	}
	var confirmed bool
	d.GuardedPush = func(_, _, _ string, c bool) error { confirmed = c; return nil }
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Publish after a person approved: %v", err)
	}
	if asked != 1 || !confirmed {
		t.Fatalf("after a person's approval: asked %d time(s) in all, push confirmed %v; want no new question and a confirmed push", asked, confirmed)
	}

	// An approval is of a head: it approves no other.
	if lines, _ := journal.Read(d.Store, fx.Ticket); !approvedByPerson(lines, head) || approvedByPerson(lines, strings.Repeat("e", 40)) {
		t.Errorf("approvedByPerson over the journal = %v for the reviewed head, %v for another", approvedByPerson(lines, head), approvedByPerson(lines, strings.Repeat("e", 40)))
	}
}

// TestPublishShipsTheClonesCopyUnchanged: the branch only the operator's clone
// has is pushed from the publish lease as it stands - the same sha, no commit
// on top - and a clone that moved on since the round is refused.
func TestPublishShipsTheClonesCopyUnchanged(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	branch := ticketBranch(fx.Ticket)
	logFile, _ := useGithubHost(t, &d, "", "")

	// A clone that moved on is not the head the round reviewed.
	writeAndCommit(t, fx.RepoDir, "late.txt", "after the round", "a commit after the round")
	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
		t.Fatalf("a refused publish pushed %s to origin", got)
	}
	if n := publishPrCalls(t, logFile); n != 0 {
		t.Fatalf("a refused publish opened a pull request")
	}
	run(t, fx.RepoDir, "reset", "--hard", head)

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Head["fixture-repo"] != head || originRef(t, fx.RepoRemote, "refs/heads/"+branch) != head {
		t.Fatalf("report head %q, origin's %s = %q, want the clone's %s", report.Head["fixture-repo"], branch, originRef(t, fx.RepoRemote, "refs/heads/"+branch), head)
	}
	if got := run(t, fx.RepoRemote, "log", "--format=%H", branch); got != run(t, fx.RepoDir, "log", "--format=%H", branch) {
		t.Fatalf("origin's history of %s differs from the clone's", branch)
	}
	if n := publishPrCalls(t, logFile); n != 1 {
		t.Fatalf("pull requests opened = %d, want 1", n)
	}
	if lease, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Publish); err != nil {
		t.Fatal(err)
	} else if got := run(t, lease, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the publish lease is at %s, want the reviewed head %s", got, head)
	}
}
