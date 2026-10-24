package tui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/events"
)

// The kinds of row a cursor can stand on.
const (
	rowTicket   = "ticket"
	rowBrief    = "brief"
	rowBuild    = "build"
	rowReview   = "review"
	rowPublish  = "publish"
	rowQuestion = "question"
	rowAsk      = "ask"
	rowSlice    = "slice"
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
// top edge its title in text and right.
type pline struct {
	text  string
	ref   *rowRef
	box   int
	right string
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
	}
	return m.mainPage()
}

// View draws the screen: a header, the body windowed so the cursor shows,
// what the last command printed, and the keys, in exactly the height the
// terminal has.
func (m *Model) View() string {
	w, h := m.width, m.height
	m.followSel()
	pg := m.page()
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
	if bodyH < 6 && len(panel) > 0 {
		panel = panel[:max(0, len(panel)-(6-bodyH))]
		bodyH = h - len(head) - len(foot) - len(panel)
	}
	if bodyH < 1 {
		bodyH = 1
	}

	var tailPane []string
	pageH := bodyH
	if m.tail && bodyH >= 8 {
		tailH := bodyH / 2
		pageH = bodyH - tailH - 1
		tailPane = append([]string{"  " + m.st.dim("tail "+strings.Repeat("─", max(0, w-7)))}, m.tailLines(w, tailH)...)
	}

	body := m.window(pg, sel, pageH, w)
	for len(body) < pageH {
		body = append(body, "")
	}
	out := append(append(append(append(head, body...), tailPane...), panel...), foot...)
	for i, l := range out {
		out[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(out, "\n")
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

// window renders the page lines that fit in h rows with the sel-th
// selectable row in view, marking that row.
func (m *Model) window(pg page, sel, h, w int) []string {
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
	}
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
	var right string
	switch e.screen {
	case screenMain:
		n := len(m.w.order)
		right = m.st.dim(strconv.Itoa(n) + " " + plural(n, "ticket", "tickets"))
	default:
		t := m.w.tickets[e.ticket]
		crumbs += " › " + m.st.bold(e.ticket)
		if t != nil && t.title != "" && e.screen == screenTicket {
			crumbs += "  " + t.title
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
		case screenSlice:
			crumbs += " › 2 build › slice " + m.st.bold(e.slice)
		}
		if t != nil {
			state, label := m.stepLabel(t)
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
		about := m.input.about
		if len(about) > 4 {
			about = append(about[:3:3], "…")
		}
		for _, l := range about {
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

// keyHints are the keys that do something on the screen on top, as key and
// meaning pairs.
func (m *Model) keyHints() []string {
	move := []string{"↑↓", "move"}
	open := []string{"enter", "open"}
	back := []string{"esc", "back"}
	answer := []string{"a", "answer"}
	keep := []string{"k", "keep"}
	dismiss := []string{"d", "dismiss"}
	gate := []string{"g", "gate"}
	publish := []string{"p", "publish"}
	tail := []string{"t", "tail"}
	quit := []string{"q", "quit"}
	var keys [][]string
	switch m.top().screen {
	case screenMain:
		keys = [][]string{move, open, gate, answer, tail, {"r", "refresh"}, quit}
	case screenTicket:
		keys = [][]string{move, open, back, answer, keep, dismiss, gate, publish, tail, quit}
	case screenBrief:
		keys = [][]string{move, open, back, tail, quit}
	case screenBuild:
		keys = [][]string{move, open, back, gate, tail, quit}
	case screenReview:
		keys = [][]string{move, back, keep, dismiss, gate, tail, quit}
	case screenPublish:
		keys = [][]string{back, publish, tail, quit}
	case screenSlice:
		keys = [][]string{move, back, answer, tail, quit}
	}
	flat := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		flat = append(flat, k...)
	}
	return flat
}

// panel is what the last command printed and the notice, above the keys.
func (m *Model) panel(w int) []string {
	var out []string
	if m.last != nil {
		for _, s := range m.last.Steps {
			state := StateOK
			if s.Code != 0 {
				state = StateFail
			}
			out = append(out, fit("  "+m.st.icon(state)+" "+m.st.bold(m.shownLine(s.Args)), w))
		}
		shown := m.last.shown()
		lines := outputLines(shown.Out)
		if len(lines) > 6 {
			lines = append(lines[:5:5], "…")
		}
		for _, l := range lines {
			if shown.Code != 0 && (strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "code:")) {
				out = append(out, fit("    "+m.st.state(StateFail, l), w))
				continue
			}
			out = append(out, fit("    "+m.st.dim(l), w))
		}
	}
	if m.notice != "" {
		out = append(out, fit("  "+m.st.state(StateFindings, m.notice), w))
	}
	return out
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
		out = append(out, fit("  "+m.tailLine(l, e.screen == screenMain), w))
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
