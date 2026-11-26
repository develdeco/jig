package tui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// reviewPage is a ticket's review: a list of its gate rounds and a list of the
// asks that wait for a person, the detail of the row the cursor is on, the probes
// the rounds recorded and the review's activity. A round's detail names its
// reviewer with the harness and model the session runs, what it read and each
// finding it raised with the fix slice it became; an ask's is its finding whole.
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
	rounds = withStatusRounds(t, rounds)
	asks := t.asks()
	var detail *detailSpec
	roundList, askList := m.roundsSpec(t, rounds), m.asksSpec(t, asks)
	// A search narrows each list over all its rows, before the Detail is chosen.
	rounds, roundList = narrowList(m, &p, rounds, roundList)
	asks, askList = narrowList(m, &p, asks, askList)
	at := m.cursorAmong(roundList, askList)
	if len(rounds)+len(asks) > 0 {
		var d detailSpec
		if at < len(rounds) {
			d = m.roundDetail(t, rounds[at])
		} else {
			d = m.askDetail(t, asks, at-len(rounds))
		}
		detail = &d
	}
	m.listAndDetail(&p, detail, at, roundList, askList)
	m.probeRows(&p, t)

	m.stepActivityPanel(&p, t, stepReview)
	return p
}

// withStatusRounds is rounds with a row for each round the status names that the
// journal has no row for (a ticket whose journal is under an earlier name), so
// what the status carries of a round is shown under it; the rows stay newest first
// by round.
func withStatusRounds(t *ticket, rounds []roundRow) []roundRow {
	if t.status == nil {
		return rounds
	}
	have := map[int]bool{}
	for _, r := range rounds {
		have[r.N] = true
	}
	added := false
	for _, rec := range t.status.Rounds {
		if have[rec.N] {
			continue
		}
		state := StateFindings
		if rec.Verdict == "clean" {
			state = StateReady
		}
		rounds = append(rounds, roundRow{N: rec.N, State: state})
		added = true
	}
	if added {
		sort.SliceStable(rounds, func(i, j int) bool { return rounds[i].N > rounds[j].N })
	}
	return rounds
}

// The columns of the review screen's lists.
var (
	roundListColumns     = []column{{}, {gap: 1}, {gap: 2}, {free: true, gap: 2}, {gap: 2}, {gap: 2}}
	roundListColumnNames = []string{"", "round", "verdict", "asked", "range", "at"}
	probeListColumns     = []column{{}, {gap: 1}, {gap: 2}, {gap: 2}}
	probeListColumnNames = []string{"", "round", "where", "outcome"}
	askColumns           = []column{{}, {gap: 1}, {gap: 2}, {gap: 2}, {free: true, gap: 2}, {gap: 2}}
	askColumnNames       = []string{"", "risk", "where", "decision", "title", "id"}
)

// roundsSpec is the Rounds list, newest first: a row each with its verdict, what
// it raised, the range it read and when. Who reviewed it and what they read are
// the detail's.
func (m *Model) roundsSpec(t *ticket, rounds []roundRow) listSpec {
	rows := make([]listRow, len(rounds))
	for i, r := range rounds {
		v := m.roundOf(t, r)
		rows[i] = listRow{
			ref:   &rowRef{kind: rowRound, id: strconv.Itoa(r.N), ticket: t.id},
			cells: []string{m.st.icon(v.state), "round " + strconv.Itoa(r.N), m.st.state(v.state, v.word), m.st.dim(v.asked), m.st.dim(v.rng), m.st.dim(r.At)},
		}
	}
	return listSpec{
		title: "Rounds", summary: strconv.Itoa(len(rounds)) + " " + plural(len(rounds), "round", "rounds"),
		cols: roundListColumns, names: roundListColumnNames, rows: rows,
		empty: "no round yet: a fresh session reviews the change once every slice is green",
	}
}

// asksSpec is the Asks list: the findings that wait for a person, a row each with
// its decision. Their text is the detail's, whole.
func (m *Model) asksSpec(t *ticket, asks []askRow) listSpec {
	rows := make([]listRow, len(asks))
	for i, a := range asks {
		state, word := askDecision(a)
		rows[i] = listRow{
			ref:   &rowRef{kind: rowAsk, id: a.ID, ticket: t.id},
			cells: []string{m.st.icon(state), a.Risk, a.Where, m.st.state(state, word), a.Title, m.st.dim(a.ID)},
		}
	}
	spec := listSpec{title: "Asks", summary: strconv.Itoa(len(asks)), cols: askColumns, names: askColumnNames, rows: rows, room: len(asks) + 2}
	switch {
	case t.status == nil:
		spec.empty = "reading the ticket's status"
	case len(asks) == 0:
		spec.empty = "nothing: no finding waits for a person"
	}
	return spec
}

// roundView is a round as the screens read it: how it stands, the words for it,
// what the status records of it and who reviewed it.
type roundView struct {
	state, word string
	rec         *statusRound
	reviewer    string
	who         []string
	asked, rng  string
	// read is the range with its scope, "full" or "delta".
	read string
}

// roundOf reads a round: its verdict, the status's row for it (none for a round
// that is open, whatever a report written an instant before its closing line
// says), the session that reviewed it and the words that name them.
func (m *Model) roundOf(t *ticket, r roundRow) roundView {
	v := roundView{word: map[string]string{StateReviewing: "open", StateFindings: "findings", StateReady: "clean"}[r.State]}
	if r.State == StateReady && r.Carried > 0 {
		v.word += " (carried over from round " + strconv.Itoa(r.Carried) + ")"
	}
	v.state = r.State
	if v.state == StateReady {
		v.state = StateOK
	}
	if t.status != nil && r.State != StateReviewing {
		for i := range t.status.Rounds {
			if t.status.Rounds[i].N == r.N {
				v.rec = &t.status.Rounds[i]
			}
		}
	}
	rec := v.rec
	if rec != nil && rec.Verdict == "not-independent" {
		v.state, v.word = StateStalled, "not clean"
	}
	if rec != nil {
		v.asked = roundCounts(*rec)
		v.rng = shortRange("", rec.Base, rec.Head)
		v.read = shortRange(rec.Scope, rec.Base, rec.Head)
	}

	v.reviewer = r.Reviewer
	if v.reviewer == "" && rec != nil {
		v.reviewer = rec.Reviewer
	}
	if v.reviewer == "" {
		v.reviewer = t.reviewerOf(r.N)
	}
	switch {
	case v.reviewer != "" && r.State == StateReviewing:
		v.who = append(v.who, m.reviewerWho(t, v.reviewer, r.N, "", rec)+" took it")
	case v.reviewer != "":
		v.who = append(v.who, m.reviewerWho(t, v.reviewer, r.N, "", rec))
	case r.State == StateReviewing:
		v.who = append(v.who, "waiting for a reviewer to take it")
	}
	if rec != nil {
		switch rec.Independent {
		case "true":
			v.who = append(v.who, "independent")
		case "false":
			// A named reviewer already reads "builder reviewing its own change".
			if v.reviewer == "" {
				v.who = append(v.who, "NOT independent: it built what it reviewed")
			}
		case "unknown":
			v.who = append(v.who, "independence unknown")
		}
	}
	if v.reviewer != "" {
		v.who = append(v.who, shortID(v.reviewer))
	}
	return v
}

// roundDetail is the detail of a round: who reviewed it, the range and the files
// they read, and each finding the status carries of it with what became of it and
// the fix slice it was sent back as. A round the status carries no findings of
// says its counts. Its probes are not here.
func (m *Model) roundDetail(t *ticket, r roundRow) detailSpec {
	v := m.roundOf(t, r)
	d := detailSpec{title: "round " + strconv.Itoa(r.N), summary: v.word}
	add := func(row detailRow) { d.rows = append(d.rows, row) }
	if len(v.who) > 0 {
		add(detailRow{label: "reviewed by", text: strings.Join(v.who, " · ")})
	}
	if rec := v.rec; rec != nil {
		read := v.read
		if rec.Files >= 0 {
			files := strconv.Itoa(rec.Files) + " " + plural(rec.Files, "file", "files") + " read"
			read = strings.TrimPrefix(read+" · "+files, " · ")
		}
		if read != "" {
			add(detailRow{label: "range", text: read})
		}
	}
	states := map[string]string{}
	for _, s := range t.slices(m.zone) {
		states[s.ID] = s.State
	}
	// The fix slices each finding became, by the finding ids the slices record.
	became := map[string][]string{}
	if t.status != nil {
		for _, s := range t.status.Slices {
			for _, f := range s.Findings {
				became[f] = append(became[f], s.ID)
			}
		}
	}
	if r.State == StateReady && r.Carried > 0 {
		// A round that carries an earlier round's verdict names it, and enter goes there.
		n := strconv.Itoa(r.Carried)
		add(detailRow{label: "carried", text: "carried over from round " + n, ref: &rowRef{kind: rowCarry, id: n, ticket: t.id}})
	}
	shown := map[string]bool{}
	label := "findings"
	if t.status != nil {
		for _, f := range t.status.Findings {
			if f.Round != r.N {
				continue
			}
			ref := &rowRef{kind: rowFinding, id: f.ID, ticket: t.id, round: strconv.Itoa(f.Round)}
			add(detailRow{label: label, icon: findingState(f), ref: ref, text: strings.TrimSpace(f.Risk + " " + f.Where + " " + f.Title + " → " + findingWord(f))})
			label = ""
			if f.Rationale != "" {
				add(detailRow{indent: 2, dim: true, text: f.Rationale})
			}
			for _, id := range became[f.ID] {
				shown[id] = true
				add(detailRow{indent: 2, icon: states[id], text: "fix slice " + id + " · " + states[id], ref: &rowRef{kind: rowSlice, id: id, ticket: t.id, slice: id, container: f.ID}})
			}
		}
	}
	if label == "findings" && v.asked != "" {
		// The status carries no findings of this round: what it raised, in counts.
		add(detailRow{label: "raised", text: v.asked})
	}
	// What the round sent back that no finding above names: its fix slices, by the
	// round they came from.
	label = "sent back"
	if t.status != nil {
		for _, s := range t.status.Slices {
			if shown[s.ID] || t.fixRound(s.ID) != r.N {
				continue
			}
			add(detailRow{label: label, icon: states[s.ID], text: s.ID + " · " + states[s.ID], ref: &rowRef{kind: rowSlice, id: s.ID, ticket: t.id, slice: s.ID, container: "sent back"}})
			label = ""
		}
	}
	return d
}

// askDetail is the detail of the ask at i of asks: its finding whole, the risk,
// the place, the title, the reason the reviewer gave, why it waits for a person
// and what has been decided of it so far. It is the text a person decides from,
// so each of its lines carries the ask.
func (m *Model) askDetail(t *ticket, asks []askRow, i int) detailSpec {
	a := asks[i]
	state, word := askDecision(a)
	d := detailSpec{title: a.ID, summary: word, ask: a.ID}
	add := func(row detailRow) { d.rows = append(d.rows, row) }
	add(detailRow{label: "finding", text: strings.Join([]string{a.Risk, a.Where, a.Title}, " · ")})
	if t.status != nil {
		for _, f := range t.status.Findings {
			if f.ID == a.ID && f.Rationale != "" {
				add(detailRow{label: "reason", text: f.Rationale})
			}
		}
	}
	if a.Why != "" && a.Why != "-" {
		add(detailRow{label: "waits because", text: a.Why})
	}
	add(detailRow{label: "decision", icon: state, text: word, apart: true})
	return d
}

// probeCounts is what the probes say of themselves: "27 probes · 25 caught · 2
// survived".
func probeCounts(probes []statusProbe) string {
	survived := 0
	for _, pr := range probes {
		if pr.Outcome == "survived" {
			survived++
		}
	}
	return strings.Join([]string{
		strconv.Itoa(len(probes)) + " " + plural(len(probes), "probe", "probes"),
		strconv.Itoa(len(probes)-survived) + " caught",
		strconv.Itoa(survived) + " survived",
	}, " · ")
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
	p.add(m.st.dim(probeCounts(probes) + " · as recorded: a file changed since a round is not marked"))
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
			p.boxUnder("    " + l)
		}
		for _, l := range wrap(pr.Seam+" · "+pr.Command, m.innerWidth()-6, probeLines) {
			p.boxUnder("    " + m.st.dim(l))
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
		return f.Decision + decidedBy(askRow{DecidedBy: f.DecidedBy})
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
