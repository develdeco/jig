package verifydeliver

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
	"gopkg.in/yaml.v3"
)

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
	// workspace has no build target (design 5.5), so it derives no
	// workspace.
	narrow := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "billing", Path: "billing"}}}
	if got := workspaceFor("other/x.go", narrow); got != "" {
		t.Errorf("workspaceFor with no matching workspace = %q, want \"\"", got)
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
	reported, cleared := ApplyRound(1, map[string]Finding{}, result, nil, man)
	if len(cleared) != 0 {
		t.Fatalf("cleared = %v, want none", cleared)
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
	// Ids are distinct and follow the r<round>-f<k> shape (design 5.1).
	seen := map[string]bool{}
	for _, f := range reported {
		if seen[f.ID] {
			t.Errorf("duplicate id %q", f.ID)
		}
		seen[f.ID] = true
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
	reported, cleared := ApplyRound(2, known, result, nil, man)
	if len(cleared) != 0 {
		t.Fatalf("cleared = %v, want none", cleared)
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
	reported, cleared := ApplyRound(2, known, result, nil, man)
	if len(cleared) != 0 {
		t.Fatalf("cleared = %v, want none", cleared)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %+v, want 1 finding", reported)
	}
	f := reported[0]
	if f.ID != "r1-f3" || f.Status != StatusDismissed {
		t.Errorf("finding = %+v, want id r1-f3 status dismissed", f)
	}
}

// --- ApplyRound: rule 3 (clearing) -----------------------------------------

func TestApplyRoundRule3ClearsUnreportedFindingWhenFileReviewed(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r"},
	}
	// a.go was reviewed this round (in ReviewedPaths) and no finding was
	// reported in it: rule 3 clears r1-f1.
	result := ReviewResult{ReviewedPaths: []string{"a.go"}}
	reported, cleared := ApplyRound(2, known, result, nil, man)
	if len(reported) != 0 {
		t.Fatalf("reported = %+v, want none", reported)
	}
	if !equalStrings(cleared, []string{"r1-f1"}) {
		t.Fatalf("cleared = %v, want [r1-f1]", cleared)
	}
}

func TestApplyRoundRule3ClearsFindingWhoseFileWasDeleted(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r"},
	}
	result := ReviewResult{ReviewedPaths: nil}
	reported, cleared := ApplyRound(2, known, result, []string{"a.go"}, man)
	if len(reported) != 0 {
		t.Fatalf("reported = %+v, want none", reported)
	}
	if !equalStrings(cleared, []string{"r1-f1"}) {
		t.Fatalf("cleared = %v, want [r1-f1]", cleared)
	}
}

func TestApplyRoundRule3StaysOpenWhenNotReviewed(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r"},
	}
	// a.go is neither reviewed nor deleted this round, and not reported
	// again: it cannot clear (design 5.2 rule 3, "otherwise it stays
	// open"), and it is absent from this round's own reported list - the
	// fold, not ApplyRound, is what carries it forward unchanged.
	result := ReviewResult{ReviewedPaths: []string{"other.go"}}
	reported, cleared := ApplyRound(2, known, result, nil, man)
	if len(reported) != 0 {
		t.Fatalf("reported = %+v, want none", reported)
	}
	if len(cleared) != 0 {
		t.Fatalf("cleared = %v, want none", cleared)
	}
}

// TestApplyRoundConvergenceDismissedRepeatDoesNotBlockOpenFinding is Q6's
// convergence test: a dismissed finding re-reported in the same file as an
// open one must not keep that open one alive.
func TestApplyRoundConvergenceDismissedRepeatDoesNotBlockOpenFinding(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r"},
		"r1-f2": {ID: "r1-f2", File: "a.go", Status: StatusDismissed, Action: ActionFix, Risk: RiskLow, RiskRationale: "r"},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			// Only the dismissed finding recurs; r1-f1 is not reported
			// again at all.
			{File: "a.go", Title: "dismissed again", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f2"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	_, cleared := ApplyRound(2, known, result, nil, man)
	if !equalStrings(cleared, []string{"r1-f1"}) {
		t.Fatalf("cleared = %v, want [r1-f1] (the dismissed repeat must not block it)", cleared)
	}
}

// TestApplyRoundConvergenceRoutedFindingBlocksClearing is the other half
// of Q6: a new or recurring fix/ask finding reported in the same file as
// an unreported open finding blocks that finding from clearing.
func TestApplyRoundConvergenceRoutedFindingBlocksClearing(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r"},
	}
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "a new problem", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, cleared := ApplyRound(2, known, result, nil, man)
	if len(cleared) != 0 {
		t.Fatalf("cleared = %v, want none (a.go still has a routed finding)", cleared)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %+v, want 1 (the new finding only)", reported)
	}
}

// --- ApplyRound: recurrence bound (5.3, Q7) --------------------------------

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
	reported, _ := ApplyRound(2, known, result, nil, man)
	if len(reported) != 1 || reported[0].Status != StatusOpen || reported[0].Recurrences != 1 {
		t.Fatalf("reported = %+v, want one open finding at recurrences 1", reported)
	}
}

func TestApplyRoundRecurrenceBoundSecondForcesAsk(t *testing.T) {
	man := oneOracleManifest()
	known := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Status: StatusOpen, Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Recurrences: 1},
	}
	// The reviewer still labels it fix; the bound overrides it to ask
	// anyway ("whatever the reviewer's label", design 5.3).
	result := ReviewResult{
		Findings: []ResultFinding{
			{File: "a.go", Title: "still there", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "test", Prior: "r1-f1"},
		},
		ReviewedPaths: []string{"a.go"},
	}
	reported, _ := ApplyRound(3, known, result, nil, man)
	if len(reported) != 1 || reported[0].Status != StatusAsked || reported[0].Recurrences != 2 {
		t.Fatalf("reported = %+v, want one asked finding at recurrences 2", reported)
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

// TestRenderFindingsMDNeverSaysCleanForANonCleanRound guards the main-loop
// review's finding on S2: a round that reports nothing new must not render
// "clean" when its own verdict is fix-slices (an earlier round's finding is
// still open or asked, just not reported against again this round).
func TestRenderFindingsMDNeverSaysCleanForANonCleanRound(t *testing.T) {
	md := renderFindingsMD(3, "fix-slices", "", nil, nil)
	if indexOf(md, "clean") >= 0 {
		t.Errorf("findings.md = %q, must not say clean for a fix-slices round", md)
	}
	if indexOf(md, "verdict: fix-slices") < 0 {
		t.Errorf("findings.md = %q, want it to record verdict: fix-slices", md)
	}
}

// TestRenderFindingsMDShowsTriageDecisionRoutedAsAndCleared covers Q2's
// additive fields and the cleared-ids line.
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

func TestMarshalFindingsYAMLRoundTripsQ2Fields(t *testing.T) {
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
		t.Errorf("finding = %+v, want Q2 fields round-tripped", got)
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
