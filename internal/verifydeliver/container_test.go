package verifydeliver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/stack"
)

// inContainer is a Deps.Stack that gives the ticket the container I-1, whose
// branch is I-1-issue, in the fixture repo.
func inContainer(ticket string) (stack.Plan, error) {
	return stack.Plan{Ticket: ticket, Repos: []stack.Repo{{
		Repo: "fixture-repo", Target: "main", StartFrom: "origin/main", PRTarget: "I-1-issue",
		Container: "I-1", ContainerBranch: "I-1-issue", ContainerFrom: "origin/main",
	}}}, nil
}

// TestContainerPublishGoesIntoTheContainersBranch: a ticket with a container
// has its pull request into the container's branch, in the body's report and in
// `gh pr create`, with no Stacked on section, and a branch the origin lacks
// does not refuse the publish (the host would).
func TestContainerPublishGoesIntoTheContainersBranch(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	d.Stack = inContainer
	logFile, _ := useGithubHost(t, &d, "", "")

	report, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if len(report.Repos) != 1 || report.Repos[0].Target != "I-1-issue" || report.Repos[0].Head != head {
		t.Fatalf("report = %+v, want the reviewed head into I-1-issue", report)
	}
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.Repos[0].PRBody)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "## Stacked on") {
		t.Errorf("the body of a ticket with a container has a Stacked on section:\n%s", body)
	}

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish into a container's branch the origin lacks: %v", err)
	}
	var create []string
	for _, argv := range loggedGh(t, logFile) {
		if len(argv) >= 2 && argv[0] == "pr" && argv[1] == "create" {
			create = argv
		}
	}
	if i := slices.Index(create, "--base"); i < 0 || i+1 >= len(create) || create[i+1] != "I-1-issue" {
		t.Errorf("gh pr create = %v, want --base I-1-issue", create)
	}
}

// TestContainerRenderStackedSection: a stacked ticket with a container is told
// to retarget to the container's branch once its parent merges, not to the
// target; a ticket with only a container has no section.
func TestContainerRenderStackedSection(t *testing.T) {
	t.Parallel()
	stacked := stack.Repo{Repo: "api", Target: "main", PRTarget: "jig/JIG-0", Parent: "JIG-0", ParentBranch: "jig/JIG-0", Container: "I-1", ContainerBranch: "I-1-issue"}
	if got, want := renderStackedSection("JIG-1", stacked), "## Stacked on JIG-0\n\nJIG-1 builds on JIG-0: this pull request goes into `jig/JIG-0`, the branch of JIG-0. Once JIG-0 merges, retarget it to `I-1-issue`.\n\n"; got != want {
		t.Errorf("renderStackedSection = %q, want %q", got, want)
	}
	only := stack.Repo{Repo: "api", Target: "main", PRTarget: "I-1-issue", Container: "I-1", ContainerBranch: "I-1-issue"}
	if got := renderStackedSection("JIG-1", only); got != "" {
		t.Errorf("a ticket with only a container has a section %q", got)
	}
}

// TestContainerGateHoldsATicketToItsContainersBranch: for a ticket with a
// container, the branch a merge must bring in to count as the base's is the
// container's: a merge of it is carried over, a merge of main is read.
func TestContainerGateHoldsATicketToItsContainersBranch(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	d.Stack = inContainer
	branch := ticketBranch(fx.Ticket)

	run(t, fx.RepoDir, "checkout", "-b", "I-1-issue", "main")
	writeAndCommit(t, fx.RepoDir, "container.txt", "from the container", "the container moves")
	run(t, fx.RepoDir, "push", "origin", "I-1-issue")
	run(t, fx.RepoDir, "checkout", branch)
	run(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "I-1-issue")
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare after merging the container's branch: %v", err)
	}
	if p.Carried == nil || p.Carried.CarriedFrom != 1 {
		t.Fatalf("Prepared = %+v, want round 2 carried over: the container's branch is the ticket's base", p)
	}

	targetMoves(t, fx, "main1.txt")
	p, err = GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare after merging main: %v", err)
	}
	if p.Carried != nil || p.Round != 3 {
		t.Fatalf("Prepared = %+v, want an open round 3: a merge of main is read for a ticket with a container", p)
	}
}

// TestContainerGateScopeIsTheTicketsOwnChange: a task branched from its
// container's branch, which holds another task's merged work, is reviewed over
// its own change alone (the pull request's diff), not over the other task's
// file too; the control, with no container, reads both.
func TestContainerGateScopeIsTheTicketsOwnChange(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		stack     func(string) (stack.Plan, error)
		wantOther bool
	}{
		{"with a container", inContainer, false},
		{"without one", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx, d, _ := cloneBuilt(t)
			d.Stack = c.stack
			branch := ticketBranch(fx.Ticket)
			run(t, fx.RepoDir, "checkout", "-b", "I-1-issue", "main")
			writeAndCommit(t, fx.RepoDir, "other-task.txt", "another task's work", "another task merged into the container")
			run(t, fx.RepoDir, "push", "origin", "I-1-issue")
			run(t, fx.RepoDir, "checkout", branch)
			run(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "I-1-issue")

			p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
			if err != nil {
				t.Fatalf("GatePrepare: %v", err)
			}
			if got := slices.Contains(p.Request.MustReview, "other-task.txt"); got != c.wantOther || len(p.Request.MustReview) == 0 {
				t.Errorf("must_review = %v, want other-task.txt in it: %v, and the ticket's own files", p.Request.MustReview, c.wantOther)
			}
		})
	}
}
