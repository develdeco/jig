package main

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
)

// TestDigestReadsNoStackPlanForAnUnstackedTask: only a ticket stacked on another
// (or shared by two issues) is read with a stack plan, which costs git; any
// other has an empty plan, whether or not it published.
func TestDigestReadsNoStackPlanForAnUnstackedTask(t *testing.T) {
	t.Parallel()
	r, merges := digestRig(t)
	r.ticket(t, "TK-14", "blocked_by:\n  - ticket: TK-12\n    kind: stacked\n", true)
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"TK-2": 0, "TK-6": 0, "TK-7": 0, "TK-12": 0, "TK-14": 1} {
		rec, err := r.st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		plan, _, err := taskPlan(r.st, cfg, project.MachineProject{}, merges, id, rec)
		if err != nil || len(plan.Repos) != want {
			t.Errorf("%s: a plan of %d repos, %v, want %d", id, len(plan.Repos), err, want)
		}
	}
}

// TestDigestPRTargetIsTheContainersBranch: the pull request of a task of an issue
// goes into the issue's branch, plan or no plan, and a task of none goes into the
// target; `jig next <ticket>` says the same.
func TestDigestPRTargetIsTheContainersBranch(t *testing.T) {
	t.Parallel()
	r, merges := digestRig(t)
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := nextForStore(r.st, cfg, project.MachineProject{}, merges)
	if err != nil {
		t.Fatal(err)
	}
	prs := map[string][]nextDigestPR{}
	for _, row := range fleet.Tickets {
		prs[row.ID] = row.Digest.PRs
	}
	issue, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := r.st.ResolveTicketBranch("TK-1", issue, "main")
	if err != nil || branch == "main" {
		t.Fatalf("the issue's branch = %q, %v", branch, err)
	}
	if got := prs["TK-2"]; len(got) != 1 || got[0].Target != branch {
		t.Errorf("TK-2 is a task of the issue: its pull request goes into %+v, want %s", got, branch)
	}
	if got := prs["TK-6"]; len(got) != 1 || got[0].Target != "main" {
		t.Errorf("TK-6 is a task of no issue: its pull request goes into %+v, want main", got)
	}
}

// TestDigestReadsEachJournalOnce: the digest takes the journal lines the phase
// derivation read, and reads none itself: with the ticket's journal gone, the
// pull requests and the slice counts are the same.
func TestDigestReadsEachJournalOnce(t *testing.T) {
	t.Parallel()
	r, merges := digestRig(t)
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"TK-9", "TK-6"} {
		data, err := readTicketData(r.st, id)
		if err != nil {
			t.Fatal(err)
		}
		plan, shared, err := taskPlan(r.st, cfg, project.MachineProject{}, merges, id, data.rec)
		if err != nil {
			t.Fatal(err)
		}
		tk, lines, err := derivePhaseStackedLines(r.st, id, merges, plan)
		if err != nil {
			t.Fatal(err)
		}
		want := taskDigest(r.st, cfg, merges, id, data, "ready", &tk, "", plan, shared, lines, nil)
		if err := os.Remove(journal.Path(r.st, id)); err != nil {
			t.Fatal(err)
		}
		got := taskDigest(r.st, cfg, merges, id, data, "ready", &tk, "", plan, shared, lines, nil)
		if len(got.PRs) != len(want.PRs) || *got.Slices != *want.Slices || (id == "TK-6" && len(got.PRs) != 1) || (id == "TK-9" && got.Slices.Failed != 2) {
			t.Errorf("%s: with its journal gone the digest is %+v, want %+v: it read the journal again", id, got, want)
		}
	}
}
