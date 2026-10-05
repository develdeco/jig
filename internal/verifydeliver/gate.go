package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// Round is one gate round's content, whether played back by a fake source
// (tests) or produced by a real reviewer session. Review is set only by the
// reviewer source (review.go): its validated result plus scope data, which
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
	// one or jig/<ticket>.
	Branch string
	// Intent and IntentDoc are `jig gate --intent`/`--doc`: at most one is
	// ever set - refused when both are, whether by the CLI or by a caller
	// going straight to GateOpts - valid in every mode, and write the
	// ticket's intent.md as an explicit, binding intent - refused with
	// INTENT_CONFLICT when the ticket already has a brief.md. Intent is the
	// literal text; IntentDoc is a file whose content becomes that text.
	Intent    string
	IntentDoc string
	PRMode    bool
	// NoDemo (`jig gate --no-demo`) skips the demo session a clean reviewer
	// round otherwise runs (demo.go). A scripted round never runs one either
	// way.
	NoDemo bool
	// Triage is the human seam for a reviewer round's fix batch and ask
	// findings (route.go). nil means DefaultTriage: every fix is
	// kept, every ask with a full build target is kept, one missing part
	// of it stays undecided. Unused for a scripted (--scenario) round.
	Triage Triage
}

// GateReport is Gate's result.
type GateReport struct {
	Round     int
	Verdict   string // clean|fix-slices
	TargetSHA map[string]string
	Model     string
	// ReviewedSHA is repoName -> the head sha a reviewer round reviewed, on
	// every reviewer round, clean or not - the anchor the next round's
	// scope resolves against (review.go's resolveScopeBase). nil for a
	// scripted round.
	ReviewedSHA map[string]string
	// Scope is this round's scope diff kind (full|delta), "" for a
	// scripted round.
	Scope string
	// Findings is every finding this round reported, after routing and
	// triage set each one's final Status/Triage/Decision/
	// RoutedAs. nil for a scripted round.
	Findings []Finding
	// FixSlices is the ids of the fix slices this round appended, in the
	// order they were built.
	FixSlices []string
	// NeedsHuman is every asked finding still undecided after this round
	// (across every round, not only this one's own): the "needs a human"
	// list, the exit-2 signal. Empty when nothing is waiting on a person.
	NeedsHuman []Finding
	// Branch is the branch the ticket adopted, "" for a ticket working on
	// its own jig/<ticket>.
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
	// Demo is what the round's demo session came to: set only for a clean
	// reviewer round that was not run with NoDemo, nil for every other round.
	// It never changes Verdict.
	Demo *DemoReport
	// BudgetParked is how many findings this round parked under the fix
	// budget (status asked, RoutedWhyBudget): 0 for a round that parked
	// none, a scripted round (the budget never forces one of its findings),
	// or a round with nothing new to route at all. BudgetUsed and
	// BudgetLimit are this round's own view of the fix budget (the
	// ticket's earlier fix-appending rounds, and gate.fix_rounds),
	// meaningful only alongside BudgetParked > 0.
	BudgetParked            int
	BudgetUsed, BudgetLimit int
}

// reportYAML is gate/round-<n>/report.yaml's exact on-disk shape.
type reportYAML struct {
	Round       int               `yaml:"round"`
	Verdict     string            `yaml:"verdict"`
	Model       string            `yaml:"model"`
	TargetSHA   map[string]string `yaml:"target_sha"`
	ReviewedSHA map[string]string `yaml:"reviewed_sha,omitempty"`
	Intent      reportIntentYAML  `yaml:"intent"`
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
// the same tolerance OutstandingAsks and `jig status`'s own round-reading
// already apply, since one corrupt earlier round is not reason enough to
// treat every ticket's budget as exhausted or to refuse every later
// round. Gate and `jig status` share this one reading, so neither can
// disagree with the other about how much of the budget is used.
func UsedFixBudget(st *store.Store, ticket string, round int) (used int, unreadable []int) {
	for r := 1; r < round; r++ {
		data, err := os.ReadFile(filepath.Join(gateRoundDir(st, ticket, r), "report.yaml"))
		if err != nil {
			if !os.IsNotExist(err) {
				unreadable = append(unreadable, r)
			}
			continue
		}
		var rep reportYAML
		if err := yaml.Unmarshal(data, &rep); err != nil {
			unreadable = append(unreadable, r)
			continue
		}
		if len(rep.FixSlices) > 0 {
			used++
		}
	}
	return used, unreadable
}

// Gate runs one gate round for ticket: it re-verifies every manifest
// oracle in a fresh gate lease checked out to the ticket's branch, then
// asks src for this round's review content.
func Gate(d Deps, src GateSource, o GateOpts) (report GateReport, err error) {
	if o.PRMode {
		return GateReport{}, &axi.Error{Msg: "gate pr-mode ships in v0.2", Code: "NOT_IMPLEMENTED"}
	}

	ticket := o.Ticket
	// journaled and roundNum back the deferred best-effort push below: once
	// this round's own gate-open journal line has been appended (a tracked
	// change to the store's working copy), any later error in this
	// function - REVIEW_INVALID, REVIEW_FAILED, GATE_NO_ORACLE, an oracle
	// failure, a routing error - would otherwise leave that journal line
	// (and any review.json/result.json this round wrote) uncommitted until
	// whatever later command next calls Store.Sync, on this ticket or any
	// other (see failureCode for why this push runs here rather than
	// waiting on Sync; Publish runs the same pattern on its own failures).
	var (
		journaled bool
		roundNum  int
	)
	defer func() {
		if err == nil || !journaled {
			return
		}
		// Best-effort: if this push itself fails, the original error is
		// still the one that reaches the caller; there is nothing more to
		// do here but try.
		_ = d.Store.Push(fmt.Sprintf("%s: gate round %d failed: %s", ticket, roundNum, failureCode(err)))
	}()

	if err := d.Store.Sync(); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: sync store: %w", err)
	}

	// Preconditions before anything this round does is recorded or pushed:
	// a ticket short of green refuses with GATE_NOT_GREEN below, and that
	// refusal must leave no trace - not a pushed intent.md, not a journal
	// line - for a plain rerun to be the whole recovery.
	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: read slices: %w", err)
	}
	if err := checkFrontier(d, ticket, slices, o.Early); err != nil {
		return GateReport{}, err
	}

	repo, repoName, target := primaryRepo(d.Cfg)
	// Resolved once: the lease and the fetch from the build lease both name
	// this one branch. The ticket's own record names it, or --branch adopts
	// one (resolveGateBranch); an adoption is recorded further down, once
	// every precondition of the round has passed.
	gb, err := resolveGateBranch(d, ticket, target, o.Branch, slices)
	if err != nil {
		return GateReport{}, err
	}
	branch := gb.Name
	// An existing gate lease is restored pristine, and forgets its own copy of
	// the branch, before Acquire ever touches it (restoreLeaseBeforeAcquire):
	// the round re-points the lease at its source below whatever the copy
	// holds.
	if err := restoreLeaseBeforeAcquire(d.Home, repoName, ticket, pool.Gate, branch); err != nil {
		return GateReport{}, err
	}
	// A branch the ticket adopted is on origin by definition, so one that is
	// not there is refused (BRANCH_NOT_FOUND) instead of being cut from the
	// target: the round would review a branch that was never pushed.
	var acquireOpts []pool.Option
	if gb.Adopted {
		acquireOpts = append(acquireOpts, pool.MustExistOnOrigin())
	}
	lease, err := pool.Acquire(d.Home, repoName, repo.Remote, target, branch, ticket, pool.Gate, acquireOpts...)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: acquire lease: %w", err)
	}

	// Restore the gate lease to a pristine, known-correct head right after
	// acquire and before any oracle runs. pool.Acquire never resets an
	// existing local branch (a deliberate rule so a same-run slice's commits
	// on it survive later acquires), so without this, a reviewer or an
	// oracle that left the lease dirty or ahead on an earlier, killed jig
	// has its leftovers reviewed by this round's own oracles, or on an
	// adopted branch makes this gate review the stale local copy instead of
	// origin's current branch tip.
	//
	// The copy the round reviews is an adopted branch jig has built nothing on,
	// which is the author's, origin's copy exactly (the gate lease never
	// commits); and any other is whichever copy holds jig's commits
	// (chooseBuiltCopy): the build lease's while origin has no copy of the
	// branch, as the ticket's own jig/<ticket> does until a publish pushes it,
	// and after that whichever copy the build lease's stands to origin's says
	// holds them.
	if _, err := pointAtTicketBranch(d, lease.Dir, repoName, ticket, branch, gb.Adopted, gb.Built, "gate"); err != nil {
		return GateReport{}, err
	}

	// `--intent`/`--doc`: record an explicit intent.md once every
	// precondition above has passed - GATE_NOT_GREEN, and, in --branch
	// mode, BRANCH_NOT_FOUND - so a refusal on either still leaves no
	// trace: not a written intent.md, not a journal line, for a plain
	// rerun to be the whole recovery. It is a ticket-level record, not
	// specific to this round's branch or frontier state, so nothing here
	// reads lease or branch state; the write only needs to happen after
	// every precondition that can still refuse the round. No separate
	// push: the write is a tracked change like any other this round makes,
	// carried by the round's own end-of-Gate push on success, or by the
	// deferred failure push below once journaled is true.
	// writeExplicitIntent itself refuses INTENT_CONFLICT when the ticket
	// already has a brief.md, which always wins resolution below
	// regardless.
	if o.Intent != "" || o.IntentDoc != "" {
		if err := writeExplicitIntent(d.Store, ticket, o.Intent, o.IntentDoc); err != nil {
			return GateReport{}, err
		}
	}

	// Intent resolution, once per round, before the round's source runs
	// and before journaling below: the ticket's own brief.md, else its
	// intent.md (including whatever --intent/--doc just wrote above), else
	// none (resolveIntent's own precedence doc comment). Resolving this
	// before journaling means a malformed intent.md fails the round before
	// a gate-open journal line is appended or an oracle suite runs, rather
	// than after paying for both.
	intent, intentText, err := resolveIntent(d.Store, ticket)
	if err != nil {
		return GateReport{}, err
	}

	// Adoption is recorded last of the round's preconditions and first of its
	// effects: a refusal above leaves nothing behind, so a plain rerun is the
	// whole recovery, and every later round and command - `jig run` above
	// all - finds the branch and where it started. The start sha is the
	// branch's tip as the author left it, not the target's: what jig builds
	// on the branch descends from the tip, and frontier's verifyGreen holds
	// it to that. It replaces any start sha an earlier dispatch left before
	// jig had built anything, which resolveGateBranch has established.
	// origin/<branch> is the tip the lease was just reset to. The start sha
	// is written first: a failure between the two writes leaves a start sha
	// no record points at, which rerunning the adoption replaces. It is
	// frontier that keeps it current until jig builds: see ensureStartSHA.
	if gb.Adopting {
		tip, err := gitx.RevParse(lease.Dir, "origin/"+branch)
		if err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: resolve origin/%s: %w", branch, err)
		}
		if err := d.Store.WriteStartSHA(ticket, repoName, tip); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: record the start sha: %w", err)
		}
		if err := d.Store.WriteTicketBranch(ticket, branch); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: record the adopted branch: %w", err)
		}
	}

	// Resolved before the journal line below (not after, as manifest
	// resolution and oracle runs once were) so roundNum is always this
	// round's real number, never the zero value, by the time journaled
	// becomes true and the deferred push above can fire.
	n, err := existingGateRounds(d.Store, ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: count rounds: %w", err)
	}
	n++
	roundNum = n

	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: read journal: %w", err)
	}
	model := staircase.Disjoint(d.Rungs, journal.BuilderModels(lines))
	if err := journal.Append(d.Store, ticket, journal.Line{Slice: "", Event: "gate-open", Model: model}); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: journal gate-open: %w", err)
	}
	journaled = true

	man, err := manifest.Resolve(lease.Dir)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: resolve manifest: %w", err)
	}

	oracleRuns, err := runGateOracles(d, ticket, lease.Dir, man, slices)
	if err != nil {
		return GateReport{}, err
	}

	roundDir := gateRoundDir(d.Store, ticket, n)
	if _, err := os.Stat(roundDir); err == nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: round %d already exists", n)
	}

	// The cumulative fold over every earlier reviewer round: its open,
	// asked and noted findings become review.json's own open list (a noted
	// finding stays a citable prior target, findings.go's
	// openAndNotedFindingsList), and its dismissed findings become
	// review.json's dismissed list. A ticket with no reviewer rounds yet,
	// or one driven entirely by the scripted source, folds to nothing.
	fold, err := FoldBefore(d.Store, ticket, n)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: fold findings: %w", err)
	}
	cum := fold.Known

	round, ok, err := src.Round(RoundInput{
		Store:         d.Store,
		Ticket:        ticket,
		Round:         n,
		LeaseDir:      lease.Dir,
		RepoName:      repoName,
		Target:        target,
		Model:         model,
		Intent:        intent,
		IntentText:    intentText,
		OperatorClone: operatorClone(d, repoName),
		Home:          d.Home,
		UserHome:      d.UserHome,
		Manifest:      man,
		OracleRuns:    oracleRuns,
		Open:          fold.Open,
		Dismissed:     fold.Dismissed,
	})
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: read round %d: %w", n, err)
	}

	// The reviewer source may have just inferred and recorded an intent.md
	// before its dispatch (review.go's inferIntent), so this round's report
	// carries the intent and the exact bytes the round itself handed the
	// reviewer (round.Review), not the pre-dispatch resolution above -
	// never a second read of brief.md or intent.md, which anything running
	// during the round could have rewritten since. The scripted source runs
	// no reviewer: its round keeps the resolution above.
	if round.Review != nil {
		intent, intentText = round.Review.Intent, round.Review.IntentText
	}

	targetSHA, err := gitx.RevParse(lease.Dir, "origin/"+target)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: resolve origin/%s: %w", target, err)
	}

	report = GateReport{
		Round: n, Model: model, TargetSHA: map[string]string{repoName: targetSHA},
		Intent: intent, IntentSHA256: intentSHA256(intent.Source, intentText),
	}
	if gb.Adopted {
		report.Branch = branch
	}
	if round.Review != nil {
		report.IntentNote = round.Review.IntentNote
	}
	switch {
	case !ok:
		// No round ran at all. The reviewer source reaches this only when
		// it has already established that nothing is outstanding, so it
		// is the scripted source - no round directory for this number, or
		// an empty one - that can arrive here with the fold still holding
		// something. Either way, a source having nothing to play says
		// nothing about what earlier rounds left behind, so the verdict
		// comes from the fold, exactly
		// as the reviewer arm's does below. Writing "clean" here without
		// consulting it declared a ticket clean over an undecided ask,
		// and `jig publish` gates on that verdict: the ticket shipped
		// with the question never answered, while `jig status`, which
		// reads the fold, said otherwise the whole time.
		if !isClean(cum) {
			report.Verdict = "fix-slices"
			report.Findings = openFindingsList(cum)
			sortByRiskThenID(report.Findings)
			report.NeedsHuman = askedFindingsList(cum)
			sortByRiskThenID(report.NeedsHuman)
			if err := writeNoRound(d, ticket, n, report); err != nil {
				return GateReport{}, err
			}
			break
		}
		report.Verdict = "clean"
		if err := writeCleanRound(d, ticket, n, report); err != nil {
			return GateReport{}, err
		}
		if err := journal.Append(d.Store, ticket, journal.Line{Event: "gate-clean", Attempt: n}); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: journal gate-clean: %w", err)
		}
	case round.Review != nil:
		// Findings bookkeeping: apply this round onto the fold. Routing and
		// triage (route.go) then turn kept fixes and asks into fix slices,
		// mutating each reported finding's final Status/Triage/Decision/
		// RoutedAs - entirely in memory, before anything is persisted or
		// pushed. Rule 3's clearing runs only after that, against those
		// final statuses: a finding dismissed at triage must not go on
		// blocking an unrelated open finding in the same file. The
		// post-round fold is then checked for what's still outstanding:
		// that, not whether the round dispatched a reviewer, decides clean
		// vs fix-slices.
		reviewHead := round.Review.HeadSHA
		existsAtHead := func(file string) (bool, error) {
			return gitx.FileExistsAtRev(lease.Dir, reviewHead, file)
		}
		sliceGreen := func(sliceID string) (bool, error) {
			st, err := d.Store.ReadSliceState(ticket, sliceID)
			if err != nil {
				return false, err
			}
			return st.State == "green", nil
		}
		gateCfg := d.Cfg.ResolvedGateConfig()
		// The fix budget: a ticket's used budget is the count of its
		// earlier gate rounds (1..n-1) that appended at least one fix
		// slice; once that equals gate.fix_rounds, this round parks every
		// would-be open fix instead of queuing it (ApplyRound's own
		// budgetReached parameter).
		budgetUsed, _ := UsedFixBudget(d.Store, ticket, n)
		budgetReached := budgetUsed >= *gateCfg.FixRounds
		reported, err := ApplyRound(n, cum, round.Review.Result, slices, sliceGreen, man, gateCfg.FixRisks, budgetReached)
		if err != nil {
			return GateReport{}, err
		}
		outstanding := outstandingAsks(cum, reported)
		routed, fixSlices, err := routeRound(n, d.Store, ticket, slices, reported, outstanding, o.Triage, man, budgetUsed, *gateCfg.FixRounds, *gateCfg.FixSliceFindings)
		if err != nil {
			return GateReport{}, err
		}
		for _, f := range routed {
			if f.Status == StatusAsked && f.RoutedWhy == RoutedWhyBudget {
				report.BudgetParked++
			}
		}
		if report.BudgetParked > 0 {
			report.BudgetUsed, report.BudgetLimit = budgetUsed, *gateCfg.FixRounds
		}
		cleared, err := ClearingAfterTriage(cum, routed, round.Review.Result.ReviewedPaths, existsAtHead)
		if err != nil {
			return GateReport{}, err
		}
		updated := cloneFindings(cum)
		foldFindings(updated, routed, cleared)
		if isClean(updated) {
			report.Verdict = "clean"
		} else {
			report.Verdict = "fix-slices"
		}
		// reviewed_sha is recorded on every reviewer round, clean or not,
		// so the next round's scope can resolve a delta against it.
		report.ReviewedSHA = map[string]string{repoName: round.Review.HeadSHA}
		report.Scope = round.Review.Scope
		// Findings and NeedsHuman are sorted by risk, high first, then id
		// (sortByRiskThenID, the same helper routing sorts the human seam
		// with): "always shown sorted by risk, high first" applies to every
		// place findings reach a human, not only the triage prompt - the
		// gate report's own tables (cmd/jig) render these two lists as
		// given, so the ordering has to be right here.
		report.Findings = append([]Finding(nil), routed...)
		sortByRiskThenID(report.Findings)
		report.NeedsHuman = askedFindingsList(updated)
		sortByRiskThenID(report.NeedsHuman)
		for _, fs := range fixSlices {
			report.FixSlices = append(report.FixSlices, fs.ID)
		}
		if err := writeReviewerRound(d, ticket, n, report, round.Review.Scope, round.Review.Result.ReviewedPaths, routed, cleared, round.Review.Result.Summary); err != nil {
			return GateReport{}, err
		}
		event := "gate-round"
		if report.Verdict == "clean" {
			event = "gate-clean"
		}
		if err := journal.Append(d.Store, ticket, journal.Line{Event: event, Attempt: n}); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: journal %s: %w", event, err)
		}
		// Routing and triage are already finished above; appending these
		// slices and, at the end of Gate, pushing the store are the only
		// on-disk/store-visible effects that follow.
		if err := appendFixSlices(d, ticket, n, fixSlices); err != nil {
			return GateReport{}, err
		}
	default:
		report.Verdict = "fix-slices"
		// The scripted (scenario) source keeps appending its own fix
		// slices unchanged, with no budget enforcement of its own, but
		// they still count toward the budget for a later, reviewer-driven
		// round: report.yaml has to list them the same way a reviewer
		// round's own routing does, for UsedFixBudget to see them.
		for _, fs := range round.FixSlices {
			report.FixSlices = append(report.FixSlices, fs.ID)
		}
		if err := writeFixRound(d, ticket, n, report, round); err != nil {
			return GateReport{}, err
		}
		if err := journal.Append(d.Store, ticket, journal.Line{Event: "gate-round", Attempt: n}); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: journal gate-round: %w", err)
		}
		if err := appendFixSlices(d, ticket, n, round.FixSlices); err != nil {
			return GateReport{}, err
		}
	}

	if err := d.Store.Push(fmt.Sprintf("%s: gate round %d %s", ticket, n, report.Verdict)); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: push store: %w", err)
	}

	// The demo runs last, once everything the round wrote is committed and
	// pushed, and never as part of the round: gateDemo returns no error, and
	// a failed or refused demo is recorded and reported beside a verdict it
	// cannot change. Only a reviewer source can dispatch one, and only a
	// reviewer round (round.Review) that came back clean is worth showing.
	if round.Review != nil && report.Verdict == "clean" && !o.NoDemo {
		if ds, ok := src.(DemoSource); ok {
			demo := gateDemo(d, ds, DemoInput{
				Store:    d.Store,
				Ticket:   ticket,
				Round:    n,
				LeaseDir: lease.Dir,
				Model:    model,
				Intent:   intent,
				HeadSHA:  round.Review.HeadSHA,
			}, repoName, target)
			report.Demo = &demo
		}
	}

	return report, nil
}

// checkFrontier errors when any slice - including a fix slice raised by an
// earlier gate round - is not yet green and early is false: a slice left
// queued, stalled, needs-input, env-blocked or otherwise unfinished means
// the delivery is incomplete, so gating (or publishing) now would review or
// ship less than the whole ticket. --early skips the check entirely, for a
// deliberate mid-ticket milestone batch.
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
			return &axi.Error{
				Msg:  fmt.Sprintf("slice %s is %s, not green; run `jig run %s` first or pass --early", s.ID, st.State, ticket),
				Code: "GATE_NOT_GREEN",
				Help: []string{fmt.Sprintf("Run `jig run %s` to finish the frontier, or `jig gate %s --early` to review anyway.", ticket, ticket)},
			}
		}
	}
	return nil
}

// fetchTicketBranchFromBuildLease points the lease's ticket branch, branch
// (the name the caller resolved for the ticket once), at the build lease's
// copy, in the pool under the jig home root jigHome: gate and publish leases
// are separate clones, and the ticket branch exists only in the build lease
// until publish pushes it.
func fetchTicketBranchFromBuildLease(jigHome, leaseDir, repoName, ticket, branch string) error {
	buildLeaseDir, err := pool.Dir(jigHome, repoName, ticket, pool.Build)
	if err != nil {
		return fmt.Errorf("verifydeliver: resolve build lease: %w", err)
	}
	refspec := fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branch, branch)
	// Fetching into refs/heads/<branch> fails while that branch is the
	// lease's own checked-out HEAD, so step off it first.
	if _, err := gitx.Run(leaseDir, "checkout", "--detach", "HEAD"); err != nil {
		return fmt.Errorf("verifydeliver: detach HEAD before fetch: %w", err)
	}
	if _, err := gitx.Run(leaseDir, "fetch", buildLeaseDir, refspec); err != nil {
		return fmt.Errorf("verifydeliver: fetch %s from build lease: %w", branch, err)
	}
	if _, err := gitx.Run(leaseDir, "checkout", branch); err != nil {
		return fmt.Errorf("verifydeliver: checkout %s: %w", branch, err)
	}
	return nil
}

// runGateOracles brings up every env class referenced by a slice, runs
// every manifest oracle across every workspace, then tears the env classes
// back down, and returns the oracle runs, all passed. An unavailable env
// with a defer-ci policy journals and skips; one with no policy pauses the
// gate with a NEEDS_INPUT error.
func runGateOracles(d Deps, ticket, dir string, man manifest.Manifest, slices []store.Slice) ([]OracleRun, error) {
	var handles []*envrun.Handle
	defer func() {
		for _, h := range handles {
			_ = h.Down()
		}
	}()

	seen := map[string]bool{}
	for _, s := range slices {
		if s.Env == "" || seen[s.Env] {
			continue
		}
		seen[s.Env] = true
		ec, ok := man.Envs[s.Env]
		if !ok {
			continue
		}
		h, err := envrun.Up(ec, ticket, dir)
		if err != nil {
			var un *envrun.Unavailable
			if errors.As(err, &un) {
				if un.Policy == "defer-ci" {
					if jerr := journal.Append(d.Store, ticket, journal.Line{Event: "env-unavailable", Outcome: "defer-ci"}); jerr != nil {
						return nil, fmt.Errorf("verifydeliver: gate: journal env-unavailable: %w", jerr)
					}
					continue
				}
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("env class %q is unavailable and needs input: %v", s.Env, err),
					Code: axi.NeedsInput,
				}
			}
			return nil, fmt.Errorf("verifydeliver: gate: bring up env %q: %w", s.Env, err)
		}
		handles = append(handles, h)
	}

	return runOracleSuite(dir, man)
}

// runOracleSuite runs every manifest oracle across every manifest
// workspace in dir, workspaces in manifest order and oracles by name, and
// returns the runs it made. Any failure stops the suite, so a nil error
// means every returned run passed.
func runOracleSuite(dir string, man manifest.Manifest) ([]OracleRun, error) {
	var runs []OracleRun
	for _, ws := range man.Workspaces {
		for _, name := range SortedOracleNames(man) {
			cmd := man.OracleCmd(name, ws)
			if err := envrun.Shell(shortenQuotedPath(cmd), dir); err != nil {
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("oracle %q failed in workspace %s: %v", name, ws.ID, err),
					Code: "GATE_ORACLE_FAILED",
				}
			}
			runs = append(runs, OracleRun{Oracle: name, Workspace: ws.ID, Command: cmd})
		}
	}
	return runs, nil
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
		if err := journal.Append(d.Store, ticket, journal.Line{Slice: fs.ID, Event: "fix-slice", Attempt: n}); err != nil {
			return fmt.Errorf("verifydeliver: gate: journal fix-slice %s: %w", fs.ID, err)
		}
	}
	return nil
}

// writeReviewerRound writes a reviewer round's files: findings.yaml,
// findings.md rendered from it, report.yaml (with
// ReviewedSHA), and diff-changelog.md.
func writeReviewerRound(d Deps, ticket string, n int, report GateReport, scope string, reviewedPaths []string, findings []Finding, cleared []string, summary string) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	yamlData, err := marshalFindingsYAML(scope, reviewedPaths, findings, cleared, summary)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: marshal findings.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), yamlData, 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.yaml: %w", err)
	}
	md := renderFindingsMD(n, report.Verdict, summary, findings, cleared)
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
	out, err := yaml.Marshal(reportYAML{
		Round:       report.Round,
		Verdict:     report.Verdict,
		Model:       report.Model,
		TargetSHA:   report.TargetSHA,
		ReviewedSHA: report.ReviewedSHA,
		Intent:      reportIntentYAML{Source: report.Intent.Source, SHA256: report.IntentSHA256},
		FixSlices:   report.FixSlices,
	})
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

// resetLeasePristine hard-resets leaseDir to head and removes every
// untracked file and directory. It never uses `clean -x`, so ignored build
// caches (e.g. node_modules) survive; only content git itself would track or
// that a reviewer left behind is wiped.
func resetLeasePristine(leaseDir, head string) error {
	if _, err := gitx.Run(leaseDir, "reset", "--hard", head); err != nil {
		return err
	}
	if _, err := gitx.Run(leaseDir, "clean", "-fd"); err != nil {
		return err
	}
	return nil
}

// restoreLeaseBeforeAcquire readies the ticket's gate or publish lease, if
// there is one, for the Acquire that follows: it restores the lease pristine
// at its current HEAD and forgets its own copy of branch (dropLeaseBranch).
// Both roles re-point the lease at the copy they work on right after
// acquiring it, so neither keeps anything of its own on the branch.
//
// The restore comes first. A reviewer that outlived a killed jig, or an oracle
// rewrite left over from an earlier attempt, can leave tracked dirt in the
// lease; if the ticket branch later advances past whatever file that dirt
// touched, Acquire's own `git checkout` (and, in normal mode,
// fetchTicketBranchFromBuildLease's checkout right after) refuses with "local
// changes ... would be overwritten" before any restore after the acquire ever
// runs, wedging every later attempt at the same point. It is best-effort and
// runs only in a pool.Usable lease: a reset where git resolves an enclosing
// repository would discard that repository's work, and one on an unborn HEAD
// would fail. Anything else is left to Acquire, which refuses a bad ticket id,
// clones where there is no lease yet, and moves aside anything git shows is
// not a repository of its own.
func restoreLeaseBeforeAcquire(jigHome, repoName, ticket string, role pool.Role, branch string) error {
	leaseDir, err := pool.Dir(jigHome, repoName, ticket, role)
	if err != nil || !pool.Usable(leaseDir) {
		return nil
	}
	if err := resetLeasePristine(leaseDir, "HEAD"); err != nil {
		return fmt.Errorf("verifydeliver: %s: restore existing lease before acquire: %w", role, err)
	}
	if err := dropLeaseBranch(leaseDir, branch); err != nil {
		return fmt.Errorf("verifydeliver: %s: drop the lease's own %s before acquire: %w", role, branch, err)
	}
	return nil
}

// dropLeaseBranch forgets a gate or publish lease's own local copy of branch,
// leaving its HEAD detached. Neither lease keeps commits of its own on the
// branch - Gate and Publish each re-point the lease at the copy they work on
// right after acquiring it - so its copy is disposable; left in place, one
// that fell behind or diverged from origin's since the last round would fail
// Acquire's sync with the branch (BRANCH_DIVERGED) before the command could
// replace it, over commits that are not the lease's to keep.
func dropLeaseBranch(leaseDir, branch string) error {
	if _, err := gitx.Run(leaseDir, "checkout", "--detach", "HEAD"); err != nil {
		return fmt.Errorf("detach HEAD: %w", err)
	}
	if _, err := gitx.Run(leaseDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return nil // no local copy
	}
	if _, err := gitx.Run(leaseDir, "branch", "-D", branch); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}
	return nil
}
