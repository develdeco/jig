package verifydeliver

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
)

// TestParseReviewResultStillPresentRules covers result.json's still_present
// list at ParseReviewResult: well-formed entries parse, and every rule that
// needs no context rejects with REVIEW_INVALID.
func TestParseReviewResultStillPresentRules(t *testing.T) {
	t.Parallel()

	res, err := ParseReviewResult(validResultJSON(t, func(r *ReviewResult) {
		r.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 52}, {Prior: "r1-f2", Line: 0}}
	}))
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if len(res.StillPresent) != 2 || res.StillPresent[0] != (StillPresentEntry{Prior: "r1-f1", Line: 52}) {
		t.Errorf("StillPresent = %+v, want both entries as written", res.StillPresent)
	}

	cases := []struct {
		name string
		data []byte
	}{
		{"missing still_present", validResultJSONRaw(t, func(m map[string]any) { delete(m, "still_present") })},
		{"null still_present", validResultJSONRaw(t, func(m map[string]any) { m["still_present"] = nil })},
		{"empty prior", validResultJSON(t, func(r *ReviewResult) { r.StillPresent = []StillPresentEntry{{Prior: " ", Line: 1}} })},
		{"negative line", validResultJSON(t, func(r *ReviewResult) { r.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: -1}} })},
		{"unknown entry key", validResultJSONRaw(t, func(m map[string]any) {
			m["still_present"] = []any{map[string]any{"prior": "r1-f1", "line": 1, "title": "t"}}
		})},
		{"case variant entry key", validResultJSONRaw(t, func(m map[string]any) {
			m["still_present"] = []any{map[string]any{"Prior": "r1-f1", "line": 1}}
		})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseReviewResult(c.data)
			if err == nil {
				t.Fatal("ParseReviewResult: expected an error, got nil")
			}
			if code := reviewInvalidCode(t, err); code != "REVIEW_INVALID" {
				t.Errorf("code = %q, want REVIEW_INVALID", code)
			}
		})
	}
}

// TestValidateReviewResultStillPresentRules covers the still_present rules
// that need the request and the lease's head.
func TestValidateReviewResultStillPresentRules(t *testing.T) {
	t.Parallel()

	leaseDir := t.TempDir()
	present := func(string) (bool, error) { return true, nil }
	absent := func(string) (bool, error) { return false, nil }
	noDeleted := map[string]bool{}
	invalid := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	}

	t.Run("open and dismissed ids pass", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 3}, {Prior: "r1-f2", Line: 4}}
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("id names nothing", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.StillPresent = []StillPresentEntry{{Prior: "r9-f9", Line: 1}}
		invalid(t, validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted))
	})

	t.Run("id appears twice", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}, {Prior: "r1-f1", Line: 2}}
		invalid(t, validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted))
	})

	t.Run("id is also a finding's prior", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f1"
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}}
		invalid(t, validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted))
	})

	t.Run("earlier finding's file neither present nor deleted", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings = nil
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}}
		invalid(t, validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), absent, noDeleted))
	})

	t.Run("earlier finding's file deleted in the scope diff", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings = nil
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}}
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), absent, map[string]bool{"a.go": true}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})
}

// TestExpandStillPresentFoldsLikeAFullReport: for every kind of earlier
// finding, ApplyRound on a still_present entry, once expanded, gives exactly
// what it gives for the same finding re-reported in full with prior.
func TestExpandStillPresentFoldsLikeAFullReport(t *testing.T) {
	t.Parallel()

	man := manifest.Manifest{
		Oracles:    map[string]string{"test": "go test ./..."},
		Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "beta"}},
	}
	earlier := map[string]Finding{
		"open fix": {ID: "r1-f1", File: "alpha/a.go", Line: 3, Title: "open fix", Detail: "d1",
			Action: ActionFix, Risk: RiskMedium, RiskRationale: "r1", Oracle: "test", Status: StatusOpen, Workspace: "alpha", Recurrences: 1},
		"noted": {ID: "r1-f2", File: "beta/b.go", Line: 4, Title: "noted", Detail: "d2",
			Action: ActionNote, Risk: RiskLow, RiskRationale: "r2", Oracle: "test", Status: StatusNoted, Workspace: "beta"},
		"asked with a decision": {ID: "r1-f3", File: "alpha/c.go", Line: 5, Title: "asked", Detail: "d3",
			Action: ActionAsk, Risk: RiskHigh, RiskRationale: "r3", Oracle: "test", Status: StatusAsked, Workspace: "alpha",
			Triage: TriageHuman, Decision: "keep the old behavior"},
		"workspace a human chose": {ID: "r1-f4", File: "docs/d.md", Line: 6, Title: "outside every workspace", Detail: "d4",
			Action: ActionFix, Risk: RiskMedium, RiskRationale: "r4", Oracle: "test", Status: StatusOpen, Workspace: "beta"},
		"dismissed": {ID: "r1-f5", File: "beta/e.go", Line: 7, Title: "dismissed", Detail: "d5",
			Action: ActionFix, Risk: RiskLow, RiskRationale: "r5", Oracle: "test", Status: StatusDismissed, Workspace: "beta",
			Triage: TriageHuman, Decision: "not worth it"},
	}
	for name, f := range earlier {
		t.Run(name, func(t *testing.T) {
			known := map[string]Finding{f.ID: f}
			full := ReviewResult{Findings: []ResultFinding{{
				File: f.File, Line: 9, Title: f.Title, Detail: f.Detail, Action: f.Action,
				Risk: f.Risk, RiskRationale: f.RiskRationale, Oracle: f.Oracle, Prior: f.ID,
			}}}
			short := ReviewResult{StillPresent: []StillPresentEntry{{Prior: f.ID, Line: 9}}}

			want, err := ApplyRound(2, known, full, nil, alwaysGreen, man, defaultTestFixRisks(), false)
			if err != nil {
				t.Fatalf("ApplyRound on the full re-report: %v", err)
			}
			got, err := ApplyRound(2, known, ExpandStillPresent(short, known), nil, alwaysGreen, man, defaultTestFixRisks(), false)
			if err != nil {
				t.Fatalf("ApplyRound on the expanded short form: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("short form folds to\n%+v\nwant the full re-report's\n%+v", got, want)
			}
		})
	}
}
