package revieweval

import (
	"fmt"
	"sort"
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
// recorded finding's span, when nothing links it to a decision, is its one
// line, From equal to To (a linked one takes the union of that line and the
// decision's own span - buildCandidates).
//
// Description always states a problem the way a finding would raise it: for
// a seeded finding, the real problem; for a trap, the concern a reviewer
// would wrongly raise about that correct code; for a decision or a
// dismissed record, the point that was decided. The judge is never told
// which of these a given candidate is - Point itself carries no kind tag -
// and it is asked the same question for all of them: does the finding raise
// the same problem as this description.
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

// Fate is what classifyUnmatched's rules settle an unmatched finding as.
type Fate string

const (
	// FateSkip is rule 1: jig's own status is dismissed (it cited an
	// already-dismissed prior) and that citation is structurally
	// supported - a permitted repeat, counted nowhere.
	FateSkip Fate = "skip"
	// FateWrongPrior is also rule 1: jig's own status is dismissed but
	// the finding carries no surviving edge to the dismissed fold point
	// its own prior names, so the citation is not a real repeat. It
	// fails the round (RoundScore.WrongPriors).
	FateWrongPrior Fate = "wrong-prior"
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

// recordedDescription is a dismissed fold finding's Point description when
// nothing links it to a decision: the recorded title and detail together,
// never the title alone, since a bare title rarely states the problem the
// way the point doc comment requires.
func recordedDescription(f verifydeliver.Finding) string {
	if f.Detail == "" {
		return f.Title
	}
	return f.Title + ": " + f.Detail
}

// buildCandidates builds every structural candidate of one round: for each
// point in gold's findings and traps, decisions, and the fold's dismissed
// findings, every result finding whose normalized file matches and whose
// line is 0 or falls in the point's span widened by lineWindow.
//
// A dismissed fold finding's own point ordinarily spans only its one
// recorded line, described by its bare title and detail (recordedDescription).
// When recordedLinks names a decision for it (Decision.Recorded), the
// point instead takes the union of that one line and the decision's own
// span, described by the decision itself: a store record carries one line,
// but the person who dismissed it judged a span, and a reworded repeat
// cited a few lines away is still the same point.
func buildCandidates(gold Gold, decisions []Decision, recordedLinks map[string]Decision, dismissed []verifydeliver.Finding, findings []verifydeliver.ResultFinding) ([]Candidate, []candMeta) {
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
		desc, from, to := recordedDescription(f), f.Line, f.Line
		if link, ok := recordedLinks[f.ID]; ok {
			desc = link.Description
			from, to = min(f.Line, link.From), max(f.Line, link.To)
		}
		addPoint(Point{ID: f.ID, File: f.File, Description: desc, From: from, To: to}, kindDismissed, "", -1)
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

// goldEdge is one surviving structural edge from a finding to a gold
// candidate, with its ranking criteria already computed: among the
// maximum matchings Kuhn's algorithm could return, the order below prefers
// the best-supported pairing rather than whichever the finding order
// happens to produce.
type goldEdge struct {
	finding int
	gold    int
	same    bool // the judge said Same
	agree   bool // the finding's resulting status agrees with the gold action
	inSpan  bool // the finding's line lies inside the gold span itself, not only the window
}

// edgeBetter reports whether edge a should be preferred over b when both
// are available for the same augmenting choice: the judge said Same; the
// resulting status agrees with the gold action; the finding's line lies
// inside the span itself; then result order (the lower finding index) -
// which, since two edges never share a finding index in the same
// comparison here, also makes this a strict order.
func edgeBetter(a, b goldEdge) bool {
	if a.same != b.same {
		return a.same
	}
	if a.agree != b.agree {
		return a.agree
	}
	if a.inSpan != b.inSpan {
		return a.inSpan
	}
	return a.finding < b.finding
}

// statusAgreesWithGoldAction reports whether status is what a fully
// successful review of a gold finding with this action would leave jig's
// bookkeeping holding: fix with open, ask with asked, note with noted.
func statusAgreesWithGoldAction(status, action string) bool {
	switch action {
	case verifydeliver.ActionFix:
		return status == verifydeliver.StatusOpen
	case verifydeliver.ActionAsk:
		return status == verifydeliver.StatusAsked
	case verifydeliver.ActionNote:
		return status == verifydeliver.StatusNoted
	default:
		return false
	}
}

// tryAugment is Kuhn's augmenting-path step: it looks for a gold index not
// yet claimed by an edge compatible with j (or claimed by one that can be
// reassigned to a different compatible gold index), so a full one-to-one
// maximum matching is found even when a naive first-fit greedy pairing
// would leave two mutually compatible findings starving each other.
// findingToGold[j] must already be ordered best edge first (edgeBetter), so
// the augmenting path itself, not only which finding is tried when, favors
// the best-supported pairing.
func tryAugment(j int, findingToGold [][]goldEdge, matchGold []int, visited []bool) bool {
	for _, e := range findingToGold[j] {
		if visited[e.gold] {
			continue
		}
		visited[e.gold] = true
		if matchGold[e.gold] == -1 || tryAugment(matchGold[e.gold], findingToGold, matchGold, visited) {
			matchGold[e.gold] = j
			return true
		}
	}
	return false
}

// classifyUnmatched decides one unmatched finding's Fate: the rules below,
// in order, the first that applies. reportedStatus is this finding's own
// jig-assigned status (ApplyRound's reported[j].Status, not the reviewer's
// own action label): rule 1 and rule 2 read jig's bookkeeping outcome, not
// the reviewer's word, because a recurrence or the recurrence bound can
// move a finding's status away from what its action alone would suggest.
//
// Rule 1 splits in two: a dismissed status is a permitted repeat only
// when the finding also carries a surviving structural edge to the
// dismissed fold point its own prior names (citedDismissedEdge); otherwise
// it is a wrong prior - the finding cited a dismissal that, structurally,
// is not what it is actually about - and the round fails.
func classifyUnmatched(reportedStatus string, citedDismissedEdge, dismissedFoldEdge, trapEdge, decisionDismissedEdge, decisionKeptEdge, exhaustive bool) Fate {
	switch {
	case reportedStatus == verifydeliver.StatusDismissed && citedDismissedEdge:
		return FateSkip
	case reportedStatus == verifydeliver.StatusDismissed:
		return FateWrongPrior
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
// surviving edges (preferring the best-supported pairing among the
// maximum matchings available), and classifies every finding the match
// left over. reported is ApplyRound's own per-finding fate, in the same
// order as findings (result.Findings): classifyUnmatched's rules read it,
// never the reviewer's own action label. recordedLinks is every earlier
// round's decisions keyed by the finding id each one's "recorded" names;
// a nil map is the common case (no round has used it yet).
func MatchRound(caseName string, round int, repoDir, workDir string, gold Gold, decisions []Decision, recordedLinks map[string]Decision, dismissed []verifydeliver.Finding, findings []verifydeliver.ResultFinding, reported []verifydeliver.Finding, judge Judge) (RoundMatch, error) {
	if len(reported) != len(findings) {
		return RoundMatch{}, fmt.Errorf("revieweval: match: %d reported findings for %d result findings, want equal", len(reported), len(findings))
	}

	cands, meta := buildCandidates(gold, decisions, recordedLinks, dismissed, findings)

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

	// Every surviving gold edge, with its ranking criteria attached.
	var edges []goldEdge
	for i, c := range cands {
		m := meta[i]
		if m.kind != kindGold || !survives[i] {
			continue
		}
		g := gold.Findings[m.goldIndex]
		f := findings[c.Finding]
		edges = append(edges, goldEdge{
			finding: c.Finding,
			gold:    m.goldIndex,
			same:    byJudge[i],
			agree:   statusAgreesWithGoldAction(reported[c.Finding].Status, g.Action),
			inSpan:  !c.Line0 && f.Line >= g.From && f.Line <= g.To,
		})
	}

	// findingToGold[j] lists j's surviving gold edges, each finding's own
	// list sorted best edge first (edgeBetter), for tryAugment below.
	findingToGold := make([][]goldEdge, len(findings))
	for _, e := range edges {
		findingToGold[e.finding] = append(findingToGold[e.finding], e)
	}
	for j := range findingToGold {
		sort.Slice(findingToGold[j], func(a, b int) bool { return edgeBetter(findingToGold[j][a], findingToGold[j][b]) })
	}

	// findingOrder processes findings best-edge-first, so Kuhn's augmenting
	// path settles a finding's own best-supported gold entry before a
	// later, weaker finding's claim can displace it.
	var findingOrder []int
	for j, es := range findingToGold {
		if len(es) > 0 {
			findingOrder = append(findingOrder, j)
		}
	}
	sort.Slice(findingOrder, func(a, b int) bool {
		return edgeBetter(findingToGold[findingOrder[a]][0], findingToGold[findingOrder[b]][0])
	})

	matchGold := make([]int, len(gold.Findings))
	for i := range matchGold {
		matchGold[i] = -1
	}
	for _, j := range findingOrder {
		tryAugment(j, findingToGold, matchGold, make([]bool, len(gold.Findings)))
	}

	structureOnly := make([]bool, len(gold.Findings))
	matchedFinding := make([]bool, len(findings))
	for gi, j := range matchGold {
		if j == -1 {
			continue
		}
		matchedFinding[j] = true
		for _, e := range findingToGold[j] {
			if e.gold == gi {
				structureOnly[gi] = !e.same
				break
			}
		}
	}

	classification := make(map[int]Fate, len(findings))
	for j := range findings {
		if matchedFinding[j] {
			continue
		}
		var citedDismissedEdge, dismissedEdge, trapEdge, decDismissed, decKept bool
		for i, c := range cands {
			if c.Finding != j || !survives[i] {
				continue
			}
			switch meta[i].kind {
			case kindDismissed:
				dismissedEdge = true
				if c.Point.ID == reported[j].ID {
					citedDismissedEdge = true
				}
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
		classification[j] = classifyUnmatched(reported[j].Status, citedDismissedEdge, dismissedEdge, trapEdge, decDismissed, decKept, gold.Exhaustive)
	}

	return RoundMatch{MatchedFinding: matchGold, StructureOnly: structureOnly, Classification: classification}, nil
}
