package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// gateSourceFor picks the GateSource for a `jig gate` invocation. The
// compatibility rule: `jig gate` never had --backend before,
// so the old scripted source (NewFakeGateSource) runs iff --scenario is
// set AND --backend is not - every old invocation behaves exactly as
// before. Any other combination (including --backend fake --scenario X)
// dispatches a real reviewer session, played back by whichever backend
// backendName resolves.
func gateSourceFor(backendFlag, scenario string) (verifydeliver.GateSource, error) {
	if scenario != "" && backendFlag == "" {
		return verifydeliver.NewFakeGateSource(scenario), nil
	}
	kind := backendName(backendFlag, scenario)
	if err := session.Available(kind); err != nil {
		return nil, err
	}
	backend, err := session.New(kind, session.Options{ScenarioDir: scenario})
	if err != nil {
		return nil, err
	}
	return verifydeliver.NewReviewerGateSource(backend), nil
}

// cmdGate implements `jig gate <ticket> [--early] [--branch <name>] [--intent
// <text> | --doc <path>] [--pr <n>] [--yes] [--backend fake|headless|herdr]
// [--scenario <dir>]`.
func cmdGate(args []string, stdout io.Writer, stdin io.Reader) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("gate")
	early := fs.Bool("early", false, "gate before the frontier is fully green")
	branch := fs.String("branch", "", "validate this branch instead of the ticket's branch (jig/<ticket> unless one is recorded)")
	intent := fs.String("intent", "", "explicit intent text, recorded as intent.md (refused when the ticket has a brief.md); with no brief, --intent or --doc, a reviewer round reads your local Claude Code sessions for this repo and has a model summarize the best match into intent.md")
	doc := fs.String("doc", "", "doc file whose content becomes the ticket's explicit intent, recorded as intent.md (refused when the ticket has a brief.md)")
	prNum := fs.Int("pr", 0, "pr number (not implemented in v0.1)")
	yes := fs.Bool("yes", false, "keep every finding jig can route on its own, without the triage prompt")
	backendFlag := fs.String("backend", "", "session backend for the reviewer: fake, headless, or herdr")
	scenario := fs.String("scenario", "", "scenario dir for the fake gate source")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
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

	st, cfg, mp, jigHome, err := resolveStoreForProject(*projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}
	check := requireSlices
	if *branch != "" {
		check = requireTicket
	}
	if err := check(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	src, err := gateSourceFor(*backendFlag, *scenario)
	if err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(st, cfg, mp, jigHome)
	report, err := verifydeliver.Gate(deps, src, verifydeliver.GateOpts{
		Ticket:    ticket,
		Early:     *early,
		Branch:    *branch,
		Intent:    *intent,
		IntentDoc: *doc,
		PRMode:    *prNum != 0,
		Triage:    triageFor(*yes, stdin, stdout),
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	return printGateReport(stdout, st, ticket, report)
}

// printGateReport prints a completed gate round's report. Gate failures
// (oracle failures, env pauses, frontier-not-empty, PR mode) surface as
// errors from verifydeliver.Gate and are handled by the caller via
// renderErr before this is reached; this function's own return value is
// the "needs a human" exit code: 2 when the round leaves any ask
// undecided, matching frontier's own PendingQuestion convention
// (printRunReport), else 0.
func printGateReport(stdout io.Writer, st *store.Store, ticket string, report verifydeliver.GateReport) int {
	var shaRows [][]string
	for repo, sha := range report.TargetSHA {
		shaRows = append(shaRows, []string{repo, sha})
	}
	intentRow := report.Intent.Source
	if report.IntentNote != "" {
		intentRow = fmt.Sprintf("%s (%s)", report.Intent.Source, report.IntentNote)
	}
	kv := [][2]string{
		{"ticket", ticket},
		{"round", strconv.Itoa(report.Round)},
		{"verdict", report.Verdict},
		{"model", report.Model},
		{"intent", intentRow},
	}
	if report.Scope != "" {
		kv = append(kv, [2]string{"scope", report.Scope})
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

// gateReportHint names the way forward after this round: when it leaves
// any ask undecided, that is always a human decision at a terminal.
//
// The next `jig gate` is refused while the frontier is short of green, so
// the hint reads the frontier itself (frontierGreen, the condition the
// check applies) rather than a proxy for it: this round's own fix slices
// are one way to be short of green and not the only one, and `jig gate
// --early` runs on an unfinished frontier and can leave an ask with no fix
// slice queued at all, which a fix-slice count reads as green and the
// check does not.
//
// Short of green, the first line is the ticket's own next step, exactly
// what `jig status` would print (hintOrFallback), because that is what
// actually moves the frontier: one parked on an unanswered question does
// not advance on a bare `jig run <ticket>`, only on `jig run <ticket>
// --answer ...`. The gate follows it as the second step. With nothing
// undecided the hint is that same next-step line alone.
func gateReportHint(st *store.Store, ticket string, report verifydeliver.GateReport) []string {
	if len(report.NeedsHuman) > 0 {
		if !frontierGreen(st, ticket) {
			// What actually moves the frontier is the ticket's own
			// next step, the same one `jig status` prints: a frontier
			// parked on an unanswered question does not advance on
			// `jig run <ticket>` at all, it advances on
			// `jig run <ticket> --answer ...`.
			return []string{
				hintOrFallback(st, ticket),
				fmt.Sprintf("Then run `jig gate %s` at a terminal to decide the listed asks", ticket),
			}
		}
		return []string{fmt.Sprintf("Run `jig gate %s` at a terminal to decide the listed asks", ticket)}
	}
	return []string{hintOrFallback(st, ticket)}
}
