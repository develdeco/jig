package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// Round is one gate round's content, whether played back by a scripted source
// or the review of a recorded round (GateFinish). Review is its validated
// result plus scope data, which
// Gate applies through findings bookkeeping (findings.go) to decide clean
// vs fix-slices and to persist findings.yaml/md, then routes into fix
// slices (route.go) - those are appended directly from routing's own
// return value, never stored back onto this struct. FixSlices
// is the scripted source's own field instead: fakeGateSource.Round reads it
// straight from a scenario's fix-slices.yaml, and Gate appends it unchanged
// for that source.
type Round struct {
	FindingsMD string
	FixSlices  []store.Slice
	Receipts   map[string][]byte
	Review     *Review
	// Carried is the number of the round whose verdict a reviewer round with
	// nothing to read carries over: 0 for a round a reviewer read.
	Carried int
}

// GateSource supplies one gate round's content. Round returns ok=false for
// a clean round (no round directory, or an empty one, for the scripted
// source; findings bookkeeping decides this for the reviewer source).
type GateSource interface {
	Round(in RoundInput) (Round, bool, error)
}

// fakeGateSource reads gate rounds from a materialized fixture scenario
// tree (scenarioDir/gate/round-<n>/).
type fakeGateSource struct {
	scenarioDir string
}

// NewFakeGateSource returns a GateSource that reads scenario rounds from
// scenarioDir/gate/round-<n>/: findings.md, fix-slices.yaml (a
// store.SliceFile), and receipts/* (any files, keyed by base name).
func NewFakeGateSource(scenarioDir string) GateSource {
	return &fakeGateSource{scenarioDir: scenarioDir}
}

func (f *fakeGateSource) Round(in RoundInput) (Round, bool, error) {
	n := in.Round
	dir := filepath.Join(f.scenarioDir, "gate", fmt.Sprintf("round-%d", n))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Round{}, false, nil
		}
		return Round{}, false, fmt.Errorf("verifydeliver: read scenario round %d: %w", n, err)
	}
	if len(entries) == 0 {
		return Round{}, false, nil
	}

	findings, err := os.ReadFile(filepath.Join(dir, "findings.md"))
	if err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: read scenario round %d findings.md: %w", n, err)
	}

	var fixSlices []store.Slice
	if data, err := os.ReadFile(filepath.Join(dir, "fix-slices.yaml")); err == nil {
		var sf store.SliceFile
		if err := yaml.Unmarshal(data, &sf); err != nil {
			return Round{}, false, fmt.Errorf("verifydeliver: parse scenario round %d fix-slices.yaml: %w", n, err)
		}
		fixSlices = sf.Slices
	} else if !os.IsNotExist(err) {
		return Round{}, false, err
	}

	receipts := map[string][]byte{}
	if recEntries, err := os.ReadDir(filepath.Join(dir, "receipts")); err == nil {
		for _, e := range recEntries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, "receipts", e.Name()))
			if err != nil {
				return Round{}, false, err
			}
			receipts[e.Name()] = data
		}
	}

	return Round{FindingsMD: string(findings), FixSlices: fixSlices, Receipts: receipts}, true, nil
}

// GateOpts configures one Gate invocation.
type GateOpts struct {
	Ticket string
	Early  bool
	// Branch is `jig gate --branch`: review this branch, built outside jig, and
	// adopt it as the ticket's own. The first round that names it records it in
	// the ticket's record, with the branch's tip as the ticket's start sha; every
	// later round works on the recorded branch, and naming another one is
	// refused (BRANCH_MISMATCH). Empty: the ticket's own branch, the recorded
	// one or the default (<ticket>-<slug>).
	Branch string
	// Intent and IntentDoc are `jig gate --intent`/`--doc`: at most one is
	// ever set - refused when both are, whether by the CLI or by a caller
	// going straight to GateOpts - valid in every mode, and write the
	// ticket's intent.md as an explicit, binding intent - refused with
	// INTENT_CONFLICT when the ticket already has a brief.md. Intent is the
	// literal text; IntentDoc is a file whose content becomes that text.
	Intent    string
	IntentDoc string
	// Yes is `jig gate --yes`: the round keeps every finding jig can route on
	// its own when it is recorded, without waiting for a person's decision on
	// each ask (DefaultTriage). A recorded round carries it from the opening
	// to the `review done` that closes it.
	Yes bool
	// Triage is the seam for a reviewer round's fix batch and ask
	// findings (route.go). nil means DefaultTriage: every fix is
	// kept, every ask with a full build target is kept, one missing part
	// of it stays undecided. Used by Gate only: a recorded round decides its
	// asks from the decisions `jig triage` journaled (recordedTriage).
	Triage Triage
}

// GateReport is Gate's result.
type GateReport struct {
	Round     int
	Verdict   string // clean|fix-slices (not-independent is an older jig's, read back from report.yaml)
	TargetSHA map[string]string
	// Model and Effort are what the reviewer said it ran on: self-reported,
	// "" when it said nothing or no reviewer ran. jig chooses no model.
	Model  string
	Effort string
	// Reviewer is who reported a recorded round, and Independent whether they
	// are independent of the agents that built what they reviewed. Both are
	// the zero value for a scripted round.
	Reviewer    Provenance
	Independent Independence
	// OracleNames are the manifest's oracle names at this round.
	OracleNames []string
	// Copy says whose copy of the branch the round reviewed: the clone's or
	// origin's.
	Copy string
	// ReviewedSHA is repoName -> the head sha a reviewer round reviewed, on
	// every reviewer round, clean or not - the anchor the next round's
	// scope resolves against (review.go's resolveScope). nil for a
	// scripted round.
	ReviewedSHA map[string]string
	// ReviewedBase is repoName -> the base the reviewer read repo from, on
	// every reviewer round: with ReviewedSHA, the range the round read. nil
	// for a scripted round and for a round an older jig recorded.
	ReviewedBase map[string]string
	// Scope is this round's scope diff kind (full|delta), "" for a
	// scripted round.
	Scope string
	// Findings is every finding this round reported, after routing and
	// triage set each one's final Status/Triage/Decision/
	// RoutedAs. nil for a scripted round.
	Findings []Finding
	// KeptFixed are the keeps this round spent as fixed (a pending keep on a
	// finding the round no longer reports at a moved head), each with who
	// kept it. nil for a round that spent none.
	KeptFixed []KeptFixed
	// FixSlices is the ids of the fix slices this round appended, in the
	// order they were built.
	FixSlices []string
	// NeedsHuman is every asked finding still undecided after this round
	// (across every round, not only this one's own): the "needs a human"
	// list, the exit-2 signal. Empty when nothing is waiting on a person.
	NeedsHuman []Finding
	// Branch is the branch the ticket adopted, "" for a ticket working on
	// its own default branch.
	Branch string
	// Intent is this round's resolved intent binding (resolveIntent), read
	// once before the round's source runs; a reviewer round reports the one
	// it handed the reviewer instead (Review.Intent), which is an
	// inference the source ran during the round (review.go's inferIntent)
	// when nothing was resolved.
	Intent Intent
	// IntentSHA256 is the sha256 hex of the exact bytes of the file at
	// Intent.Path, "" for Intent.Source "none".
	IntentSHA256 string
	// IntentNote is a one-line reason this round's intent stayed "none"
	// after an inference attempt (round.Review.IntentNote) - empty when
	// inference was not attempted or it succeeded.
	IntentNote string
	// BudgetParked is how many findings this round parked under the fix
	// budget (status asked, RoutedWhyBudget): 0 for a round that parked
	// none, a scripted round (the budget never forces one of its findings),
	// or a round with nothing new to route at all. BudgetUsed and
	// BudgetLimit are this round's own view of the fix budget (the
	// ticket's earlier fix-appending rounds, and gate.fix_rounds),
	// meaningful only alongside BudgetParked > 0.
	BudgetParked            int
	BudgetUsed, BudgetLimit int
	// Probes is how many probes a reviewer round recorded, and Unprobed how
	// many hunks of the whole change it left with no current probe inside and
	// no current skip over them (ADR 0036): 0 for a scripted round.
	Probes, Unprobed int
	// CarriedFrom is the number of the round whose verdict this clean round
	// carries over: 0 for a round a reviewer read.
	CarriedFrom int
	// StoreRefs are the lines the ticket's whole change adds that name
	// something only the store holds (storerefs.go): a fact of the round,
	// never a reason it is not clean. nil for a scripted round and for a round
	// with none.
	StoreRefs []StoreRef
	// CarriedMerge is repo -> the merge of the target a delta round carried
	// rather than read: nil for a round that carried none.
	CarriedMerge map[string]CarriedMerge
}

// reportYAML is gate/round-<n>/report.yaml's exact on-disk shape.
type reportYAML struct {
	Round   int    `yaml:"round"`
	Verdict string `yaml:"verdict"`
	Model   string `yaml:"model,omitempty"`
	Effort  string `yaml:"effort,omitempty"`
	// Jig is the build stamp of the jig that wrote the report
	// (journal.Stamp); absent from a report an older jig wrote.
	Jig         string            `yaml:"jig,omitempty"`
	TargetSHA   map[string]string `yaml:"target_sha"`
	ReviewedSHA map[string]string `yaml:"reviewed_sha,omitempty"`
	// ReviewedBase is the base the reviewer read each repo from, so the
	// range a round read is on its own record (ReadRounds).
	ReviewedBase map[string]string `yaml:"reviewed_base,omitempty"`
	Intent       reportIntentYAML  `yaml:"intent"`
	// Independent says whether the reviewer of a recorded round is
	// independent of the builders (true, false or unknown); absent for a
	// scripted round. A fact of the round, never a reason it
	// is not clean (ADR 0046).
	Independent Independence `yaml:"independent,omitempty"`
	// CarriedFrom is the number of the round whose verdict this clean round
	// carries over: its head added nothing to read past the head
	// that round reviewed (that head itself, or a clean merge of the target past
	// it). It names the round that was really reviewed. 0 for
	// every round a reviewer read.
	CarriedFrom int `yaml:"carried_from,omitempty"`
	// StoreRefs are the added lines that name something only the store holds,
	// in diff order, every one of them; absent when there are none and in a
	// report an older jig wrote.
	StoreRefs []StoreRef `yaml:"store_refs,omitempty"`
	// CarriedMerge is the merge of the target each repo's round carried, not
	// read; absent when there is none and in a report an older jig wrote.
	CarriedMerge map[string]CarriedMerge `yaml:"carried_merge,omitempty"`
	// Reviewer is who reported the round, as the reviewer's session and
	// agent ids and how jig learned them.
	Reviewer *Provenance `yaml:"reviewer,omitempty"`
	// OracleNames are the manifest's oracle names, kept so the review request
	// and the contract index can name them without a checkout.
	OracleNames []string `yaml:"oracle_names,omitempty"`
	// FixSlices is the ids of the fix slices this round appended, whoever
	// kept them (a reviewer round's own routing, or a scripted round's
	// scenario fix slices): the fix budget's own used-rounds count
	// (UsedFixBudget) is a round whose list here is non-empty.
	FixSlices []string `yaml:"fix_slices,omitempty"`
}

// reportIntentYAML is report.yaml's own "intent" block: the source
// resolveIntent bound this round to, and the sha256 hex of the exact bytes
// of the file at that source's path ("" for source "none").
type reportIntentYAML struct {
	Source string `yaml:"source"`
	SHA256 string `yaml:"sha256"`
}

// UsedFixBudget returns how many of ticket's gate rounds before round
// appended at least one fix slice (report.yaml's fix_slices, written for a
// reviewer round's own routing and for a scripted round's scenario fix
// slices alike): the count "The fix budget" compares against
// gate.fix_rounds to decide whether a round still queues fix findings or
// parks them. unreadable names any round in that range whose report.yaml
// could not be read or parsed, skipped rather than counted either way -
// the same tolerance OutstandingFindings and `jig status`'s own round-reading
// already apply, since one corrupt earlier round is not reason enough to
// treat every ticket's budget as exhausted or to refuse every later
// round. Gate and `jig status` share this one reading, so neither can
// disagree with the other about how much of the budget is used.
func UsedFixBudget(st *store.Store, ticket string, round int) (used int, unreadable []int) {
	// A fix slice a gate queued for a keep before its round opened belongs to
	// that round (FromGate), though the round may record none of its own.
	queued := map[int]bool{}
	if slices, err := st.ReadSlices(ticket); err == nil {
		for _, s := range slices {
			queued[s.FromGate] = true
		}
	}
	for r := 1; r < round; r++ {
		data, err := os.ReadFile(filepath.Join(gateRoundDir(st, ticket, r), "report.yaml"))
		if err != nil {
			if !os.IsNotExist(err) {
				unreadable = append(unreadable, r)
			} else if queued[r] {
				used++ // queued for a round that has not been recorded yet
			}
			continue
		}
		var rep reportYAML
		if err := yaml.Unmarshal(data, &rep); err != nil {
			unreadable = append(unreadable, r)
			continue
		}
		if len(rep.FixSlices) > 0 || queued[r] {
			used++
		}
	}
	return used, unreadable
}

// checkFrontier errors when any slice - including a fix slice raised by an
// earlier gate round - is not yet green and early is false: a slice left
// queued, stalled, needs-input, env-blocked or otherwise unfinished means
// the delivery is incomplete, so gating (or publishing) now would review or
// ship less than the whole ticket. The slices of every repo count, and in a
// project of several repos the refusal names the repo of the slice it stops
// at. --early skips the check entirely, for a deliberate mid-ticket milestone
// batch.
func checkFrontier(d Deps, ticket string, slices []store.Slice, early bool) error {
	if early {
		return nil
	}
	for _, s := range slices {
		st, err := d.Store.ReadSliceState(ticket, s.ID)
		if err != nil {
			return fmt.Errorf("verifydeliver: gate: read slice %s state: %w", s.ID, err)
		}
		if st.State != "green" {
			where := ""
			if len(d.Cfg.Repos) > 1 {
				if repo := ready.Place(ready.ProjectRepos(d.Cfg, d.Machine.Clones), slices).Repo[s.Workspace]; repo != "" && repo != ready.Unplaced {
					where = " of repo " + repo
				}
			}
			help := []string{fmt.Sprintf("Run `jig next %s` to see what is left to build, or `jig gate %s --early` to review anyway.", ticket, ticket)}
			if d.Prompt != nil {
				if p := d.Prompt(ticket); p != "" {
					help = append(help, "ask: "+p)
				}
			}
			return &axi.Error{
				Msg:  fmt.Sprintf("slice %s%s is %s, not green; finish it first or pass --early", s.ID, where, st.State),
				Code: "GATE_NOT_GREEN",
				Help: help,
			}
		}
	}
	return nil
}

// writeNoRound writes the files for a round that never ran while something
// was still outstanding: the source had nothing to review for this round
// number, but the fold still holds an open or asked finding. It records
// what is outstanding rather than the word "clean", so the stored round
// agrees with the report and with `jig status`.
func writeNoRound(d Deps, ticket string, n int, report GateReport) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Gate round %d\n", n)
	fmt.Fprintf(&b, "\nNo review ran this round. Still outstanding from earlier rounds:\n\n")
	for _, f := range report.Findings {
		fmt.Fprintf(&b, "- %s (%s) %s:%d %s\n", f.ID, f.Status, f.File, f.Line, f.Title)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}
	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	if err := writeDiffChangelog(d, ticket, dir, n); err != nil {
		return err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "gate-round", Attempt: n}); err != nil {
		return fmt.Errorf("verifydeliver: gate: journal gate-round: %w", err)
	}
	return nil
}

// writeCleanRound writes a clean round's files: findings.md declares it
// clean, report.yaml records the verdict, and diff-changelog.md is the
// pure journal render of what landed since the previous round.
func writeCleanRound(d Deps, ticket string, n int, report GateReport) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	findings := fmt.Sprintf("# Gate round %d\n\nclean\n", n)
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(findings), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}
	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	if err := writeDiffChangelog(d, ticket, dir, n); err != nil {
		return err
	}
	return nil
}

// writeFixRound writes a fix-slices round's files: the reviewer's
// findings, report.yaml, diff-changelog.md, and receipts under the
// ticket's evidence tree.
func writeFixRound(d Deps, ticket string, n int, report GateReport, round Round) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	findings := round.FindingsMD
	if findings == "" {
		findings = fmt.Sprintf("# Gate round %d\n", n)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(findings), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}
	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	if err := writeDiffChangelog(d, ticket, dir, n); err != nil {
		return err
	}
	if len(round.Receipts) > 0 {
		evDir := evidenceDir(d.Store, ticket, n)
		if err := os.MkdirAll(evDir, 0o755); err != nil {
			return fmt.Errorf("verifydeliver: gate: create evidence dir: %w", err)
		}
		for name, data := range round.Receipts {
			if err := os.WriteFile(filepath.Join(evDir, name), data, 0o644); err != nil {
				return fmt.Errorf("verifydeliver: gate: write receipt %s: %w", name, err)
			}
		}
	}
	return nil
}

// appendFixSlices appends every fix slice a round routed, initializing
// each to queued and journaling it. It is shared by the scripted source's
// own fix-slices (round.FixSlices) and a reviewer round's routing output
// (routeRound's slices, passed by Gate).
func appendFixSlices(d Deps, ticket string, n int, slices []store.Slice) error {
	for _, fs := range slices {
		if fs.FromGate == 0 {
			fs.FromGate = n
		}
		if err := d.Store.AppendSlices(ticket, []store.Slice{fs}); err != nil {
			return fmt.Errorf("verifydeliver: gate: append fix slice %s: %w", fs.ID, err)
		}
		if err := d.Store.WriteSliceState(ticket, fs.ID, store.SliceState{State: "queued"}); err != nil {
			return fmt.Errorf("verifydeliver: gate: init fix slice %s state: %w", fs.ID, err)
		}
		if err := journal.Append(d.Store, ticket, journal.Line{Slice: fs.ID, Event: "fix-slice"}); err != nil {
			return fmt.Errorf("verifydeliver: gate: journal fix-slice %s: %w", fs.ID, err)
		}
	}
	return nil
}

// writeReviewerRound writes a reviewer round's files: findings.yaml (with the
// round's probes and skips), findings.md rendered from it, report.yaml (with
// ReviewedSHA), and diff-changelog.md.
func writeReviewerRound(d Deps, ticket string, n int, report GateReport, scope string, reviewedPaths []string, findings []Finding, cleared []string, summary string, probes []ProbeRecord, skips []SkipRecord) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	yamlData, err := marshalFindingsYAML(scope, reviewedPaths, findings, cleared, report.KeptFixed, summary, probes, skips)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: marshal findings.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), yamlData, 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.yaml: %w", err)
	}
	md := renderFindingsMD(n, report.Verdict, summary, findings, cleared, report.KeptFixed)
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(md), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}
	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	if err := writeDiffChangelog(d, ticket, dir, n); err != nil {
		return err
	}
	return nil
}

func writeReportYAML(dir string, report GateReport) error {
	rep := reportYAML{
		Round:        report.Round,
		Verdict:      report.Verdict,
		Model:        report.Model,
		Effort:       report.Effort,
		Jig:          journal.Stamp(),
		TargetSHA:    report.TargetSHA,
		ReviewedSHA:  report.ReviewedSHA,
		ReviewedBase: report.ReviewedBase,
		Intent:       reportIntentYAML{Source: report.Intent.Source, SHA256: report.IntentSHA256},
		FixSlices:    report.FixSlices,
		Independent:  report.Independent,
		CarriedFrom:  report.CarriedFrom,
		OracleNames:  report.OracleNames,
		StoreRefs:    report.StoreRefs,
		CarriedMerge: report.CarriedMerge,
	}
	if report.Independent != "" {
		reviewer := report.Reviewer
		rep.Reviewer = &reviewer
	}
	out, err := yaml.Marshal(rep)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: marshal report.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), out, 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write report.yaml: %w", err)
	}
	return nil
}

func writeDiffChangelog(d Deps, ticket, dir string, n int) error {
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: read journal for diff changelog: %w", err)
	}
	content := journal.RenderDiffChangelog(lines, n)
	if err := os.WriteFile(filepath.Join(dir, "diff-changelog.md"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write diff-changelog.md: %w", err)
	}
	return nil
}
