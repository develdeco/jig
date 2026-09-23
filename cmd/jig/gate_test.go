package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPrintGateReportShowsFindingsWithFileLineAndRationale pins the rule
// that findings are always shown sorted by risk, high first, each
// with its rationale: the gate report's own findings table and its
// needs_a_human table must carry file:line and risk_rationale, not just
// title - the triage prompt and notes/fixes tables are covered by
// triage_test.go.
func TestPrintGateReportShowsFindingsWithFileLineAndRationale(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	report := verifydeliver.GateReport{
		Round:   1,
		Verdict: "fix-slices",
		Scope:   "full",
		Findings: []verifydeliver.Finding{
			{ID: "r1-f1", File: "alpha/alpha.go", Line: 5, Title: "FIX-TITLE", Status: verifydeliver.StatusOpen, Risk: "high", RiskRationale: "FIX-RATIONALE"},
		},
		NeedsHuman: []verifydeliver.Finding{
			{ID: "r1-f2", File: "beta/beta.go", Line: 4, Title: "ASK-TITLE", Status: verifydeliver.StatusAsked, Risk: "medium", RiskRationale: "ASK-RATIONALE"},
		},
	}

	var out bytes.Buffer
	printGateReport(&out, st, fx.Ticket, report)
	text := out.String()

	for _, want := range []string{"alpha/alpha.go:5", "FIX-RATIONALE", "beta/beta.go:4", "ASK-RATIONALE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("gate report missing %q:\n%s", want, text)
		}
	}
}

// TestGateReportHintNamesDecidingAsksNotFixSlices pins the rule that a
// round leaving any ask undecided always names a human decision at a
// terminal as the way forward, never "work the fix-slice round" - true
// even when the round built no fix slices at all (every routed finding
// was an undecided ask).
func TestGateReportHintNamesDecidingAsksNotFixSlices(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	report := verifydeliver.GateReport{
		Round:   1,
		Verdict: "fix-slices",
		Scope:   "full",
		NeedsHuman: []verifydeliver.Finding{
			{ID: "r1-f1", File: "beta/beta.go", Line: 4, Title: "ASK-TITLE", Status: verifydeliver.StatusAsked, Risk: "medium", RiskRationale: "ASK-RATIONALE"},
		},
	}

	hint := strings.Join(gateReportHint(st, fx.Ticket, report), "\n")
	if strings.Contains(hint, "fix-slice round") {
		t.Fatalf("hint = %q, must not point at fix-slice work when only an ask is undecided", hint)
	}
	if !strings.Contains(hint, "jig gate "+fx.Ticket) || !strings.Contains(hint, "decide the listed asks") {
		t.Fatalf("hint = %q, want it to point at a terminal `jig gate` to decide the listed asks", hint)
	}
}

// TestGateReportHintNamesTheOrderWhenFixSlicesAreQueuedToo pins the rule
// that when the same round both queues fix slices (which checkFrontier
// refuses the next `jig gate` until green) and leaves an ask undecided, the
// hint names the order: work the fix slices first, then gate at a
// terminal. The printed `jig gate <ticket>` command alone cannot work in
// this state, since checkFrontier would refuse it.
func TestGateReportHintNamesTheOrderWhenFixSlicesAreQueuedToo(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	report := verifydeliver.GateReport{
		Round:   1,
		Verdict: "fix-slices",
		Scope:   "full",
		NeedsHuman: []verifydeliver.Finding{
			{ID: "r1-f1", File: "beta/beta.go", Line: 4, Title: "ASK-TITLE", Status: verifydeliver.StatusAsked, Risk: "medium", RiskRationale: "ASK-RATIONALE"},
		},
		FixSlices: []string{"fix-1-alpha-test"},
	}

	lines := gateReportHint(st, fx.Ticket, report)
	joined := strings.Join(lines, "\n")
	if len(lines) < 2 {
		t.Fatalf("hint = %v, want several lines naming the order", lines)
	}
	runIdx := strings.Index(joined, "jig run "+fx.Ticket)
	gateIdx := strings.Index(joined, "jig gate "+fx.Ticket)
	if runIdx < 0 || gateIdx < 0 || runIdx > gateIdx {
		t.Fatalf("hint = %q, want `jig run` named before `jig gate`", joined)
	}
}
