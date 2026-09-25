package revieweval

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// openFinding is a small ResultFinding/Finding pair builder for match
// tests: the reported fate defaults to open, which is enough for every
// test below that does not care about classification.
func openFinding(file string, line int, title string) (verifydeliver.ResultFinding, verifydeliver.Finding) {
	rf := verifydeliver.ResultFinding{File: file, Line: line, Title: title, Detail: "", Action: verifydeliver.ActionFix, Risk: verifydeliver.RiskLow, RiskRationale: "r"}
	f := verifydeliver.Finding{ID: "id", File: file, Line: line, Title: title, Status: verifydeliver.StatusOpen}
	return rf, f
}

func TestMatchRejectsTheSameWordingInAnotherFile(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "nil deref"}}}
	rf, f := openFinding("b.go", 10, "nil deref")

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.MatchedFinding[0] != -1 {
		t.Errorf("MatchedFinding[0] = %d, want -1: same wording in a different file must not match", m.MatchedFinding[0])
	}
}

func TestMatchWindowEdgeThreeLinesOutMatchesFourDoesNot(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d"}}}

	for _, tc := range []struct {
		name string
		line int
		want bool
	}{
		{"3-out-below", 7, true},
		{"3-out-above", 13, true},
		{"4-out-below", 6, false},
		{"4-out-above", 14, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rf, f := openFinding("a.go", tc.line, "d")
			m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
			if err != nil {
				t.Fatalf("MatchRound: %v", err)
			}
			got := m.MatchedFinding[0] != -1
			if got != tc.want {
				t.Errorf("line %d matched = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestMatchLineZeroSurvivesOnlyWithJudgeSame(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d"}}}
	rf, f := openFinding("a.go", 0, "d")

	t.Run("nil-judge-undecided-does-not-survive", func(t *testing.T) {
		m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
		if err != nil {
			t.Fatalf("MatchRound: %v", err)
		}
		if m.MatchedFinding[0] != -1 {
			t.Errorf("MatchedFinding[0] = %d, want -1: line 0 with no judge confirmation must not match", m.MatchedFinding[0])
		}
	})
	t.Run("judge-same-survives", func(t *testing.T) {
		judge := scriptedJudge{verdicts: []Verdict{Same}}
		m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
		if err != nil {
			t.Fatalf("MatchRound: %v", err)
		}
		if m.MatchedFinding[0] != 0 {
			t.Errorf("MatchedFinding[0] = %d, want 0: line 0 confirmed Same must match", m.MatchedFinding[0])
		}
	})
}

func TestMatchDifferentVerdictVetoesAStructuralEdge(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d"}}}
	rf, f := openFinding("a.go", 10, "d") // a perfect structural match otherwise

	judge := scriptedJudge{verdicts: []Verdict{Different}}
	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.MatchedFinding[0] != -1 {
		t.Errorf("MatchedFinding[0] = %d, want -1: a Different verdict vetoes even a same-line edge", m.MatchedFinding[0])
	}
}

func TestMatchOneToOneOneLumpedFindingSatisfiesOnlyOneGold(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{
		{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d1"},
		{ID: "g2", File: "a.go", From: 12, To: 12, Action: verifydeliver.ActionFix, Description: "d2"},
	}}
	// line 11 sits in both g1's window [7,13] and g2's window [9,15]: one
	// lumped finding structurally compatible with both.
	rf, f := openFinding("a.go", 11, "both at once")

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	matched := 0
	for _, j := range m.MatchedFinding {
		if j != -1 {
			matched++
		}
	}
	if matched != 1 {
		t.Errorf("matched %d gold entries, want exactly 1: one finding cannot satisfy two gold entries", matched)
	}
}

func TestMatchAugmentingPathFindsTheMaximumWhereGreedyWouldMiss(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{
		{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d1"}, // window [7,13]
		{ID: "g2", File: "a.go", From: 12, To: 12, Action: verifydeliver.ActionFix, Description: "d2"}, // window [9,15]
	}}
	// finding 0 (line 12) is compatible with BOTH g1 and g2; finding 1
	// (line 8) is compatible with g1 only. A first-fit greedy processing
	// finding 0 first claims g1 for it and leaves finding 1 (g1-only)
	// unmatched; Kuhn's augmenting path instead reassigns finding 0 to g2,
	// freeing g1 for finding 1, so both gold entries are found.
	rf0, f0 := openFinding("a.go", 12, "dual")
	rf1, f1 := openFinding("a.go", 8, "g1-only")

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil,
		[]verifydeliver.ResultFinding{rf0, rf1}, []verifydeliver.Finding{f0, f1}, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.MatchedFinding[0] == -1 || m.MatchedFinding[1] == -1 {
		t.Fatalf("MatchedFinding = %v, want both gold entries matched", m.MatchedFinding)
	}
	if m.MatchedFinding[0] == m.MatchedFinding[1] {
		t.Fatalf("MatchedFinding = %v, want g1 and g2 matched to different findings", m.MatchedFinding)
	}
}

// --- classification (classifyUnmatched's rules, in order) -------------

func TestClassifyUnmatchedRulesInOrder(t *testing.T) {
	const (
		open   = verifydeliver.StatusOpen
		dismis = verifydeliver.StatusDismissed
		noted  = verifydeliver.StatusNoted
	)
	cases := []struct {
		name                                                                                          string
		status                                                                                        string
		citedDismissedEdge, dismissedFoldEdge, trapEdge, decisionDismissedEdge, decisionKeptEdge, exh bool
		want                                                                                          Fate
	}{
		{"rule1-dismissed-status-with-cited-edge-is-a-permitted-repeat", dismis, true, true, false, false, false, false, FateSkip},
		{"rule1-wins-even-with-a-trap-edge", dismis, true, true, true, false, false, false, FateSkip},
		{"rule1-dismissed-status-without-cited-edge-is-a-wrong-prior", dismis, false, false, false, false, false, false, FateWrongPrior},
		{"rule1-wrong-prior-even-with-a-general-dismissed-edge", dismis, false, true, false, false, false, false, FateWrongPrior},
		{"rule1-wrong-prior-wins-over-a-trap-edge", dismis, false, false, true, false, false, false, FateWrongPrior},
		{"rule2-noted-status-is-pending", noted, false, false, false, false, false, false, FatePending},
		{"rule2-wins-over-a-trap-edge", noted, false, false, true, false, false, false, FatePending},
		{"rule3-dismissed-fold-edge-is-relitigated", open, false, true, false, false, false, false, FateRelitigated},
		{"rule3-wins-over-exhaustive", open, false, true, false, false, false, true, FateRelitigated},
		{"rule4-trap-edge-is-a-false-alarm", open, false, false, true, false, false, false, FateFalseAlarm},
		{"rule4-dismissed-decision-edge-is-a-false-alarm", open, false, false, false, true, false, false, FateFalseAlarm},
		{"rule5-kept-decision-edge-is-extra-true", open, false, false, false, false, true, false, FateExtraTrue},
		{"rule5-wins-over-exhaustive", open, false, false, false, false, true, true, FateExtraTrue},
		{"rule6-exhaustive-with-no-edge-is-a-false-alarm", open, false, false, false, false, false, true, FateFalseAlarm},
		{"rule7-non-exhaustive-with-no-edge-is-pending", open, false, false, false, false, false, false, FatePending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyUnmatched(c.status, c.citedDismissedEdge, c.dismissedFoldEdge, c.trapEdge, c.decisionDismissedEdge, c.decisionKeptEdge, c.exh)
			if got != c.want {
				t.Errorf("classifyUnmatched(...) = %q, want %q", got, c.want)
			}
		})
	}
}

// --- a recorded decision locates the finding it decided --------------------

// capturingJudge records the last JudgeQuery it was asked to confirm, so a
// test can inspect the Point a dismissed-fold candidate was actually built
// with, and answers every candidate Undecided (which survives unless the
// candidate is line-0), so it never itself changes which edges match.
type capturingJudge struct {
	got JudgeQuery
}

func (j *capturingJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	j.got = q
	return make([]Verdict, len(q.Candidates)), nil
}

func TestMatchUnlinkedDismissedDescriptionIsTitleAndDetailTogether(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "a.go", Line: 5, Title: "Loop rebuilds string", Detail: "Highest iterates and concatenates.", Status: verifydeliver.StatusDismissed}}
	rf, f := openFinding("a.go", 5, "same spot again")

	judge := &capturingJudge{}
	_, err := MatchRound("case", 1, "", "", Gold{}, nil, nil, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if len(judge.got.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(judge.got.Candidates))
	}
	p := judge.got.Candidates[0].Point
	if !strings.Contains(p.Description, "Loop rebuilds string") || !strings.Contains(p.Description, "Highest iterates and concatenates.") {
		t.Errorf("description = %q, want both the recorded title and detail (unlinked dismissed finding)", p.Description)
	}
	if p.From != 5 || p.To != 5 {
		t.Errorf("span = [%d,%d], want [5,5]: an unlinked dismissed finding keeps its one-line span", p.From, p.To)
	}
}

func TestMatchLinkedDismissedSpanIsUnionAndUsesDecisionDescription(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "a.go", Line: 5, Title: "t", Detail: "d", Status: verifydeliver.StatusDismissed}}
	links := map[string]Decision{
		"r1-f1": {ID: "dec1", File: "a.go", From: 8, To: 9, Description: "the whole loop, a defensible style choice", Decision: DecisionDismissed, Recorded: "r1-f1"},
	}

	judge := &capturingJudge{}
	rf, f := openFinding("a.go", 9, "re-raise")
	_, err := MatchRound("case", 2, "", "", Gold{}, nil, links, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if len(judge.got.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1: line 9 is only reachable via the recorded link's widened span", len(judge.got.Candidates))
	}
	p := judge.got.Candidates[0].Point
	if p.From != 5 || p.To != 9 {
		t.Errorf("span = [%d,%d], want [5,9]: the union of the fold finding's line and the decision's own span", p.From, p.To)
	}
	if p.Description != "the whole loop, a defensible style choice" {
		t.Errorf("description = %q, want the linked decision's own description", p.Description)
	}

	m, err := MatchRound("case", 2, "", "", Gold{}, nil, links, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.Classification[0] != FateRelitigated {
		t.Errorf("Classification[0] = %q, want %q: line 9 sits inside the union span widened by the window, only reachable via the recorded link", m.Classification[0], FateRelitigated)
	}
}

// --- prefer the best-supported pairing among maximum matchings ------------

// TestMatchPrefersBestSupportedEdgeRegardlessOfResultOrder pins a single
// gold finding (fix, span 17-18) with two structurally compatible
// candidates: a note at line 15 (outside the span itself, and its status
// does not agree with the gold's fix action) and a fix at line 17 (inside
// the span itself, status agrees). Whichever order the reviewer's result
// lists them in, the better-supported edge - the fix at 17 - must be the
// one matched.
func TestMatchPrefersBestSupportedEdgeRegardlessOfResultOrder(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 17, To: 18, Action: verifydeliver.ActionFix, Description: "d"}}}
	note := verifydeliver.ResultFinding{File: "a.go", Line: 15, Title: "note-at-15", Action: verifydeliver.ActionNote}
	fix := verifydeliver.ResultFinding{File: "a.go", Line: 17, Title: "fix-at-17", Action: verifydeliver.ActionFix}

	t.Run("note-first", func(t *testing.T) {
		findings := []verifydeliver.ResultFinding{note, fix}
		reported := []verifydeliver.Finding{
			{ID: "r1-f1", Status: verifydeliver.StatusNoted},
			{ID: "r1-f2", Status: verifydeliver.StatusOpen},
		}
		m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, findings, reported, nil)
		if err != nil {
			t.Fatalf("MatchRound: %v", err)
		}
		if m.MatchedFinding[0] != 1 {
			t.Errorf("MatchedFinding[0] = %d, want 1 (the fix at line 17: inside the span and status-agreeing)", m.MatchedFinding[0])
		}
	})
	t.Run("fix-first", func(t *testing.T) {
		findings := []verifydeliver.ResultFinding{fix, note}
		reported := []verifydeliver.Finding{
			{ID: "r1-f1", Status: verifydeliver.StatusOpen},
			{ID: "r1-f2", Status: verifydeliver.StatusNoted},
		}
		m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, findings, reported, nil)
		if err != nil {
			t.Fatalf("MatchRound: %v", err)
		}
		if m.MatchedFinding[0] != 0 {
			t.Errorf("MatchedFinding[0] = %d, want 0 (the fix at line 17: inside the span and status-agreeing)", m.MatchedFinding[0])
		}
	})
}

// --- rule 1 only forgives a repeat that is one ------------------------------

// TestApplyRoundThenMatchWrongPriorFailsTheRound goes through the real
// verifydeliver.ApplyRound, not a hand-built reported[] slice: an
// exhaustive round with a trap and a dismissed fold finding in an unrelated
// file; the round's one finding sits on the trap and cites the dismissed
// finding's id as its prior. ApplyRound (rule 2) still marks it dismissed
// on that citation alone, but MatchRound must not treat it as a permitted
// repeat, since it carries no structural edge to the point that id names -
// the citation does not survive contact with where the finding actually is.
func TestApplyRoundThenMatchWrongPriorFailsTheRound(t *testing.T) {
	known := map[string]verifydeliver.Finding{
		"r1-f1": {ID: "r1-f1", File: "other.go", Line: 5, Title: "dismissed elsewhere", Status: verifydeliver.StatusDismissed},
	}
	man := manifest.Manifest{
		Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
		Oracles:    map[string]string{"test": "go test ./..."},
	}
	result := verifydeliver.ReviewResult{Findings: []verifydeliver.ResultFinding{
		{File: "a.go", Line: 10, Title: "trap flagged", Detail: "d", Action: verifydeliver.ActionFix, Risk: verifydeliver.RiskLow, RiskRationale: "r", Prior: "r1-f1"},
	}}
	reported, err := verifydeliver.ApplyRound(2, known, result, nil, func(string) (bool, error) { return false, nil }, man)
	if err != nil {
		t.Fatalf("ApplyRound: %v", err)
	}
	if len(reported) != 1 || reported[0].Status != verifydeliver.StatusDismissed {
		t.Fatalf("ApplyRound reported = %+v, want one dismissed finding (rule 2, citing an already-dismissed prior)", reported)
	}

	gold := Gold{Exhaustive: true, Traps: []GoldTrap{{ID: "trap1", File: "a.go", From: 10, To: 10, Description: "correct code near the finding"}}}
	dismissed := []verifydeliver.Finding{known["r1-f1"]}

	m, err := MatchRound("case", 2, "", "", gold, nil, nil, dismissed, result.Findings, reported, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.Classification[0] != FateWrongPrior {
		t.Errorf("Classification[0] = %q, want %q", m.Classification[0], FateWrongPrior)
	}

	sc := ScoreRound(2, gold, nil, result.Findings, reported, nil, m)
	if sc.Passed {
		t.Error("Passed = true, want false: a wrong prior must fail the round")
	}
	if len(sc.WrongPriors) != 1 || sc.WrongPriors[0] != "trap flagged" {
		t.Errorf("WrongPriors = %v, want [\"trap flagged\"]", sc.WrongPriors)
	}
	if len(sc.FalseAlarms) != 0 {
		t.Errorf("FalseAlarms = %v, want none: it is scored as a wrong prior, not a false alarm", sc.FalseAlarms)
	}
}
