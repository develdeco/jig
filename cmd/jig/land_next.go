package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/land"
)

// nextLanding is what the next landing is: a published ticket that waits for
// this one, else the container this one lands into once its tasks have all
// landed (land.Complete), else what the container still waits for, else that
// nothing waits. into is the container the landing was into: a dependent that
// is a task two issues share is read for it, and named with its --issue.
func nextLanding(d land.Deps, ticket, into string) string {
	st := d.Store
	all, err := st.Tickets()
	if err != nil {
		return "Nothing waits on a landing here"
	}
	for _, id := range all {
		rec, err := st.ReadTicket(id)
		if err != nil || rec.Closed != nil {
			continue
		}
		for _, b := range rec.BlockedBy {
			if resolved, err := st.ResolveTicket(b.Ticket); err != nil || resolved != ticket {
				continue
			}
			// A dependent is named when the land rules let it land now, this
			// landing counted: one that waits for another issue's merge of a
			// shared task, or for anything else, is not the next landing.
			// A task two issues share lands per issue, so it is judged for each
			// of its issues, in chart order, and named with the first that
			// can land.
			issues := []string{""}
			if cs, err := st.ContainersOf(id, rec); err == nil && len(cs) > 1 {
				issues = cs
			}
			for _, issue := range issues {
				if v, err := land.Judge(d, id, land.Options{Issue: issue}); err == nil && v.Refusal == nil && !v.Done {
					return fmt.Sprintf("The next landing is `%s`, which waited for it", land.Command(st, id, issue))
				}
			}
		}
	}
	if rec, err := st.ReadTicket(ticket); err == nil {
		if cs, err := st.ContainersOf(ticket, rec); err == nil && len(cs) > 0 && slices.Contains(cs, into) && published(st, into) {
			done, waiting, err := land.Complete(d, into)
			switch {
			case err != nil || done:
			case len(waiting) == 0:
				return fmt.Sprintf("Its tasks have all landed: the next landing is `%s`", land.Command(st, into, ""))
			default:
				return fmt.Sprintf("%s lands once %s have: the next landing is `%s`", into, strings.Join(waiting, ", "), land.Command(st, waiting[0], into))
			}
		}
	}
	return "Nothing waits on a landing here"
}

// issuesOfRows are the issues of a shared task's rows, each once, in order.
func issuesOfRows(rows []nextIssueRow) []string {
	var out []string
	for _, r := range rows {
		if !slices.Contains(out, r.Issue) {
			out = append(out, r.Issue)
		}
	}
	return out
}
