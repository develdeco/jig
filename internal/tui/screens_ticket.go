package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// activityLines is how many of a step's latest lines a screen shows, and
// substepLines the most sub-steps the block lists, not counting its frame (past
// that it lists the ones that matter and counts the rest), on the screen they
// were set for: 80 by 24, whose body has bodyRef rows. A taller terminal has
// room for more of both (activityCap, substepCap).
const (
	activityLines = 8
	substepLines  = 6
	bodyRef       = 19
	// nextLines is how many lines the "next" line may take.
	nextLines = 2
)

// substepCap is the most sub-steps the block lists in a body of body rows:
// substepLines on the 80x24 screen, and a third of each row a taller one adds.
func substepCap(body int) int { return substepLines + max(0, body-bodyRef)/3 }

// activityCap is the most lines of a step's activity a body of body rows shows:
// activityLines on the 80x24 screen, and two thirds of each row a taller one
// adds. Together with substepCap they take all the rows a tall terminal adds.
func activityCap(body int) int { return activityLines + max(0, body-bodyRef)*2/3 }

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
		// The line wraps to a second line and is cut, with an ellipsis, only
		// after that: what to do next is the sentence a person reads.
		for i, line := range wrap(t.status.Help[0], m.width-8, nextLines) {
			if i == 0 {
				line = m.st.dim("next  ") + line
			} else {
				line = "      " + line
			}
			next.add(line)
		}
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
	floor := 2 + min(3, max(1, len(t.logLines())))
	spare := body - taken - floor

	var block page
	if subs := d.Phases[focus].Subs; len(subs) > 0 && spare >= 3 {
		// The sub-step that matters now takes the rows of its line and as
		// many more as its fields and live line need.
		extra := 0
		if f := focusIndex(subs); f >= 0 {
			extra = max(0, m.openWant(t, focus, subs[f])-1)
		}
		block = m.substepsPanel(t, focus, subs, min(substepCap(body)+2+extra, spare, len(subs)+2+extra))
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
	m.activityPanel(&p, t)
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
// frame, titled by the step's name alone (the steps above number it): a list
// with a state, a label and a few words each, the labels in a column, and the
// one that matters now (focusIndex) opened (focus.go) when the panel has room
// for it beside the rest. When they do not all fit it shows the ones that
// matter most (a person's turn, a failure, the work under way), in their
// order, and says how many more there are.
func (m *Model) substepsPanel(t *ticket, step int, subs []phase.Sub, height int) page {
	var p page
	done := 0
	labelW := 0
	for _, s := range subs {
		if s.State == phase.SubDone {
			done++
		}
		labelW = max(labelW, min(labelMaxW, width(s.Label)))
	}
	p.boxTop(stepNames[step], fmt.Sprintf("%d of %d done", done, len(subs)))
	room := height - 2
	focus, openRows := focusIndex(subs), 0
	if focus >= 0 {
		// The open sub-step takes what the other lines leave; with too many
		// of them, two stay, the rest are counted, and it takes the remainder.
		others := len(subs) - 1
		want := m.openWant(t, step, subs[focus])
		switch {
		case room-others >= openMin:
			openRows = min(want, room-others)
		case room-min(others, 2) >= openMin:
			openRows = min(want, room-min(others, 2))
		}
	}
	if openRows == 0 {
		shown := pickSubs(subs, room)
		for _, i := range shown {
			p.boxAdd(m.foldedLine(t, step, subs[i], labelW))
		}
		if more := len(subs) - len(shown); more > 0 && room >= 2 {
			p.boxAdd(m.st.dim("+" + strconv.Itoa(more) + " more"))
		}
		p.boxEnd()
		return p
	}

	others := make([]phase.Sub, 0, len(subs)-1)
	at := make([]int, 0, len(subs)-1)
	for i, s := range subs {
		if i != focus {
			others, at = append(others, s), append(at, i)
		}
	}
	shownAt := map[int]bool{}
	picked := pickSubs(others, room-openRows)
	for _, j := range picked {
		shownAt[at[j]] = true
	}
	for i, s := range subs {
		switch {
		case i == focus:
			for _, line := range m.openLines(t, step, s, subs, labelW, openRows) {
				p.boxAdd(line)
			}
		case shownAt[i]:
			p.boxAdd(m.foldedLine(t, step, s, labelW))
		}
	}
	if more := len(others) - len(picked); more > 0 {
		p.boxAdd(m.st.dim("+" + strconv.Itoa(more) + " more"))
	}
	p.boxEnd()
	return p
}

// labelMaxW is the widest a sub-step's label column gets.
const labelMaxW = 16

// foldedLine is a sub-step on one line: subLine with what the status adds to
// its detail (subDetail), and how long it took once it is done.
func (m *Model) foldedLine(t *ticket, step int, s phase.Sub, labelW int) string {
	s.Detail = strings.Join(nonEmpty(m.subDetail(t, s), t.subSummary(step, s)), " · ")
	return m.subLine(step, s, labelW)
}

// subDetail is a sub-step's detail with what the status adds to it once it is
// done: the headings of the brief's sections, the ids of its slices.
func (m *Model) subDetail(t *ticket, s phase.Sub) string {
	if s.State != phase.SubDone {
		return s.Detail
	}
	var names []string
	switch s.Key {
	case "drafted":
		names = t.headings()
	case "slices":
		for _, sl := range t.planned() {
			names = append(names, sl.ID)
		}
	}
	switch {
	case len(names) == 0:
		return s.Detail
	case s.Detail == "":
		return strings.Join(names, ", ")
	}
	return s.Detail + ": " + strings.Join(names, ", ")
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

// subLine is a sub-step as a line of the block: its state's icon, its label in
// a column labelW wide, and a few words of detail. The ones that are waiting or
// still to come are drawn back.
func (m *Model) subLine(step int, s phase.Sub, labelW int) string {
	label := pad(s.Label, labelW)
	detail := ""
	if s.Detail != "" {
		detail = m.st.dim("  " + s.Detail)
	}
	switch s.State {
	case phase.SubActive, phase.SubNeedsYou, phase.SubFailed:
		label = m.st.bold(label)
	case phase.SubWaiting, phase.SubPending:
		label = m.st.dim(label)
	}
	return m.st.icon(subState(step, s)) + " " + label + detail
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

// stepActivityPanel adds a panel of the latest lines of step to p, for the
// step's own screen: as many as
// the rows left below what p holds allow, activityLines at most and three at
// least, so the newest line and the panel's edge stay on the screen. Each
// line names the sub-step it advances.
func (m *Model) stepActivityPanel(p *page, t *ticket, step int) {
	lines := t.stepLines(step)
	keep := min(activityCap(m.bodyRows()), max(3, m.bodyRows()-len(p.lines)-2))
	right := ""
	if n := len(lines); n > keep {
		right = fmt.Sprintf("latest %d of %d", keep, n)
		lines = lines[n-keep:]
	}
	p.boxTop(stepNames[step]+" activity", right)
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
