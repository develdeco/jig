package tui

import (
	"fmt"
	"strings"
)

// recentLines is how many of a ticket's latest lines its screen shows.
const recentLines = 6

// ticketPage is one ticket: where it stands in build, review and publish, what
// waits for a person, what to do next, and what happened last.
func (m *Model) ticketPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	bs, btext := m.buildSummary(t)
	rs, rtext := m.reviewSummary(t)
	ps, ptext := m.publishSummary(t)
	p.addRow(rowRef{kind: rowBuild, ticket: id}, m.st.icon(bs)+" "+pad("build", 9)+btext)
	p.addRow(rowRef{kind: rowReview, ticket: id}, m.st.icon(rs)+" "+pad("review", 9)+rtext)
	p.addRow(rowRef{kind: rowPublish, ticket: id}, m.st.icon(ps)+" "+pad("publish", 9)+ptext)

	qs := t.openQuestions(m.zone, "")
	asks := t.asks()
	if len(qs)+len(asks) > 0 {
		p.add("")
		p.add(m.st.bold("needs a person"))
		for _, q := range qs {
			p.addRow(rowRef{kind: rowQuestion, id: q.ID, ticket: id, slice: q.Slice},
				m.st.icon(StateNeedsInput)+" "+q.ID+"  "+m.st.dim("slice "+q.Slice)+"  "+firstLine(q.Text))
		}
		for i, line := range m.askLines(asks) {
			p.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
		}
	}

	if t.status != nil && len(t.status.Help) > 0 {
		p.add("")
		p.add(m.st.dim("next  ") + t.status.Help[0])
	}

	if n := len(t.lines); n > 0 {
		p.add("")
		p.add(m.st.bold("recent"))
		from := max(0, n-recentLines)
		for _, l := range t.lines[from:] {
			p.add(m.st.dim(clock(l, m.zone)) + " " + m.st.icon(eventState(l)) + " " + describe(l) + sliceTag(l.Slice, m))
		}
	}
	return p
}

// sliceTag is a dim " [slice]" after a line about a slice.
func sliceTag(slice string, m *Model) string {
	if slice == "" {
		return ""
	}
	return m.st.dim("  [" + slice + "]")
}

// firstLine is the first line of text.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// askLines are the asks as rows with their columns aligned: the icon (the
// ask's decision), id, risk, place and title, and what has been decided.
func (m *Model) askLines(asks []askRow) []string {
	idW, riskW, whereW := 0, 0, 0
	for _, a := range asks {
		idW, riskW, whereW = max(idW, width(a.ID)), max(riskW, width(a.Risk)), max(whereW, width(a.Where))
	}
	out := make([]string, len(asks))
	for i, a := range asks {
		state := StateAsk
		decision := m.st.dim("undecided")
		switch a.Decision {
		case "keep":
			state, decision = StateKept, m.st.state(StateKept, "keep"+decidedBy(a))
		case "dismiss":
			state, decision = StateDismissed, m.st.state(StateDismissed, "dismiss"+decidedBy(a))
		}
		out[i] = fmt.Sprintf("%s %s  %s  %s  %s  %s", m.st.icon(state), pad(a.ID, idW), pad(a.Risk, riskW), pad(a.Where, whereW), a.Title, decision)
	}
	return out
}

func decidedBy(a askRow) string {
	if a.DecidedBy == "" {
		return ""
	}
	return " (" + a.DecidedBy + ")"
}

// buildSummary is the build row: how many slices are green and what holds up
// the rest.
func (m *Model) buildSummary(t *ticket) (state, text string) {
	slices := t.slices(m.zone)
	if len(slices) == 0 {
		return StateQueued, "no slices yet"
	}
	counts := map[string]int{}
	for _, s := range slices {
		counts[s.State]++
	}
	state = StateQueued
	switch {
	case counts[StateStalled] > 0:
		state = StateStalled
	case counts[StateEnvBlocked] > 0:
		state = StateEnvBlocked
	case counts[StateNeedsInput] > 0:
		state = StateNeedsInput
	case counts[StateVerifying] > 0:
		state = StateVerifying
	case counts[StateBuilding] > 0:
		state = StateBuilding
	case counts[StateGreen] == len(slices):
		state = StateGreen
	case counts[StateGreen] > 0:
		state = StateBuilding
	}
	text = fmt.Sprintf("%d of %d slices green", counts[StateGreen], len(slices))
	for _, s := range []struct{ state, word string }{
		{StateNeedsInput, "needs input"}, {StateStalled, "stalled"}, {StateEnvBlocked, "env blocked"},
		{StateVerifying, "verifying"}, {StateBuilding, "building"},
	} {
		if n := counts[s.state]; n > 0 {
			text += fmt.Sprintf(", %d %s", n, s.word)
		}
	}
	return state, text
}

// reviewSummary is the review row: the latest gate round and how it ended.
func (m *Model) reviewSummary(t *ticket) (state, text string) {
	rounds := t.rounds(m.zone)
	if len(rounds) == 0 {
		return StateQueued, "no round yet"
	}
	r := rounds[len(rounds)-1]
	switch r.State {
	case StateReviewing:
		return StateReviewing, fmt.Sprintf("round %d is open: waiting for the reviewer", r.N)
	case StateReady:
		return StateReady, fmt.Sprintf("round %d: clean", r.N)
	}
	text = fmt.Sprintf("round %d: findings", r.N)
	if asks := t.asks(); len(asks) > 0 {
		text += fmt.Sprintf(", %d %s", len(asks), plural(len(asks), "ask", "asks"))
	}
	return StateFindings, text
}

// publishSummary is the publish row.
func (m *Model) publishSummary(t *ticket) (state, text string) {
	pub := t.publish()
	switch {
	case pub.Done:
		return StatePublished, strings.TrimSpace("published " + pub.PR)
	case pub.PR != "":
		return StatePublishing, strings.TrimSpace("pull request " + pub.PROutcome + " " + pub.PR)
	case pub.ApprovedFor != "":
		return StatePublishing, "approved by " + firstOf(pub.ApprovedBy, "?") + " at " + short(pub.ApprovedFor)
	case pub.BodyFor != "":
		return StatePublishing, "body rendered for " + short(pub.BodyFor)
	}
	return StateQueued, "not yet"
}
