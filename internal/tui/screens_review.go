package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// fixRow is a fix slice a round sent back: its id and its row.
type fixRow struct {
	id, line string
	at       time.Time
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
		round := "  "
		if n := t.fixRound(l.Slice); n > 0 {
			round = "  round " + strconv.Itoa(n) + "  "
		}
		line := m.st.icon(state) + " " + l.Slice + m.st.dim(round) + t.goals()[l.Slice]
		at, _ := l.At()
		out = append(out, fixRow{id: l.Slice, line: line, at: at})
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
	p.add(m.st.bold("rounds"))
	rounds := t.rounds(m.zone)
	if len(rounds) == 0 {
		p.add(m.st.dim("no round yet: a fresh session reviews the change once every slice is green"))
	}
	for _, r := range newestFirst(rounds, func(r roundRow) time.Time { return r.Began }) {
		m.roundRecord(&p, t, r)
	}

	asks := t.asks()
	asking := map[string]bool{}
	for _, a := range asks {
		asking[a.ID] = true
	}
	m.findingRows(&p, t, asking)
	m.probeRows(&p, t)
	m.unprobedRows(&p, t)

	if fixes := m.fixRows(t); len(fixes) > 0 {
		p.add("")
		p.add(m.st.bold("sent back to the build"))
		for _, f := range fixes {
			p.addRow(rowRef{kind: rowSlice, id: f.id, ticket: id, slice: f.id}, f.line)
		}
	}

	p.add("")
	p.add(m.st.bold("asks"))
	switch {
	case t.status == nil:
		p.add(m.st.dim("reading the ticket's status"))
	case len(asks) == 0:
		p.add(m.st.dim("nothing: no finding waits for a person"))
	}
	for i, line := range m.askLines(asks) {
		p.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
		m.addAskText(&p, t, asks[i])
	}

	p.add("")
	m.stepActivityPanel(&p, t, stepReview)
	return p
}

// roundRecord adds a round to the page: its line, who reviewed it and whether
// they stood apart from the builders, and what range of the change it read.
func (m *Model) roundRecord(p *page, t *ticket, r roundRow) {
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
	line := m.st.icon(state) + " round " + fmt.Sprint(r.N) + "  " + m.st.state(state, pad(word, max(8, width(word))))
	var said []string
	if r.State == StateReviewing {
		said = append(said, "opened "+r.At)
	} else {
		if rec != nil {
			if counts := roundCounts(*rec); counts != "" {
				said = append(said, "raised "+counts)
			}
		}
		said = append(said, r.At)
	}
	p.add(line + m.st.dim("  "+strings.Join(said, " · ")))

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
		for _, l := range wrap(strings.Join(who, " · "), m.width-8, 2) {
			p.add("    " + m.st.dim(l))
		}
	}
	if rec != nil {
		var scope []string
		if rng := shortRange(rec.Scope, rec.Base, rec.Head); rng != "" {
			scope = append(scope, rng)
		}
		if rec.Files >= 0 {
			scope = append(scope, strconv.Itoa(rec.Files)+" "+plural(rec.Files, "file", "files")+" read")
		}
		if len(scope) > 0 {
			p.add("    " + m.st.dim(strings.Join(scope, " · ")))
		}
		if line := ledgerCounts(*rec); line != "" {
			p.add("    " + m.st.dim(line))
		}
	}
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
	p.add("")
	p.add(m.st.bold("probes"))
	p.add(m.st.dim("as recorded: a file changed since a round is not marked"))
	for _, pr := range probes {
		state := StateOK
		if pr.Outcome == "survived" {
			state = StateFindings
		}
		lead := "round " + strconv.Itoa(pr.Round) + "  "
		tail := "  " + pr.Outcome
		next := later[pr.Where]
		superseded := next.Round > pr.Round
		if superseded {
			tail += " · " + supersededBy(pr, next)
		}
		// The path gives way, never the round or the verdict.
		head := lead + tailCut(pr.Where, m.width-4-width(lead)-width(tail))
		if superseded {
			p.add(m.st.icon(state) + " " + m.st.dim(head+tail))
		} else {
			p.add(m.st.icon(state) + " " + head + m.st.state(state, tail))
		}
		for _, l := range wrap(pr.Mutation, m.width-8, probeLines) {
			if superseded {
				l = m.st.dim(l)
			}
			p.add("    " + l)
		}
		for _, l := range wrap(pr.Seam+" · "+pr.Command, m.width-8, probeLines) {
			p.add("    " + m.st.dim(l))
		}
	}
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
	var rows []string
	for _, h := range t.status.Unprobed {
		if h.Round == round {
			rows = append(rows, h.File+":"+strconv.Itoa(h.From)+"-"+strconv.Itoa(h.To))
		}
	}
	title := "round " + strconv.Itoa(round) + " left no hunk unprobed"
	if len(rows) > 0 {
		title = "round " + strconv.Itoa(round) + " left " + strconv.Itoa(len(rows)) + " " + plural(len(rows), "hunk", "hunks") + " unprobed"
	}
	p.add("")
	p.add(m.st.bold(title))
	for _, r := range rows {
		p.add("  " + r)
	}
	for _, s := range t.status.ProbeSkips {
		if s.Round != round {
			continue
		}
		where := s.File
		if s.Range != "" && s.Range != "whole file" {
			where += ":" + s.Range
		}
		p.add("  " + where + m.st.dim("  set aside: "+s.Why))
	}
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
	p.add("")
	p.add(m.st.bold("round " + strconv.Itoa(rows[0].Round) + " findings"))
	riskW, whereW, wordW := 0, 0, 0
	words := make([]string, len(rows))
	for i, f := range rows {
		words[i] = findingWord(f)
		riskW, whereW, wordW = max(riskW, width(f.Risk)), max(whereW, width(f.Where)), max(wordW, width(words[i]))
	}
	for i, f := range rows {
		state := findingState(f)
		line := fmt.Sprintf("%s %s  %s  %s  %s%s", m.st.icon(state), pad(f.Risk, riskW), pad(f.Where, whereW),
			m.st.state(state, pad(words[i], wordW)), f.Title, m.st.dim("  "+f.ID))
		p.addRow(rowRef{kind: rowStop, id: f.ID, ticket: t.id}, line)
		for _, l := range wrap(f.Rationale, m.width-8, 2) {
			p.add("    " + m.st.dim(l))
		}
	}
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
