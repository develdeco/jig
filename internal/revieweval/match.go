package revieweval

import (
	"fmt"
	"math"
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
	// CitedPrior is true for the one extra candidate buildCandidates adds
	// per finding whose own Prior names a dismissed fold point in the same
	// file: a citation is itself location evidence, so this candidate
	// survives the way any non-line0 edge does - on anything but Different
	// - even when Line0 is also true.
	CitedPrior bool
}

// Verdict is the judge's answer for one candidate.
type Verdict string

const (
	Same      Verdict = "same"
	Different Verdict = "different"
	Undecided Verdict = ""
)

// JudgeQuery is one round's whole batch of structural candidates, asked of
// the judge once. Case is the real case name, for a caller that needs it
// (a test fixture locating its own file, a report) - a live judge (judge.go)
// never puts it in anything the dispatched model reads or is identified
// by; it derives the opaque run id from Case itself where a real dispatch
// needs one.
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

// dismissedPoint builds one dismissed fold finding's Point. Ordinarily its
// span is just its own one line and its description its bare title and
// detail (recordedDescription) - both read from the fold's own current
// occurrence, f. When recordedLinks names a decision for it
// (Decision.Recorded) and that decision's own file still matches the
// fold's current one - a later round could have re-reported the same id
// under a different file, which the loader never saw and the decision
// never judged, so the link is skipped rather than trusted - the point
// instead takes the decision's own description and the union of the
// decision's own span with the record's line as the loader validated it
// (Decision.RecordedLine), never the fold's latest occurrence: a re-report
// could have moved that line anywhere since, but the human judged the line
// that was actually on record when they decided it, and that is the only
// one a decision's own span was ever checked against.
func dismissedPoint(f verifydeliver.Finding, recordedLinks map[string]Decision) Point {
	if link, ok := recordedLinks[f.ID]; ok && normalizeFile(link.File) == normalizeFile(f.File) {
		from, to := min(link.From, link.RecordedLine), max(link.To, link.RecordedLine)
		return Point{ID: f.ID, File: f.File, Description: link.Description, From: from, To: to}
	}
	return Point{ID: f.ID, File: f.File, Description: recordedDescription(f), From: f.Line, To: f.Line}
}

// dismissedCandKey identifies one (point, finding) pair among the
// dismissed-fold candidates buildCandidates has already added structurally,
// so its cited-prior pass (below) never adds a second, duplicate candidate
// for a pair the ordinary window already covered.
type dismissedCandKey struct {
	point   string
	finding int
}

// buildCandidates builds every structural candidate of one round: for each
// point in gold's findings and traps, decisions, and the fold's dismissed
// findings, every result finding whose normalized file matches and whose
// line is 0 or falls in the point's span widened by lineWindow.
//
// It then marks one candidate per finding whose own Prior names a
// dismissed fold point in the same file: a cited prior is itself location
// evidence, so that candidate's CitedPrior flag is set whatever the
// finding's line - even 0, where the ordinary structural pass above
// already treats every point in the file as a candidate too, needing an
// explicit Same to survive (edgeSurvives) - so that it needs no explicit
// Same, only not Different, the way a normal in-window edge already does.
// A finding whose line sits outside the point's window gets no structural
// candidate at all above, so this adds one from scratch. A prior naming a
// point in a different file, or one the judge rejects, is then simply no
// edge: classifyUnmatched's rule 1 fails it as a wrong prior exactly as it
// would any other unsupported citation.
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
	dismissedByID := make(map[string]verifydeliver.Finding, len(dismissed))
	for _, f := range dismissed {
		dismissedByID[f.ID] = f
		addPoint(dismissedPoint(f, recordedLinks), kindDismissed, "", -1)
	}

	// dismissedIdx maps a (point, finding) pair already built above (by
	// the ordinary structural pass) to its index in cands, so the cited-
	// prior pass below upgrades that same candidate in place - never a
	// second, duplicate one - whenever it already exists.
	dismissedIdx := make(map[dismissedCandKey]int, len(cands))
	for i, c := range cands {
		if meta[i].kind == kindDismissed {
			dismissedIdx[dismissedCandKey{c.Point.ID, c.Finding}] = i
		}
	}
	for j, f := range findings {
		if f.Prior == "" {
			continue
		}
		d, ok := dismissedByID[f.Prior]
		if !ok || normalizeFile(d.File) != normalizeFile(f.File) {
			continue
		}
		if idx, exists := dismissedIdx[dismissedCandKey{d.ID, j}]; exists {
			cands[idx].CitedPrior = true
			continue
		}
		cands = append(cands, Candidate{Point: dismissedPoint(d, recordedLinks), Finding: j, Line0: f.Line == 0, CitedPrior: true})
		meta = append(meta, candMeta{kind: kindDismissed, goldIndex: -1})
	}

	return cands, meta
}

// edgeSurvives decides whether a candidate edge survives: Different
// vetoes an edge outright; a plain line-0 finding carries no location
// evidence of its own, so it survives only on the judge's explicit Same,
// unless the candidate is the finding's own cited prior in the same file
// (Candidate.CitedPrior) - a citation is itself location evidence, so
// that candidate survives the way any other edge does, on anything but
// Different (Undecided is a structural match).
func edgeSurvives(c Candidate, v Verdict) bool {
	if v == Different {
		return false
	}
	if c.Line0 && !c.CitedPrior {
		return v == Same
	}
	return true
}

// closenessInside is lineCloseness's score for a line inside the span
// itself; each line outside it, up to lineWindow, scores one less.
const closenessInside = lineWindow + 1

// lineCloseness scores how well a finding's line sits against a gold
// span, highest first: inside the span itself (best), then 1, 2 or 3 lines
// outside it, then line 0 - unknown location, the weakest evidence a
// surviving edge can carry, since edgeSurvives only lets a line-0 edge
// through on the judge's own explicit Same or a cited prior.
func lineCloseness(line0 bool, line, from, to int) int {
	switch {
	case line0:
		return 0
	case line >= from && line <= to:
		return closenessInside
	case line < from:
		return closenessInside - (from - line)
	default:
		return closenessInside - (line - to)
	}
}

// matchOption is one gold entry's one candidate finding: everything
// bestMatching needs to score it, and to record the edge if it is chosen.
type matchOption struct {
	finding          int
	same, priorMatch bool
	closeness        int
}

// matchScore is one edge's score, or a whole matching's summed score,
// compared lexicographically, most significant field first: how many gold
// entries it matches - cardinality always wins first, so a bigger matching
// beats a smaller one whatever its other totals - then how many of its
// edges the judge confirmed Same, then how many cite the gold's own
// recorded prior, then the summed closeness of every matched edge's line
// to its span, and last how early the paired findings were reported, which
// only ever decides between matchings the evidence cannot tell apart, and
// then decides them the same way every time. It never reads a finding's
// status or action: those are what the score measures (Lost,
// DroppedQuestions, ActionAgreed, ...), so ranking a pairing by them would
// let the score choose its own inputs instead of being measured by them.
type matchScore struct {
	matched, same, prior, closeness, early int
}

// better reports whether s ranks strictly above other, field by field,
// most significant first. Lexicographic order is a total order that
// addition preserves, which is what lets bestMatching run the Hungarian
// algorithm over matchScore values directly instead of over one packed
// integer that a large round could overflow.
func (s matchScore) better(other matchScore) bool {
	if s.matched != other.matched {
		return s.matched > other.matched
	}
	if s.same != other.same {
		return s.same > other.same
	}
	if s.prior != other.prior {
		return s.prior > other.prior
	}
	if s.closeness != other.closeness {
		return s.closeness > other.closeness
	}
	return s.early > other.early
}

func (s matchScore) plus(o matchScore) matchScore {
	return matchScore{s.matched + o.matched, s.same + o.same, s.prior + o.prior, s.closeness + o.closeness, s.early + o.early}
}

func (s matchScore) minus(o matchScore) matchScore {
	return matchScore{s.matched - o.matched, s.same - o.same, s.prior - o.prior, s.closeness - o.closeness, s.early - o.early}
}

// optionScore is one candidate edge's own matchScore; nFindings turns the
// finding's index into its earliness, so an earlier-reported finding
// scores higher on that last field.
func optionScore(o matchOption, nFindings int) matchScore {
	s := matchScore{matched: 1, closeness: o.closeness, early: nFindings - o.finding}
	if o.same {
		s.same = 1
	}
	if o.priorMatch {
		s.prior = 1
	}
	return s
}

// bestMatching assigns gold entries (optionsByGold's own index) to
// findings one to one, and returns, per gold entry, its finding's index or
// -1: the matching whose summed matchScore ranks highest, which is a
// maximum-cardinality matching first and the best-supported one among
// those after. It is the Hungarian algorithm (the O(n^2 m) potentials
// form), minimizing each assignment's cost below a common ceiling: one row
// per gold entry, one column per finding plus one "unmatched" column per
// gold entry, a real edge costing the ceiling minus its score and anything
// else costing the ceiling itself. Every row takes exactly one column, so
// minimizing the summed cost is exactly maximizing the summed score. It is
// exact, and polynomial: a round with many seeded findings stays cheap.
func bestMatching(optionsByGold [][]matchOption, nFindings int) []int {
	n := len(optionsByGold)
	best := make([]int, n)
	for i := range best {
		best[i] = -1
	}
	if n == 0 || nFindings == 0 {
		return best
	}

	ceiling := matchScore{matched: 1, same: 1, prior: 1, closeness: closenessInside, early: nFindings}
	m := nFindings + n
	cost := make([][]matchScore, n)
	edge := make([][]bool, n)
	for i := range cost {
		cost[i] = make([]matchScore, m)
		edge[i] = make([]bool, m)
		for j := range cost[i] {
			cost[i][j] = ceiling
		}
		for _, o := range optionsByGold[i] {
			cost[i][o.finding] = ceiling.minus(optionScore(o, nFindings))
			edge[i][o.finding] = true
		}
	}

	// u and v are the row and column potentials; p[j] is the row (1-based)
	// holding column j, 0 for none; way[j] is the column before j on the
	// current augmenting path. Column 0 is the algorithm's own sentinel.
	inf := matchScore{matched: math.MaxInt32}
	u := make([]matchScore, n+1)
	v := make([]matchScore, m+1)
	p := make([]int, m+1)
	way := make([]int, m+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]matchScore, m+1)
		for j := range minv {
			minv[j] = inf
		}
		used := make([]bool, m+1)
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], inf, 0
			for j := 1; j <= m; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1].minus(u[i0]).minus(v[j])
				if minv[j].better(cur) {
					minv[j], way[j] = cur, j0
				}
				if delta.better(minv[j]) {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= m; j++ {
				if used[j] {
					u[p[j]] = u[p[j]].plus(delta)
					v[j] = v[j].minus(delta)
				} else {
					minv[j] = minv[j].minus(delta)
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}

	for j := 1; j <= m; j++ {
		if i := p[j]; i != 0 && j-1 < nFindings && edge[i-1][j-1] {
			best[i-1] = j - 1
		}
	}
	return best
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
// runs the exact maximum-weight matching among maximum-cardinality
// matchings over the seeded gold findings' surviving edges (bestMatching,
// and classifies every finding the match left over. reported is
// ApplyRound's own per-finding fate, in the same order as findings
// (result.Findings): classifyUnmatched's rules read it, never the
// reviewer's own action label - and, bestMatching itself never
// reads status or action at all, only the judge's verdict, prior agreement
// and line closeness. recordedLinks is every earlier round's decisions
// keyed by the finding id each one's "recorded" names; a nil map is the
// common case (no round has used it yet).
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
		survives[i] = edgeSurvives(c, verdicts[i])
		byJudge[i] = verdicts[i] == Same
	}

	// Every surviving gold edge, organized per gold entry with its own
	// scoring already computed, for bestMatching.
	optionsByGold := make([][]matchOption, len(gold.Findings))
	for i, c := range cands {
		m := meta[i]
		if m.kind != kindGold || !survives[i] {
			continue
		}
		g := gold.Findings[m.goldIndex]
		f := findings[c.Finding]
		optionsByGold[m.goldIndex] = append(optionsByGold[m.goldIndex], matchOption{
			finding:    c.Finding,
			same:       byJudge[i],
			priorMatch: f.Prior != "" && g.Prior != "" && f.Prior == g.Prior,
			closeness:  lineCloseness(c.Line0, f.Line, g.From, g.To),
		})
	}
	matchGold := bestMatching(optionsByGold, len(findings))

	structureOnly := make([]bool, len(gold.Findings))
	matchedFinding := make([]bool, len(findings))
	for gi, j := range matchGold {
		if j == -1 {
			continue
		}
		matchedFinding[j] = true
		for _, opt := range optionsByGold[gi] {
			if opt.finding == j {
				structureOnly[gi] = !opt.same
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
