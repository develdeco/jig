package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

// The key table: every key the screens answer is one row of it. The key
// handler runs a row, the hint line lists the rows that can run now, and `?`
// lists every row the screen has, so a key added later is one row and is run,
// hinted and listed at once.
//
// A row says whether it can run through `avail`, and the handler, the hint
// line and `?` ask it the same way. What it can know is what the digest and the
// status already read say: a key whose verb needs the screen to have drawn
// something whole (`a`, `k` and `d` on a question or an ask) can still refuse
// for what the screen drew, in its own words, after `avail` said it can run.

// screenSet is a set of screens.
type screenSet uint16

func onScreens(ks ...screenKind) screenSet {
	var s screenSet
	for _, k := range ks {
		s |= 1 << k
	}
	return s
}

func (s screenSet) has(k screenKind) bool { return s&(1<<k) != 0 }

var (
	// onTicketScreens are the screens of one ticket.
	onTicketScreens = onScreens(screenTicket, screenBrief, screenBuild, screenReview, screenPublish, screenSlice, screenLand)
	// siblingScreens are the screens that stand among others of their kind.
	siblingScreens = onTicketScreens | onScreens(screenChart)
	// onAllScreens is every screen.
	onAllScreens = onTicketScreens | onScreens(screenMain, screenChart)
)

// keyRow is one key of the table.
type keyRow struct {
	// names are the key strings the row answers to; a row with none is a hint
	// only (the person's word at a landing stop).
	names []string
	// label is the key as `?` says it, and words what it does.
	label, words string
	// hintKey and hintText are what the hint line says when they differ from
	// the label and the words: rows that share them are one key family, said
	// once.
	hintKey, hintText string
	// quiet keeps the row off the hint line; `?` still lists it.
	quiet bool
	// screens are the screens the row works on; hinted, when not zero, are the
	// ones whose hint line carries it.
	screens, hinted screenSet
	// act, when set, is the action whose `can` decides the row.
	act action
	// can decides the row when it is not an action: it reads what the screens
	// already hold and runs nothing.
	can func(m *Model, sub subject) (bool, string)
	// say words the row from what the keys act on, over words.
	say func(m *Model, sub subject) string
	// run is what the row does with the key pressed.
	run func(m *Model, key string) tea.Cmd
}

// wordsFor are the row's words for sub.
func (r *keyRow) wordsFor(m *Model, sub subject) string {
	if r.say != nil {
		return r.say(m, sub)
	}
	return r.words
}

// avail says whether the row can run now on sub and, when it cannot, why.
func (r *keyRow) avail(m *Model, sub subject) (bool, string) {
	switch {
	case r.can != nil:
		return r.can(m, sub)
	case r.act != "":
		return m.canOn(sub, r.act)
	}
	return true, ""
}

// needsSubject says whether availability reads the subject: a key that only
// moves the cursor builds no page to find it.
func (r *keyRow) needsSubject() bool { return r.can != nil || r.act != "" }

// listed says whether the screen on top has the row: the screens it works on,
// less what a ticket that does not exist yet or is a multi-task does not have.
func (m *Model) listed(r *keyRow) bool {
	e := m.top()
	if !r.screens.has(e.screen) {
		return false
	}
	if e.screen != screenTicket {
		return true
	}
	t := m.w.tickets[e.ticket]
	switch {
	case t != nil && t.awaiting:
		// Nothing to move on or act on until the ticket exists.
		return slices.Contains([]string{"esc", "q", "?", "1-5", "h", "t", "r"}, r.label)
	case isMulti(t):
		// A multi-task's tasks open; nothing else is done from its screen.
		return slices.Contains([]string{"esc", "q", "?", "↑↓", "pgup pgdn", "home end", "enter", "1-5", "[", "]", "h", "c", "o", "t", "r"}, r.label)
	}
	return true
}

// rowsOn are the rows the screen on top has, in the order the hint line and `?`
// list them.
func (m *Model) rowsOn() []*keyRow {
	var out []*keyRow
	for i := range m.table {
		if r := &m.table[i]; m.listed(r) {
			out = append(out, r)
		}
	}
	return out
}

// keyHints are the keys that can run on the screen on top, as key and meaning
// pairs: the rows its hint line carries that avail lets run now, a family once.
// The key back and the key quit come first, so a line that wraps never strands
// them.
func (m *Model) keyHints() []string {
	sub := m.keysOn
	var flat []string
	seen := map[string]bool{}
	for _, r := range m.rowsOn() {
		if r.quiet || (r.hinted != 0 && !r.hinted.has(m.top().screen)) {
			continue
		}
		if ok, _ := r.avail(m, sub); !ok {
			continue
		}
		key, text := r.label, r.wordsFor(m, sub)
		if r.hintKey != "" {
			key, text = r.hintKey, r.hintText
		}
		if seen[key+" "+text] {
			continue
		}
		seen[key+" "+text] = true
		flat = append(flat, key, text)
	}
	return flat
}

// canOpen is the availability of enter: the row under the cursor opens a screen
// other than the one on top.
func canOpen(m *Model, sub subject) (bool, string) {
	if sub.hasRow {
		if _, ok := m.openTarget(sub.row); ok {
			return true, ""
		}
	}
	return false, "nothing under the cursor opens another screen"
}

// keyRows is the table. Its order is the hint line's and `?`'s.
func keyRows() []keyRow {
	moves := onScreens(screenMain, screenChart, screenTicket, screenBrief, screenBuild, screenReview, screenSlice)
	scrolls := onScreens(screenPublish, screenLand)
	step := func(delta int) func(*Model, string) tea.Cmd {
		return func(m *Model, _ string) tea.Cmd { m.move(delta); return nil }
	}
	return []keyRow{
		{names: []string{"esc", "left", "backspace"}, label: "esc", words: "back", screens: onAllScreens &^ onScreens(screenMain),
			run: func(m *Model, _ string) tea.Cmd { m.back(); return nil }},
		{names: []string{"q"}, label: "q", words: "quit", screens: onAllScreens,
			run: func(m *Model, _ string) tea.Cmd { return m.quit() }},
		{names: []string{"?"}, label: "?", words: "keys", screens: onAllScreens,
			run: func(m *Model, _ string) tea.Cmd { m.help = &helpView{}; return nil }},
		{names: []string{"up", "down"}, label: "↑↓", words: "move", screens: moves,
			run: func(m *Model, key string) tea.Cmd {
				if key == "up" {
					return step(-1)(m, key)
				}
				return step(1)(m, key)
			}},
		{names: []string{"up", "down"}, label: "↑↓", words: "scroll", screens: scrolls,
			run: func(m *Model, key string) tea.Cmd {
				if key == "up" {
					return step(-1)(m, key)
				}
				return step(1)(m, key)
			}},
		{names: []string{"pgup", "pgdown"}, label: "pgup pgdn", words: "page", quiet: true, screens: onAllScreens,
			run: func(m *Model, key string) tea.Cmd {
				if key == "pgup" {
					return step(-5)(m, key)
				}
				return step(5)(m, key)
			}},
		{names: []string{"home", "end"}, label: "home end", words: "first, last", quiet: true, screens: onAllScreens,
			run: func(m *Model, key string) tea.Cmd {
				if key == "home" {
					return step(-1<<20)(m, key)
				}
				return step(1<<20)(m, key)
			}},
		{names: []string{"enter", "right"}, label: "enter", words: "open", screens: moves &^ onScreens(screenChart), hinted: onScreens(screenMain, screenTicket, screenBrief, screenBuild),
			can: canOpen, run: func(m *Model, _ string) tea.Cmd { return m.open() }},
		{names: []string{"enter", "right"}, label: "enter", words: "open ticket", screens: onScreens(screenChart),
			can: canOpen, run: func(m *Model, _ string) tea.Cmd { return m.open() }},
		{names: []string{"a"}, label: "a", words: "answer", act: actAnswer, screens: onScreens(screenMain, screenChart, screenTicket, screenSlice),
			run: func(m *Model, _ string) tea.Cmd { m.beginAnswer(); return nil }},
		{names: []string{"k"}, label: "k", words: "keep", act: actKeep, screens: onScreens(screenTicket, screenReview),
			run: func(m *Model, _ string) tea.Cmd { return m.triage("keep") }},
		{names: []string{"d"}, label: "d", words: "dismiss", act: actDismiss, screens: onScreens(screenTicket, screenReview),
			run: func(m *Model, _ string) tea.Cmd { return m.triage("dismiss") }},
		{names: []string{"u"}, label: "u", words: "requeue", act: actRequeue, screens: onScreens(screenTicket, screenBuild, screenSlice),
			run: func(m *Model, _ string) tea.Cmd { return m.requeue() }},
		{names: []string{"v"}, label: "v", words: "approve", act: actApprove, screens: onScreens(screenTicket, screenBrief),
			run: func(m *Model, _ string) tea.Cmd { m.askApprove(); return nil }},
		{names: []string{"g"}, label: "g", words: "gate", act: actGate, screens: onScreens(screenMain, screenChart, screenTicket, screenBuild, screenReview),
			run: func(m *Model, _ string) tea.Cmd { return m.gate() }},
		{names: []string{"p"}, label: "p", words: "publish", act: actPublish, screens: onScreens(screenMain, screenChart, screenTicket, screenPublish),
			run: func(m *Model, _ string) tea.Cmd { return m.beginPublish() }},
		{label: "say", act: actLandWord, screens: onAllScreens,
			say: func(m *Model, sub subject) string { return `"Land ` + sub.ticket + `"` },
			can: func(m *Model, sub subject) (bool, string) {
				ok, _ := m.canOn(sub, actLandWord)
				return ok, ""
			}},
		{names: []string{"1", "2", "3", "4", "5"}, label: "1-5", words: "steps", screens: onTicketScreens,
			can: func(m *Model, _ subject) (bool, string) {
				if why := m.stepsReason(); why != "" {
					return false, why
				}
				return true, ""
			},
			run: func(m *Model, key string) tea.Cmd { return m.openStep(int(key[0] - '0')) }},
		{names: []string{"["}, label: "[", words: "previous sibling", hintKey: "[ ]", hintText: "sibling", screens: siblingScreens,
			say: func(m *Model, _ subject) string { return "previous " + m.siblingNoun() },
			can: func(m *Model, _ subject) (bool, string) { return m.canSibling(-1) },
			run: func(m *Model, _ string) tea.Cmd { return m.openSibling(-1) }},
		{names: []string{"]"}, label: "]", words: "next sibling", hintKey: "[ ]", hintText: "sibling", screens: siblingScreens,
			say: func(m *Model, _ subject) string { return "next " + m.siblingNoun() },
			can: func(m *Model, _ subject) (bool, string) { return m.canSibling(1) },
			run: func(m *Model, _ string) tea.Cmd { return m.openSibling(1) }},
		{names: []string{"h"}, label: "h", words: "main", screens: onAllScreens &^ onScreens(screenMain), hinted: onTicketScreens,
			run: func(m *Model, _ string) tea.Cmd { return m.openMain() }},
		{names: []string{"c"}, label: "c", words: "chart", screens: onTicketScreens,
			say: func(m *Model, _ subject) string {
				switch up := m.upFrom(); {
				case up != "" && m.multiParent(up) != "":
					return "multi-task"
				case up != "" && len(m.issuesOf(up)) > 0:
					return "issue"
				}
				return "chart"
			},
			can: func(m *Model, _ subject) (bool, string) {
				up := m.upFrom()
				switch {
				case up == "":
					return false, "this ticket does not exist yet"
				case m.multiParent(up) != "" || len(m.issuesOf(up)) > 0 || len(m.chartsOf(up)) > 0:
					return true, ""
				}
				return false, up + " is in no chart"
			},
			run: func(m *Model, _ string) tea.Cmd { return m.openIssue() }},
		{names: []string{"o"}, label: "o", words: "open PRs", screens: onAllScreens,
			can: func(m *Model, sub subject) (bool, string) {
				_, why := m.prsOf(sub)
				return why == "", why
			},
			run: func(m *Model, _ string) tea.Cmd { return m.openPRs(m.keysOn) }},
		{names: []string{"m"}, label: "m", words: "map", screens: onScreens(screenChart),
			can: func(m *Model, _ subject) (bool, string) {
				if c := m.w.charts[m.top().chart]; c != nil && c.issueID() != "" {
					return true, ""
				}
				return false, "this chart plans no issue, so it has no map"
			},
			run: func(m *Model, _ string) tea.Cmd { m.toggleMap(); return nil }},
		{names: []string{"t"}, label: "t", words: "tail", screens: onAllScreens,
			run: func(m *Model, _ string) tea.Cmd { m.tail = !m.tail; return nil }},
		{names: []string{"r"}, label: "r", words: "refresh", screens: onAllScreens, hinted: onScreens(screenMain, screenChart),
			run: func(m *Model, _ string) tea.Cmd { return m.refreshAll() }},
	}
}

// runKey runs the row the key names on the screen on top. A row that can not
// run puts its reason in the notice and runs nothing; an action key the screen
// does not have says why it cannot run, else that it is not done from here, and
// any other key the screen does not have does nothing.
func (m *Model) runKey(key string) tea.Cmd {
	var elsewhere *keyRow
	for i := range m.table {
		r := &m.table[i]
		if !slices.Contains(r.names, key) {
			continue
		}
		if !r.screens.has(m.top().screen) {
			elsewhere = cmpRow(elsewhere, r)
			continue
		}
		if r.act != "" && !m.listed(r) {
			// A ticket that does not exist yet, or a multi-task, has no action keys.
			return m.refuseAction(r)
		}
		if r.needsSubject() {
			sub := m.subjectNow()
			m.keysOn = sub
			if ok, why := r.avail(m, sub); !ok {
				m.notice = why
				return nil
			}
		}
		return r.run(m, key)
	}
	if elsewhere != nil && elsewhere.act != "" {
		return m.refuseAction(elsewhere)
	}
	return nil
}

// cmpRow keeps the first row of a key.
func cmpRow(a, b *keyRow) *keyRow {
	if a != nil {
		return a
	}
	return b
}

// refuseAction says why an action key the screen does not have cannot run, else
// that the screen does not do it.
func (m *Model) refuseAction(r *keyRow) tea.Cmd {
	m.keysOn = m.subjectNow()
	if ok, why := r.avail(m, m.keysOn); !ok {
		m.notice = why
	} else {
		m.notice = string(r.act) + " is not done from this screen"
	}
	return nil
}

// helpView is the `?` overlay: it draws over the screen's body and holds the
// line it is scrolled to.
type helpView struct{ scroll int }

// helpKey takes a key while `?` is up: it scrolls, closes, or quits, and
// nothing else acts.
func (m *Model) helpKey(k tea.KeyMsg) tea.Cmd {
	h := m.help
	switch k.String() {
	case "?", "esc":
		m.help = nil
	case "q":
		return m.quit()
	case "up":
		h.scroll--
	case "down":
		h.scroll++
	case "pgup":
		h.scroll -= 5
	case "pgdown":
		h.scroll += 5
	case "home":
		h.scroll = 0
	case "end":
		h.scroll = 1 << 20
	}
	m.clampHelp()
	return nil
}

// clampHelp keeps the overlay's scroll inside its lines.
func (m *Model) clampHelp() {
	if h := m.help; h != nil {
		h.scroll = max(0, min(h.scroll, len(m.helpLines(m.width))-m.bodyRows()))
	}
}

// helpLines are the overlay's lines at width w: a title, then one row per key
// the screen has with its names, its words and now or not now with the reason,
// the reason wrapped under its own column.
func (m *Model) helpLines(w int) []string {
	sub := m.keysOn
	rows := m.rowsOn()
	keyW, wordW := 3, 3
	for _, r := range rows {
		if len(r.names) > 0 {
			keyW = max(keyW, width(r.label))
			wordW = max(wordW, width(r.wordsFor(m, sub)))
		}
	}
	out := []string{"  " + m.st.bold("Keys on this screen") + m.st.dim("  ? or esc closes this")}
	lead := 2 + keyW + 2 + wordW + 2
	for _, r := range rows {
		if len(r.names) == 0 {
			continue
		}
		state := []string{m.st.state(StateOK, "now")}
		if ok, why := r.avail(m, sub); !ok {
			text := "not now"
			if why != "" {
				text += ": " + why
			}
			state = nil
			for _, l := range wrapWhole(text, max(10, w-lead)) {
				state = append(state, m.st.dim(l))
			}
		}
		words := r.wordsFor(m, sub)
		out = append(out, fit("  "+m.st.key(r.label)+spaces(keyW-width(r.label))+"  "+words+spaces(wordW-width(words))+"  "+state[0], w))
		for _, l := range state[1:] {
			out = append(out, fit(spaces(lead)+l, w))
		}
	}
	return out
}

// helpBody is the overlay's h rows of the body, scrolled.
func (m *Model) helpBody(h, w int) []string {
	lines := m.helpLines(w)
	start := max(0, min(m.help.scroll, len(lines)-h))
	m.help.scroll = start
	return append([]string(nil), lines[start:min(len(lines), start+h)]...)
}
