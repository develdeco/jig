package verifydeliver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
)

// writeAttemptResult writes a slice attempt's result.json (outcome.Result
// shape) under workDir, for previousFixSlice's summary lookup.
func writeAttemptResult(t *testing.T, workDir, sliceID string, attempt int, summary string) error {
	t.Helper()
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(outcome.Result{Outcome: outcome.Green, Summary: summary})
	if err != nil {
		return err
	}
	path := filepath.Join(workDir, fmt.Sprintf("%s.attempt-%d.result.json", sliceID, attempt))
	return os.WriteFile(path, data, 0o644)
}

func twoWorkspaceManifest() manifest.Manifest {
	return manifest.Manifest{
		Oracles: map[string]string{"test": "go test ./..."},
		Workspaces: []manifest.Workspace{
			{ID: "alpha", Path: "alpha"},
			{ID: "beta", Path: "beta"},
		},
	}
}

func findFinding(fs []Finding, id string) Finding {
	for _, f := range fs {
		if f.ID == id {
			return f
		}
	}
	return Finding{}
}

func sliceByID(t *testing.T, ss []store.Slice, id string) store.Slice {
	t.Helper()
	for _, s := range ss {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no slice %q among %v", id, sliceIDs(ss))
	return store.Slice{}
}

func sliceIDs(ss []store.Slice) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

// --- DefaultTriage -----------------------------------------------------

func TestDefaultTriageKeepsFixesAndWorkspaceAsksLeavesNoWorkspaceAsksUndecided(t *testing.T) {
	in := TriageInput{
		Fixes: []Finding{{ID: "r1-f1"}},
		Asks: []Finding{
			{ID: "r1-f2", Workspace: "alpha", Oracle: "test"},
			{ID: "r1-f3", Workspace: ""},
		},
		Manifest: oneOracleManifest(),
	}
	out := DefaultTriage(in)
	if out.DismissedFixIDs["r1-f1"] {
		t.Error("DefaultTriage dismissed a fix, want every fix kept")
	}
	dec, ok := out.Asks["r1-f2"]
	if !ok || !dec.Keep {
		t.Errorf("Asks[r1-f2] = %+v, ok=%v, want kept", dec, ok)
	}
	if _, ok := out.Asks["r1-f3"]; ok {
		t.Error("Asks[r1-f3] decided, want undecided (no declared workspace)")
	}
}

// TestDefaultTriageLeavesAnAskWithNoResolvableOracleUndecided is the
// oracle-side half: a workspace ask whose recorded oracle is neither valid
// nor defaultable (more than one manifest oracle, none recorded) stays
// undecided even though it has a workspace.
func TestDefaultTriageLeavesAnAskWithNoResolvableOracleUndecided(t *testing.T) {
	man := oneOracleManifest()
	man.Oracles["lint"] = "true" // now two oracles: no single default
	in := TriageInput{
		Asks:     []Finding{{ID: "r1-f1", Workspace: "root"}},
		Manifest: man,
	}
	out := DefaultTriage(in)
	if _, ok := out.Asks["r1-f1"]; ok {
		t.Error("Asks[r1-f1] decided, want undecided (no oracle jig can resolve)")
	}
}

// TestResolveOracleWithOneManifestOracle pins the other half of the same
// rule: with exactly one manifest oracle there is no choice to make, so a
// finding that records none, or records a name the manifest no longer has
// (the manifest changed between rounds), resolves to that one oracle in
// every mode. Only a manifest with several oracles leaves the choice to a
// human.
func TestResolveOracleWithOneManifestOracle(t *testing.T) {
	one := []string{"test"}
	for _, recorded := range []string{"", "vet"} {
		got, ok := resolveOracle(recorded, one)
		if !ok || got != "test" {
			t.Errorf("resolveOracle(%q, [test]) = (%q, %v), want (test, true)", recorded, got, ok)
		}
	}

	man := oneOracleManifest()
	f := Finding{ID: "r1-f1", Workspace: "root", Oracle: "vet"}
	if noWorkspace, noOracle := BuildTargetGaps(f, man); noWorkspace || noOracle {
		t.Errorf("BuildTargetGaps with a stale oracle and one manifest oracle = (%v, %v), want (false, false)", noWorkspace, noOracle)
	}
	out := DefaultTriage(TriageInput{Asks: []Finding{f}, Manifest: man})
	if d, ok := out.Asks["r1-f1"]; !ok || !d.Keep {
		t.Errorf("Asks[r1-f1] = (%+v, %v), want kept: one manifest oracle leaves no choice", d, ok)
	}
}

// --- routeRound: grouping ------------------------------------------------

func TestRouteRoundGroupsFixesByWorkspaceAndOracle(t *testing.T) {
	st := newReviewStore(t)
	man := twoWorkspaceManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "alpha/a.go", Title: "t1", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "alpha", Status: StatusOpen},
		{ID: "r1-f2", File: "alpha/b.go", Title: "t2", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "alpha", Status: StatusOpen},
		{ID: "r1-f3", File: "beta/c.go", Title: "t3", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "beta", Status: StatusOpen},
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("slices = %v, want 2 (one per workspace)", sliceIDs(slices))
	}
	alpha := sliceByID(t, slices, "fix-1-alpha-test")
	if !equalStrings(alpha.Findings, []string{"r1-f1", "r1-f2"}) {
		t.Errorf("alpha slice Findings = %v, want [r1-f1 r1-f2]", alpha.Findings)
	}
	beta := sliceByID(t, slices, "fix-1-beta-test")
	if !equalStrings(beta.Findings, []string{"r1-f3"}) {
		t.Errorf("beta slice Findings = %v, want [r1-f3]", beta.Findings)
	}
	for _, id := range []string{"r1-f1", "r1-f2", "r1-f3"} {
		f := findFinding(routed, id)
		if f.Status != StatusOpen {
			t.Errorf("%s Status = %q, want open (kept)", id, f.Status)
		}
		if f.Triage != TriageAuto {
			t.Errorf("%s Triage = %q, want auto (nil hook)", id, f.Triage)
		}
	}
}

// TestRouteRoundGoalNamesEveryFindingAndDismissedFixIsExcluded checks that
// a dismissed fix becomes Status dismissed and never reaches a slice,
// while every kept fix's file:line/title/detail/risk rationale lands in
// the group's goal.
func TestRouteRoundGoalNamesEveryFindingAndDismissedFixIsExcluded(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Line: 3, Title: "keep me", Detail: "detail1", RiskRationale: "rationale1", Action: ActionFix, Workspace: "root", Status: StatusOpen},
		{ID: "r1-f2", File: "b.go", Line: 9, Title: "dismiss me", RiskRationale: "r2", Action: ActionFix, Workspace: "root", Status: StatusOpen},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{DismissedFixIDs: map[string]bool{"r1-f2": true}, FixHuman: true}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, triage, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 {
		t.Fatalf("slices = %v, want 1", sliceIDs(slices))
	}
	if !equalStrings(slices[0].Findings, []string{"r1-f1"}) {
		t.Errorf("Findings = %v, want [r1-f1] (r1-f2 was dismissed)", slices[0].Findings)
	}
	for _, want := range []string{"a.go:3", "keep me", "detail1", "rationale1"} {
		if !contains(slices[0].Goal, want) {
			t.Errorf("goal missing %q:\n%s", want, slices[0].Goal)
		}
	}
	if contains(slices[0].Goal, "dismiss me") {
		t.Errorf("goal must not mention the dismissed finding:\n%s", slices[0].Goal)
	}
	keep := findFinding(routed, "r1-f1")
	if keep.Status != StatusOpen || keep.Triage != TriageHuman {
		t.Errorf("r1-f1 = %+v, want open/human", keep)
	}
	dismiss := findFinding(routed, "r1-f2")
	if dismiss.Status != StatusDismissed || dismiss.Triage != TriageHuman {
		t.Errorf("r1-f2 = %+v, want dismissed/human", dismiss)
	}
}

// --- routeRound: kept asks -------------------------------------------------

func TestRouteRoundKeptAskBecomesItsOwnSliceWithDecision(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r2-f3", File: "a.go", Title: "needs a call", RiskRationale: "r", Action: ActionAsk, Workspace: "root", Status: StatusAsked},
	}
	triage := func(in TriageInput) TriageResult {
		if len(in.Asks) != 1 || in.Asks[0].ID != "r2-f3" {
			t.Fatalf("TriageInput.Asks = %+v, want [r2-f3]", in.Asks)
		}
		return TriageResult{Asks: map[string]AskOutcome{
			"r2-f3": {Keep: true, Decision: "go ahead with plan B", Human: true},
		}}
	}
	routed, slices, err := routeRound(2, st, "T-1", nil, reported, triage, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 || slices[0].ID != "fix-2-r2-f3" {
		t.Fatalf("slices = %v, want [fix-2-r2-f3]", sliceIDs(slices))
	}
	if !contains(slices[0].Goal, "go ahead with plan B") {
		t.Errorf("goal missing the human's decision:\n%s", slices[0].Goal)
	}
	f := findFinding(routed, "r2-f3")
	// asked --> open: you keep it.
	if f.Status != StatusOpen {
		t.Errorf("kept ask Status = %q, want open", f.Status)
	}
	if f.Decision != "go ahead with plan B" || f.Triage != TriageHuman {
		t.Errorf("kept ask = %+v, want the decision and triage recorded", f)
	}
}

// TestRouteRoundAutoKeptAskGoalDoesNotClaimAHumanDecided: DefaultTriage
// keeps a workspace ask with no human involved (Triage: auto), and the fix
// slice goal it builds must say so rather than "kept by the human".
func TestRouteRoundAutoKeptAskGoalDoesNotClaimAHumanDecided(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "needs a call", RiskRationale: "r", Action: ActionAsk, Workspace: "root", Status: StatusAsked},
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, man) // nil = DefaultTriage
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 {
		t.Fatalf("slices = %v, want 1", sliceIDs(slices))
	}
	f := findFinding(routed, "r1-f1")
	if f.Triage != TriageAuto {
		t.Fatalf("Triage = %q, want auto", f.Triage)
	}
	if contains(slices[0].Goal, "kept by the human") {
		t.Errorf("goal claims a human decided an auto-kept ask:\n%s", slices[0].Goal)
	}
	if !contains(slices[0].Goal, "no human decision") {
		t.Errorf("goal does not say a human did not decide:\n%s", slices[0].Goal)
	}
}

func TestRouteRoundDismissedAskNeverBuildsASlice(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "ask", RiskRationale: "r", Action: ActionAsk, Workspace: "root", Status: StatusAsked},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Asks: map[string]AskOutcome{"r1-f1": {Keep: false, Human: true}}}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, triage, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 0 {
		t.Fatalf("slices = %v, want none", sliceIDs(slices))
	}
	f := findFinding(routed, "r1-f1")
	if f.Status != StatusDismissed || f.Triage != TriageHuman {
		t.Errorf("dismissed ask = %+v, want dismissed/human", f)
	}
}

// TestRouteRoundUndecidedNoWorkspaceAskStaysAskedWithNoTriage is the core
// case: a hook that declines to decide a no-workspace ask leaves it asked,
// with no Triage value and no slice.
func TestRouteRoundUndecidedNoWorkspaceAskStaysAskedWithNoTriage(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "orphan.go", Title: "no workspace", RiskRationale: "r", Action: ActionFix, Workspace: "", Status: StatusAsked, RoutedAs: ActionAsk},
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, man) // nil = DefaultTriage
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 0 {
		t.Fatalf("slices = %v, want none", sliceIDs(slices))
	}
	f := findFinding(routed, "r1-f1")
	if f.Status != StatusAsked {
		t.Errorf("Status = %q, want asked (still undecided)", f.Status)
	}
	if f.Triage != "" {
		t.Errorf("Triage = %q, want empty (absent for an undecided ask)", f.Triage)
	}
}

// TestRouteRoundWorkspaceSuppliedByTriageBuildsTheSlice checks the other
// half: when the hook keeps a no-workspace ask and supplies a workspace,
// routing builds its slice in that workspace.
func TestRouteRoundWorkspaceSuppliedByTriageBuildsTheSlice(t *testing.T) {
	st := newReviewStore(t)
	man := twoWorkspaceManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "orphan.go", Title: "no workspace", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "", Status: StatusAsked, RoutedAs: ActionAsk},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Asks: map[string]AskOutcome{
			"r1-f1": {Keep: true, Workspace: "beta", Human: true},
		}}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, triage, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 || slices[0].Workspace != "beta" {
		t.Fatalf("slices = %+v, want one slice in workspace beta", slices)
	}
	f := findFinding(routed, "r1-f1")
	if f.Status != StatusOpen || f.Workspace != "beta" {
		t.Errorf("f = %+v, want open in workspace beta", f)
	}
}

// TestRouteRoundUndecidedInvalidOracleAskStaysAskedWithNoTriage is the
// oracle-side twin of the no-workspace case: a hook that declines to
// resolve a missing oracle leaves the ask asked, with no slice built.
func TestRouteRoundUndecidedInvalidOracleAskStaysAskedWithNoTriage(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	man.Oracles["lint"] = "true" // two oracles now, so "" cannot default
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "no oracle", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusAsked, RoutedAs: ActionAsk},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Asks: map[string]AskOutcome{"r1-f1": {Keep: true, Human: true}}} // no Oracle supplied
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, triage, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 0 {
		t.Fatalf("slices = %v, want none", sliceIDs(slices))
	}
	f := findFinding(routed, "r1-f1")
	if f.Status != StatusAsked || f.Triage != "" {
		t.Errorf("f = %+v, want asked with no triage (the hook never resolved an oracle)", f)
	}
}

// TestRouteRoundOracleSuppliedByTriageBuildsTheSlice is the oracle-side
// half: when the hook keeps an ask whose recorded oracle is no longer valid
// and supplies one, routing builds its slice with that oracle.
func TestRouteRoundOracleSuppliedByTriageBuildsTheSlice(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	man.Oracles["lint"] = "true" // two oracles now: "old" is neither
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "stale oracle", RiskRationale: "r", Action: ActionFix, Oracle: "old", Workspace: "root", Status: StatusAsked, RoutedAs: ActionAsk},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Asks: map[string]AskOutcome{
			"r1-f1": {Keep: true, Oracle: "lint", Human: true},
		}}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, triage, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 || slices[0].Oracle != "lint" {
		t.Fatalf("slices = %+v, want one slice with oracle lint", slices)
	}
	f := findFinding(routed, "r1-f1")
	if f.Status != StatusOpen || f.Oracle != "lint" {
		t.Errorf("f = %+v, want open with oracle lint", f)
	}
}

// --- notes are never triaged -----------------------------------------------

func TestRouteRoundNotesPassThroughUntouched(t *testing.T) {
	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "fyi", RiskRationale: "r", Action: ActionNote, Status: StatusNoted},
	}
	called := false
	triage := func(in TriageInput) TriageResult {
		called = true
		if len(in.Notes) != 1 || in.Notes[0].ID != "r1-f1" {
			t.Errorf("TriageInput.Notes = %+v, want [r1-f1]", in.Notes)
		}
		if len(in.Fixes) != 0 || len(in.Asks) != 0 {
			t.Errorf("TriageInput fixes/asks = %+v/%+v, want none", in.Fixes, in.Asks)
		}
		return TriageResult{}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, triage, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if !called {
		t.Fatal("triage hook was never called")
	}
	if len(slices) != 0 {
		t.Fatalf("slices = %v, want none", sliceIDs(slices))
	}
	f := findFinding(routed, "r1-f1")
	if f.Status != StatusNoted || f.Triage != "" {
		t.Errorf("note = %+v, want noted with no triage", f)
	}
}

// --- GATE_NO_ORACLE ----------------------------------------------------

func TestRouteRoundNoOracleManifestFailsGateNoOracle(t *testing.T) {
	st := newReviewStore(t)
	man := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "root", Path: "."}}} // zero oracles
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "t", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusOpen},
	}
	_, _, err := routeRound(1, st, "T-1", nil, reported, nil, man)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_NO_ORACLE" {
		t.Fatalf("err = %v, want *axi.Error GATE_NO_ORACLE", err)
	}
}

// TestOracleForFindingMultiOracleManifestNamesTheOracles is the multi-
// oracle twin of the zero-oracle GATE_NO_ORACLE message: it must not claim
// the manifest has no oracles when it names several, and must name them.
func TestOracleForFindingMultiOracleManifestNamesTheOracles(t *testing.T) {
	_, err := oracleForFinding(Finding{ID: "r1-f1"}, []string{"lint", "test"})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_NO_ORACLE" {
		t.Fatalf("err = %v, want *axi.Error GATE_NO_ORACLE", err)
	}
	if contains(ae.Msg, "has no oracles") {
		t.Errorf("message = %q, want it not to claim the manifest has no oracles", ae.Msg)
	}
	for _, name := range []string{"lint", "test"} {
		if !contains(ae.Msg, name) {
			t.Errorf("message = %q, want it to name oracle %q", ae.Msg, name)
		}
	}
}

// TestRouteRoundNoOracleManifestFailsBeforeTriage checks that a zero-oracle
// manifest fails GATE_NO_ORACLE before the triage hook ever runs, so a
// human at a terminal is never asked to decide a fix or ask that can never
// build a slice - and never has their answer discarded when routing then
// fails anyway.
func TestRouteRoundNoOracleManifestFailsBeforeTriage(t *testing.T) {
	st := newReviewStore(t)
	man := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "root", Path: "."}}} // zero oracles
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "t", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusOpen},
		{ID: "r1-f2", File: "b.go", Title: "t2", RiskRationale: "r", Action: ActionAsk, Workspace: "root", Status: StatusAsked},
	}
	called := false
	triage := func(TriageInput) TriageResult {
		called = true
		return TriageResult{}
	}
	_, _, err := routeRound(1, st, "T-1", nil, reported, triage, man)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_NO_ORACLE" {
		t.Fatalf("err = %v, want *axi.Error GATE_NO_ORACLE", err)
	}
	if called {
		t.Error("the triage hook ran despite a zero-oracle manifest that can never route anything")
	}
}

// TestRouteRoundNoOracleManifestWithOnlyNotesDoesNotFail is the other half:
// a zero-oracle manifest with nothing to route (only notes) has nothing
// GATE_NO_ORACLE needs to protect, so it must not fail the round at all.
func TestRouteRoundNoOracleManifestWithOnlyNotesDoesNotFail(t *testing.T) {
	st := newReviewStore(t)
	man := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "root", Path: "."}}} // zero oracles
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "t", RiskRationale: "r", Action: ActionNote, Status: StatusNoted},
	}
	_, slices, err := routeRound(1, st, "T-1", nil, reported, nil, man)
	if err != nil {
		t.Fatalf("routeRound: %v, want no error (nothing to route)", err)
	}
	if len(slices) != 0 {
		t.Fatalf("slices = %v, want none", sliceIDs(slices))
	}
}

// --- recurrence names the previous fix slice --------------------------

func TestRouteRoundRecurrenceGoalNamesPreviousFixSlice(t *testing.T) {
	st := newReviewStore(t)
	ticket := "T-1"
	man := oneOracleManifest()
	existing := []store.Slice{
		{ID: "fix-1-root-test", Workspace: "root", Oracle: "test", Findings: []string{"r1-f1"}},
	}
	if err := st.WriteSliceState(ticket, "fix-1-root-test", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state: %v", err)
	}
	resultPath := gateWorkDir(st, ticket)
	if err := writeAttemptResult(t, resultPath, "fix-1-root-test", 1, "fixed the null check"); err != nil {
		t.Fatalf("write attempt result: %v", err)
	}

	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "still broken", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusOpen, Recurrences: 1},
	}
	_, slices, err := routeRound(2, st, ticket, existing, reported, nil, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 {
		t.Fatalf("slices = %v, want 1", sliceIDs(slices))
	}
	for _, want := range []string{"fix-1-root-test", "fixed the null check"} {
		if !contains(slices[0].Goal, want) {
			t.Errorf("goal missing %q:\n%s", want, slices[0].Goal)
		}
	}
}

// TestRouteRoundRecurrenceGoalNamesOnlyIDWhenResultMissing covers the
// "only its id is named when that file is absent".
func TestRouteRoundRecurrenceGoalNamesOnlyIDWhenResultMissing(t *testing.T) {
	st := newReviewStore(t)
	ticket := "T-1"
	man := oneOracleManifest()
	existing := []store.Slice{
		{ID: "fix-1-root-test", Workspace: "root", Oracle: "test", Findings: []string{"r1-f1"}},
	}
	// No slice state / result written: previousFixSliceSummary must return "".
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "still broken", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusOpen, Recurrences: 1},
	}
	_, slices, err := routeRound(2, st, ticket, existing, reported, nil, man)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if !contains(slices[0].Goal, "fix-1-root-test") {
		t.Errorf("goal missing the previous slice id:\n%s", slices[0].Goal)
	}
}

// --- disambiguation ----------------------------------------------------

func TestDisambiguateFixSliceIDsBreaksTies(t *testing.T) {
	slices := []store.Slice{{ID: "fix-1-a"}, {ID: "fix-1-a"}, {ID: "fix-1-a"}}
	disambiguateFixSliceIDs(slices)
	got := sliceIDs(slices)
	sort.Strings(got)
	want := []string{"fix-1-a", "fix-1-a-2", "fix-1-a-3"}
	if !equalStrings(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestSanitizeSliceIDReplacesUnsafeCharacters(t *testing.T) {
	if got := sanitizeSliceID("fix-1-svc/a-test:x"); got != "fix-1-svc-a-test-x" {
		t.Fatalf("sanitizeSliceID = %q, want fix-1-svc-a-test-x", got)
	}
}

// --- helpers ---------------------------------------------------------------

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }
