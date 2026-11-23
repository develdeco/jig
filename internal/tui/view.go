package tui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/events"
)

// The kinds of row a cursor can stand on.
const (
	rowChart    = "chart"
	rowTicket   = "ticket"
	rowBrief    = "brief"
	rowBuild    = "build"
	rowReview   = "review"
	rowPublish  = "publish"
	rowLand     = "land"
	rowQuestion = "question"
	rowAsk      = "ask"
	rowSlice    = "slice"
	// rowStop is a line the cursor can rest on and that opens nothing: the head
	// of a record on a screen whose page is longer than its window, so the
	// window can be moved to it.
	rowStop = "stop"
)

// rowRef says what a selectable line is: its kind, its id, and the ticket and
// slice it belongs to.
type rowRef struct {
	kind   string
	id     string
	ticket string
	slice  string
}

// pline is one line of a screen's body, with what it is when the cursor can
// stand on it, and its place in a panel (frame.go): box, and for a panel's
// top edge its title in text and right. A line of an ask's text carries the
// ask's id, so View can record whether it drew the ask whole (drawnAsks).
type pline struct {
	text  string
	ref   *rowRef
	box   int
	right string
	ask   string
}

// page is a screen's body before it is windowed and framed.
type page struct {
	lines []pline
}

func (p *page) add(text string) { p.lines = append(p.lines, pline{text: text}) }

func (p *page) addRow(ref rowRef, text string) {
	p.lines = append(p.lines, pline{text: text, ref: &ref})
}

// refs are the selectable rows, top to bottom.
func (p page) refs() []rowRef {
	var out []rowRef
	for _, l := range p.lines {
		if l.ref != nil {
			out = append(out, *l.ref)
		}
	}
	return out
}

// page builds the body of the screen on top.
func (m *Model) page() page {
	m.pageBuilds++
	e := m.top()
	switch e.screen {
	case screenTicket:
		return m.ticketPage(e.ticket)
	case screenBuild:
		return m.buildPage(e.ticket)
	case screenReview:
		return m.reviewPage(e.ticket)
	case screenSlice:
		return m.slicePage(e.ticket, e.slice)
	case screenBrief:
		return m.briefPage(e.ticket)
	case screenPublish:
		return m.publishPage(e.ticket)
	case screenLand:
		return m.landPage(e.ticket)
	case screenChart:
		return m.chartPage(e.chart)
	}
	return m.mainPage()
}

// minBodyRows is the rows a screen's body keeps whatever the panel and the
// footer ask for, and headerRows the rows over it: the title and its rule.
const (
	minBodyRows = 6
	headerRows  = 2
)

// View draws the screen: a header, the body windowed so the cursor shows,
// what the last command printed, and the keys, in exactly the height the
// terminal has.
func (m *Model) View() string {
	w := m.width
	m.followSel()
	l := m.layout()

	var tailPane []string
	if l.tailH > 0 {
		tailPane = append([]string{"  " + m.st.dim("tail "+strings.Repeat("─", max(0, w-7)))}, m.tailLines(w, l.tailH)...)
	}

	start := m.windowStart(l.pg, l.sel, l.pageH, true)
	body := m.window(l.pg, l.sel, start, l.pageH, w)
	m.drawn = drawnAsks(l.pg, start, l.pageH)
	for len(body) < l.pageH {
		body = append(body, "")
	}
	out := append(append(append(append(l.head, body...), tailPane...), l.panel...), l.foot...)
	for i, line := range out {
		out[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(out, "\n")
}

// screenLayout is how the screen on top is laid out at the size the terminal
// has: its page and the row the cursor stands on, the rows around the body, and
// how many rows of the body the page has and the tail takes.
type screenLayout struct {
	pg                page
	sel               int
	head, foot, panel []string
	pageH, tailH      int
}

// layout lays the screen on top out as View draws it.
func (m *Model) layout() screenLayout {
	w, h := m.width, m.height
	pg := m.page()
	if sub := m.subjectOf(pg); sub != m.keysOn {
		// The page was sized by a hint line cut for another subject: it is
		// built again, once, for this one.
		m.keysOn = sub
		pg = m.page()
	}
	refs := pg.refs()
	sel := m.top().sel
	if sel >= len(refs) {
		sel = len(refs) - 1
	}
	if sel < 0 {
		sel = 0
	}

	head := m.header(w)
	foot := m.footer(w)
	panel := m.panel(w)
	bodyH := h - len(head) - len(foot) - len(panel)
	if bodyH < minBodyRows && len(panel) > 0 {
		// The last command goes first: the notice says what to do.
		panel = panel[min(len(panel), minBodyRows-bodyH):]
		bodyH = h - len(head) - len(foot) - len(panel)
	}
	if bodyH < 1 {
		bodyH = 1
	}

	l := screenLayout{pg: pg, sel: sel, head: head, foot: foot, panel: panel, pageH: bodyH}
	if m.tail && bodyH >= 8 {
		l.tailH = bodyH / 2
		l.pageH = bodyH - l.tailH - 1
	}
	return l
}

// What View drew of an ask: some of its lines, or all of them. An ask the page
// has no line of is not in the record.
const (
	askCut = iota + 1
	askWhole
)

// drawnAsks is what the window of h rows from start draws of each ask the page
// has lines of. View records it as it draws, so a key acts on the screen the
// person read, notice and tail and all, and the one rule for acting on what a
// person must read first names no screen.
func drawnAsks(pg page, start, h int) map[string]int {
	total, shown := map[string]int{}, map[string]int{}
	for i, line := range pg.lines {
		if line.ask == "" {
			continue
		}
		total[line.ask]++
		if i >= start && i < start+h {
			shown[line.ask]++
		}
	}
	out := make(map[string]int, len(total))
	for id, n := range total {
		out[id] = askCut
		if shown[id] == n {
			out[id] = askWhole
		}
	}
	return out
}

// bodyRows is how many rows the screen's body has: the height less the
// header, the keys and what the last command printed, and less the tail when
// it is open.
func (m *Model) bodyRows() int {
	h := m.height - 2 - len(m.footer(m.width)) - len(m.panel(m.width))
	if m.tail && h >= 8 {
		h = h - h/2 - 1
	}
	return h
}

// windowStart is the first page line a window of h rows shows with the sel-th
// selectable row in view, then moved up by what the page is held above it
// (lead). With clamp, lead is cut to the lines there are above the window, so a
// page opened at its top lessens what is held as it is read down.
func (m *Model) windowStart(pg page, sel, h int, clamp bool) int {
	selLine, n := 0, 0
	for i, l := range pg.lines {
		if l.ref == nil {
			continue
		}
		if n == sel {
			selLine = i
		}
		n++
	}
	start := 0
	if len(pg.lines) > h {
		start = selLine - h/2
		start = max(0, min(start, len(pg.lines)-h))
		if selLine < h {
			// A row in the first screenful leaves the page where it begins.
			start = 0
		}
		if n > 0 && sel == n-1 {
			// On the last row, show as much of what follows it as fits
			// with the row still in view: a panel of activity is below.
			start = max(0, min(selLine, len(pg.lines)-h))
		}
		if n == 0 {
			// A page with no row to stand on is moved by its own offset.
			start = max(0, min(m.top().scroll, len(pg.lines)-h))
		}
	}
	if n > 0 {
		lead := m.top().lead
		if clamp {
			lead = min(lead, start)
			m.top().lead = lead
		}
		start = max(0, start-lead)
	}
	return start
}

// window renders the page lines from start that fit in h rows, marking the
// sel-th selectable row.
func (m *Model) window(pg page, sel, start, h, w int) []string {
	end := min(len(pg.lines), start+h)
	out := make([]string, 0, end-start)
	idx := 0
	for i, l := range pg.lines {
		marker := "  "
		if l.ref != nil {
			if idx == sel {
				marker = m.st.key("▸") + " "
			}
			idx++
		}
		if i < start || i >= end {
			continue
		}
		if l.box != boxNone {
			out = append(out, fit(m.frameLine(l, marker, w), w))
			continue
		}
		out = append(out, fit(marker+l.text, w))
	}
	return out
}

// header is the title line and the rule under it.
func (m *Model) header(w int) []string {
	e := m.top()
	crumbs := m.st.bold("jig")
	if m.cfg.Project != "" {
		crumbs += "  " + m.cfg.Project
	}
	var right, title, idCrumb string
	switch e.screen {
	case screenMain:
		n := len(m.w.order)
		right = strconv.Itoa(n) + " " + plural(n, "ticket", "tickets")
		if nc := len(m.w.chartOrder); nc > 0 {
			right += " · " + strconv.Itoa(nc) + " " + plural(nc, "chart", "charts")
		}
		right = m.st.dim(right)
	case screenChart:
		crumbs += " › " + m.st.bold(e.chart)
		if c := m.w.charts[e.chart]; c != nil {
			if title := c.title(); title != "" && title != e.chart {
				crumbs += "  " + title
			}
			state, label := m.chartStatus(c, m.chartRows(c))
			right = m.st.icon(state) + " " + m.st.state(state, label)
		}
	default:
		t := m.w.tickets[e.ticket]
		crumbs += " › " + m.st.bold(e.ticket)
		idCrumb = crumbs
		if t != nil && t.title != "" {
			title = t.title
		}
		if t != nil && e.screen == screenTicket {
			if k := m.kindIn(t); k != "" {
				crumbs += "  " + m.st.dim(k)
			}
		}
		switch e.screen {
		case screenBrief:
			crumbs += " › 1 brief"
		case screenBuild:
			crumbs += " › 2 build"
		case screenReview:
			crumbs += " › 3 review"
		case screenPublish:
			crumbs += " › 4 publish"
		case screenLand:
			crumbs += " › 5 land"
		case screenSlice:
			crumbs += " › 2 build › slice " + m.st.bold(e.slice)
		}
		if t != nil {
			state, label := m.stepLabel(t)
			if isMulti(t) {
				// A multi-task has no phase: its word is where its own pull request is.
				state, label = m.multiLabel(t)
			}
			if t.awaiting {
				state, label = StateQueued, "waiting"
			}
			if e.screen == screenSlice {
				for _, s := range t.slices(m.zone) {
					if s.ID == e.slice {
						state, label = s.State, s.State
					}
				}
			}
			right = m.st.icon(state) + " " + m.st.state(state, label)
		}
	}
	if title != "" {
		// The title gives way first: it takes what the id, the step and the
		// state leave, whole or cut, and none when nothing is left.
		if room := w - width(crumbs) - width(right) - 3; room > 1 {
			crumbs = idCrumb + "  " + fit(title, room) + strings.TrimPrefix(crumbs, idCrumb)
		}
	}
	gap := w - width(crumbs) - width(right)
	if gap < 1 {
		crumbs = fit(crumbs, max(1, w-width(right)-1))
		gap = max(1, w-width(crumbs)-width(right))
	}
	return []string{crumbs + spaces(gap) + right, m.st.dim(strings.Repeat("─", w))}
}

// footer is the rule, then the line being typed, the question being asked, or
// the keys that do something here.
func (m *Model) footer(w int) []string {
	rule := m.st.dim(strings.Repeat("─", w))
	switch {
	case m.input != nil:
		out := []string{rule}
		out = append(out, fit("  "+m.st.dim(m.input.head), w))
		for _, l := range wrapWhole(m.input.about, w-2) {
			out = append(out, fit("  "+m.st.dim(l), w))
		}
		out = append(out, fit("  "+m.st.key(m.input.prompt+" ›")+" "+string(m.input.value)+m.st.dim("▏"), w))
		return append(out, m.hintLines(w, "enter", "send", "esc", "cancel")...)
	case m.ask != nil:
		out := []string{rule, fit("  "+m.st.state(StateFindings, m.ask.prompt), w)}
		return append(out, m.hintLines(w, "y", "yes", "any other key", "no")...)
	}
	return append([]string{rule}, m.hintLines(w, m.keyHints()...)...)
}

// hintLines lays key/meaning pairs out in as many lines as w cells need.
func (m *Model) hintLines(w int, pairs ...string) []string {
	sep := m.st.dim(" · ")
	var lines []string
	cur := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		hint := m.st.key(pairs[i]) + " " + m.st.dim(pairs[i+1])
		if cur != "" && width(cur)+width(sep)+width(hint) > w-2 {
			lines = append(lines, "  "+cur)
			cur = ""
		}
		if cur != "" {
			cur += sep
		}
		cur += hint
	}
	if cur != "" {
		lines = append(lines, "  "+cur)
	}
	return lines
}

// hint is a key on the hint line: the key, its meaning, and the action that
// decides whether it is listed (none for a key that is always there).
type hint struct {
	key, text string
	act       action
}

// keyHints are the keys that do something on the screen on top, as key and
// meaning pairs: the screen's keys, and of those the actions only the ones can
// lets run now. The key back and the key quit come first, so a line that wraps
// never strands them.
func (m *Model) keyHints() []string {
	sub := m.keysOn
	keys := m.screenKeys(sub)
	// The key up to the issue, on the screens of a task that is in one's plan, and
	// the one that shows the map, on the plan: each goes after the key quit, on
	// a screen that has a key back.
	var extra *hint
	switch up := m.upFrom(); {
	case up != "" && m.multiParent(up) != "":
		extra = &hint{key: "c", text: "multi-task"}
	case up != "" && len(m.issuesOf(up)) > 0:
		extra = &hint{key: "c", text: "issue"}
	case m.top().screen == screenChart:
		if c := m.w.charts[m.top().chart]; c != nil && c.issueID() != "" {
			extra = &hint{key: "m", text: "map"}
		}
	}
	flat := make([]string, 0, 2*len(keys)+2)
	for _, k := range keys {
		if k.act != "" {
			if ok, _ := m.canOn(sub, k.act); !ok {
				continue
			}
		}
		flat = append(flat, k.key, k.text)
		if k.key == "q" && extra != nil && keys[0].key == "esc" {
			flat = append(flat, extra.key, extra.text)
		}
	}
	return flat
}

// screenKeys are the keys the screen on top has, in the order they are listed,
// whether or not their action can run: the hint line lists the ones can lets
// run, and a key the screen does not have runs nothing.
func (m *Model) screenKeys(sub subject) []hint {
	id := sub.ticket
	move := hint{key: "↑↓", text: "move"}
	open := hint{key: "enter", text: "open"}
	back := hint{key: "esc", text: "back"}
	answer := hint{"a", "answer", actAnswer}
	keep := hint{"k", "keep", actKeep}
	dismiss := hint{"d", "dismiss", actDismiss}
	gate := hint{"g", "gate", actGate}
	publish := hint{"p", "publish", actPublish}
	requeue := hint{"u", "requeue", actRequeue}
	approve := hint{"v", "approve", actApprove}
	say := hint{"say", `"Land ` + id + `"`, actLandWord}
	land := hint{key: "5", text: "land"}
	tail := hint{key: "t", text: "tail"}
	quit := hint{key: "q", text: "quit"}
	var keys []hint
	switch m.top().screen {
	case screenMain:
		keys = []hint{quit, move, open, gate, publish, answer, say, tail, {key: "r", text: "refresh"}}
	case screenChart:
		keys = []hint{back, quit, move, {key: "enter", text: "open ticket"}, gate, publish, answer, say, tail, {key: "r", text: "refresh"}}
	case screenTicket:
		keys = []hint{back, quit, move, open, answer, keep, dismiss, requeue, approve, gate, publish, say, land, tail}
		if t := m.w.tickets[m.top().ticket]; t != nil && t.awaiting {
			// Nothing to move on or act on until the ticket exists.
			keys = []hint{back, quit, tail}
		} else if isMulti(t) {
			// A multi-task's tasks open; nothing else is done from its screen.
			keys = []hint{back, quit, move, open, tail}
		}
	case screenBrief:
		keys = []hint{back, quit, move, open, approve, say, land, tail}
	case screenBuild:
		keys = []hint{back, quit, move, open, requeue, gate, say, land, tail}
	case screenReview:
		keys = []hint{back, quit, move, keep, dismiss, gate, say, land, tail}
	case screenPublish:
		keys = []hint{back, quit, {key: "↑↓", text: "scroll"}, publish, say, land, tail}
	case screenSlice:
		keys = []hint{back, quit, move, answer, requeue, say, land, tail}
	case screenLand:
		keys = []hint{back, quit, {key: "↑↓", text: "scroll"}, say, tail}
	}
	return keys
}

// panel is the last command as one line (whole, for a verb that leaves the next
// move to someone else), and the notice, above the keys.
func (m *Model) panel(w int) []string {
	var out []string
	if m.last != nil {
		out = append(out, m.lastLines(m.last, w)...)
	}
	if m.notice != "" {
		// Drawn whole, in the rows it needs: what it tells the person is the
		// point of it, and a cut notice loses its end.
		for _, l := range wrapWhole(m.notice, w-2) {
			out = append(out, fit("  "+m.st.state(StateFindings, l), w))
		}
	}
	return out
}

// helpWrapRows is the most rows a verb's help line takes in the panel when it is
// shown whole: a sentence of the verb's own is never this long.
const helpWrapRows = 6

// lastLines are the panel's rows for a key's command: the one line, and for a
// verb that leaves the next move to someone else the help line it printed, whole
// and wrapped, beneath the command. The words are the verb's own; the TUI adds
// none. They are part of the line and expire with it.
func (m *Model) lastLines(o *outcome, w int) []string {
	if s := o.shown(); o.leavesTheMove() && s.Code == 0 {
		if help, ok := firstHelp(s.Out); ok {
			out := []string{fit("  "+m.st.icon(StateOK)+" "+m.st.bold(m.shownLine(s.Args)), w)}
			for _, l := range wrap(help, w-4, helpWrapRows) {
				out = append(out, "    "+m.st.dim(l))
			}
			return out
		}
	}
	return []string{m.lastLine(o, w)}
}

// firstHelp is the first line of the help a verb printed, false when it printed
// none.
func firstHelp(out string) (string, bool) {
	lines := outputLines(out)
	for i, l := range lines {
		if strings.HasPrefix(l, "help[") && i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1]), true
		}
	}
	return "", false
}

// lastLine is a key's command as typed and what the verb said of it: its own
// first sentence when it worked, its error and code when it did not. The step
// that failed is the one shown, so a chain that stopped reads as the failure.
// The raw output is not printed, and a line too long for the screen is cut.
func (m *Model) lastLine(o *outcome, w int) string {
	s := o.shown()
	state, said := StateOK, firstSentence(s.Out)
	if s.Code != 0 {
		state, said = StateFail, errorSentence(s.Out)
	}
	line := "  " + m.st.icon(state) + " " + m.st.bold(m.shownLine(s.Args))
	if said != "" {
		if state == StateFail {
			said = m.st.state(StateFail, said)
		} else {
			said = m.st.dim(said)
		}
		line += "  " + said
	}
	return fit(line, w)
}

// firstSentence is what a verb that worked says of it: the first line of its
// help, which is a sentence, else its first line.
func firstSentence(out string) string {
	lines := outputLines(out)
	for i, l := range lines {
		if strings.HasPrefix(l, "help[") && i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1])
		}
	}
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

// errorSentence is a refusal's error and its code, "error text (CODE)"; output
// that names neither says its first line.
func errorSentence(out string) string {
	var msg, code string
	for _, l := range outputLines(out) {
		switch {
		case msg == "" && strings.HasPrefix(l, "error:"):
			msg = strings.TrimSpace(strings.TrimPrefix(l, "error:"))
		case code == "" && strings.HasPrefix(l, "code:"):
			code = strings.TrimSpace(strings.TrimPrefix(l, "code:"))
		}
	}
	switch {
	case msg != "" && code != "":
		return msg + " (" + code + ")"
	case msg != "":
		return msg
	case code != "":
		return code
	}
	return firstSentence(out)
}

// outputLines are the lines a command printed, without blanks.
func outputLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimRight(l, " \t"))
		}
	}
	return lines
}

// tailLines are the last n lines of the stream the screen is about, one line
// each, newest last.
func (m *Model) tailLines(w, n int) []string {
	var envs []events.Envelope
	e := m.top()
	if e.screen == screenMain {
		envs = m.w.feed
	} else if e.screen == screenChart {
		// Every line of the chart's tickets, as the feed names them.
		if c := m.w.charts[e.chart]; c != nil {
			for _, id := range c.ids() {
				if t := m.w.tickets[id]; t != nil {
					envs = append(append(envs, t.lines...), t.ledger...)
				}
			}
			sort.SliceStable(envs, func(i, j int) bool {
				a, _ := envs[i].At()
				b, _ := envs[j].At()
				return a.Before(b)
			})
		}
	} else if t := m.w.tickets[e.ticket]; t != nil {
		envs = append(append(envs, t.lines...), t.ledger...)
		sort.SliceStable(envs, func(i, j int) bool {
			a, _ := envs[i].At()
			b, _ := envs[j].At()
			return a.Before(b)
		})
		if e.screen == screenSlice {
			var only []events.Envelope
			for _, l := range envs {
				if l.Slice == e.slice {
					only = append(only, l)
				}
			}
			envs = only
		}
	}
	if len(envs) > n {
		envs = envs[len(envs)-n:]
	}
	out := make([]string, 0, n)
	for _, l := range envs {
		out = append(out, fit("  "+m.tailLine(l, e.screen == screenMain || e.screen == screenChart), w))
	}
	return out
}

// tailLine is one stream line: the time, where it came from, the event, and
// what it is about.
func (m *Model) tailLine(l events.Envelope, withTicket bool) string {
	src := "j"
	if l.Src == events.SrcLedger {
		src = "l"
	}
	line := m.st.dim(clock(l, m.zone)+" "+src) + " "
	event := pad(l.Event, 15)
	if s := eventState(l); s != "" {
		event = m.st.state(s, event)
	}
	line += event + " "
	if withTicket {
		line += pad(l.Ticket, 8) + " "
	}
	line += pad(l.Slice, 3) + " " + detailOf(l)
	return line
}

// detailOf is what a stream line is about, beyond its event: whatever of the
// line's own fields name a subject or say what came of it.
func detailOf(l events.Envelope) string {
	var parts []string
	for _, s := range []string{l.Tool, l.Ref, l.Outcome, short(l.Commit), l.Summary, l.URL} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if l.Provenance != "" {
		parts = append(parts, "("+l.Provenance+")")
	}
	if l.Session != "" {
		parts = append(parts, l.Session)
	}
	return strings.Join(parts, " ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
