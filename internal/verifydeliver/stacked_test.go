package verifydeliver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
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
	want := "## Stacked on JIG-0\n\n" + fx.Ticket + " builds on JIG-0: this pull request goes into `jig/JIG-0`, the branch of JIG-0. Once JIG-0 merges, retarget it to `main`.\n\n## What\n"
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

// TestPublishRetargetsTheOpenPRFoundByBranch: a stacked ticket's pull request
// that still goes into the branch of a parent that has merged since (GitHub does
// not retarget it when the branch is kept) is the ticket's open pull request all
// the same: publish updates it in one `gh pr edit` that also points it at the
// target, opens no second one, and the report says it was retargeted. A pull
// request already into the target is updated and left where it is.
func TestPublishRetargetsTheOpenPRFoundByBranch(t *testing.T) {
	t.Parallel()
	mergedParent := func(ticket string) (stack.Plan, error) {
		return stack.Plan{Ticket: ticket, Repos: []stack.Repo{{
			Repo: "fixture-repo", Target: "main", StartFrom: "origin/main", PRTarget: "main",
			Parent: "JIG-0", ParentBranch: "jig/JIG-0", Merged: true,
		}}}, nil
	}
	for _, tc := range []struct {
		name       string
		base       string
		retargeted string
	}{
		{"into the merged parent's branch", "jig/JIG-0", "main"},
		{"already into the target", "main", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx, d, head := reviewedInTheClone(t)
			d.Stack = mergedParent
			branch := ticketBranch(fx.Ticket)
			pulls := fixture.GhPulls(t, fixture.GhPull{Number: 7, Owner: "owner", Head: branch, Base: tc.base})
			logFile, _ := useGithubHost(t, &d, pulls, "")

			report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			const url = "https://github.example/owner/repo/pull/7"
			if report.PRURL["fixture-repo"] != url || !report.PRUpdated["fixture-repo"] || report.PRRetargeted["fixture-repo"] != tc.retargeted {
				t.Errorf("report = url %q updated %v retargeted %q, want %s updated, retargeted to %q", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"], report.PRRetargeted["fixture-repo"], url, tc.retargeted)
			}
			if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != head {
				t.Errorf("origin's %s = %s, want the reviewed head %s", branch, got, head)
			}
			var edits, creates [][]string
			for _, argv := range loggedGh(t, logFile) {
				switch {
				case len(argv) >= 2 && argv[0] == "pr" && argv[1] == "edit":
					edits = append(edits, argv)
				case len(argv) >= 2 && argv[0] == "pr" && argv[1] == "create":
					creates = append(creates, argv)
				}
			}
			if len(creates) != 0 {
				t.Errorf("publish opened a second pull request: %v", creates)
			}
			if len(edits) != 1 || edits[0][2] != url {
				t.Fatalf("pr edit calls = %v, want exactly one, of %s", edits, url)
			}
			i := slices.Index(edits[0], "--base")
			switch {
			case tc.retargeted == "" && i >= 0:
				t.Errorf("gh pr edit = %v, want no --base: the pull request already goes into main", edits[0])
			case tc.retargeted != "" && (i < 0 || i+1 >= len(edits[0]) || edits[0][i+1] != tc.retargeted):
				t.Errorf("gh pr edit = %v, want --base %s in the same call", edits[0], tc.retargeted)
			case tc.retargeted != "" && !slices.Contains(edits[0], "--body-file"):
				t.Errorf("gh pr edit = %v, want --body-file in the same call", edits[0])
			}
			lines, err := journal.Read(d.Store, fx.Ticket)
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range lines {
				if l.Event == journal.EventPr && l.Outcome != "updated" {
					t.Errorf("pr line = %+v, want the outcome updated", l)
				}
			}
		})
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
