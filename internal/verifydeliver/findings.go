package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
	"gopkg.in/yaml.v3"
)

// Finding status vocabulary: a finding's place in jig's own bookkeeping,
// distinct from the reviewer's action label. open and asked together are
// "the open set": findings still outstanding across rounds. dismissed and
// noted are terminal for this bookkeeping; only a human dismissal (routing)
// or a repeat of an already dismissed finding (rule 2) produces dismissed.
const (
	StatusOpen      = "open"
	StatusAsked     = "asked"
	StatusDismissed = "dismissed"
	StatusNoted     = "noted"
)

// Triage vocabulary: who decided a finding's outcome - a person at a
// terminal, or --yes / no terminal.
const (
	TriageHuman = "human"
	TriageAuto  = "auto"
)

// Finding is one persisted entry of gate/round-N/findings.yaml, plus its
// additive triage fields (triage, decision, routed_as). It is jig's own
// bookkeeping record: the reviewer never sees it directly, only the subset
// review.json's open and dismissed lists project (OpenFinding,
// DismissedFinding).
type Finding struct {
	ID            string `yaml:"id"`
	File          string `yaml:"file"`
	Line          int    `yaml:"line"`
	Title         string `yaml:"title"`
	Detail        string `yaml:"detail"`
	Action        string `yaml:"action"`
	Risk          string `yaml:"risk"`
	RiskRationale string `yaml:"risk_rationale"`
	Oracle        string `yaml:"oracle,omitempty"`
	Workspace     string `yaml:"workspace,omitempty"`
	Status        string `yaml:"status"`
	Recurrences   int    `yaml:"recurrences"`
	// Triage, Decision and RoutedAs are additive fields beyond what the
	// reviewer reports. ApplyRound (this file) sets RoutedAs when the
	// recurrence bound or a missing build target forces asked despite the
	// reviewer's own label, and carries Decision forward on a recurrence
	// (rule 1); routing and triage (route.go) decide Triage, and Decision
	// for a freshly kept ask, before findings.yaml is persisted.
	Triage   string `yaml:"triage,omitempty"`
	Decision string `yaml:"decision,omitempty"`
	RoutedAs string `yaml:"routed_as,omitempty"`
}

// findingsYAML is gate/round-<n>/findings.yaml's exact on-disk shape, plus
// the round's own summary.
type findingsYAML struct {
	Scope         string    `yaml:"scope"`
	ReviewedPaths []string  `yaml:"reviewed_paths"`
	Findings      []Finding `yaml:"findings"`
	Cleared       []string  `yaml:"cleared,omitempty"`
	Summary       string    `yaml:"summary,omitempty"`
}

// statusForAction maps a reviewer's action label to jig's own status: fix
// queues toward a fix slice (open), ask needs a human (asked), note is
// recorded only and leaves the open set (noted). Every action jig
// recognizes is handled explicitly; ParseReviewResult already rejects any
// other action before a result ever reaches ApplyRound, so an unrecognized
// one here is a programming error, not a case to guess at silently - it is
// returned up the call chain rather than folded into note, which would let
// an unvalidated caller's mistake reach the open set as if it were a
// harmless, non-blocking record.
func statusForAction(action string) (string, error) {
	switch action {
	case ActionFix:
		return StatusOpen, nil
	case ActionAsk:
		return StatusAsked, nil
	case ActionNote:
		return StatusNoted, nil
	default:
		return "", fmt.Errorf("verifydeliver: findings: action %q is not fix, ask or note", action)
	}
}

// workspaceFor derives a finding's workspace: the manifest workspace whose
// path is the longest prefix of file, matched by path
// segment rather than raw string prefix (so "billing" matches
// "billing/x.go" but not "billingx/y.go"). "." (or an empty path) is the
// root workspace and matches every file. The empty string means file lies
// in no declared workspace.
func workspaceFor(file string, man manifest.Manifest) string {
	best, bestLen := "", -1
	for _, ws := range man.Workspaces {
		p := strings.TrimSuffix(strings.ReplaceAll(ws.Path, "\\", "/"), "/")
		var match bool
		var length int
		switch {
		case p == "" || p == ".":
			match, length = true, 0
		case file == p || strings.HasPrefix(file, p+"/"):
			match, length = true, len(p)
		}
		if match && length > bestLen {
			best, bestLen = ws.ID, length
		}
	}
	return best
}

// findingHasGreenFixSlice reports whether any slice in existingSlices both
// records id in its own Findings (the structural link, not a parsed slice
// id) and is green: the recurrence bound's premise is that a recurrence
// means that finding's fix slice went green without resolving it, which can
// only be true once such a slice has actually finished, not merely while it
// is still queued or building (as under --early). sliceGreen reports one
// slice's live state by id.
func findingHasGreenFixSlice(id string, existingSlices []store.Slice, sliceGreen func(sliceID string) (bool, error)) (bool, error) {
	for _, s := range existingSlices {
		for _, fid := range s.Findings {
			if fid != id {
				continue
			}
			green, err := sliceGreen(s.ID)
			if err != nil {
				return false, fmt.Errorf("verifydeliver: findings: read slice %s state: %w", s.ID, err)
			}
			if green {
				return true, nil
			}
		}
	}
	return false, nil
}

// ApplyRound applies one validated reviewer round's result onto known, the
// cumulative fold of every earlier round (rules 1, 2 and 4 below): it
// assigns ids to new findings and resolves recurrences and the recurrence
// bound. It returns this round's own reported findings - every finding
// reported this round - with a provisional Status - routing
// and triage (route.go) still have to run on it, dismissing some and
// keeping others, before it is final. known is untouched. Rule 3's
// clearing is not decided here: ClearingAfterTriage needs reported's final,
// post-triage statuses, so the caller runs it only after routeRound.
//
// existingSlices is the ticket's slices.yaml as of before this round;
// sliceGreen reports one existing slice's live state by id. Together they
// decide whether a re-report is a genuine recurrence: recurrences only
// counts up once a fix slice that already records the finding's id has
// gone green, since that is the premise the recurrence bound rests on - a
// fix slice that finished without resolving it - rather than a finding
// merely reported again before any slice for it was ever built, or while
// one is still queued or building (an undecided ask re-reported, or a
// round run with --early).
func ApplyRound(round int, known map[string]Finding, result ReviewResult, existingSlices []store.Slice, sliceGreen func(sliceID string) (bool, error), man manifest.Manifest) (reported []Finding, err error) {
	seq := 0
	oracleNames := SortedOracleNames(man)

	for _, rf := range result.Findings {
		file, ferr := normalizeRepoRelPath(rf.File)
		if ferr != nil {
			return nil, fmt.Errorf("verifydeliver: findings: finding file %q: %w", rf.File, ferr)
		}

		var id string
		var prior Finding
		var hasPrior bool
		if rf.Prior != "" {
			id = rf.Prior
			prior, hasPrior = known[rf.Prior]
		} else {
			seq++
			id = fmt.Sprintf("r%d-f%d", round, seq)
		}

		if hasPrior && prior.Status == StatusDismissed {
			// Rule 2: a repeat of a dismissed finding stays dismissed. It
			// is not routed and not triaged, so it carries no round's
			// triage decision forward: Triage, Decision and RoutedAs reset
			// to empty rather than restating the round that dismissed it -
			// those fields record who decided this round, and nobody did.
			f := prior
			f.File, f.Line, f.Title, f.Detail = file, rf.Line, rf.Title, rf.Detail
			f.Action, f.Risk, f.RiskRationale, f.Oracle = rf.Action, rf.Risk, rf.RiskRationale, rf.Oracle
			f.Workspace = workspaceFor(file, man)
			f.Triage, f.Decision, f.RoutedAs = "", "", ""
			reported = append(reported, f)
			continue
		}

		// Rule 1: a recurrence keeps its id and starts from its prior
		// occurrence, so every field the rule does not name as replaced
		// survives - most notably Oracle (kept when this round names none)
		// and Decision (the human's decision for an earlier kept ask, kept
		// so a fix slice built from this recurrence can still cite it).
		// Triage and RoutedAs are always this round's own routing, decided
		// afresh below and by routeRound, never carried.
		var f Finding
		recurrences := 0
		if hasPrior {
			f = prior
			recurrences = prior.Recurrences
			hasGreen, gerr := findingHasGreenFixSlice(id, existingSlices, sliceGreen)
			if gerr != nil {
				return nil, gerr
			}
			if hasGreen {
				recurrences = prior.Recurrences + 1
			}
		}
		f.ID = id
		f.File, f.Line, f.Title = file, rf.Line, rf.Title
		f.Detail, f.Action, f.Risk, f.RiskRationale = rf.Detail, rf.Action, rf.Risk, rf.RiskRationale
		if rf.Oracle != "" {
			f.Oracle = rf.Oracle
		}
		// An omitted oracle defaults to the manifest's sole oracle when it
		// has exactly one (there is no choice to make); a recurrence whose
		// prior occurrence never got an oracle resolved this same way
		// falls back to the same default rather than staying empty.
		if resolved, ok := resolveOracle(f.Oracle, oracleNames); ok {
			f.Oracle = resolved
		}
		f.Recurrences = recurrences
		f.Triage, f.RoutedAs = "", ""

		newWorkspace := workspaceFor(file, man)
		// The workspace is jig-derived, not the reviewer's word, so rule 1
		// leaves it alone only when the file is unchanged and still maps to
		// no workspace: then a human already chose one for this same file
		// (routeRound) and that choice survives the recurrence. Any other
		// case - a fresh finding, a changed file, or a file that now does
		// map somewhere - recomputes it.
		if newWorkspace != "" || !hasPrior || file != prior.File || prior.Workspace == "" {
			f.Workspace = newWorkspace
		}

		// Recurrence bound: the second recurrence comes to the human
		// as an ask, whatever this round's label - including note, which is
		// exactly the case that forces an already-oracled finding (rule 1
		// above) to a human decision instead of silently dropping it from
		// the open set.
		status, serr := statusForAction(rf.Action)
		if serr != nil {
			return nil, serr
		}
		if recurrences >= 2 {
			status = StatusAsked
		}
		// A fix finding missing part of its build target - no declared
		// workspace for its file, or no oracle jig can resolve against the
		// current manifest (an oracle recorded before the manifest changed,
		// say) - can never become a fix slice on its own; jig routes it to
		// the human as an ask instead. A zero-oracle manifest
		// is not this case: routeRound fails the whole round with
		// GATE_NO_ORACLE before triage ever runs, so nothing here needs to
		// force individual findings to ask over it.
		missingOracle := len(oracleNames) > 0 && !validOracle(f.Oracle, oracleNames)
		if status == StatusOpen && (f.Workspace == "" || missingOracle) {
			status = StatusAsked
		}
		f.Status = status
		// routed_as: written whenever jig's own status ends up asked
		// although the reviewer labeled this finding something else (fix,
		// via the recurrence bound or a missing build target above); the
		// persisted action always stays the reviewer's own label.
		if status == StatusAsked && rf.Action != ActionAsk {
			f.RoutedAs = ActionAsk
		}

		reported = append(reported, f)
	}

	return reported, nil
}

// ClearingAfterTriage computes rule 3's clearing set: known's open/asked
// findings not present in reported (id-wise) clear when this
// round reviewed their file, or their file no longer exists at head
// (existsAtHead checks the lease directly, whatever the scope diff says -
// a file gone before this round's base, or never in a full-scope diff
// after a rebase, clears a finding on it just the same). Called only after
// routing and triage (route.go) have set reported's final status: the
// blocking set - a file this round ends up routing a finding into, open or
// asked - must reflect what a human actually kept, not merely what the
// reviewer reported before triage dismissed some of it (a finding the
// human dismisses at triage must not go on blocking an unrelated open
// finding in the same file from clearing).
func ClearingAfterTriage(known map[string]Finding, reported []Finding, reviewedPaths []string, existsAtHead func(file string) (bool, error)) (cleared []string, err error) {
	reviewedSet := map[string]bool{}
	for _, p := range reviewedPaths {
		norm, err := normalizeRepoRelPath(p)
		if err != nil {
			continue
		}
		reviewedSet[norm] = true
	}

	reportedIDs := map[string]bool{}
	blocking := map[string]bool{}
	for _, f := range reported {
		reportedIDs[f.ID] = true
		if f.Status == StatusOpen || f.Status == StatusAsked {
			blocking[f.File] = true
		}
	}

	for id, f := range known {
		if f.Status != StatusOpen && f.Status != StatusAsked {
			continue
		}
		if reportedIDs[id] {
			continue
		}
		if blocking[f.File] {
			continue
		}
		if reviewedSet[f.File] {
			cleared = append(cleared, id)
			continue
		}
		exists, eerr := existsAtHead(f.File)
		if eerr != nil {
			return nil, fmt.Errorf("verifydeliver: findings: check %q at head: %w", f.File, eerr)
		}
		if !exists {
			cleared = append(cleared, id)
		}
	}
	sort.Strings(cleared)

	return cleared, nil
}

// outstandingAsks returns cum's still-asked findings that this round's
// reviewer did not report again (by id), sorted by risk then id for
// deterministic triage ordering. routeRound offers these to triage
// alongside this round's own asks, so an outstanding ask is decidable
// every round it stays undecided, not only when the reviewer happens to
// report it again.
func outstandingAsks(cum map[string]Finding, reported []Finding) []Finding {
	reportedIDs := make(map[string]bool, len(reported))
	for _, f := range reported {
		reportedIDs[f.ID] = true
	}
	var out []Finding
	for id, f := range cum {
		if f.Status != StatusAsked || reportedIDs[id] {
			continue
		}
		out = append(out, f)
	}
	sortByRiskThenID(out)
	return out
}

// foldFindings applies one round's findings.yaml content onto cum in
// place: the latest occurrence of each id wins, and cleared
// removes an id. It is the single operation both the historical fold
// (cumulativeFindings) and a round just applied (ApplyRound's result) use,
// so the two always agree.
func foldFindings(cum map[string]Finding, findings []Finding, cleared []string) {
	for _, f := range findings {
		cum[f.ID] = f
	}
	for _, id := range cleared {
		delete(cum, id)
	}
}

// cloneFindings returns a shallow copy of cum, so a caller can fold a
// speculative round onto it without mutating the original.
func cloneFindings(cum map[string]Finding) map[string]Finding {
	out := make(map[string]Finding, len(cum))
	for id, f := range cum {
		out[id] = f
	}
	return out
}

// isClean reports whether cum has no finding open or asked: a round is
// clean when, after applying it, nothing is outstanding. Notes,
// dismissed findings and cleared findings never block it.
func isClean(cum map[string]Finding) bool {
	for _, f := range cum {
		if f.Status == StatusOpen || f.Status == StatusAsked {
			return false
		}
	}
	return true
}

// openFindingsList returns cum's open and asked findings (the open set),
// sorted by id for determinism.
func openFindingsList(cum map[string]Finding) []Finding {
	var out []Finding
	for _, f := range cum {
		if f.Status == StatusOpen || f.Status == StatusAsked {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// openAndNotedFindingsList returns cum's open, asked and noted findings,
// sorted by id for determinism: review.json's own Open list (built from
// this, not openFindingsList). A noted finding left the open set (it is
// not outstanding work and never blocks clean or forces its file into
// must_review - review.go's Round filters this list back down to
// open/asked for both), but it must stay a citable `prior` target, or its
// identity and recurrence count are lost the moment a round notes it:
// prior is the only structural channel a later round has to continue the
// same finding, and the recurrence bound (ApplyRound's rule 1) must apply
// to that finding whatever label it wore in between, not reset because a
// note interrupted it.
func openAndNotedFindingsList(cum map[string]Finding) []Finding {
	var out []Finding
	for _, f := range cum {
		if f.Status == StatusOpen || f.Status == StatusAsked || f.Status == StatusNoted {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// dismissedFindingsList returns cum's dismissed findings, sorted by id for
// determinism.
func dismissedFindingsList(cum map[string]Finding) []Finding {
	var out []Finding
	for _, f := range cum {
		if f.Status == StatusDismissed {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// askedFindingsList returns cum's asked findings, sorted by id for
// determinism: the "needs a human" list (the exit-2 signal), across every
// round, not only the one just applied.
func askedFindingsList(cum map[string]Finding) []Finding {
	var out []Finding
	for _, f := range cum {
		if f.Status == StatusAsked {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// toOpenFindingList projects fs (cum's open/asked/noted findings, per
// openAndNotedFindingsList) onto review.json's open shape. Building fs from
// the cumulative fold, and calling this before every reviewer round, is
// what keeps review.json's open list honest.
func toOpenFindingList(fs []Finding) []OpenFinding {
	out := make([]OpenFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, OpenFinding{ID: f.ID, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail, Action: f.Action, Recurrences: f.Recurrences})
	}
	return out
}

// toDismissedFindingList projects fs (cum's dismissed findings) onto
// review.json's dismissed shape.
func toDismissedFindingList(fs []Finding) []DismissedFinding {
	out := make([]DismissedFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, DismissedFinding{ID: f.ID, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail})
	}
	return out
}

// readFindingsYAML reads gate/round-<n>/findings.yaml for ticket. ok is
// false, with no error, when the round has no findings.yaml (a scripted
// round, or one that hasn't happened yet).
func readFindingsYAML(st *store.Store, ticket string, round int) (findingsYAML, bool, error) {
	data, err := os.ReadFile(filepath.Join(gateRoundDir(st, ticket, round), "findings.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return findingsYAML{}, false, nil
		}
		return findingsYAML{}, false, err
	}
	var ff findingsYAML
	if err := yaml.Unmarshal(data, &ff); err != nil {
		return findingsYAML{}, false, err
	}
	return ff, true, nil
}

// cumulativeFindings folds every gate/round-<n>/findings.yaml for ticket,
// from round 1 up to (not including) upToRound, into one map: the latest
// occurrence of each id wins, and a round's cleared list
// removes it. A round with no findings.yaml (a scripted round, or one that
// hasn't happened yet) contributes nothing.
func cumulativeFindings(st *store.Store, ticket string, upToRound int) (map[string]Finding, error) {
	cum := map[string]Finding{}
	for r := 1; r < upToRound; r++ {
		ff, ok, err := readFindingsYAML(st, ticket, r)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: findings: read round %d findings.yaml: %w", r, err)
		}
		if !ok {
			continue
		}
		foldFindings(cum, ff.Findings, ff.Cleared)
	}
	return cum, nil
}

// OutstandingAsks returns ticket's cumulative still-asked findings, sorted
// by risk then id, across every gate round recorded so far. It is the
// state `jig status` shows between rounds so a waiting decision is
// visible even when no gate round is running: the same cumulative fold
// Gate itself uses (cumulativeFindings), read one round past the last one
// recorded.
func OutstandingAsks(st *store.Store, ticket string) ([]Finding, error) {
	n, err := existingGateRounds(st, ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: findings: count rounds: %w", err)
	}
	cum, err := cumulativeFindings(st, ticket, n+1)
	if err != nil {
		return nil, err
	}
	asks := askedFindingsList(cum)
	sortByRiskThenID(asks)
	return asks, nil
}

// marshalFindingsYAML renders one round's findings.yaml, marshaling nil
// reviewedPaths/findings as [] rather than null.
func marshalFindingsYAML(scope string, reviewedPaths []string, findings []Finding, cleared []string, summary string) ([]byte, error) {
	if reviewedPaths == nil {
		reviewedPaths = []string{}
	}
	if findings == nil {
		findings = []Finding{}
	}
	return yaml.Marshal(findingsYAML{
		Scope:         scope,
		ReviewedPaths: reviewedPaths,
		Findings:      findings,
		Cleared:       cleared,
		Summary:       summary,
	})
}

// riskRank orders findings.md's sections, high risk first.
var riskRank = map[string]int{RiskHigh: 0, RiskMedium: 1, RiskLow: 2}

// renderFindingsMD renders one round's findings.md, sorted by risk, high
// first. verdict is the round's own verdict (clean|fix-slices,
// GateReport.Verdict): the word "clean" is only ever printed when verdict
// itself is clean, never merely because this round reported nothing new -
// an earlier round's finding can still be open or asked with nothing new
// reported against it this round. cleared lists the ids this round cleared
// (rule 3); findings is every finding this round reported (ApplyRound's
// "reported"), after routing and triage have set each one's final Status,
// Triage, Decision and RoutedAs.
func renderFindingsMD(round int, verdict, summary string, findings []Finding, cleared []string) string {
	sorted := make([]Finding, len(findings))
	copy(sorted, findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := riskRank[sorted[i].Risk], riskRank[sorted[j].Risk]
		if ri != rj {
			return ri < rj
		}
		return sorted[i].ID < sorted[j].ID
	})
	clearedSorted := make([]string, len(cleared))
	copy(clearedSorted, cleared)
	sort.Strings(clearedSorted)

	var b strings.Builder
	fmt.Fprintf(&b, "# Gate round %d\n\n", round)
	fmt.Fprintf(&b, "verdict: %s\n\n", verdict)
	if summary != "" {
		fmt.Fprintf(&b, "%s\n\n", summary)
	}
	if len(sorted) == 0 && len(clearedSorted) == 0 {
		if verdict == "clean" {
			b.WriteString("clean\n")
		} else {
			b.WriteString("nothing new this round\n")
		}
		return b.String()
	}
	for _, f := range sorted {
		fmt.Fprintf(&b, "## %s (%s, %s): %s\n\n", f.ID, f.Risk, f.Status, f.Title)
		fmt.Fprintf(&b, "- file: %s:%d\n", f.File, f.Line)
		fmt.Fprintf(&b, "- action: %s\n", f.Action)
		if f.Oracle != "" {
			fmt.Fprintf(&b, "- oracle: %s\n", f.Oracle)
		}
		if f.Workspace != "" {
			fmt.Fprintf(&b, "- workspace: %s\n", f.Workspace)
		}
		if f.Recurrences > 0 {
			fmt.Fprintf(&b, "- recurrences: %d\n", f.Recurrences)
		}
		if f.Triage != "" {
			fmt.Fprintf(&b, "- triage: %s\n", f.Triage)
		}
		if f.Decision != "" {
			fmt.Fprintf(&b, "- decision: %s\n", f.Decision)
		}
		if f.RoutedAs != "" {
			fmt.Fprintf(&b, "- routed as: %s\n", f.RoutedAs)
		}
		fmt.Fprintf(&b, "- risk rationale: %s\n\n", f.RiskRationale)
		if f.Detail != "" {
			fmt.Fprintf(&b, "%s\n\n", f.Detail)
		}
	}
	if len(clearedSorted) > 0 {
		fmt.Fprintf(&b, "cleared: %s\n", strings.Join(clearedSorted, ", "))
	}
	return b.String()
}
