package tui

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// The sub-steps block folds every sub-step to a line and opens the one that
// matters now into a card: what it is for, who holds it, and its latest moves,
// which are its own journal lines and what the session that holds it did
// meanwhile, in the words it gave each call (the machine ledger's tool lines).
// A block too short for the card keeps the one-line form.

const (
	// cardMoves is the most moves a card lists on the 80 by 24 screen; a
	// taller one lists a third of each body row it adds more (cardMovesIn).
	cardMoves = 8
	// cardMin is the fewest rows a card takes: its two edges, one fact and one
	// move.
	cardMin = 4
	// goalRows is the most rows a slice's goal takes in its card, and
	// briefRows the most a brief's sections or slices take.
	goalRows  = 3
	briefRows = 3
)

// focusIndex is the sub-step the block opens into a card: a person's turn or a
// failure first, else the work under way. -1 when none is, as in a step that
// is done or not reached, whose sub-steps all fold.
func focusIndex(subs []phase.Sub) int {
	best, bestW := -1, 2
	for i, s := range subs {
		if w := subWeight(s); w < bestW {
			best, bestW = i, w
		}
	}
	return best
}

// subTrack is what happened on one sub-step: its own lines, the session that
// holds it (the latest of them to name one), when it began and, once it is
// over, when it ended.
type subTrack struct {
	own        []events.Envelope
	holder     string
	start, end time.Time
	// borrowed is set when the holder is the ticket's latest session, not one
	// the sub-step's own lines name (openTrack): it finds the moves, and is
	// named only for a sub-step whose role says what it does there.
	borrowed bool
}

// trackOf reads the sub-step key of step from the ticket's lines.
func (t *ticket) trackOf(step int, key string) subTrack {
	var tr subTrack
	for _, l := range t.stepLines(step) {
		if t.subKey(l) != key {
			continue
		}
		tr.own = append(tr.own, l)
		if l.Session != "" {
			tr.holder = l.Session
		}
		if at, ok := l.At(); ok {
			if tr.start.IsZero() {
				tr.start = at
			}
			tr.end = at
		}
	}
	return tr
}

// openTrack is trackOf for a sub-step under way. One that has written no line
// of its own yet (a pull request being opened once its body is written, a
// brief being drafted) is held by the session that wrote the ticket's latest
// line naming one, since the ticket's latest line: what that session has done
// since are its moves. A ticket whose journal names no session yet is held by
// the session the machine ledger ties to it (the one that minted it, for a
// brief), since its first line there.
func (t *ticket) openTrack(step int, key string) subTrack {
	tr := t.trackOf(step, key)
	if len(tr.own) > 0 {
		return tr
	}
	for _, l := range t.seen {
		if at, ok := l.At(); ok && at.After(tr.start) {
			tr.start = at
		}
	}
	for i := len(t.lines) - 1; i >= 0; i-- {
		l := t.lines[i]
		if at, ok := l.At(); ok && at.After(tr.start) {
			tr.start = at
		}
		if l.Session != "" {
			tr.holder, tr.borrowed = l.Session, true
			return tr
		}
	}
	for _, l := range t.ledger {
		if l.Session == "" {
			continue
		}
		tr.holder, tr.borrowed = l.Session, true
		if at, ok := l.At(); ok && tr.start.IsZero() {
			tr.start = at
		}
	}
	return tr
}

// moves are the track's lines and, between its first line and its end (now,
// while the sub-step is open), the calls its holder made, oldest first. A
// line that took the sub-step is left out: the card's header says who holds
// it and since when.
func (t *ticket) moves(tr subTrack, open bool, now time.Time) []events.Envelope {
	var out []events.Envelope
	for _, l := range tr.own {
		if l.Event != journal.EventClaim && l.Event != journal.EventGateReviewer {
			out = append(out, l)
		}
	}
	if tr.holder == "" || tr.start.IsZero() {
		return out
	}
	end := tr.end
	if open {
		end = now
	}
	for _, l := range t.ledger {
		if l.Event != journal.EventTool || l.Session != tr.holder || l.Command == "" {
			continue
		}
		at, ok := l.At()
		if !ok || !at.After(tr.start) || at.After(end) {
			continue
		}
		out = insertSorted(out, l)
	}
	return out
}

// shownCommand is a command as a move shows it: without the `cd <dir> &&` a
// harness puts before it to run it in a folder, nor the `2>&1` that sends its
// errors where its output goes, which say nothing of what ran.
func shownCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	for strings.HasPrefix(cmd, "cd ") {
		_, rest, ok := strings.Cut(cmd, "&&")
		if !ok {
			break
		}
		cmd = strings.TrimSpace(rest)
	}
	var words []string
	for _, w := range strings.Fields(cmd) {
		if w != "2>&1" {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

// homePattern matches home in the spellings a command may give it: as the
// system writes it, with forward slashes, and as Git Bash names a drive
// (/c/Users/...), in any case, and only as a whole folder name (followed by a
// separator, a quote, a space or the end). Nil for no home.
func homePattern(home string) *regexp.Regexp {
	home = strings.TrimRight(home, `\/`)
	if home == "" {
		return nil
	}
	slash := strings.ReplaceAll(home, `\`, "/")
	forms := []string{regexp.QuoteMeta(home), regexp.QuoteMeta(slash)}
	if len(slash) > 2 && slash[1] == ':' {
		forms = append(forms, regexp.QuoteMeta("/"+slash[:1]+slash[2:]))
	}
	return regexp.MustCompile(`(?i)(` + strings.Join(forms, "|") + `)([\\/"'\s]|$)`)
}

// tildeHome is cmd with the home folder named ~.
func (m *Model) tildeHome(cmd string) string {
	if m.home == nil {
		return cmd
	}
	return m.home.ReplaceAllString(cmd, "~${2}")
}

// moveLine is one move of a card: its time, then what the journal line says,
// or what the holder did in the words it gave the call (the command itself
// when it gave none), with the exit status of a command that failed.
func (m *Model) moveLine(t *ticket, l events.Envelope) string {
	at := m.st.dim(clock(l, m.zone)) + " "
	if l.Src != events.SrcLedger {
		return at + m.st.icon(eventState(l)) + " " + m.say(t, l)
	}
	text := "$ `" + m.tildeHome(shownCommand(l.Command)) + "`"
	if d := strings.TrimSpace(l.Description); d != "" {
		text = m.tildeHome(d)
	}
	if l.Exit != nil && *l.Exit != 0 {
		return at + m.st.state(StateStalled, "✗ "+text+" · exit "+strconv.Itoa(*l.Exit))
	}
	return at + m.st.dim("·") + " " + text
}

// subRole is the role a sub-step's holder plays in it, for naming the session.
func subRole(key string) string {
	switch kind, _, _ := strings.Cut(key, ":"); kind {
	case "slice":
		return roleBuilder
	case "round":
		return roleReviewer
	case "drafted", "slices", "approved":
		return rolePlanner
	}
	return ""
}

// cardFact is one fact of a card, above its moves: a label and the rows that
// say it, wrapped to the card.
type cardFact struct {
	label string
	rows  []string
}

// cardText is how many cells a card leaves the text of a row: the card is the
// panel's inside, and a row is its edges, a space on each side and a label.
func (m *Model) cardText() int { return max(10, m.width-7-11) }

// cardFacts are the rows of a card above its moves, each when known and not
// already said by the sub-step's detail, which heads the card ("claimed by
// builder (...)"): the goal of a slice; the sections of a brief whose slices
// are being written, or its slices once they are; and who holds the sub-step.
func (m *Model) cardFacts(t *ticket, s phase.Sub, tr subTrack) []cardFact {
	var facts []cardFact
	tw := m.cardText()
	kind, id, _ := strings.Cut(s.Key, ":")
	switch kind {
	case "slice":
		if goal := goalText(t.goal(id)); goal != "" {
			facts = append(facts, cardFact{"goal", wrap(goal, tw, goalRows)})
		}
	case "slices":
		if hs := t.headings(); len(hs) > 0 {
			facts = append(facts, cardFact{"brief", wrap(strings.Join(hs, " · "), tw, briefRows)})
		}
	case "approved":
		if rows := m.sliceRows(t, tw); len(rows) > 0 {
			facts = append(facts, cardFact{"slices", rows})
		}
	}
	if tr.holder != "" && (!tr.borrowed || subRole(s.Key) != "") {
		who := m.holderWho(t, tr.holder, subRole(s.Key))
		if kind == "round" {
			n, _ := strconv.Atoi(id)
			who = firstOf(m.reviewerWho(t, tr.holder, n, "", nil), who)
		}
		if who != "" && !strings.Contains(s.Detail, who) {
			facts = append(facts, cardFact{"who", []string{who}})
		}
	}
	return facts
}

// holderWho names the session that holds a sub-step of role: as whoWithAs
// does, and by the role itself when the session has played none on the ticket
// yet (the planner of a brief nobody has approved).
func (m *Model) holderWho(t *ticket, session, role string) string {
	if role != "" && t.firstRole(session) == "" {
		if d := m.sessionDesc(session); d != "" {
			return role + " (" + d + ")"
		}
		return role
	}
	return m.whoWithAs(t, session, role)
}

// sliceRows are the brief's planned slices as rows of a card, each its id and
// goal, briefRows at most, the last one counting the rest when they do not
// all fit.
func (m *Model) sliceRows(t *ticket, tw int) []string {
	var rows []string
	planned := t.planned()
	for i, s := range planned {
		if len(rows) == briefRows-1 && len(planned) > briefRows {
			rows = append(rows, m.st.dim("+"+strconv.Itoa(len(planned)-i)+" more"))
			break
		}
		rows = append(rows, fit(s.ID+m.st.dim(" · ")+goalText(s.Goal), tw))
	}
	return rows
}

// factRows is how many rows facts take.
func factRows(facts []cardFact) int {
	n := 0
	for _, f := range facts {
		n += len(f.rows)
	}
	return n
}

// trimFacts is facts in at most n rows: the last fact gives up rows first, its
// last kept row ending in an ellipsis, and goes when it has one row left.
func trimFacts(facts []cardFact, n int) []cardFact {
	for len(facts) > 0 && factRows(facts) > n {
		last := &facts[len(facts)-1]
		if len(last.rows) <= 1 {
			facts = facts[:len(facts)-1]
			continue
		}
		last.rows = last.rows[:len(last.rows)-1]
		last.rows[len(last.rows)-1] = strings.TrimRight(last.rows[len(last.rows)-1], " …") + " …"
	}
	return facts
}

// cardParts are what the card of s shows: its track, its moves and its facts.
func (m *Model) cardParts(t *ticket, step int, s phase.Sub) (subTrack, []events.Envelope, []cardFact) {
	tr := t.openTrack(step, s.Key)
	return tr, t.moves(tr, true, m.now()), m.cardFacts(t, s, tr)
}

// cardMovesIn is the most moves a card lists in a body of body rows: cardMoves
// on the 80 by 24 screen, and a third of each row a taller one adds, which the
// sub-steps block and the activity share with it.
func cardMovesIn(body int) int { return cardMoves + max(0, body-bodyRef)/3 }

// cardWant is how many rows the card of s would take with room for all its
// moves (up to cardMovesIn): its edges, its facts and its moves. 0 for a card
// with nothing to show, which does not open.
func (m *Model) cardWant(t *ticket, step int, s phase.Sub) int {
	_, moves, facts := m.cardParts(t, step, s)
	if len(moves)+len(facts) == 0 {
		return 0
	}
	return 2 + factRows(facts) + max(1, min(len(moves), cardMovesIn(m.bodyRows())))
}

// card is s opened up in rows lines, the most it may take: a frame in its
// state's color headed by its label, its detail and how long it has run, then
// its facts and its latest moves. Facts give way to moves when rows are few.
func (m *Model) card(t *ticket, step int, s phase.Sub, rows int) []string {
	tr, moves, facts := m.cardParts(t, step, s)
	now := m.now()
	room := max(1, rows-2)
	facts = trimFacts(facts, room-min(max(1, len(moves)), 2))
	keep := min(cardMovesIn(m.bodyRows()), room-factRows(facts))
	if len(moves) > keep {
		moves = moves[len(moves)-keep:]
	}

	state := subState(step, s)
	edge := func(text string) string { return m.st.state(state, text) }
	w := max(20, m.width-7)
	title := m.st.icon(state) + " " + m.st.bold(s.Label)
	if s.Detail != "" {
		title += m.st.dim(" · " + s.Detail)
	}
	right := ""
	if !tr.start.IsZero() && now.After(tr.start) {
		right = " " + m.st.state(state, span(now.Sub(tr.start))) + " "
	}
	if w-4-width(title)-width(right) < 1 {
		title = fit(title, max(1, w-5-width(right)))
	}
	fill := max(1, w-4-width(title)-width(right))
	out := []string{edge("╭ ") + title + " " + edge(strings.Repeat("─", fill)) + right + edge("╮")}
	row := func(label, text string) string {
		return edge("│") + " " + pad(m.st.dim(pad(label, 7))+text, w-4) + " " + edge("│")
	}
	for _, f := range facts {
		for i, r := range f.rows {
			label := ""
			if i == 0 {
				label = f.label
			}
			out = append(out, row(label, r))
		}
	}
	if len(moves) == 0 {
		out = append(out, row("moves", m.st.dim("nothing yet")))
	}
	for i, l := range moves {
		label := ""
		if i == 0 {
			label = "moves"
		}
		out = append(out, row(label, m.moveLine(t, l)))
	}
	out = append(out, edge("╰"+strings.Repeat("─", max(0, w-2))+"╯"))
	return out
}

// subSummary is what a folded sub-step that is done adds to its line: how long
// it took from its first line to its last, when that is known.
func (t *ticket) subSummary(step int, s phase.Sub) string {
	if s.State != phase.SubDone {
		return ""
	}
	tr := t.trackOf(step, s.Key)
	if tr.start.IsZero() || !tr.end.After(tr.start) {
		return ""
	}
	return span(tr.end.Sub(tr.start))
}
