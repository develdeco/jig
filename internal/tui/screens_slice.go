package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// slicePage is the whole story of one slice, top to bottom: for a fix slice its
// finding, then the slice's own panel (its goal, oracle and facts), every commit
// with its runs, the questions asked about it, the calls its holder made, and
// its lines newest first.
func (m *Model) slicePage(id, slice string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	p.lines = append(p.lines, m.sliceHead(t, slice)...)
	p.lines = append(p.lines, m.sliceBody(t, slice)...)
	return p
}

// sliceRowOf is the row of the slice among the ticket's, nil when it has none.
func (m *Model) sliceRowOf(t *ticket, slice string) *sliceRow {
	for _, s := range t.slices(m.zone) {
		if s.ID == slice {
			return &s
		}
	}
	return nil
}

// sliceHead is the lines of the slice screen over its commits: the finding of a
// fix slice and the slice's own panel. They show the slice's work time, which
// counts to the second, so they are built once for each second as well as for
// what they read; a key within the second draws them again and builds nothing.
func (m *Model) sliceHead(t *ticket, slice string) []pline {
	memo := t.memoFor()
	key := "head|" + slice + "|" + strconv.Itoa(m.width) + "|" + m.zone.String() + "|" + strconv.FormatInt(m.now().Unix(), 10)
	if lines, ok := memo.sliceBody[key]; ok {
		return lines
	}
	var p page
	row := m.sliceRowOf(t, slice)
	switch {
	case row == nil && t.status == nil:
		p.add(m.st.dim("reading the ticket's status"))
	case row == nil:
		p.add(m.st.dim("slice " + slice + " has no lines yet"))
	default:
		m.findingPanel(&p, t, *row)
		m.slicePanel(&p, t, *row)
	}
	return m.keepLines(memo, key, p)
}

// keepLines wraps a built page for the width once, marks its lines as fitting,
// and keeps them in the ticket's memo under key.
func (m *Model) keepLines(memo *ticketMemo, key string, p page) []pline {
	p = m.wrapLong(p)
	for i := range p.lines {
		p.lines[i].fits = true
	}
	if memo.sliceBody == nil {
		memo.sliceBody = map[string][]pline{}
	}
	if strings.HasPrefix(key, "head|") {
		// The heads of the seconds gone by.
		for k := range memo.sliceBody {
			if strings.HasPrefix(k, "head|") {
				delete(memo.sliceBody, k)
			}
		}
	}
	memo.sliceBody[key] = p.lines
	return p.lines
}

// sliceBody is the lines of the slice screen under the slice's own panel: its
// commits, the questions, the calls and the timeline. They are built once for
// what they read, the ticket's lines, status and the width, and a key that moves
// the cursor draws them again and builds nothing.
func (m *Model) sliceBody(t *ticket, slice string) []pline {
	memo := t.memoFor()
	key := "body|" + slice + "|" + strconv.Itoa(m.width) + "|" + m.zone.String()
	if lines, ok := memo.sliceBody[key]; ok {
		return lines
	}
	m.sliceBuilds++
	var p page
	row := m.sliceRowOf(t, slice)
	oracle := ""
	if row != nil {
		oracle = row.Oracle
	}
	m.commitsPanel(&p, t, slice, oracle)

	var qs []questionRow
	for _, q := range t.questions(m.zone) {
		if q.Slice == slice {
			qs = append(qs, q)
		}
	}
	if len(qs) > 0 {
		cells := make([][]string, len(qs))
		for i, q := range qs {
			state, word := StateNeedsInput, "open"
			if !q.Open {
				// The answer says by whom, in the words the timeline uses.
				state, word = StateOK, "answered"
				if by := m.byWhom(t, q.Provenance, q.AnswerSession); by != "" {
					word += " by " + by
				}
			}
			asked := ""
			if q.Session != "" {
				asked = m.st.dim("asked by " + m.whoAs(t, q.Session, roleOf(journal.EventQuestion)) + " at " + q.At)
			}
			cells[i] = []string{m.st.icon(state), q.ID, m.st.state(state, word), asked}
		}
		p.boxTop("Questions", strconv.Itoa(len(qs)))
		laid := m.laidList(&p, questionColumns, dimmed(m.st, questionColumnNames), cells, 0)
		for i, q := range qs {
			// Every question is a row, open or answered: the cursor stands on it and
			// its text and answer are drawn whole under it.
			p.boxLaid(&rowRef{kind: rowQuestion, id: q.ID, ticket: t.id, slice: slice}, laid[i])
			for _, l := range m.panelLines(q.Text) {
				p.boxUnder("    " + l)
			}
			if !q.Open && q.Answer != "" {
				// An answer is what a person acted on: drawn whole, wrapped.
				m.under(&p, 4, "", "answer: "+q.Answer, func(s string) string { return s })
			}
		}
		p.boxEnd()
	}

	m.callsPanel(&p, t, slice, row)

	var lines []int
	for i, l := range t.lines {
		// An old worker's request for a verification is nothing here.
		if l.Slice == slice && l.Event != journal.EventVerifyPending {
			lines = append(lines, i)
		}
	}
	if len(lines) > 0 {
		lines = newestFirst(lines, func(i int) time.Time { at, _ := t.lines[i].At(); return at })
		rows := make([]listRow, len(lines))
		for k, i := range lines {
			rows[k] = listRow{cells: m.timelineCells(t, t.lines[i])}
		}
		m.listPanel(&p, listSpec{title: "Timeline", summary: strconv.Itoa(len(lines)), cols: timelineColumns, names: timelineColumnNames, rows: rows})
	}
	return m.keepLines(memo, key, p)
}

// The columns of the slice screen's lists: a commit, a question, and a line of
// the timeline or of the calls.
var (
	commitColumns       = []column{{}, {gap: 1}, {free: true, gap: 2}, {gap: 2}}
	commitColumnNames   = []string{"", "commit", "subject", "time"}
	questionColumns     = []column{{}, {gap: 1}, {gap: 2}, {free: true, gap: 2}}
	questionColumnNames = []string{"", "question", "state", "asked"}
	timelineColumns     = []column{{}, {free: true, gap: 1}}
	timelineColumnNames = []string{"time", "what"}
)

// lastClaimant is the session of the slice's latest claim line, whoever has let
// go of the slice since, and whether it still holds it.
func lastClaimant(t *ticket, slice string) (session string, held, ok bool) {
	claim := t.latest(slice, journal.EventClaim)
	if claim < 0 {
		return "", false, false
	}
	return t.lines[claim].Session, t.latest(slice, journal.EventRelease) < claim, true
}

// factRow is a row of the slice's panel: a label, its value, and the slice the
// value names, if it names one.
type factRow struct {
	label, value string
	ref          *rowRef
}

// slicePanel adds the slice's own panel: its id, with what it stands at, and a
// grid of what is known of it, each field whole. A fact with nothing to say is
// not drawn.
func (m *Model) slicePanel(p *page, t *ticket, s sliceRow) {
	var rows []factRow
	add := func(label, value string) {
		if value != "" {
			rows = append(rows, factRow{label: label, value: value})
		}
	}
	full := t.statusSlice(s.ID)
	// The facts come first, so each is on the screen whatever the length of the
	// goal and the oracle under them.
	add("state", m.st.icon(s.State)+" "+m.st.state(s.State, s.State))
	add("tries", strconv.Itoa(s.Attempts))
	if holder, held, ok := lastClaimant(t, s.ID); ok {
		verb := "built by"
		if held && s.State != StateGreen {
			verb = "held by"
		}
		// A session that claimed the slice is its builder, whatever else it did
		// on the ticket and whether it holds the slice still.
		add(verb, firstOf(m.whoWithAs(t, holder, roleBuilder), "a session"))
	}
	all := t.slices(m.zone)
	named := func(label string, ids []string) {
		for i, id := range ids {
			state := ""
			for _, o := range all {
				if o.ID == id {
					state = o.State
				}
			}
			value := id
			if state != "" {
				value = m.st.icon(state) + " " + id
			}
			if i > 0 {
				label = ""
			}
			rows = append(rows, factRow{label: label, value: value, ref: &rowRef{kind: rowSlice, id: id, ticket: t.id, slice: id}})
		}
	}
	if s.BlockedBy != "" {
		named("after", strings.Split(s.BlockedBy, "+"))
	}
	var waiting []string
	for _, o := range all {
		if o.BlockedBy != "" && slices.Contains(strings.Split(o.BlockedBy, "+"), s.ID) {
			waiting = append(waiting, o.ID)
		}
	}
	named("needed by", waiting)
	add("workspace", s.Workspace)
	add("oracle", s.Oracle)
	if t.fixRound(s.ID) == 0 {
		// A fix slice's goal is its finding, drawn above in a panel of its own.
		add("goal", strings.Join(strings.Fields(full.Goal), " "))
	}

	summary := m.st.icon(s.State) + " " + m.st.state(s.State, s.State) + " · " + strconv.Itoa(s.Attempts) + " " + plural(s.Attempts, "try", "tries")
	if d := m.clocksOf(t).work.of(stepBuild, "slice:"+s.ID); d > 0 {
		summary += " · " + span(d)
	}
	p.boxTop("Slice "+s.ID, summary)
	m.factRows(p, rows)
	p.boxEnd()
}

// factRows adds the rows of a grid of facts to the panel: a dim label column and
// the value after it, wrapped whole by the allocator.
func (m *Model) factRows(p *page, rows []factRow) {
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = []string{m.st.dim(r.label), r.value}
	}
	// A fact is never cut: its free text wraps as far as it goes.
	for i, lines := range layoutRows(runColumns, cells, m.innerWidth(), 1<<20) {
		p.boxLaid(rows[i].ref, lines)
	}
}

// findingPanel adds, for a fix slice, the panel that says why the slice exists:
// the round that sent it back, the findings it answers and the goal jig wrote
// for the builder, whole and as written, from its first line that is not a
// heading on. A slice that is no fix slice has none.
func (m *Model) findingPanel(p *page, t *ticket, s sliceRow) {
	round := t.fixRound(s.ID)
	if round == 0 {
		return
	}
	full := t.statusSlice(s.ID)
	p.boxTop("Finding", "round "+strconv.Itoa(round))
	// The round and each finding the slice answers are rows: the round opens the
	// review screen on it, a finding opens its own screen.
	n := strconv.Itoa(round)
	rows := []factRow{{label: "round", value: "round " + n, ref: &rowRef{kind: rowRound, id: n, ticket: t.id, slice: s.ID}}}
	label := "findings"
	for _, id := range full.Findings {
		value := id
		if f := t.findingNamed(id); f != nil {
			value += " " + f.Title
		}
		rows = append(rows, factRow{label: label, value: value, ref: &rowRef{kind: rowFinding, id: id, ticket: t.id, slice: s.ID, round: n}})
		label = ""
	}
	m.factRows(p, rows)
	for _, l := range wrapWhole(goalBody(full.Goal), m.innerWidth()) {
		p.boxAdd(l)
	}
	p.boxEnd()
}

// goalBody is a goal from its first line that is not a heading (blank, or ending
// in a colon) on, its own line breaks kept.
func goalBody(goal string) string {
	lines := strings.Split(strings.ReplaceAll(goal, "\r\n", "\n"), "\n")
	for i, l := range lines {
		if l = strings.TrimSpace(l); l != "" && !strings.HasSuffix(l, ":") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return ""
}

// statusSlice is the status's row of the slice, empty before the status is read.
func (t *ticket) statusSlice(id string) statusSlice {
	if t.status != nil {
		for _, s := range t.status.Slices {
			if s.ID == id {
				return s
			}
		}
	}
	return statusSlice{}
}

// commitsPanel adds the panel of every commit of the slice, newest first, none
// left out: its short id, its subject and its time, and under it how its
// verification stands in phase's words with the command that clears it, each run
// of the oracle against it, the output of a run that failed, and the recordings
// its scenarios wrote. A slice with no commit has none.
func (m *Model) commitsPanel(p *page, t *ticket, slice, oracle string) {
	commits := newestFirst(t.sliceCommits(slice), func(c commitRecord) time.Time { return c.Began })
	if len(commits) == 0 {
		return
	}
	cells := make([][]string, len(commits))
	icons := make([]string, len(commits))
	for i, c := range commits {
		row, hasRow := t.status.verificationOf(slice, c.SHA)
		icons[i] = c.State
		switch {
		case t.status == nil:
			icons[i] = StateQueued
		case hasRow:
			icons[i] = row.icon()
		}
		when := m.commitWhen(c)
		if when != "" {
			when = m.st.dim(when)
		}
		cells[i] = []string{m.st.icon(icons[i]), short(c.SHA), c.Subject, when}
	}
	p.boxTop("Commits", strconv.Itoa(len(commits)))
	// A commit's subject is never cut, and a column nothing is written in has no
	// name.
	laid := m.laidList(p, commitColumns, dimmed(m.st, namedColumns(commitColumnNames, cells)), cells, -1)
	for i, c := range commits {
		p.boxLaid(&rowRef{kind: rowTry, id: c.SHA, ticket: t.id, slice: slice}, laid[i])
		row, hasRow := t.status.verificationOf(slice, c.SHA)
		switch {
		case t.status == nil:
			m.under(p, 2, "", readingText, m.st.dim)
		case hasRow:
			// The state in phase's words, then the command that clears it.
			icon := icons[i]
			m.under(p, 2, "", row.State, func(s string) string { return m.st.state(icon, s) })
			if row.Clear != "" {
				m.under(p, 4, "", row.Clear, m.st.dim)
			}
		case c.State == StateGreen:
			m.under(p, 2, "", "verified", func(s string) string { return m.st.state(StateGreen, s) })
		}
		if c.Error != nil {
			m.under(p, 2, "", "could not be verified: "+firstOf(c.Error.Summary, "no reason recorded"), func(s string) string { return m.st.state(StateStalled, s) })
		}
		for _, run := range newestFirst(c.Runs, func(l events.Envelope) time.Time { at, _ := l.At(); return at }) {
			state := StateGreen
			switch {
			case run.Cause != "":
				// A run that judged nothing is not the code failing.
				state = StateQueued
			case run.Outcome != "pass":
				state = StateStalled
			}
			m.under(p, 2, m.st.icon(state), m.runWords(t, run, oracle), m.st.dim)
			if run.Outcome == "fail" && run.Cause == "" {
				output, note := failureOutput(run)
				if output != "" {
					m.under(p, 4, "", output, func(s string) string { return m.st.state(StateStalled, s) })
				}
				m.under(p, 4, "", note, m.st.dim)
			}
		}
		for _, r := range c.Recordings {
			m.under(p, 2, "", "recorded: "+recordingWords(r), m.st.dim)
		}
		if c.RecordNote != "" {
			m.under(p, 2, "", "recordings: "+c.RecordNote, m.st.dim)
		}
	}
	if t.status != nil {
		if run, ok := t.status.LastRuns[slice]; ok && run.Result != "" {
			// What the status says of the slice's last witnessed run: whether it
			// covers the slice's last commit.
			state := StateGreen
			if run.Result != "pass" {
				state = StateStalled
			}
			p.boxAdd(m.st.icon(state) + " " + m.st.dim("last run: "+run.Result+" \u00b7 covers: "+run.Covers))
		}
	}
	p.boxEnd()
}

// under adds text under the row above it in the panel, standing indent cells in
// and wrapped whole at the room that leaves, none of it cut. A mark, when there
// is one, leads the first line; style colors each line after the wrap, so the
// color is whole on every line the text came to.
func (m *Model) under(p *page, indent int, mark, text string, style func(string) string) {
	lead := indent
	if mark != "" {
		lead += width(mark) + 1
	}
	room := max(1, m.innerWidth()-lead)
	lines := []string{text}
	if strings.Contains(text, "\n") || (len(text) > room && width(text) > room) {
		lines = wrapWhole(text, room)
	}
	for i, l := range lines {
		if i == 0 && mark != "" {
			p.boxUnder(spaces(indent) + mark + " " + style(l))
			continue
		}
		p.boxUnder(spaces(lead) + style(l))
	}
}

// namedColumns are the names of a list's columns, a blank for each column whose
// cells are all blank.
func namedColumns(names []string, cells [][]string) []string {
	out := append([]string(nil), names...)
	for c := range out {
		blank := true
		for _, row := range cells {
			if c < len(row) && row[c] != "" {
				blank = false
				break
			}
		}
		if blank {
			out[c] = ""
		}
	}
	return out
}

// recordingWords is a recorded file in words: the scenario with its caption, the
// flow and step when it has them, the file's name and its size.
func recordingWords(r journal.Recording) string {
	parts := []string{r.Scenario}
	if r.Caption != "" {
		parts = append(parts, r.Caption)
	}
	if r.Flow != "" {
		flow := "flow " + r.Flow
		if r.Step > 0 {
			flow += " step " + strconv.Itoa(r.Step)
		}
		parts = append(parts, flow)
	}
	parts = append(parts, r.File, sizeWords(r.Size))
	return strings.Join(parts, " \u00b7 ")
}

// sizeWords is a file's size in the unit that reads shortest.
func sizeWords(n int64) string {
	const kib, mib = 1 << 10, 1 << 20
	switch {
	case n >= mib:
		return strconv.FormatFloat(float64(n)/mib, 'f', 1, 64) + " MiB"
	case n >= kib:
		return strconv.FormatFloat(float64(n)/kib, 'f', 1, 64) + " KiB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

// callsPanel adds the panel of the calls the slice's holder made while it held
// the slice, newest first, each as the sub-step's live line words it. A slice
// whose holder made none has no panel.
func (m *Model) callsPanel(p *page, t *ticket, slice string, row *sliceRow) {
	tr := t.trackOf(stepBuild, "slice:"+slice)
	open := row != nil && row.State != StateGreen
	var calls []events.Envelope
	for _, l := range t.moves(tr, open, m.now()) {
		if isMove(l) {
			calls = append(calls, l)
		}
	}
	if len(calls) == 0 {
		return
	}
	calls = newestFirst(calls, func(l events.Envelope) time.Time { at, _ := l.At(); return at })
	rows := make([]listRow, len(calls))
	for i, l := range calls {
		rows[i] = listRow{cells: []string{m.st.dim(clock(l, m.zone)), m.moveText(t, l)}}
	}
	m.listPanel(p, listSpec{title: "Calls", summary: strconv.Itoa(len(calls)), cols: timelineColumns, names: timelineColumnNames, rows: rows})
}
