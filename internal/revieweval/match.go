package revieweval

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// lineWindow is how far, in either direction, a finding's line may sit from
// a point's seeded span and still count as the same location. Structure
// alone cannot ask for exact agreement: a reviewer citing "line 19" for a
// bug seeded across lines 17-18 is still pointing at it.
const lineWindow = 3

// Point is one thing a round's result can be matched against: a gold
// finding, a trap, a recorded human decision, or a fold finding the human
// already dismissed. From and To are the span, 1-based, inclusive; a
// recorded finding's span (the fold's dismissed findings) is its one line,
// From equal to To.
type Point struct {
	ID, File, Description string
	From, To              int
}

// Candidate is one structural edge between a point and a round finding,
// before the judge (or a nil judge's Undecided default) decides whether it
// survives.
type Candidate struct {
	Point   Point
	Finding int  // index into the round's result.Findings
	Line0   bool // the finding gave line 0 (unknown)
}

// Verdict is the judge's answer for one candidate.
type Verdict string

const (
	Same      Verdict = "same"
	Different Verdict = "different"
	Undecided Verdict = ""
)

// JudgeQuery is one round's whole batch of structural candidates, asked of
// the judge once.
type JudgeQuery struct {
	Case       string
	Round      int
	RepoDir    string // the case repo at this round's head
	WorkDir    string // scratch dir for the judge's own files
	Findings   []verifydeliver.ResultFinding
	Candidates []Candidate
}

// Judge confirms structural candidates: one verdict per candidate, in the
// same order as JudgeQuery.Candidates.
type Judge interface {
	Confirm(q JudgeQuery) ([]Verdict, error)
}

// Fate is what classifyUnmatched's seven rules settle an unmatched finding
// as.
type Fate string

const (
	// FateSkip is rule 1: a repeat of a dismissed prior, a permitted
	// repeat counted nowhere.
	FateSkip Fate = "skip"
	// FatePending is rules 2 and 7: a note (never a false alarm) or an
	// otherwise unlabeled finding, both left for a person to label.
	FatePending Fate = "pending"
	// FateRelitigated is rule 3: an edge to a fold finding the human
	// already dismissed, raised again with no prior citing it.
	FateRelitigated Fate = "relitigated"
	// FateFalseAlarm is rules 4 and 6: an edge to a trap or a dismissed
	// decision, or any unmatched finding in a round whose gold is
	// exhaustive.
	FateFalseAlarm Fate = "false-alarm"
	// FateExtraTrue is rule 5: an edge to a decision the human kept - a
	// true positive beyond the seeded gold.
	FateExtraTrue Fate = "extra-true"
)

// RoundMatch is one round's whole matching result: the seeded gold's
// one-to-one match, plus a Fate for every finding that match left over.
type RoundMatch struct {
	// MatchedFinding[i] is the result.Findings index matched to
	// gold.Findings[i], or -1 when unmatched.
	MatchedFinding []int
	// StructureOnly[i] is true when gold.Findings[i]'s match survived on
	// structure alone, with no judge confirmation (Unconfirmed).
	StructureOnly []bool
	// Classification[j] is the Fate of result.Findings[j], for every j not
	// present in MatchedFinding.
	Classification map[int]Fate
}

// normalizeFile makes a file comparable across a point and a finding: both
// are already validated repo-relative paths by the time they reach here
// (the loader for a point, ParseReviewResult for a finding), so this is
// only a defensive slash/prefix normalization, not a validator.
func normalizeFile(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

// candKind tags which of the four point sources one candidate came
// from, so classifyUnmatched can tell a trap's edge from a decision's.
type candKind int

const (
	kindGold candKind = iota
	kindTrap
	kindDecision
	kindDismissed
)

// candMeta is the bookkeeping buildCandidates keeps alongside each public
// Candidate: which source it came from, a decision's own outcome, and a
// gold candidate's index back into gold.Findings.
type candMeta struct {
	kind      candKind
	decision  string // kindDecision only: DecisionKept or DecisionDismissed
	goldIndex int    // kindGold only
}

// buildCandidates builds every structural candidate of one round: for each
// point in gold's findings and traps, decisions, and the fold's dismissed
// findings, every result finding whose normalized file matches and whose
// line is 0 or falls in the point's span widened by lineWindow.
func buildCandidates(gold Gold, decisions []Decision, dismissed []verifydeliver.Finding, findings []verifydeliver.ResultFinding) ([]Candidate, []candMeta) {
	var cands []Candidate
	var meta []candMeta

	addPoint := func(p Point, kind candKind, decision string, goldIndex int) {
		file := normalizeFile(p.File)
		for j, f := range findings {
			if normalizeFile(f.File) != file {
				continue
			}
			line0 := f.Line == 0
			if !line0 && (f.Line < p.From-lineWindow || f.Line > p.To+lineWindow) {
				continue
			}
			cands = append(cands, Candidate{Point: p, Finding: j, Line0: line0})
			meta = append(meta, candMeta{kind: kind, decision: decision, goldIndex: goldIndex})
		}
	}

	for i, g := range gold.Findings {
		addPoint(Point{ID: g.ID, File: g.File, Description: g.Description, From: g.From, To: g.To}, kindGold, "", i)
	}
	for _, tr := range gold.Traps {
		addPoint(Point{ID: tr.ID, File: tr.File, Description: tr.Description, From: tr.From, To: tr.To}, kindTrap, "", -1)
	}
	for _, d := range decisions {
		addPoint(Point{ID: d.ID, File: d.File, Description: d.Description, From: d.From, To: d.To}, kindDecision, d.Decision, -1)
	}
	for _, f := range dismissed {
		addPoint(Point{ID: f.ID, File: f.File, Description: f.Title, From: f.Line, To: f.Line}, kindDismissed, "", -1)
	}

	return cands, meta
}

// edgeSurvives decides whether a candidate edge survives: Different
// vetoes an edge outright; a line-0 finding carries no location evidence
// of its own, so it survives only on the judge's explicit Same; any other
// edge survives unless the judge said Different (Undecided is a
// structural match).
func edgeSurvives(line0 bool, v Verdict) bool {
	if v == Different {
		return false
	}
	if line0 {
		return v == Same
	}
	return true
}

// tryAugment is Kuhn's augmenting-path step: it looks for a finding not yet
// claimed by a gold index compatible with j (or claimed by one that can be
// reassigned to a different compatible gold index), so a full one-to-one
// maximum matching is found even when a naive first-fit greedy pairing
// would leave two mutually compatible findings starving each other.
func tryAugment(j int, findingToGold [][]int, matchGold []int, visited []bool) bool {
	for _, gi := range findingToGold[j] {
		if visited[gi] {
			continue
		}
		visited[gi] = true
		if matchGold[gi] == -1 || tryAugment(matchGold[gi], findingToGold, matchGold, visited) {
			matchGold[gi] = j
			return true
		}
	}
	return false
}

// classifyUnmatched decides one unmatched finding's Fate: seven rules, in
// order, the first that applies. reportedStatus is this
// finding's own jig-assigned status (ApplyRound's reported[j].Status, not
// the reviewer's own action label): rules 1 and 2 read jig's bookkeeping
// outcome, not the reviewer's word, because a recurrence or the recurrence
// bound can move a finding's status away from what its action alone would
// suggest.
func classifyUnmatched(reportedStatus string, dismissedFoldEdge, trapEdge, decisionDismissedEdge, decisionKeptEdge, exhaustive bool) Fate {
	switch {
	case reportedStatus == verifydeliver.StatusDismissed:
		return FateSkip
	case reportedStatus == verifydeliver.StatusNoted:
		return FatePending
	case dismissedFoldEdge:
		return FateRelitigated
	case trapEdge || decisionDismissedEdge:
		return FateFalseAlarm
	case decisionKeptEdge:
		return FateExtraTrue
	case exhaustive:
		return FateFalseAlarm
	default:
		return FatePending
	}
}

// MatchRound builds every structural candidate of one round, asks judge
// once for the whole batch (a nil judge answers Undecided for everything),
// runs a maximum bipartite matching over the seeded gold findings'
// surviving edges, and classifies every finding the match left over.
// reported is ApplyRound's own per-finding fate, in the same order as
// findings (result.Findings): rules 1 and 2 of classifyUnmatched read it,
// never the reviewer's own action label.
func MatchRound(caseName string, round int, repoDir, workDir string, gold Gold, decisions []Decision, dismissed []verifydeliver.Finding, findings []verifydeliver.ResultFinding, reported []verifydeliver.Finding, judge Judge) (RoundMatch, error) {
	if len(reported) != len(findings) {
		return RoundMatch{}, fmt.Errorf("revieweval: match: %d reported findings for %d result findings, want equal", len(reported), len(findings))
	}

	cands, meta := buildCandidates(gold, decisions, dismissed, findings)

	verdicts := make([]Verdict, len(cands))
	if judge != nil && len(cands) > 0 {
		q := JudgeQuery{Case: caseName, Round: round, RepoDir: repoDir, WorkDir: workDir, Findings: findings, Candidates: cands}
		v, err := judge.Confirm(q)
		if err != nil {
			return RoundMatch{}, fmt.Errorf("revieweval: match: judge: %w", err)
		}
		if len(v) != len(cands) {
			return RoundMatch{}, fmt.Errorf("revieweval: match: judge returned %d verdicts for %d candidates", len(v), len(cands))
		}
		verdicts = v
	}

	survives := make([]bool, len(cands))
	byJudge := make([]bool, len(cands))
	for i, c := range cands {
		survives[i] = edgeSurvives(c.Line0, verdicts[i])
		byJudge[i] = verdicts[i] == Same
	}

	// findingToGold[j] lists the gold indices j has a surviving edge to,
	// for Kuhn's algorithm below; goldByFinding remembers, for the edge
	// finally chosen, whether the judge (not structure alone) confirmed
	// it.
	findingToGold := make([][]int, len(findings))
	type goldFindingEdge struct {
		finding int
		byJudge bool
	}
	goldEdges := make([][]goldFindingEdge, len(gold.Findings))
	for i, c := range cands {
		m := meta[i]
		if m.kind != kindGold || !survives[i] {
			continue
		}
		findingToGold[c.Finding] = append(findingToGold[c.Finding], m.goldIndex)
		goldEdges[m.goldIndex] = append(goldEdges[m.goldIndex], goldFindingEdge{finding: c.Finding, byJudge: byJudge[i]})
	}

	matchGold := make([]int, len(gold.Findings))
	for i := range matchGold {
		matchGold[i] = -1
	}
	for j := range findings {
		tryAugment(j, findingToGold, matchGold, make([]bool, len(gold.Findings)))
	}

	structureOnly := make([]bool, len(gold.Findings))
	matchedFinding := make([]bool, len(findings))
	for gi, j := range matchGold {
		if j == -1 {
			continue
		}
		matchedFinding[j] = true
		for _, e := range goldEdges[gi] {
			if e.finding == j {
				structureOnly[gi] = !e.byJudge
				break
			}
		}
	}

	classification := make(map[int]Fate, len(findings))
	for j := range findings {
		if matchedFinding[j] {
			continue
		}
		var dismissedEdge, trapEdge, decDismissed, decKept bool
		for i, c := range cands {
			if c.Finding != j || !survives[i] {
				continue
			}
			switch meta[i].kind {
			case kindDismissed:
				dismissedEdge = true
			case kindTrap:
				trapEdge = true
			case kindDecision:
				switch meta[i].decision {
				case DecisionDismissed:
					decDismissed = true
				case DecisionKept:
					decKept = true
				}
			}
		}
		classification[j] = classifyUnmatched(reported[j].Status, dismissedEdge, trapEdge, decDismissed, decKept, gold.Exhaustive)
	}

	return RoundMatch{MatchedFinding: matchGold, StructureOnly: structureOnly, Classification: classification}, nil
}
