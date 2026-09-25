package revieweval

import (
	"encoding/json"
	"fmt"
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

// TestScorePriorCitationCounting uses two correctly-cited gold findings and
// one wrongly-cited one (want PriorCited=2): with only one of each (as an
// earlier version of this test had it), negating the equality gate
// (f.Prior == g.Prior) would lose the one correct match but gain the one
// wrong one, leaving PriorCited unchanged at 1 and the mutation undetected.
// Two correct against one wrong breaks that symmetry.
func TestScorePriorCitationCounting(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{
		{ID: "g1", Prior: "r1-f1", Action: verifydeliver.ActionFix}, // cited correctly
		{ID: "g2", Prior: "r1-f2", Action: verifydeliver.ActionFix}, // cited correctly
		{ID: "g3", Prior: "r1-f3", Action: verifydeliver.ActionFix}, // not cited
	}}
	findings := []verifydeliver.ResultFinding{
		{Title: "t1", Prior: "r1-f1"},
		{Title: "t2", Prior: "r1-f2"},
		{Title: "t3", Prior: "r1-f9"}, // wrongly cites something else
	}
	reported := []verifydeliver.Finding{
		{ID: "r1-f1", Status: verifydeliver.StatusOpen},
		{ID: "r1-f2", Status: verifydeliver.StatusOpen},
		{ID: "r1-f9", Status: verifydeliver.StatusOpen},
	}
	match := RoundMatch{MatchedFinding: []int{0, 1, 2}, StructureOnly: []bool{false, false, false}, Classification: map[int]Fate{}}

	sc := ScoreRound(2, gold, nil, findings, reported, nil, match)
	if sc.PriorExpected != 3 {
		t.Errorf("PriorExpected = %d, want 3 (g1, g2 and g3 all have a prior)", sc.PriorExpected)
	}
	if sc.PriorCited != 2 {
		t.Errorf("PriorCited = %d, want 2 (g1 and g2's findings cited it, g3's finding cited something else)", sc.PriorCited)
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

// TestScoreWrongPriorFailsTheRound pins ScoreRound's own wiring of
// FateWrongPrior into RoundScore: a finding classified with it lands in
// WrongPriors by title and fails the round, the same way a false alarm or
// a re-litigation would.
func TestScoreWrongPriorFailsTheRound(t *testing.T) {
	findings := []verifydeliver.ResultFinding{{Title: "cites the wrong dismissal"}}
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusDismissed}}
	match := RoundMatch{Classification: map[int]Fate{0: FateWrongPrior}}

	sc := ScoreRound(2, Gold{}, nil, findings, reported, nil, match)
	if len(sc.WrongPriors) != 1 || sc.WrongPriors[0] != "cites the wrong dismissal" {
		t.Errorf("WrongPriors = %v, want [\"cites the wrong dismissal\"]", sc.WrongPriors)
	}
	if sc.Passed {
		t.Error("Passed = true, want false: a wrong prior must fail the round")
	}
	if sc.Verdict != VerdictFail {
		t.Errorf("Verdict = %q, want %q", sc.Verdict, VerdictFail)
	}
}

// TestScoreRoundIsProvisionalWithUnlabeledNoise pins the three-valued
// verdict's middle state: a round whose one seeded gold is matched, plus a
// pile of findings nothing classified as a failure, is not a plain pass -
// nothing failed, but nobody has said the extra findings are right or
// wrong, so a person still has to look. This is the shape a reviewer that
// spams noise alongside a real finding produces: every extra finding lands
// in Pending (no structural edge to anything, a non-exhaustive round), and
// Passed alone could not tell that shotgun apart from a clean round.
func TestScoreRoundIsProvisionalWithUnlabeledNoise(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", Action: verifydeliver.ActionFix}}}
	const junk = 18
	findings := make([]verifydeliver.ResultFinding, junk+1)
	reported := make([]verifydeliver.Finding, junk+1)
	classification := map[int]Fate{}
	for i := range findings {
		findings[i] = verifydeliver.ResultFinding{Title: fmt.Sprintf("f%d", i)}
		reported[i] = verifydeliver.Finding{ID: fmt.Sprintf("r%d", i), Status: verifydeliver.StatusOpen}
		if i > 0 {
			classification[i] = FatePending
		}
	}
	match := RoundMatch{MatchedFinding: []int{0}, StructureOnly: []bool{false}, Classification: classification}

	sc := ScoreRound(1, gold, nil, findings, reported, nil, match)
	if !sc.Passed {
		t.Errorf("Passed = false, want true: nothing in this round failed")
	}
	if sc.Verdict != VerdictProvisional {
		t.Errorf("Verdict = %q, want %q: %d findings are still unlabeled", sc.Verdict, VerdictProvisional, junk)
	}
	if len(sc.Pending) != junk {
		t.Fatalf("Pending = %d, want %d", len(sc.Pending), junk)
	}
}

// TestScoreRoundVerdictThreeValued pins verdictFor's own three cases
// directly, isolated from ScoreRound's own field wiring.
func TestScoreRoundVerdictThreeValued(t *testing.T) {
	for _, tc := range []struct {
		name    string
		passed  bool
		pending int
		want    RoundVerdict
	}{
		{"failed-beats-pending", false, 3, VerdictFail},
		{"no-failure-no-pending-is-a-clean-pass", true, 0, VerdictPass},
		{"no-failure-with-pending-is-provisional", true, 1, VerdictProvisional},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdictFor(tc.passed, tc.pending); got != tc.want {
				t.Errorf("verdictFor(%v, %d) = %q, want %q", tc.passed, tc.pending, got, tc.want)
			}
		})
	}
}

// TestRenderReportRecallNAWithZeroSeededGold: a corpus round with
// no seeded gold at all (an exhaustive round with nothing to find, say)
// must not render "recall 0.00" - there was nothing to recall, n/a, the
// same way the other rates already read n/a with a zero denominator.
func TestRenderReportRecallNAWithZeroSeededGold(t *testing.T) {
	scores := []CaseScore{{Name: "c1", Passed: true, Rounds: []RoundScore{
		{Round: 1, Passed: true},
	}}}
	report := RenderReport(scores)
	if !strings.Contains(report, "recall n/a") {
		t.Errorf("report = %q, want \"recall n/a\" (found=0, gold=0)", report)
	}
	if strings.Contains(report, "recall 0.") {
		t.Errorf("report = %q, want no numeric recall", report)
	}
}

func TestRenderReportPrecisionNAWithZeroDenominator(t *testing.T) {
	// FalsePositiveGold is true (a trap or exhaustive round could have
	// produced a false alarm) but nothing was found, extra-true or a false
	// alarm: 0/0 must read n/a, not a misleading 0.00.
	scores := []CaseScore{{Name: "c1", Rounds: []RoundScore{
		{Round: 1, Missed: []string{"g1"}, FalsePositiveGold: true},
	}}}
	report := RenderReport(scores)
	if !strings.Contains(report, "precision n/a") {
		t.Errorf("report = %q, want \"precision n/a\" (found=0, extra-true=0, false alarms=0)", report)
	}
	if strings.Contains(report, "precision 0.") {
		t.Errorf("report = %q, want no numeric precision line", report)
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
