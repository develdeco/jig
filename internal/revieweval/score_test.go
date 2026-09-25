package revieweval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

func TestScoreLostWhenAFixOrAskMatchEndsNotedOrDismissed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		status string
		lost   bool
	}{
		{"fix-matched-open-not-lost", verifydeliver.ActionFix, verifydeliver.StatusOpen, false},
		{"fix-matched-noted-is-lost", verifydeliver.ActionFix, verifydeliver.StatusNoted, true},
		{"fix-matched-dismissed-is-lost", verifydeliver.ActionFix, verifydeliver.StatusDismissed, true},
		{"ask-matched-asked-not-lost", verifydeliver.ActionAsk, verifydeliver.StatusAsked, false},
		{"ask-matched-dismissed-is-lost", verifydeliver.ActionAsk, verifydeliver.StatusDismissed, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gold := Gold{Findings: []GoldFinding{{ID: "g1", Action: tc.action}}}
			findings := []verifydeliver.ResultFinding{{Title: "t1"}}
			reported := []verifydeliver.Finding{{ID: "r1-f1", Status: tc.status}}
			match := RoundMatch{MatchedFinding: []int{0}, StructureOnly: []bool{false}, Classification: map[int]Fate{}}

			sc := ScoreRound(1, gold, nil, findings, reported, nil, match)
			got := len(sc.Lost) == 1 && sc.Lost[0] == "g1"
			if got != tc.lost {
				t.Errorf("Lost = %v, want lost=%v", sc.Lost, tc.lost)
			}
		})
	}
}

func TestScoreForgottenRequiresThePriorToBeCleared(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g-b", Prior: "r1-f2", Action: verifydeliver.ActionFix}}}
	match := RoundMatch{MatchedFinding: []int{-1}, StructureOnly: []bool{false}, Classification: map[int]Fate{}}

	t.Run("prior-cleared-is-forgotten", func(t *testing.T) {
		sc := ScoreRound(2, gold, nil, nil, nil, []string{"r1-f2"}, match)
		if len(sc.Missed) != 1 || sc.Missed[0] != "g-b" {
			t.Errorf("Missed = %v, want [g-b]", sc.Missed)
		}
		if len(sc.Forgotten) != 1 || sc.Forgotten[0] != "g-b" {
			t.Errorf("Forgotten = %v, want [g-b]: its prior was cleared", sc.Forgotten)
		}
	})
	t.Run("prior-not-cleared-is-a-plain-miss-not-forgotten", func(t *testing.T) {
		// A finding blocked from clearing by another open finding in its
		// file (or simply never cleared) is missed, not forgotten.
		sc := ScoreRound(2, gold, nil, nil, nil, nil, match)
		if len(sc.Missed) != 1 || sc.Missed[0] != "g-b" {
			t.Errorf("Missed = %v, want [g-b]", sc.Missed)
		}
		if len(sc.Forgotten) != 0 {
			t.Errorf("Forgotten = %v, want none: the prior was never cleared", sc.Forgotten)
		}
	})
}

func TestScoreDroppedQuestionWhenAnAskMatchIsNotAsked(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", Action: verifydeliver.ActionAsk}}}
	findings := []verifydeliver.ResultFinding{{Title: "t1"}}
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}} // routed to a fix, not asked
	match := RoundMatch{MatchedFinding: []int{0}, StructureOnly: []bool{false}, Classification: map[int]Fate{}}

	sc := ScoreRound(1, gold, nil, findings, reported, nil, match)
	if len(sc.DroppedQuestions) != 1 || sc.DroppedQuestions[0] != "g1" {
		t.Errorf("DroppedQuestions = %v, want [g1]", sc.DroppedQuestions)
	}
	if len(sc.Lost) != 0 {
		t.Errorf("Lost = %v, want none: open is not noted or dismissed", sc.Lost)
	}
}

func TestScoreMisattributedWhenTheFindingsPriorDiffersFromGold(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", Prior: "r1-f2", Action: verifydeliver.ActionFix}}}
	findings := []verifydeliver.ResultFinding{{Title: "t1", Prior: "r1-f9"}}
	reported := []verifydeliver.Finding{{ID: "r1-f9", Status: verifydeliver.StatusOpen}}
	match := RoundMatch{MatchedFinding: []int{0}, StructureOnly: []bool{false}, Classification: map[int]Fate{}}

	sc := ScoreRound(1, gold, nil, findings, reported, nil, match)
	if len(sc.Misattributed) != 1 || sc.Misattributed[0] != "g1" {
		t.Errorf("Misattributed = %v, want [g1]: r1-f9 != gold's prior r1-f2", sc.Misattributed)
	}
}

func TestScorePriorCitationCounting(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{
		{ID: "g1", Prior: "r1-f1", Action: verifydeliver.ActionFix}, // cited correctly
		{ID: "g2", Prior: "r1-f2", Action: verifydeliver.ActionFix}, // not cited
		{ID: "g3", Action: verifydeliver.ActionFix},                 // no prior expected
	}}
	findings := []verifydeliver.ResultFinding{
		{Title: "t1", Prior: "r1-f1"},
		{Title: "t2", Prior: ""},
		{Title: "t3"},
	}
	reported := []verifydeliver.Finding{
		{ID: "r1-f1", Status: verifydeliver.StatusOpen},
		{ID: "r2-f1", Status: verifydeliver.StatusOpen},
		{ID: "r2-f2", Status: verifydeliver.StatusOpen},
	}
	match := RoundMatch{MatchedFinding: []int{0, 1, 2}, StructureOnly: []bool{false, false, false}, Classification: map[int]Fate{}}

	sc := ScoreRound(2, gold, nil, findings, reported, nil, match)
	if sc.PriorExpected != 2 {
		t.Errorf("PriorExpected = %d, want 2 (g1 and g2 have a prior, g3 does not)", sc.PriorExpected)
	}
	if sc.PriorCited != 1 {
		t.Errorf("PriorCited = %d, want 1 (only g1's finding cited it)", sc.PriorCited)
	}
}

func TestScoreTriagePromptsOneForTheFixBatchPlusOnePerAsk(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		want     int
	}{
		{"no-open-no-asked", []string{verifydeliver.StatusNoted, verifydeliver.StatusDismissed}, 0},
		{"one-asked-no-batch", []string{verifydeliver.StatusAsked}, 1},
		{"open-and-two-asked", []string{verifydeliver.StatusOpen, verifydeliver.StatusOpen, verifydeliver.StatusAsked, verifydeliver.StatusAsked, verifydeliver.StatusNoted}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reported []verifydeliver.Finding
			for i, s := range tc.statuses {
				reported = append(reported, verifydeliver.Finding{ID: string(rune('a' + i)), Status: s})
			}
			findings := make([]verifydeliver.ResultFinding, len(reported))
			match := RoundMatch{Classification: map[int]Fate{}}
			for i := range findings {
				match.Classification[i] = FatePending
			}
			sc := ScoreRound(1, Gold{}, nil, findings, reported, nil, match)
			if sc.TriagePrompts != tc.want {
				t.Errorf("TriagePrompts = %d, want %d", sc.TriagePrompts, tc.want)
			}
		})
	}
}

func TestRenderReportWithholdsPrecisionWithoutFalsePositiveGold(t *testing.T) {
	scores := []CaseScore{{Name: "c1", Passed: true, Rounds: []RoundScore{
		{Round: 1, Found: []string{"g1"}, Passed: true, FalsePositiveGold: false},
	}}}
	report := RenderReport(scores)
	if !strings.Contains(report, "precision withheld") {
		t.Errorf("report = %q, want it to withhold precision (no round has false-positive gold)", report)
	}
	if strings.Contains(report, "precision 0.") || strings.Contains(report, "precision 1.") {
		t.Errorf("report = %q, want no numeric precision line", report)
	}
}

func TestRenderReportComputesPrecisionWithFalsePositiveGold(t *testing.T) {
	scores := []CaseScore{{Name: "c1", Rounds: []RoundScore{
		{Round: 1, Found: []string{"g1"}, FalseAlarms: []string{"bad finding"}, FalsePositiveGold: true},
	}}}
	report := RenderReport(scores)
	if !strings.Contains(report, "precision 0.50") {
		t.Errorf("report = %q, want \"precision 0.50\" (1 found / (1 found + 1 false alarm))", report)
	}
}

func TestRenderJSONCarriesEachFindingForAPersonToLabel(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", Action: verifydeliver.ActionFix}}}
	findings := []verifydeliver.ResultFinding{
		{File: "a/a.go", Line: 4, Title: "seeded", Detail: "the seeded bug", Action: verifydeliver.ActionFix, Risk: verifydeliver.RiskHigh},
		{File: "b/b.go", Line: 9, Title: "extra", Detail: "something beyond the gold", Action: verifydeliver.ActionFix, Risk: verifydeliver.RiskLow},
	}
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}, {ID: "r1-f2", Status: verifydeliver.StatusOpen}}
	match := RoundMatch{MatchedFinding: []int{0}, StructureOnly: []bool{false}, Classification: map[int]Fate{1: FatePending}}

	sc := ScoreRound(1, gold, nil, findings, reported, nil, match)
	data, err := RenderJSON([]CaseScore{{Name: "c", Rounds: []RoundScore{sc}}})
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var out []struct {
		Rounds []struct {
			Findings []ScoredFinding `json:"findings"`
		} `json:"rounds"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse RenderJSON output: %v\n%s", err, data)
	}
	if len(out) != 1 || len(out[0].Rounds) != 1 || len(out[0].Rounds[0].Findings) != 2 {
		t.Fatalf("want one case, one round, two findings, got:\n%s", data)
	}
	got := out[0].Rounds[0].Findings
	if got[0].Gold != "g1" || got[0].Fate != "" {
		t.Errorf("matched finding: gold=%q fate=%q, want gold g1 and no fate", got[0].Gold, got[0].Fate)
	}
	want := ScoredFinding{File: "b/b.go", Line: 9, Title: "extra", Detail: "something beyond the gold", Action: verifydeliver.ActionFix, Risk: verifydeliver.RiskLow, Status: verifydeliver.StatusOpen, Fate: FatePending}
	if got[1] != want {
		t.Errorf("unmatched finding = %+v, want %+v: a person labels it from this record alone", got[1], want)
	}
}
