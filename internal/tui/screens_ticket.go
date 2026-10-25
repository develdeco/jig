package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// activityLines is how many of a step's latest lines a screen shows.
const activityLines = 8

// substepLines is the most sub-steps the block lists, not counting its frame.
// Past that it lists the ones that matter and counts the rest.
const substepLines = 6

// ticketPage is one ticket: its four steps in order, then the sub-steps of the
// step under the cursor, what waits for a person, what to do next, and the
// activity of that step (the current step until the cursor moves).
func (m *Model) ticketPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	if t.awaiting {
		return m.waitingPage(id)
	}
	d := m.phaseOf(t)
	steps := m.stepRows(t, d)
	cur := currentStep(steps)
	right := fmt.Sprintf("step %d of %d", cur+1, len(steps))
	if steps[len(steps)-1].Done {
		right = "done"
	}
	p.boxTop("Steps", right)
	for i, s := range steps {
		p.boxRow(rowRef{kind: s.Kind, ticket: id}, m.stepLine(s, i == cur))
	}
	p.boxEnd()

	// What follows the sub-steps: the rows that wait for a person, whose
	// cursor places come after the steps', and the next line.
	var needs, next page
	qs := t.openQuestions(m.zone, "")
	asks := t.asks()
	if len(qs)+len(asks) > 0 {
		needs.add(m.st.bold("needs you"))
		for _, q := range qs {
			needs.addRow(rowRef{kind: rowQuestion, id: q.ID, ticket: id, slice: q.Slice}, m.questionLine(t, q))
		}
		for i, line := range m.askLines(asks) {
			needs.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
		}
	}
	if t.status != nil && len(t.status.Help) > 0 {
		next.add(m.st.dim("next  ") + t.status.Help[0])
	}

	// The heights. The screen has a body of so many rows: the steps, the
	// groups below, and the activity panel at its floor of three lines come
	// first, the sub-steps block takes what is left up to its own most, and
	// the blank lines that set text apart from text come out of what remains
	// of that. The activity panel gets the rest.
	focus := m.ticketFocus(steps, len(qs))
	body := m.bodyRows()
	var groups []page
	for _, g := range []page{needs, next} {
		if len(g.lines) > 0 {
			groups = append(groups, g)
		}
	}
	taken := len(p.lines)
	for _, g := range groups {
		taken += len(g.lines)
	}
	floor := 2 + min(3, max(1, len(t.stepLines(focus))))
	spare := body - taken - floor

	var block page
	if subs := d.Phases[focus].Subs; len(subs) > 0 && spare >= 3 {
		block = m.substepsPanel(focus, subs, min(substepLines+2, spare, len(subs)+2))
		spare -= len(block.lines)
	}
	blank := func() bool {
		if spare > 0 {
			spare--
			return true
		}
		return false
	}
	gapBetween := len(groups) > 1 && blank()
	gapBefore := len(groups) > 0 && blank()
	gapActivity := blank()

	p.lines = append(p.lines, block.lines...)
	for i, g := range groups {
		if (i == 0 && gapBefore) || (i > 0 && gapBetween) {
			p.add("")
		}
		p.lines = append(p.lines, g.lines...)
	}
	if gapActivity {
		p.add("")
	}
	m.activityPanel(&p, t, focus)
	return p
}

// waitingPage is the screen of a ticket the store has no folder for yet, which
// the person asked to watch: it fills in once the ticket exists.
func (m *Model) waitingPage(id string) page {
	var p page
	p.add(m.st.icon(StateQueued) + " waiting for " + id + " to be created")
	p.add(m.st.dim("it fills in as soon as the ticket has a journal line or is listed"))
	return p
}

// stepLine is a step's row: its icon, number and name, where it stands, and
// how long it took once done. A step not reached yet is drawn back.
func (m *Model) stepLine(s stepRow, current bool) string {
	name := pad(stepNames[s.Step], 8)
	num := strconv.Itoa(s.Step+1) + " "
	text := s.Text
	switch {
	case current:
		name, text = m.st.bold(name), m.st.state(s.State, text)
	case !s.Done:
		num, name, text = m.st.dim(num), m.st.dim(name), m.st.dim(text)
	}
	return m.st.icon(s.State) + " " + num + name + " " + m.st.dim(pad(s.Took, 7)) + " " + text
}

// ticketFocus is the step whose sub-steps and activity the ticket screen
// shows: the one under the cursor, the build for a question, the review for an
// ask, and the current step while the cursor follows it. The screen's rows are
// the steps, then the open questions, then the asks, so the cursor's place says
// which.
func (m *Model) ticketFocus(steps []stepRow, questions int) int {
	e := m.top()
	switch {
	case e.follow:
		return currentStep(steps)
	case e.sel < len(steps):
		return max(0, e.sel)
	case e.sel < len(steps)+questions:
		return stepBuild
	}
	return stepReview
}

// substepsPanel is a panel of the sub-steps of step, height lines tall with its
// frame: a checklist with a state, a label and a few words of detail each. When
// they do not all fit it shows the ones that matter most (a person's turn, a
// failure, the work under way), in their order, and says how many more there
// are.
func (m *Model) substepsPanel(step int, subs []phase.Sub, height int) page {
	var p page
	done := 0
	for _, s := range subs {
		if s.State == phase.SubDone {
			done++
		}
	}
	p.boxTop(stepTitle(step)+" · sub-steps", fmt.Sprintf("%d of %d done", done, len(subs)))
	room := height - 2
	shown := pickSubs(subs, room)
	for _, i := range shown {
		p.boxAdd(m.subLine(step, subs[i]))
	}
	if more := len(subs) - len(shown); more > 0 && room >= 2 {
		p.boxAdd(m.st.dim("+" + strconv.Itoa(more) + " more"))
	}
	p.boxEnd()
	return p
}

// pickSubs are the positions of the sub-steps to show in room lines, in their
// order. All of them when they fit; else room-1 of them, to leave a line for
// the count of the rest (the only one when room is 1).
func pickSubs(subs []phase.Sub, room int) []int {
	if len(subs) <= room {
		all := make([]int, len(subs))
		for i := range all {
			all[i] = i
		}
		return all
	}
	keep := room - 1
	if room < 2 {
		keep = room
	}
	order := make([]int, len(subs))
	for i := range order {
		order[i] = i
	}
	// A person's turn and a failure first, then what is under way, what waits,
	// what is next, and last what is done, the latest first.
	sort.SliceStable(order, func(a, b int) bool {
		pa, pb := subWeight(subs[order[a]]), subWeight(subs[order[b]])
		if pa != pb {
			return pa < pb
		}
		return pa == subWeightDone && order[a] > order[b]
	})
	picked := order[:max(0, keep)]
	sort.Ints(picked)
	return picked
}

// subWeightDone is the weight of a sub-step that is done: the last to show.
const subWeightDone = 4

// subWeight orders sub-steps by how much a person looking at the block needs
// to see them: lower first.
func subWeight(s phase.Sub) int {
	switch s.State {
	case phase.SubNeedsYou, phase.SubFailed:
		return 0
	case phase.SubActive:
		return 1
	case phase.SubWaiting:
		return 2
	case phase.SubPending:
		return 3
	}
	return subWeightDone
}

// subLine is a sub-step as a line of the block: its state's icon, its label and
// a few words of detail. The ones that are done, waiting or still to come are
// drawn back.
func (m *Model) subLine(step int, s phase.Sub) string {
	text := s.Label
	if s.Detail != "" {
		text += m.st.dim(" · " + s.Detail)
	}
	switch s.State {
	case phase.SubActive, phase.SubNeedsYou, phase.SubFailed:
		text = m.st.bold(s.Label)
		if s.Detail != "" {
			text += m.st.dim(" · " + s.Detail)
		}
	case phase.SubWaiting, phase.SubPending:
		text = m.st.dim(s.Label)
		if s.Detail != "" {
			text += m.st.dim(" · " + s.Detail)
		}
	}
	return m.st.icon(subState(step, s)) + " " + text
}

// subState is the state a sub-step is drawn in.
func subState(step int, s phase.Sub) string {
	switch s.State {
	case phase.SubDone:
		return StateOK
	case phase.SubFailed:
		return StateStalled
	case phase.SubNeedsYou:
		if s.Key == "asks" {
			return StateAsk
		}
		return StateNeedsInput
	case phase.SubActive:
		switch {
		case strings.HasPrefix(s.Key, "round:"):
			return StateReviewing
		case step == stepPublish:
			return StatePublishing
		}
		return StateBuilding
	}
	return StateQueued
}

// activityPanel adds a panel of the latest lines of step to p: as many as
// the rows left below what p holds allow, activityLines at most and three at
// least, so the newest line and the panel's edge stay on the screen. Each
// line names the sub-step it advances.
func (m *Model) activityPanel(p *page, t *ticket, step int) {
	lines := t.stepLines(step)
	keep := min(activityLines, max(3, m.bodyRows()-len(p.lines)-2))
	right := ""
	if n := len(lines); n > keep {
		right = fmt.Sprintf("latest %d of %d", keep, n)
		lines = lines[n-keep:]
	}
	p.boxTop(stepTitle(step)+" · activity", right)
	if len(lines) == 0 {
		p.boxAdd(m.st.dim("nothing yet"))
	}
	labels := m.subLabelsOf(t)
	for _, l := range lines {
		p.boxAdd(m.activityLine(t, labels, l))
	}
	p.boxEnd()
}

// questionLine is an open question as a row: who asked about which slice, and
// the question.
func (m *Model) questionLine(t *ticket, q questionRow) string {
	who := firstOf(m.who(t, q.Session), "a session")
	return m.st.icon(StateNeedsInput) + " " + who + " asks about slice " + q.Slice + ": " + firstLine(q.Text) + m.st.dim("  "+q.ID)
}

// firstLine is the first line of text.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// askLines are the asks as rows with their columns aligned: the icon (the
// ask's decision), risk, place, what has been decided, the title and the id.
// The decision comes before the title, which a narrow screen cuts.
func (m *Model) askLines(asks []askRow) []string {
	riskW, whereW, decW := 0, 0, 0
	words := make([]string, len(asks))
	for i, a := range asks {
		riskW, whereW = max(riskW, width(a.Risk)), max(whereW, width(a.Where))
		switch a.Decision {
		case "keep":
			words[i] = "kept" + decidedBy(a)
		case "dismiss":
			words[i] = "dismissed" + decidedBy(a)
		default:
			words[i] = "undecided"
		}
		decW = max(decW, width(words[i]))
	}
	out := make([]string, len(asks))
	for i, a := range asks {
		state, decision := StateAsk, m.st.dim(pad(words[i], decW))
		switch a.Decision {
		case "keep":
			state, decision = StateKept, m.st.state(StateKept, pad(words[i], decW))
		case "dismiss":
			state, decision = StateDismissed, m.st.state(StateDismissed, pad(words[i], decW))
		}
		out[i] = fmt.Sprintf("%s %s  %s  %s  %s%s", m.st.icon(state), pad(a.Risk, riskW), pad(a.Where, whereW), decision, a.Title, m.st.dim("  "+a.ID))
	}
	return out
}

func decidedBy(a askRow) string {
	switch a.DecidedBy {
	case "":
		return ""
	case "person":
		return " by you"
	}
	return " by an " + a.DecidedBy
}
