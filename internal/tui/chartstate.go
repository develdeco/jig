package tui

import (
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// What a chart's standing is (which ticket waits for which, what a run would
// do, whether it is ready) is the verb's word: internal/phase derives it and
// `jig next <chart>` prints it, so the chart screen only draws it. What the
// screens add is their own: where each of the chart's tickets stands among its
// four steps (the same rows the project's list draws), and which of them needs
// the person.

// The kinds of need a ticket can wait on.
const kindStacked = "stacked"

// stepUnderWay is the state a step is drawn in while it is under way, for a
// ticket the screens know only from the verb's word.
var stepUnderWay = [stepCount]string{StateBuilding, StateBuilding, StateReviewing, StatePublishing}

// ids are the chart's tickets as the screens know them: the entries of the
// verb that have a ticket once it has been read, in its order, then the ones
// the store listing names that it did not (graduated since it was read).
func (c *chart) ids() []string {
	var ids []string
	if c.info != nil {
		for _, t := range c.info.Tickets {
			if t.ID != "" {
				ids = append(ids, t.ID)
			}
		}
	}
	for _, id := range c.listing.Tickets {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// verbOf is the verb's entry for the ticket, nil when it has none.
func (c *chart) verbOf(id string) *chartTicket {
	if c.info == nil {
		return nil
	}
	for i, t := range c.info.Tickets {
		if t.ID == id {
			return &c.info.Tickets[i]
		}
	}
	return nil
}

// has is whether id is one of the chart's tickets.
func (c *chart) has(id string) bool {
	for _, x := range c.ids() {
		if x == id {
			return true
		}
	}
	return false
}

// title is the chart's title, its name when the map names none.
func (c *chart) title() string {
	if c.info != nil && c.info.Title != "" {
		return c.info.Title
	}
	return c.listing.Title
}

// chartRow is one ticket of a chart as the screens show it.
type chartRow struct {
	id, title string
	// t and d are the ticket and where it stands, nil and zero when the screens
	// know no such ticket yet; steps are its four rows.
	t     *ticket
	d     phase.Ticket
	steps []stepRow
	// verb is the verb's entry for the ticket, nil before it was read.
	verb *chartTicket
	// waits are the needs of the ticket that are not met: the verb's, or the
	// ticket list's blockers until the verb has been read.
	waits []chartWait
	// done is whether the ticket is published; needs, whether it waits for the
	// person.
	done, needs bool
}

// chartRows are the chart's tickets that have a ticket, in the verb's order (the
// chart's own before it was read).
func (m *Model) chartRows(c *chart) []chartRow {
	var rows []chartRow
	add := func(id, title string, verb *chartTicket) {
		r := chartRow{id: id, title: title, verb: verb}
		if t := m.w.tickets[id]; t != nil {
			r.t, r.title = t, firstOf(title, t.title)
			r.d = m.phaseOf(t)
			r.steps = m.stepRows(t, r.d)
			r.done = r.steps[len(r.steps)-1].Done
			r.needs = needsYou(r.d)
			r.waits = t.blockers
		}
		if verb != nil {
			r.waits = verb.Waits
			if r.t == nil {
				r.done = len(verb.Steps) > 0 && verb.Steps[len(verb.Steps)-1] == phase.StateDone
			}
		}
		rows = append(rows, r)
	}
	for _, id := range c.ids() {
		verb := c.verbOf(id)
		if verb != nil {
			add(id, verb.Title, verb)
		} else {
			add(id, "", nil)
		}
	}
	return rows
}

// needsYou is whether a phase that is under way waits for the person: an open
// question, an ask nobody decided, a brief nobody approved.
func needsYou(d phase.Ticket) bool {
	for _, p := range d.Phases {
		if p.State == phase.StateCurrent && hasSub(p, phase.SubNeedsYou) {
			return true
		}
	}
	return false
}

// state is the state the ticket's row is drawn in: its current step's.
func (r chartRow) state() string {
	if r.steps != nil {
		return r.steps[currentStep(r.steps)].State
	}
	if r.verb != nil {
		for i, s := range r.verb.Steps {
			if s == phase.StateCurrent && i < stepCount {
				return stepUnderWay[i]
			}
		}
		if r.done {
			return StatePublished
		}
	}
	return StateQueued
}

// chartDots are the ticket's four steps as icons: the screens' own derivation, or
// the verb's words for a ticket the screens do not know yet.
func (m *Model) chartDots(r chartRow) string {
	if r.steps != nil {
		return m.stepDots(r.steps)
	}
	parts := make([]string, stepCount)
	for i := range parts {
		state := StateQueued
		if r.verb != nil && i < len(r.verb.Steps) {
			switch r.verb.Steps[i] {
			case phase.StateDone:
				state = StateOK
			case phase.StateCurrent:
				state = stepUnderWay[i]
			}
		}
		parts[i] = m.st.icon(state)
	}
	return strings.Join(parts, " ")
}

// chartProgress counts the chart's tickets: all of them, the published ones and
// the ones that wait for the person.
func chartProgress(rows []chartRow) (total, done, needs int) {
	for _, r := range rows {
		total++
		if r.done {
			done++
		}
		if r.needs {
			needs++
		}
	}
	return total, done, needs
}

// chartStatus is where a chart stands, for a header and for the project's
// list: the state and the words. A ticket that needs the person comes first;
// then the verb's verdict, or, before it was read, what the screens can tell.
func (m *Model) chartStatus(c *chart, rows []chartRow) (state, label string) {
	total, done, needs := chartProgress(rows)
	switch {
	case needs > 0:
		return StateNeedsInput, "run needs you"
	case c.info != nil:
		switch c.info.Code {
		case phase.CodeChartDone:
			return StatePublished, "done"
		case phase.CodeChartWaitsMerge:
			return StateQueued, "waits for a merge"
		case phase.CodeChartBlocked:
			return StateStalled, "blocked"
		case phase.CodeChartNotGraduated:
			return StateQueued, "not graduated"
		}
		switch {
		case c.info.Ready && c.info.Started:
			return StateBuilding, "in progress"
		case c.info.Ready:
			return StateReady, "ready"
		}
		return StateQueued, "waiting"
	case total == 0:
		return StateQueued, "not graduated"
	case done == total:
		return StatePublished, "done"
	}
	for _, r := range rows {
		if r.t != nil && len(r.t.lines) > 0 {
			return StateBuilding, "in progress"
		}
	}
	return StateQueued, "not started"
}

// waitNote is what a ticket's unmet need says on its row: "stacked on X" or
// "waits for X merged", with the pull request's state when it is open.
func waitNote(w chartWait) string {
	if w.Kind == kindStacked {
		return "stacked on " + w.Ticket
	}
	note := "waits for " + w.Ticket + " merged"
	for _, pr := range w.PRs {
		if strings.EqualFold(pr.State, "open") {
			return note + " · its PR is open"
		}
	}
	return note
}

// endNote is where a run stops, in words: "SHOP-5 waits for SHOP-2 merged ·
// <pr> is open".
func endNote(e chartEnd) string {
	text := e.Ticket + " waits for " + e.Blocker + " " + firstOf(e.Kind, "merged")
	switch {
	case len(e.PRs) > 0 && e.PRs[0].State != "":
		return text + " · " + e.PRs[0].URL + " is " + strings.ToLower(e.PRs[0].State)
	case len(e.PRs) > 0:
		return text + " · " + e.PRs[0].URL
	case e.Fact != "":
		return text + " · " + e.Fact
	}
	return text
}

// runWord is where the chart's run stands, for the Run panel's edge.
func runWord(info *chartInfo) string {
	switch {
	case info.Code == phase.CodeChartDone:
		return "done"
	case info.Code == phase.CodeChartWaitsMerge:
		return "waits for a merge"
	case info.Started:
		return "in progress"
	}
	return "not started"
}
