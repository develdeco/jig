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

// Finding status vocabulary (design 5.5): a finding's place in jig's own
// bookkeeping, distinct from the reviewer's action label. open and asked
// together are "the open set" (design 5.2): findings still outstanding
// across rounds. dismissed and noted are terminal for this bookkeeping;
// only a human dismissal (routing, design 6) or a repeat of an already
// dismissed finding (rule 2) produces dismissed.
const (
	StatusOpen      = "open"
	StatusAsked     = "asked"
	StatusDismissed = "dismissed"
	StatusNoted     = "noted"
)

// Triage vocabulary (Q2, design 5.5): who decided a finding's outcome -
// a person at a terminal, or --yes / no terminal.
const (
	TriageHuman = "human"
	TriageAuto  = "auto"
)

// Finding is one persisted entry of gate/round-N/findings.yaml (design
// 5.5), plus Q2's additive fields (triage, decision, routed_as). It is
// jig's own bookkeeping record: the reviewer never sees it directly, only
// the subset review.json's open and dismissed lists project (OpenFinding,
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
	// Triage, Decision and RoutedAs are Q2's additive fields. jig's
	// bookkeeping (this file) never sets them; routing and triage
	// (design 6, a later stage) do, before findings.yaml is persisted.
	Triage   string `yaml:"triage,omitempty"`
	Decision string `yaml:"decision,omitempty"`
	RoutedAs string `yaml:"routed_as,omitempty"`
}

// findingsYAML is gate/round-<n>/findings.yaml's exact on-disk shape
// (design 5.5, plus Q2's round summary).
type findingsYAML struct {
	Scope         string    `yaml:"scope"`
	ReviewedPaths []string  `yaml:"reviewed_paths"`
	Findings      []Finding `yaml:"findings"`
	Cleared       []string  `yaml:"cleared,omitempty"`
	Summary       string    `yaml:"summary,omitempty"`
}

// statusForAction maps a reviewer's action label to jig's own status
// (design 5.2, 5.5): fix queues toward a fix slice (open), ask needs a
// human (asked), note is recorded only and leaves the open set (noted).
func statusForAction(action string) string {
	switch action {
	case ActionFix:
		return StatusOpen
	case ActionAsk:
		return StatusAsked
	default:
		return StatusNoted
	}
}

// workspaceFor derives a finding's workspace (design 5.5): the manifest
// workspace whose path is the longest prefix of file, matched by path
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

// ApplyRound applies one validated reviewer round's result onto known, the
// cumulative fold of every earlier round (design 5.2, Q6, Q7): it assigns
// ids to new findings, resolves recurrences and the recurrence bound, and
// reports which earlier open findings clear. It returns this round's own
// findings.yaml content: reported is every finding actually reported this
// round (design 5.5's "every finding reported this round"), cleared is the
// ids of earlier open findings this round clears. known is untouched;
// folding reported and cleared onto it (foldFindings) is the caller's job,
// so the same round can be applied speculatively (Q8) without committing
// it.
//
// deleted is this round's scope-diff deleted-file list (Review.Deleted):
// rule 3 treats a deleted file as reviewed, since must_review never
// requires covering a file that no longer exists.
func ApplyRound(round int, known map[string]Finding, result ReviewResult, deleted []string, man manifest.Manifest) (reported []Finding, cleared []string) {
	reviewedSet := map[string]bool{}
	for _, p := range result.ReviewedPaths {
		norm, err := normalizeRepoRelPath(p)
		if err != nil {
			continue
		}
		reviewedSet[norm] = true
	}
	deletedSet := map[string]bool{}
	for _, p := range deleted {
		norm, err := normalizeRepoRelPath(p)
		if err != nil {
			norm = p
		}
		deletedSet[norm] = true
	}

	// blocking (Q6): a file this round reports a routed (fix or ask)
	// finding in. A repeat of a dismissed finding (rule 2) and a note
	// never block, so they are never added here.
	blocking := map[string]bool{}
	reportedIDs := map[string]bool{}
	seq := 0

	for _, rf := range result.Findings {
		file, err := normalizeRepoRelPath(rf.File)
		if err != nil {
			file = rf.File // already validated upstream; defensive only
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
		reportedIDs[id] = true

		if hasPrior && prior.Status == StatusDismissed {
			// Rule 2: a repeat of a dismissed finding stays dismissed. It
			// is not routed and not triaged.
			f := prior
			f.File, f.Line, f.Title, f.Detail = file, rf.Line, rf.Title, rf.Detail
			f.Action, f.Risk, f.RiskRationale, f.Oracle = rf.Action, rf.Risk, rf.RiskRationale, rf.Oracle
			f.Workspace = workspaceFor(file, man)
			reported = append(reported, f)
			continue
		}

		recurrences := 0
		if hasPrior {
			recurrences = prior.Recurrences + 1
		}

		// Recurrence bound (5.3, Q7): the second recurrence comes to the
		// human as an ask, whatever the reviewer's label. A finding can
		// only recur through prior, and prior may only name an id under
		// open or dismissed (validated upstream), so a note - which
		// leaves the open set on its very first occurrence - can never
		// legitimately be the target of a second recurrence; applying the
		// bound unconditionally here is equivalent to applying it only
		// when the label is fix, and simpler.
		status := statusForAction(rf.Action)
		if recurrences >= 2 {
			status = StatusAsked
		}
		workspace := workspaceFor(file, man)
		// Q1: a fix finding whose file lies in no declared workspace has
		// no build target, so it can never become a fix slice on its own;
		// jig routes it to the human as an ask instead (design 5.5).
		if status == StatusOpen && workspace == "" {
			status = StatusAsked
		}
		// routed_as (Q2): written whenever jig's own status ends up asked
		// although the reviewer labeled this finding something else
		// (fix, via the recurrence bound or the no-workspace rule above);
		// the persisted action always stays the reviewer's own label.
		var routedAs string
		if status == StatusAsked && rf.Action != ActionAsk {
			routedAs = ActionAsk
		}
		if status == StatusOpen || status == StatusAsked {
			blocking[file] = true
		}

		reported = append(reported, Finding{
			ID: id, File: file, Line: rf.Line, Title: rf.Title, Detail: rf.Detail,
			Action: rf.Action, Risk: rf.Risk, RiskRationale: rf.RiskRationale,
			Oracle: rf.Oracle, Workspace: workspace,
			Status: status, Recurrences: recurrences, RoutedAs: routedAs,
		})
	}

	// Rule 3: an open finding not reported again clears when this round
	// reviewed its file (or the file no longer exists at head) and this
	// round's routed findings don't block it (Q6). Otherwise it stays
	// open (or asked), unchanged, and is simply absent from this round's
	// own findings list (the fold, not this function, carries it forward).
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
		if reviewedSet[f.File] || deletedSet[f.File] {
			cleared = append(cleared, id)
		}
	}
	sort.Strings(cleared)

	return reported, cleared
}

// foldFindings applies one round's findings.yaml content onto cum in
// place (design 5.5): the latest occurrence of each id wins, and cleared
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

// isClean reports whether cum has no finding open or asked (design 5.4):
// a round is clean when, after applying it, nothing is outstanding. Notes,
// dismissed findings and cleared findings never block it.
func isClean(cum map[string]Finding) bool {
	for _, f := range cum {
		if f.Status == StatusOpen || f.Status == StatusAsked {
			return false
		}
	}
	return true
}

// openFindingsList returns cum's open and asked findings (design 5.2's
// open set), sorted by id for determinism.
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
// determinism: design 6.4's "needs a human" list (Q1's exit-2 signal),
// across every round, not only the one just applied.
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

// sortedFindingsByID returns a copy of fs sorted by id, for deterministic
// display (GateReport.Findings).
func sortedFindingsByID(fs []Finding) []Finding {
	out := make([]Finding, len(fs))
	copy(out, fs)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// toOpenFindingList projects fs (cum's open/asked findings) onto
// review.json's open shape (design 4.1). Building fs from the cumulative
// fold, and calling this before every reviewer round, is what keeps
// review.json's open list honest.
func toOpenFindingList(fs []Finding) []OpenFinding {
	out := make([]OpenFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, OpenFinding{ID: f.ID, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail, Action: f.Action, Recurrences: f.Recurrences})
	}
	return out
}

// toDismissedFindingList projects fs (cum's dismissed findings) onto
// review.json's dismissed shape (design 4.1).
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
// from round 1 up to (not including) upToRound, into one map (design 5.5):
// the latest occurrence of each id wins, and a round's cleared list
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

// marshalFindingsYAML renders one round's findings.yaml (design 5.5),
// marshaling nil reviewedPaths/findings as [] rather than null.
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

// riskRank orders findings.md's sections, high risk first (design 5.5).
var riskRank = map[string]int{RiskHigh: 0, RiskMedium: 1, RiskLow: 2}

// renderFindingsMD renders one round's findings.md (design 5.5), sorted by
// risk, high first. verdict is the round's own verdict (clean|fix-slices,
// GateReport.Verdict): the word "clean" is only ever printed when verdict
// itself is clean, never merely because this round reported nothing new -
// an earlier round's finding can still be open or asked with nothing new
// reported against it this round. cleared lists the ids this round cleared
// (design 5.2 rule 3); findings is every finding this round reported
// (ApplyRound's "reported"), after routing and triage (design 6) have set
// each one's final Status, Triage, Decision and RoutedAs.
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
