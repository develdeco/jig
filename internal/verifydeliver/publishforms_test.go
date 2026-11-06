package verifydeliver

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
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
	if len(report.Repos) != 1 {
		t.Fatalf("report = %+v, want a body for the one repo", report)
	}
	repo := report.Repos[0]
	if repo.Head != head || report.Branch != ticketBranch(fx.Ticket) || repo.Target != "main" || repo.Repo != "fixture-repo" {
		t.Fatalf("report = %+v, want the reviewed head %s of %s into main", report, head, ticketBranch(fx.Ticket))
	}
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(repo.PRBody)))
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	for _, want := range []string{"## What\n", "## Verification", "at the reviewed head " + head[:7]} {
		if !strings.Contains(string(body), want) {
			t.Errorf("pr body lacks %q:\n%s", want, body)
		}
	}
	if _, err := os.Stat(filepath.Join(d.Store.Root, filepath.FromSlash(repo.ReviewNotes))); err != nil {
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
	if bodyLine.Commit != head || bodyLine.Branch != report.Branch || bodyLine.Ref != repo.PRBody {
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

// TestPublishRecordPRPostsTheReviewNotes: a pull request recorded with --pr
// after --body carries the ticket's review notes as a comment, the same call
// the bare publish makes, so a session that opened the pull request itself
// never posts them by hand.
func TestPublishRecordPRPostsTheReviewNotes(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	logFile, addEnv := useGithubHost(t, &d, "", "")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if _, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil); err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	comments := reviewNotesCommentCalls(loggedGh(t, logFile))
	if len(comments) != 1 {
		t.Fatalf("--pr posted %d comment(s), want 1: the review notes", len(comments))
	}
	wantReviewNotesBodyFile(t, comments[0])
}

// reviewNotesLines are the journal's review-notes lines of ticket.
func reviewNotesLines(t *testing.T, d Deps, ticket string) []journal.Line {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range lines {
		if l.Event == journal.EventReviewNotes {
			out = append(out, l)
		}
	}
	return out
}

// TestPublishRecordPRPostsReviewNotesOncePerContent: a posted comment is
// journaled as a review-notes line with the pull request's url and the notes'
// sha256, and a second --pr with the same notes posts nothing, while notes
// that changed post again.
func TestPublishRecordPRPostsReviewNotesOncePerContent(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	logFile, addEnv := useGithubHost(t, &d, "", "")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	notesFile := filepath.Join(d.Store.TicketDir(fx.Ticket), "pr", "review-notes.md")
	notes, err := os.ReadFile(notesFile)
	if err != nil {
		t.Fatal(err)
	}

	report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	if report.ReviewNotes.Status != ReviewNotesPosted || report.ReviewNotes.File != notesFile {
		t.Fatalf("report.ReviewNotes = %+v, want posted from %s", report.ReviewNotes, notesFile)
	}
	posted := reviewNotesLines(t, d, fx.Ticket)
	if len(posted) != 1 || posted[0].URL != prURL || posted[0].Summary != fmt.Sprintf("%x", sha256.Sum256(notes)) {
		t.Fatalf("review-notes lines = %+v, want one for %s carrying the notes' sha256", posted, prURL)
	}

	report, err = PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR again: %v", err)
	}
	if report.ReviewNotes.Status != ReviewNotesAlreadyPosted || len(reviewNotesCommentCalls(loggedGh(t, logFile))) != 1 {
		t.Fatalf("second --pr: %+v, %d comment(s), want already posted and still one comment", report.ReviewNotes, len(reviewNotesCommentCalls(loggedGh(t, logFile))))
	}

	if err := os.WriteFile(notesFile, append(notes, []byte("\nA new round.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR with changed notes: %v", err)
	}
	if report.ReviewNotes.Status != ReviewNotesPosted || len(reviewNotesCommentCalls(loggedGh(t, logFile))) != 2 || len(reviewNotesLines(t, d, fx.Ticket)) != 2 {
		t.Fatalf("changed notes: %+v, %d comment(s), want posted again as a second comment", report.ReviewNotes, len(reviewNotesCommentCalls(loggedGh(t, logFile))))
	}
}

// TestPublishRecordPRRecordsWhenTheReviewNotesCannotBePosted: a host that
// refuses the comment makes a warning and a report that names the file to
// post by hand; the pull request is recorded and nothing says the notes were
// posted.
func TestPublishRecordPRRecordsWhenTheReviewNotesCannotBePosted(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	_, addEnv := useGithubHost(t, &d, "", "pr comment")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	rendered, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	// The pull request is the render, so its readback has nothing to warn of.
	render, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(rendered.Repos[0].PRBody)))
	if err != nil {
		t.Fatal(err)
	}
	addEnv("GH_STUB_BODY="+string(render), "GH_STUB_TITLE="+rendered.Repos[0].Title)
	var warnings []string
	report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	if !report.Verified || report.ReviewNotes.Status != ReviewNotesNotPosted || !strings.Contains(report.ReviewNotes.Reason, "pr comment") || report.ReviewNotes.File == "" {
		t.Fatalf("report = %+v, want a recorded pull request and review notes not posted for the host's error", report)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], report.ReviewNotes.File) {
		t.Errorf("warnings = %q, want one naming the notes file", warnings)
	}
	lines, _ := journal.Read(d.Store, fx.Ticket)
	if !hasEvent(lines, journal.EventPr) || !hasEvent(lines, journal.EventPublishDone) || len(reviewNotesLines(t, d, fx.Ticket)) != 0 {
		t.Errorf("journal = %+v, want the pr and publish-done lines and no review-notes line", lines)
	}
}

// TestPublishOfAnOpenPullRequestPostsTheSameReviewNotesOnce: a bare publish
// that updates the pull request again for the same head posts the review notes
// once, not on every run.
func TestPublishOfAnOpenPullRequestPostsTheSameReviewNotesOnce(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	logFile, _ := useGithubHost(t, &d, openPullOf(t, ticketBranch(fx.Ticket)), "")
	d.GuardedPush = func(string, string, string, bool) error { return nil }
	for i := 0; i < 2; i++ {
		report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		if err != nil {
			t.Fatalf("Publish %d: %v", i+1, err)
		}
		want := ReviewNotesPosted
		if i == 1 {
			want = ReviewNotesAlreadyPosted
		}
		if got := report.ReviewNotes["fixture-repo"].Status; got != want {
			t.Errorf("publish %d: review notes %q, want %q", i+1, got, want)
		}
	}
	if n := len(reviewNotesCommentCalls(loggedGh(t, logFile))); n != 1 {
		t.Errorf("two publishes of the same head posted %d comment(s), want 1", n)
	}
}

// TestPublishRecordPRThroughMerges: a pull request whose head is past the
// reviewed head by merges git itself would have made (the target merged into
// the branch since the round) is recorded, though its sha is not the reviewed
// one; one whose head carries a merge resolved by hand, a commit of the
// session's own, or an object the clone does not hold is refused as it was.
func TestPublishRecordPRThroughMerges(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	_, addEnv := useGithubHost(t, &d, "", "")
	branch := ticketBranch(fx.Ticket)
	addEnv("GH_STUB_HEAD_REF=" + branch)

	// The target moves, and the branch merges it in without a conflict.
	run(t, fx.RepoDir, "checkout", "main")
	writeAndCommit(t, fx.RepoDir, "main1.txt", "from main", "the target moves")
	run(t, fx.RepoDir, "checkout", branch)
	run(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "main")
	clean := run(t, fx.RepoDir, "rev-parse", "HEAD")

	// A head the clone does not hold is refused, though it names a merge.
	addEnv("GH_STUB_HEAD_OID=" + strings.Repeat("0", 40))
	_, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")

	// An ordinary commit past the merge is refused.
	ordinary := writeAndCommit(t, fx.RepoDir, "late.txt", "after the merge", "a commit after the merge")
	addEnv("GH_STUB_HEAD_OID=" + ordinary)
	_, err = PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")

	// A merge resolved by hand is refused.
	run(t, fx.RepoDir, "reset", "--hard", head)
	run(t, fx.RepoDir, "merge", "--no-ff", "--no-commit", "main")
	resolved := writeAndCommit(t, fx.RepoDir, "hand.txt", "an edit the merge did not need", "merge main, by hand")
	addEnv("GH_STUB_HEAD_OID=" + resolved)
	_, err = PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	if lines, _ := journal.Read(d.Store, fx.Ticket); hasEvent(lines, journal.EventPr) {
		t.Fatal("a refused pull request was journaled")
	}

	// The clean merge is covered: recorded, at the head the host names.
	addEnv("GH_STUB_HEAD_OID=" + clean)
	report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR for a pull request a clean merge past the reviewed head: %v", err)
	}
	if !report.Verified || report.Commit != clean {
		t.Errorf("report = %+v, want it verified at the merge %s", report, clean)
	}
}

func hasEvent(lines []journal.Line, event string) bool {
	for _, l := range lines {
		if l.Event == event {
			return true
		}
	}
	return false
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

	if report, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "agent"); err != nil || len(report.Repos) != 1 || report.Repos[0].Head != head || report.Provenance != "agent" {
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

// TestPublishRecordPRPostsTheSameNotesOncePerPullRequest: one pull request is
// one pull request however its url is spelled (a trailing slash, /files, a
// fragment, http), and the journal keeps the canonical url; the same notes
// still post to a different pull request, such as the replacement of a closed
// one.
func TestPublishRecordPRPostsTheSameNotesOncePerPullRequest(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	logFile, addEnv := useGithubHost(t, &d, "", "")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	record := func(url, want string) {
		t.Helper()
		report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, url, nil)
		if err != nil {
			t.Fatalf("PublishRecordPR %s: %v", url, err)
		}
		if report.ReviewNotes.Status != want {
			t.Fatalf("--pr %s: review notes %q, want %q", url, report.ReviewNotes.Status, want)
		}
	}
	record(prURL, ReviewNotesPosted)
	for _, spelled := range []string{prURL + "/", prURL + "/files", prURL + "#issuecomment-1", "http://github.com/owner/fixture-repo/pull/7"} {
		record(spelled, ReviewNotesAlreadyPosted)
	}
	if n := len(reviewNotesCommentCalls(loggedGh(t, logFile))); n != 1 {
		t.Fatalf("five spellings of one pull request posted %d comment(s), want 1", n)
	}
	if posted := reviewNotesLines(t, d, fx.Ticket); len(posted) != 1 || posted[0].URL != prURL {
		t.Fatalf("review-notes lines = %+v, want one with the canonical url %s", posted, prURL)
	}

	record("https://github.com/owner/fixture-repo/pull/8", ReviewNotesPosted)
	if n := len(reviewNotesCommentCalls(loggedGh(t, logFile))); n != 2 {
		t.Fatalf("the same notes on a second pull request posted %d comment(s) in all, want 2", n)
	}
}

// TestPublishRecordPRRendersTheReviewNotesItPosts: --pr posts notes rendered
// for the reviewed head. Run before --body they are rendered first, and notes
// an earlier head's body left behind are rendered again.
func TestPublishRecordPRRendersTheReviewNotesItPosts(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	logFile, addEnv := useGithubHost(t, &d, "", "")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	bodyLines := func() int {
		n := 0
		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if l.Event == journal.EventPrBody && l.Commit == head {
				n++
			}
		}
		return n
	}

	report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR before any --body: %v", err)
	}
	if report.ReviewNotes.Status != ReviewNotesPosted || bodyLines() != 1 || len(reviewNotesCommentCalls(loggedGh(t, logFile))) != 1 {
		t.Fatalf("--pr before --body: %+v, %d body line(s), want the notes rendered and posted", report.ReviewNotes, bodyLines())
	}

	// The store's latest body is an earlier head's.
	if err := journal.Append(d.Store, fx.Ticket, journal.Line{Event: journal.EventPrBody, Commit: "0000000"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil); err != nil {
		t.Fatalf("PublishRecordPR after an earlier head's body: %v", err)
	}
	if bodyLines() != 2 {
		t.Errorf("notes of an earlier head were posted as found: %d body line(s) for the reviewed head, want them rendered again", bodyLines())
	}
}

// TestPostReviewNotesSaysWhenTheNotesAreNotRendered: notes that are not in the
// store are not posted, and the report names no file to post by hand.
func TestPostReviewNotesSaysWhenTheNotesAreNotRendered(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	useGithubHost(t, &d, "", "")
	host, err := d.publishHost(fx.RepoRemote)
	if err != nil || host == nil {
		t.Skipf("no host for the fixture remote: %v", err)
	}
	report := postReviewNotes(d, host, fx.Ticket, "https://github.com/owner/fixture-repo/pull/7", "", "review-notes.md", func(string, ...any) {})
	if report.Status != ReviewNotesNotPosted || report.File != "" || !strings.Contains(report.Reason, "not rendered") {
		t.Fatalf("report = %+v, want not posted, not rendered, with no file", report)
	}
}

// TestPublishRecordPRNotesSayTheApprovalTheHeadHad: a pull request recorded
// with --pr carries review notes that say which approval its head had, and its
// pr line journals the same (person, agent or none), recorded and never
// refused. An agent's approval reads as an agent's, not a person's at a
// terminal.
func TestPublishRecordPRNotesSayTheApprovalTheHeadHad(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	cases := []struct{ name, provenance, wantLine, wantApproval string }{
		{"none", "", "Published without a recorded approval", "none"},
		{"agent", "agent", "Publish approved by an agent, as ", "agent"},
		{"person", "person", "Publish approved by ", "person"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx, d, head := reviewedInTheClone(t)
			_, addEnv := useGithubHost(t, &d, "", "")
			addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
			if c.provenance != "" {
				if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, c.provenance); err != nil {
					t.Fatalf("PublishApprove: %v", err)
				}
			}
			if _, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil); err != nil {
				t.Fatalf("PublishRecordPR: %v", err)
			}
			notes := readNotes(t, d.Store, fx.Ticket, "review-notes.md")
			switch c.name {
			case "none":
				if !strings.Contains(notes, c.wantLine+"\n") {
					t.Errorf("notes lack %q:\n%s", c.wantLine, notes)
				}
			case "agent":
				if !strings.Contains(notes, "\n"+c.wantLine) || !strings.Contains(notes, ", not by a person at a terminal\n") {
					t.Errorf("notes lack an agent's approval line:\n%s", notes)
				}
			case "person":
				if !strings.Contains(notes, "\n"+c.wantLine) || !strings.Contains(notes, " at a terminal\n") || strings.Contains(notes, "Publish approved by an agent") {
					t.Errorf("notes lack a person's approval line:\n%s", notes)
				}
				if want := "\nPublish approved by " + approverName(t, d) + " at a terminal\n"; !strings.Contains(notes, want) {
					t.Errorf("notes lack the approver's git identity, %q:\n%s", want, notes)
				}
			}
			lines, err := journal.Read(d.Store, fx.Ticket)
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range lines {
				if l.Event == journal.EventPr && l.Approval != c.wantApproval {
					t.Errorf("pr line approval = %q, want %q", l.Approval, c.wantApproval)
				}
			}
		})
	}
}

// TestPublishRecordPRNotesSayAnApprovalGivenAfterTheBody: the hand-off order
// --body, --approve, --pr posts notes that say the person's approval, the
// same the pr line journals.
func TestPublishRecordPRNotesSayAnApprovalGivenAfterTheBody(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	_, addEnv := useGithubHost(t, &d, "", "")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "person"); err != nil {
		t.Fatalf("PublishApprove: %v", err)
	}
	if _, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, nil); err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	notes := readNotes(t, d.Store, fx.Ticket, "review-notes.md")
	if !strings.Contains(notes, "\nPublish approved by ") || !strings.Contains(notes, " at a terminal\n") || strings.Contains(notes, "Published without a recorded approval") {
		t.Errorf("notes do not say the person's approval:\n%s", notes)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Event == journal.EventPr && l.Approval != "person" {
			t.Errorf("pr line approval = %q, want person", l.Approval)
		}
	}
}

// TestPublishApprovalLineWordsEachApproval: the line is worded from the last
// publish-approved line for the head and repo, with the identity the approval
// recorded.
func TestPublishApprovalLineWordsEachApproval(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	line := func(prov string) []journal.Line {
		return []journal.Line{{Event: journal.EventPublishApproved, Commit: head, Provenance: prov, Summary: "Dana <d@example.test>"}}
	}
	noWho := func(prov string) []journal.Line {
		return []journal.Line{{Event: journal.EventPublishApproved, Commit: head, Provenance: prov}}
	}
	for _, c := range []struct {
		lines []journal.Line
		head  string
		want  string
	}{
		{line("person"), head, "Publish approved by Dana at a terminal"},
		{line("agent"), head, "Publish approved by an agent, as Dana, not by a person at a terminal"},
		{line("person"), strings.Repeat("b", 40), "Published without a recorded approval"},
		{nil, head, "Published without a recorded approval"},
		{noWho("person"), head, "Publish approved by a person at a terminal"},
		{noWho("agent"), head, "Publish approved by an agent, not by a person at a terminal"},
	} {
		if got := publishApprovalLine(c.lines, "", c.head); got != c.want {
			t.Errorf("publishApprovalLine = %q, want %q", got, c.want)
		}
	}
}

// approverName is the git name the store credits an approval to.
func approverName(t *testing.T, d Deps) string {
	t.Helper()
	who, err := gitx.ResolveIdentity(d.Store.Root)
	if err != nil || who.Name == "" || who.Name == who.Email {
		t.Fatalf("the fixture store has no git identity to credit: %+v (%v)", who, err)
	}
	return who.Name
}

// TestPublishRecordPRWithNoHostNotesSayAnApprovalGivenAfterTheBody: with no
// pull-request host the notes --pr names to post by hand say the person's
// approval given after --body, the same the pr line journals.
func TestPublishRecordPRWithNoHostNotesSayAnApprovalGivenAfterTheBody(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "person"); err != nil {
		t.Fatalf("PublishApprove: %v", err)
	}
	if _, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, "https://example.test/owner/repo/pull/7", nil); err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	want := "Publish approved by " + approverName(t, d) + " at a terminal\n"
	notes := readNotes(t, d.Store, fx.Ticket, "review-notes.md")
	if !strings.Contains(notes, want) || strings.Contains(notes, "Published without a recorded approval") {
		t.Errorf("notes to post by hand lack %q:\n%s", want, notes)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Event == journal.EventPr && l.Approval != "person" {
			t.Errorf("pr line approval = %q, want person", l.Approval)
		}
	}
}

// TestPublishBareFormApprovalComesFromHowItWasConfirmed: an interactive yes
// journals a person's publish-approved line for the head, and the review notes
// and the pr line say it; --yes stands for no person, so it journals none and
// the notes say no recorded approval.
func TestPublishBareFormApprovalComesFromHowItWasConfirmed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		yes      bool
		terminal bool
		approval string
	}{
		{"interactive yes at a terminal", false, true, "person"},
		{"piped y, not a terminal", false, false, "agent"},
		{"--yes", true, true, "none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx, d, head := reviewedInTheClone(t)
			d.Confirm = func(string, string, string, bool) bool { return true }
			d.StdinIsTerminal = func() bool { return c.terminal }
			d.GuardedPush = func(string, string, string, bool) error { return nil }
			if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: c.yes}); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			notes := readNotes(t, d.Store, fx.Ticket, "review-notes.md")
			lines, err := journal.Read(d.Store, fx.Ticket)
			if err != nil {
				t.Fatal(err)
			}
			approved := 0
			for _, l := range lines {
				switch l.Event {
				case journal.EventPublishApproved:
					approved++
					if l.Commit != head || l.Provenance != c.approval {
						t.Errorf("publish-approved line = %+v, want provenance %s for %s", l, c.approval, head)
					}
				case journal.EventPr:
					if l.Approval != c.approval {
						t.Errorf("pr line approval = %q, want %q", l.Approval, c.approval)
					}
				}
			}
			if c.approval == "person" {
				want := "Publish approved by " + approverName(t, d) + " at a terminal\n"
				if approved != 1 || !strings.Contains(notes, want) {
					t.Errorf("%d publish-approved line(s); notes lack %q:\n%s", approved, want, notes)
				}
			} else if c.approval == "agent" {
				want := "Publish approved by an agent, as " + approverName(t, d) + ", not by a person at a terminal\n"
				if approved != 1 || !strings.Contains(notes, want) {
					t.Errorf("%d publish-approved line(s); notes lack %q:\n%s", approved, want, notes)
				}
			} else if approved != 0 || !strings.Contains(notes, "Published without a recorded approval\n") {
				t.Errorf("%d publish-approved line(s); notes lack the no-approval line:\n%s", approved, notes)
			}
		})
	}
}
