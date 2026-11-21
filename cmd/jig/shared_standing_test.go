package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
)

// entryCode is the code of the ticket of the entry with id in chart's facts.
func entryCode(t *testing.T, facts phase.ChartFacts, id string) string {
	t.Helper()
	for _, e := range facts.Entries {
		if e.ID == id && e.Ticket != nil {
			return e.Ticket.Code
		}
	}
	t.Fatalf("chart %s has no read entry %s", facts.Name, id)
	return ""
}

// TestSharedChartStandingOfTheTaskItself: with the task published into the first
// issue alone, the first chart's entry for it is done by that pull request and
// the second chart's is not; the task read for no issue is not done until both
// are; a pending entry of the second chart that waits for it waits on the second
// issue's pull request.
func TestSharedChartStandingOfTheTaskItself(t *testing.T) {
	t.Parallel()
	r := sharedRig(t)
	r.chart(t, "b", "issue: SHOP-2\ntickets:\n  - id: SHOP-3\n    title: Shared\n  - id: SHOP-5\n    title: Dependent B\n  - title: Pending\n    blocked_by:\n      - ref: SHOP-3\n", "")
	r.publishedInto(t, "SHOP-1", sharedPR1, "h1")
	merges := issueMerges{"SHOP-3/SHOP-1": mergedInto(sharedPR1), "SHOP-3": openInto(sharedPR2)}
	cfg, mp := project.Config{}, project.MachineProject{}

	a, err := chartFacts(r.st, cfg, mp, "a", merges)
	if err != nil {
		t.Fatal(err)
	}
	b, err := chartFacts(r.st, cfg, mp, "b", merges)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryCode(t, a, "SHOP-3"); got != phase.CodeDone {
		t.Errorf("chart a's SHOP-3 = %q, want done by its issue's pull request", got)
	}
	if got := entryCode(t, b, "SHOP-3"); got == phase.CodeDone {
		t.Errorf("chart b's SHOP-3 is done, but only the other issue published it")
	}
	if tk, err := derivePhaseWith(r.st, "SHOP-3", merges); err != nil || tk.Code == phase.CodeDone {
		t.Errorf("SHOP-3 for no issue = %+v, %v, want it not done while one issue has not published", tk.Code, err)
	}
	// The pending entry's need is read for chart b's issue, which has no pull
	// request: it is not met by the first issue's merge.
	last := b.Entries[len(b.Entries)-1]
	if last.ID != "" || len(last.Needs) != 1 || last.Needs[0].Met {
		t.Errorf("pending entry = %+v, want one need on SHOP-3, not met", last)
	}

	r.publishedInto(t, "SHOP-2", sharedPR2, "h1")
	if tk, err := derivePhaseWith(r.st, "SHOP-3", merges); err != nil || tk.Code != phase.CodeDone {
		t.Errorf("SHOP-3 for no issue = %+v, %v, want done once both issues published", tk.Code, err)
	}
	merges["SHOP-3/SHOP-2"] = openInto(sharedPR2)
	b, err = chartFacts(r.st, cfg, mp, "b", merges)
	if err != nil {
		t.Fatal(err)
	}
	last = b.Entries[len(b.Entries)-1]
	if len(last.Needs) != 1 || last.Needs[0].Met || len(last.Needs[0].PRs) != 1 || last.Needs[0].PRs[0].URL != sharedPR2 {
		t.Errorf("pending entry = %+v, want it waiting on the second issue's pull request", last.Needs)
	}
}

// TestSharedNextStore: a listing of the store reads a shared blocker whole: a
// task merged into one issue and open in the other releases no dependent of
// either, and one published into a single issue holds both; `jig next` of one
// dependent reads it for the dependent's own issue.
func TestSharedNextStore(t *testing.T) {
	t.Parallel()
	r := sharedRig(t)
	r.publishedInto(t, "SHOP-1", sharedPR1, "h1")
	r.publishedInto(t, "SHOP-2", sharedPR2, "h1")
	merges := issueMerges{"SHOP-3/SHOP-1": mergedInto(sharedPR1), "SHOP-3/SHOP-2": openInto(sharedPR2), "SHOP-3": openInto(sharedPR2)}
	cfg, mp := project.Config{}, project.MachineProject{}

	ready := func(r chartRig, merges mergeSource) map[string]bool {
		t.Helper()
		store, err := nextForStore(r.st, cfg, mp, merges)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, id := range store.Next {
			out[id] = true
		}
		return out
	}
	if got := ready(r, merges); got["SHOP-4"] || got["SHOP-5"] {
		t.Errorf("the listing frees %v while the task is open in one issue", got)
	}
	n4, err := nextForTicket(r.st, cfg, mp, "SHOP-4", "", false, "", merges)
	if err != nil {
		t.Fatal(err)
	}
	n5, err := nextForTicket(r.st, cfg, mp, "SHOP-5", "", false, "", merges)
	if err != nil {
		t.Fatal(err)
	}
	if len(n4.BlockedBy) != 0 || len(n5.BlockedBy) != 1 {
		t.Errorf("jig next of each dependent: blocked %v and %v, want the first free and the second held", n4.BlockedBy, n5.BlockedBy)
	}

	r2 := sharedRig(t)
	r2.publishedInto(t, "SHOP-1", sharedPR1, "h1")
	both := issueMerges{"SHOP-3/SHOP-1": mergedInto(sharedPR1), "SHOP-3": mergedInto(sharedPR1)}
	if got := ready(r2, both); got["SHOP-4"] || got["SHOP-5"] {
		t.Errorf("the listing frees %v while the task published into one issue alone", got)
	}
}

// TestSharedStatus: `jig next` and `jig status` of a shared task print one row
// for each issue, and name the publish of the issue that has none.
func TestSharedStatus(t *testing.T) {
	t.Parallel()
	r := sharedRig(t)
	r.publishedInto(t, "SHOP-1", sharedPR1, "h1")
	for _, verb := range []string{"next", "status"} {
		out, code := r.run(t, verb, "SHOP-3")
		if code != 0 || !strings.Contains(out, "shared[2]{issue,repo,branch,pr,state}") || !strings.Contains(out, "SHOP-1,") || !strings.Contains(out, "SHOP-2,") || !strings.Contains(out, "jig publish SHOP-3 --issue SHOP-2") || strings.Contains(out, "jig publish SHOP-3 --issue SHOP-1") {
			t.Errorf("jig %s SHOP-3: exit %d, want the table of the two issues and the publish into SHOP-2 alone:\n%s", verb, code, out)
		}
	}
	out, code := r.run(t, "status", "SHOP-4")
	if code != 0 || strings.Contains(out, "shared[") {
		t.Errorf("jig status of a task of one issue prints a shared table:\n%s", out)
	}
}
