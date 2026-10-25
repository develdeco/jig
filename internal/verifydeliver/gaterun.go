package verifydeliver

import (
	"errors"
	"fmt"
	"os"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// VerdictNotIndependent is a round's verdict when the reviewer was one of the
// agents that built what it reviewed: whatever it found, the round cannot
// stand behind a publish, so it is not clean (REVIEW_NOT_INDEPENDENT).
const VerdictNotIndependent = "not-independent"

// gateRun is one gate round as far as its review: every precondition has
// passed, the lease is on the copy of the branch the round works on, the
// oracles passed on its head, and the round is numbered and journaled as
// opened. A recorded round (GatePrepare) stops here and hands the review to
// an agent; a scripted or launched one (Gate) asks its source; either way
// finish does the rest.
type gateRun struct {
	d      Deps
	o      GateOpts
	ticket string
	slices []store.Slice

	repoName string
	target   string
	gb       gateBranch
	branch   string
	lease    pool.Lease
	// copy says whose copy of the branch the lease was left at.
	copy branchCopy

	intent     Intent
	intentText string
	// intentSHA256 is the hash report.yaml pins for the intent: of intentText
	// for a round that read the file itself, or the hash the opening of a
	// recorded round took, which is what its reviewer was shown.
	intentSHA256 string

	n          int
	man        manifest.Manifest
	oracleRuns []OracleRun
	fold       Fold

	// journaled is true once the round has put a tracked change in the
	// store's working copy that a failure would otherwise leave uncommitted
	// (the gate-open line, or a recorded round's own files): the deferred
	// best-effort push (pushFailure) is for exactly that.
	journaled bool
}

// pushFailure is the best-effort push a round runs on any error after its
// first journal write: that write (the gate-open line, and any review.json or
// result.json the round wrote) would otherwise stay uncommitted until
// whatever later command next calls Store.Sync, on this ticket or any other.
// It runs at the point of failure so the leftovers land attributed to this
// failure by name instead of swept into some other command's anonymous commit
// (see failureCode for why only the code is in the subject). If this push
// itself fails, the original error is still the one that reaches the caller;
// there is nothing more to do here but try. r may be nil (the round failed
// before it existed).
func (r *gateRun) pushFailure(err error) {
	if r == nil || err == nil || !r.journaled {
		return
	}
	_ = r.d.Store.Push(fmt.Sprintf("%s: gate round %d failed: %s", r.ticket, r.n, failureCode(err)))
}

// gatePrepare runs a gate round up to its review. It always returns the run
// it made, even with an error, so the caller can push what the failed round
// left behind.
//
// Preconditions come before anything this round does is recorded or pushed:
// a ticket short of green refuses with GATE_NOT_GREEN, and that refusal must
// leave no trace - not a pushed intent.md, not a journal line - for a plain
// rerun to be the whole recovery.
func gatePrepare(d Deps, o GateOpts) (*gateRun, error) {
	r := &gateRun{d: d, o: o, ticket: o.Ticket}
	ticket := o.Ticket

	if err := d.Store.Sync(); err != nil {
		return r, fmt.Errorf("verifydeliver: gate: sync store: %w", err)
	}
	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return r, fmt.Errorf("verifydeliver: gate: read slices: %w", err)
	}
	r.slices = slices
	if err := checkFrontier(d, ticket, slices, o.Early); err != nil {
		return r, err
	}

	repo, repoName, target := primaryRepo(d.Cfg)
	r.repoName, r.target = repoName, target
	// Resolved once: the lease and the copy of the branch both name this one
	// branch. The ticket's own record names it, or --branch adopts one
	// (resolveGateBranch); an adoption is recorded further down, once every
	// precondition of the round has passed.
	gb, err := resolveGateBranch(d, ticket, target, o.Branch, slices)
	if err != nil {
		return r, err
	}
	r.gb, r.branch = gb, gb.Name
	// An existing gate lease is restored pristine, and forgets its own copy of
	// the branch, before Acquire ever touches it (restoreLeaseBeforeAcquire):
	// the round re-points the lease at its source below whatever the copy
	// holds.
	if err := restoreLeaseBeforeAcquire(d.Home, repoName, ticket, pool.Gate, r.branch); err != nil {
		return r, err
	}
	// The branch is not required to be on origin: the work may be committed
	// in the operator's clone and not pushed yet, and pointAtBranchCopy
	// refuses (BRANCH_NOT_FOUND) a branch no copy has.
	lease, err := pool.Acquire(d.Home, repoName, repo.Remote, target, r.branch, ticket, pool.Gate)
	if err != nil {
		return r, fmt.Errorf("verifydeliver: gate: acquire lease: %w", err)
	}
	r.lease = lease

	// Restore the gate lease to a pristine, known-correct head right after
	// acquire and before any oracle runs. pool.Acquire never resets an
	// existing local branch (a deliberate rule so a same-run slice's commits
	// on it survive later acquires), so without this, a lease an earlier,
	// killed run left dirty or ahead would have its leftovers reviewed, or on
	// an adopted branch would make this gate review the stale local copy
	// instead of the branch as it stands now.
	//
	// The copy the round reviews is the clone's when the clone has the branch
	// and its copy is the newer, else origin's, else - for work an earlier
	// jig built - the build lease's (pointAtBranchCopy).
	r.copy, err = pointAtBranchCopy(d, lease.Dir, repoName, ticket, r.branch, gb.Adopted, gb.Built, "gate")
	if err != nil {
		return r, err
	}

	// `--intent`/`--doc`: record an explicit intent.md once every
	// precondition above has passed - GATE_NOT_GREEN, and BRANCH_NOT_FOUND -
	// so a refusal on either still leaves no trace: not a written intent.md,
	// not a journal line, for a plain rerun to be the whole recovery. It is a
	// ticket-level record, not specific to this round's branch or frontier
	// state, so nothing here reads lease or branch state; the write only needs
	// to happen after every precondition that can still refuse the round. No
	// separate push: the write is a tracked change like any other this round
	// makes, carried by the round's own end-of-round push on success, or by
	// the deferred failure push once journaled is true. writeExplicitIntent
	// itself refuses INTENT_CONFLICT when the ticket already has a brief.md,
	// which always wins resolution below regardless.
	if o.Intent != "" || o.IntentDoc != "" {
		if err := writeExplicitIntent(d.Store, ticket, o.Intent, o.IntentDoc); err != nil {
			return r, err
		}
	}

	// Intent resolution, once per round, before the round's review and before
	// journaling below: the ticket's own brief.md, else its intent.md
	// (including whatever --intent/--doc just wrote above), else none
	// (resolveIntent's own precedence doc comment). Resolving this before
	// journaling means a malformed intent.md fails the round before a
	// gate-open journal line is appended or an oracle suite runs, rather than
	// after paying for both.
	r.intent, r.intentText, err = resolveIntent(d.Store, ticket)
	if err != nil {
		return r, err
	}
	r.intentSHA256 = intentSHA256(r.intent.Source, r.intentText)

	// Adoption is recorded last of the round's preconditions and first of its
	// effects: a refusal above leaves nothing behind, so a plain rerun is the
	// whole recovery, and every later round and command finds the branch and
	// where it started. The start sha is the branch's tip as the author left
	// it, not the target's: what jig builds on the branch descends from the
	// tip, and verification holds it to that. It replaces any start sha an
	// earlier dispatch left before jig had built anything, which
	// resolveGateBranch has established. The lease's HEAD is the tip of the
	// copy it was just pointed at. The start sha is written first: a failure
	// between the two writes leaves a start sha no record points at, which
	// rerunning the adoption replaces.
	if gb.Adopting {
		tip, err := gitx.RevParse(lease.Dir, "HEAD")
		if err != nil {
			return r, fmt.Errorf("verifydeliver: gate: resolve the tip of %s: %w", r.branch, err)
		}
		if err := d.Store.WriteStartSHA(ticket, repoName, tip); err != nil {
			return r, fmt.Errorf("verifydeliver: gate: record the start sha: %w", err)
		}
		if err := d.Store.WriteTicketBranch(ticket, r.branch); err != nil {
			return r, fmt.Errorf("verifydeliver: gate: record the adopted branch: %w", err)
		}
	}

	// Resolved before the journal line below so r.n is always this round's real
	// number, never the zero value, by the time journaled becomes true and the
	// deferred push can fire.
	n, err := existingGateRounds(d.Store, ticket)
	if err != nil {
		return r, fmt.Errorf("verifydeliver: gate: count rounds: %w", err)
	}
	r.n = n + 1

	if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventGateOpen, Attempt: r.n}); err != nil {
		return r, fmt.Errorf("verifydeliver: gate: journal gate-open: %w", err)
	}
	r.journaled = true

	r.man, err = manifest.Resolve(lease.Dir)
	if err != nil {
		return r, fmt.Errorf("verifydeliver: gate: resolve manifest: %w", err)
	}
	r.oracleRuns, err = runGateOracles(d, ticket, lease.Dir, r.man, slices)
	if err != nil {
		return r, err
	}

	if _, err := os.Stat(gateRoundDir(d.Store, ticket, r.n)); err == nil {
		return r, fmt.Errorf("verifydeliver: gate: round %d already exists", r.n)
	}

	// The cumulative fold over every earlier reviewer round: its open,
	// asked and noted findings become review.json's own open list (a noted
	// finding stays a citable prior target, findings.go's
	// openAndNotedFindingsList), and its dismissed findings become
	// review.json's dismissed list. A ticket with no reviewer rounds yet,
	// or one driven entirely by the scripted source, folds to nothing.
	r.fold, err = FoldBefore(d.Store, ticket, r.n)
	if err != nil {
		return r, fmt.Errorf("verifydeliver: gate: fold findings: %w", err)
	}
	return r, nil
}

// roundInput is what the review of this round is handed: the request is built
// from it (PrepareReviewRequest), and a launched or scripted source reads it.
func (r *gateRun) roundInput() RoundInput {
	return RoundInput{
		Store:         r.d.Store,
		Ticket:        r.ticket,
		Round:         r.n,
		LeaseDir:      r.lease.Dir,
		RepoName:      r.repoName,
		Target:        r.target,
		Branch:        r.branch,
		Effort:        r.d.Cfg.ReviewEffortFor,
		Intent:        r.intent,
		IntentText:    r.intentText,
		OperatorClone: operatorClone(r.d, r.repoName),
		Home:          r.d.Home,
		UserHome:      r.d.UserHome,
		Manifest:      r.man,
		OracleRuns:    r.oracleRuns,
		Open:          r.fold.Open,
		Dismissed:     r.fold.Dismissed,
	}
}

// Gate runs one gate round for ticket on a source that supplies its review
// itself: the scripted source a scenario plays back, or a reviewer session
// jig launches. It re-verifies every manifest oracle in a fresh gate lease
// checked out to the ticket's branch, then asks src for this round's review
// content, and records the round (finish).
//
// `jig gate` does not use it for a real review: a person's or an agent's
// review is recorded by GatePrepare, which opens the round, and GateFinish,
// which closes it once a reviewer jig did not start has reported. Gate stays
// for the scripted source (`jig gate --scenario`, the tests) and the launched
// reviewer of internal/revieweval.
func Gate(d Deps, src GateSource, o GateOpts) (report GateReport, err error) {
	var run *gateRun
	defer func() { run.pushFailure(err) }()
	if run, err = gatePrepare(d, o); err != nil {
		return GateReport{}, err
	}
	round, ok, err := src.Round(run.roundInput())
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: read round %d: %w", run.n, err)
	}
	return run.finish(round, ok, finishInfo{Triage: o.Triage})
}

// finishInfo is what a round's record carries of who reviewed it, beyond the
// review itself: nothing for a scripted or launched round, the reviewer's
// self-reported model and effort, who they are and whether they are
// independent of the builders for a recorded one.
type finishInfo struct {
	// Triage decides the round's fix batch and asks; nil means DefaultTriage.
	Triage Triage
	// Model and Effort are what the reviewer said it ran on, or "".
	Model, Effort string
	// Reviewer is who reported the round, and Independent whether they are
	// independent of the agents that built what they reviewed. Both are the
	// zero value for a round no recorded reviewer reported.
	Reviewer    Provenance
	Independent Independence
	// OracleNames are the manifest's oracle names at this round, kept in
	// report.yaml for the PR body a later publish renders without a checkout.
	OracleNames []string
}

// finish records the round once its review is known: the findings bookkeeping,
// routing and triage, the round's files, the journal lines, the fix slices and
// the push. round and ok are what a source gave back (ok false: nothing ran);
// a recorded round's review comes from the reviewer's result. Everything after
// the review is session-free.
func (r *gateRun) finish(round Round, ok bool, info finishInfo) (GateReport, error) {
	d, ticket, n := r.d, r.ticket, r.n
	slices, man := r.slices, r.man
	lease := r.lease
	branch, repoName, target := r.branch, r.repoName, r.target
	cum := r.fold.Known

	intent, intentText, intentHash := r.intent, r.intentText, r.intentSHA256
	// The reviewer source may have been handed a different intent than the one
	// resolved before the round, so this round's report carries the intent and
	// the exact bytes the round itself handed the reviewer (round.Review),
	// never a second read of brief.md or intent.md, which anything running
	// during the round could have rewritten since. The scripted source runs no
	// reviewer: its round keeps the resolution made when the round opened.
	if round.Review != nil {
		intent, intentText = round.Review.Intent, round.Review.IntentText
		intentHash = round.Review.IntentSHA256
		if intentHash == "" {
			intentHash = intentSHA256(intent.Source, intentText)
		}
	}

	targetSHA, err := gitx.RevParse(lease.Dir, "origin/"+target)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: resolve origin/%s: %w", target, err)
	}

	report := GateReport{
		Round: n, Model: info.Model, Effort: info.Effort, TargetSHA: map[string]string{repoName: targetSHA},
		Intent: intent, IntentSHA256: intentHash,
		Reviewer: info.Reviewer, Independent: info.Independent, OracleNames: info.OracleNames,
		Copy: string(r.copy),
	}
	if r.gb.Adopted {
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
		if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventGateClean, Attempt: n}); err != nil {
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
		// still_present entries are expanded into full ResultFindings here,
		// right before ApplyRound, so recurrence counting, routing, triage,
		// the fix budget and findings.yaml all see exactly what a full
		// re-report of each confirmed finding would give them;
		// round.Review.Result (and the reviewer's raw result.json on disk)
		// stays as the reviewer actually wrote it.
		expanded := ExpandStillPresent(round.Review.Result, cum)
		reported, err := ApplyRound(n, cum, expanded, slices, sliceGreen, man, gateCfg.FixRisks, budgetReached)
		if err != nil {
			return GateReport{}, err
		}
		outstanding := outstandingAsks(cum, reported)
		routed, fixSlices, err := routeRound(n, d.Store, ticket, slices, reported, outstanding, info.Triage, man, budgetUsed, *gateCfg.FixRounds, *gateCfg.FixSliceFindings)
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
		// A review whose reviewer was one of the builders is no review the
		// publish can stand behind: the round is recorded, with its
		// findings, but it is not clean.
		if report.Verdict == "clean" && report.Independent == IndependentNo {
			report.Verdict = VerdictNotIndependent
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
		event := journal.EventGateRound
		if report.Verdict == "clean" {
			event = journal.EventGateClean
		}
		line := journal.Line{Event: event, Effort: firstNonEmpty(round.Review.Effort, info.Effort), Model: info.Model, Attempt: n}
		line.Session, line.Agent = info.Reviewer.Session, info.Reviewer.Agent
		if err := journal.Append(d.Store, ticket, line); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: journal %s: %w", event, err)
		}
		// Routing and triage are already finished above; appending these
		// slices and, at the end of the round, pushing the store are the only
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
		if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventGateRound, Attempt: n}); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: journal gate-round: %w", err)
		}
		if err := appendFixSlices(d, ticket, n, round.FixSlices); err != nil {
			return GateReport{}, err
		}
	}

	if err := d.Store.Push(fmt.Sprintf("%s: gate round %d %s", ticket, n, report.Verdict)); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: push store: %w", err)
	}
	if report.Independent == IndependentNo {
		return report, notIndependentError(ticket, report)
	}
	return report, nil
}

// firstNonEmpty returns the first of its arguments that is not "".
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// notIndependentError is the refusal of a round whose reviewer built what it
// reviewed. The round is on disk when it is returned; what is missing is a
// reviewer who is not one of the builders.
func notIndependentError(ticket string, report GateReport) error {
	who := report.Reviewer.Session
	if report.Reviewer.Agent != "" {
		who += "/" + report.Reviewer.Agent
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("round %d of %s was reviewed by %s, which built part of what it reviewed", report.Round, ticket, who),
		Code: "REVIEW_NOT_INDEPENDENT",
		Help: []string{
			fmt.Sprintf("The round is recorded as %q, not clean: publish needs a review by an agent that did not build the change", report.Verdict),
			fmt.Sprintf("Have a fresh-context reviewer (a different session or subagent, with the `review` skill) review the next round: run `jig gate %s` again", ticket),
		},
	}
}

// errNoOpenRound is the refusal for a verb that closes or extends a round
// when none is open.
func errNoOpenRound(ticket string) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("no review round is open for %s", ticket),
		Code: "REVIEW_NOT_OPEN",
		Help: []string{fmt.Sprintf("Open one with `jig gate %s` (or `jig review context %s`)", ticket, ticket)},
	}
}

// isNotExist reports whether err is, or wraps, a missing file.
func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
