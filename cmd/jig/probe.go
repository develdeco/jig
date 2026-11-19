package main

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The usage lines of `jig probe`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	probeSeamUsage     = "the critical path the probed term is on, in the intent's words"
	probeFileUsage     = "the repo-relative file the term is in, as <repo>/<path> in a project of several repos"
	probeLineUsage     = "the line the term is on"
	probeMutationUsage = "the edit made to the term, in words"
	probeCommandUsage  = "the test command run against the mutant: the narrowest that covers the term"
	probeOutcomeUsage  = "caught (the command failed or timed out) or survived (it passed)"
	probeSkipUsage     = "instead of a probe: a file, or F:A-B for its lines A to B, with no term on a critical path"
	probeWhyUsage      = "with --skip, why no term there is on a critical path: a test, a doc, wiring"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"probe", "record a probe of a critical path's term in the open gate round, or set aside code on no critical path", []flagSpec{
			{"seam", probeSeamUsage, false},
			{"file", probeFileUsage, false},
			{"line", probeLineUsage, false},
			{"mutation", probeMutationUsage, false},
			{"command", probeCommandUsage, false},
			{"outcome", probeOutcomeUsage, false},
			{"skip", probeSkipUsage, false},
			{"why", probeWhyUsage, false},
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

// skipRangeRe is --skip's F:A-B form.
var skipRangeRe = regexp.MustCompile(`^(.+):(\d+)-(\d+)$`)

// parseSkip reads --skip: a file alone (the whole file, lines 0 to 0), or
// F:A-B.
func parseSkip(v string) verifydeliver.ResultSkip {
	if m := skipRangeRe.FindStringSubmatch(v); m != nil {
		from, _ := strconv.Atoi(m[2])
		to, _ := strconv.Atoi(m[3])
		return verifydeliver.ResultSkip{File: m[1], From: from, To: to}
	}
	return verifydeliver.ResultSkip{File: v}
}

// cmdProbe implements `jig probe [T] --seam S --file F --line N --mutation M
// --command C --outcome caught|survived` and `jig probe [T] --skip F[:A-B]
// --why W` (ADR 0036): one draft of the open round's report, held to the
// rules a probe or a skip of a result file is held to as it is taken, and
// appended to the round's drafts beside its findings. It says how many hunks
// of the change are still unprobed.
func cmdProbe(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "probe")
	seam := fs.String("seam", "", probeSeamUsage)
	file := fs.String("file", "", probeFileUsage)
	line := fs.Int("line", 0, probeLineUsage)
	mutation := fs.String("mutation", "", probeMutationUsage)
	command := fs.String("command", "", probeCommandUsage)
	outcome := fs.String("outcome", "", probeOutcomeUsage)
	skip := fs.String("skip", "", probeSkipUsage)
	why := fs.String("why", "", probeWhyUsage)
	storeFlag := fs.String("store", "", reviewStoreUsage)
	projectFlag := fs.String("project", "", reviewProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *skip != "" {
		for _, name := range []string{"seam", "file", "line", "mutation", "command", "outcome"} {
			if set[name] {
				return renderErr(stdout, &axi.Error{
					Msg:  fmt.Sprintf("--skip sets code aside and takes only --why, not --%s", name),
					Code: "VALIDATION_ERROR",
				})
			}
		}
	} else if set["why"] {
		return renderErr(stdout, &axi.Error{Msg: "--why goes with --skip", Code: "VALIDATION_ERROR"})
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

	kind := "probe"
	var dr verifydeliver.Draft
	if *skip != "" {
		kind = "skip"
		s := parseSkip(*skip)
		s.Why = *why
		dr.Skip = &s
	} else {
		dr.Probe = &verifydeliver.ResultProbe{
			Seam: *seam, File: *file, Line: *line, Mutation: *mutation, Command: *command, Outcome: *outcome,
		}
	}
	n, err := verifydeliver.AppendDraft(deps, p, dr)
	if err != nil {
		return renderErr(stdout, err)
	}
	drafts, err := verifydeliver.ReadDrafts(p)
	if err != nil {
		return renderErr(stdout, err)
	}
	left := verifydeliver.UnprobedAfter(p, drafts)
	axi.Render(stdout,
		axi.KV("probe", [][2]string{
			{"ticket", ticket}, {"round", strconv.Itoa(p.Round)}, {"kind", kind}, {"drafts", strconv.Itoa(n)},
			{"unprobed", strconv.Itoa(len(left))},
		}),
		axi.Help(fmt.Sprintf("Probe the next term or skip the next hunk, or close the round: `jig review done %s --reviewed <every file you read> --summary <text>`", ticket)),
	)
	return 0
}
