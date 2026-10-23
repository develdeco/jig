package tui

import (
	"fmt"
	"strings"
)

// reviewPage is a ticket's review and publish: the gate rounds, the asks a
// round left for a person and what has been decided about each, and how far
// the publish has got.
func (m *Model) reviewPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}

	p.add(m.st.bold("rounds"))
	rounds := t.rounds(m.zone)
	if len(rounds) == 0 {
		p.add(m.st.dim("no round yet: g opens one once the slices are green"))
	}
	for _, r := range rounds {
		word := map[string]string{StateReviewing: "open", StateFindings: "findings", StateReady: "clean"}[r.State]
		line := m.st.icon(r.State) + " round " + fmt.Sprint(r.N) + "  " + m.st.state(r.State, pad(word, 8))
		switch {
		case r.State == StateReviewing:
			line += m.st.dim("  opened " + r.At + ", waiting for the reviewer")
		case r.Reviewer != "":
			line += m.st.dim("  reviewed by " + r.Reviewer + " at " + r.At)
		default:
			line += m.st.dim("  " + r.At)
		}
		p.add(line)
	}

	p.add("")
	p.add(m.st.bold("asks"))
	asks := t.asks()
	switch {
	case t.status == nil:
		p.add(m.st.dim("reading the ticket's status"))
	case len(asks) == 0:
		p.add(m.st.dim("no ask is waiting for a decision"))
	}
	for i, line := range m.askLines(asks) {
		p.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
		if why := asks[i].Why; why != "" && why != "-" {
			p.add("    " + m.st.dim(why))
		}
	}

	p.add("")
	p.add(m.st.bold("publish"))
	pub := t.publish()
	body := "body not rendered"
	bodyState := StateQueued
	if pub.BodyFor != "" {
		body, bodyState = "body rendered for "+short(pub.BodyFor), StatePublishing
	}
	approved, approvedState := "not approved", StateQueued
	if pub.ApprovedFor != "" {
		approved, approvedState = "approved by "+firstOf(pub.ApprovedBy, "?")+" at "+short(pub.ApprovedFor), StatePublishing
	}
	pr, prState := "no pull request", StateQueued
	if pub.PR != "" {
		pr, prState = strings.TrimSpace("pull request "+pub.PROutcome+" "+pub.PR), StatePublishing
	}
	if pub.Done {
		prState = StatePublished
	}
	p.add(m.st.icon(bodyState) + " " + body)
	p.add(m.st.icon(approvedState) + " " + approved)
	p.add(m.st.icon(prState) + " " + pr)
	return p
}
