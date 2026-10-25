package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// fixRow is a fix slice a round sent back: its id and its row.
type fixRow struct {
	id, line string
}

// fixRows are the fix slices the rounds sent back, oldest first: the state
// each is in, the round, and the finding it fixes.
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
		line := m.st.icon(state) + " " + l.Slice + m.st.dim("  round "+strconv.Itoa(max(1, l.Attempt))+"  ") + t.goals()[l.Slice]
		out = append(out, fixRow{id: l.Slice, line: line})
	}
	return out
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
	for _, r := range rounds {
		m.roundRecord(&p, t, r)
	}

	asks := t.asks()
	asking := map[string]bool{}
	for _, a := range asks {
		asking[a.ID] = true
	}
	m.findingRows(&p, t, asking)

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
	rationale := map[string]string{}
	if t.status != nil {
		for _, f := range t.status.Findings {
			rationale[f.ID] = f.Rationale
		}
	}
	for i, line := range m.askLines(asks) {
		p.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
		var why []string
		if r := rationale[asks[i].ID]; r != "" {
			why = append(why, r)
		}
		if w := asks[i].Why; w != "" && w != "-" {
			why = append(why, w)
		}
		for _, l := range wrap(strings.Join(why, " · "), m.width-8, 2) {
			p.add("    " + m.st.dim(l))
		}
	}

	p.add("")
	m.activityPanel(&p, t, stepReview)
	return p
}

// roundRecord adds a round to the page: its line, who reviewed it and whether
// they stood apart from the builders, and what range of the change it read.
func (m *Model) roundRecord(p *page, t *ticket, r roundRow) {
	word := map[string]string{StateReviewing: "open", StateFindings: "findings", StateReady: "clean"}[r.State]
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
			who = append(who, "NOT independent: it built what it reviewed")
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
