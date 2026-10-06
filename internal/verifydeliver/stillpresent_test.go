package verifydeliver

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
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
		if err := validateReviewResult(req, result, leaseDir, present, noDeleted, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("id names nothing", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.StillPresent = []StillPresentEntry{{Prior: "r9-f9", Line: 1}}
		invalid(t, validateReviewResult(req, result, leaseDir, present, noDeleted, []string{"test"}))
	})

	t.Run("id appears twice", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}, {Prior: "r1-f1", Line: 2}}
		invalid(t, validateReviewResult(req, result, leaseDir, present, noDeleted, []string{"test"}))
	})

	t.Run("id is also a finding's prior", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f1"
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}}
		invalid(t, validateReviewResult(req, result, leaseDir, present, noDeleted, []string{"test"}))
	})

	t.Run("earlier finding's file neither present nor deleted", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings = nil
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}}
		invalid(t, validateReviewResult(req, result, leaseDir, absent, noDeleted, []string{"test"}))
	})

	t.Run("earlier finding's file deleted in the scope diff", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings = nil
		result.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}}
		if err := validateReviewResult(req, result, leaseDir, absent, map[string]bool{"a.go": true}, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})
}

// TestGateFoldsAStillPresentEntryLikeAFullReport drives two real gate
// rounds: round 1 reports a fix finding, its fix slice goes green, and
// round 2 confirms the finding by id and a new line in still_present. The
// fold must carry it exactly as a full re-report would: the same id, its
// earlier text, the new line, one recurrence, still open, so a new fix slice
// is queued. The reviewer's raw result.json keeps the short form.
func TestGateFoldsAStillPresentEntryLikeAFullReport(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	round := 0
	var round2Result string
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}
		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "the first round's detail", Action: ActionFix, Risk: RiskMedium,
					RiskRationale: "the first round's rationale", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
			}
		case 2:
			round2Result = sd.ResultJSON
			result = ReviewResult{
				StillPresent:  []StillPresentEntry{{Prior: "r1-f1", Line: 7}},
				ReviewedPaths: req.MustReview,
			}
		default:
			t.Fatalf("unexpected round %d", round)
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := NewReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, NoDemo: true})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want exactly one", report1.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report1.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 1 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report1.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 1 fix slice state: %v", err)
	}

	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, NoDemo: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(report2.FixSlices) != 1 {
		t.Fatalf("round 2 FixSlices = %v, want one: the confirmed fix finding is still open", report2.FixSlices)
	}
	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("read round 2 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff2.Findings) != 1 {
		t.Fatalf("round 2 findings = %+v, want exactly r1-f1", ff2.Findings)
	}
	f := ff2.Findings[0]
	if f.ID != "r1-f1" || f.Status != StatusOpen || f.Recurrences != 1 || f.Line != 7 ||
		f.File != "alpha/alpha.go" || f.Title != "needs a fix" || f.Detail != "the first round's detail" ||
		f.Action != ActionFix || f.Risk != RiskMedium || f.RiskRationale != "the first round's rationale" || f.Oracle != "test" {
		t.Errorf("round 2 r1-f1 = %+v, want the round-1 finding at line 7, open, 1 recurrence", f)
	}

	raw, err := os.ReadFile(round2Result)
	if err != nil {
		t.Fatalf("read round 2 result.json: %v", err)
	}
	onDisk, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("parse round 2 result.json: %v", err)
	}
	if len(onDisk.Findings) != 0 || len(onDisk.StillPresent) != 1 {
		t.Errorf("round 2 result.json = %+v, want the reviewer's short form as written", onDisk)
	}
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
