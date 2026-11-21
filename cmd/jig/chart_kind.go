package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// chartKindProblems is every problem of a chart's issue and of the kinds its
// entries mint: the checks of the model (an issue, tasks and multi-tasks).
// A legacy chart, with no issue and no entry kind or under, has none. They run
// after the entry checks and before the ref checks, so `jig graduate` refuses
// the first of them the same way as any other.
func chartKindProblems(st *store.Store, chart string, entries []store.ChartEntry) []chartProblem {
	var out []chartProblem
	issueProblem := func(reason string) {
		out = append(out, chartProblem{
			Reason: reason,
			Err: &axi.Error{
				Msg:  fmt.Sprintf("chart %q: %s", chart, reason),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Fix issue in charts/%s/tickets.yaml", chart)},
			},
			Line: reason,
		})
	}

	issue, _ := st.ChartIssue(chart)
	if issue != "" {
		if err := requireTicketID(st, issue); err != nil {
			issueProblem(fmt.Sprintf("issue %q is not a ticket of the store", issue))
		} else if rec, rerr := st.ReadTicket(issue); rerr == nil && rec.KindOrDefault() != store.KindIssue {
			issueProblem(fmt.Sprintf("issue %q is a %s, not an issue", issue, rec.KindOrDefault()))
		}
		if other := otherChartOfIssue(st, chart, issue); other != "" {
			issueProblem(fmt.Sprintf("issue %q is also the issue of chart %q", issue, other))
		}
	}

	entryProblem := func(i int, reason string) {
		out = append(out, chartProblem{
			Entry:  i + 1,
			Reason: reason,
			Err: &axi.Error{
				Msg:  fmt.Sprintf("entry %d in chart %q %s", i+1, chart, reason),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Fix entry %d in charts/%s/tickets.yaml", i+1, chart)},
			},
			Line: fmt.Sprintf("%s: %s", entryName(i, entries[i].Title), reason),
		})
	}

	// holds[j] is whether a task entry sits under entry j.
	holds := map[int]bool{}
	for i, e := range entries {
		if issue != "" && e.ID != "" {
			if rec, err := st.ReadTicket(e.ID); err == nil && rec.Kind != "" {
				if k := entryKind(e); k != rec.Kind {
					entryProblem(i, fmt.Sprintf("has kind %q but its ticket %s is a %s", k, e.ID, rec.Kind))
					continue
				}
			}
		}
		if e.Kind == "" && e.Under == "" {
			continue
		}
		if e.Kind != "" && e.Kind != store.KindTask && e.Kind != store.KindMultiTask {
			entryProblem(i, fmt.Sprintf("has kind %q; must be \"task\" or \"multi-task\"", e.Kind))
			continue
		}
		if issue == "" {
			entryProblem(i, "kind or under needs the chart's issue")
			continue
		}
		if e.Under == "" {
			continue
		}
		j := underIndex(entries, e.Under)
		if j < 0 {
			entryProblem(i, fmt.Sprintf("is under %q, which is not an entry of this chart", e.Under))
			continue
		}
		holds[j] = true
		switch {
		case e.Kind == store.KindMultiTask:
			entryProblem(i, fmt.Sprintf("is a multi-task under entry %d; a multi-task holds tasks only", j+1))
		case entries[j].Kind != store.KindMultiTask:
			entryProblem(i, fmt.Sprintf("is under entry %d, which is not a multi-task", j+1))
		case e.ID == "" && entries[j].ID == "" && j >= i:
			entryProblem(i, fmt.Sprintf("is under entry %d, which must come before it: the multi-task is minted first", j+1))
		}
	}
	for j, e := range entries {
		if issue != "" && e.Kind == store.KindMultiTask && !holds[j] {
			entryProblem(j, "is a multi-task with no task under it")
		}
	}
	return out
}

// entryKind is the kind an entry mints: a task unless it says otherwise.
func entryKind(e store.ChartEntry) string {
	if e.Kind == "" {
		return store.KindTask
	}
	return e.Kind
}

// recordExistingKinds gives each entry that already had a ticket (not one this
// run minted) the kind and parent its entry names, when its ticket records
// none: an existing ticket keeps the kind it has. A chart with no issue is a
// legacy chart and changes nothing. The writes land as one commit.
func recordExistingKinds(st *store.Store, chart string, entries []store.ChartEntry, created map[int]bool) error {
	if issue, _ := st.ChartIssue(chart); issue == "" {
		return nil
	}
	type update struct{ id, kind, parent string }
	var todo []update
	for i, e := range entries {
		if e.ID == "" || created[i] {
			continue
		}
		rec, err := st.ReadTicket(e.ID)
		if err != nil {
			continue
		}
		u := update{id: e.ID}
		if rec.Kind == "" && e.Kind == store.KindMultiTask {
			u.kind = e.Kind
		}
		if j := underIndex(entries, e.Under); j >= 0 && rec.Parent == "" && entries[j].ID != "" {
			u.parent = entries[j].ID
		}
		if u.kind != "" || u.parent != "" {
			todo = append(todo, u)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	_, err := st.Claim(
		func() (string, []string, error) {
			var paths []string
			for _, u := range todo {
				if err := st.SetTicketKind(u.id, u.kind, u.parent); err != nil {
					return "", nil, err
				}
				paths = append(paths, st.TicketFileRelPath(u.id))
			}
			return chart, paths, nil
		},
		func(string) string { return fmt.Sprintf("chart %s: record kinds", chart) },
	)
	return err
}

// underIndex is the 0-based position of the entry an `under` names: "#k" is the
// 1-based position of an entry, anything else the ticket id of one. -1 when it
// names none.
func underIndex(entries []store.ChartEntry, under string) int {
	if name, pos, ok := parseRef(under); ok {
		if name != "" || pos < 1 || pos > len(entries) {
			return -1
		}
		return pos - 1
	}
	for i, e := range entries {
		if e.ID != "" && e.ID == under {
			return i
		}
	}
	return -1
}

// otherChartOfIssue is the name of a chart other than chart that plans issue,
// in chart-name order, or "" when none does.
func otherChartOfIssue(st *store.Store, chart, issue string) string {
	names, err := st.ChartNames()
	if err != nil {
		return ""
	}
	for _, name := range names {
		if name == chart {
			continue
		}
		if got, err := st.ChartIssue(name); err == nil && got == issue {
			return name
		}
	}
	return ""
}

// checkNewChartIssue refuses an issue `jig chart new --issue` cannot plan: a
// ticket the store lacks, one that is not an issue, or one another chart plans.
// It returns the issue's current id (an alias resolves to it).
func checkNewChartIssue(st *store.Store, issue string) (string, error) {
	id, err := st.ResolveTicket(issue)
	if err != nil {
		return "", err
	}
	if err := requireTicketID(st, id); err != nil {
		return "", err
	}
	rec, err := st.ReadTicket(id)
	if err != nil {
		return "", err
	}
	if k := rec.KindOrDefault(); k != store.KindIssue {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%s is a %s, not an issue", id, k),
			Code: "VALIDATION_ERROR",
			Help: []string{"Mint the issue first: jig ticket new --kind issue --title \"<the need>\""},
		}
	}
	if other := otherChartOfIssue(st, "", id); other != "" {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("issue %s already has chart %q: an issue has one chart", id, other),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Add the tickets to charts/%s/tickets.yaml", other)},
		}
	}
	return id, nil
}
