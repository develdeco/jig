package verifydeliver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/stack"
)

// stackedOnJIG0 is a Deps.Stack that has the ticket stacked on JIG-0 in the
// fixture repo, whose branch jig/JIG-0 has not merged.
func stackedOnJIG0(ticket string) (stack.Plan, error) {
	return stack.Plan{Ticket: ticket, Repos: []stack.Repo{{
		Repo: "fixture-repo", Target: "main", StartFrom: "jig/JIG-0", PRTarget: "jig/JIG-0",
		Parent: "JIG-0", ParentBranch: "jig/JIG-0", ParentRef: "jig/JIG-0",
	}}}, nil
}

// TestPublishOfAStackedTicketGoesIntoTheParentsBranch: --body of a ticket
// stacked on a parent that has not merged names the parent's branch as the
// base and says so in the body; the bare publish refuses while that branch is
// not on the origin, pushing nothing, and once it is opens the pull request
// into it.
func TestPublishOfAStackedTicketGoesIntoTheParentsBranch(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	d.Stack = stackedOnJIG0
	logFile, _ := useGithubHost(t, &d, "", "")
	branch := ticketBranch(fx.Ticket)

	report, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if len(report.Repos) != 1 || report.Repos[0].Target != "jig/JIG-0" || report.Repos[0].Head != head {
		t.Fatalf("report = %+v, want the reviewed head into jig/JIG-0", report)
	}
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.Repos[0].PRBody)))
	if err != nil {
		t.Fatal(err)
	}
	want := "## Stacked on JIG-0\n\n" + fx.Ticket + " builds on JIG-0: this pull request goes into `jig/JIG-0`, the branch of JIG-0. Once JIG-0 merges, retarget it to `main`.\n\n## What changed"
	if !strings.Contains(string(body), want) {
		t.Errorf("pr body lacks %q:\n%s", want, body)
	}

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_PARENT_NOT_PUSHED")
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
		t.Fatalf("a refused publish pushed %s to origin at %s", branch, got)
	}
	if n := publishPrCalls(t, logFile); n != 0 {
		t.Fatalf("a refused publish opened a pull request")
	}

	run(t, fx.RepoDir, "push", fx.RepoRemote, "main:refs/heads/jig/JIG-0")
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var create []string
	for _, argv := range loggedGh(t, logFile) {
		if len(argv) >= 2 && argv[0] == "pr" && argv[1] == "create" {
			create = argv
		}
	}
	if i := slices.Index(create, "--base"); i < 0 || i+1 >= len(create) || create[i+1] != "jig/JIG-0" {
		t.Errorf("gh pr create = %v, want --base jig/JIG-0", create)
	}
}

// TestPublishOfAStackedTicketWhoseParentMergedGoesIntoTheTarget: once the
// parent merged in the repo, the pull request goes into the target, and the
// body has no Stacked on section.
func TestPublishOfAStackedTicketWhoseParentMergedGoesIntoTheTarget(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	d.Stack = func(ticket string) (stack.Plan, error) {
		return stack.Plan{Ticket: ticket, Repos: []stack.Repo{{
			Repo: "fixture-repo", Target: "main", StartFrom: "origin/main", PRTarget: "main",
			Parent: "JIG-0", ParentBranch: "jig/JIG-0", Merged: true,
		}}}, nil
	}
	report, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if report.Repos[0].Target != "main" {
		t.Errorf("report = %+v, want the pull request into main", report)
	}
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.Repos[0].PRBody)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "## Stacked on") {
		t.Errorf("the body of a ticket whose parent merged has a Stacked on section:\n%s", body)
	}
}

// TestRenderStackedSection: the section names the parent's branch while the
// pull request goes into it, and is not there otherwise.
func TestRenderStackedSection(t *testing.T) {
	t.Parallel()
	shared := stack.Repo{Repo: "api", Target: "main", PRTarget: "jig/JIG-0", Parent: "JIG-0", ParentBranch: "jig/JIG-0"}
	if got, want := renderStackedSection("JIG-1", shared), "## Stacked on JIG-0\n\nJIG-1 builds on JIG-0: this pull request goes into `jig/JIG-0`, the branch of JIG-0. Once JIG-0 merges, retarget it to `main`.\n\n"; got != want {
		t.Errorf("renderStackedSection = %q, want %q", got, want)
	}
	merged := shared
	merged.Merged, merged.PRTarget = true, "main"
	for name, r := range map[string]stack.Repo{"merged": merged, "own repo": {Repo: "web", Target: "main", PRTarget: "main"}} {
		if got := renderStackedSection("JIG-1", r); got != "" {
			t.Errorf("%s: renderStackedSection = %q, want none", name, got)
		}
	}
}
