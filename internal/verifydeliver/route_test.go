package verifydeliver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
	t.Parallel()

	in := TriageInput{
		Fixes: []Finding{{ID: "r1-f1"}},
		Asks: []Finding{
			{ID: "r1-f2", Workspace: "alpha", Oracle: "test"},
			{ID: "r1-f3", Workspace: ""},
		},
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

// TestDefaultTriageLeavesAnAskWithNoCommandUndecided is the command-side
// half: a workspace ask whose reviewer named no command stays undecided even
// though it has a workspace and the manifest has one oracle, while one that
// names a command (a name the manifest lacks is just a command) is kept.
func TestDefaultTriageLeavesAnAskWithNoCommandUndecided(t *testing.T) {
	t.Parallel()

	in := TriageInput{
		Asks: []Finding{
			{ID: "r1-f1", Workspace: "root"},
			{ID: "r1-f2", Workspace: "root", Oracle: "vet"},
		},
	}
	out := DefaultTriage(in)
	if _, ok := out.Asks["r1-f1"]; ok {
		t.Error("Asks[r1-f1] decided, want undecided (it names no command)")
	}
	if d, ok := out.Asks["r1-f2"]; !ok || !d.Keep {
		t.Errorf("Asks[r1-f2] = %+v, ok=%v, want kept (it names a command)", d, ok)
	}
}

// TestDefaultTriageLeavesABudgetParkedAskUndecidedEvenWithAFullBuildTarget
// pins the fix budget's own triage rule: a finding the budget forced to
// asked (RoutedWhy RoutedWhyBudget) stays undecided under DefaultTriage
// even though its workspace and oracle both already resolve - the one
// case an ordinary ask with a full build target would be auto-kept.
// --yes, a non-terminal stdin and `jig solve` all build on DefaultTriage,
// so this is the one place that has to hold for every one of them.
func TestDefaultTriageLeavesABudgetParkedAskUndecidedEvenWithAFullBuildTarget(t *testing.T) {
	t.Parallel()

	in := TriageInput{
		Asks: []Finding{
			{ID: "r1-f1", Workspace: "alpha", Oracle: "test", RoutedWhy: RoutedWhyBudget},
			{ID: "r1-f2", Workspace: "alpha", Oracle: "test"},
		},
	}
	out := DefaultTriage(in)
	if _, ok := out.Asks["r1-f1"]; ok {
		t.Error("Asks[r1-f1] decided, want undecided (budget-parked, only a terminal human can keep it)")
	}
	dec, ok := out.Asks["r1-f2"]
	if !ok || !dec.Keep {
		t.Errorf("Asks[r1-f2] = %+v, ok=%v, want kept (an ordinary ask with a full build target)", dec, ok)
	}
}

// TestAnOmittedOracleStaysEmpty pins that jig never picks a command for a
// reviewer: a finding that names none stays without one, even with exactly
// one manifest oracle, so its build target lacks the command and a person
// supplies it.
func TestAnOmittedOracleStaysEmpty(t *testing.T) {
	t.Parallel()

	f := Finding{ID: "r1-f1", Workspace: "root"}
	if noWorkspace, noOracle := BuildTargetGaps(f); noWorkspace || !noOracle {
		t.Errorf("BuildTargetGaps with no oracle = (%v, %v), want (false, true)", noWorkspace, noOracle)
	}
	if noWorkspace, noOracle := BuildTargetGaps(Finding{ID: "r1-f2", Workspace: "root", Oracle: "go test ./x -run 'TestA'"}); noWorkspace || noOracle {
		t.Errorf("BuildTargetGaps with a command = (%v, %v), want (false, false)", noWorkspace, noOracle)
	}
	out := DefaultTriage(TriageInput{Asks: []Finding{f}})
	if _, ok := out.Asks["r1-f1"]; ok {
		t.Error("Asks[r1-f1] decided, want undecided: the manifest's only oracle is not a default")
	}

	man := oneOracleManifest()
	result := ReviewResult{Findings: []ResultFinding{
		{File: "a.go", Line: 1, Title: "t", RiskRationale: "r", Action: ActionAsk, Risk: RiskMedium},
	}}
	reported, err := ApplyRound(1, map[string]Finding{}, result, nil, nil, man, []string{RiskLow, RiskMedium, RiskHigh}, false)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 || reported[0].Oracle != "" {
		t.Errorf("reported = %+v, want one finding whose oracle stays empty", reported)
	}
}

// --- routeRound: grouping ------------------------------------------------

func TestRouteRoundGroupsFixesByWorkspaceAndOracle(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := twoWorkspaceManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "alpha/a.go", Title: "t1", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "alpha", Status: StatusOpen},
		{ID: "r1-f2", File: "alpha/b.go", Title: "t2", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "alpha", Status: StatusOpen},
		{ID: "r1-f3", File: "beta/c.go", Title: "t3", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "beta", Status: StatusOpen},
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, nil, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("slices = %v, want 2 (one per workspace)", sliceIDs(slices))
	}
	alpha := sliceByID(t, slices, "fix-1-alpha")
	if !equalStrings(alpha.Findings, []string{"r1-f1", "r1-f2"}) {
		t.Errorf("alpha slice Findings = %v, want [r1-f1 r1-f2]", alpha.Findings)
	}
	beta := sliceByID(t, slices, "fix-1-beta")
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

// TestRouteRoundSplitsAFixGroupPackingFilesGreedilyByPathOrder pins "Fix
// slices sized to one session": a workspace group over the
// fix_slice_findings bound splits into several slices, same-file findings
// always sharing one, files packed greedily in path order (not finding id
// order), a file over the bound getting a slice of its own, and the
// split slices numbered "-1", "-2", "-3" from 1.
func TestRouteRoundSplitsAFixGroupPackingFilesGreedilyByPathOrder(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	// IDs are deliberately out of path order (r1-f2/r1-f4/r1-f6 name
	// alpha/c.go, which sorts last by path, yet start before r1-f5's
	// alpha/b.go by id) so the test fails if packing followed id order
	// instead of path order.
	reported := []Finding{
		{ID: "r1-f1", File: "alpha/a.go", Title: "a1", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
		{ID: "r1-f2", File: "alpha/c.go", Title: "c1", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
		{ID: "r1-f3", File: "alpha/a.go", Title: "a2", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
		{ID: "r1-f4", File: "alpha/c.go", Title: "c2", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
		{ID: "r1-f5", File: "alpha/b.go", Title: "b1", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
		{ID: "r1-f6", File: "alpha/c.go", Title: "c3", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, nil, oneRepoFiles(man), 0, 0, 2)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 3 {
		t.Fatalf("slices = %v, want 3", sliceIDs(slices))
	}
	a := sliceByID(t, slices, "fix-1-root-1")
	if !equalStrings(a.Findings, []string{"r1-f1", "r1-f3"}) {
		t.Errorf("slice 1 (alpha/a.go, fits the bound) Findings = %v, want [r1-f1 r1-f3]", a.Findings)
	}
	b := sliceByID(t, slices, "fix-1-root-2")
	if !equalStrings(b.Findings, []string{"r1-f5"}) {
		t.Errorf("slice 2 (alpha/b.go, crowded out of slice 1) Findings = %v, want [r1-f5]", b.Findings)
	}
	c := sliceByID(t, slices, "fix-1-root-3")
	if !equalStrings(c.Findings, []string{"r1-f2", "r1-f4", "r1-f6"}) {
		t.Errorf("slice 3 (alpha/c.go, over the bound alone) Findings = %v, want [r1-f2 r1-f4 r1-f6]", c.Findings)
	}
	for _, id := range []string{"r1-f1", "r1-f2", "r1-f3", "r1-f4", "r1-f5", "r1-f6"} {
		if f := findFinding(routed, id); f.Status != StatusOpen {
			t.Errorf("%s Status = %q, want open (kept)", id, f.Status)
		}
	}
}

// TestRouteRoundGoalNamesEveryFindingAndDismissedFixIsExcluded checks that
// a dismissed fix becomes Status dismissed and never reaches a slice,
// while every kept fix's file:line/title/detail/risk rationale lands in
// the group's goal.
func TestRouteRoundGoalNamesEveryFindingAndDismissedFixIsExcluded(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Line: 3, Title: "keep me", Detail: "detail1", RiskRationale: "rationale1", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
		{ID: "r1-f2", File: "b.go", Line: 9, Title: "dismiss me", RiskRationale: "r2", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{DismissedFixIDs: map[string]bool{"r1-f2": true}, FixHuman: true}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
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
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r2-f3", File: "a.go", Title: "needs a call", RiskRationale: "r", Action: ActionAsk, Oracle: "test", Workspace: "root", Status: StatusAsked},
	}
	triage := func(in TriageInput) TriageResult {
		if len(in.Asks) != 1 || in.Asks[0].ID != "r2-f3" {
			t.Fatalf("TriageInput.Asks = %+v, want [r2-f3]", in.Asks)
		}
		return TriageResult{Asks: map[string]AskOutcome{
			"r2-f3": {Keep: true, Decision: "go ahead with plan B", Human: true},
		}}
	}
	routed, slices, err := routeRound(2, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 || slices[0].ID != "fix-2-root" {
		t.Fatalf("slices = %v, want [fix-2-root]", sliceIDs(slices))
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

// TestRouteRoundGroupsKeptAsksAndNotesWithFixesByWorkspaceAndOracle: a
// round's kept fix, ask and notes of one workspace and oracle are built as
// one slice, each finding keeping its own block and its kept-by header.
func TestRouteRoundGroupsKeptAsksAndNotesWithFixesByWorkspaceAndOracle(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := twoWorkspaceManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "alpha/a.go", Title: "a fix", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "alpha", Status: StatusOpen},
		{ID: "r1-f2", File: "alpha/b.go", Title: "an ask", RiskRationale: "r", Action: ActionAsk, Oracle: "test", Workspace: "alpha", Status: StatusAsked},
		{ID: "r1-f3", File: "alpha/c.go", Title: "first note", RiskRationale: "r", Action: ActionNote, Oracle: "test", Workspace: "alpha", Status: StatusNoted},
		{ID: "r1-f4", File: "alpha/d.go", Title: "second note", RiskRationale: "r", Action: ActionNote, Oracle: "test", Workspace: "alpha", Status: StatusNoted},
		{ID: "r1-f5", File: "beta/e.go", Title: "other workspace note", RiskRationale: "r", Action: ActionNote, Oracle: "test", Workspace: "beta", Status: StatusNoted},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{
			Asks: map[string]AskOutcome{"r1-f2": {Keep: true, Decision: "decision for the ask", Human: true}},
			Notes: map[string]AskOutcome{
				"r1-f3": {Keep: true, Decision: "decision for note three", Human: true},
				"r1-f4": {Keep: true, Decision: "decision for note four"},
				"r1-f5": {Keep: true, Decision: "decision for note five", Human: true},
			},
		}
	}
	_, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("slices = %v, want 2 (one per workspace)", sliceIDs(slices))
	}
	alpha := sliceByID(t, slices, "fix-1-alpha")
	if !equalStrings(alpha.Findings, []string{"r1-f1", "r1-f2", "r1-f3", "r1-f4"}) {
		t.Errorf("alpha Findings = %v, want [r1-f1 r1-f2 r1-f3 r1-f4]", alpha.Findings)
	}
	for _, want := range []string{
		"a fix", "an ask", "first note", "second note",
		"Gate finding r1-f2, kept by the human:", "decision for the ask",
		"Gate finding r1-f3, kept by the human:", "decision for note three",
		"Gate finding r1-f4, kept with no human decision (--yes or no terminal):", "decision for note four",
	} {
		if !contains(alpha.Goal, want) {
			t.Errorf("alpha goal missing %q:\n%s", want, alpha.Goal)
		}
	}
	if contains(alpha.Goal, "other workspace note") {
		t.Errorf("alpha goal carries the beta note:\n%s", alpha.Goal)
	}
	if contains(alpha.Goal, "Gate finding r1-f1") {
		t.Errorf("a kept fix carries no kept-by header:\n%s", alpha.Goal)
	}
	beta := sliceByID(t, slices, "fix-1-beta")
	if !equalStrings(beta.Findings, []string{"r1-f5"}) {
		t.Errorf("beta Findings = %v, want [r1-f5]", beta.Findings)
	}
}

// TestRouteRoundSplitsKeptAsksAndNotesUnderTheCap: kept notes pack under
// fixSliceFindings like kept fixes, one file always in one slice.
func TestRouteRoundSplitsKeptAsksAndNotesUnderTheCap(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "n1", RiskRationale: "r", Action: ActionNote, Oracle: "test", Workspace: "root", Status: StatusNoted},
		{ID: "r1-f2", File: "b.go", Title: "n2", RiskRationale: "r", Action: ActionNote, Oracle: "test", Workspace: "root", Status: StatusNoted},
		{ID: "r1-f3", File: "a.go", Title: "n3", RiskRationale: "r", Action: ActionNote, Oracle: "test", Workspace: "root", Status: StatusNoted},
	}
	triage := func(in TriageInput) TriageResult {
		notes := map[string]AskOutcome{}
		for _, n := range in.Notes {
			notes[n.ID] = AskOutcome{Keep: true, Decision: "keep " + n.ID, Human: true}
		}
		return TriageResult{Notes: notes}
	}
	_, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 2)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("slices = %v, want 2", sliceIDs(slices))
	}
	if first := sliceByID(t, slices, "fix-1-root-1"); !equalStrings(first.Findings, []string{"r1-f1", "r1-f3"}) {
		t.Errorf("slice 1 Findings = %v, want [r1-f1 r1-f3] (same file together)", first.Findings)
	}
	if second := sliceByID(t, slices, "fix-1-root-2"); !equalStrings(second.Findings, []string{"r1-f2"}) {
		t.Errorf("slice 2 Findings = %v, want [r1-f2]", second.Findings)
	}
}

// TestRouteRoundGroupedKeptRecurrenceNamesItsPreviousSlice: a kept note that
// recurs finds its earlier slice by finding id, though that slice carried
// two findings and its id names neither.
func TestRouteRoundGroupedKeptRecurrenceNamesItsPreviousSlice(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	ticket := "T-1"
	man := oneOracleManifest()
	existing := []store.Slice{
		{ID: "fix-1-root", Workspace: "root", Oracle: "test", Findings: []string{"r1-f1", "r1-f2"}},
	}
	if err := st.WriteSliceState(ticket, "fix-1-root", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state: %v", err)
	}
	if err := writeAttemptResult(t, gateWorkDir(st, ticket), "fix-1-root", 1, "tightened both"); err != nil {
		t.Fatalf("write attempt result: %v", err)
	}
	reported := []Finding{
		{ID: "r1-f2", File: "a.go", Title: "back again", RiskRationale: "r", Action: ActionNote, Oracle: "test", Workspace: "root", Status: StatusNoted, Recurrences: 1},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Notes: map[string]AskOutcome{"r1-f2": {Keep: true, Decision: "again", Human: true}}}
	}
	_, slices, err := routeRound(2, st, ticket, existing, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 {
		t.Fatalf("slices = %v, want 1", sliceIDs(slices))
	}
	for _, want := range []string{"Previous fix slice fix-1-root: tightened both", "Gate finding r1-f2, kept by the human:"} {
		if !contains(slices[0].Goal, want) {
			t.Errorf("goal missing %q:\n%s", want, slices[0].Goal)
		}
	}
}

// TestRouteRoundAutoKeptAskGoalDoesNotClaimAHumanDecided: DefaultTriage
// keeps a workspace ask with no human involved (Triage: auto), and the fix
// slice goal it builds must say so rather than "kept by the human".
func TestRouteRoundAutoKeptAskGoalDoesNotClaimAHumanDecided(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "needs a call", RiskRationale: "r", Action: ActionAsk, Oracle: "test", Workspace: "root", Status: StatusAsked},
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, nil, oneRepoFiles(man), 0, 0, 5) // nil = DefaultTriage
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
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "ask", RiskRationale: "r", Action: ActionAsk, Oracle: "test", Workspace: "root", Status: StatusAsked},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Asks: map[string]AskOutcome{"r1-f1": {Keep: false, Human: true}}}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
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
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "orphan.go", Title: "no workspace", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "", Status: StatusAsked, RoutedAs: ActionAsk},
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, nil, oneRepoFiles(man), 0, 0, 5) // nil = DefaultTriage
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
	t.Parallel()

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
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
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

// TestAKeptAskWithNoCommandIsNotBuilt is the command-side twin of the
// no-workspace case: a keep that supplies no command for an ask that has none
// leaves the ask asked, with no slice built.
func TestAKeptAskWithNoCommandIsNotBuilt(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "no oracle", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusAsked, RoutedAs: ActionAsk},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Asks: map[string]AskOutcome{"r1-f1": {Keep: true, Human: true}}} // no Oracle supplied
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
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

// TestRouteRoundOracleSuppliedByTriageBuildsTheSlice is the command-side
// half: when the hook keeps an ask that names no command and supplies one
// (with spaces, as a command has), routing builds its slice with it.
func TestRouteRoundOracleSuppliedByTriageBuildsTheSlice(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	const command = "go test ./a -run 'TestA|TestB'"
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "no command", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusAsked, RoutedAs: ActionAsk},
	}
	triage := func(in TriageInput) TriageResult {
		return TriageResult{Asks: map[string]AskOutcome{
			"r1-f1": {Keep: true, Oracle: command, Human: true},
		}}
	}
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 || slices[0].Oracle != command {
		t.Fatalf("slices = %+v, want one slice with the person's command", slices)
	}
	f := findFinding(routed, "r1-f1")
	if f.Status != StatusOpen || f.Oracle != command {
		t.Errorf("f = %+v, want open with the person's command", f)
	}
}

// --- notes are never triaged -----------------------------------------------

func TestRouteRoundNotesPassThroughUntouched(t *testing.T) {
	t.Parallel()

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
	routed, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
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

// --- a manifest with no oracle ---------------------------------------------

// TestAManifestWithNoOracleStillBuildsFixSlices pins that a reviewer's command
// is all a fix slice needs: a project whose manifest declares no oracle still
// gets its fix slice, and the triage hook still runs.
func TestAManifestWithNoOracleStillBuildsFixSlices(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "root", Path: "."}}} // zero oracles
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "t", RiskRationale: "r", Action: ActionFix, Oracle: "go test ./a -run 'TestA'", Workspace: "root", Status: StatusOpen},
	}
	called := false
	triage := func(in TriageInput) TriageResult {
		called = true
		return DefaultTriage(in)
	}
	_, slices, err := routeRound(1, st, "T-1", nil, reported, nil, triage, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if !called {
		t.Error("the triage hook never ran")
	}
	if len(slices) != 1 || slices[0].ID != "fix-1-root" || slices[0].Oracle != "go test ./a -run 'TestA'" {
		t.Fatalf("slices = %+v, want fix-1-root with the finding's command", slices)
	}
}

// TestAFixFindingWithNoOracleIsRoutedAsAnAsk pins the recurrence whose prior
// never named a command: it comes to a person as an ask with the build-target
// reason, as a fix with no workspace does.
func TestAFixFindingWithNoOracleIsRoutedAsAnAsk(t *testing.T) {
	t.Parallel()

	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Workspace: "root", Action: ActionNote, Risk: RiskMedium, Status: StatusNoted},
	}
	result := ReviewResult{Findings: []ResultFinding{
		{File: "a.go", Line: 2, Title: "t", RiskRationale: "r", Action: ActionFix, Risk: RiskMedium, Prior: "r1-f1"},
	}}
	reported, err := ApplyRound(2, known, result, nil, nil, man, []string{RiskLow, RiskMedium, RiskHigh}, false)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %+v, want one finding", reported)
	}
	f := reported[0]
	if f.Status != StatusAsked || f.RoutedAs != ActionAsk || f.RoutedWhy != RoutedWhyBuildTarget {
		t.Errorf("finding = %+v, want asked, routed as ask for the build target", f)
	}
}

// --- recurrence names the previous fix slice --------------------------

func TestRouteRoundRecurrenceGoalNamesPreviousFixSlice(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	ticket := "T-1"
	man := oneOracleManifest()
	existing := []store.Slice{
		{ID: "fix-1-root", Workspace: "root", Oracle: "test", Findings: []string{"r1-f1"}},
	}
	if err := st.WriteSliceState(ticket, "fix-1-root", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state: %v", err)
	}
	resultPath := gateWorkDir(st, ticket)
	if err := writeAttemptResult(t, resultPath, "fix-1-root", 1, "fixed the null check"); err != nil {
		t.Fatalf("write attempt result: %v", err)
	}

	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "still broken", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen, Recurrences: 1},
	}
	_, slices, err := routeRound(2, st, ticket, existing, reported, nil, nil, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 {
		t.Fatalf("slices = %v, want 1", sliceIDs(slices))
	}
	for _, want := range []string{"fix-1-root", "fixed the null check"} {
		if !contains(slices[0].Goal, want) {
			t.Errorf("goal missing %q:\n%s", want, slices[0].Goal)
		}
	}
}

// TestRouteRoundRecurrenceGoalNamesOnlyIDWhenResultMissing covers the
// "only its id is named when that file is absent".
func TestRouteRoundRecurrenceGoalNamesOnlyIDWhenResultMissing(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	ticket := "T-1"
	man := oneOracleManifest()
	existing := []store.Slice{
		{ID: "fix-1-root", Workspace: "root", Oracle: "test", Findings: []string{"r1-f1"}},
	}
	// No slice state / result written: previousFixSliceSummary must return "".
	reported := []Finding{
		{ID: "r1-f1", File: "a.go", Title: "still broken", RiskRationale: "r", Action: ActionFix, Oracle: "test", Workspace: "root", Status: StatusOpen, Recurrences: 1},
	}
	_, slices, err := routeRound(2, st, ticket, existing, reported, nil, nil, oneRepoFiles(man), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if !contains(slices[0].Goal, "fix-1-root") {
		t.Errorf("goal missing the previous slice id:\n%s", slices[0].Goal)
	}
}

// --- disambiguation ----------------------------------------------------

// TestAQueuedKeepsFixSliceCarriesItsOwnMarker: a keep queued before round n
// and the round's own finding of the same workspace and oracle are two slices
// with two ids, neither of them a "-2" of the other.
func TestAQueuedKeepsFixSliceCarriesItsOwnMarker(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := oneOracleManifest()
	files := oneRepoFiles(man)
	keep := Finding{ID: "r1-f1", File: "a.go", Title: "kept", RiskRationale: "r", Action: ActionAsk, Workspace: "root", Oracle: "test", Status: StatusOpen}
	own := Finding{ID: "r2-f1", File: "b.go", Title: "own", RiskRationale: "r", Action: ActionFix, Workspace: "root", Status: StatusOpen}

	queued, err := buildFixSlices(2, st, "T-1", nil, nil, []Finding{keep}, files, 5, true)
	if err != nil {
		t.Fatalf("buildFixSlices (queued): %v", err)
	}
	round, err := buildFixSlices(2, st, "T-1", queued, []Finding{own}, nil, files, 5, false)
	if err != nil {
		t.Fatalf("buildFixSlices (round): %v", err)
	}
	if len(queued) != 1 || len(round) != 1 || queued[0].ID == round[0].ID {
		t.Fatalf("ids = %v and %v, want two different ids", sliceIDs(queued), sliceIDs(round))
	}
	for _, id := range append(sliceIDs(queued), sliceIDs(round)...) {
		if strings.HasSuffix(id, "-2") {
			t.Errorf("id %q ends in -2, want each slice to keep its own id", id)
		}
	}
}

func TestDisambiguateFixSliceIDsBreaksTies(t *testing.T) {
	t.Parallel()

	slices := []store.Slice{{ID: "fix-1-a"}, {ID: "fix-1-a"}, {ID: "fix-1-a"}}
	disambiguateFixSliceIDs(slices, nil)
	got := sliceIDs(slices)
	sort.Strings(got)
	want := []string{"fix-1-a", "fix-1-a-2", "fix-1-a-3"}
	if !equalStrings(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

// TestDisambiguateFixSliceIDsAvoidsAnExistingSliceID pins the fix for the
// review finding that disambiguation seeded its used-id set from the
// batch only, never from the ticket's existing slices.yaml: a batch id
// that collides with an id already on the ticket (hand-written, or
// carried in from a chart) used to go through unchanged and would fail
// AppendSlices with SLICE_ID_DUPLICATE mid-batch, leaving the round
// half-applied - some slices already appended, the rest rejected, after
// this round's own findings.yaml (with the human's triage decisions) was
// already persisted.
func TestDisambiguateFixSliceIDsAvoidsAnExistingSliceID(t *testing.T) {
	t.Parallel()

	existing := []store.Slice{{ID: "fix-1-a"}}
	slices := []store.Slice{{ID: "fix-1-a"}, {ID: "fix-1-b"}}
	disambiguateFixSliceIDs(slices, existing)
	got := sliceIDs(slices)
	want := []string{"fix-1-a-2", "fix-1-b"}
	if !equalStrings(got, want) {
		t.Fatalf("ids = %v, want %v (the batch's fix-1-a must yield to the existing slice)", got, want)
	}
}

func TestSanitizeSliceIDReplacesUnsafeCharacters(t *testing.T) {
	t.Parallel()

	if got := sanitizeSliceID("fix-1-svc/a-test:x"); got != "fix-1-svc-a-test-x" {
		t.Fatalf("sanitizeSliceID = %q, want fix-1-svc-a-test-x", got)
	}
}

// --- helpers ---------------------------------------------------------------

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }
