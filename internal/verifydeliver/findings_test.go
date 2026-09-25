package verifydeliver

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
	"gopkg.in/yaml.v3"
)

// sliceRecording returns an existingSlices stub with one slice, "fix-x",
// whose Findings names id (the structural link, not a parsed slice id):
// the shape findingHasGreenFixSlice looks for.
func sliceRecording(id string) []store.Slice {
	return []store.Slice{{ID: "fix-x", Findings: []string{id}}}
}

// alwaysGreen is an ApplyRound sliceGreen stub for tests that want every
// existing slice treated as finished.
func alwaysGreen(string) (bool, error) { return true, nil }

// greenExcept is an ApplyRound sliceGreen stub reporting every slice green
// except the named ones (still queued or building).
func greenExcept(notGreen ...string) func(string) (bool, error) {
	set := map[string]bool{}
	for _, id := range notGreen {
		set[id] = true
	}
	return func(id string) (bool, error) { return !set[id], nil }
}

// --- workspaceFor ---------------------------------------------------------

func TestWorkspaceForNestedPathsAndNoWorkspace(t *testing.T) {
	man := manifest.Manifest{Workspaces: []manifest.Workspace{
		{ID: "root", Path: "."},
		{ID: "billing", Path: "billing"},
		{ID: "billing-invoices", Path: "billing/invoices"},
		{ID: "other", Path: "other/"},
	}}

	cases := []struct {
		file string
		want string
	}{
		{"main.go", "root"},
		{"billing/report.go", "billing"},
		{"billing/invoices/list.go", "billing-invoices"},
		{"billingx/foo.go", "root"}, // must not match "billing" as a raw string prefix
		{"other/x.go", "other"},
	}
	for _, c := range cases {
		if got := workspaceFor(c.file, man); got != c.want {
			t.Errorf("workspaceFor(%q) = %q, want %q", c.file, got, c.want)
		}
	}

	// No root workspace declared at all: a file outside every declared
	// workspace has no build target, so it derives no workspace.
	narrow := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "billing", Path: "billing"}}}
	if got := workspaceFor("other/x.go", narrow); got != "" {
		t.Errorf("workspaceFor with no matching workspace = %q, want \"\"", got)
	}
}

// TestWorkspaceForNormalizesDeclaredPath pins that a workspace path is read
// in the same normalized form a finding's file already has. A manifest path
// is written by hand, and "./billing" or "billing\" names the same
// directory as "billing"; matching them literally would leave every finding
// in that workspace with no derivable build target, routing each one to a
// human as an unbuildable ask.
func TestWorkspaceForNormalizesDeclaredPath(t *testing.T) {
	man := manifest.Manifest{Workspaces: []manifest.Workspace{
		{ID: "root", Path: "./"},
		{ID: "billing", Path: "./billing"},
		{ID: "billing-invoices", Path: ".\\billing\\invoices\\"},
	}}

	cases := []struct {
		file string
		want string
	}{
		{"main.go", "root"},
		{"billing/report.go", "billing"},
		{"billing/invoices/list.go", "billing-invoices"},
		{"billingx/foo.go", "root"},
	}
	for _, c := range cases {
		if got := workspaceFor(c.file, man); got != c.want {
			t.Errorf("workspaceFor(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

// --- statusForAction ---------------------------------------------------------

// TestStatusForActionRejectsUnknownAction: every action jig recognizes is
// handled explicitly, and an action outside that set (only
// reachable if a caller skips ParseReviewResult's own validation) is a
// programming error returned up the call chain, never silently folded into
// StatusNoted.
func TestStatusForActionRejectsUnknownAction(t *testing.T) {
	for _, action := range []string{ActionFix, ActionAsk, ActionNote} {
		status, err := statusForAction(action)
		if err != nil {
			t.Errorf("statusForAction(%q): %v", action, err)
		}
		if status == "" {
			t.Errorf("statusForAction(%q) = empty status", action)
		}
	}
	if _, err := statusForAction("maybe"); err == nil {
		t.Error("statusForAction(\"maybe\"): want an error, got nil")
	}
	if _, err := statusForAction(""); err == nil {
		t.Error("statusForAction(\"\"): want an error, got nil")
	}
}

// --- ApplyRound: rule 4 (new findings) ------------------------------------

func TestApplyRoundRule4NewFindingsRouteByAction(t *testing.T) {
	man := oneOracleManifest()
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "fix me", Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test"},
			{File: "b.go", Title: "ask me", Detail: "d", Action: ActionAsk, Risk: RiskMedium, RiskRationale: "r", Oracle: "test"},
			{File: "c.go", Title: "note me", Detail: "d", Action: ActionNote, Risk: RiskLow, RiskRationale: "r"},
		},
		ReviewedPaths: []string{"a.go", "b.go", "c.go"},
	}
	reported, err := ApplyRound(1, map[string]Finding{}, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 3 {
		t.Fatalf("reported = %+v, want 3 findings", reported)
	}
	byFile := map[string]Finding{}
	for _, f := range reported {
		byFile[f.File] = f
	}
	if got := byFile["a.go"].Status; got != StatusOpen {
		t.Errorf("a.go status = %q, want open", got)
	}
	if got := byFile["b.go"].Status; got != StatusAsked {
		t.Errorf("b.go status = %q, want asked", got)
	}
	if got := byFile["c.go"].Status; got != StatusNoted {
		t.Errorf("c.go status = %q, want noted", got)
	}
	for _, f := range reported {
		if f.ID == "" {
			t.Errorf("finding for %s has no id", f.File)
		}
		if f.Recurrences != 0 {
			t.Errorf("finding for %s recurrences = %d, want 0", f.File, f.Recurrences)
		}
	}
	// Ids are distinct and follow the r<round>-f<k> shape.
	seen := map[string]bool{}
	for _, f := range reported {
		if seen[f.ID] {
			t.Errorf("duplicate id %q", f.ID)
		}
		seen[f.ID] = true
	}
}

// TestApplyRoundRecordsTheSoleOracleWhenTheReviewerOmitsIt: with exactly
// one manifest oracle there is no choice to make, so a fix or ask finding
// that leaves oracle unset still gets it recorded - never left empty on a
// manifest that does have one.
func TestApplyRoundRecordsTheSoleOracleWhenTheReviewerOmitsIt(t *testing.T) {
	man := oneOracleManifest()
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "fix me", Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(1, map[string]Finding{}, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if reported[0].Oracle != "test" {
		t.Errorf("Oracle = %q, want the manifest's sole oracle recorded", reported[0].Oracle)
	}
}

// TestApplyRoundForcesAskWhenNoOracleCanBeResolved: a fix finding whose
// oracle cannot be resolved against a multi-oracle manifest (omitted, and
// there is no single default to fall back to) has no build target jig can
// derive, the same as a no-workspace fix, so it routes to the human as an
// ask instead of silently becoming a fix slice with no oracle.
func TestApplyRoundForcesAskWhenNoOracleCanBeResolved(t *testing.T) {
	man := oneOracleManifest()
	man.Oracles["lint"] = "true" // two oracles now: no single default
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "fix me", Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(1, map[string]Finding{}, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	f := reported[0]
	if f.Status != StatusAsked || f.RoutedAs != ActionAsk {
		t.Errorf("finding = %+v, want asked/routed_as ask (no oracle jig can resolve)", f)
	}
}

// TestApplyRoundStaleOracleAfterManifestChangeForcesAsk covers a recurrence
// whose earlier occurrence recorded an oracle the manifest no longer has
// (removed or renamed between rounds): the recorded oracle is not a
// manifest oracle any more, so this is the same missing-build-target case,
// not a silent pass-through.
func TestApplyRoundStaleOracleAfterManifestChangeForcesAsk(t *testing.T) {
	man := manifest.Manifest{
		Oracles:    map[string]string{"test": "true", "vet": "true"},
		Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
	}
	// Recurrences stays at 0 and no slice recorded it yet, so the recurrence
	// bound itself has no say here: only the stale oracle can force this to
	// ask.
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "old"},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "still there", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	f := reported[0]
	if f.Recurrences >= 2 {
		t.Fatalf("test setup: Recurrences = %d, want under 2 so the recurrence bound isn't what forces this", f.Recurrences)
	}
	if f.Status != StatusAsked || f.RoutedAs != ActionAsk {
		t.Errorf("finding = %+v, want asked/routed_as ask (the recorded oracle is no longer a manifest oracle)", f)
	}
}

// TestApplyRoundRejectsAnUnnormalizableFindingFile: a finding file that
// fails path normalization (an absolute path or a ".." segment - already
// validated upstream by ParseReviewResult in production, but ApplyRound is
// directly callable, so it must not silently keep an invalid path either)
// is an error, the same as an unknown action, never kept as if valid.
func TestApplyRoundRejectsAnUnnormalizableFindingFile(t *testing.T) {
	man := oneOracleManifest()
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "../secret.go", Title: "t", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test"},
		},
		ReviewedPaths: []string{"../secret.go"},
	}
	if _, err := ApplyRound(1, map[string]Finding{}, result, nil, alwaysGreen, man); err == nil {
		t.Error("ApplyRound: want an error for an unnormalizable finding file, got nil")
	}
}

// --- ApplyRound: rule 1 (open recurrence) ---------------------------------

func TestApplyRoundRule1RecurrenceStaysOpenAndCountsUp(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Line: 1, Title: "old title", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "old", Recurrences: 0},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Line: 2, Title: "new title", Detail: "still there", Action: ActionFix, Risk: RiskHigh, RiskRationale: "new", Oracle: "test", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, sliceRecording("r1-f1"), alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %+v, want 1 finding", reported)
	}
	f := reported[0]
	if f.ID != "r1-f1" {
		t.Errorf("ID = %q, want r1-f1 (a recurrence keeps its id)", f.ID)
	}
	if f.Status != StatusOpen {
		t.Errorf("Status = %q, want open", f.Status)
	}
	if f.Recurrences != 1 {
		t.Errorf("Recurrences = %d, want 1", f.Recurrences)
	}
	if f.Title != "new title" || f.Risk != RiskHigh {
		t.Errorf("finding = %+v, want this round's content", f)
	}
}

// --- ApplyRound: rule 2 (dismissed recurrence) -----------------------------

func TestApplyRoundRule2DismissedRecurrenceStaysDismissed(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f3": {ID: "r1-f3", File: "a.go", Line: 7, Title: "old", Status: StatusDismissed, Action: ActionFix, Risk: RiskLow, RiskRationale: "old"},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Line: 7, Title: "old again", Detail: "still there", Action: ActionFix, Risk: RiskHigh, RiskRationale: "new", Oracle: "test", Prior: "r1-f3"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %+v, want 1 finding", reported)
	}
	f := reported[0]
	if f.ID != "r1-f3" || f.Status != StatusDismissed {
		t.Errorf("finding = %+v, want id r1-f3 status dismissed", f)
	}
}

// TestApplyRoundRule2ResetsTriageFieldsOnADismissedRepeat: a dismissed
// repeat must not restate an earlier round's human decision as if it were
// made again this round.
func TestApplyRoundRule2ResetsTriageFieldsOnADismissedRepeat(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f3": {ID: "r1-f3", File: "a.go", Status: StatusDismissed, Action: ActionFix, Risk: RiskLow, RiskRationale: "old",
			Triage: TriageHuman, Decision: "not worth it", RoutedAs: ActionAsk},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "old again", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f3"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	f := reported[0]
	if f.Triage != "" || f.Decision != "" || f.RoutedAs != "" {
		t.Errorf("dismissed repeat = %+v, want Triage/Decision/RoutedAs all empty (nobody decided this round)", f)
	}
}

// --- ApplyRound: rule 1 carries forward oracle, decision and workspace ---

func TestApplyRoundRule1CarriesOracleWhenThisRoundNamesNone(t *testing.T) {
	man := oneOracleManifest()
	man.Oracles["lint"] = "true" // now two oracles, so an omitted oracle is never a default
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "old", Oracle: "test"},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			// This round's own report names no oracle at all (a bare note).
			{File: "a.go", Title: "still there", Detail: "d", Action: ActionNote, Risk: RiskLow, RiskRationale: "r", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if reported[0].Oracle != "test" {
		t.Errorf("Oracle = %q, want the prior occurrence's test (carried forward)", reported[0].Oracle)
	}
}

func TestApplyRoundRule1CarriesDecisionAndWorkspaceWhenFileUnchanged(t *testing.T) {
	man := manifest.Manifest{Oracles: map[string]string{"test": "true"}} // no workspaces declared
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "orphan.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "old",
			Oracle: "test", Workspace: "billing", Decision: "ship it in billing"},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "orphan.go", Title: "still there", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"orphan.go"},
	}
	reported, err := ApplyRound(2, known, result, nil, alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	f := reported[0]
	if f.Workspace != "billing" {
		t.Errorf("Workspace = %q, want billing (the human's earlier choice, file unchanged)", f.Workspace)
	}
	if f.Decision != "ship it in billing" {
		t.Errorf("Decision = %q, want the earlier kept ask's decision carried forward", f.Decision)
	}
	if f.Status != StatusOpen {
		t.Errorf("Status = %q, want open (a non-empty carried workspace, not forced to ask)", f.Status)
	}
}

// --- ApplyRound: recurrence bound ---------------------------------------

func TestApplyRoundRecurrenceBoundFirstRoutesLikeNew(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Recurrences: 0},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "still there", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, sliceRecording("r1-f1"), alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 || reported[0].Status != StatusOpen || reported[0].Recurrences != 1 {
		t.Fatalf("reported = %+v, want one open finding at recurrences 1", reported)
	}
}

// TestApplyRoundRecurrenceNotCountedWithoutAFixSlice: a re-report with
// prior naming an id that no existing slice has ever recorded is not a
// genuine recurrence (the recurrence bound's premise needs a fix slice to
// have gone green without resolving it) - it updates the finding without
// bumping Recurrences. Covers both an undecided ask re-reported and an
// --early round outrunning the frontier: neither has built a slice for the
// id yet.
func TestApplyRoundRecurrenceNotCountedWithoutAFixSlice(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusAsked, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Recurrences: 0},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "still there", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, nil, alwaysGreen, man) // no existing slice records r1-f1
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 || reported[0].Recurrences != 0 {
		t.Fatalf("reported = %+v, want recurrences unchanged at 0 (no fix slice ever went green for it)", reported)
	}
}

// TestApplyRoundRecurrenceNotCountedWhenRecordingSliceIsNotGreen covers the
// other half: a slice recording the id exists, but is still queued or
// building (as under --early), which is not enough on its own - the
// recurrence bound's premise is a slice that already went green without
// resolving it.
func TestApplyRoundRecurrenceNotCountedWhenRecordingSliceIsNotGreen(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Recurrences: 0},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "still there", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(2, known, result, sliceRecording("r1-f1"), greenExcept("fix-x"), man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 || reported[0].Recurrences != 0 {
		t.Fatalf("reported = %+v, want recurrences unchanged at 0 (the recording slice is not green yet)", reported)
	}
}

func TestApplyRoundRecurrenceBoundSecondForcesAsk(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Recurrences: 1},
	}
	// The reviewer still labels it fix; the bound overrides it to ask
	// anyway, whatever the reviewer's label.
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "still there", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(3, known, result, sliceRecording("r1-f1"), alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 || reported[0].Status != StatusAsked || reported[0].Recurrences != 2 {
		t.Fatalf("reported = %+v, want one asked finding at recurrences 2", reported)
	}
}

// TestApplyRoundRecurrenceBoundSecondAsNoteKeepsPriorOracle covers this
// scenario: a second recurrence reported as a bare note still carries
// an oracle forward, so the ask it becomes can build a fix slice if kept.
func TestApplyRoundRecurrenceBoundSecondAsNoteKeepsPriorOracle(t *testing.T) {
	man := oneOracleManifest()
	man.Oracles["lint"] = "true"
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Recurrences: 1},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "still there but harmless now", Detail: "d", Action: ActionNote, Risk: RiskLow, RiskRationale: "r", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, err := ApplyRound(3, known, result, sliceRecording("r1-f1"), alwaysGreen, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	f := reported[0]
	if f.Status != StatusAsked || f.RoutedAs != ActionAsk {
		t.Fatalf("finding = %+v, want asked/routed_as ask (bound forces it, whatever this round's label)", f)
	}
	if f.Oracle != "test" {
		t.Errorf("Oracle = %q, want the prior occurrence's test carried forward", f.Oracle)
	}
}

// --- ClearingAfterTriage (rule 3) -------------------------------------------

func TestClearingAfterTriageClearsUnreportedFindingWhenFileReviewed(t *testing.T) {
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen},
	}
	// a.go was reviewed this round and no finding was reported in it: rule
	// 3 clears r1-f1.
	cleared, err := ClearingAfterTriage(known, nil, []string{"a.go"}, alwaysExists)
	if err != nil {
		t.Fatalf("ClearingAfterTriage: %v", err)
	}
	if !equalStrings(cleared, []string{"r1-f1"}) {
		t.Fatalf("cleared = %v, want [r1-f1]", cleared)
	}
}

// TestClearingAfterTriageKeepsAnUndecidedAsk pins that an ask nobody has
// decided never clears on its own - not when a later round reads its file
// and reports nothing there, and not when the file is gone at head. An ask
// is a question put to a person, and nothing the reviewer reports answers
// it. Letting coverage clear one would mean an unattended run drops the
// question, reports the round clean and points at publish, shipping the
// ticket with the decision never made; an undecided ask is exactly what
// an unattended run is supposed to park on.
func TestClearingAfterTriageKeepsAnUndecidedAsk(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exists func(string) (bool, error)
	}{
		{"file reviewed with nothing reported", alwaysExists},
		{"file gone at head", func(string) (bool, error) { return false, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			known := map[string]Finding{
				"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusAsked},
			}
			cleared, err := ClearingAfterTriage(known, nil, []string{"a.go"}, tc.exists)
			if err != nil {
				t.Fatalf("ClearingAfterTriage: %v", err)
			}
			if len(cleared) != 0 {
				t.Fatalf("cleared = %v, want none: only a human decision resolves an ask", cleared)
			}
		})
	}
}

func TestClearingAfterTriageClearsFindingWhoseFileIsGoneAtHead(t *testing.T) {
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen},
	}
	cleared, err := ClearingAfterTriage(known, nil, nil, existsExcept("a.go"))
	if err != nil {
		t.Fatalf("ClearingAfterTriage: %v", err)
	}
	if !equalStrings(cleared, []string{"r1-f1"}) {
		t.Fatalf("cleared = %v, want [r1-f1]", cleared)
	}
}

func TestClearingAfterTriageStaysOpenWhenNotReviewed(t *testing.T) {
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen},
	}
	// a.go is neither reviewed nor gone at head this round, and not
	// reported again: it cannot clear (rule 3, "otherwise it stays open").
	cleared, err := ClearingAfterTriage(known, nil, []string{"other.go"}, alwaysExists)
	if err != nil {
		t.Fatalf("ClearingAfterTriage: %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("cleared = %v, want none", cleared)
	}
}

// TestClearingAfterTriageDismissedRepeatDoesNotBlockOpenFinding is the
// convergence test: a dismissed finding re-reported in the same file as an
// open one must not keep that open one alive.
func TestClearingAfterTriageDismissedRepeatDoesNotBlockOpenFinding(t *testing.T) {
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen},
		"r1-f2": {ID: "r1-f2", File: "a.go", Status: StatusDismissed},
	}
	// Only the dismissed finding was reported this round (r1-f2, now still
	// dismissed); r1-f1 was not reported again at all.
	reported := []Finding{{ID: "r1-f2", File: "a.go", Status: StatusDismissed}}
	cleared, err := ClearingAfterTriage(known, reported, []string{"a.go"}, alwaysExists)
	if err != nil {
		t.Fatalf("ClearingAfterTriage: %v", err)
	}
	if !equalStrings(cleared, []string{"r1-f1"}) {
		t.Fatalf("cleared = %v, want [r1-f1] (the dismissed repeat must not block it)", cleared)
	}
}

// TestClearingAfterTriageRoutedFindingBlocksClearing is the other half:
// a new or recurring fix/ask finding that ends up routed (open or
// asked) in the same file as an unreported open finding blocks that
// finding from clearing.
func TestClearingAfterTriageRoutedFindingBlocksClearing(t *testing.T) {
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen},
	}
	reported := []Finding{{ID: "r2-f1", File: "a.go", Status: StatusOpen}}
	cleared, err := ClearingAfterTriage(known, reported, []string{"a.go"}, alwaysExists)
	if err != nil {
		t.Fatalf("ClearingAfterTriage: %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("cleared = %v, want none (a.go still has a routed finding)", cleared)
	}
}

// TestClearingAfterTriageDismissedAtTriageDoesNotBlock: a finding
// the human dismisses at triage must not go on blocking an unrelated open
// finding in the same file, because clearing runs against the final,
// post-triage status, not what the reviewer reported before triage.
func TestClearingAfterTriageDismissedAtTriageDoesNotBlock(t *testing.T) {
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen},
	}
	// r2-f1 was reported open this round, but triage (route.go) already
	// dismissed it by the time ClearingAfterTriage runs.
	reported := []Finding{{ID: "r2-f1", File: "a.go", Status: StatusDismissed}}
	cleared, err := ClearingAfterTriage(known, reported, []string{"a.go"}, alwaysExists)
	if err != nil {
		t.Fatalf("ClearingAfterTriage: %v", err)
	}
	if !equalStrings(cleared, []string{"r1-f1"}) {
		t.Fatalf("cleared = %v, want [r1-f1] (a triage dismissal must not keep blocking it)", cleared)
	}
}

// --- fold / clean -----------------------------------------------------------

func TestFoldFindingsWithCleared(t *testing.T) {
	cum := map[string]Finding{}
	foldFindings(cum, []Finding{
		{ID: "r1-f1", Status: StatusOpen},
		{ID: "r1-f2", Status: StatusDismissed},
	}, nil)
	if len(cum) != 2 {
		t.Fatalf("cum = %+v, want 2 entries", cum)
	}
	// Round 2: r1-f1 recurs (latest occurrence wins) and r1-f2 is a fresh
	// dismissal target that never recurred; round 2 also clears nothing
	// yet, then round 3 clears r1-f1.
	foldFindings(cum, []Finding{
		{ID: "r1-f1", Status: StatusOpen, Recurrences: 1},
		{ID: "r3-f1", Status: StatusNoted},
	}, nil)
	if cum["r1-f1"].Recurrences != 1 {
		t.Fatalf("r1-f1 = %+v, want recurrences 1 (latest occurrence wins)", cum["r1-f1"])
	}
	foldFindings(cum, nil, []string{"r1-f1"})
	if _, ok := cum["r1-f1"]; ok {
		t.Fatal("r1-f1 survived being cleared")
	}
	if len(cum) != 2 {
		t.Fatalf("cum = %+v, want r1-f2 and r3-f1 left", cum)
	}
}

func TestIsCleanWithNotesAndDismissedPresent(t *testing.T) {
	cum := map[string]Finding{
		"r1-f1": {ID: "r1-f1", Status: StatusNoted},
		"r1-f2": {ID: "r1-f2", Status: StatusDismissed},
	}
	if !isClean(cum) {
		t.Errorf("isClean = false, want true (notes and dismissed findings never block clean)")
	}
	cum["r1-f3"] = Finding{ID: "r1-f3", Status: StatusOpen}
	if isClean(cum) {
		t.Errorf("isClean = true, want false (an open finding blocks clean)")
	}
	delete(cum, "r1-f3")
	cum["r1-f4"] = Finding{ID: "r1-f4", Status: StatusAsked}
	if isClean(cum) {
		t.Errorf("isClean = true, want false (an asked finding blocks clean)")
	}
}

func TestOpenAndDismissedFindingsListsSplitByStatus(t *testing.T) {
	cum := map[string]Finding{
		"r1-f1": {ID: "r1-f1", Status: StatusOpen},
		"r1-f2": {ID: "r1-f2", Status: StatusAsked},
		"r1-f3": {ID: "r1-f3", Status: StatusDismissed},
		"r1-f4": {ID: "r1-f4", Status: StatusNoted},
	}
	open := openFindingsList(cum)
	if len(open) != 2 {
		t.Fatalf("open = %+v, want 2 (open + asked)", open)
	}
	dismissed := dismissedFindingsList(cum)
	if len(dismissed) != 1 || dismissed[0].ID != "r1-f3" {
		t.Fatalf("dismissed = %+v, want [r1-f3]", dismissed)
	}
}

// --- cumulativeFindings (store-backed fold) ---------------------------------

func TestCumulativeFindingsFoldsAcrossRoundsAndSkipsMissingOnes(t *testing.T) {
	st := newReviewStore(t)
	ticket := "JIG-1"

	write := func(round int, ff findingsYAML) {
		t.Helper()
		dir := gateRoundDir(st, ticket, round)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir round %d: %v", round, err)
		}
		data, err := marshalFindingsYAML(ff.Scope, ff.ReviewedPaths, ff.Findings, ff.Cleared, ff.Summary)
		if err != nil {
			t.Fatalf("marshal round %d: %v", round, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), data, 0o644); err != nil {
			t.Fatalf("write round %d: %v", round, err)
		}
	}

	write(1, findingsYAML{Scope: "full", Findings: []Finding{
		{ID: "r1-f1", File: "a.go", Status: StatusOpen},
		{ID: "r1-f2", File: "b.go", Status: StatusDismissed},
	}})
	// Round 2 is a scripted round with no findings.yaml at all: it must
	// contribute nothing to the fold.
	write(3, findingsYAML{Scope: "delta", Findings: []Finding{
		{ID: "r1-f1", File: "a.go", Status: StatusOpen, Recurrences: 1},
	}, Cleared: []string{"r1-f2"}})

	cum, err := cumulativeFindings(st, ticket, 4)
	if err != nil {
		t.Fatalf("cumulativeFindings: %v", err)
	}
	if len(cum) != 1 {
		t.Fatalf("cum = %+v, want only r1-f1 (r1-f2 was cleared)", cum)
	}
	if cum["r1-f1"].Recurrences != 1 {
		t.Errorf("r1-f1 = %+v, want recurrences 1 (round 3's occurrence)", cum["r1-f1"])
	}

	// Folding only up to round 1 (exclusive of round 1 itself) sees
	// nothing yet.
	empty, err := cumulativeFindings(st, ticket, 1)
	if err != nil {
		t.Fatalf("cumulativeFindings upToRound=1: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("cum = %+v, want empty", empty)
	}
}

// --- FoldBefore (store-backed fold, projected) ------------------------------

// TestFoldBeforeProjectsOpenNotedAndDismissed pins Fold's three views of the
// same cumulative state: a noted finding is a citable prior target (Open)
// but never a false alarm (findings.go's isClean, elsewhere), a dismissed
// finding appears only in Dismissed, a later round's cleared id is gone from
// Known, Open and Dismissed alike, and the latest occurrence of a
// recurring id wins.
func TestFoldBeforeProjectsOpenNotedAndDismissed(t *testing.T) {
	st := newReviewStore(t)
	ticket := "JIG-1"

	write := func(round int, ff findingsYAML) {
		t.Helper()
		dir := gateRoundDir(st, ticket, round)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir round %d: %v", round, err)
		}
		data, err := marshalFindingsYAML(ff.Scope, ff.ReviewedPaths, ff.Findings, ff.Cleared, ff.Summary)
		if err != nil {
			t.Fatalf("marshal round %d: %v", round, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), data, 0o644); err != nil {
			t.Fatalf("write round %d: %v", round, err)
		}
	}

	write(1, findingsYAML{Scope: "full", Findings: []Finding{
		{ID: "r1-f1", File: "a.go", Status: StatusOpen},
		{ID: "r1-f2", File: "b.go", Status: StatusNoted},
		{ID: "r1-f3", File: "c.go", Status: StatusDismissed},
		{ID: "r1-f4", File: "d.go", Status: StatusOpen},
	}})
	write(2, findingsYAML{Scope: "delta", Findings: []Finding{
		{ID: "r1-f1", File: "a.go", Status: StatusOpen, Recurrences: 1},
	}, Cleared: []string{"r1-f4"}})

	fold, err := FoldBefore(st, ticket, 3)
	if err != nil {
		t.Fatalf("FoldBefore: %v", err)
	}

	if len(fold.Known) != 3 {
		t.Fatalf("Known = %+v, want 3 ids (r1-f4 cleared)", fold.Known)
	}
	if _, ok := fold.Known["r1-f4"]; ok {
		t.Errorf("Known still has r1-f4, want it cleared")
	}
	if fold.Known["r1-f1"].Recurrences != 1 {
		t.Errorf("r1-f1 = %+v, want round 2's occurrence (recurrences 1)", fold.Known["r1-f1"])
	}

	openIDs := map[string]bool{}
	for _, f := range fold.Open {
		openIDs[f.ID] = true
	}
	if !openIDs["r1-f1"] || !openIDs["r1-f2"] || len(openIDs) != 2 {
		t.Errorf("Open ids = %+v, want exactly r1-f1 (open) and r1-f2 (noted)", openIDs)
	}

	if len(fold.Dismissed) != 1 || fold.Dismissed[0].ID != "r1-f3" {
		t.Fatalf("Dismissed = %+v, want only r1-f3", fold.Dismissed)
	}
}

// --- rendering ---------------------------------------------------------------

func TestRenderFindingsMDSortsByRiskHighFirst(t *testing.T) {
	findings := []Finding{
		{ID: "r1-f1", Title: "low one", Risk: RiskLow, RiskRationale: "r", Status: StatusOpen},
		{ID: "r1-f2", Title: "high one", Risk: RiskHigh, RiskRationale: "r", Status: StatusOpen},
		{ID: "r1-f3", Title: "medium one", Risk: RiskMedium, RiskRationale: "r", Status: StatusOpen},
	}
	md := renderFindingsMD(1, "fix-slices", "a summary", findings, nil)
	first := indexOf(md, "high one")
	second := indexOf(md, "medium one")
	third := indexOf(md, "low one")
	if !(first >= 0 && first < second && second < third) {
		t.Fatalf("findings.md did not sort high, medium, low:\n%s", md)
	}
	if indexOf(md, "a summary") < 0 {
		t.Errorf("findings.md missing the round summary:\n%s", md)
	}
}

func TestRenderFindingsMDCleanWhenNoFindings(t *testing.T) {
	md := renderFindingsMD(2, "clean", "", nil, nil)
	if indexOf(md, "clean") < 0 {
		t.Errorf("findings.md = %q, want it to say clean", md)
	}
}

// TestRenderFindingsMDNeverSaysCleanForANonCleanRound: a round that reports
// nothing new must not render "clean" when its own verdict is fix-slices
// (an earlier round's finding is still open or asked, just not reported
// against again this round).
func TestRenderFindingsMDNeverSaysCleanForANonCleanRound(t *testing.T) {
	md := renderFindingsMD(3, "fix-slices", "", nil, nil)
	if indexOf(md, "clean") >= 0 {
		t.Errorf("findings.md = %q, must not say clean for a fix-slices round", md)
	}
	if indexOf(md, "verdict: fix-slices") < 0 {
		t.Errorf("findings.md = %q, want it to record verdict: fix-slices", md)
	}
}

// TestRenderFindingsMDShowsTriageDecisionRoutedAsAndCleared covers the
// triage/decision/routed_as additive fields and the cleared-ids line.
func TestRenderFindingsMDShowsTriageDecisionRoutedAsAndCleared(t *testing.T) {
	findings := []Finding{
		{ID: "r2-f1", Title: "kept ask", Risk: RiskHigh, RiskRationale: "r", Status: StatusOpen,
			Action: ActionAsk, Triage: TriageHuman, Decision: "go ahead"},
		{ID: "r2-f2", Title: "recurrence bound", Risk: RiskMedium, RiskRationale: "r", Status: StatusAsked,
			Action: ActionFix, Triage: TriageAuto, RoutedAs: ActionAsk},
	}
	md := renderFindingsMD(2, "fix-slices", "", findings, []string{"r1-f9"})
	for _, want := range []string{"triage: human", "decision: go ahead", "triage: auto", "routed as: ask", "cleared: r1-f9"} {
		if indexOf(md, want) < 0 {
			t.Errorf("findings.md missing %q:\n%s", want, md)
		}
	}
}

// alwaysExists is an existsAtHead stub for tests with nothing gone at head.
func alwaysExists(file string) (bool, error) { return true, nil }

// existsExcept returns an existsAtHead stub reporting every name in gone as
// absent at head and everything else present.
func existsExcept(gone ...string) func(string) (bool, error) {
	missing := map[string]bool{}
	for _, f := range gone {
		missing[f] = true
	}
	return func(file string) (bool, error) { return !missing[file], nil }
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestMarshalFindingsYAMLEmptyListsAsBrackets(t *testing.T) {
	data, err := marshalFindingsYAML("full", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("marshalFindingsYAML: %v", err)
	}
	var ff findingsYAML
	if err := yaml.Unmarshal(data, &ff); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ff.ReviewedPaths == nil {
		t.Error("ReviewedPaths round-tripped as nil, want an empty slice")
	}
	if ff.Findings == nil {
		t.Error("Findings round-tripped as nil, want an empty slice")
	}
}

func TestMarshalFindingsYAMLRoundTripsTriageFields(t *testing.T) {
	findings := []Finding{
		{ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Triage: TriageHuman, Decision: "do it", RoutedAs: ActionAsk},
	}
	data, err := marshalFindingsYAML("full", []string{"a.go"}, findings, []string{"r0-f1"}, "round summary")
	if err != nil {
		t.Fatalf("marshalFindingsYAML: %v", err)
	}
	var ff findingsYAML
	if err := yaml.Unmarshal(data, &ff); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ff.Summary != "round summary" {
		t.Errorf("Summary = %q, want round summary", ff.Summary)
	}
	if !equalStrings(ff.Cleared, []string{"r0-f1"}) {
		t.Errorf("Cleared = %v, want [r0-f1]", ff.Cleared)
	}
	if len(ff.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1", ff.Findings)
	}
	got := ff.Findings[0]
	if got.Triage != TriageHuman || got.Decision != "do it" || got.RoutedAs != ActionAsk {
		t.Errorf("finding = %+v, want the triage fields round-tripped", got)
	}
}

func TestFindingsSortStable(t *testing.T) {
	// Guard against a non-deterministic map iteration leaking into
	// rendered output: two calls with the same input must render
	// identically.
	findings := []Finding{
		{ID: "r1-f1", Title: "a", Risk: RiskHigh, RiskRationale: "r"},
		{ID: "r1-f2", Title: "b", Risk: RiskHigh, RiskRationale: "r"},
	}
	a := renderFindingsMD(1, "fix-slices", "", findings, nil)
	b := renderFindingsMD(1, "fix-slices", "", findings, nil)
	if a != b {
		t.Fatalf("renderFindingsMD is not deterministic:\n%s\n---\n%s", a, b)
	}
	ids := []string{findings[0].ID, findings[1].ID}
	sort.Strings(ids)
	if ids[0] != "r1-f1" || ids[1] != "r1-f2" {
		t.Fatalf("sanity check failed: %v", ids)
	}
}

// TestApplyRoundAnUndecidedAskStaysAskedWhateverTheLabel pins the rule a
// dismissed finding already has, for asked: once a question is put to a
// person, only that person moves it out of asked. A later occurrence
// replaces the finding's text, file, line, action and risk, but never its
// status.
//
// Without this the reviewer's own label retired the question. Re-reported
// as `note` the ask became a record, the round went clean and publish
// unlocked with nobody having answered it; re-reported as `fix` it became
// queued work with no decision recorded anywhere.
func TestApplyRoundAnUndecidedAskStaysAskedWhateverTheLabel(t *testing.T) {
	man := oneOracleManifest()
	for _, action := range []string{ActionNote, ActionFix, ActionAsk} {
		t.Run(action, func(t *testing.T) {
			known := map[string]Finding{
				"r1-f1": {ID: "r1-f1", File: "a.go", Line: 7, Title: "old", Status: StatusAsked, Action: ActionAsk, Risk: RiskLow, RiskRationale: "old"},
			}
			result := ReviewResult{
				Findings: []ResultFinding{
					{File: "a.go", Line: 9, Title: "new title", Detail: "d", Action: action, Risk: RiskHigh, RiskRationale: "new", Oracle: "test", Prior: "r1-f1"},
				},
				ReviewedPaths: []string{"a.go"},
			}
			reported, err := ApplyRound(2, known, result, nil, alwaysGreen, man)
			if err != nil {
				t.Fatalf("ApplyRound: %v", err)
			}
			if len(reported) != 1 {
				t.Fatalf("reported = %+v, want 1 finding", reported)
			}
			f := reported[0]
			if f.Status != StatusAsked {
				t.Errorf("status = %q, want %q: only a person decides an ask", f.Status, StatusAsked)
			}
			// The rest of the occurrence is this round's, as for any
			// recurrence: the question is unanswered, not frozen.
			if f.Line != 9 || f.Title != "new title" || f.Risk != RiskHigh || f.Action != action {
				t.Errorf("finding = %+v, want this round's line, title, risk and label", f)
			}
			if action != ActionAsk && f.RoutedAs != ActionAsk {
				t.Errorf("routed_as = %q, want %q recorded when the label disagrees with the status", f.RoutedAs, ActionAsk)
			}
		})
	}
}
