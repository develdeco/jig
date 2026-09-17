package main

import (
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// noopGateSource is the placeholder GateSource used when jig gate runs
// without --scenario (the real, session-dispatching gate reviewer).
//
// NOTE: v0.1 only specifies the fake scenario-backed source
// (verifydeliver.NewFakeGateSource); a real, session-driven gate reviewer
// is out of scope for the tested v0.1 surface ("never invoked by tests",
// same as the herdr session backend). This always reports a clean round so
// `jig gate` without --scenario still completes rather than hanging on an
// unimplemented dependency; wiring a real reviewer is future work.
type noopGateSource struct{}

func (noopGateSource) Round(n int) (verifydeliver.Round, bool, error) {
	return verifydeliver.Round{}, false, nil
}

// gateSourceFor picks the GateSource for a gate invocation: the fake
// scenario reader when --scenario is set, else the noop placeholder.
func gateSourceFor(scenario string) verifydeliver.GateSource {
	if scenario != "" {
		return verifydeliver.NewFakeGateSource(scenario)
	}
	return noopGateSource{}
}

// cmdGate implements `jig gate <ticket> [--early] [--branch <name> [--doc
// <path>]] [--pr <n>] [--scenario <dir>]`.
func cmdGate(args []string, stdout io.Writer) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("gate")
	early := fs.Bool("early", false, "gate before the frontier is fully green")
	branch := fs.String("branch", "", "validate this branch instead of jig/<ticket>")
	doc := fs.String("doc", "", "brief doc path, used together with --branch")
	prNum := fs.Int("pr", 0, "pr number (not implemented in v0.1)")
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

	deps := verifydeliverDeps(st, cfg, mp)
	report, err := verifydeliver.Gate(deps, gateSourceFor(*scenario), verifydeliver.GateOpts{
		Ticket:   ticket,
		Early:    *early,
		Branch:   *branch,
		BriefDoc: *doc,
		PRMode:   *prNum != 0,
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	return printGateReport(stdout, st, ticket, report)
}

// printGateReport prints a completed gate round's report and returns 0:
// gate failures (oracle failures, env pauses, frontier-not-empty, PR mode)
// surface as errors from verifydeliver.Gate and are handled by the caller
// via renderErr before this is reached.
func printGateReport(stdout io.Writer, st *store.Store, ticket string, report verifydeliver.GateReport) int {
	var shaRows [][]string
	for repo, sha := range report.TargetSHA {
		shaRows = append(shaRows, []string{repo, sha})
	}
	axi.Render(stdout,
		axi.KV("gate", [][2]string{
			{"ticket", ticket},
			{"round", strconv.Itoa(report.Round)},
			{"verdict", report.Verdict},
			{"model", report.Model},
		}),
		axi.Table("target_sha", []string{"repo", "sha"}, shaRows),
		axi.Help(hintOrFallback(st, ticket)),
	)
	return 0
}
