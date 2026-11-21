package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// nextAfterRow is one issue another is suggested to land after, with its state
// (land.AfterLanded, land.AfterNotLanded or land.AfterClosed).
type nextAfterRow struct {
	Ticket string `json:"ticket"`
	Title  string `json:"title,omitempty"`
	State  string `json:"state"`
}

// nextIssueOrder is an issue with a predecessor not landed: `<issue> after
// <predecessor>`.
type nextIssueOrder struct {
	Issue string `json:"issue"`
	After string `json:"after"`
}

// afterDeps is what land.Suggestions reads: the store and, when the merge
// source can say a ticket's repos, those facts. It asks the host nothing.
func afterDeps(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource) land.Deps {
	d := land.Deps{Store: st, Cfg: cfg, Clones: mp.Clones}
	if m, ok := merges.(land.Merges); ok {
		d.Merges = m
	}
	return d
}

// afterRows are the rows land.Suggestions gives for issue, in the shape
// `jig next` shows them.
func afterRows(d land.Deps, issue string) ([]nextAfterRow, error) {
	rows, err := land.Suggestions(d, issue)
	if err != nil {
		return nil, err
	}
	var out []nextAfterRow
	for _, r := range rows {
		out = append(out, nextAfterRow{Ticket: r.Ticket, Title: r.Title, State: r.State})
	}
	return out, nil
}

// afterBlock is the lands_after table of an issue: nothing when it has none.
func afterBlock(rows []nextAfterRow) []string {
	if len(rows) == 0 {
		return nil
	}
	var table [][]string
	for _, r := range rows {
		table = append(table, []string{r.Ticket, r.Title, r.State})
	}
	return []string{axi.Table("lands_after", []string{"ticket", "title", "state"}, table)}
}

// afterHelp names the suggested order when a predecessor is not landed. It is
// advice: nothing here blocks the issue.
func afterHelp(issue string, rows []nextAfterRow) []string {
	for _, r := range rows {
		if r.State == land.AfterNotLanded {
			return []string{fmt.Sprintf("Suggested order: land %s before %s; it is a suggestion, nothing waits", r.Ticket, issue)}
		}
	}
	return nil
}

// issueOrder is the issues of the store that have a predecessor not landed, one
// row for each, in the order of the ids. A closed issue is no row.
func issueOrder(d land.Deps) []nextIssueOrder {
	ids, err := d.Store.Tickets()
	if err != nil {
		return nil
	}
	var out []nextIssueOrder
	for _, id := range ids {
		rec, err := d.Store.ReadTicket(id)
		if err != nil || rec.Closed != nil || rec.KindOrDefault() != store.KindIssue || len(rec.After) == 0 {
			continue
		}
		rows, err := land.Suggestions(d, id)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if r.Pending() {
				out = append(out, nextIssueOrder{Issue: id, After: r.Ticket})
			}
		}
	}
	return out
}

// issueOrderBlock is the issue_order table of `jig next`, nothing when no issue
// names another.
func issueOrderBlock(rows []nextIssueOrder) []string {
	if len(rows) == 0 {
		return nil
	}
	var table [][]string
	for _, r := range rows {
		table = append(table, []string{r.Issue, r.After})
	}
	return []string{axi.Table("issue_order", []string{"issue", "after"}, table)}
}
