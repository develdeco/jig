// Package revieweval scores the gate reviewer's output against a labeled
// corpus: for each case, one or more rounds of a diff applied to a small
// throwaway repo, a brief, and a gold.yaml of findings a correct review
// must find and traps it must not flag. It drives the reviewer through the
// SAME code path Gate itself uses - verifydeliver.NewReviewerGateSource's
// Round, verifydeliver.ApplyRound and verifydeliver.ClearingAfterTriage,
// reading a round's history from the store's own gate/round-N/findings.yaml
// the way verifydeliver.FoldBefore does - so a corpus run measures the real
// reviewer contract and the real bookkeeping it feeds, never a stand-in.
//
// There is no triage step: this package never calls the routing hook that
// turns a kept fix or ask into a fix slice. A round's fate is exactly what
// ApplyRound's own status assignment leaves it - the same status a fresh
// finding carries before any human or --yes hook ever runs - because
// folding jig's own default triage policy (which asks to auto-approve,
// which fixes to queue) into a review-quality measurement would score the
// repo's triage defaults, not the reviewer.
//
// Multi-round cases are teacher-forced: round N's fold always comes from
// the case's own recorded round-(N-1)/findings.yaml, copied into the store
// before the round runs, never from what this run's own reviewer said in
// round N-1. Every round then tests the behavior it was built for,
// whatever an earlier round's live result was; free-running drift across
// rounds is out of scope for this package.
//
// Matching is structural, never a regexp over the reviewer's prose: same
// file, then the finding's line inside the gold span widened by a few
// lines on each side, then a judge (match.go). Findings beyond the seeded
// gold are labeled from recorded human decisions: kept is a true positive,
// dismissed is a false positive, anything nobody decided stays pending.
package revieweval

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/verifydeliver"
	"gopkg.in/yaml.v3"
)

// Decision vocabulary: a human's verdict on a finding beyond the seeded
// gold, recorded in a round's decisions.yaml.
const (
	DecisionKept      = "kept"
	DecisionDismissed = "dismissed"
)

// GoldFinding is one problem a round's patch seeds on purpose: the review
// must find it, at Action, matching this package's structural rules
// (match.go).
type GoldFinding struct {
	ID          string
	File        string
	From, To    int    // the seeded span, 1-based, inclusive
	Action      string // fix|ask|note: the action a correct review gives it
	Prior       string // optional: the recorded id a true repeat must cite
	Description string
}

// GoldTrap is one span a round's patch seeds on purpose that is correct
// code built to tempt a false alarm: matching it is never a point in the
// review's favor.
type GoldTrap struct {
	ID          string
	File        string
	From, To    int
	Description string
}

// Gold is one round's gold.yaml: the seeded truth its result is matched
// against. Exhaustive marks a round whose Findings list is every problem
// in its diff, so an unmatched finding with no other explanation
// (match.go's classifyUnmatched, rule 6) is a false alarm rather than
// merely unlabeled.
type Gold struct {
	Exhaustive bool
	Findings   []GoldFinding
	Traps      []GoldTrap
}

// Decision is one recorded human decision on a finding beyond the seeded
// gold: a real reviewer round raised it and a person decided it, kept (a
// true positive) or dismissed (a false positive).
type Decision struct {
	ID          string
	File        string
	From, To    int
	Description string
	Decision    string // DecisionKept | DecisionDismissed
}

// recordedRoundYAML is gate/round-N/findings.yaml's on-disk shape: the
// store's own findingsYAML (internal/verifydeliver/findings.go), mirrored
// field for field so a case's recorded history round-trips through the
// same YAML the store persists. Read here only to validate a later round's
// gold "prior" at load time and to seed the runner's store history; never
// re-derived through any fold of its own - RunCase always folds it back
// through the real verifydeliver.FoldBefore once it is filed under the
// store, so this package and Gate always agree on what a round starts
// from.
type recordedRoundYAML struct {
	Scope         string                  `yaml:"scope"`
	ReviewedPaths []string                `yaml:"reviewed_paths"`
	Findings      []verifydeliver.Finding `yaml:"findings"`
	Cleared       []string                `yaml:"cleared,omitempty"`
	Summary       string                  `yaml:"summary,omitempty"`
}

// Round is one case round: the patch applied on top of the previous
// round's head, this round's gold and optional decisions, and - on every
// round but the last - the findings.yaml jig recorded for it.
type Round struct {
	N            int
	Dir          string // <case dir>/round-<N>
	PatchPath    string
	Gold         Gold
	Decisions    []Decision
	FindingsPath string             // round-N/findings.yaml; "" on the last round
	Recorded     *recordedRoundYAML // parsed FindingsPath; nil when FindingsPath is ""
}

// Case is one loaded corpus case: a brief and one or more rounds in order,
// each a patch applied on top of the last plus that round's own gold.
type Case struct {
	Name      string
	Dir       string
	BriefPath string
	Rounds    []Round
}

// caseErrorf formats a loader error naming the case, the round (0 for a
// case-level rule such as a missing brief.md) and the problem.
func caseErrorf(name string, round int, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if round == 0 {
		return fmt.Errorf("revieweval: case %s: %s", name, msg)
	}
	return fmt.Errorf("revieweval: case %s round %d: %s", name, round, msg)
}

// yamlKnownFields decodes data strictly into v: an unknown key (a
// "title_pattern" left over from an older corpus shape, say) is an error,
// never silently ignored. An empty file decodes to v's zero value.
func yamlKnownFields(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	return nil
}

// validCaseFile reports whether p is a clean, repo-relative forward-slash
// path: non-empty, no backslash, not absolute, no ".." segment, and
// already in path.Clean form (so a hand-authored "./x" or "a//b" is
// rejected rather than silently accepted).
func validCaseFile(p string) error {
	if p == "" {
		return fmt.Errorf("file is empty")
	}
	if strings.Contains(p, "\\") {
		return fmt.Errorf("file %q must use \"/\", not \"\\\\\"", p)
	}
	if path.IsAbs(p) {
		return fmt.Errorf("file %q is absolute", p)
	}
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		return fmt.Errorf("file %q is an absolute Windows path", p)
	}
	clean := path.Clean(p)
	if clean != p || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("file %q is not a clean repo-relative path", p)
	}
	return nil
}

// validLines validates a gold.yaml/decisions.yaml "lines" entry: exactly
// two positive integers, from <= to.
func validLines(lines []int) (from, to int, err error) {
	if len(lines) != 2 {
		return 0, 0, fmt.Errorf("lines must be exactly two integers, got %d", len(lines))
	}
	from, to = lines[0], lines[1]
	if from <= 0 || to <= 0 {
		return 0, 0, fmt.Errorf("lines %v must both be positive", lines)
	}
	if from > to {
		return 0, 0, fmt.Errorf("lines %v: from must be <= to", lines)
	}
	return from, to, nil
}

// goldWire, goldFindingWire, goldTrapWire, decisionsWire and decisionWire
// are gold.yaml's and decisions.yaml's strict decode targets, before their
// "lines" field is validated and split into From/To.
type goldWire struct {
	Exhaustive bool              `yaml:"exhaustive"`
	Findings   []goldFindingWire `yaml:"findings"`
	Traps      []goldTrapWire    `yaml:"traps"`
}

type goldFindingWire struct {
	ID          string `yaml:"id"`
	File        string `yaml:"file"`
	Lines       []int  `yaml:"lines"`
	Action      string `yaml:"action"`
	Prior       string `yaml:"prior"`
	Description string `yaml:"description"`
}

type goldTrapWire struct {
	ID          string `yaml:"id"`
	File        string `yaml:"file"`
	Lines       []int  `yaml:"lines"`
	Description string `yaml:"description"`
}

type decisionsWire struct {
	Decisions []decisionWire `yaml:"decisions"`
}

type decisionWire struct {
	ID          string `yaml:"id"`
	File        string `yaml:"file"`
	Lines       []int  `yaml:"lines"`
	Description string `yaml:"description"`
	Decision    string `yaml:"decision"`
}

// checkUniqueEntryID validates one gold finding's, trap's or decision's id:
// non-empty and not already used by another entry of this same round
// (findings, traps and decisions together share one id space, so a
// "prior" or a fold lookup is never ambiguous about which one it names).
func checkUniqueEntryID(name string, round int, seen map[string]bool, kind, id string) error {
	if id == "" {
		return caseErrorf(name, round, "%s has an empty id", kind)
	}
	if seen[id] {
		return caseErrorf(name, round, "id %q is used more than once", id)
	}
	seen[id] = true
	return nil
}

// foldRecordedIDs folds every already-loaded earlier round's Recorded
// findings (latest occurrence wins, a later Cleared id removed) into one
// set, so LoadCase can validate a gold finding's "prior" against exactly
// the ids a fold entering this round would offer - open, asked or noted -
// without depending on a store or reimplementing verifydeliver's own fold.
func foldRecordedIDs(earlier []Round) map[string]verifydeliver.Finding {
	known := map[string]verifydeliver.Finding{}
	for _, r := range earlier {
		if r.Recorded == nil {
			continue
		}
		for _, f := range r.Recorded.Findings {
			known[f.ID] = f
		}
		for _, id := range r.Recorded.Cleared {
			delete(known, id)
		}
	}
	return known
}

// priorIsCitable reports whether id names an open, asked or noted finding
// in known: the only statuses a later round may legitimately cite as a
// true repeat's prior (a dismissed finding is cited by re-litigating it,
// never by "prior", and any other id simply does not exist yet).
func priorIsCitable(known map[string]verifydeliver.Finding, id string) bool {
	f, ok := known[id]
	if !ok {
		return false
	}
	switch f.Status {
	case verifydeliver.StatusOpen, verifydeliver.StatusAsked, verifydeliver.StatusNoted:
		return true
	default:
		return false
	}
}

// loadGold reads and strictly validates round n's gold.yaml.
func loadGold(name string, n int, goldPath string, fold map[string]verifydeliver.Finding) (Gold, error) {
	data, err := os.ReadFile(goldPath)
	if err != nil {
		return Gold{}, caseErrorf(name, n, "read gold.yaml: %v", err)
	}
	var wire goldWire
	if err := yamlKnownFields(data, &wire); err != nil {
		return Gold{}, caseErrorf(name, n, "parse gold.yaml: %v", err)
	}

	seen := map[string]bool{}
	var g Gold
	g.Exhaustive = wire.Exhaustive
	for _, fw := range wire.Findings {
		if err := checkUniqueEntryID(name, n, seen, "gold finding", fw.ID); err != nil {
			return Gold{}, err
		}
		from, to, err := validLines(fw.Lines)
		if err != nil {
			return Gold{}, caseErrorf(name, n, "gold finding %s: %v", fw.ID, err)
		}
		switch fw.Action {
		case verifydeliver.ActionFix, verifydeliver.ActionAsk, verifydeliver.ActionNote:
		default:
			return Gold{}, caseErrorf(name, n, "gold finding %s: action %q is not fix, ask or note", fw.ID, fw.Action)
		}
		if strings.TrimSpace(fw.Description) == "" {
			return Gold{}, caseErrorf(name, n, "gold finding %s: empty description", fw.ID)
		}
		if err := validCaseFile(fw.File); err != nil {
			return Gold{}, caseErrorf(name, n, "gold finding %s: %v", fw.ID, err)
		}
		if fw.Prior != "" && !priorIsCitable(fold, fw.Prior) {
			return Gold{}, caseErrorf(name, n, "gold finding %s: prior %q does not name an open, asked or noted finding in the fold entering this round", fw.ID, fw.Prior)
		}
		g.Findings = append(g.Findings, GoldFinding{
			ID: fw.ID, File: fw.File, From: from, To: to,
			Action: fw.Action, Prior: fw.Prior, Description: fw.Description,
		})
	}
	for _, tw := range wire.Traps {
		if err := checkUniqueEntryID(name, n, seen, "trap", tw.ID); err != nil {
			return Gold{}, err
		}
		from, to, err := validLines(tw.Lines)
		if err != nil {
			return Gold{}, caseErrorf(name, n, "trap %s: %v", tw.ID, err)
		}
		if strings.TrimSpace(tw.Description) == "" {
			return Gold{}, caseErrorf(name, n, "trap %s: empty description", tw.ID)
		}
		if err := validCaseFile(tw.File); err != nil {
			return Gold{}, caseErrorf(name, n, "trap %s: %v", tw.ID, err)
		}
		g.Traps = append(g.Traps, GoldTrap{ID: tw.ID, File: tw.File, From: from, To: to, Description: tw.Description})
	}
	return g, nil
}

// loadDecisions reads and strictly validates round n's optional
// decisions.yaml; absent is not an error (nil, nil).
func loadDecisions(name string, n int, decisionsPath string, seen map[string]bool) ([]Decision, error) {
	data, err := os.ReadFile(decisionsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, caseErrorf(name, n, "read decisions.yaml: %v", err)
	}
	var wire decisionsWire
	if err := yamlKnownFields(data, &wire); err != nil {
		return nil, caseErrorf(name, n, "parse decisions.yaml: %v", err)
	}
	var out []Decision
	for _, dw := range wire.Decisions {
		if err := checkUniqueEntryID(name, n, seen, "decision", dw.ID); err != nil {
			return nil, err
		}
		from, to, err := validLines(dw.Lines)
		if err != nil {
			return nil, caseErrorf(name, n, "decision %s: %v", dw.ID, err)
		}
		switch dw.Decision {
		case DecisionKept, DecisionDismissed:
		default:
			return nil, caseErrorf(name, n, "decision %s: decision %q is not %q or %q", dw.ID, dw.Decision, DecisionKept, DecisionDismissed)
		}
		if strings.TrimSpace(dw.Description) == "" {
			return nil, caseErrorf(name, n, "decision %s: empty description", dw.ID)
		}
		if err := validCaseFile(dw.File); err != nil {
			return nil, caseErrorf(name, n, "decision %s: %v", dw.ID, err)
		}
		out = append(out, Decision{ID: dw.ID, File: dw.File, From: from, To: to, Description: dw.Description, Decision: dw.Decision})
	}
	return out, nil
}

// roundNumbers returns dir's round-<n> subdirectory numbers, ascending,
// erroring on a malformed name or a gap (rounds are numbered from 1 with
// no gap).
func roundNumbers(name, dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, caseErrorf(name, 0, "read case dir: %v", err)
	}
	var nums []int
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "round-") {
			continue
		}
		suffix := strings.TrimPrefix(e.Name(), "round-")
		n, err := strconv.Atoi(suffix)
		if err != nil || n <= 0 || strconv.Itoa(n) != suffix {
			return nil, caseErrorf(name, 0, "round directory %q is not \"round-<positive integer>\"", e.Name())
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	if len(nums) == 0 {
		return nil, caseErrorf(name, 0, "has no round directories")
	}
	for i, n := range nums {
		if n != i+1 {
			return nil, caseErrorf(name, 0, "round directories are not numbered from 1 with no gap: got %v", nums)
		}
	}
	return nums, nil
}

// LoadCase loads one case directory: brief.md at its root, then round-1,
// round-2, ... in order, each with patch.diff and gold.yaml required,
// decisions.yaml optional, and findings.yaml required on every round but
// the last (forbidden on the last). Every rule below fails naming the
// case, the round, and the entry.
func LoadCase(dir string) (Case, error) {
	name := filepath.Base(dir)
	c := Case{Name: name, Dir: dir, BriefPath: filepath.Join(dir, "brief.md")}
	if _, err := os.Stat(c.BriefPath); err != nil {
		return Case{}, caseErrorf(name, 0, "missing brief.md: %v", err)
	}

	nums, err := roundNumbers(name, dir)
	if err != nil {
		return Case{}, err
	}

	for _, n := range nums {
		roundDir := filepath.Join(dir, fmt.Sprintf("round-%d", n))
		patchPath := filepath.Join(roundDir, "patch.diff")
		if _, err := os.Stat(patchPath); err != nil {
			return Case{}, caseErrorf(name, n, "missing patch.diff: %v", err)
		}
		goldPath := filepath.Join(roundDir, "gold.yaml")
		if _, err := os.Stat(goldPath); err != nil {
			return Case{}, caseErrorf(name, n, "missing gold.yaml: %v", err)
		}

		fold := foldRecordedIDs(c.Rounds)
		gold, err := loadGold(name, n, goldPath, fold)
		if err != nil {
			return Case{}, err
		}

		seenIDs := map[string]bool{}
		for _, g := range gold.Findings {
			seenIDs[g.ID] = true
		}
		for _, t := range gold.Traps {
			seenIDs[t.ID] = true
		}
		decisions, err := loadDecisions(name, n, filepath.Join(roundDir, "decisions.yaml"), seenIDs)
		if err != nil {
			return Case{}, err
		}

		isLast := n == nums[len(nums)-1]
		findingsPath := filepath.Join(roundDir, "findings.yaml")
		_, statErr := os.Stat(findingsPath)
		hasFindings := statErr == nil
		switch {
		case isLast && hasFindings:
			return Case{}, caseErrorf(name, n, "findings.yaml is present on the last round")
		case !isLast && !hasFindings:
			return Case{}, caseErrorf(name, n, "findings.yaml is missing (required on every round but the last)")
		}

		r := Round{N: n, Dir: roundDir, PatchPath: patchPath, Gold: gold, Decisions: decisions}
		if hasFindings {
			data, err := os.ReadFile(findingsPath)
			if err != nil {
				return Case{}, caseErrorf(name, n, "read findings.yaml: %v", err)
			}
			var rec recordedRoundYAML
			if err := yamlKnownFields(data, &rec); err != nil {
				return Case{}, caseErrorf(name, n, "parse findings.yaml: %v", err)
			}
			r.FindingsPath = findingsPath
			r.Recorded = &rec
		}
		c.Rounds = append(c.Rounds, r)
	}

	return c, nil
}

// LoadCorpus loads every case directory directly under root, in name
// order, failing if root has none.
func LoadCorpus(root string) ([]Case, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("revieweval: read corpus root %s: %w", root, err)
	}
	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := LoadCase(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	if len(cases) == 0 {
		return nil, fmt.Errorf("revieweval: corpus root %s has no case directories", root)
	}
	return cases, nil
}
