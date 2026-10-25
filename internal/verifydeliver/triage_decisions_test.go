package verifydeliver

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// triageLine is a `jig triage` line as cmd/jig journals it.
func triageLine(ref, outcome, provenance, reason, target string) journal.Line {
	return journal.Line{Event: journal.EventTriage, Ref: ref, Outcome: outcome, Provenance: provenance, Summary: reason, Command: target}
}

// TestTriageDecisionsFoldsTheJournal: the latest line per finding stands, with
// the reason, who decided and the build target the person chose; it is pending
// when it comes after the last gate-round or gate-clean line and spent before
// it.
func TestTriageDecisionsFoldsTheJournal(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		{Event: journal.EventGateOpen, Attempt: 1},
		{Event: journal.EventGateRound, Attempt: 1},
		triageLine("r1-f1", TriageKeep, "person", "fix it", TriageTarget("alpha", "test")),
		triageLine("r1-f2", TriageDismiss, "agent", "", ""),
		triageLine("r1-f2", TriageKeep, "person", "on second thought", ""),
		{Event: journal.EventGateOpen, Attempt: 2},
		{Event: journal.EventGateClean, Attempt: 2},
		triageLine("r1-f3", TriageDismiss, "person", "no", ""),
		{Event: journal.EventTriage, Outcome: TriageKeep},
	}
	want := map[string]TriageDecision{
		"r1-f1": {Ref: "r1-f1", Outcome: TriageKeep, Provenance: "person", Reason: "fix it", Workspace: "alpha", Oracle: "test"},
		"r1-f2": {Ref: "r1-f2", Outcome: TriageKeep, Provenance: "person", Reason: "on second thought"},
		"r1-f3": {Ref: "r1-f3", Outcome: TriageDismiss, Provenance: "person", Reason: "no", Pending: true},
	}
	if got := TriageDecisions(lines); !reflect.DeepEqual(got, want) {
		t.Errorf("TriageDecisions = %+v, want %+v", got, want)
	}

	// With no round closed, every decision is pending.
	for ref, d := range TriageDecisions([]journal.Line{lines[2], lines[3]}) {
		if !d.Pending {
			t.Errorf("%s is spent with no round closed", ref)
		}
	}
	if got := TriageDecisions(nil); len(got) != 0 {
		t.Errorf("TriageDecisions(nil) = %v, want none", got)
	}
}

// TestTriageDecisionLabel: the decision as status and the request show it
// follows route.go's rule for a kept ask: it needs a workspace and an oracle,
// from the finding or from the decision, or the round leaves it asked.
func TestTriageDecisionLabel(t *testing.T) {
	t.Parallel()
	buildable := Finding{ID: "r1-f1", Workspace: "alpha", Oracle: "test"}
	noWorkspace := Finding{ID: "r1-f1", Oracle: "test"}
	noOracle := Finding{ID: "r1-f1", Workspace: "alpha"}
	neither := Finding{ID: "r1-f1"}
	keep := TriageDecision{Outcome: TriageKeep, Pending: true}
	for _, tc := range []struct {
		name string
		d    TriageDecision
		f    Finding
		want string
	}{
		{"a keep of a buildable ask", keep, buildable, "keep"},
		{"a keep with no workspace", keep, noWorkspace, "keep (no build target)"},
		{"a keep with no oracle", keep, noOracle, "keep (no build target)"},
		{"a keep with neither", keep, neither, "keep (no build target)"},
		{"a keep that names the workspace", TriageDecision{Outcome: TriageKeep, Pending: true, Workspace: "alpha"}, noWorkspace, "keep"},
		{"a keep that names the oracle", TriageDecision{Outcome: TriageKeep, Pending: true, Oracle: "test"}, noOracle, "keep"},
		{"a keep that names both", TriageDecision{Outcome: TriageKeep, Pending: true, Workspace: "alpha", Oracle: "test"}, neither, "keep"},
		{"a dismissal needs no target", TriageDecision{Outcome: TriageDismiss, Pending: true}, neither, "dismiss"},
		{"a spent keep", TriageDecision{Outcome: TriageKeep}, buildable, ""},
		{"a spent dismissal", TriageDecision{Outcome: TriageDismiss}, buildable, ""},
		{"no decision", TriageDecision{}, buildable, ""},
		{"an outcome that is no decision", TriageDecision{Outcome: "maybe", Pending: true}, buildable, ""},
	} {
		if got := tc.d.Label(tc.f); got != tc.want {
			t.Errorf("%s: Label = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestOpenRowsCarryAPendingDecision: the request's open rows keep every
// finding in place, and an asked one with a pending decision says what was
// decided, by whom and why. A spent decision, and a finding that is not an
// ask, are not annotated.
func TestOpenRowsCarryAPendingDecision(t *testing.T) {
	t.Parallel()
	findings := []Finding{
		{ID: "r1-f1", Status: StatusAsked, Workspace: "alpha", Oracle: "test"},
		{ID: "r1-f2", Status: StatusAsked},
		{ID: "r1-f3", Status: StatusAsked, Workspace: "alpha", Oracle: "test"},
		{ID: "r1-f4", Status: StatusOpen, Workspace: "alpha", Oracle: "test"},
		{ID: "r1-f5", Status: StatusAsked},
	}
	decisions := TriageDecisions([]journal.Line{
		triageLine("r1-f3", TriageKeep, "person", "spent", ""),
		{Event: journal.EventGateRound, Attempt: 1},
		triageLine("r1-f1", TriageDismiss, "person", "intended", ""),
		triageLine("r1-f2", TriageKeep, "agent", "later", ""),
		triageLine("r1-f4", TriageDismiss, "person", "not an ask", ""),
	})
	rows := toOpenFindingList(findings, decisions)
	if len(rows) != len(findings) {
		t.Fatalf("%d rows for %d findings: the decided ask must stay in the open list", len(rows), len(findings))
	}
	for _, tc := range []struct {
		id, decision, by, reason, text string
	}{
		{"r1-f1", "dismiss", "person", "intended", "decided: dismiss, by a person: intended"},
		{"r1-f2", "keep (no build target)", "agent", "later", "decided: keep (no build target), by an agent: later"},
		{"r1-f3", "", "", "", ""},
		{"r1-f4", "", "", "", ""},
		{"r1-f5", "", "", "", ""},
	} {
		var row OpenFinding
		for _, r := range rows {
			if r.ID == tc.id {
				row = r
			}
		}
		if row.Decision != tc.decision || row.DecidedBy != tc.by || row.DecisionReason != tc.reason || row.DecisionText() != tc.text {
			t.Errorf("%s: decision %q by %q (%q), text %q; want %q by %q (%q), text %q",
				tc.id, row.Decision, row.DecidedBy, row.DecisionReason, row.DecisionText(), tc.decision, tc.by, tc.reason, tc.text)
		}
	}
	if text := (OpenFinding{Decision: "dismiss", DecidedBy: "person"}).DecisionText(); text != "decided: dismiss, by a person" {
		t.Errorf("a decision with no reason reads %q", text)
	}
	if rows := toOpenFindingList(findings[:1], nil); rows[0].Decision != "" {
		t.Errorf("a request with no decisions annotated a row: %+v", rows[0])
	}
}
