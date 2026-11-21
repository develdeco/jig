package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// chartSharedProblems is every problem of an entry that is a shared task: its
// id is an entry of another model chart too. Only a task that waits on nothing
// and sits directly under each issue can be shared, so each of these is listed
// (the first one `jig graduate` refuses on), for the first other chart that
// lists it:
//   - it waits on a ref, in this chart, in the other, or on the ticket;
//   - it is a multi-task, here, there or on the ticket;
//   - it is under a multi-task, here, there or by the ticket's parent.
//
// A chart with no issue is a legacy chart: it shares nothing.
func chartSharedProblems(st *store.Store, chart string, entries []store.ChartEntry) []chartProblem {
	if issue, _ := st.ChartIssue(chart); issue == "" {
		return nil
	}
	var out []chartProblem
	for i, e := range entries {
		if e.ID == "" {
			continue
		}
		listings, err := st.ChartsListing(e.ID)
		if err != nil {
			continue
		}
		var other *store.Listing
		for k := range listings {
			if listings[k].Chart != chart {
				other = &listings[k]
				break
			}
		}
		if other == nil {
			continue
		}
		rec, _ := st.ReadTicket(e.ID)
		add := func(reason string) {
			out = append(out, chartProblem{
				Entry:  i + 1,
				Reason: reason,
				Err: &axi.Error{
					Msg:  fmt.Sprintf("entry %d in chart %q %s", i+1, chart, reason),
					Code: "VALIDATION_ERROR",
					Help: []string{fmt.Sprintf("Fix entry %d in charts/%s/tickets.yaml", i+1, chart)},
				},
				Line: fmt.Sprintf("%s: %s", entryName(i, e.Title), reason),
			})
		}
		also := fmt.Sprintf("is also an entry of chart %q", other.Chart)
		wait := ""
		switch {
		case len(e.BlockedBy) > 0:
			wait = e.BlockedBy[0].Ref
		case len(other.Entry.BlockedBy) > 0:
			wait = other.Entry.BlockedBy[0].Ref
		case len(rec.BlockedBy) > 0:
			wait = rec.BlockedBy[0].Ticket
		}
		if wait != "" {
			add(fmt.Sprintf("%s (issue %s) but waits on %s; a shared task starts from the target", also, other.Issue, wait))
		}
		if e.Kind == store.KindMultiTask || other.Entry.Kind == store.KindMultiTask || rec.KindOrDefault() == store.KindMultiTask {
			add(also + " but is a multi-task; only a task is shared")
		}
		under := ""
		switch {
		case underIndex(entries, e.Under) >= 0:
			under = fmt.Sprintf("entry %d", underIndex(entries, e.Under)+1)
		case e.Under != "":
			under = e.Under
		case other.Entry.Under != "":
			under = other.Entry.Under
			if _, pos, ok := parseRef(under); ok {
				under = fmt.Sprintf("entry %d", pos)
			}
		case rec.Parent != "":
			under = rec.Parent
			if j := underIndex(entries, rec.Parent); j >= 0 {
				under = fmt.Sprintf("entry %d", j+1)
			}
		}
		if under != "" {
			add(fmt.Sprintf("%s and is under %s; a shared task sits directly under each issue", also, under))
		}
	}
	return out
}
