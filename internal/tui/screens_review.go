package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// fixRow is a fix slice a round sent back: its id and the cells of its row.
type fixRow struct {
	id    string
	cells []string
	at    time.Time
}

// fixRows are the fix slices the rounds sent back, the newest first by the line
// that sent each back: the state each is in, the round, and the finding it
// fixes.
func (m *Model) fixRows(t *ticket) []fixRow {
	states := map[string]string{}
	for _, s := range t.slices(m.zone) {
		states[s.ID] = s.State
	}
	var out []fixRow
	seen := map[string]bool{}
	for _, l := range t.lines {
		if l.Event != journal.EventFixSlice || l.Slice == "" || seen[l.Slice] {
			continue
		}
		seen[l.Slice] = true
		state := firstOf(states[l.Slice], StateQueued)
		round := ""
		if n := t.fixRound(l.Slice); n > 0 {
			round = m.st.dim("round " + strconv.Itoa(n))
		}
		at, _ := l.At()
		out = append(out, fixRow{id: l.Slice, cells: []string{m.st.icon(state), l.Slice, round, t.goals()[l.Slice]}, at: at})
	}
	return newestFirst(out, func(f fixRow) time.Time { return f.at })
}

// reviewPage is a ticket's review: each gate round with who reviewed it,
// whether they were independent, what they reviewed and how it ended; what the
// latest round found, with the reason for each finding; the fix slices sent
// back; the asks that wait for a person and what has been decided about each;
// and the review's activity. A round names its reviewer with the harness and
// model the session runs, so the screen has no list of the reviewing sessions.
func (m *Model) reviewPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	step := m.steps(t)[stepReview]
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))
	if line := m.fixBudgetLine(t); line != "" {
		p.add(line)
	}

	p.add("")
	rounds := newestFirst(t.rounds(m.zone), func(r roundRow) time.Time { return r.Began })
	m.roundsPanel(&p, t, rounds)

	asks := t.asks()
	asking := map[string]bool{}
	for _, a := range asks {
		asking[a.ID] = true
	}
	m.findingRows(&p, t, asking)
	m.probeRows(&p, t)
	m.unprobedRows(&p, t)

	if fixes := m.fixRows(t); len(fixes) > 0 {
		rows := make([]listRow, len(fixes))
		for i, f := range fixes {
			rows[i] = listRow{ref: &rowRef{kind: rowSlice, id: f.id, ticket: id, slice: f.id}, cells: f.cells}
		}
		m.listPanel(&p, listSpec{title: "Sent back", summary: strconv.Itoa(len(fixes)), cols: sentBackColumns, names: sentBackColumnNames, rows: rows})
	}

	m.asksPanel(&p, t, asks)

	m.stepActivityPanel(&p, t, stepReview)
	return p
}

// The columns of the review screen's lists.
var (
	roundListColumns       = []column{{}, {gap: 1}, {gap: 2}, {gap: 2}, {gap: 2}, {gap: 2}, {gap: 2}}
	roundListColumnNames   = []string{"", "round", "verdict", "asked", "range", "read", "at"}
	findingListColumns     = []column{{}, {gap: 1}, {gap: 2}, {gap: 2}, {free: true, gap: 2}, {gap: 2}}
	findingListColumnNames = []string{"", "risk", "where", "outcome", "title", "id"}
	probeListColumns       = []column{{}, {gap: 1}, {gap: 2}, {gap: 2}}
	probeListColumnNames   = []string{"", "round", "where", "outcome"}
	askColumns             = []column{{}, {gap: 1}, {gap: 2}, {gap: 2}, {free: true, gap: 2}, {gap: 2}}
	askColumnNames         = []string{"", "risk", "where", "decision", "title", "id"}
	sentBackColumns        = []column{{}, {id: true, gap: 1}, {gap: 2}, {free: true, gap: 2}}
	sentBackColumnNames    = []string{"", "slice", "round", "goal"}
	unprobedColumns        = []column{{}, {free: true, gap: 1}}
	unprobedColumnNames    = []string{"where", "note"}
)

// roundsPanel adds the rounds, newest first: a row each with its verdict, what
// it raised, the range it read, how many files and when, and under it who
// reviewed it and whether they stood apart from the builders, and what its
// reviewer probed.
func (m *Model) roundsPanel(p *page, t *ticket, rounds []roundRow) {
	p.boxTop("Rounds", strconv.Itoa(len(rounds)))
	if len(rounds) == 0 {
		p.boxAdd(m.st.dim("no round yet: a fresh session reviews the change once every slice is green"))
		p.boxEnd()
		return
	}
	cells := make([][]string, len(rounds))
	notes := make([][]string, len(rounds))
	for i, r := range rounds {
		cells[i], notes[i] = m.roundRecord(t, r)
	}
	laid := m.laidList(p, roundListColumns, dimmed(m.st, roundListColumnNames), cells, 0)
	for i := range rounds {
		p.boxLaid(nil, laid[i])
		for _, l := range notes[i] {
			p.boxAdd(l)
		}
	}
	p.boxEnd()
}

// roundRecord is a round as the cells of its row, and the lines under it: who
// reviewed it and whether they stood apart from the builders, and what its
// reviewer probed.
func (m *Model) roundRecord(t *ticket, r roundRow) (cells, notes []string) {
	word := map[string]string{StateReviewing: "open", StateFindings: "findings", StateReady: "clean"}[r.State]
	if r.State == StateReady && r.Carried > 0 {
		word += " (carried over from round " + strconv.Itoa(r.Carried) + ")"
	}
	state := r.State
	if state == StateReady {
		state = StateOK
	}
	// What the status records of a round is what its close left: a round that is
	// open has none, whatever a report written an instant before its closing
	// line says.
	var rec *statusRound
	if t.status != nil && r.State != StateReviewing {
		for i := range t.status.Rounds {
			if t.status.Rounds[i].N == r.N {
				rec = &t.status.Rounds[i]
			}
		}
	}
	if rec != nil && rec.Verdict == "not-independent" {
		state, word = StateStalled, "not clean"
	}
	asked := ""
	if rec != nil && r.State != StateReviewing {
		asked = roundCounts(*rec)
	}

	reviewer := r.Reviewer
	if reviewer == "" && rec != nil {
		reviewer = rec.Reviewer
	}
	if reviewer == "" {
		reviewer = t.reviewerOf(r.N)
	}
	var who []string
	switch {
	case reviewer != "" && r.State == StateReviewing:
		who = append(who, m.reviewerWho(t, reviewer, r.N, "", rec)+" took it")
	case reviewer != "":
		who = append(who, m.reviewerWho(t, reviewer, r.N, "", rec))
	case r.State == StateReviewing:
		who = append(who, "waiting for a reviewer to take it")
	}
	if rec != nil {
		switch rec.Independent {
		case "true":
			who = append(who, "independent")
		case "false":
			// A named reviewer already reads "builder reviewing its own change".
			if reviewer == "" {
				who = append(who, "NOT independent: it built what it reviewed")
			}
		case "unknown":
			who = append(who, "independence unknown")
		}
	}
	if reviewer != "" {
		who = append(who, shortID(reviewer))
	}
	if len(who) > 0 {
		for _, l := range wrap(strings.Join(who, " · "), m.innerWidth()-6, 2) {
			notes = append(notes, "    "+m.st.dim(l))
		}
	}
	rng, read := "", ""
	if rec != nil {
		rng = shortRange(rec.Scope, rec.Base, rec.Head)
		if rec.Files >= 0 {
			read = strconv.Itoa(rec.Files) + " " + plural(rec.Files, "file", "files")
		}
		if line := ledgerCounts(*rec); line != "" {
			notes = append(notes, "    "+m.st.dim(line))
		}
	}
	cells = []string{m.st.icon(state), "round " + fmt.Sprint(r.N), m.st.state(state, word), m.st.dim(asked), m.st.dim(rng), m.st.dim(read), m.st.dim(r.At)}
	return cells, notes
}

// ledgerCounts is what a reviewer round probed, in the ledger's words:
// "27 probes · 25 caught · 2 survived · 3 hunks unprobed · 1 skipped". A round
// that probed nothing says so, so it reads as one; a round no reviewer ran, or
// one whose status has no ledger columns, says nothing.
func ledgerCounts(r statusRound) string {
	if r.Files < 0 || r.Probes < 0 {
		return ""
	}
	var parts []string
	if r.Probes == 0 {
		parts = append(parts, "no probes")
	} else {
		parts = append(parts, strconv.Itoa(r.Probes)+" "+plural(r.Probes, "probe", "probes"),
			strconv.Itoa(max(0, r.Probes-r.Survived))+" caught", strconv.Itoa(max(0, r.Survived))+" survived")
	}
	if r.Unprobed >= 0 {
		parts = append(parts, strconv.Itoa(r.Unprobed)+" "+plural(r.Unprobed, "hunk", "hunks")+" unprobed")
	}
	if r.Skipped > 0 {
		parts = append(parts, strconv.Itoa(r.Skipped)+" skipped")
	}
	return strings.Join(parts, " · ")
}

// probeRows adds every round's probes as plain rows, the newest round first
// and as recorded within it: a survivor first, then the caught ones in the
// order taken. A probe a
// later round probed on the same file and line is drawn back and says what
// that round found; whether the file changed since a round needs git, which the
// screen does not read, so the block says it does not mark that.
func (m *Model) probeRows(p *page, t *ticket) {
	if t.status == nil || len(t.status.Probes) == 0 {
		return
	}
	probes := append([]statusProbe(nil), t.status.Probes...)
	sort.SliceStable(probes, func(i, j int) bool {
		if probes[i].Round != probes[j].Round {
			return probes[i].Round > probes[j].Round
		}
		return probes[i].Outcome == "survived" && probes[j].Outcome != "survived"
	})
	later := map[string]statusProbe{} // file:line -> the latest round's probe of it
	for _, pr := range t.status.Probes {
		if cur, ok := later[pr.Where]; !ok || pr.Round >= cur.Round {
			later[pr.Where] = pr
		}
	}
	p.add(m.st.dim("as recorded: a file changed since a round is not marked"))
	cells := make([][]string, len(probes))
	superseded := make([]bool, len(probes))
	outcomes := make([]string, len(probes))
	roundW, outcomeW := width(probeListColumnNames[1]), width(probeListColumnNames[3])
	for i, pr := range probes {
		outcomes[i] = pr.Outcome
		next := later[pr.Where]
		if superseded[i] = next.Round > pr.Round; superseded[i] {
			outcomes[i] += " · " + supersededBy(pr, next)
		}
		roundW = max(roundW, width("round "+strconv.Itoa(pr.Round)))
		outcomeW = max(outcomeW, width(outcomes[i]))
	}
	// The path gives way, never the round or the verdict: it is cut from its front
	// to what the screen has left beside them.
	whereMax := max(12, m.innerWidth()-(1+1+roundW+2+2+outcomeW))
	for i, pr := range probes {
		state := StateOK
		if pr.Outcome == "survived" {
			state = StateFindings
		}
		where := tailCut(pr.Where, whereMax)
		round := "round " + strconv.Itoa(pr.Round)
		if superseded[i] {
			cells[i] = []string{m.st.icon(state), m.st.dim(round), m.st.dim(where), m.st.dim(outcomes[i])}
		} else {
			cells[i] = []string{m.st.icon(state), round, where, m.st.state(state, outcomes[i])}
		}
	}
	p.boxTop("Probes", strconv.Itoa(len(probes)))
	laid := m.laidList(p, probeListColumns, dimmed(m.st, probeListColumnNames), cells, 0)
	for i, pr := range probes {
		p.boxLaid(nil, laid[i])
		for _, l := range wrap(pr.Mutation, m.innerWidth()-6, probeLines) {
			if superseded[i] {
				l = m.st.dim(l)
			}
			p.boxAdd("    " + l)
		}
		for _, l := range wrap(pr.Seam+" · "+pr.Command, m.innerWidth()-6, probeLines) {
			p.boxAdd("    " + m.st.dim(l))
		}
	}
	p.boxEnd()
}

// supersededBy is what the later round found of the line a probe probed: a
// survivor probed again and caught, a survivor that survived again, or a caught
// probe the later round lost.
func supersededBy(pr, next statusProbe) string {
	round := strconv.Itoa(next.Round)
	switch {
	case next.Outcome != "survived":
		return next.Outcome + " in round " + round
	case pr.Outcome == "survived":
		return "survived again in round " + round
	}
	return "survived in round " + round + " (" + pr.Outcome + " in round " + strconv.Itoa(pr.Round) + ")"
}

// tailCut is s cut to w cells from its front, so the end of a file:line stays.
func tailCut(s string, w int) string {
	if w < 4 {
		w = 4
	}
	r := []rune(s)
	for len(r) > 1 && width(string(r)) > w {
		r = r[1:]
		if width("…"+string(r)) <= w {
			return "…" + string(r)
		}
	}
	return string(r)
}

// probeLines is how many lines a probe's text may take: more than any probe
// has, so a long one wraps and is never cut.
const probeLines = 100

// unprobedRows adds what the latest round that recorded its unprobed hunks left
// uncovered, as file:from-to rows, and the skips that round set aside with
// their reasons. A status whose rounds recorded none shows no block.
func (m *Model) unprobedRows(p *page, t *ticket) {
	if t.status == nil {
		return
	}
	round := 0
	for _, r := range t.status.Rounds {
		if r.Files >= 0 && r.Unprobed >= 0 {
			round = r.N
		}
	}
	if round == 0 {
		return
	}
	var rows []listRow
	hunks := 0
	for _, h := range t.status.Unprobed {
		if h.Round == round {
			hunks++
			rows = append(rows, listRow{cells: []string{h.File + ":" + strconv.Itoa(h.From) + "-" + strconv.Itoa(h.To), ""}})
		}
	}
	for _, s := range t.status.ProbeSkips {
		if s.Round != round {
			continue
		}
		where := s.File
		if s.Range != "" && s.Range != "whole file" {
			where += ":" + s.Range
		}
		rows = append(rows, listRow{cells: []string{where, m.st.dim("set aside: " + s.Why)}})
	}
	summary := "round " + strconv.Itoa(round) + " left no hunk unprobed"
	if hunks > 0 {
		summary = "round " + strconv.Itoa(round) + " left " + strconv.Itoa(hunks) + " " + plural(hunks, "hunk", "hunks")
	}
	m.listPanel(p, listSpec{title: "Unprobed", summary: summary, cols: unprobedColumns, names: unprobedColumnNames, rows: rows, empty: "no hunk left unprobed"})
}

// roundCounts are the findings a round raised, as "1 fix · 2 asks", "" for
// none.
func roundCounts(r statusRound) string {
	var parts []string
	if r.Fix > 0 {
		parts = append(parts, strconv.Itoa(r.Fix)+" "+plural(r.Fix, "fix", "fixes"))
	}
	if r.Ask > 0 {
		parts = append(parts, strconv.Itoa(r.Ask)+" "+plural(r.Ask, "ask", "asks"))
	}
	if r.Note > 0 {
		parts = append(parts, strconv.Itoa(r.Note)+" "+plural(r.Note, "note", "notes"))
	}
	return strings.Join(parts, " · ")
}

// reviewerOf is the session the journal says took round n: its latest
// gate-reviewer line, "" when none did.
func (t *ticket) reviewerOf(n int) string {
	for i := len(t.lines) - 1; i >= 0; i-- {
		l := t.lines[i]
		if l.Event == journal.EventGateReviewer && l.Attempt == n && l.Session != "" {
			return l.Session
		}
	}
	return ""
}

// findingRows adds what the latest recorded round found that is not an ask
// waiting for a person (those have their own rows below): the fixes that were
// sent back, the notes, and what was dismissed, each with the reason the
// reviewer gave.
func (m *Model) findingRows(p *page, t *ticket, asking map[string]bool) {
	if t.status == nil {
		return
	}
	var rows []statusFinding
	for _, f := range t.status.Findings {
		if !asking[f.ID] {
			rows = append(rows, f)
		}
	}
	if len(rows) == 0 {
		return
	}
	cells := make([][]string, len(rows))
	for i, f := range rows {
		state := findingState(f)
		cells[i] = []string{m.st.icon(state), f.Risk, f.Where, m.st.state(state, findingWord(f)), f.Title, m.st.dim(f.ID)}
	}
	p.boxTop("Findings", "round "+strconv.Itoa(rows[0].Round))
	laid := m.laidList(p, findingListColumns, dimmed(m.st, findingListColumnNames), cells, 0)
	for i, f := range rows {
		p.boxLaid(&rowRef{kind: rowStop, id: f.ID, ticket: t.id}, laid[i])
		for _, l := range wrap(f.Rationale, m.innerWidth()-6, 2) {
			p.boxAdd("    " + m.st.dim(l))
		}
	}
	p.boxEnd()
}

// asksPanel adds the asks that wait for a person and have been decided, a row
// each with its decision, and under it the ask's text, whole.
func (m *Model) asksPanel(p *page, t *ticket, asks []askRow) {
	p.boxTop("Asks", strconv.Itoa(len(asks)))
	switch {
	case t.status == nil:
		p.boxAdd(m.st.dim("reading the ticket's status"))
	case len(asks) == 0:
		p.boxAdd(m.st.dim("nothing: no finding waits for a person"))
	}
	if len(asks) > 0 {
		cells := make([][]string, len(asks))
		for i, a := range asks {
			state, word := askDecision(a)
			cells[i] = []string{m.st.icon(state), a.Risk, a.Where, m.st.state(state, word), a.Title, m.st.dim(a.ID)}
		}
		laid := m.laidList(p, askColumns, dimmed(m.st, askColumnNames), cells, -1)
		for i, a := range asks {
			p.boxLaid(&rowRef{kind: rowAsk, id: a.ID, ticket: t.id}, laid[i])
			at := len(p.lines)
			m.addAskText(p, t, a)
			for k := at; k < len(p.lines); k++ {
				p.lines[k].box = boxMid
			}
		}
	}
	p.boxEnd()
}

// findingWord is what became of a finding, in a word.
func findingWord(f statusFinding) string {
	switch f.Status {
	case "open":
		return "to fix"
	case "noted":
		return "noted"
	case "dismissed":
		return "dismissed"
	}
	if f.Decision != "" {
		return f.Decision
	}
	return "ask"
}

// findingState is the state a finding is drawn in.
func findingState(f statusFinding) string {
	switch f.Status {
	case "open":
		return StateBuilding
	case "noted":
		return StateQueued
	case "dismissed":
		return StateDismissed
	}
	return StateAsk
}
