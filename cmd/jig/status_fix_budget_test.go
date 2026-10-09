package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// setFixRounds appends a gate.fix_rounds override to storeDir's
// project.yaml, store-side (the way a project opts into a smaller or
// larger fix budget than the default of 3).
func setFixRounds(t *testing.T, storeDir string, n int) {
	t.Helper()
	path := filepath.Join(storeDir, "project.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read project.yaml: %v", err)
	}
	data = append(data, []byte(fmt.Sprintf("gate:\n  fix_rounds: %d\n", n))...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
}

// writeGateReportYAML records one gate round's report.yaml holding
// fixSlices (the fix slices that round appended, UsedFixBudget's own
// source), the minimal shape `jig status` and UsedFixBudget both read.
func writeGateReportYAML(t *testing.T, storeDir, ticket string, round int, fixSlices []string) {
	t.Helper()
	dir := filepath.Join(storeDir, "tickets", ticket, "gate", fmt.Sprintf("round-%d", round))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "round: %d\nverdict: fix-slices\n", round)
	if len(fixSlices) > 0 {
		b.WriteString("fix_slices:\n")
		for _, id := range fixSlices {
			fmt.Fprintf(&b, "  - %s\n", id)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write report.yaml: %v", err)
	}
}

// writeParkedFindingYAML records round's findings.yaml holding a single
// budget-parked finding: status asked, routed_as ask, routed_why budget.
func writeParkedFindingYAML(t *testing.T, storeDir, ticket string, round int) {
	t.Helper()
	dir := filepath.Join(storeDir, "tickets", ticket, "gate", fmt.Sprintf("round-%d", round))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	body := "scope: full\n" +
		"reviewed_paths:\n" +
		"  - a.go\n" +
		"findings:\n" +
		"  - id: r1-f1\n" +
		"    file: a.go\n" +
		"    line: 1\n" +
		"    title: fix me\n" +
		"    detail: it needs fixing\n" +
		"    action: fix\n" +
		"    risk: high\n" +
		"    risk_rationale: it matters\n" +
		"    oracle: test\n" +
		"    workspace: root\n" +
		"    status: asked\n" +
		"    recurrences: 0\n" +
		"    routed_as: ask\n" +
		"    routed_why: budget\n" +
		"summary: round 1\n"
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write findings.yaml: %v", err)
	}
}

// TestRenderStatusNoFixBudgetLineBeforeAnyGateRound pins "once the ticket
// has a gate round": a ticket with no gate rounds at all must not print a
// fix_budget line - there is nothing yet to report a used/limit count
// against.
func TestRenderStatusNoFixBudgetLineBeforeAnyGateRound(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if strings.Contains(got, "fix_budget") {
		t.Errorf("status printed a fix_budget line before any gate round:\n%s", got)
	}
}

// TestRenderStatusFixBudgetLineNotReached pins the fix_budget line's own
// shape once the ticket has a gate round that used none of its budget:
// "<used> of <limit> rounds used", with no ", reached" suffix.
func TestRenderStatusFixBudgetLineNotReached(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	writeGateReportYAML(t, fx.StoreDir, fx.Ticket, 1, nil) // round 1 appended no fix slice

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if !strings.Contains(got, "fix_budget: 0 of 3 rounds used\n") {
		t.Fatalf("status missing the fix_budget line:\n%s", got)
	}
}

// TestRenderStatusFixBudgetLineReached pins the ", reached" suffix once
// the used count equals a project's own configured gate.fix_rounds.
func TestRenderStatusFixBudgetLineReached(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	setFixRounds(t, fx.StoreDir, 1)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	writeGateReportYAML(t, fx.StoreDir, fx.Ticket, 1, []string{"fix-1-root-test"})

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if !strings.Contains(got, "fix_budget: 1 of 1 rounds used, reached\n") {
		t.Fatalf("status missing the reached fix_budget line:\n%s", got)
	}
}

// TestRenderStatusOutstandingAsksWhyColumnAndParkedHelpLine covers both
// the outstanding_asks table's own "why" column and the help line's
// budget-aware wording together: a parked finding's row ends in "budget",
// and with the frontier green the help line says the budget is reached
// and names `jig gate <ticket>` at a terminal, rather than the generic
// "decide the outstanding asks" wording an ordinary ask gets. Round 1 is
// the one that used the ticket's whole (1-round) budget; round 2's own
// finding is the one the now-reached budget parks.
func TestRenderStatusOutstandingAsksWhyColumnAndParkedHelpLine(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	setFixRounds(t, fx.StoreDir, 1)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := st.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatalf("write slice state %s: %v", id, err)
		}
	}
	writeGateReportYAML(t, fx.StoreDir, fx.Ticket, 1, []string{"fix-1-root-test"})
	writeParkedFindingYAML(t, fx.StoreDir, fx.Ticket, 2)

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	wantTable := "outstanding_asks[1]{id,risk,file:line,title,why}:\n" +
		"  r1-f1,high,\"a.go:1\",fix me,budget\n"
	if !strings.Contains(got, wantTable) {
		t.Fatalf("status is missing the parked outstanding_asks row %q:\n%s", wantTable, got)
	}

	wantHelp := "help[1]:\n" +
		"  Run `jig gate JIG-1` at a terminal to keep or dismiss the parked finding(s): the fix budget is reached (1 of 1)\n"
	if !strings.HasSuffix(got, wantHelp) {
		t.Fatalf("status help does not name the budget-reached remedy:\n--- got ---\n%s--- want suffix ---\n%s", got, wantHelp)
	}
}
