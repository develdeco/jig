package main

import (
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/frontier"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// cmdRun implements `jig run <ticket> [--answer <qid> <text>] [--backend
// fake|headless|herdr] [--scenario <dir>]`.
func cmdRun(args []string, stdout io.Writer) int {
	ticket, rest0, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	qid, text, rest1, err := extractAnswer(rest0)
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("run")
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
	backend, err := session.New(backendName(*backendFlag, *scenario), session.Options{ScenarioDir: *scenario})
	if err != nil {
		return renderErr(stdout, err)
	}

	deps := frontierDeps(st, cfg, mp, backend, ticket)
	report, err := frontier.Run(deps, frontier.RunOpts{Ticket: ticket, AnswerQID: qid, AnswerText: text})
	if err != nil {
		return renderErr(stdout, err)
	}
	return printRunReport(stdout, st, ticket, report)
}

// cmdRequeue implements `jig requeue <ticket> --from-brief-diff`.
func cmdRequeue(args []string, stdout io.Writer) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("requeue")
	fromBriefDiff := fs.Bool("from-brief-diff", false, "requeue slices whose brief section hash changed")
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

	deps := frontierDeps(st, cfg, mp, nil, ticket)
	touched, err := frontier.Requeue(deps, ticket, *fromBriefDiff)
	if err != nil {
		return renderErr(stdout, err)
	}

	axi.Render(stdout,
		axi.Table("requeued", []string{"id"}, idRows(touched)),
		axi.Help(hintOrFallback(st, ticket)),
	)
	return 0
}

// printRunReport prints the outcome of a frontier.Run call and returns the
// exit code its report implies: 2 on a pending question, 1 when the run
// stopped (stall or attempt-cap), else 0.
func printRunReport(stdout io.Writer, st *store.Store, ticket string, report frontier.RunReport) int {
	blocks := []string{
		axi.KV("run", [][2]string{{"ticket", ticket}}),
		axi.Table("green", []string{"id"}, idRows(report.Green)),
		axi.Table("stalled", []string{"id"}, idRows(report.Stalled)),
		axi.Table("needs_input", []string{"id"}, idRows(report.NeedsInput)),
		axi.Table("env_blocked", []string{"id"}, idRows(report.EnvBlocked)),
	}
	if report.StopReason != "" {
		blocks = append(blocks, axi.KV("stopped", [][2]string{{"reason", report.StopReason}}))
	}
	blocks = append(blocks, axi.Help(hintOrFallback(st, ticket)))
	axi.Render(stdout, blocks...)

	switch {
	case report.PendingQuestion != "":
		return 2
	case report.Stopped, len(report.Stalled) > 0, len(report.EnvBlocked) > 0:
		return 1
	default:
		return 0
	}
}
