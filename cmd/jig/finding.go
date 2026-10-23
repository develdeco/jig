package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The usage lines of `jig finding`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	findingFileUsage       = "the repo-relative file the problem is in"
	findingLineUsage       = "the line it is on (0 if unknown); with --still-present, the line the earlier finding is on now"
	findingTitleUsage      = "what is wrong, in one line"
	findingDetailUsage     = "the detail: why it is wrong and what would fix it"
	findingActionUsage     = "who acts: fix (objective, does not change what the intent asks for), ask (only a person can decide) or note (nothing to change, a human reviewer should know)"
	findingRiskUsage       = "how much harm follows if this part of the change is wrong: low, medium or high"
	findingRationaleUsage  = "why that risk"
	findingOracleUsage     = "the manifest oracle that best proves the fix (required with more than one oracle, for a fix or an ask)"
	findingPriorUsage      = "the id of the earlier finding (open or dismissed in the request) this one is a changed version of"
	findingStillPresentUse = "instead of a new finding: the id of an earlier finding (open or dismissed in the request) that is still present and unchanged, with --line its current line"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"finding", "report one problem found in the open gate round, or confirm an earlier one is still present", []flagSpec{
			{"file", findingFileUsage, false},
			{"line", findingLineUsage, false},
			{"title", findingTitleUsage, false},
			{"detail", findingDetailUsage, false},
			{"action", findingActionUsage, false},
			{"risk", findingRiskUsage, false},
			{"risk-rationale", findingRationaleUsage, false},
			{"oracle", findingOracleUsage, false},
			{"prior", findingPriorUsage, false},
			{"still-present", findingStillPresentUse, false},
			{"store", reviewStoreUsage, false},
			{"project", reviewProjectUsage, false},
		}, false},
		Order: 331,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdFinding(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// cmdFinding implements `jig finding [T] --file F --line N --title T --action
// A --risk R --risk-rationale R [--detail D] [--oracle O] [--prior ID]` and `jig
// finding [T] --still-present ID --line N`: one draft of the open round's
// report, held to the rules a finding of a result file is held to as it is
// taken, and appended to the round's drafts. `jig review done` assembles them.
func cmdFinding(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "finding")
	file := fs.String("file", "", findingFileUsage)
	line := fs.Int("line", 0, findingLineUsage)
	title := fs.String("title", "", findingTitleUsage)
	detail := fs.String("detail", "", findingDetailUsage)
	action := fs.String("action", "", findingActionUsage)
	risk := fs.String("risk", "", findingRiskUsage)
	rationale := fs.String("risk-rationale", "", findingRationaleUsage)
	oracle := fs.String("oracle", "", findingOracleUsage)
	prior := fs.String("prior", "", findingPriorUsage)
	stillPresent := fs.String("still-present", "", findingStillPresentUse)
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
	if *stillPresent != "" {
		for _, name := range []string{"file", "title", "detail", "action", "risk", "risk-rationale", "oracle", "prior"} {
			if set[name] {
				return renderErr(stdout, &axi.Error{
					Msg:  fmt.Sprintf("--still-present confirms an earlier finding and takes only --line, not --%s", name),
					Code: "VALIDATION_ERROR",
				})
			}
		}
	}
	if *storeFlag, err = e.abs(*storeFlag); err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := reviewTicket(e, st, jigHome, positionals, "finding", stdout)
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

	kind := "finding"
	var dr verifydeliver.Draft
	if *stillPresent != "" {
		kind = "still_present"
		dr.StillPresent = &verifydeliver.StillPresentEntry{Prior: *stillPresent, Line: *line}
	} else {
		dr.Finding = &verifydeliver.ResultFinding{
			File: *file, Line: *line, Title: *title, Detail: *detail, Action: *action,
			Risk: *risk, RiskRationale: *rationale, Oracle: *oracle, Prior: *prior,
		}
	}
	n, err := verifydeliver.AppendDraft(deps, p, dr)
	if err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("finding", [][2]string{
			{"ticket", ticket}, {"round", strconv.Itoa(p.Round)}, {"kind", kind}, {"drafts", strconv.Itoa(n)},
		}),
		axi.Help(fmt.Sprintf("Report the next, or close the round: `jig review done %s --reviewed <every file you read> --summary <text>`", ticket)),
	)
	return 0
}
