package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/frontier"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// maxSolveRounds caps the run/gate fix-slice loop `jig solve` drives at 5
// rounds before handing back to the human (see solveShouldPublish).
const maxSolveRounds = 5

// gateSourceForSolve picks the GateSource for `jig solve`'s own gate/fix-
// slice loop. The compatibility rule (design Q10): `jig solve` always had
// --backend, so unlike jig gate its scripted-source decision does not look
// at it - the old scripted source runs iff --scenario is set, whatever
// --backend says; otherwise the real reviewer runs on solve's own backend
// (the same one its frontier.Run steps use).
func gateSourceForSolve(scenario string, backend session.Backend) verifydeliver.GateSource {
	if scenario != "" {
		return verifydeliver.NewFakeGateSource(scenario)
	}
	return verifydeliver.NewReviewerGateSource(backend)
}

// cmdSolve implements `jig solve <ticket> [--yes] [--answer <qid> <text>]
// [--backend <name>] [--scenario <dir>]`.
//
// NOTE: --backend/--scenario are accepted here, beyond solve's own --yes
// and --answer, because solve's internal run steps need a session backend
// exactly like `jig run` does; they are the closest working superset
// (needed for the fake-backend e2e chain) rather than a redesign. --yes
// also drives the finding triage (design 6.4): it skips the interactive
// publish confirm and keeps every fix and workspace ask without prompting.
func cmdSolve(args []string, stdout io.Writer, stdin io.Reader) int {
	ticket, rest0, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	qid, text, rest1, err := extractAnswer(rest0)
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("solve")
	yes := fs.Bool("yes", false, "skip the interactive publish confirm and finding triage")
	backendFlag := fs.String("backend", "", "session backend: fake, headless, or herdr")
	scenario := fs.String("scenario", "", "scenario dir for the fake backend")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest1); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, err := resolveStoreForProject(*projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireSlices(st, ticket); err != nil {
		return renderErr(stdout, err)
	}
	backendKind := backendName(*backendFlag, *scenario)
	if err := session.Available(backendKind); err != nil {
		return renderErr(stdout, err)
	}
	backend, err := session.New(backendKind, session.Options{ScenarioDir: *scenario})
	if err != nil {
		return renderErr(stdout, err)
	}

	fdeps := frontierDeps(st, cfg, mp, backend, ticket)
	vdeps := verifydeliverDeps(st, cfg, mp)
	src := gateSourceForSolve(*scenario, backend)
	triage := triageFor(*yes, stdin, stdout)

	// Check identity before any session or gate round runs, not only at
	// publish.
	if err := verifydeliver.CheckIdentity(vdeps); err != nil {
		return renderErr(stdout, err)
	}

	report, err := frontier.Run(fdeps, frontier.RunOpts{Ticket: ticket, AnswerQID: qid, AnswerText: text})
	if err != nil {
		return renderErr(stdout, err)
	}
	if code := reportExitCode(report); code != 0 {
		return printRunReport(stdout, st, ticket, report)
	}

	var lastVerdict string
	for round := 0; round < maxSolveRounds; round++ {
		gr, err := verifydeliver.Gate(vdeps, src, verifydeliver.GateOpts{Ticket: ticket, Triage: triage})
		if err != nil {
			return renderErr(stdout, err)
		}
		lastVerdict = gr.Verdict
		if gr.Verdict == "clean" {
			break
		}

		report, err = frontier.Run(fdeps, frontier.RunOpts{Ticket: ticket})
		if err != nil {
			return renderErr(stdout, err)
		}
		if code := reportExitCode(report); code != 0 {
			return printRunReport(stdout, st, ticket, report)
		}
	}
	if err := solveShouldPublish(lastVerdict); err != nil {
		return renderErr(stdout, err)
	}

	pdeps := vdeps
	preport, err := verifydeliver.Publish(pdeps, verifydeliver.PublishOpts{Ticket: ticket, Yes: *yes})
	if err != nil {
		return renderErr(stdout, err)
	}

	var squashRows [][]string
	for repo, sha := range preport.Squashed {
		squashRows = append(squashRows, []string{repo, sha})
	}
	axi.Render(stdout,
		axi.KV("solve", [][2]string{{"ticket", ticket}, {"tier", preport.Tier}}),
		axi.Table("squashed", []string{"repo", "sha"}, squashRows),
		axi.Help("Run `jig status "+ticket+"` to confirm the ticket is fully green"),
	)
	return 0
}

// reportExitCode mirrors printRunReport's exit-code logic without printing,
// so cmdSolve can decide whether to stop the chain before rendering. A
// non-empty Stalled or EnvBlocked table means the ticket is not actually
// done even when this call's Stopped flag is false (e.g. a slice left
// stalled or env-blocked by an earlier invocation, not this one).
func reportExitCode(report frontier.RunReport) int {
	switch {
	case report.PendingQuestion != "":
		return 2
	case report.Stopped, len(report.Stalled) > 0, len(report.EnvBlocked) > 0:
		return 1
	default:
		return 0
	}
}

// solveShouldPublish gates the fall-through to Publish on the gate/fix-slice
// loop's last verdict: only "clean" permits it. Exhausting maxSolveRounds
// without ever reaching clean must hand back to the human, not ship
// unresolved must-fix work.
func solveShouldPublish(lastVerdict string) error {
	if lastVerdict == "clean" {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("gate still returning %q after %d rounds; not publishing", lastVerdict, maxSolveRounds),
		Code: "GATE_ROUNDS_EXHAUSTED",
	}
}
