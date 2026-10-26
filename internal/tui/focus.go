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
// which are its own journal lines and the commands the session that holds it
// ran meanwhile (the machine ledger's tool lines). A block too short for the
// card keeps the one-line form.

const (
	// cardMoves is the most moves a card lists.
	cardMoves = 8
	// cardMin is the fewest rows a card takes: its two edges, one fact and one
	// move.
	cardMin = 4
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
	// not named as the sub-step's holder.
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
// of its own yet (a pull request being opened once its body is written) is held
// by the session that wrote the ticket's latest line naming one, since the
// ticket's latest line: what that session has run since are its moves.
func (t *ticket) openTrack(step int, key string) subTrack {
	tr := t.trackOf(step, key)
	if len(tr.own) > 0 {
		return tr
	}
	for i := len(t.lines) - 1; i >= 0; i-- {
		l := t.lines[i]
		if at, ok := l.At(); ok && tr.start.IsZero() {
			tr.start = at
		}
		if l.Session != "" {
			tr.holder, tr.borrowed = l.Session, true
			break
		}
	}
	return tr
}

// moves are the track's lines and, between its first line and its end (now,
// while the sub-step is open), the commands its holder ran, oldest first.
func (t *ticket) moves(tr subTrack, open bool, now time.Time) []events.Envelope {
	out := append([]events.Envelope(nil), tr.own...)
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
// or the command the holder ran with its exit status when it failed.
func (m *Model) moveLine(t *ticket, l events.Envelope) string {
	at := m.st.dim(clock(l, m.zone)) + " "
	if l.Src != events.SrcLedger {
		text := m.say(t, l)
		if l.Event == journal.EventClaim {
			// The card's goal row says what a claim's line adds after it.
			text = firstOf(m.whoAs(t, l.Session, roleOf(l.Event)), "a session") + " took it"
		}
		return at + m.st.icon(eventState(l)) + " " + text
	}
	cmd := m.tildeHome(shownCommand(l.Command))
	text := m.st.dim("$") + " ran `" + cmd + "`"
	if l.Exit != nil && *l.Exit != 0 {
		text = m.st.state(StateStalled, "$") + " ran `" + cmd + "`" + m.st.state(StateStalled, " · exit "+strconv.Itoa(*l.Exit))
	}
	return at + text
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

// cardFacts are the rows of a card above its moves: the goal of a slice and
// who holds the sub-step, each when known and not already said by the
// sub-step's detail, which heads the card ("claimed by builder (...)").
func (m *Model) cardFacts(t *ticket, s phase.Sub, tr subTrack) [][2]string {
	var facts [][2]string
	kind, id, _ := strings.Cut(s.Key, ":")
	if kind == "slice" {
		if goal := t.goals()[id]; goal != "" {
			facts = append(facts, [2]string{"goal", goal})
		}
	}
	if tr.holder != "" && !tr.borrowed {
		who := m.whoWithAs(t, tr.holder, subRole(s.Key))
		if kind == "round" {
			n, _ := strconv.Atoi(id)
			who = firstOf(m.reviewerWho(t, tr.holder, n, "", nil), who)
		}
		if who != "" && !strings.Contains(s.Detail, who) {
			facts = append(facts, [2]string{"who", who})
		}
	}
	return facts
}

// cardParts are what the card of s shows: its track, its moves and its facts.
func (m *Model) cardParts(t *ticket, step int, s phase.Sub) (subTrack, []events.Envelope, [][2]string) {
	tr := t.openTrack(step, s.Key)
	return tr, t.moves(tr, true, m.now()), m.cardFacts(t, s, tr)
}

// cardWant is how many rows the card of s would take with room for all its
// moves (up to cardMoves): its edges, its facts and its moves. 0 for a card
// with nothing to show, which does not open.
func (m *Model) cardWant(t *ticket, step int, s phase.Sub) int {
	_, moves, facts := m.cardParts(t, step, s)
	if len(moves)+len(facts) == 0 {
		return 0
	}
	return 2 + len(facts) + max(1, min(len(moves), cardMoves))
}

// card is s opened up in rows lines, the most it may take: a frame in its
// state's color headed by its label, its detail and how long it has run, then
// its facts and its latest moves. Facts give way to moves when rows are few.
func (m *Model) card(t *ticket, step int, s phase.Sub, rows int) []string {
	tr, moves, facts := m.cardParts(t, step, s)
	now := m.now()
	room := max(1, rows-2)
	for len(facts) > 0 && len(facts)+min(max(1, len(moves)), 2) > room {
		facts = facts[:len(facts)-1]
	}
	keep := min(cardMoves, room-len(facts))
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
	fill := w - 4 - width(title) - width(right)
	if fill < 1 {
		right, fill = "", max(1, w-4-width(title))
	}
	out := []string{edge("╭ ") + title + " " + edge(strings.Repeat("─", fill)) + right + edge("╮")}
	row := func(label, text string) string { return edge("│") + " " + m.st.dim(pad(label, 6)) + text }
	for _, f := range facts {
		out = append(out, row(f[0], f[1]))
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
