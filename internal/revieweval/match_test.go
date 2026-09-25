package revieweval

import (
	"testing"

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

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
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
			m, err := MatchRound("case", 1, "", "", gold, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
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
		m, err := MatchRound("case", 1, "", "", gold, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
		if err != nil {
			t.Fatalf("MatchRound: %v", err)
		}
		if m.MatchedFinding[0] != -1 {
			t.Errorf("MatchedFinding[0] = %d, want -1: line 0 with no judge confirmation must not match", m.MatchedFinding[0])
		}
	})
	t.Run("judge-same-survives", func(t *testing.T) {
		judge := scriptedJudge{verdicts: []Verdict{Same}}
		m, err := MatchRound("case", 1, "", "", gold, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
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
	m, err := MatchRound("case", 1, "", "", gold, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
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

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, nil)
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

	m, err := MatchRound("case", 1, "", "", gold, nil, nil,
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

// --- classification (classifyUnmatched's seven rules, in order) -------------

func TestClassifyUnmatchedSevenRulesInOrder(t *testing.T) {
	const (
		open   = verifydeliver.StatusOpen
		dismis = verifydeliver.StatusDismissed
		noted  = verifydeliver.StatusNoted
	)
	cases := []struct {
		name                                                                      string
		status                                                                    string
		dismissedFoldEdge, trapEdge, decisionDismissedEdge, decisionKeptEdge, exh bool
		want                                                                      Fate
	}{
		{"rule1-dismissed-status-is-a-permitted-repeat", dismis, false, false, false, false, false, FateSkip},
		{"rule1-wins-even-with-a-trap-edge", dismis, false, true, false, false, false, FateSkip},
		{"rule2-noted-status-is-pending", noted, false, false, false, false, false, FatePending},
		{"rule2-wins-over-a-trap-edge", noted, false, true, false, false, false, FatePending},
		{"rule3-dismissed-fold-edge-is-relitigated", open, true, false, false, false, false, FateRelitigated},
		{"rule3-wins-over-exhaustive", open, true, false, false, false, true, FateRelitigated},
		{"rule4-trap-edge-is-a-false-alarm", open, false, true, false, false, false, FateFalseAlarm},
		{"rule4-dismissed-decision-edge-is-a-false-alarm", open, false, false, true, false, false, FateFalseAlarm},
		{"rule5-kept-decision-edge-is-extra-true", open, false, false, false, true, false, FateExtraTrue},
		{"rule5-wins-over-exhaustive", open, false, false, false, true, true, FateExtraTrue},
		{"rule6-exhaustive-with-no-edge-is-a-false-alarm", open, false, false, false, false, true, FateFalseAlarm},
		{"rule7-non-exhaustive-with-no-edge-is-pending", open, false, false, false, false, false, FatePending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyUnmatched(c.status, c.dismissedFoldEdge, c.trapEdge, c.decisionDismissedEdge, c.decisionKeptEdge, c.exh)
			if got != c.want {
				t.Errorf("classifyUnmatched(...) = %q, want %q", got, c.want)
			}
		})
	}
}
