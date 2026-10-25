package main

import (
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 210, Name: "findings", Blocks: findingsBlocks})
}

// findingColumns are the columns of the findings table.
var findingColumns = []string{"id", "round", "action", "risk", "file:line", "title", "status", "rationale", "decision", "decided_by"}

// findingsBlocks is the `findings` table of `jig status`: what the latest
// recorded round raised, high risk first, and nothing of the rounds before it
// (outstanding_asks lists the asks that are still waiting, whichever round
// raised them). A round's file also holds the earlier asks whose decision it
// recorded; those are not its findings and are not listed.
//
//	findings[2]{id,round,action,risk,file:line,title,status,rationale,decision,decided_by}:
//	  r2-f1,2,ask,high,"a/a.go:3",Unchecked error,asked,a user-visible failure,dismiss,person
//	  r2-f2,2,fix,low,"b/b.go:9",Name shadows import,open,a readability fix,,
//
// action is the reviewer's own label (fix, ask or note) and status jig's
// (open: a fix slice was queued; asked; noted; dismissed). decision and
// decided_by are the pending `jig triage` decision for the finding, the same
// words outstanding_asks gives, and are empty when nobody has decided it or the
// decision was taken by a round already closed. A latest round that holds no
// finding has no block.
func findingsBlocks(st *store.Store, ticket string) ([]string, error) {
	recs, err := verifydeliver.ReadRounds(st, ticket)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 || len(recs[len(recs)-1].Findings) == 0 {
		return nil, nil
	}
	latest := recs[len(recs)-1]
	decisions, err := verifydeliver.ReadTriageDecisions(st, ticket)
	if err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(latest.Findings))
	for _, f := range latest.Findings {
		d := decisions[f.ID]
		decision, decidedBy := d.Label(f), ""
		if decision != "" {
			decidedBy = d.Provenance
		}
		rows = append(rows, []string{
			f.ID, strconv.Itoa(latest.Round), f.Action, f.Risk, fileLine(f), f.Title, f.Status, f.RiskRationale, decision, decidedBy,
		})
	}
	return []string{axi.Table("findings", findingColumns, rows)}, nil
}
