package main

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// suiteWaiverAllowed says whether `jig publish --without-suite <reason>` can be
// asked of ticket: an issue only, with a reason, and no other form. jig records
// the waiver and never judges who may say it.
func suiteWaiverAllowed(st *store.Store, ticket, reason string, otherForm bool) error {
	invalid := func(msg, help string) error {
		return &axi.Error{Msg: msg, Code: "VALIDATION_ERROR", Help: []string{help}}
	}
	switch rec, err := st.ReadTicket(ticket); {
	case err != nil:
		return err
	case rec.KindOrDefault() != store.KindIssue:
		return invalid(fmt.Sprintf("--without-suite is for an issue: %s is a %s, and only an issue runs a suite", ticket, rec.KindOrDefault()), "Publish it without the flag")
	case strings.TrimSpace(reason) == "":
		return invalid("--without-suite needs a reason, which jig records and the pull request shows", "Give it: --without-suite \"<why the suite is skipped>\"")
	case otherForm:
		return invalid("--without-suite goes with the bare `jig publish "+ticket+" --yes`, not with --body, --pr or --approve", "Drop the other form")
	}
	return nil
}

// waiveSuites writes one suite-waived line into the issue's journal for each
// repo whose suite is unmet at the tip of its branch, with the reason and who
// said it, then leaves publish to ask land.Check as it always does: a waiver at
// the tip meets the suite, and a head that moves later asks again. Nothing is
// written while a child has not landed (publish refuses for that) or for a repo
// with no copy of the branch (no tip to waive); a met suite needs no waiver.
func waiveSuites(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, ticket, reason, provenance string) error {
	r, err := containerReadiness(st, cfg, mp, merges, ticket)
	if err != nil {
		return err
	}
	if r.State != land.StateWaiting || len(r.Waits) > 0 {
		return nil
	}
	d := land.Deps{Store: st, Cfg: cfg, Clones: mp.Clones}
	c, err := land.ContainerOf(d, ticket)
	if err != nil {
		return err
	}
	suites, err := land.SuiteOf(d, c)
	if err != nil {
		return err
	}
	for _, s := range suites {
		if s.Unmet == nil || s.Tip == "" {
			continue
		}
		line := journal.Line{
			Event: journal.EventSuiteWaived, Repo: land.JournalRepo(d, s.Repo), Commit: s.Tip,
			Summary: strings.TrimSpace(reason), Provenance: provenance,
		}
		if err := journal.Append(st, ticket, line); err != nil {
			return fmt.Errorf("record the suite waiver of %s: %w", s.Repo, err)
		}
	}
	return nil
}
