package verifydeliver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
)

// TriageInput is what one Triage hook call sees: this round's newly routed
// findings (findings.go's ApplyRound output, split by jig's own status),
// each list sorted by risk, high first. Fixes is every finding routed open
// (a new or recurring fix; a fix with no build target jig can derive never
// appears here - see Asks); Asks is every finding routed asked; Notes is
// what the round reported as a note and StandingNotes the noted findings of
// earlier rounds it did not report again: a person may keep or dismiss either
// with `jig triage` (TriageResult.Notes), and a note nobody decided stays a
// note, so no default decides one. Manifest is
// offered so a hook can list workspace ids and oracle names when keeping an
// ask needs one it lacks.
type TriageInput struct {
	Fixes []Finding
	Asks  []Finding
	Notes []Finding
	// StandingNotes are the earlier rounds' noted findings this round's
	// reviewer did not report again.
	StandingNotes []Finding
	Manifest      manifest.Manifest
	// Repos are the manifests of a round in a project of several repos, by repo
	// name: a finding's file begins with its repo's name, and its workspace and
	// oracle are those of that repo's manifest. Manifest is empty then, and
	// Repos is nil for a round over one repo.
	Repos map[string]manifest.Manifest
	// BudgetUsed and BudgetLimit are this ticket's fix budget state (the
	// gate's earlier fix-appending rounds, and gate.fix_rounds), carried so
	// a hook can phrase the header it shows Asks' own budget-parked
	// entries (Finding.RoutedWhy == RoutedWhyBudget) under, without
	// recomputing either number itself. Meaningful only when at least one
	// of Asks is budget-parked.
	BudgetUsed, BudgetLimit int
}

// AskOutcome is one ask finding's triage decision. Keep false dismisses
// it. Decision is the human's text for a kept ask, carried into
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

// TriageResult is a Triage hook's output. DismissedFixIDs names which of
// TriageInput.Fixes the human dismissed; every other fix is
// kept. FixHuman records whether a person at a terminal decided the fix
// batch. Asks maps a TriageInput.Asks finding's id to its outcome; an id
// absent from Asks is left undecided (an ask missing part of its build
// target that no human resolved stays asked). Notes maps the id of a
// TriageInput note (Notes or StandingNotes) a person decided to its outcome,
// the same shape as an ask's: kept, the note becomes a fix slice of this round
// and an open finding; dismissed, it reads dismissed by a human; an id absent
// stays a note.
type TriageResult struct {
	DismissedFixIDs map[string]bool
	FixHuman        bool
	Asks            map[string]AskOutcome
	Notes           map[string]AskOutcome
}

// Triage is the human seam for a gate round's fix batch and ask findings:
// it decides which fix findings to keep, and keeps or dismisses each ask.
// It performs no IO of its own - cmd wires
// stdin/stdout through a closure it builds. A nil Triage means
// DefaultTriage.
type Triage func(TriageInput) TriageResult

// DefaultTriage is what runs when a GateOpts.Triage hook is nil, and what
// --yes or a non-terminal stdin build on too: every fix is kept, every ask
// that already has a full build target (a derived workspace and a command
// its reviewer named) is kept with no decision text, and an
// ask missing either stays undecided - keeping it needs a human's choice,
// which nothing here can supply. A budget-parked ask (Finding.RoutedWhy ==
// RoutedWhyBudget) stays undecided too, whatever its build target: only a
// person at a terminal can keep one, so --yes and a non-terminal stdin
// (which both build on this function) never spend past the budget. Every
// decision it makes is auto (Human false).
func DefaultTriage(in TriageInput) TriageResult {
	asks := make(map[string]AskOutcome, len(in.Asks))
	for _, f := range in.Asks {
		if f.RoutedWhy == RoutedWhyBudget {
			continue // undecided: only a person at a terminal can keep a parked finding
		}
		if noWorkspace, noOracle := BuildTargetGaps(f); noWorkspace || noOracle {
			continue // undecided: keeping it needs a human's workspace/oracle choice
		}
		asks[f.ID] = AskOutcome{Keep: true}
	}
	return TriageResult{Asks: asks}
}

// BuildTargetGaps reports which part of f's build target jig cannot derive
// on its own: workspace is true when f's file lies in no declared manifest
// workspace; oracle is true when f names no command (its reviewer gave none,
// as a recurrence of a finding that never had one). Either gap needs a
// person's choice before f can become a fix slice; jig never picks a command
// for a reviewer. This is the one mechanism every caller that has to
// decide whether a fix or kept ask has a full build target shares:
// DefaultTriage above, routeRound's kept-ask handling, and cmd/jig's
// terminal triage prompt and its stdin-closed count - none of them
// re-derives either rule on its own.
func BuildTargetGaps(f Finding) (workspace, oracle bool) {
	return f.Workspace == "", strings.TrimSpace(f.Oracle) == ""
}

// sortByRiskThenID sorts fs by risk (high first) then id, in place, for
// deterministic display and deterministic slice-building order: a human
// always sees findings sorted by risk, high first.
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
// this round) plus outstanding (cum's still-asked findings this round's
// reviewer did not report again, findings.go's outstandingAsks) and turns
// the outcome into fix slices. It mutates and returns reported's own
// Status/Triage/Decision entries in place (a dismissed fix or ask becomes
// Status dismissed; a kept ask becomes Status open, carrying the human's
// decision and, for a no-workspace ask, the chosen workspace), appends a
// decided outstanding ask to the returned list (so this round's
// findings.yaml records the decision), and returns the fix slices to
// append. An outstanding ask left undecided is not appended: it stays
// exactly as cum already has it, offered again next round. The same holds
// for outstanding's noted findings (cum's notes this round's reviewer did not
// report again): a person's keep or dismissal of one is applied like an ask's
// (TriageResult.Notes), and a note nobody decided is left exactly as
// ApplyRound reported it or cum has it. Routing
// and triage finish entirely inside this call, before Gate appends any
// slice or pushes the store.
//
// files is the files of the round, which say which repo's manifest a finding
// is routed against: a round over several repos routes a finding in
// web/ui/page.go into a fix slice that builds in the workspace web/ui, with an
// oracle of web's manifest (roundrepos.go).
//
// budgetUsed and budgetLimit are this ticket's own fix budget state
// (Gate's own UsedFixBudget and gate.fix_rounds), passed through to the
// triage hook's TriageInput unchanged so it can phrase its own
// budget-parked header without recomputing either number. fixSliceFindings
// is gate.fix_slice_findings, the most findings one fix slice may carry
// (buildFixSlices' own splitFixGroupIntoSlices enforces it).
func routeRound(round int, st *store.Store, ticket string, existingSlices []store.Slice, reported []Finding, outstanding []Finding, triage Triage, files roundFiles, budgetUsed, budgetLimit, fixSliceFindings int) ([]Finding, []store.Slice, error) {
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
	// Outstanding asks are not in reported (idx has no entry for their id):
	// they are offered to triage the same as this round's own asks, but
	// only a decided one is folded back into the returned list below.
	// A noted finding among them is a standing note, offered to triage as a
	// note, never as an ask.
	var standingNotes []Finding
	for _, f := range outstanding {
		if f.Status == StatusNoted {
			standingNotes = append(standingNotes, f)
			continue
		}
		asks = append(asks, f)
	}
	sortByRiskThenID(fixes)
	sortByRiskThenID(asks)
	sortByRiskThenID(notes)
	sortByRiskThenID(standingNotes)

	in := TriageInput{Fixes: fixes, Asks: asks, Notes: notes, StandingNotes: standingNotes, BudgetUsed: budgetUsed, BudgetLimit: budgetLimit}
	if files.qualified {
		in.Repos = make(map[string]manifest.Manifest, len(files.repos))
		for _, r := range files.repos {
			in.Repos[r.Name] = r.Man
		}
	} else {
		in.Manifest = files.repos[0].Man
	}
	result := triage(in)

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
	var decidedOutstanding []Finding
	for _, f := range asks {
		decision, decided := result.Asks[f.ID]
		if !decided {
			continue // undecided: stays asked, Triage left empty
		}
		// wf is the working copy to decide: reported[i] for a finding this
		// round's reviewer reported, or a copy of the outstanding cum
		// finding (not reported this round, only decided) otherwise.
		i, isReported := idx[f.ID]
		wf := f
		if isReported {
			wf = reported[i]
		}
		wf, kept, applied := decideFinding(wf, decision)
		if !applied {
			continue
		}
		if kept {
			keptAsks = append(keptAsks, wf)
		}
		if isReported {
			reported[i] = wf
		} else {
			decidedOutstanding = append(decidedOutstanding, wf)
		}
	}
	// A note a person decided is applied the way a decided ask is: kept, it
	// is an open finding with a fix slice of this round (counted in the fix
	// budget, like any slice a round appends); dismissed, a dismissed one. A
	// note nobody decided is left exactly as it is.
	for _, f := range append(append([]Finding(nil), notes...), standingNotes...) {
		decision, decided := result.Notes[f.ID]
		if !decided {
			continue
		}
		i, isReported := idx[f.ID]
		wf := f
		if isReported {
			wf = reported[i]
		}
		wf, kept, applied := decideFinding(wf, decision)
		if !applied {
			continue
		}
		if kept {
			keptAsks = append(keptAsks, wf)
		}
		if isReported {
			reported[i] = wf
		} else {
			decidedOutstanding = append(decidedOutstanding, wf)
		}
	}
	reported = append(reported, decidedOutstanding...)

	slices, err := buildFixSlices(round, st, ticket, existingSlices, keptFixes, keptAsks, files, fixSliceFindings, false)
	if err != nil {
		return nil, nil, err
	}
	return reported, slices, nil
}

// queueKeeps turns the pending keeps among cum's asked and noted findings
// into fix slices of round, before any round opens: the same slice a round's
// close would build for the keep (decideFinding, buildFixSlices), so a person's
// keep is built with the fixes already queued instead of costing a round.
// kept names the findings that got one, in id order. A keep that lacks part of
// its build target is left as it is, offered again, and a dismiss is not
// touched: the round that closes decides it.
func queueKeeps(round int, st *store.Store, ticket string, existingSlices []store.Slice, cum map[string]Finding, decisions map[string]TriageDecision, files roundFiles, fixSliceFindings int) (slices []store.Slice, kept []string, err error) {
	var keeps []Finding
	for _, f := range cum {
		if f.Status != StatusAsked && f.Status != StatusNoted {
			continue
		}
		if d, ok := decisions[f.ID]; !ok || !d.Pending || d.Outcome != TriageKeep {
			continue
		}
		d := decisions[f.ID]
		wf, isKept, applied := decideFinding(f, AskOutcome{Keep: true, Decision: d.Reason, Workspace: d.Workspace, Oracle: d.Oracle, Human: d.Human()})
		if applied && isKept {
			keeps = append(keeps, wf)
		}
	}
	sort.Slice(keeps, func(i, j int) bool { return keeps[i].ID < keeps[j].ID })
	slices, err = buildFixSlices(round, st, ticket, existingSlices, nil, keeps, files, fixSliceFindings, true)
	for _, f := range keeps {
		kept = append(kept, f.ID)
	}
	return slices, kept, err
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
// Triage: the builder must never be told a person decided when nobody
// did, since that is exactly the guessing the ask label exists to prevent.
// "kept by the human" only when a person at a terminal actually decided
// it; a plain, named default otherwise (--yes or no terminal).
func keptAskHeader(triage string) string {
	if triage == TriageHuman {
		return "kept by the human"
	}
	return "kept with no human decision (--yes or no terminal)"
}

// findingGoalBlock renders one finding's contribution to a fix slice's
// goal: file:line, title, detail and risk rationale, the human's decision
// for a kept ask, and the previous fix slice for a recurrence.
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
// sanitizeSliceID maps to the same string - and against any id the
// ticket's slices.yaml already has (existingSlices, as of before this
// round): AppendSlices refuses a duplicate id outright, which would
// otherwise leave the round half-applied (some fix slices already
// appended, the rest rejected), whether the collision is within this
// batch or against a slice that already exists. The first occurrence of
// an id, in build order, keeps it; each later duplicate gets the lowest
// "-2", "-3", ... suffix not already taken by any id in the batch
// (original or already disambiguated) or by an existing slice, so this
// can never itself produce a new collision.
func disambiguateFixSliceIDs(slices []store.Slice, existingSlices []store.Slice) {
	used := make(map[string]bool, len(slices)+len(existingSlices))
	for _, s := range existingSlices {
		used[s.ID] = true
	}
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

// splitFixGroupIntoSlices packs one (workspace, oracle) group's kept
// findings, already sorted by id, into one or more fix slices bounded by
// fixSliceFindings findings each ("Fix slices sized to one session"):
// findings in the same file always share a slice; files are taken in path
// order and packed greedily, each slice filled as full as the bound
// allows before starting the next one; a file whose own finding count
// exceeds the bound gets a slice of its own, over the bound, rather than
// being split itself. A group whose findings fit in one slice returns
// exactly one subset, so the caller can keep that group's existing id.
func splitFixGroupIntoSlices(group []Finding, fixSliceFindings int) [][]Finding {
	byFile := map[string][]Finding{}
	var files []string
	for _, f := range group {
		if _, ok := byFile[f.File]; !ok {
			files = append(files, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	sort.Strings(files)

	var out [][]Finding
	var current []Finding
	for _, file := range files {
		fs := byFile[file]
		if len(fs) > fixSliceFindings {
			if len(current) > 0 {
				out = append(out, current)
				current = nil
			}
			out = append(out, fs)
			continue
		}
		if len(current) > 0 && len(current)+len(fs) > fixSliceFindings {
			out = append(out, current)
			current = nil
		}
		current = append(current, fs...)
	}
	if len(current) > 0 {
		out = append(out, current)
	}
	return out
}

// buildFixSlices turns this round's kept findings into fix slices: kept
// fixes, kept asks and kept notes group together by workspace alone (a round
// over several repos names a workspace "<repo>/<id>", so a group never spans
// repos), each group then split into one or more slices bounded by
// fixSliceFindings (splitFixGroupIntoSlices). A slice's oracle is the commands
// its findings' reviewers named (fixSliceOracle). A kept ask or note keeps its
// own header and the person's decision inside the slice's goal; a kept fix
// has no header. existingSlices is the ticket's slices.yaml as of before this
// round, used only to look up a recurrence's previous fix slice. queued says
// the slices are a person's keeps queued before the round opened: their ids
// carry a "q" marker, so the round's own findings of the same workspace keep
// the plain id instead of taking a "-2" suffix from the queued slice.
func buildFixSlices(round int, st *store.Store, ticket string, existingSlices []store.Slice, keptFixes, keptAsks []Finding, files roundFiles, fixSliceFindings int, queued bool) ([]store.Slice, error) {
	groups := map[string][]Finding{}
	var order []string
	asked := map[string]bool{}
	add := func(f Finding) {
		if _, ok := groups[f.Workspace]; !ok {
			order = append(order, f.Workspace)
		}
		groups[f.Workspace] = append(groups[f.Workspace], f)
	}
	for _, f := range keptFixes {
		add(f)
	}
	for _, f := range keptAsks {
		asked[f.ID] = true
		add(f)
	}
	sort.Strings(order)

	var out []store.Slice
	for _, workspace := range order {
		group := groups[workspace]
		sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		parts := splitFixGroupIntoSlices(group, fixSliceFindings)
		baseID := fmt.Sprintf("fix-%d-%s", round, workspace)
		if queued {
			baseID = fmt.Sprintf("fix-%d-q-%s", round, workspace)
		}
		for i, part := range parts {
			var blocks []string
			var ids []string
			for _, f := range part {
				block := findingGoalBlock(f, st, ticket, existingSlices)
				if asked[f.ID] {
					block = fmt.Sprintf("Gate finding %s, %s:\n\n%s", f.ID, keptAskHeader(f.Triage), block)
				}
				blocks = append(blocks, block)
				ids = append(ids, f.ID)
			}
			goal := "Fix these gate findings:\n\n" + strings.Join(blocks, "\n\n")
			id := baseID
			if len(parts) > 1 {
				id = fmt.Sprintf("%s-%d", baseID, i+1)
			}
			out = append(out, store.Slice{
				ID:        sanitizeSliceID(id),
				Workspace: workspace,
				Goal:      goal,
				Oracle:    fixSliceOracle(part, files),
				FromGate:  round,
				Findings:  ids,
			})
		}
	}

	disambiguateFixSliceIDs(out, existingSlices)
	return out, nil
}

// fixSliceOracle is the oracle of a fix slice over findings: the commands the
// findings' reviewers named, in finding-id order, identical text once. One
// command is carried verbatim, so a manifest oracle's name still runs as the
// manifest defines it. Several are joined as `(a) && (b)`, the composition
// `jig verify --final` uses: each in parentheses so its own operators stay its
// own, and one that holds a # on its own lines, since a comment would swallow
// its closing parenthesis. A manifest oracle's name among several is written
// out as the manifest defines it, since a name inside parentheses is no longer
// one.
func fixSliceOracle(findings []Finding, files roundFiles) string {
	sorted := append([]Finding(nil), findings...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	var cmds []string
	first := map[string]Finding{}
	for _, f := range sorted {
		cmd := strings.TrimSpace(f.Oracle)
		if cmd == "" {
			continue
		}
		if _, dup := first[cmd]; dup {
			continue
		}
		first[cmd] = f
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	parts := make([]string, len(cmds))
	for i, cmd := range cmds {
		// A name is resolved against the manifest of the repo of the first
		// finding that named it, the one it was written for.
		f := first[cmd]
		if repo, _, found := files.of(f.File); found {
			if ws, ok := repo.Man.WorkspaceIn(repo.Name, f.Workspace); ok {
				cmd = strings.TrimSpace(repo.Man.OracleCmd(cmd, ws))
			}
		}
		if strings.Contains(cmd, "#") {
			parts[i] = "(\n" + cmd + "\n)"
		} else {
			parts[i] = "(" + cmd + ")"
		}
	}
	return strings.Join(parts, " && ")
}

// decideFinding applies a person's decision on an ask or a note to wf: a
// dismissal makes it dismissed; a keep makes it open, carrying the decision
// text and the build target (the workspace and oracle it lacked, from the
// decision). applied is false when the keep leaves part of the build target
// unresolved: jig never builds a slice with no build target, so the finding
// stays as it was and is offered again. kept says the finding now waits for a
// fix slice.
func decideFinding(wf Finding, decision AskOutcome) (out Finding, kept, applied bool) {
	triage := TriageAuto
	if decision.Human {
		triage = TriageHuman
	}
	wf.Triage = triage
	if !decision.Keep {
		wf.Status = StatusDismissed
		return wf, false, true
	}
	noWorkspace, noOracle := BuildTargetGaps(wf)
	ws := wf.Workspace
	if noWorkspace {
		ws = decision.Workspace
	}
	oracle := wf.Oracle
	if noOracle {
		oracle = strings.TrimSpace(decision.Oracle)
	}
	if ws == "" || oracle == "" {
		// The hook kept a finding without resolving every missing part of
		// its build target (a workspace, an oracle, or both); defensively
		// leave it undecided rather than build a slice with no build target.
		return wf, false, false
	}
	wf.Status = StatusOpen
	wf.Decision = decision.Decision
	wf.Workspace = ws
	wf.Oracle = oracle
	return wf, true, true
}
