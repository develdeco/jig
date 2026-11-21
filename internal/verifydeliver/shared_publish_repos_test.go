package verifydeliver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// shareTicketIn is shareTicket for a ticket that builds in several repos.
func shareTicketIn(t *testing.T, d *Deps, ticket string, repos ...string) {
	t.Helper()
	shareTicket(t, d, ticket)
	d.StackFor = func(tk, issue string) (stack.Plan, error) {
		plan := stack.Plan{Ticket: tk}
		for _, name := range repos {
			plan.Repos = append(plan.Repos, stack.Repo{
				Repo: name, Target: "main", StartFrom: "origin/main", PRTarget: issue + "-issue",
				Container: issue, ContainerBranch: issue + "-issue", ContainerFrom: "origin/main",
			})
		}
		return plan, nil
	}
}

// issuePRs records, as a publish would, an issue's pull request in each repo,
// with no publish-done: a journal in which a reader that took the first issue's
// pull requests for the second's would find every repo recorded.
func issuePRs(t *testing.T, st *store.Store, ticket, issue string, urls map[string]string) {
	t.Helper()
	for _, repo := range []string{"api", "web"} {
		if err := journal.Append(st, ticket, journal.Line{Event: journal.EventPr, Into: issue, Repo: repo, URL: urls[repo]}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSharedPublishInTwoRepos: for a task of two repos shared by two issues,
// the other issue's pull requests are not this issue's: a publish of one repo
// into the second issue leaves the other repo pending and writes no
// publish-done, the body links the other repo as pending and not the first
// issue's pull request there, and the hand-off records one repo and leaves the
// other pending.
func TestSharedPublishInTwoRepos(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "api", "web")
	headAPI := o.build(t, "api")
	o.build(t, "web")
	o.reviewedClean(t)
	shareTicketIn(t, &o.d, o.ticket, "api", "web")
	_, addEnv := useGithubHost(t, &o.d, "", "")
	first := map[string]string{"api": "https://github.com/owner/fixture-repo/pull/90", "web": "https://github.com/owner/fixture-repo/pull/91"}
	issuePRs(t, o.d.Store, o.ticket, "I-1", first)

	rep, err := Publish(o.d, PublishOpts{Ticket: o.ticket, Yes: true, Repo: "api", Issue: "I-2"})
	if err != nil {
		t.Fatalf("Publish api into I-2: %v", err)
	}
	if !slices.Equal(rep.Pending, []string{"web"}) {
		t.Errorf("pending = %v, want web: I-1's pull request there is not I-2's", rep.Pending)
	}
	if got := linesInto(t, o.d, o.ticket, "I-2"); slices.Contains(got, journal.EventPublishDone) {
		t.Errorf("lines into I-2 = %v, want no publish-done before web has its pull request", got)
	}

	body, err := PublishBody(o.d, PublishOpts{Ticket: o.ticket, Repo: "api", Issue: "I-2"})
	if err != nil || len(body.Repos) != 1 {
		t.Fatalf("PublishBody: %+v, %v", body, err)
	}
	text, err := os.ReadFile(filepath.Join(o.d.Store.Root, filepath.FromSlash(body.Repos[0].PRBody)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), first["web"]) || !strings.Contains(string(text), "Shared with I-1 (pull request "+first["api"]+")") {
		t.Errorf("the body of I-2's api pull request:\n%s\nwant it to link web as pending, not I-1's pull request there, and I-1's api one as shared", text)
	}

	addEnv("GH_STUB_HEAD_REF="+o.branch, "GH_STUB_HEAD_OID="+headAPI)
	rec, err := PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket, Repo: "api", Issue: "I-2"}, "https://github.com/owner/fixture-repo/pull/95", nil)
	if err != nil {
		t.Fatalf("PublishRecordPR api into I-2: %v", err)
	}
	if !slices.Equal(rec.Pending, []string{"web"}) {
		t.Errorf("recorded api: pending = %v, want web", rec.Pending)
	}
	if rec.ReviewNotes.Status != ReviewNotesPosted {
		t.Errorf("the hand-off's review notes: %+v, want posted: the notes are rendered for the issue", rec.ReviewNotes)
	}
	if got := linesInto(t, o.d, o.ticket, "I-2"); slices.Contains(got, journal.EventPublishDone) {
		t.Errorf("lines into I-2 = %v, want no publish-done after one repo", got)
	}

	// The publish of the last repo finishes I-2, with the size of the whole
	// change (both repos), which the first publish's targets measure.
	if _, err := Publish(o.d, PublishOpts{Ticket: o.ticket, Yes: true, Repo: "web", Issue: "I-2"}); err != nil {
		t.Fatalf("Publish web into I-2: %v", err)
	}
	lines, err := journal.ReadFor(o.d.Store, o.ticket, "I-2")
	if err != nil {
		t.Fatal(err)
	}
	var done *journal.Line
	for i := range lines {
		if lines[i].Event == journal.EventPublishDone {
			done = &lines[i]
		}
	}
	if done == nil || done.Change == nil || done.Change.Files < 2 {
		t.Errorf("publish-done into I-2 = %+v, want the size of both repos' change", done)
	}
}

// TestSharedPublishNotesCarryTheirOwnApproval: the review notes rendered for
// the second issue say no approval was recorded for it, though a person approved
// the first.
func TestSharedPublishNotesCarryTheirOwnApproval(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	shareTicket(t, &d, fx.Ticket)
	useGithubHost(t, &d, "", "")
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-1"}, "person"); err != nil {
		t.Fatal(err)
	}
	body, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket, Issue: "I-2"})
	if err != nil || len(body.Repos) != 1 {
		t.Fatalf("PublishBody: %+v, %v", body, err)
	}
	notes, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(body.Repos[0].ReviewNotes)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(notes), "at a terminal") || !strings.Contains(string(notes), "Published without a recorded approval") {
		t.Errorf("the notes of I-2's pull request carry I-1's approval:\n%s", notes)
	}
}
