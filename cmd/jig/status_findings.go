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
var findingColumns = []string{"id", "round", "action", "risk", "file:line", "title", "status", "rationale", "decision", "decided_by", "detail", "triage"}

// findingsBlocks is the `findings` table of `jig status`: what every recorded
// round raised, oldest round first and a round's high risk first, so a round
// a later one superseded still lists its own. A round's file also holds the
// earlier asks whose decision it recorded; those are not its findings and are
// not listed (outstanding_asks lists the asks that are still waiting,
// whichever round raised them).
//
//	findings[3]{id,round,action,risk,file:line,title,status,rationale,decision,decided_by,detail,triage}:
//	  r1-f1,1,fix,high,"a/a.go:3",Unchecked error,open,a user-visible failure,,,"The error is dropped.\nCheck it.",auto
//	  r1-f2,1,ask,medium,"b/b.go:9",Which greeting,asked,a product call,dismiss,person,Pick one.,human
//	  r2-f1,2,fix,low,"b/b.go:9",Name shadows import,open,a readability fix,,,Rename it.,auto
//
// action is the reviewer's own label (fix, ask or note) and status jig's
// (open: a fix slice was queued; asked; noted; dismissed), both as the
// finding's own round recorded them: what became of it since is its fix
// slice's state, which the slices table's findings column names. decision and
// decided_by are the pending `jig triage` decision for the finding, the same
// words outstanding_asks gives, and are empty when nobody has decided it or the
// decision was taken by a round already closed. detail is the finding's detail
// whole (the table quotes its newlines) and triage says who sorted the
// finding, human or auto, and is "-" when nobody has. A ticket whose rounds
// raised nothing has no block.
func findingsBlocks(st *store.Store, ticket string) ([]string, error) {
	recs, err := verifydeliver.ReadRounds(st, ticket)
	if err != nil {
		return nil, err
	}
	var raised int
	for _, rec := range recs {
		raised += len(rec.Findings)
	}
	if raised == 0 {
		return nil, nil
	}
	decisions, err := verifydeliver.ReadTriageDecisions(st, ticket)
	if err != nil {
		return nil, err
	}
	rows := make([][]string, 0, raised)
	for _, rec := range recs {
		for _, f := range rec.Findings {
			d := decisions[f.ID]
			decision, decidedBy := d.Label(f), ""
			if decision != "" {
				decidedBy = d.Provenance
			}
			triage := f.Triage
			if triage == "" {
				triage = "-"
			}
			rows = append(rows, []string{
				f.ID, strconv.Itoa(rec.Round), f.Action, f.Risk, fileLine(f), f.Title, f.Status, f.RiskRationale, decision, decidedBy, f.Detail, triage,
			})
		}
	}
	return []string{axi.Table("findings", findingColumns, rows)}, nil
}
