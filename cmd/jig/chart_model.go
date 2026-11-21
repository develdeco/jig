package main

import (
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// layModel lays on the facts of a chart that plans an issue what its run needs
// to stop at each landing (ADR 0064): the issue, the tickets that must land
// before each entry and the branch it lands into, where each multi-task stands
// before its own pull request, and where the issue itself stands. A chart whose
// issue cannot be read stays as a legacy chart is read: it is never hidden.
func layModel(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, issue string, facts *phase.ChartFacts) {
	rec, err := st.ReadTicket(issue)
	if err != nil || rec.KindOrDefault() != store.KindIssue || merges == nil {
		return
	}
	facts.Issue = issue
	d := land.Deps{Store: st, Cfg: cfg, Clones: mp.Clones}
	if lm, ok := merges.(land.Merges); ok {
		d.Merges = lm
	} else {
		d.Merges = ofForOf{merges}
	}
	for i := range facts.Entries {
		e := &facts.Entries[i]
		if e.ID == "" || e.Ticket == nil && e.Kind == "" {
			continue
		}
		erec, err := st.ReadTicket(e.ID)
		if err != nil {
			continue
		}
		containers, err := st.ContainersOf(e.ID, erec)
		if err != nil {
			continue
		}
		// A task two issues share is read for this chart's own issue.
		shared := ""
		if len(containers) > 1 {
			shared = issue
		}
		if after, err := land.LandFirst(d, e.ID, shared); err == nil {
			e.After = after
		}
		if into := containerOf(containers, issue, shared); into != "" {
			if crec, err := st.ReadTicket(into); err == nil {
				if branch, err := ticketBranch(st, cfg, into, crec); err == nil {
					e.Into = branch
				}
			}
		}
		if e.Kind == store.KindMultiTask {
			if r, err := land.ReadinessOf(d, e.ID); err == nil {
				e.Container = &phase.ChartContainer{State: r.State, Prompt: r.Prompt}
			}
		}
	}
	if r, err := land.ReadinessOf(d, issue); err == nil {
		tail := &phase.ChartTail{State: r.State, Prompt: r.Prompt}
		for _, w := range r.Waits {
			tail.Waits = append(tail.Waits, w.Ticket)
		}
		for _, u := range r.Unmet {
			tail.Unmet = append(tail.Unmet, phase.ChartUnmet{Code: u.Code, Why: u.Why, Prompt: u.Prompt})
		}
		facts.Tail = tail
	}
}

// chartOfIssueWord is the chart that plans the issue a word names (an id or an
// alias), "" when the word is no issue or no chart plans it.
func chartOfIssueWord(st *store.Store, word string) string {
	if word == "" {
		return ""
	}
	id, err := st.ResolveTicket(word)
	if err != nil || !st.TicketExists(id) {
		return ""
	}
	rec, err := st.ReadTicket(id)
	if err != nil || rec.KindOrDefault() != store.KindIssue {
		return ""
	}
	chart, _ := st.ChartOfIssue(id)
	return chart
}

// containerOf is the container an entry lands into: the issue for a shared
// task, else its one container.
func containerOf(containers []string, issue, shared string) string {
	switch {
	case shared != "":
		return shared
	case len(containers) > 0:
		return containers[0]
	}
	return issue
}
