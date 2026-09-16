package main

import (
	"io"

	"github.com/develdeco/jig/axi"
	makepkg "github.com/develdeco/jig/make"
	"github.com/develdeco/jig/session"
	"github.com/develdeco/jig/verifydeliver"
)

// maxSolveRounds caps the run/gate fix-slice loop `jig solve` drives, per
// the contract's "cap 5 rounds".
const maxSolveRounds = 5

// cmdSolve implements `jig solve <ticket> [--yes] [--answer <qid> <text>]`.
//
// NOTE: the contract's grammar line for solve lists only --yes and
// --answer, but solve's internal run steps need a session backend exactly
// like `jig run` does; --backend/--scenario are added here as the closest
// working superset (needed for the fake-backend e2e chain) rather than a
// redesign.
func cmdSolve(args []string, stdout io.Writer) int {
	ticket, rest0, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	qid, text, rest1, err := extractAnswer(rest0)
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("solve")
	yes := fs.Bool("yes", false, "skip the interactive publish confirm")
	backendFlag := fs.String("backend", "", "session backend: fake, headless, or herdr")
	scenario := fs.String("scenario", "", "scenario dir for the fake backend")
	storeFlag := fs.String("store", "", "explicit store path")
	if err := fs.Parse(rest1); err != nil {
		return renderErr(stdout, &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"})
	}

	st, cfg, mp, err := resolveStore(*storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}
	backend, err := session.New(backendName(*backendFlag, *scenario), session.Options{ScenarioDir: *scenario})
	if err != nil {
		return renderErr(stdout, err)
	}

	mdeps := makeDeps(st, cfg, mp, backend, ticket)
	vdeps := verifydeliverDeps(st, cfg, mp)
	src := gateSourceFor(*scenario)

	report, err := makepkg.Run(mdeps, makepkg.RunOpts{Ticket: ticket, AnswerQID: qid, AnswerText: text})
	if err != nil {
		return renderErr(stdout, err)
	}
	if code := reportExitCode(report); code != 0 {
		return printRunReport(stdout, st, ticket, report)
	}

	for round := 0; round < maxSolveRounds; round++ {
		gr, err := verifydeliver.Gate(vdeps, src, verifydeliver.GateOpts{Ticket: ticket})
		if err != nil {
			return renderErr(stdout, err)
		}
		if gr.Verdict == "clean" {
			break
		}

		report, err = makepkg.Run(mdeps, makepkg.RunOpts{Ticket: ticket})
		if err != nil {
			return renderErr(stdout, err)
		}
		if code := reportExitCode(report); code != 0 {
			return printRunReport(stdout, st, ticket, report)
		}
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
// so cmdSolve can decide whether to stop the chain before rendering.
func reportExitCode(report makepkg.RunReport) int {
	switch {
	case report.PendingQuestion != "":
		return 2
	case report.Stopped:
		return 1
	default:
		return 0
	}
}
