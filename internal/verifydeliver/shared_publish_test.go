package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
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
