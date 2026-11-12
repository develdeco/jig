package verifydeliver

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestRouteRoundAppliesAPersonsDecisionOnANote: a note nobody reported this
// round (a standing note) that a person kept becomes an open finding with its
// own fix slice of this round, carrying their reason; one they dismissed
// becomes dismissed by a human; one a keep leaves with no build target stays a
// note; a note nobody decided is left exactly as it was and the round queues
// nothing for it.
func TestRouteRoundAppliesAPersonsDecisionOnANote(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	standing := func(id, ws string) Finding {
		return Finding{ID: id, File: id + ".go", Title: "t " + id, RiskRationale: "r", Action: ActionFix, Risk: RiskLow, Workspace: ws, Status: StatusNoted, RoutedAs: ActionNote}
	}
	outstanding := []Finding{standing("r1-f1", "root"), standing("r1-f2", "root"), standing("r1-f3", ""), standing("r1-f4", "root")}
	triage := func(in TriageInput) TriageResult {
		if len(in.Asks) != 0 || len(in.StandingNotes) != 4 {
			t.Fatalf("TriageInput = asks %+v, standing notes %+v, want the four notes offered as notes only", in.Asks, in.StandingNotes)
		}
		return TriageResult{Notes: map[string]AskOutcome{
			"r1-f1": {Keep: true, Decision: "do it now", Human: true},
			"r1-f2": {Keep: false, Human: true},
			"r1-f3": {Keep: true, Human: true}, // no workspace, none given
		}}
	}
	routed, slices, err := routeRound(2, st, "T-1", nil, nil, outstanding, triage, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 || slices[0].ID != "fix-2-root-test" || slices[0].FromGate != 2 || !equalStrings(slices[0].Findings, []string{"r1-f1"}) {
		t.Fatalf("slices = %+v, want the one fix-2-root-test of round 2", slices)
	}
	if !contains(slices[0].Goal, "do it now") || !contains(slices[0].Goal, "kept by the human") {
		t.Errorf("goal = %q, want the reason and who kept it", slices[0].Goal)
	}
	if f := findFinding(routed, "r1-f1"); f.Status != StatusOpen || f.Decision != "do it now" || f.Triage != TriageHuman || f.Oracle != "test" {
		t.Errorf("kept note = %+v, want open with the decision, a human and the oracle", f)
	}
	if f := findFinding(routed, "r1-f2"); f.Status != StatusDismissed || f.Triage != TriageHuman {
		t.Errorf("dismissed note = %+v, want dismissed by a human", f)
	}
	for _, id := range []string{"r1-f3", "r1-f4"} {
		if f := findFinding(routed, id); f.ID != "" {
			t.Errorf("%s was recorded by the round (%+v): a note left as it is stays out of it", id, f)
		}
	}
}

// TestRouteRoundDecidesANoteTheRoundReportedAgain: a note the reviewer
// reported again this round is decided in place, like an ask the round
// reported, and is not queued twice.
func TestRouteRoundDecidesANoteTheRoundReportedAgain(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	reported := []Finding{{ID: "r1-f1", File: "a.go", Title: "t", RiskRationale: "r", Action: ActionFix, Risk: RiskLow, Workspace: "root", Status: StatusNoted, RoutedAs: ActionNote}}
	triage := func(in TriageInput) TriageResult {
		if len(in.Notes) != 1 || len(in.StandingNotes) != 0 {
			t.Fatalf("TriageInput notes = %+v, standing = %+v", in.Notes, in.StandingNotes)
		}
		return TriageResult{Notes: map[string]AskOutcome{"r1-f1": {Keep: true}}}
	}
	routed, slices, err := routeRound(2, st, "T-1", nil, reported, nil, triage, oneRepoFiles(oneOracleManifest()), 0, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(routed) != 1 || routed[0].Status != StatusOpen || routed[0].Triage != TriageAuto || len(slices) != 1 {
		t.Fatalf("routed = %+v, slices = %v, want the one finding open (auto) and one slice", routed, sliceIDs(slices))
	}
}

// TestDefaultTriageNeverDecidesANote: no default keeps or dismisses a note, so
// --yes and an unattended round leave every note a note.
func TestDefaultTriageNeverDecidesANote(t *testing.T) {
	t.Parallel()
	res := DefaultTriage(TriageInput{
		Notes:         []Finding{{ID: "r1-f1", Status: StatusNoted}},
		StandingNotes: []Finding{{ID: "r1-f2", Status: StatusNoted}},
	})
	if len(res.Notes) != 0 {
		t.Fatalf("DefaultTriage decided notes: %+v", res.Notes)
	}
}

// TestRecordedTriageDecidesANoteOnlyByAPendingDecision: a person's keep or
// dismissal journaled since the last round closed decides a note, with the
// reason and build target they gave; a decision an earlier round already
// applied is spent, and --yes decides no note.
func TestRecordedTriageDecidesANoteOnlyByAPendingDecision(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	line := func(ref, outcome, summary string) journal.Line {
		return journal.Line{Event: journal.EventTriage, Ref: ref, Outcome: outcome, Summary: summary, Command: TriageTarget("alpha", "test"), Provenance: "person"}
	}
	for _, l := range []journal.Line{
		line("r1-f1", TriageKeep, "spent"),
		{Event: journal.EventGateRound, Attempt: 1},
		line("r1-f2", TriageKeep, "yes, fix it"),
		line("r1-f3", TriageDismiss, ""),
	} {
		if err := journal.Append(d.Store, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	in := TriageInput{
		Notes:         []Finding{{ID: "r1-f1"}, {ID: "r1-f4"}},
		StandingNotes: []Finding{{ID: "r1-f2"}, {ID: "r1-f3"}},
	}
	for _, yes := range []bool{false, true} {
		res := recordedTriage(d, fx.Ticket, yes)(in)
		want := map[string]AskOutcome{
			"r1-f2": {Keep: true, Decision: "yes, fix it", Workspace: "alpha", Oracle: "test", Human: true},
			"r1-f3": {Keep: false, Workspace: "alpha", Oracle: "test", Human: true},
		}
		if !reflect.DeepEqual(res.Notes, want) {
			t.Errorf("yes=%v: Notes = %+v, want %+v (r1-f1's decision is spent, r1-f4 has none)", yes, res.Notes, want)
		}
	}
}

// TestTriageOutstandingFindingsListsTheNotes: the notes a person may take up are the
// cumulative noted findings, sorted by risk then id, beside the asks.
func TestTriageOutstandingFindingsListsTheNotes(t *testing.T) {
	t.Parallel()
	st, ticketDir := gate7Rounds(t)
	_ = ticketDir
	asks, notes, unreadable, err := OutstandingFindings(st, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(asks) != 0 || len(unreadable) != 0 {
		t.Fatalf("asks = %+v, unreadable = %v", asks, unreadable)
	}
	var ids []string
	for _, f := range notes {
		ids = append(ids, f.ID)
	}
	// r1-f3 cleared at round 2 is no note any more; r1-f1 (continued as a
	// note) and the reviewer's own r1-f5 remain.
	if !reflect.DeepEqual(ids, []string{"r1-f1", "r1-f5"}) {
		t.Fatalf("notes = %v, want [r1-f1 r1-f5]", ids)
	}
}
