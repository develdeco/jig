package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The usage lines of `jig probe`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	probeSeamUsage     = "the seam the probed term is on, in the intent's words"
	probeFileUsage     = "the repo-relative file the term is in, as <repo>/<path> in a project of several repos"
	probeLineUsage     = "the line the term is on"
	probeMutationUsage = "the edit made to the term, in words"
	probeCommandUsage  = "the test command run against the mutant: the narrowest that covers the term"
	probeOutcomeUsage  = "caught (the command failed or timed out) or survived (it passed)"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"probe", "optionally record, in the open gate round, a probe that proves a finding", []flagSpec{
			{"seam", probeSeamUsage, false},
			{"file", probeFileUsage, false},
			{"line", probeLineUsage, false},
			{"mutation", probeMutationUsage, false},
			{"command", probeCommandUsage, false},
			{"outcome", probeOutcomeUsage, false},
			{"store", reviewStoreUsage, false},
			{"project", reviewProjectUsage, false},
		}, false},
		Order: 331,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdProbe(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// cmdProbe implements `jig probe [T] --seam S --file F --line N --mutation M
// --command C --outcome caught|survived` (ADR 0036): one draft of the open
// round's report, held to the rules a probe of a result file is held to as it
// is taken, and appended to the round's drafts beside its findings.
func cmdProbe(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "probe")
	seam := fs.String("seam", "", probeSeamUsage)
	file := fs.String("file", "", probeFileUsage)
	line := fs.Int("line", 0, probeLineUsage)
	mutation := fs.String("mutation", "", probeMutationUsage)
	command := fs.String("command", "", probeCommandUsage)
	outcome := fs.String("outcome", "", probeOutcomeUsage)
	storeFlag := fs.String("store", "", reviewStoreUsage)
	projectFlag := fs.String("project", "", reviewProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	if *storeFlag, err = e.abs(*storeFlag); err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := reviewTicket(e, st, jigHome, positionals, "probe", stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	deps := verifydeliverDeps(e, st, cfg, mp, jigHome)
	p, open, err := verifydeliver.CurrentRound(deps, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	if !open {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("no review round is open for %s", ticket),
			Code: "REVIEW_NOT_OPEN",
			Help: []string{fmt.Sprintf("Open one with `jig gate %s` (or `jig review context %s`)", ticket, ticket)},
		})
	}

	n, err := verifydeliver.AppendDraft(deps, p, verifydeliver.Draft{Probe: &verifydeliver.ResultProbe{
		Seam: *seam, File: *file, Line: *line, Mutation: *mutation, Command: *command, Outcome: *outcome,
	}})
	if err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("probe", [][2]string{
			{"ticket", ticket}, {"round", strconv.Itoa(p.Round)}, {"drafts", strconv.Itoa(n)},
		}),
		axi.Help(fmt.Sprintf("File the finding it proves with `jig finding`, or close the round: `jig review done %s --reviewed <every file you read> --summary <text>`", ticket)),
	)
	return 0
}
