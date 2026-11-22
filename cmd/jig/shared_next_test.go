package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
)

// issueMerges is a mergeSource that answers for a shared task from a table:
// "TICKET/ISSUE" is the merge fact into that issue, "TICKET" the whole one.
type issueMerges map[string]ready.Merge

func (m issueMerges) Of(ticket string) (ready.Merge, error) { return m[ticket], nil }
func (m issueMerges) OfFor(ticket, issue string) (ready.Merge, error) {
	return m[ticket+"/"+issue], nil
}

const (
	sharedPR1 = "https://example.test/shop/pull/1"
	sharedPR2 = "https://example.test/shop/pull/2"
)

func mergedInto(url string) ready.Merge {
	return ready.Merge{Published: true, Repos: []ready.RepoMerge{{PR: url, State: ready.PRMerged, Met: true, By: "pr"}}}
}

func openInto(url string) ready.Merge {
	return ready.Merge{Published: true, Repos: []ready.RepoMerge{{PR: url, State: ready.PROpen, Why: "its PR is open (" + url + ")"}}}
}

// sharedRig is two issues, SHOP-1 and SHOP-2, each with a chart that lists the
// task SHOP-3, and a dependent of it in each chart (SHOP-4 in the first, SHOP-5
// in the second) that waits for it to merge.
func sharedRig(t *testing.T) chartRig {
	t.Helper()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "kind: issue\n", false)
	r.ticket(t, "SHOP-2", "kind: issue\n", false)
	r.ticket(t, "SHOP-3", "", true)
	r.ticket(t, "SHOP-4", "blocked_by:\n  - ticket: SHOP-3\n    kind: merged\n", true)
	r.ticket(t, "SHOP-5", "blocked_by:\n  - ticket: SHOP-3\n    kind: merged\n", true)
	r.chart(t, "a", "issue: SHOP-1\ntickets:\n  - id: SHOP-3\n    title: Shared\n  - id: SHOP-4\n    title: Dependent A\n", "")
	r.chart(t, "b", "issue: SHOP-2\ntickets:\n  - id: SHOP-3\n    title: Shared\n  - id: SHOP-5\n    title: Dependent B\n", "")
	return r
}

// published records the pull request and the publish of SHOP-3 into issue.
func (r chartRig) publishedInto(t *testing.T, issue, url, head string) {
	t.Helper()
	r.green(t, "SHOP-3")
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: issue, URL: url, Commit: head},
		{Event: journal.EventPublishDone, Into: issue},
	} {
		if err := journal.Append(r.st, "SHOP-3", l); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSharedChartStanding: a task merged into the first issue and open in the
// second is done in the first chart, which starts its dependent, and waiting on
// a merge in the second, whose dependent still waits; a claim of each dependent
// says the same. Neither issue's copy releases the other's dependent.
func TestSharedChartStanding(t *testing.T) {
	t.Parallel()
	r := sharedRig(t)
	r.publishedInto(t, "SHOP-1", sharedPR1, "h1")
	r.publishedInto(t, "SHOP-2", sharedPR2, "h1")
	merges := issueMerges{"SHOP-3/SHOP-1": mergedInto(sharedPR1), "SHOP-3/SHOP-2": openInto(sharedPR2)}
	cfg, mp := project.Config{}, project.MachineProject{}

	a, _, err := deriveChart(r.st, cfg, mp, "a", merges)
	if err != nil {
		t.Fatal(err)
	}
	if want := "SHOP-4"; !a.Ready || len(a.Run) != 1 || len(a.Run[0]) != 1 || a.Run[0][0] != want {
		t.Errorf("chart a: ready %v run %v code %q, want %s to run: the task merged into its issue", a.Ready, a.Run, a.Code, want)
	}
	b, _, err := deriveChart(r.st, cfg, mp, "b", merges)
	if err != nil {
		t.Fatal(err)
	}
	if b.Ready || b.Code != phase.CodeChartWaitsLand || len(b.Ends) != 1 || b.Ends[0].ID != "SHOP-5" || b.Ends[0].Because.Ticket != "SHOP-3" || len(b.Ends[0].Because.PRs) != 1 || b.Ends[0].Because.PRs[0].URL != sharedPR2 {
		t.Errorf("chart b: ready %v code %q ends %+v, want SHOP-5 waiting on the task's pull request into SHOP-2", b.Ready, b.Code, b.Ends)
	}
	// The task two issues share is asked for per issue.
	if len(b.Lands) != 1 || b.Lands[0].ID != "SHOP-3" || b.Lands[0].Prompt != "Land SHOP-3 --issue SHOP-2" {
		t.Errorf("chart b: lands = %+v, want SHOP-3 landed with --issue SHOP-2", b.Lands)
	}
	if help := chartHelp(b); len(help) != 1 || !strings.HasPrefix(help[0], "Land SHOP-3 --issue SHOP-2 (or merge it on the host), then ask: ") {
		t.Errorf("chart b: help = %v, want the landing with its issue", help)
	}

	for dependent, wantProblem := range map[string]bool{"SHOP-4": false, "SHOP-5": true} {
		p, err := blockerProblem(r.st, dependent, "SHOP-3", "merged", claimDeps{merges: merges})
		if err != nil || (p != nil) != wantProblem {
			t.Errorf("blockerProblem(%s) = %+v, %v, want a problem: %v", dependent, p, err, wantProblem)
		}
	}
	// Judged by the journal alone, the blocker has published for the dependent's
	// own issue only.
	r2 := sharedRig(t)
	r2.publishedInto(t, "SHOP-1", sharedPR1, "h1")
	for dependent, wantProblem := range map[string]bool{"SHOP-4": false, "SHOP-5": true} {
		p, err := blockerProblem(r2.st, dependent, "SHOP-3", "merged", claimDeps{})
		if err != nil || (p != nil) != wantProblem {
			t.Errorf("by journal, blockerProblem(%s) = %+v, %v, want a problem: %v", dependent, p, err, wantProblem)
		}
	}
}

// sharedClone is a clone of a repo called app whose branch for SHOP-3 is one
// commit past main, returned with that commit; the project and machine mapping
// that name it.
func sharedClone(t *testing.T, r chartRig) (project.Config, project.MachineProject, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := gitx.Run(dir, args...)
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(out)
	}
	run("init", "-b", "main")
	run("config", "user.email", "fixture@example.invalid")
	run("config", "user.name", "fixture")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "base")
	rec, err := r.st.ReadTicket("SHOP-3")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := r.st.ResolveTicketBranch("SHOP-3", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	run("switch", "-c", branch)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "the task")
	head := run("rev-parse", "HEAD")
	cfg := project.Config{Repos: []project.Repo{{Remote: dir, Label: "app"}}}
	return cfg, project.MachineProject{Clones: map[string]string{"app": dir}}, head
}

// TestSharedNext: `jig next` of a task two issues share prints one row for each
// issue and repo and names the publish of the issue that has none; once both
// published, an issue that merged an older head reports stale and its fix is a
// new task, while the issue whose pull request is open takes the push.
func TestSharedNext(t *testing.T) {
	t.Parallel()
	r := sharedRig(t)
	cfg, mp, head := sharedClone(t, r)
	r.publishedInto(t, "SHOP-1", sharedPR1, head)
	merges := issueMerges{"SHOP-3/SHOP-1": mergedInto(sharedPR1)}

	n, err := nextForTicket(r.st, cfg, mp, "SHOP-3", "", false, "", merges)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Shared) != 2 {
		t.Fatalf("rows = %+v, want one for each issue", n.Shared)
	}
	if got := n.Shared[0]; got.Issue != "SHOP-1" || got.Repo != "app" || got.PR != sharedPR1 || got.State != issueMerged || got.Stale != "" || !strings.HasPrefix(got.Branch, "SHOP-1") {
		t.Errorf("row 1 = %+v, want SHOP-1's merged pull request, not stale, into SHOP-1's branch", got)
	}
	if got := n.Shared[1]; got.Issue != "SHOP-2" || got.PR != "-" || got.State != issueUnpublished {
		t.Errorf("row 2 = %+v, want SHOP-2 not published", got)
	}
	text := strings.Join(n.blocks(false), "\n")
	if !strings.Contains(text, "jig publish SHOP-3 --issue SHOP-2") || strings.Contains(text, "jig publish SHOP-3 --issue SHOP-1") {
		t.Errorf("jig next names the publish of SHOP-2 alone:\n%s", text)
	}

	// The task moves on after SHOP-1 merged it, and SHOP-2 has its pull request.
	dir := mp.Clones["app"]
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "a fix"}} {
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	r.publishedInto(t, "SHOP-2", sharedPR2, head)
	merges["SHOP-3/SHOP-2"] = openInto(sharedPR2)
	n, err = nextForTicket(r.st, cfg, mp, "SHOP-3", "", false, "", merges)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Shared) != 2 || !strings.HasPrefix(n.Shared[0].Stale, "stale: merged at "+head[:7]) || n.Shared[1].Stale != "" || n.Shared[1].State != issueOpen {
		t.Fatalf("rows = %+v, want SHOP-1 stale and SHOP-2 open", n.Shared)
	}
	text = strings.Join(n.blocks(false), "\n")
	if !strings.Contains(text, "the fix for SHOP-1 is a new task in its chart") || strings.Contains(text, "jig publish SHOP-3 --issue") {
		t.Errorf("jig next of the stale task:\n%s", text)
	}
}
