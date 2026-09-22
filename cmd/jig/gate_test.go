package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPrintGateReportShowsFindingsWithFileLineAndRationale pins F10b
// (design 6.4: "Findings are always shown sorted by risk, high first, each
// with its rationale"): the gate report's own findings table and its
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
