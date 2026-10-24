package tui

import (
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// publishPage is the publish: the pull request body jig writes, the person's
// approval of the reviewed head, the pull request, and the publish's
// activity.
func (m *Model) publishPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	step := m.steps(t)[stepPublish]
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))
	p.add("")

	pub := t.publish()
	check := func(done bool, text, when string) {
		state := StateQueued
		if done {
			state = StateOK
		} else {
			text = m.st.dim(text)
		}
		if when != "" {
			text += m.st.dim("  " + when)
		}
		p.add(m.st.icon(state) + " " + text)
	}
	at := func(event string) string {
		if i := t.latest("", event); i >= 0 {
			return clock(t.lines[i], m.zone)
		}
		return ""
	}
	if pub.BodyFor != "" {
		check(true, "body written from the brief, the slices and the review, for "+commitWords(t, pub.BodyFor), at(journal.EventPrBody))
	} else {
		check(false, "body: jig writes it from the brief, the slices and the review", "")
	}
	if pub.ApprovedFor != "" {
		check(true, "approved by "+firstOf(m.byWhom(t, pub.ApprovedBy, ""), "someone")+" for "+short(pub.ApprovedFor), at(journal.EventPublishApproved))
	} else {
		check(false, "approval: yours, of the reviewed head (p)", "")
	}
	if pub.Branch != "" {
		check(pub.PR != "", "branch "+pub.Branch+" pushed", "")
	} else {
		check(false, "branch pushed", "")
	}
	if pub.PR != "" {
		check(true, strings.TrimSpace("pull request "+firstOf(pub.PROutcome, "recorded")+"  "+pub.PR), at(journal.EventPr))
	} else {
		check(false, "pull request opened", "")
	}

	p.add("")
	m.activityPanel(&p, t, stepPublish)
	return p
}
