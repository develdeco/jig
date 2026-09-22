package main

import (
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// gateSourceFor picks the GateSource for a `jig gate` invocation. The
// compatibility rule (design Q10): `jig gate` never had --backend before,
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

// cmdGate implements `jig gate <ticket> [--early] [--branch <name> [--doc
// <path>]] [--pr <n>] [--yes] [--backend fake|headless|herdr] [--scenario
// <dir>]`.
func cmdGate(args []string, stdout io.Writer, stdin io.Reader) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("gate")
	early := fs.Bool("early", false, "gate before the frontier is fully green")
	branch := fs.String("branch", "", "validate this branch instead of jig/<ticket>")
	doc := fs.String("doc", "", "brief doc path, used together with --branch")
	prNum := fs.Int("pr", 0, "pr number (not implemented in v0.1)")
	yes := fs.Bool("yes", false, "keep every fix and workspace ask without the triage prompt")
	backendFlag := fs.String("backend", "", "session backend for the reviewer: fake, headless, or herdr")
	scenario := fs.String("scenario", "", "scenario dir for the fake gate source")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, err := resolveStoreForProject(*projectFlag, *storeFlag)
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

	deps := verifydeliverDeps(st, cfg, mp)
	report, err := verifydeliver.Gate(deps, src, verifydeliver.GateOpts{
		Ticket:   ticket,
		Early:    *early,
		Branch:   *branch,
		BriefDoc: *doc,
		PRMode:   *prNum != 0,
		Triage:   triageFor(*yes, stdin, stdout),
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
// the "needs a human" exit code (Q1): 2 when the round leaves any ask
// undecided, matching frontier's own PendingQuestion convention
// (printRunReport), else 0.
func printGateReport(stdout io.Writer, st *store.Store, ticket string, report verifydeliver.GateReport) int {
	var shaRows [][]string
	for repo, sha := range report.TargetSHA {
		shaRows = append(shaRows, []string{repo, sha})
	}
	kv := [][2]string{
		{"ticket", ticket},
		{"round", strconv.Itoa(report.Round)},
		{"verdict", report.Verdict},
		{"model", report.Model},
	}
	if report.Scope != "" {
		kv = append(kv, [2]string{"scope", report.Scope})
	}
	blocks := []string{
		axi.KV("gate", kv),
		axi.Table("target_sha", []string{"repo", "sha"}, shaRows),
	}
	if report.Scope != "" {
		var findingRows [][]string
		for _, f := range report.Findings {
			findingRows = append(findingRows, []string{f.ID, f.Status, f.RoutedAs, f.Title})
		}
		blocks = append(blocks, axi.Table("findings", []string{"id", "status", "routed_as", "title"}, findingRows))
		blocks = append(blocks, axi.Table("fix_slices", []string{"id"}, idRows(report.FixSlices)))
		if len(report.NeedsHuman) > 0 {
			var needsRows [][]string
			for _, f := range report.NeedsHuman {
				needsRows = append(needsRows, []string{f.ID, f.Risk, f.Title})
			}
			blocks = append(blocks, axi.Table("needs_a_human", []string{"id", "risk", "title"}, needsRows))
		}
	}
	blocks = append(blocks, axi.Help(hintOrFallback(st, ticket)))
	axi.Render(stdout, blocks...)
	if len(report.NeedsHuman) > 0 {
		return 2
	}
	return 0
}
