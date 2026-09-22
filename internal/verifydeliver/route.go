package verifydeliver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
)

// TriageInput is what one Triage hook call sees: this round's newly routed
// findings (findings.go's ApplyRound output, split by jig's own status),
// each list sorted by risk, high first (design 6.4). Fixes is every
// finding routed open (a new or recurring fix; a fix with no build target
// jig can derive never appears here - see Asks); Asks is every finding
// routed asked; Notes is included for display only, since a note is never
// triaged (design 6.3). Manifest is offered so a hook can list workspace
// ids and oracle names when keeping an ask needs one it lacks.
type TriageInput struct {
	Fixes    []Finding
	Asks     []Finding
	Notes    []Finding
	Manifest manifest.Manifest
}

// AskOutcome is one ask finding's triage decision (design 6.2). Keep false
// dismisses it. Decision is the human's text for a kept ask, carried into
// its fix slice's goal. Workspace and Oracle are required only when Keep is
// true and the finding itself is missing that part of its build target (no
// derived workspace, or no oracle jig can resolve): the hook must resolve
// whichever is missing, since jig never substitutes a silent default for
// that judgment. Human records whether a person at a terminal decided it,
// as opposed to --yes or a non-terminal stdin.
type AskOutcome struct {
	Keep      bool
	Decision  string
	Workspace string
	Oracle    string
	Human     bool
}

// TriageResult is a Triage hook's output (design 6.1-6.4). DismissedFixIDs
// names which of TriageInput.Fixes the human dismissed; every other fix is
// kept. FixHuman records whether a person at a terminal decided the fix
// batch. Asks maps a TriageInput.Asks finding's id to its outcome; an id
// absent from Asks is left undecided (an ask missing part of its build
// target that no human resolved stays asked).
type TriageResult struct {
	DismissedFixIDs map[string]bool
	FixHuman        bool
	Asks            map[string]AskOutcome
}

// Triage is the human seam for a gate round's fix batch and ask findings
// (design 6.1-6.4, D-1, D-2): it decides which fix findings to keep, and
// keeps or dismisses each ask. It performs no IO of its own - cmd wires
// stdin/stdout through a closure it builds. A nil Triage means
// DefaultTriage.
type Triage func(TriageInput) TriageResult

// DefaultTriage is what runs when a GateOpts.Triage hook is nil, and what
// --yes or a non-terminal stdin build on too (design 6.4, D-1): every fix
// is kept, every ask that already has a full build target (a derived
// workspace and a resolvable oracle) is kept with no decision text, and an
// ask missing either stays undecided - keeping it needs a human's choice,
// which nothing here can supply. Every decision it makes is auto (Human
// false).
func DefaultTriage(in TriageInput) TriageResult {
	oracleNames := sortedOracleNames(in.Manifest)
	asks := make(map[string]AskOutcome, len(in.Asks))
	for _, f := range in.Asks {
		if f.Workspace == "" {
			continue // undecided: keeping it needs a human's workspace choice
		}
		if _, ok := resolveOracle(f.Oracle, oracleNames); !ok {
			continue // undecided: keeping it needs a human's oracle choice
		}
		asks[f.ID] = AskOutcome{Keep: true}
	}
	return TriageResult{Asks: asks}
}

// sortByRiskThenID sorts fs by risk (high first) then id, in place, for
// deterministic display and deterministic slice-building order (design
// 6.4: "always shown sorted by risk, high first").
func sortByRiskThenID(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		ri, rj := riskRank[fs[i].Risk], riskRank[fs[j].Risk]
		if ri != rj {
			return ri < rj
		}
		return fs[i].ID < fs[j].ID
	})
}

// routeRound runs triage over reported (findings.go's ApplyRound output for
// this round) and turns the outcome into fix slices (design 6). It mutates
// and returns reported's own Status/Triage/Decision entries in place (a
// dismissed fix or ask becomes Status dismissed; a kept ask becomes Status
// open, per design 3's state diagram, carrying the human's decision and,
// for a no-workspace ask, the chosen workspace) and returns the fix slices
// to append. Notes are left exactly as ApplyRound reported them: design 6.3
// never triages a note. Routing and triage finish entirely inside this
// call, before Gate appends any slice or pushes the store.
func routeRound(round int, st *store.Store, ticket string, existingSlices []store.Slice, reported []Finding, triage Triage, man manifest.Manifest) ([]Finding, []store.Slice, error) {
	if triage == nil {
		triage = DefaultTriage
	}

	var fixes, asks, notes []Finding
	idx := make(map[string]int, len(reported))
	for i, f := range reported {
		idx[f.ID] = i
		switch f.Status {
		case StatusOpen:
			fixes = append(fixes, f)
		case StatusAsked:
			asks = append(asks, f)
		case StatusNoted:
			notes = append(notes, f)
		}
	}
	sortByRiskThenID(fixes)
	sortByRiskThenID(asks)
	sortByRiskThenID(notes)

	// GATE_NO_ORACLE is checked before triage, not after: a manifest with
	// zero oracles can never build a fix slice for anything, so asking a
	// human to keep or dismiss a fix or ask first - only to discard every
	// answer once building the slice fails - wastes their judgment on a
	// round that was already going to fail. "The manifest has no oracles"
	// is reserved for exactly this case; a finding that individually lacks
	// a resolvable oracle in a manifest that does have some is routed to
	// the human as an ask instead (findings.go's ApplyRound), the same
	// mechanism a no-workspace fix uses.
	oracleNames := sortedOracleNames(man)
	if len(oracleNames) == 0 && (len(fixes) > 0 || len(asks) > 0) {
		return nil, nil, &axi.Error{
			Msg:  "cannot build a fix slice, the manifest has no oracles",
			Code: "GATE_NO_ORACLE",
		}
	}

	result := triage(TriageInput{Fixes: fixes, Asks: asks, Notes: notes, Manifest: man})

	fixTriage := TriageAuto
	if result.FixHuman {
		fixTriage = TriageHuman
	}
	var keptFixes []Finding
	for _, f := range fixes {
		i := idx[f.ID]
		reported[i].Triage = fixTriage
		if result.DismissedFixIDs[f.ID] {
			reported[i].Status = StatusDismissed
			continue
		}
		keptFixes = append(keptFixes, reported[i])
	}

	var keptAsks []Finding
	for _, f := range asks {
		i := idx[f.ID]
		decision, decided := result.Asks[f.ID]
		if !decided {
			continue // undecided: stays asked, Triage left empty
		}
		askTriage := TriageAuto
		if decision.Human {
			askTriage = TriageHuman
		}
		reported[i].Triage = askTriage
		if !decision.Keep {
			reported[i].Status = StatusDismissed
			continue
		}
		ws := reported[i].Workspace
		if ws == "" {
			ws = decision.Workspace
		}
		oracle, ok := resolveOracle(reported[i].Oracle, oracleNames)
		if !ok {
			oracle, ok = resolveOracle(decision.Oracle, oracleNames)
		}
		if ws == "" || !ok {
			// The hook kept an ask without resolving every missing part of
			// its build target (a workspace, an oracle, or both);
			// defensively leave this ask undecided rather than build a
			// slice with no build target.
			reported[i].Triage = ""
			continue
		}
		reported[i].Status = StatusOpen
		reported[i].Decision = decision.Decision
		reported[i].Workspace = ws
		reported[i].Oracle = oracle
		keptAsks = append(keptAsks, reported[i])
	}

	slices, err := buildFixSlices(round, st, ticket, existingSlices, keptFixes, keptAsks, oracleNames)
	if err != nil {
		return nil, nil, err
	}
	return reported, slices, nil
}

// validOracle reports whether oracle is one of the manifest's own oracle
// names, so a manifest change between rounds (an oracle renamed or removed)
// is caught the same way an unset oracle is.
func validOracle(oracle string, oracleNames []string) bool {
	for _, name := range oracleNames {
		if name == oracle {
			return true
		}
	}
	return false
}

// resolveOracle resolves a finding's recorded oracle against the manifest's
// current oracle names: an already-valid oracle is returned unchanged; an
// empty one defaults to the sole manifest oracle when there is exactly one
// (there is no choice to make); anything else (empty with more than one
// manifest oracle, or a name the manifest no longer has) is unresolved, ok
// false.
func resolveOracle(oracle string, oracleNames []string) (string, bool) {
	if validOracle(oracle, oracleNames) {
		return oracle, true
	}
	if oracle == "" && len(oracleNames) == 1 {
		return oracleNames[0], true
	}
	return "", false
}

// oracleForFinding resolves f's fix-slice oracle (resolveOracle); every
// caller here has already routed a finding with no resolvable oracle to the
// human as an ask (findings.go's ApplyRound) or had the triage hook resolve
// one (routeRound's kept-ask handling above), so this only ever reports
// GATE_NO_ORACLE as a last defense, never as the primary mechanism.
func oracleForFinding(f Finding, oracleNames []string) (string, error) {
	if oracle, ok := resolveOracle(f.Oracle, oracleNames); ok {
		return oracle, nil
	}
	if len(oracleNames) == 0 {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("finding %s: cannot build a fix slice, the manifest has no oracles", f.ID),
			Code: "GATE_NO_ORACLE",
		}
	}
	return "", &axi.Error{
		Msg:  fmt.Sprintf("finding %s: cannot build a fix slice, no oracle recorded and the manifest has more than one: %s", f.ID, strings.Join(oracleNames, ", ")),
		Code: "GATE_NO_ORACLE",
	}
}

// previousFixSlice finds the latest slice in existingSlices whose Findings
// contains findingID, and reads its summary from its last attempt's
// work/<id>.attempt-<attempts>.result.json. found is false when no earlier
// slice recorded this finding id at all. summary is "" when that result
// file is absent, in which case only the slice's id is named.
func previousFixSlice(st *store.Store, ticket, findingID string, existingSlices []store.Slice) (id, summary string, found bool) {
	for i := len(existingSlices) - 1; i >= 0; i-- {
		for _, fid := range existingSlices[i].Findings {
			if fid == findingID {
				return existingSlices[i].ID, previousFixSliceSummary(st, ticket, existingSlices[i].ID), true
			}
		}
	}
	return "", "", false
}

// previousFixSliceSummary reads sliceID's last attempt's result.json
// summary from the store, or "" when the slice has no attempts yet or the
// file is missing or unparseable.
func previousFixSliceSummary(st *store.Store, ticket, sliceID string) string {
	state, err := st.ReadSliceState(ticket, sliceID)
	if err != nil || state.Attempts == 0 {
		return ""
	}
	path := filepath.Join(gateWorkDir(st, ticket), fmt.Sprintf("%s.attempt-%d.result.json", sliceID, state.Attempts))
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var res outcome.Result
	if err := json.Unmarshal(data, &res); err != nil {
		return ""
	}
	return res.Summary
}

// keptAskHeader phrases a kept ask's fix-slice goal from its recorded
// Triage (design 6.2): the builder must never be told a person decided
// when nobody did, since that is exactly the guessing the ask label exists
// to prevent (design 1). "kept by the human" only when a person at a
// terminal actually decided it; a plain, named default otherwise (--yes or
// no terminal).
func keptAskHeader(triage string) string {
	if triage == TriageHuman {
		return "kept by the human"
	}
	return "kept with no human decision (--yes or no terminal)"
}

// findingGoalBlock renders one finding's contribution to a fix slice's
// goal (design 6.1): file:line, title, detail and risk rationale, the
// human's decision for a kept ask, and the previous fix slice for a
// recurrence.
func findingGoalBlock(f Finding, st *store.Store, ticket string, existingSlices []store.Slice) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d %s", f.File, f.Line, f.Title)
	if f.Detail != "" {
		fmt.Fprintf(&b, "\n%s", f.Detail)
	}
	fmt.Fprintf(&b, "\nRisk (%s): %s", f.Risk, f.RiskRationale)
	if f.Decision != "" {
		fmt.Fprintf(&b, "\nThe human's decision: %s", f.Decision)
	}
	if f.Recurrences > 0 {
		if prevID, summary, ok := previousFixSlice(st, ticket, f.ID, existingSlices); ok {
			if summary != "" {
				fmt.Fprintf(&b, "\nPrevious fix slice %s: %s", prevID, summary)
			} else {
				fmt.Fprintf(&b, "\nPrevious fix slice: %s", prevID)
			}
		}
	}
	return b.String()
}

// sanitizeSliceID replaces every character outside [A-Za-z0-9._-] with "-":
// a manifest workspace id, an oracle name, or a finding id can legally
// contain a character (e.g. "/", ":") that is not safe in a slice id, since
// it becomes part of slices/<id>.state and work/<id>.attempt-N.* paths.
func sanitizeSliceID(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, id)
}

// disambiguateFixSliceIDs breaks ties between two synthesized slices that
// computed the same id - in practice, two workspace/oracle names that
// sanitizeSliceID maps to the same string. AppendSlices refuses a
// duplicate id outright, which would otherwise leave the round
// half-applied (some fix slices already appended, the rest rejected). The
// first occurrence of an id, in build order, keeps it; each later
// duplicate gets the lowest "-2", "-3", ... suffix not already taken by
// any id in the batch (original or already disambiguated), so this can
// never itself produce a new collision.
func disambiguateFixSliceIDs(slices []store.Slice) {
	used := make(map[string]bool, len(slices))
	for i := range slices {
		id := slices[i].ID
		if !used[id] {
			used[id] = true
			continue
		}
		for n := 2; ; n++ {
			candidate := fmt.Sprintf("%s-%d", id, n)
			if !used[candidate] {
				slices[i].ID = candidate
				used[candidate] = true
				break
			}
		}
	}
}

// buildFixSlices turns this round's kept findings into fix slices (design
// 6.1, 6.2): keptFixes group one slice per (workspace, oracle); keptAsks
// each become their own slice, carrying the human's decision. existingSlices
// is the ticket's slices.yaml as of before this round, used only to look up
// a recurrence's previous fix slice. oracleNames is the manifest's sorted
// oracle names, computed once by the caller (the zero-oracle check already
// ran on it before triage).
func buildFixSlices(round int, st *store.Store, ticket string, existingSlices []store.Slice, keptFixes, keptAsks []Finding, oracleNames []string) ([]store.Slice, error) {
	type groupKey struct{ workspace, oracle string }
	groups := map[groupKey][]Finding{}
	var order []groupKey
	for _, f := range keptFixes {
		oracle, err := oracleForFinding(f, oracleNames)
		if err != nil {
			return nil, err
		}
		key := groupKey{f.Workspace, oracle}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], f)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].workspace != order[j].workspace {
			return order[i].workspace < order[j].workspace
		}
		return order[i].oracle < order[j].oracle
	})

	var out []store.Slice
	for _, key := range order {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		var blocks []string
		var ids []string
		for _, f := range group {
			blocks = append(blocks, findingGoalBlock(f, st, ticket, existingSlices))
			ids = append(ids, f.ID)
		}
		goal := "Fix these gate findings:\n\n" + strings.Join(blocks, "\n\n")
		out = append(out, store.Slice{
			ID:        sanitizeSliceID(fmt.Sprintf("fix-%d-%s-%s", round, key.workspace, key.oracle)),
			Workspace: key.workspace,
			Goal:      goal,
			Oracle:    key.oracle,
			FromGate:  round,
			Findings:  ids,
		})
	}

	sort.Slice(keptAsks, func(i, j int) bool { return keptAsks[i].ID < keptAsks[j].ID })
	for _, f := range keptAsks {
		oracle, err := oracleForFinding(f, oracleNames)
		if err != nil {
			return nil, err
		}
		goal := fmt.Sprintf("Gate finding %s, %s:\n\n%s", f.ID, keptAskHeader(f.Triage), findingGoalBlock(f, st, ticket, existingSlices))
		out = append(out, store.Slice{
			ID:        sanitizeSliceID(fmt.Sprintf("fix-%d-%s", round, f.ID)),
			Workspace: f.Workspace,
			Goal:      goal,
			Oracle:    oracle,
			FromGate:  round,
			Findings:  []string{f.ID},
		})
	}

	disambiguateFixSliceIDs(out)
	return out, nil
}
