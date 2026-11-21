package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// shareTicket lists the fixture's ticket in two model charts, of issues I-1
// and I-2, and gives each issue's plan its branch.
func shareTicket(t *testing.T, d *Deps, ticket string) {
	t.Helper()
	for name, issue := range map[string]string{"a": "I-1", "b": "I-2"} {
		if err := store.AtomicWrite(d.Store.ChartFile(name), []byte("issue: "+issue+"\ntickets:\n  - id: "+ticket+"\n    title: Shared\n")); err != nil {
			t.Fatal(err)
		}
	}
	d.StackFor = func(tk, issue string) (stack.Plan, error) {
		return stack.Plan{Ticket: tk, Repos: []stack.Repo{{
			Repo: "fixture-repo", Target: "main", StartFrom: "origin/main", PRTarget: issue + "-issue",
			Container: issue, ContainerBranch: issue + "-issue", ContainerFrom: "origin/main",
		}}}, nil
	}
}

// TestSharedPublish: a task two issues list is published into each issue by
// name: a missing or foreign --issue refuses before any write, each publish
// opens a pull request into its issue's branch with its own journal lines, the
// second finds its own pull request and not the first's, and its body says the
// task is shared with the first.
func TestSharedPublish(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	shareTicket(t, &d, fx.Ticket)
	logFile, _ := useGithubHost(t, &d, "", "")
	before, _ := journal.Read(d.Store, fx.Ticket)

	for _, issue := range []string{"", "I-9"} {
		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true, Issue: issue})
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != SharedTaskIssueNeeded || !strings.Contains(ae.Msg, "I-1: no pull request") || !strings.Contains(ae.Msg, "I-2: no pull request") {
			t.Fatalf("Publish(issue %q) = %v, want %s listing both issues", issue, err, SharedTaskIssueNeeded)
		}
	}
	if after, _ := journal.Read(d.Store, fx.Ticket); len(after) != len(before) {
		t.Fatalf("a refused publish wrote %d journal lines", len(after)-len(before))
	}

	bases := func() []string {
		var out []string
		for _, argv := range loggedGh(t, logFile) {
			if len(argv) >= 2 && argv[0] == "pr" && argv[1] == "create" {
				if i := slices.Index(argv, "--base"); i >= 0 {
					out = append(out, argv[i+1])
				}
			}
		}
		return out
	}
	r1, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true, Issue: "I-1"})
	if err != nil {
		t.Fatalf("Publish into I-1: %v", err)
	}
	url1 := r1.PRURL["fixture-repo"]
	if !slices.Equal(bases(), []string{"I-1-issue"}) || url1 == "" {
		t.Fatalf("after I-1: bases %v, url %q", bases(), url1)
	}
	st, err := ReadStatus(d.Store, fx.Ticket)
	if err != nil || len(st.Issues) != 2 || st.PR != "" || st.Issues[0].Issue != "I-1" || !st.Issues[0].Published || st.Issues[0].PR != url1 || st.Issues[1].Published || st.Issues[1].PR != "" {
		t.Fatalf("status = %+v, %v, want I-1 published with %s and I-2 not", st, err, url1)
	}

	r2, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true, Issue: "I-2"})
	if err != nil {
		t.Fatalf("Publish into I-2: %v", err)
	}
	if !slices.Equal(bases(), []string{"I-1-issue", "I-2-issue"}) || r2.PRUpdated["fixture-repo"] {
		t.Fatalf("after I-2: bases %v, updated %v, want a second pull request opened", bases(), r2.PRUpdated)
	}
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(r2.PRBody["fixture-repo"])))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Shared with I-1 (pull request " + url1 + ")"; !strings.Contains(string(body), want) {
		t.Errorf("the body of I-2's pull request lacks %q:\n%s", want, body)
	}

	lines, _ := journal.Read(d.Store, fx.Ticket)
	into := map[string][]string{}
	for _, l := range lines {
		if journal.IsPublishEvent(l.Event) {
			into[l.Into] = append(into[l.Into], l.Event)
		}
	}
	for _, issue := range []string{"I-1", "I-2"} {
		if !slices.Contains(into[issue], journal.EventPr) || !slices.Contains(into[issue], journal.EventPublishDone) {
			t.Errorf("lines into %s = %v, want pr and publish-done", issue, into[issue])
		}
	}
	if len(into[""]) != 0 {
		t.Errorf("publish lines with no into: %v", into[""])
	}
	st, _ = ReadStatus(d.Store, fx.Ticket)
	if len(st.Issues) != 2 || !st.Issues[1].Published || st.Issues[1].PR != r2.PRURL["fixture-repo"] {
		t.Errorf("status after both = %+v", st)
	}
}

// linesInto are the publish events the journal holds into issue, in order.
func linesInto(t *testing.T, d Deps, ticket, issue string) []string {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range lines {
		if journal.IsPublishEvent(l.Event) && l.Into == issue {
			out = append(out, l.Event)
		}
	}
	return out
}

// TestSharedPublishFindsItsOwnPullRequest: with the first issue's pull request
// already open from the branch, a publish into the second opens its own, into
// the second issue's branch, and leaves the first's alone; a publish into the
// first updates the first's and nothing else.
func TestSharedPublishFindsItsOwnPullRequest(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	shareTicket(t, &d, fx.Ticket)
	pulls := fixture.GhPulls(t, fixture.GhPull{Number: 7, Owner: "owner", Head: ticketBranch(fx.Ticket), Base: "I-1-issue"})
	logFile, _ := useGithubHost(t, &d, pulls, "")

	argvOf := func(verb string) [][]string {
		var out [][]string
		for _, argv := range loggedGh(t, logFile) {
			if len(argv) >= 2 && argv[0] == "pr" && argv[1] == verb {
				out = append(out, argv)
			}
		}
		return out
	}
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true, Issue: "I-2"}); err != nil {
		t.Fatalf("Publish into I-2: %v", err)
	}
	creates := argvOf("create")
	if len(creates) != 1 || len(argvOf("edit")) != 0 {
		t.Fatalf("publish into I-2: %d creates and %d edits, want one create and no edit of I-1's pull request", len(creates), len(argvOf("edit")))
	}
	if i := slices.Index(creates[0], "--base"); i < 0 || creates[0][i+1] != "I-2-issue" {
		t.Errorf("gh pr create = %v, want --base I-2-issue", creates[0])
	}
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true, Issue: "I-1"}); err != nil {
		t.Fatalf("Publish into I-1: %v", err)
	}
	edits := argvOf("edit")
	if len(argvOf("create")) != 1 || len(edits) != 1 || !slices.Contains(edits[0], "https://github.example/owner/repo/pull/7") {
		t.Errorf("publish into I-1: creates %d, edits %v, want it to update the open pull request #7", len(argvOf("create")), edits)
	}
	if i := slices.Index(edits[0], "--base"); i >= 0 {
		t.Errorf("the update retargets I-1's pull request: %v", edits[0])
	}
}

// TestSharedPublishApprovalAndHandOffAreForTheIssue: a person's approval of one
// issue's pull request is not the other's, so a bare publish into the other
// asks again; a yes records the approval into its own issue; the hand-off
// (--body, then --pr) writes its lines into the issue it names and records no
// other issue's.
func TestSharedPublishApprovalAndHandOffAreForTheIssue(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	shareTicket(t, &d, fx.Ticket)
	useGithubHost(t, &d, "", "")
	asked := 0
	d.Confirm = func(string, string, string, bool) bool { asked++; return false }

	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-1"}, "person"); err != nil {
		t.Fatal(err)
	}
	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-2"})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "PUBLISH_DECLINED" || asked != 1 {
		t.Fatalf("a bare publish into I-2 on I-1's approval: %v, asked %d, want PUBLISH_DECLINED after one question", err, asked)
	}
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-1"}); err != nil || asked != 1 {
		t.Fatalf("a bare publish into I-1 on its own approval: %v, asked %d, want no question", err, asked)
	}

	d.Confirm = func(string, string, string, bool) bool { return true }
	d.StdinIsTerminal = func() bool { return true }
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-2"}); err != nil {
		t.Fatalf("Publish into I-2 on a yes: %v", err)
	}
	if got := linesInto(t, d, fx.Ticket, "I-2"); !slices.Contains(got, journal.EventPublishApproved) {
		t.Errorf("lines into I-2 = %v, want the yes recorded as its approval", got)
	}
	for _, issue := range []string{"I-1", "I-2"} {
		if st, err := ReadStatusFor(d.Store, fx.Ticket, issue); err != nil || st.ApprovedFor != head {
			t.Errorf("approved for %s = %q, %v, want the reviewed head", issue, st.ApprovedFor, err)
		}
	}

	// The hand-off into a third copy of the issue's own lines: body then pr.
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-2"}); err != nil {
		t.Fatalf("PublishBody into I-2: %v", err)
	}
	if got := linesInto(t, d, fx.Ticket, "I-2"); !slices.Contains(got, journal.EventPrBody) {
		t.Errorf("lines into I-2 = %v, want the rendered body", got)
	}
	if slices.Contains(linesInto(t, d, fx.Ticket, "I-1"), journal.EventPrBody) {
		t.Errorf("the body rendered for I-2 is recorded for I-1")
	}
}

// TestSharedPublishRecordsItsOwnApproval: the approval a pull request records is
// its own issue's: an agent's yes into the second issue stays an agent's when a
// person later approves the first.
func TestSharedPublishRecordsItsOwnApproval(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	shareTicket(t, &d, fx.Ticket)
	useGithubHost(t, &d, "", "")
	d.Confirm = func(string, string, string, bool) bool { return true }
	d.StdinIsTerminal = func() bool { return false }
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-2"}); err != nil {
		t.Fatalf("Publish into I-2: %v", err)
	}
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-1"}, "person"); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-2", Yes: true}); err != nil {
		t.Fatalf("Publish into I-2 again: %v", err)
	}
	lines, err := journal.ReadFor(d.Store, fx.Ticket, "I-2")
	if err != nil {
		t.Fatal(err)
	}
	approval := ""
	for _, l := range lines {
		if l.Event == journal.EventPr {
			approval = l.Approval
		}
	}
	if approval != approvalAgent {
		t.Errorf("the pull request into I-2 records approval %q, want %q: its own, not I-1's", approval, approvalAgent)
	}
}

// TestSharedPublishRecordsAHandedOffPullRequestIntoItsIssue: PublishRecordPR
// with an issue records the pull request, and finishes the publish, for that
// issue alone.
func TestSharedPublishRecordsAHandedOffPullRequestIntoItsIssue(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	shareTicket(t, &d, fx.Ticket)
	_, addEnv := useGithubHost(t, &d, "", "")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	rec, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-2"}, "https://github.com/owner/fixture-repo/pull/7", nil)
	if err != nil {
		t.Fatalf("PublishRecordPR into I-2: %v", err)
	}
	// The notes are rendered for the issue before the pull request is read back.
	if rec.ReviewNotes.Status != ReviewNotesPosted || rec.Readback.Match == journal.BodyUnknown {
		t.Errorf("the hand-off's notes %+v and read-back %q, want the notes posted and the body read back", rec.ReviewNotes, rec.Readback.Match)
	}
	if got := linesInto(t, d, fx.Ticket, "I-2"); !slices.Contains(got, journal.EventPr) || !slices.Contains(got, journal.EventPublishDone) {
		t.Errorf("lines into I-2 = %v, want pr and publish-done", got)
	}
	if got := linesInto(t, d, fx.Ticket, "I-1"); len(got) != 0 {
		t.Errorf("lines into I-1 = %v, want none", got)
	}
	if _, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, "https://github.com/owner/fixture-repo/pull/8", nil); err == nil {
		t.Error("PublishRecordPR of a shared task with no issue was not refused")
	}
}

// TestSharedPublishApprovalIsPerIssue: a person's approval of one issue's pull
// request is not the approval of the other's.
func TestSharedPublishApprovalIsPerIssue(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	shareTicket(t, &d, fx.Ticket)
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-1"}, "person"); err != nil {
		t.Fatalf("PublishApprove I-1: %v", err)
	}
	one, _ := ReadStatusFor(d.Store, fx.Ticket, "I-1")
	two, _ := ReadStatusFor(d.Store, fx.Ticket, "I-2")
	if one.ApprovedFor == "" || two.ApprovedFor != "" {
		t.Errorf("approved for I-1 %q, I-2 %q; want only I-1's", one.ApprovedFor, two.ApprovedFor)
	}
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "person"); err == nil {
		t.Error("PublishApprove of a shared task with no issue was not refused")
	}
}
