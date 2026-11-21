package verifydeliver

import (
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

const (
	issueBranch = "I-1-the-issue"
	multiBranch = "M-1-the-multi-task"
)

// containerDeps is a store of an issue I-1 (with a need), its multi-task M-1 of
// tasks T-1 and T-2, and the task T-3 beside it, each task landed into its
// container with a pull request, and the Deps that publish them: a fake gh, the
// plan of each container, and a readiness the test sets.
func containerDeps(t *testing.T) (*Deps, *ContainerStanding, string) {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	for id, rec := range map[string]store.Ticket{
		"I-1": {Title: "The issue", Body: "People need the thing.", Kind: store.KindIssue, Slug: "the-issue"},
		"M-1": {Title: "The multi-task", Body: "chart notes of the multi-task", Kind: store.KindMultiTask, Slug: "the-multi-task"},
		"T-1": {Title: "First task", Parent: "M-1"}, "T-2": {Title: "Second task", Parent: "M-1"},
		"T-3": {Title: "Third task"},
	} {
		if err := d.Store.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	chart := "issue: I-1\ntickets:\n  - id: M-1\n    title: The multi-task\n  - id: T-1\n    title: First task\n  - id: T-2\n    title: Second task\n  - id: T-3\n    title: Third task\n"
	if err := store.AtomicWrite(d.Store.ChartFile("c"), []byte(chart)); err != nil {
		t.Fatal(err)
	}
	for id, into := range map[string]string{"T-1": "M-1", "T-2": "M-1", "T-3": "I-1"} {
		url := "https://github.com/owner/fixture-repo/pull/" + id[2:]
		for _, l := range []journal.Line{
			{Event: journal.EventPr, Into: into, URL: url}, {Event: journal.EventPublishDone, Into: into},
			{Event: journal.EventLanded, Into: into, URL: url},
		} {
			if err := journal.Append(d.Store, id, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	d.Stack = func(ticket string) (stack.Plan, error) {
		r := stack.Repo{Repo: "fixture-repo", Target: "main", StartFrom: "origin/main", PRTarget: "main"}
		if ticket == "M-1" {
			r.PRTarget, r.Container, r.ContainerBranch, r.ContainerFrom = issueBranch, "I-1", issueBranch, "origin/main"
		}
		return stack.Plan{Ticket: ticket, Repos: []stack.Repo{r}}, nil
	}
	standing := &ContainerStanding{State: ContainerReady}
	d.Container = func(string) (ContainerStanding, error) { return *standing, nil }
	return &d, standing, fx.Ticket
}

func gitCreates(t *testing.T, logFile string) [][]string {
	t.Helper()
	var out [][]string
	for _, argv := range loggedGh(t, logFile) {
		if len(argv) >= 2 && argv[0] == "pr" && argv[1] == "create" {
			out = append(out, argv)
		}
	}
	return out
}

func journalEvents(t *testing.T, d *Deps, ticket string) []journal.Line {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range lines {
		if journal.IsPublishEvent(l.Event) {
			out = append(out, l)
		}
	}
	return out
}

// TestContainerPublishOpensAMultiTasksPullRequest: a multi-task whose tasks have
// landed opens its pull request from its branch into its issue's, with the
// short body naming each task and its pull request and no round or demo, and
// writes pr-body, publish-approved, pr and publish-done lines into its issue.
func TestContainerPublishOpensAMultiTasksPullRequest(t *testing.T) {
	t.Parallel()
	d, _, _ := containerDeps(t)
	logFile, _ := useGithubHost(t, d, "", "")

	report, err := PublishContainer(*d, PublishOpts{Ticket: "M-1", Yes: true})
	if err != nil {
		t.Fatalf("PublishContainer: %v", err)
	}
	creates := gitCreates(t, logFile)
	if len(creates) != 1 {
		t.Fatalf("gh pr create ran %d times: %v", len(creates), creates)
	}
	if i := slices.Index(creates[0], "--base"); i < 0 || creates[0][i+1] != issueBranch {
		t.Errorf("gh pr create = %v, want --base %s", creates[0], issueBranch)
	}
	if i := slices.Index(creates[0], "--head"); i < 0 || creates[0][i+1] != multiBranch {
		t.Errorf("gh pr create = %v, want --head %s", creates[0], multiBranch)
	}
	if report.PRURL["fixture-repo"] == "" || len(report.Pending) != 0 {
		t.Errorf("report = %+v, want a pull request and nothing pending", report)
	}
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"])))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# The multi-task\n", "- First task (T-1): https://github.com/owner/fixture-repo/pull/1\n", "- Second task (T-2): https://github.com/owner/fixture-repo/pull/2\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the body lacks %q:\n%s", want, body)
		}
	}
	for _, not := range []string{"## Demo", "## Verification", "## What", "T-3", "chart notes"} {
		if strings.Contains(string(body), not) {
			t.Errorf("the body has %q:\n%s", not, body)
		}
	}
	var events []string
	for _, l := range journalEvents(t, d, "M-1") {
		events = append(events, l.Event)
		if l.Into != "I-1" {
			t.Errorf("line %s is into %q, want I-1", l.Event, l.Into)
		}
	}
	if want := []string{journal.EventPrBody, journal.EventPublishApproved, journal.EventPr, journal.EventPublishDone}; !slices.Equal(events, want) {
		t.Errorf("publish lines = %v, want %v", events, want)
	}
}

// TestContainerPublishOpensAnIssuesPullRequest: an issue's pull request goes
// into the target, its body has its need, one line per child in chart order, a
// multi-task's tasks nested under it, and the project's trailer and nothing after
// it.
func TestContainerPublishOpensAnIssuesPullRequest(t *testing.T) {
	t.Parallel()
	d, _, _ := containerDeps(t)
	d.Cfg.PublishTrailer = "Opened by an agent"
	// The multi-task landed into the issue.
	for _, l := range []journal.Line{{Event: journal.EventPr, Into: "I-1", URL: "https://github.com/owner/fixture-repo/pull/9"}, {Event: journal.EventLanded, Into: "I-1", URL: "https://github.com/owner/fixture-repo/pull/9"}} {
		if err := journal.Append(d.Store, "M-1", l); err != nil {
			t.Fatal(err)
		}
	}
	logFile, _ := useGithubHost(t, d, "", "")

	report, err := PublishContainer(*d, PublishOpts{Ticket: "I-1", Yes: true})
	if err != nil {
		t.Fatalf("PublishContainer: %v", err)
	}
	creates := gitCreates(t, logFile)
	if len(creates) != 1 {
		t.Fatalf("gh pr create ran %d times", len(creates))
	}
	if i := slices.Index(creates[0], "--base"); i < 0 || creates[0][i+1] != "main" {
		t.Errorf("gh pr create = %v, want --base main", creates[0])
	}
	body, _ := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"])))
	want := "# The issue\n\nPeople need the thing.\n\n## Children\n\n" +
		"- The multi-task (M-1): https://github.com/owner/fixture-repo/pull/9\n" +
		"  - First task (T-1): https://github.com/owner/fixture-repo/pull/1\n" +
		"  - Second task (T-2): https://github.com/owner/fixture-repo/pull/2\n" +
		"- Third task (T-3): https://github.com/owner/fixture-repo/pull/3\n" +
		"\nOpened by an agent\n"
	if string(body) != want {
		t.Errorf("body:\n%s\nwant:\n%s", body, want)
	}
	for _, l := range journalEvents(t, d, "I-1") {
		if l.Into != "" {
			t.Errorf("line %s is into %q, want the target", l.Event, l.Into)
		}
	}
}

// TestContainerPublishRefusesBeforeAnyWrite: a child not landed is
// PUBLISH_NOT_READY naming it with its standing, an unmet precondition is its
// own code, a container with no child is not ready, and none writes a line, a
// body or a pull request.
func TestContainerPublishRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	d, standing, _ := containerDeps(t)
	logFile, _ := useGithubHost(t, d, "", "")
	before := len(journalEvents(t, d, "I-1"))
	for _, c := range []struct {
		name     string
		standing ContainerStanding
		code     string
		msg      string
	}{
		{"a child waits", ContainerStanding{State: ContainerWaiting, Waits: []ContainerWait{{"T-1", "published"}, {"T-3", "building"}}}, PublishNotReady, "T-1 (published), T-3 (building)"},
		{"a precondition", ContainerStanding{State: ContainerWaiting, Unmet: []ContainerUnmet{{"LAND_SUITE_NOT_RUN", "the suite has not run", "Run the suite"}}}, "LAND_SUITE_NOT_RUN", "the suite has not run"},
		{"a precondition with no prompt", ContainerStanding{State: ContainerWaiting, Unmet: []ContainerUnmet{{"LAND_SUITE_NOT_RUN", "the suite has not run", ""}}}, "LAND_SUITE_NOT_RUN", "the suite has not run"},
		{"no children", ContainerStanding{State: ContainerWaiting}, PublishNotReady, "no children"},
		{"landed already", ContainerStanding{State: ContainerLanded}, PublishNotReady, "has landed"},
	} {
		*standing = c.standing
		_, err := PublishContainer(*d, PublishOpts{Ticket: "I-1", Yes: true})
		ae, ok := err.(*axi.Error)
		if !ok || ae.Code != c.code || !strings.Contains(ae.Msg, c.msg) {
			t.Errorf("%s: err = %v, want %s containing %q", c.name, err, c.code, c.msg)
			continue
		}
		if c.name == "a precondition" && !strings.Contains(strings.Join(ae.Help, " "), "`Run the suite`") {
			t.Errorf("%s: help = %v, want the precondition's prompt", c.name, ae.Help)
		}
		if c.name == "a precondition with no prompt" && strings.Contains(strings.Join(ae.Help, " "), "Ask ``") {
			t.Errorf("%s: help = %v names an empty prompt", c.name, ae.Help)
		}
	}
	if got := len(journalEvents(t, d, "I-1")); got != before {
		t.Errorf("a refused publish wrote %d lines", got-before)
	}
	if n := len(loggedGh(t, logFile)); n != 0 {
		t.Errorf("a refused publish talked to the host %d times", n)
	}
	if _, err := os.Stat(filepath.Join(d.Store.TicketDir("I-1"), "pr")); err == nil {
		t.Error("a refused publish wrote a body")
	}
}

// TestContainerPublishAsksWithoutYes: with no --yes the confirmation is asked, a
// no writes nothing, and a yes records the approval as the agent's (stdin is not
// a terminal).
func TestContainerPublishAsksWithoutYes(t *testing.T) {
	t.Parallel()
	d, _, _ := containerDeps(t)
	logFile, _ := useGithubHost(t, d, "", "")
	asked := 0
	d.Confirm = func(branch, ticket, openPR string, hasHost bool) bool {
		asked++
		return asked > 1
	}
	_, err := PublishContainer(*d, PublishOpts{Ticket: "M-1"})
	if ae, ok := err.(*axi.Error); !ok || ae.Code != "PUBLISH_DECLINED" {
		t.Fatalf("declined publish = %v, want PUBLISH_DECLINED", err)
	}
	if len(journalEvents(t, d, "M-1")) != 0 || len(gitCreates(t, logFile)) != 0 {
		t.Fatal("a declined publish wrote or opened something")
	}
	if _, err := PublishContainer(*d, PublishOpts{Ticket: "M-1"}); err != nil {
		t.Fatal(err)
	}
	for _, l := range journalEvents(t, d, "M-1") {
		if l.Event == journal.EventPublishApproved && l.Provenance != "agent" {
			t.Errorf("approval provenance = %q, want agent", l.Provenance)
		}
	}
}
