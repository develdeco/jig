package revieweval

import (
	"fmt"
	"math/rand"
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

// TestMatchFindsTheMaximumWhereGreedyWouldMiss pins cardinality first: a
// first-fit greedy pass over findings in result order would claim g1 for
// finding 0 (the first gold entry it is compatible with) and leave finding
// 1 (g1-only) unmatched. bestMatching finds the reassignment plain greedy
// cannot: finding 0 to g2, freeing g1 for finding 1, so both gold entries
// are found - the maximum cardinality, not merely a maximal one.
func TestMatchFindsTheMaximumWhereGreedyWouldMiss(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{
		{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d1"}, // window [7,13]
		{ID: "g2", File: "a.go", From: 12, To: 12, Action: verifydeliver.ActionFix, Description: "d2"}, // window [9,15]
	}}
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
	// The fold's own current line (5) intentionally matches what the
	// loader would have recorded, so this test alone cannot tell the two
	// apart - TestMatchLinkedDismissedUsesTheRecordedLineNotTheFoldsLatest
	// below pins that RecordedLine, not f.Line, is what the union actually
	// reads.
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "a.go", Line: 5, Title: "t", Detail: "d", Status: verifydeliver.StatusDismissed}}
	links := map[string]Decision{
		"r1-f1": {ID: "dec1", File: "a.go", From: 8, To: 9, RecordedLine: 5, Description: "the whole loop, a defensible style choice", Decision: DecisionDismissed, Recorded: "r1-f1"},
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

// TestMatchLinkedDismissedUsesTheRecordedLineNotTheFoldsLatest is
// TestMatchLinkedDismissedSpanIsUnionAndUsesDecisionDescription's other
// half: the fold's own current occurrence has since moved to a line the
// decision never saw (a later round re-reported the same id at a new
// line), so the union must read Decision.RecordedLine, not f.Line, or the
// span would silently drift with whatever the fold happens to say now.
func TestMatchLinkedDismissedUsesTheRecordedLineNotTheFoldsLatest(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "a.go", Line: 50, Title: "t", Detail: "d", Status: verifydeliver.StatusDismissed}}
	links := map[string]Decision{
		"r1-f1": {ID: "dec1", File: "a.go", From: 8, To: 9, RecordedLine: 5, Description: "d", Decision: DecisionDismissed, Recorded: "r1-f1"},
	}

	judge := &capturingJudge{}
	rf, f := openFinding("a.go", 9, "re-raise")
	_, err := MatchRound("case", 2, "", "", Gold{}, nil, links, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	p := judge.got.Candidates[0].Point
	if p.From != 5 || p.To != 9 {
		t.Errorf("span = [%d,%d], want [5,9]: the union of the decision's own span and RecordedLine (5), not the fold's current line (50)", p.From, p.To)
	}
}

// TestMatchLinkedDismissedSkipsTheLinkOnAFileMismatch covers the other
// guard on links: a fold record whose file no longer matches the decision's
// own file (a later round re-reported the same id under a different file,
// which the loader never saw and the decision never judged) is treated as
// an unlinked dismissed finding - its own bare one-line span and title -
// rather than trusting a link the loader could not have validated.
func TestMatchLinkedDismissedSkipsTheLinkOnAFileMismatch(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "b.go", Line: 5, Title: "moved", Detail: "d", Status: verifydeliver.StatusDismissed}}
	links := map[string]Decision{
		"r1-f1": {ID: "dec1", File: "a.go", From: 8, To: 9, RecordedLine: 5, Description: "the linked description", Decision: DecisionDismissed, Recorded: "r1-f1"},
	}

	judge := &capturingJudge{}
	rf, f := openFinding("b.go", 5, "re-raise")
	_, err := MatchRound("case", 2, "", "", Gold{}, nil, links, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{f}, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if len(judge.got.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(judge.got.Candidates))
	}
	p := judge.got.Candidates[0].Point
	if p.From != 5 || p.To != 5 {
		t.Errorf("span = [%d,%d], want [5,5]: the file mismatch must skip the link, keeping the fold record's own one-line span", p.From, p.To)
	}
	if p.Description != "moved: d" {
		t.Errorf("description = %q, want %q: the fold record's own title and detail, not the mismatched link's description", p.Description, "moved: d")
	}
}

// --- a cited prior is location evidence --------------------------------

// byPointJudge answers MatchRound's candidates by the point id they pair
// with, regardless of which finding or how many candidates carry that id:
// simpler than a plain scriptedJudge for a test that cares about a
// specific point's verdict, not the candidate array's own order.
type byPointJudge map[string]Verdict

func (j byPointJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	out := make([]Verdict, len(q.Candidates))
	for i, c := range q.Candidates {
		out[i] = j[c.Point.ID]
	}
	return out, nil
}

// TestMatchCitedPriorSurvivesOutsideTheWindow is the title-collision
// shape: a repeat citing its dismissed prior 5 lines off the recorded
// line - well past lineWindow (3) - still gets a candidate for that exact
// point, and a nil judge (Undecided) keeps it: the citation is itself
// location evidence, so it needs no explicit Same, only not Different.
func TestMatchCitedPriorSurvivesOutsideTheWindow(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "a.go", Line: 10, Title: "t", Detail: "d", Status: verifydeliver.StatusDismissed}}
	rf := verifydeliver.ResultFinding{File: "a.go", Line: 15, Title: "repeat", Prior: "r1-f1"} // 5 lines off r1-f1's own line
	reported := verifydeliver.Finding{ID: "r1-f1", Status: verifydeliver.StatusDismissed}      // ApplyRound's rule 2: id becomes the cited prior

	m, err := MatchRound("case", 2, "", "", Gold{}, nil, nil, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{reported}, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.Classification[0] != FateSkip {
		t.Errorf("Classification[0] = %q, want %q: a nil judge keeps a cited prior that structure alone (window) would have missed", m.Classification[0], FateSkip)
	}
}

// TestMatchCitedPriorSurvivesAtLineZero: a cited prior counts whatever
// the lines, line 0 included: a finding that gave no line at all still gets the special
// cited-prior candidate, and a nil judge (Undecided) still keeps it -
// unlike a plain line-0 structural candidate (TestMatchLineZeroSurvivesOnlyWithJudgeSame),
// which needs the judge's explicit Same, since here the citation is
// itself the location evidence.
func TestMatchCitedPriorSurvivesAtLineZero(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "a.go", Line: 10, Title: "t", Detail: "d", Status: verifydeliver.StatusDismissed}}
	rf := verifydeliver.ResultFinding{File: "a.go", Line: 0, Title: "repeat, no line", Prior: "r1-f1"}
	reported := verifydeliver.Finding{ID: "r1-f1", Status: verifydeliver.StatusDismissed}

	m, err := MatchRound("case", 2, "", "", Gold{}, nil, nil, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{reported}, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.Classification[0] != FateSkip {
		t.Errorf("Classification[0] = %q, want %q: a nil judge keeps a cited prior even at line 0", m.Classification[0], FateSkip)
	}
}

// TestMatchCitedPriorJudgeDifferentIsWrongPrior is the same shape as
// TestMatchCitedPriorSurvivesOutsideTheWindow, but the judge answers
// Different on the cited pair: the citation is rejected, so it is a wrong
// prior, not a permitted repeat.
func TestMatchCitedPriorJudgeDifferentIsWrongPrior(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "a.go", Line: 10, Title: "t", Detail: "d", Status: verifydeliver.StatusDismissed}}
	rf := verifydeliver.ResultFinding{File: "a.go", Line: 15, Title: "repeat", Prior: "r1-f1"}
	reported := verifydeliver.Finding{ID: "r1-f1", Status: verifydeliver.StatusDismissed}

	judge := byPointJudge{"r1-f1": Different}
	m, err := MatchRound("case", 2, "", "", Gold{}, nil, nil, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{reported}, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.Classification[0] != FateWrongPrior {
		t.Errorf("Classification[0] = %q, want %q: the judge rejected the cited pair", m.Classification[0], FateWrongPrior)
	}
}

// TestMatchCitedPriorInAnotherFileIsWrongPrior: a wrong prior is a cited
// dismissed finding in another file (or one the judge rejects): the citation names
// a real dismissed fold finding, but in a file this finding does not
// touch, so no candidate is built for it at all (same-file
// requirement) and no structural window reaches it either.
func TestMatchCitedPriorInAnotherFileIsWrongPrior(t *testing.T) {
	dismissed := []verifydeliver.Finding{{ID: "r1-f1", File: "other.go", Line: 10, Title: "t", Detail: "d", Status: verifydeliver.StatusDismissed}}
	rf := verifydeliver.ResultFinding{File: "a.go", Line: 10, Title: "repeat", Prior: "r1-f1"}
	reported := verifydeliver.Finding{ID: "r1-f1", Status: verifydeliver.StatusDismissed}

	m, err := MatchRound("case", 2, "", "", Gold{}, nil, nil, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{reported}, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.Classification[0] != FateWrongPrior {
		t.Errorf("Classification[0] = %q, want %q: r1-f1 is in another file, so citing it is no edge at all", m.Classification[0], FateWrongPrior)
	}
}

// TestMatchCitedPriorPinsIDEqualityAgainstAnotherDismissedPointInTheSameFile
// pins the id equality: a finding sits structurally on dismissed point X
// (a surviving edge exists to it) while its own Prior cites a different
// dismissed point Y in the same file; the judge answers Different only on
// Y. The general dismissed edge to X survives, but citedDismissedEdge must
// still require the exact point id the finding cited, not merely any
// surviving dismissed edge - so this stays a wrong prior even though a
// dismissed edge (to X) did survive.
func TestMatchCitedPriorPinsIDEqualityAgainstAnotherDismissedPointInTheSameFile(t *testing.T) {
	dismissed := []verifydeliver.Finding{
		{ID: "X", File: "a.go", Line: 5, Title: "x", Detail: "d", Status: verifydeliver.StatusDismissed},
		{ID: "Y", File: "a.go", Line: 20, Title: "y", Detail: "d", Status: verifydeliver.StatusDismissed},
	}
	rf := verifydeliver.ResultFinding{File: "a.go", Line: 5, Title: "sits on X, cites Y", Prior: "Y"}
	reported := verifydeliver.Finding{ID: "Y", Status: verifydeliver.StatusDismissed} // ApplyRound's rule 2: id becomes the cited prior, Y

	judge := byPointJudge{"Y": Different}
	m, err := MatchRound("case", 2, "", "", Gold{}, nil, nil, dismissed, []verifydeliver.ResultFinding{rf}, []verifydeliver.Finding{reported}, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.Classification[0] != FateWrongPrior {
		t.Errorf("Classification[0] = %q, want %q: citing Y, not X, is what must decide this, and the judge rejected Y", m.Classification[0], FateWrongPrior)
	}
}

// --- choose the pairing by evidence, exactly, never by outcome -------

// TestMatchScoreBetterEachCriterionAlone pins matchScore.better's own
// field order directly, one criterion at a time, everything else tied:
// cardinality, then same, then prior, then closeness, then earliness. A mutation that
// swapped two fields' priority, or compared a field with the wrong sign,
// fails exactly one of these.
func TestMatchScoreBetterEachCriterionAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b matchScore
		want bool
	}{
		{"more-matched-wins", matchScore{matched: 2}, matchScore{matched: 1, same: 9, prior: 9, closeness: 9}, true},
		{"same-count-breaks-a-matched-tie", matchScore{matched: 1, same: 1}, matchScore{matched: 1, same: 0, prior: 9, closeness: 9}, true},
		{"prior-count-breaks-a-same-tie", matchScore{matched: 1, same: 1, prior: 1}, matchScore{matched: 1, same: 1, prior: 0, closeness: 9}, true},
		{"closeness-breaks-a-prior-tie", matchScore{matched: 1, same: 1, prior: 1, closeness: 4}, matchScore{matched: 1, same: 1, prior: 1, closeness: 1}, true},
		{"early-breaks-a-closeness-tie", matchScore{matched: 1, same: 1, prior: 1, closeness: 4, early: 2}, matchScore{matched: 1, same: 1, prior: 1, closeness: 4, early: 1}, true},
		{"exact-tie-is-not-better", matchScore{matched: 1, same: 1, prior: 1, closeness: 4}, matchScore{matched: 1, same: 1, prior: 1, closeness: 4}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.better(tc.b); got != tc.want {
				t.Errorf("(%+v).better(%+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestMatchPrefersJudgeSameOverEverythingButCardinality isolates the same
// criterion: two candidates for one gold entry, tied on prior (both
// unset) and on closeness (symmetric, one line off on each side), only one
// confirmed Same by the judge. The judge's own confirmation must win.
func TestMatchPrefersJudgeSameOverEverythingButCardinality(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d"}}}
	above := verifydeliver.ResultFinding{File: "a.go", Line: 9, Title: "above"}  // 1 off
	below := verifydeliver.ResultFinding{File: "a.go", Line: 11, Title: "below"} // 1 off
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}, {ID: "r1-f2", Status: verifydeliver.StatusOpen}}

	judge := scriptedJudge{verdicts: []Verdict{Undecided, Same}} // candidates are built gold-then-in-file-order: above, below
	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{above, below}, reported, judge)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.MatchedFinding[0] != 1 {
		t.Errorf("MatchedFinding[0] = %d, want 1 (below, the one the judge confirmed Same)", m.MatchedFinding[0])
	}
}

// TestMatchPrefersPriorAgreementOverCloseness isolates the prior
// criterion: two candidates for one gold entry (prior r1-p), tied on same
// (neither confirmed) and NOT tied on closeness in the wrong direction -
// the one citing the gold's own prior sits further from the span than the
// one that does not, so only the prior criterion can explain a preference
// for it.
func TestMatchPrefersPriorAgreementOverCloseness(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Prior: "r1-p", Description: "d"}}}
	closer := verifydeliver.ResultFinding{File: "a.go", Line: 10, Title: "closer, no prior"}               // inside the span: closeness 4
	citing := verifydeliver.ResultFinding{File: "a.go", Line: 13, Title: "cites the prior", Prior: "r1-p"} // 3 off: closeness 1
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}, {ID: "r1-f2", Status: verifydeliver.StatusOpen}}

	m, err := MatchRound("case", 2, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{closer, citing}, reported, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.MatchedFinding[0] != 1 {
		t.Errorf("MatchedFinding[0] = %d, want 1 (the finding citing the gold's own prior, even though it sits further from the span)", m.MatchedFinding[0])
	}
}

// TestMatchPrefersClosenessWhenSameAndPriorAreTied isolates the closeness
// criterion, the last evidence criterion: two candidates, neither confirmed
// Same, neither with a prior - one inside the span, one 2 lines off. The
// closer one must win.
func TestMatchPrefersClosenessWhenSameAndPriorAreTied(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d"}}}
	inside := verifydeliver.ResultFinding{File: "a.go", Line: 10, Title: "inside"}
	off := verifydeliver.ResultFinding{File: "a.go", Line: 12, Title: "2-off"}
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}, {ID: "r1-f2", Status: verifydeliver.StatusOpen}}

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{off, inside}, reported, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.MatchedFinding[0] != 1 {
		t.Errorf("MatchedFinding[0] = %d, want 1 (inside the span, closer than 2 lines off)", m.MatchedFinding[0])
	}
}

// TestMatchNeverRanksByStatusOrAction pins the negative rule: a note
// sitting exactly inside the gold span beats a fix sitting outside it,
// even though the note's own status (noted) disagrees with the gold's fix
// action and the fix's (open) agrees. The two candidates' closeness scores
// are exactly one step apart (4 inside the span versus 3 one line out) -
// close enough that a one-point action-agreement bonus for the finding
// whose action matches the gold's would tie the two, and then flip the
// winner to the wrong one on the "early" tiebreak (fix dispatched first).
// Closeness alone decides it; status and action never enter the
// comparison.
func TestMatchNeverRanksByStatusOrAction(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d"}}}
	fix := verifydeliver.ResultFinding{File: "a.go", Line: 11, Title: "fix-1-off", Action: verifydeliver.ActionFix}     // closeness 3, gold's own action
	note := verifydeliver.ResultFinding{File: "a.go", Line: 10, Title: "note-inside", Action: verifydeliver.ActionNote} // closeness 4, mismatched action
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}, {ID: "r1-f2", Status: verifydeliver.StatusNoted}}

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, []verifydeliver.ResultFinding{fix, note}, reported, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	if m.MatchedFinding[0] != 1 {
		t.Errorf("MatchedFinding[0] = %d, want 1 (the note, one step closer): status/action must never break a real one-step closeness gap", m.MatchedFinding[0])
	}
}

// TestMatchRealBugAsANoteInsideSpanStaysLost is the reviewer's repro (b):
// the real bug reported as a note inside the span, plus a stray fix
// elsewhere in the window, stays Lost - the note (closer) is what gets
// matched, and ScoreRound marks a fix-action gold matched to a noted
// finding Lost, exactly as it should: a nearby fix on something else is
// not the same as actually fixing the seeded bug. The stray fix is
// reported first (the higher "early" field) and the note second (the
// lower one), although the note's closeness is the higher of the two: a
// mutant that let earliness outrank closeness, instead of only breaking a
// tie closeness itself leaves standing, would pick the stray fix instead.
func TestMatchRealBugAsANoteInsideSpanStaysLost(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "the real bug"}}}
	stray := verifydeliver.ResultFinding{File: "a.go", Line: 12, Title: "unrelated-stray-fix", Action: verifydeliver.ActionFix}
	note := verifydeliver.ResultFinding{File: "a.go", Line: 10, Title: "note-on-the-real-bug", Action: verifydeliver.ActionNote}
	findings := []verifydeliver.ResultFinding{stray, note}
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}, {ID: "r1-f2", Status: verifydeliver.StatusNoted}}

	m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, findings, reported, nil)
	if err != nil {
		t.Fatalf("MatchRound: %v", err)
	}
	sc := ScoreRound(1, gold, nil, findings, reported, nil, m)
	if len(sc.Lost) != 1 || sc.Lost[0] != "g1" {
		t.Errorf("Lost = %v, want [g1]: the note matched (closer to the span), and a fix gold matched to a noted finding is lost", sc.Lost)
	}
	if len(sc.Found) != 1 || sc.Found[0] != "g1" {
		t.Errorf("Found = %v, want [g1]: matched, just lost", sc.Found)
	}
}

// TestMatchCitedPriorsResolveWhichGoldRegardlessOfOrder is the reviewer's
// repro (a): two gold findings whose own priors are r1-f1 and r1-f2, and
// two findings each citing one of them, one line off its own gold's line -
// both inside the other gold's window too, so structure alone could not
// tell them apart without the prior criterion. Both result orders must
// resolve to the same, correct pairing.
func TestMatchCitedPriorsResolveWhichGoldRegardlessOfOrder(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{
		{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Prior: "r1-f1", Description: "d1"},
		{ID: "g2", File: "a.go", From: 12, To: 12, Action: verifydeliver.ActionFix, Prior: "r1-f2", Description: "d2"},
	}}
	forG1 := verifydeliver.ResultFinding{File: "a.go", Line: 11, Title: "cites r1-f1", Prior: "r1-f1"} // one off g1, inside g2's window too
	forG2 := verifydeliver.ResultFinding{File: "a.go", Line: 11, Title: "cites r1-f2", Prior: "r1-f2"} // one off g2, inside g1's window too
	reported := []verifydeliver.Finding{{ID: "r1-f1", Status: verifydeliver.StatusOpen}, {ID: "r1-f2", Status: verifydeliver.StatusOpen}}

	check := func(t *testing.T, findings []verifydeliver.ResultFinding, wantG1, wantG2 int) {
		m, err := MatchRound("case", 2, "", "", gold, nil, nil, nil, findings, reported, nil)
		if err != nil {
			t.Fatalf("MatchRound: %v", err)
		}
		if m.MatchedFinding[0] != wantG1 || m.MatchedFinding[1] != wantG2 {
			t.Errorf("MatchedFinding = %v, want [%d %d]: each gold matched to the finding citing its own prior", m.MatchedFinding, wantG1, wantG2)
		}
	}
	t.Run("g1-finding-first", func(t *testing.T) { check(t, []verifydeliver.ResultFinding{forG1, forG2}, 0, 1) })
	t.Run("g2-finding-first", func(t *testing.T) { check(t, []verifydeliver.ResultFinding{forG2, forG1}, 1, 0) })
}

// TestMatchOrderIndependencePermutation pins order independence:
// every permutation of a set of findings with no exact tie among them
// gives the same pairing. Four findings, each a distinct edge for one of
// two gold entries, scored differently enough (one confirmed Same, one
// citing the right prior, two at different closeness) that no two
// orderings could tie.
func TestMatchOrderIndependencePermutation(t *testing.T) {
	gold := Gold{Findings: []GoldFinding{
		{ID: "g1", File: "a.go", From: 10, To: 10, Action: verifydeliver.ActionFix, Description: "d1"},
		{ID: "g2", File: "a.go", From: 20, To: 20, Action: verifydeliver.ActionFix, Prior: "r1-p", Description: "d2"},
	}}
	// g1's own candidates: sameOne (judge Same, wins over insideOne) and
	// insideOne (inside the span, no judge, no prior).
	sameOne := verifydeliver.ResultFinding{File: "a.go", Line: 9, Title: "g1-same"}
	insideOne := verifydeliver.ResultFinding{File: "a.go", Line: 10, Title: "g1-inside"}
	// g2's own candidates: priorOne (cites r1-p, 2 off) and farOne (no
	// prior, 3 off - strictly worse than priorOne on both criteria
	// ranked after same).
	priorOne := verifydeliver.ResultFinding{File: "a.go", Line: 22, Title: "g2-prior", Prior: "r1-p"}
	farOne := verifydeliver.ResultFinding{File: "a.go", Line: 23, Title: "g2-far"}

	all := []verifydeliver.ResultFinding{sameOne, insideOne, priorOne, farOne}
	indices := []int{0, 1, 2, 3}

	var permute func([]int, int)
	permute = func(a []int, k int) {
		if k == len(a) {
			findings := make([]verifydeliver.ResultFinding, len(a))
			reported := make([]verifydeliver.Finding, len(a))
			judgeVerdictByTitle := map[string]Verdict{"g1-same": Same}
			for i, srcIdx := range a {
				findings[i] = all[srcIdx]
				reported[i] = verifydeliver.Finding{ID: fmt.Sprintf("r%d", srcIdx), Status: verifydeliver.StatusOpen}
			}
			judge := titleJudge(judgeVerdictByTitle)
			m, err := MatchRound("case", 1, "", "", gold, nil, nil, nil, findings, reported, judge)
			if err != nil {
				t.Fatalf("MatchRound: %v", err)
			}
			g1Title, g2Title := "", ""
			if m.MatchedFinding[0] != -1 {
				g1Title = findings[m.MatchedFinding[0]].Title
			}
			if m.MatchedFinding[1] != -1 {
				g2Title = findings[m.MatchedFinding[1]].Title
			}
			if g1Title != "g1-same" || g2Title != "g2-prior" {
				t.Errorf("order %v: matched (%q, %q), want (g1-same, g2-prior)", a, g1Title, g2Title)
			}
			return
		}
		for i := k; i < len(a); i++ {
			a[k], a[i] = a[i], a[k]
			permute(a, k+1)
			a[k], a[i] = a[i], a[k]
		}
	}
	permute(indices, 0)
}

// titleJudge answers by the finding's own title, the way
// TestMatchOrderIndependencePermutation needs: a judge keyed by point id
// (byPointJudge) or candidate order (scriptedJudge) would not survive
// permuting which finding sits at which index.
type titleJudge map[string]Verdict

func (j titleJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	out := make([]Verdict, len(q.Candidates))
	for i, c := range q.Candidates {
		out[i] = j[q.Findings[c.Finding].Title]
	}
	return out, nil
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

// exhaustiveBest is bestMatching's reference: every assignment of gold
// entries to distinct findings (or to none), scored and compared the same
// way. Exponential, so only for the small instances below.
func exhaustiveBest(optionsByGold [][]matchOption, nFindings int) matchScore {
	var best matchScore
	used := map[int]bool{}
	var walk func(gi int, score matchScore)
	walk = func(gi int, score matchScore) {
		if gi == len(optionsByGold) {
			if score.better(best) {
				best = score
			}
			return
		}
		walk(gi+1, score)
		for _, o := range optionsByGold[gi] {
			if used[o.finding] {
				continue
			}
			used[o.finding] = true
			walk(gi+1, score.plus(optionScore(o, nFindings)))
			delete(used, o.finding)
		}
	}
	walk(0, matchScore{})
	return best
}

// matchingScore validates a bestMatching result as a one-to-one matching
// over real edges and returns its summed score.
func matchingScore(t *testing.T, optionsByGold [][]matchOption, nFindings int, got []int) matchScore {
	t.Helper()
	var total matchScore
	seen := map[int]bool{}
	for gi, j := range got {
		if j == -1 {
			continue
		}
		if seen[j] {
			t.Fatalf("finding %d matched twice in %v", j, got)
		}
		seen[j] = true
		found := false
		for _, o := range optionsByGold[gi] {
			if o.finding == j {
				total = total.plus(optionScore(o, nFindings))
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("gold %d matched to finding %d with no edge between them, in %v", gi, j, got)
		}
	}
	return total
}

// TestBestMatchingAgreesWithExhaustiveSearch checks bestMatching against
// the exhaustive reference on many small instances, generated from a fixed
// seed so a failure reproduces: every result must be a valid one-to-one
// matching over real edges, and its summed score must equal the best any
// assignment reaches. Scores can tie, so the scores are compared, not the
// assignments.
func TestBestMatchingAgreesWithExhaustiveSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 2000; iter++ {
		nGold, nFindings := 1+rng.Intn(5), 1+rng.Intn(6)
		options := make([][]matchOption, nGold)
		for gi := range options {
			for j := 0; j < nFindings; j++ {
				if rng.Intn(3) == 0 {
					continue
				}
				options[gi] = append(options[gi], matchOption{
					finding:    j,
					same:       rng.Intn(3) == 0,
					priorMatch: rng.Intn(4) == 0,
					closeness:  rng.Intn(closenessInside + 1),
				})
			}
		}
		got := bestMatching(options, nFindings)
		if gotScore, want := matchingScore(t, options, nFindings, got), exhaustiveBest(options, nFindings); gotScore != want {
			t.Fatalf("instance %d: bestMatching %v scores %+v, exhaustive best is %+v; options: %+v", iter, got, gotScore, want, options)
		}
	}
}

// TestBestMatchingStaysPolynomialOnALargeRound gives bestMatching a round
// no exhaustive search could finish (40 gold entries, each with an edge to
// every one of 60 findings) and pins optimality, not only completeness:
// each gold entry's own same-indexed finding carries the strongest possible
// edge (closeness-4, inside the span itself), every other edge strictly
// weaker (closeness 0-3), so the diagonal is the unique best matching - any
// matching that used even one off-diagonal edge would score strictly lower,
// since cardinality and every field ahead of closeness already tie.
func TestBestMatchingStaysPolynomialOnALargeRound(t *testing.T) {
	const nGold, nFindings = 40, 60
	options := make([][]matchOption, nGold)
	for gi := range options {
		for j := 0; j < nFindings; j++ {
			closeness := (gi + j) % closenessInside // 0..3: always weaker than the diagonal edge below
			if j == gi {
				closeness = closenessInside // gi's own finding: the strongest possible edge
			}
			options[gi] = append(options[gi], matchOption{finding: j, closeness: closeness})
		}
	}
	got := bestMatching(options, nFindings)
	matchingScore(t, options, nFindings, got)
	for gi, j := range got {
		if j != gi {
			t.Errorf("gold %d matched to finding %d, want %d: the diagonal's closeness-%d edges are the unique optimum", gi, j, gi, closenessInside)
		}
	}
}
