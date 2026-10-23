package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The usage lines of `jig gate`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	gateEarlyUsage    = "gate before the frontier is fully green"
	gateBranchUsage   = "review this branch, built outside jig, and adopt it as the ticket's own (recorded on the first round)"
	gateIntentUsage   = "explicit intent text, recorded as intent.md (refused when the ticket has a brief.md); with no brief, --intent or --doc, the reviewer is pointed at an excerpt of your local Claude Code session that best matches the change"
	gateDocUsage      = "doc file whose content becomes the ticket's explicit intent, recorded as intent.md (refused when the ticket has a brief.md)"
	gateYesUsage      = "when the round is recorded, keep every finding jig can route on its own, without waiting for a person's `jig triage` on each ask"
	gateScenarioUsage = "scenario dir for the scripted gate source (tests)"
	gateStoreUsage    = "explicit store path"
	gateProjectUsage  = "project name, resolved via the machine mapping"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"gate", "open a gate round: re-verify the branch and hand a reviewer what to review", []flagSpec{
			{"early", gateEarlyUsage, false},
			{"branch", gateBranchUsage, false},
			{"intent", gateIntentUsage, false},
			{"doc", gateDocUsage, false},
			{"yes", gateYesUsage, false},
			{"scenario", gateScenarioUsage, true},
			{"store", gateStoreUsage, false},
			{"project", gateProjectUsage, false},
		}, false},
		Order: 160,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdGate(e, args, stdout, stdin)
		},
		Probe: []string{"T-1"},
	})
}

// cmdGate implements `jig gate <ticket> [--early] [--branch <name>] [--intent
// <text> | --doc <path>] [--yes]`: open a gate round over the ticket's branch.
// The branch is read from the operator's clone (committed work only), or
// origin's copy when that is the newer; the oracles are re-verified on its
// head, reusing the passes `jig verify` recorded; and the request a
// reviewer reads is written. jig starts no reviewer: a fresh-context agent
// reads the round with `jig review context`, reports findings with `jig
// finding` and ends with `jig review done`, which records the round.
//
// The hidden --scenario plays back a scripted round instead, for the tests:
// the one-shot path the gate had before its reviewer was an agent of its own.
func cmdGate(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet(e, "gate")
	early := fs.Bool("early", false, gateEarlyUsage)
	branch := fs.String("branch", "", gateBranchUsage)
	intent := fs.String("intent", "", gateIntentUsage)
	doc := fs.String("doc", "", gateDocUsage)
	yes := fs.Bool("yes", false, gateYesUsage)
	scenario := fs.String("scenario", "", gateScenarioUsage)
	storeFlag := fs.String("store", "", gateStoreUsage)
	projectFlag := fs.String("project", "", gateProjectUsage)
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	// --intent "" or --doc "" (the flag explicitly given an empty value) is
	// otherwise indistinguishable from that flag never having been passed
	// at all - *intent/*doc is "" either way - and would silently skip
	// recording anything: for --doc specifically, that means running a
	// whole round against no intent at all rather than refusing it. Worse,
	// checking only *intent != "" && *doc != "" for the "not both" rule
	// misses --intent x --doc "", since the empty *doc reads as unset by
	// value even though --doc was on the command line. fs.Visit sees only
	// flags that actually appeared on the command line, so both are
	// checked that way - by presence, not by their resulting value - and
	// "not both" is refused whenever both appeared, before either
	// set-but-empty check below ever runs. A set-but-empty flag is then
	// refused with INTENT_EMPTY, the code writeExplicitIntent gives an
	// intent text with nothing in it (`--intent ""` and `--intent "  "`
	// are one mistake, so one code), rather than treated as unset.
	var intentFlagSet, docFlagSet bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "intent":
			intentFlagSet = true
		case "doc":
			docFlagSet = true
		}
	})
	if intentFlagSet && docFlagSet {
		return renderErr(stdout, &axi.Error{
			Msg:  "jig gate accepts --intent or --doc, not both",
			Code: "VALIDATION_ERROR",
		})
	}
	if intentFlagSet && *intent == "" {
		return renderErr(stdout, &axi.Error{
			Msg:  "jig gate --intent must not be empty",
			Code: "INTENT_EMPTY",
		})
	}
	if docFlagSet && *doc == "" {
		return renderErr(stdout, &axi.Error{
			Msg:  "jig gate --doc must not be empty",
			Code: "INTENT_EMPTY",
		})
	}

	if *doc, err = e.abs(*doc); err != nil {
		return renderErr(stdout, err)
	}
	if *scenario, err = e.abs(*scenario); err != nil {
		return renderErr(stdout, err)
	}
	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err = resolveTicketArg(st, ticket, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	// --branch adopts a branch for a ticket that may have no slices yet, so it
	// needs only the ticket's folder; without it, the ticket needs work.
	check := requireWork
	if *branch != "" {
		check = requireTicket
	}
	if err := check(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(e, st, cfg, mp, jigHome)
	opts := verifydeliver.GateOpts{
		Ticket:    ticket,
		Early:     *early,
		Branch:    *branch,
		Intent:    *intent,
		IntentDoc: *doc,
		Yes:       *yes,
	}
	if *scenario != "" {
		opts.Triage = triageFor(e, *yes, stdin, stdout)
		report, err := verifydeliver.Gate(deps, verifydeliver.NewFakeGateSource(*scenario), opts)
		if err != nil {
			return renderErr(stdout, err)
		}
		return printGateReport(stdout, st, ticket, report)
	}

	p, err := verifydeliver.GatePrepare(deps, opts)
	if err != nil {
		return renderErr(stdout, err)
	}
	blocks := roundBlocks(p, "", false)
	blocks = append(blocks, axi.Help(
		"Spawn a fresh-context reviewer (a subagent or another session) with the `review` skill: it reads the round with `jig review context "+ticket+"` and ends with `jig review done "+ticket+"`",
		"The agent that built the change must not review it: a reviewer who built part of it makes the round not clean (REVIEW_NOT_INDEPENDENT)",
	))
	axi.Render(stdout, blocks...)
	return 0
}

// printGateReport prints a completed gate round's report. Gate failures
// (oracle failures, env pauses, frontier-not-empty) surface as errors from
// verifydeliver.Gate and are handled by the caller via renderErr before this
// is reached; this function's own return value is the "needs a human" exit
// code: 2 when the round leaves any ask undecided, else 0.
func printGateReport(stdout io.Writer, st *store.Store, ticket string, report verifydeliver.GateReport) int {
	var shaRows [][]string
	for repo, sha := range report.TargetSHA {
		shaRows = append(shaRows, []string{repo, sha})
	}
	intentRow := report.Intent.Source
	if report.IntentNote != "" {
		intentRow = fmt.Sprintf("%s (%s)", report.Intent.Source, report.IntentNote)
	}
	kv := [][2]string{{"ticket", ticket}}
	if report.Branch != "" {
		kv = append(kv, [2]string{"branch", report.Branch})
	}
	if report.Copy != "" {
		kv = append(kv, [2]string{"copy", report.Copy})
	}
	kv = append(kv,
		[2]string{"round", strconv.Itoa(report.Round)},
		[2]string{"verdict", report.Verdict},
	)
	// Who reviewed, as the reviewer said (model and effort are self-reported;
	// jig chooses none) and as jig found out (the reviewer and whether it
	// built what it reviewed). A scripted or launched round has none of it.
	if report.Model != "" {
		kv = append(kv, [2]string{"model", report.Model})
	}
	if report.Effort != "" {
		kv = append(kv, [2]string{"effort", report.Effort})
	}
	if report.Reviewer.Known() {
		kv = append(kv, [2]string{"reviewer", reviewerLabel(report.Reviewer)})
	}
	if report.Independent != "" {
		kv = append(kv, [2]string{"independent", string(report.Independent)})
	}
	kv = append(kv, [2]string{"intent", intentRow})
	if report.Scope != "" {
		kv = append(kv, [2]string{"scope", report.Scope})
	}
	// The fix budget: named only for a round that actually parked
	// findings, saying the budget is reached and how many it parked -
	// jig status is where the budget's ongoing state (used of limit,
	// every round until it changes) lives.
	if report.BudgetParked > 0 {
		kv = append(kv, [2]string{"fix_budget", fmt.Sprintf("reached (%d of %d): %d finding(s) parked", report.BudgetUsed, report.BudgetLimit, report.BudgetParked)})
	}
	blocks := []string{
		axi.KV("gate", kv),
		axi.Table("target_sha", []string{"repo", "sha"}, shaRows),
	}
	// A reviewer round prints these tables whether or not they hold
	// anything, so an empty findings table still says "this round found
	// nothing". Any other round prints one only when it holds something.
	// Scope alone used to gate all three, and a scripted round never sets
	// it: a round that exits 2 over an outstanding ask told the reader to
	// "decide the listed asks" and listed none, while `jig status` and the
	// stored round both named it.
	//
	// Findings are always shown sorted by risk, high first, each with its
	// rationale - report.Findings and .NeedsHuman already come sorted that
	// way (sortByRiskThenID, applied in Gate); these tables add the
	// file:line and risk_rationale columns that carry it.
	if report.Scope != "" || len(report.Findings) > 0 {
		var findingRows [][]string
		for _, f := range report.Findings {
			findingRows = append(findingRows, []string{f.ID, f.Status, f.RoutedAs, fileLine(f), f.Title, f.Risk, f.RiskRationale})
		}
		blocks = append(blocks, axi.Table("findings", []string{"id", "status", "routed_as", "file:line", "title", "risk", "risk_rationale"}, findingRows))
	}
	if report.Scope != "" || len(report.FixSlices) > 0 {
		blocks = append(blocks, axi.Table("fix_slices", []string{"id"}, idRows(report.FixSlices)))
	}
	if len(report.NeedsHuman) > 0 {
		var needsRows [][]string
		for _, f := range report.NeedsHuman {
			needsRows = append(needsRows, []string{f.ID, f.Risk, fileLine(f), f.Title, f.RiskRationale})
		}
		blocks = append(blocks, axi.Table("needs_a_human", []string{"id", "risk", "file:line", "title", "risk_rationale"}, needsRows))
	}
	blocks = append(blocks, axi.Help(gateReportHint(st, ticket, report)...))
	axi.Render(stdout, blocks...)
	if len(report.NeedsHuman) > 0 {
		return 2
	}
	return 0
}

// reviewerLabel names a reviewer for a report row: its session, then its
// agent when it is a subagent, then how jig learned who it is.
func reviewerLabel(p verifydeliver.Provenance) string {
	label := p.Session
	if p.Agent != "" {
		label += "/" + p.Agent
	}
	if p.How != "" {
		label += " (" + p.How + ")"
	}
	return label
}

// gateReportHint names the way forward after this round. When it leaves any
// ask undecided, that is a person's decision, recorded with `jig triage`,
// which the next round applies. The ticket's own next step comes first when
// the frontier is short of green (hintOrFallback, exactly what `jig status`
// would print), because a round is refused while any slice is short of green
// and that is what actually moves the frontier; the decisions and the next
// round follow it. The hint reads the frontier itself (frontierGreen, the
// condition the next round's check applies), never a proxy such as this
// round's fix slices: `jig gate --early` can leave an ask with no fix slice
// queued while the frontier is still short of green, and one parked on an
// unanswered question advances only once it is answered (`jig answer`). With
// nothing undecided the hint is that same next-step line alone.
func gateReportHint(st *store.Store, ticket string, report verifydeliver.GateReport) []string {
	if report.Verdict == verifydeliver.VerdictNotIndependent {
		return []string{fmt.Sprintf("Have a fresh-context reviewer, who did not build the change, review it: `jig gate %s`", ticket)}
	}
	if len(report.NeedsHuman) > 0 {
		decide := fmt.Sprintf("Decide the listed asks with `jig triage %s <id> keep|dismiss`, then run `jig gate %s`: the next round applies the decisions", ticket, ticket)
		if !frontierGreen(st, ticket) {
			// What actually moves the frontier is the ticket's own next
			// step, the same one `jig status` prints.
			return []string{hintOrFallback(st, ticket), decide}
		}
		return []string{decide}
	}
	return []string{hintOrFallback(st, ticket)}
}
