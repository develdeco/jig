package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
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
)

// substepCap is the most sub-steps the block lists in a body of body rows:
// substepLines on the 80x24 screen, and a third of each row a taller one adds.
func substepCap(body int) int { return substepLines + max(0, body-bodyRef)/3 }

// activityCap is the most lines of a step's activity a body of body rows shows:
// activityLines on the 80x24 screen, and two thirds of each row a taller one
// adds. Together with substepCap they take all the rows a tall terminal adds.
func activityCap(body int) int { return activityLines + max(0, body-bodyRef)*2/3 }

// ticketPage is one ticket: its five steps in order, then the sub-steps of the
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
	if isMulti(t) {
		// A multi-task has no five steps: its screen is the plan one level down.
		return m.multiPage(t)
	}
	d := m.phaseOf(t)
	steps := m.stepRows(t, d)
	cur := currentStep(steps)
	right := fmt.Sprintf("step %d of %d", cur+1, len(steps))
	if steps[len(steps)-1].Done {
		right = "done"
	}
	if total := totalTook(steps); total > 0 {
		right += " · worked " + ago(total)
	}
	rows := make([]listRow, len(steps))
	for i, s := range steps {
		rows[i] = listRow{ref: &rowRef{kind: s.Kind, ticket: id}, cells: m.stepCells(s, i == cur)}
	}
	m.listPanel(&p, listSpec{title: "Steps", summary: right, cols: stepColumns, names: stepColumnNames, rows: rows})

	// What follows the sub-steps: the rows that wait for a person, whose
	// cursor places come after the steps'. There is no line of what to run
	// next: the status's help is written for the session, which reads it
	// itself, and the session tells the person the prompt a phase waits for.
	var needs page
	// The group is drawn while something still waits on the person: a question
	// nobody answered or an ask nobody decided (the asks already decided stay
	// beside the ones left, so a decision can be seen). When the last answer or
	// decision is journaled the group goes. The asks of a round stay with their
	// controls whether or not a session is live, since the person decides them
	// from here whoever else is at work; only the words that tell the person to
	// act wait for the line to go quiet.
	qs := t.openQuestions(m.zone, "")
	asks := t.asks()
	undecided := false
	for _, a := range asks {
		undecided = undecided || a.Decision == ""
	}
	if !undecided {
		asks = nil
	}
	// The landing is the person's word, which a session merges at: never held
	// back.
	landing := m.landingNeedsYou(t)
	if len(qs)+len(asks) > 0 || landing {
		// A live session holds back the call to act, not the asks: the group
		// reads asks while one is at work and only asks wait.
		title := "Needs you"
		if len(qs) == 0 && !landing && m.ticketLive(t) {
			title = "Asks"
		}
		for _, q := range qs {
			needs.addRow(rowRef{kind: rowQuestion, id: q.ID, ticket: id, slice: q.Slice}, m.questionLine(t, q))
			for _, l := range m.panelLines(q.Text) {
				needs.lines = append(needs.lines, pline{text: "    " + l, cont: true})
			}
		}
		for i, line := range m.askLines(asks) {
			needs.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
			m.addAskText(&needs, t, asks[i])
		}
		if landing {
			needs.addRow(rowRef{kind: rowLand, ticket: id}, m.landNeedsLine(t))
		}
		n := len(qs) + len(asks)
		if landing {
			n++
		}
		needs.intoPanel(0, title, strconv.Itoa(n)+" waiting")
	}

	// The heights. The screen has a body of so many rows: the steps, the
	// groups below, and the activity panel at its floor of three lines come
	// first, the sub-steps block takes what is left up to its own most, and
	// the blank lines that set text apart from text come out of what remains
	// of that. The activity panel gets the rest.
	focus := m.ticketFocus(steps, len(qs), len(asks))
	body := m.bodyRows()
	var groups []page
	for _, g := range []page{needs} {
		if len(g.lines) > 0 {
			groups = append(groups, g)
		}
	}
	taken := m.taken(&p)
	for _, g := range groups {
		taken += len(g.lines)
	}
	floor := 3 + min(3, max(1, len(t.logLines())))
	spare := body - taken - floor

	var block page
	if subs := d.Phases[focus].Subs; len(subs) > 0 && spare >= 3 {
		// The sub-step that matters now takes the rows of its line and as
		// many more as its fields and live line need.
		extra := 0
		if f := focusIndex(subs); f >= 0 {
			extra = max(0, m.openWant(t, focus, subs[f])-1)
		}
		block = m.substepsPanel(t, focus, subs, min(substepCap(body)+3+extra, spare, len(subs)+3+extra))
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
	p.windowed = block.windowed
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

// stepColumns are the columns of the steps: the icon, the number, the name, how
// long it has taken, the words and the clock. The time taken and the clock are
// never cut: the names give way first, and the words wrap.
var stepColumns = []column{{}, {gap: 1}, {gap: 1}, {gap: 1, keep: true}, {free: true, gap: 2}, {gap: 1, keep: true}}

// stepColumnNames name the columns of the steps.
var stepColumnNames = []string{"", "", "step", "took", "where it stands", ""}

// stepCells are a step's row as cells: its icon, number and name, how long it
// has taken, where it stands, and the clock of a wait or of the time since
// nobody worked on it. A step not reached yet is drawn back, and the time of
// the step under way, which counts on, is not.
func (m *Model) stepCells(s stepRow, current bool) []string {
	num, name, text, took, clock := strconv.Itoa(s.Step+1), stepNames[s.Step], s.Text, m.st.dim(s.Took), ""
	switch {
	case s.Idle != "":
		// A step nobody is working on is drawn back, its time stopped.
		name, text, clock = m.st.bold(name), m.st.dim(text), m.st.dim("· "+s.Idle)
	case s.Wait != "":
		// A step that waits for a person or the host reads it in the colour of
		// a turn that is not the builder's, current or not.
		name, text, clock = m.st.bold(name), m.st.state(StateNeedsInput, text), m.st.state(StateNeedsInput, s.Wait)
		took = s.Took
	case current && !s.Done:
		name, text, took = m.st.bold(name), m.st.state(s.State, text), s.Took
	case current:
		name, text = m.st.bold(name), m.st.state(s.State, text)
	case !s.Done:
		num, name, text = m.st.dim(num), m.st.dim(name), m.st.dim(text)
	}
	return []string{m.st.icon(s.State), num, name, took, text, clock}
}

// ticketFocus is the step whose sub-steps and activity the ticket screen
// shows: the one under the cursor, the build for a question, the review for an
// ask, and the current step while the cursor follows it. The screen's rows are
// the steps, then the open questions, then the asks, so the cursor's place says
// which.
func (m *Model) ticketFocus(steps []stepRow, questions, asks int) int {
	e := m.top()
	if pf := e.paneFocus; pf != "" && pf != "Steps" && !e.follow {
		// The panes show the step the Steps cursor is on, whichever has the focus.
		if pc := e.paneCurs["Steps"]; pc.set {
			for i, s := range steps {
				if s.Kind == pc.id.kind {
					return i
				}
			}
		}
		return currentStep(steps)
	}
	switch {
	case e.follow:
		return currentStep(steps)
	case e.sel < len(steps):
		return max(0, e.sel)
	case e.sel < len(steps)+questions:
		return stepBuild
	case e.sel < len(steps)+questions+asks:
		return stepReview
	}
	return stepLand
}

// subColumns are the columns of a sub-step's line: its state's icon, its label
// and its detail.
var subColumns = []column{{}, {gap: 1}, {free: true, gap: 2}}

// subColumnNames name the columns of a sub-step's line.
var subColumnNames = []string{" ", "sub-step", "detail"}


// foldedCells are a sub-step on one line: subCells with what the status adds to
// its detail (subDetail), and how long it took once it is done.
func (m *Model) foldedCells(t *ticket, step int, s phase.Sub, labelW int, c clocks) []string {
	s.Detail = strings.Join(nonEmpty(m.subDetail(t, s), m.subSummary(c, step, s)), " · ")
	return m.subCells(step, s, labelW)
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

// subCells are a sub-step as the cells of a line of the block: its state's
// icon, its label in a column labelW wide, and a few words of detail. The ones
// that are waiting or still to come are drawn back.
func (m *Model) subCells(step int, s phase.Sub, labelW int) []string {
	label := pad(s.Label, labelW)
	switch s.State {
	case phase.SubActive, phase.SubNeedsYou, phase.SubFailed:
		label = m.st.bold(label)
	case phase.SubWaiting, phase.SubPending:
		label = m.st.dim(label)
	}
	return []string{m.st.icon(subState(step, s)), label, m.st.dim(s.Detail)}
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
	all := newestFirst(t.stepLines(step), func(l events.Envelope) time.Time { at, _ := l.At(); return at })
	lines, older := cutNewest(all, min(activityCap(m.bodyRows()), max(3, m.bodyRows()-m.taken(p)-3)))
	right := ""
	if older > 0 {
		right = fmt.Sprintf("latest %d of %d", len(lines), len(all))
	}
	labels := m.subLabelsOf(t)
	rows := make([]listRow, len(lines))
	for i, l := range lines {
		rows[i] = listRow{cells: m.activityCells(t, labels, l)}
	}
	spec := listSpec{title: stepNames[step] + " activity", summary: right, cols: activityColumns, names: activityColumnNames, rows: rows, empty: "nothing yet"}
	if older > 0 {
		spec.after = []string{m.olderRow(older)}
	}
	m.listPanel(p, spec)
}

// questionLine is an open question as a row: who asked about which slice, and
// the question's id. The question itself follows the row, whole (panelLines).
func (m *Model) questionLine(t *ticket, q questionRow) string {
	who := firstOf(m.who(t, q.Session), "a session")
	return m.st.icon(StateNeedsInput) + " " + who + " asks about slice " + q.Slice + m.st.dim("  "+q.ID)
}

// panelLines is the text of a question or an ask as a person reads it to act:
// wrapped to the room a panel's line has under its marker and a four-cell
// indent, none cut. A question's ask and its default are often its last line,
// so a cut would hide them.
func (m *Model) panelLines(text string) []string {
	return wrapWhole(text, m.innerWidth()-4)
}

// addAskText adds an ask's text under its row, whole, each line tagged with the
// ask's id.
func (m *Model) addAskText(p *page, t *ticket, a askRow) {
	for _, l := range m.panelLines(t.askText(a)) {
		// The text belongs to its row, as the lines a row wrapped onto do: a window
		// that shows the row shows it.
		p.lines = append(p.lines, pline{text: "    " + m.st.dim(l), ask: a.ID, cont: true})
	}
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
		_, words[i] = askDecision(a)
		decW = max(decW, width(words[i]))
	}
	out := make([]string, len(asks))
	for i, a := range asks {
		state, _ := askDecision(a)
		decision := m.st.dim(pad(words[i], decW))
		if state != StateAsk {
			decision = m.st.state(state, pad(words[i], decW))
		}
		out[i] = fmt.Sprintf("%s %s  %s  %s  %s%s", m.st.icon(state), pad(a.Risk, riskW), pad(a.Where, whereW), decision, a.Title, m.st.dim("  "+a.ID))
	}
	return out
}

// askDecision is the state an ask is drawn in and the word for what has been
// decided of it.
func askDecision(a askRow) (state, word string) {
	switch a.Decision {
	case "keep":
		return StateKept, "kept" + decidedBy(a)
	case "dismiss":
		return StateDismissed, "dismissed" + decidedBy(a)
	}
	return StateAsk, "undecided"
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
