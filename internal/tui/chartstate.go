package tui

import (
	"regexp"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// What a chart's standing is (which ticket waits for which, what a run would
// do, whether it is ready) is the verb's word: internal/phase derives it and
// `jig next <chart>` prints it, so the chart screen only draws it. Where each
// of the chart's tickets stands among its five steps, and what it waits for, is
// the ticket's digest (`jig next --json`): the same words its screen and the
// project's list draw, so a row and its ticket cannot differ.

// The kinds of need a ticket can wait on.
const kindStacked = "stacked"

// The words of a ticket that published and whose parent merged since: the
// verb's own for it (behind, a merge, a publish). The chart verb does not say
// which merge, so the screens cannot name it.
const (
	behindWord = "behind"
	behindOwes = "owes a merge, then a publish"
	behindText = behindWord + " · " + behindOwes
)

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

// waitsOnMerge is whether the chart's run ends at a merge: the last read that
// printed a chart listed at least one entry in its ends, which the verb sets
// from each entry's own unmet merged edge, so no text of a fact is read.
func (c *chart) waitsOnMerge() bool { return c.mergeWait }

// ticketWaitsOnMerge is whether the ticket's screens wait on a merge: it is
// known and not awaited, it is published with a pull request on record, and it
// is not closed. A merge on the host writes no line to any journal and a
// closure made in another session writes none either, so the status is where
// a screen would see either.
func (m *Model) ticketWaitsOnMerge(id string) bool {
	t := m.w.tickets[id]
	if t == nil || t.awaiting {
		return false
	}
	if p := t.publish(); !p.Done || p.PR == "" {
		return false
	}
	return t.status == nil || t.status.Closed == ""
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
	// t and d are the ticket and where its digest says it stands, nil and zero
	// when the screens know no such ticket yet; steps are its rows, saying
	// "reading" until the roster has listed the ticket.
	t     *ticket
	d     phase.Ticket
	steps []stepRow
	// verb is the verb's entry for the ticket, nil before it was read.
	verb *chartTicket
	// waits are the needs of the ticket that are not met, as its digest lists
	// them.
	waits []chartWait
	// done is whether the ticket is published; needs, whether it waits for the
	// person; behind, whether the ticket published and owes a merge, then a
	// publish again.
	done, needs, behind bool
	// end is where the ticket stands at its end, when atEnd says it does.
	end   endState
	atEnd bool
	// depth is how far the row is nested in a plan: 1 for a task of a
	// multi-task listed under it.
	depth int
	// mark is the state a row is drawn in when it is not its current step's, and
	// bare says it draws no steps: a multi-task's row in a plan.
	mark string
	bare bool
}

// chartRows are the chart's tickets that have a ticket, in the verb's order (the
// chart's own before it was read). A ticket stands where its digest says, and
// waits for what the digest says; a ticket the roster has not listed yet reads
// "reading" and nothing else. The verb's entry adds only the chart's own facts
// (its problem, the entries with no ticket).
func (m *Model) chartRows(c *chart) []chartRow {
	var rows []chartRow
	for _, id := range c.ids() {
		if verb := c.verbOf(id); verb != nil {
			rows = append(rows, m.rowOf(id, verb.Title, verb))
		} else {
			rows = append(rows, m.rowOf(id, "", nil))
		}
	}
	return rows
}

// rowOf is the row of ticket id as the screens show it, under title when the
// chart names one.
func (m *Model) rowOf(id, title string, verb *chartTicket) chartRow {
	r := chartRow{id: id, title: title, verb: verb}
	if t := m.w.tickets[id]; t != nil {
		r.t, r.title = t, firstOf(title, t.title)
		r.d = readingPhases()
		r.steps, _ = m.rowSteps(t, r.d)
		if t.digest != nil {
			r.d = m.phaseOf(t)
			r.steps, _ = m.rowSteps(t, r.d)
			// A question and a landing are the person's whoever else is at work; what a
			// live session records itself (an approval, a round's asks) waits.
			r.needs = m.needsYouOf(t, r.d)
			r.waits = t.blockers
			r.behind = t.digest.Behind
			r.end, r.atEnd = m.endOf(t)
		}
		r.done = t.digest != nil && r.steps[stepPublish].Done
	}
	return r
}

// needsYouOf is whether the ticket t, standing as d says, waits for the person: a
// question or a landing is theirs whoever else is at work, and what a live
// session records itself (an approval, a round's asks) waits. A task two issues
// list is asked about as it stands in one issue (cutFor), so its landing stop
// is that issue's and no other's.
func (m *Model) needsYouOf(t *ticket, d phase.Ticket) bool {
	return t.digest.waitsForAnswer() || len(t.openQuestions(m.zone, "")) > 0 || (needsYou(t, d) && !m.ticketLive(t)) || m.landingNeedsYou(t)
}

// needsYou is whether a phase that is under way waits for the person: an open
// question, an ask nobody decided, a brief nobody approved.
func needsYou(t *ticket, d phase.Ticket) bool {
	for _, p := range d.Phases {
		if p.State == phase.StateCurrent && t.subIs(p, phase.SubNeedsYou) {
			return true
		}
	}
	return false
}

// state is the state the ticket's row is drawn in: its current step's, queued
// for a ticket nothing is known of.
func (r chartRow) state() string {
	if r.mark != "" {
		return r.mark
	}
	if r.steps != nil {
		return r.steps[currentStep(r.steps)].State
	}
	return StateQueued
}

// chartDots are the ticket's steps as icons; queued ones for a ticket the
// screens know nothing of.
func (m *Model) chartDots(r chartRow) string {
	if r.bare {
		// A multi-task has no five steps of its own: the column is left empty.
		return spaces(2*stepCount - 1)
	}
	if r.steps != nil {
		return m.stepDots(r.steps)
	}
	parts := make([]string, stepCount)
	for i := range parts {
		parts[i] = m.st.icon(StateQueued)
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
	case c.info != nil && c.info.Code == phase.CodeChartWaitsLand:
		// The run stops at a landing, which is the person's word: it says so
		// before it says that a ticket needs them, since the landing is what
		// they are asked for.
		return StateNeedsInput, "landing waits for you"
	case needs > 0:
		return StateNeedsInput, "run needs you"
	case c.info != nil:
		switch c.info.Code {
		case phase.CodeChartDone:
			return StatePublished, "done"
		case phase.CodeChartWaitsMerge:
			return StateQueued, "waits for a merge"
		case phase.CodeChartWaitsLand:
			// The run stops at a landing, which is the person's word.
			return StateNeedsInput, "landing waits for you"
		case phase.CodeChartBlocked:
			return StateStalled, "blocked"
		case phase.CodeChartNotGraduated:
			return StateQueued, "not graduated"
		}
		if _, live := c.live(m.w, m.now()); live {
			// A session is at work on the run: it is running, whatever else
			// the verdict says of what comes next.
			return StateBuilding, "running"
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
	if _, live := c.live(m.w, m.now()); live {
		return StateBuilding, "running"
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

// endNote is where a run stops, in words, naming the pull request to merge
// once: "SHOP-5 waits for SHOP-2 to merge · PR #7 open", or for a published
// ticket stacked on a parent whose pull request is open, "SHOP-5 retargets
// when SHOP-2 merges · PR #7 open". The verb's own fact is said only when the
// blocker has no pull request, since it says the same of the one it has.
func endNote(e chartEnd) string {
	text := e.Ticket + " waits for " + e.Blocker + " to merge"
	if e.Kind == kindStacked {
		// A published ticket on a parent whose pull request is open: it is done,
		// and the parent's merge owes it a retarget (ADR 0038).
		text = e.Ticket + " retargets when " + e.Blocker + " merges"
	}
	switch {
	case len(e.PRs) > 0:
		return text + " · " + chartPRWords(e.PRs[0])
	case e.Fact != "":
		return text + " · " + e.Fact
	}
	return text
}

// prNumber matches the number a pull request's address ends in.
var prNumber = regexp.MustCompile(`/(?:pull|pulls|merge_requests)/(\d+)`)

// chartPRWords names a pull request and its state: "PR #7 open", or its address when
// no number can be read from it.
func chartPRWords(pr chartPR) string {
	name := pr.URL
	if m := prNumber.FindStringSubmatch(pr.URL); m != nil {
		name = "PR #" + m[1]
	}
	if pr.State != "" {
		name += " " + strings.ToLower(pr.State)
	}
	return name
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
