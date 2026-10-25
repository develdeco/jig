package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// briefPage is the brief: the record of its approval, its sections with the
// first lines of each and the slices that cite it, the slices it was cut into
// with the workspace and oracle of each, and the brief's activity.
//
// The sections' words come from `jig brief show --text` (brieftext.go) and the
// rest from the status, so a status that lists no text still shows the headings.
func (m *Model) briefPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	if t.status == nil {
		p.add(m.st.dim("reading the ticket's status"))
		return p
	}
	b := t.status.Brief
	step := m.steps(t)[stepBrief]
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))
	for _, line := range m.approvalRecord(t, b) {
		p.add(m.st.dim("  " + line))
	}
	if b == nil {
		p.add("")
		p.add(m.st.dim("this ticket has no brief"))
		return p
	}
	if t.brief != nil && t.brief.Illustration {
		p.add(m.st.dim("  it has an illustration: `jig brief show " + id + "` serves it"))
	}

	p.add("")
	p.add(m.st.bold("sections") + m.st.dim("  cited by"))
	switch {
	case t.brief == nil:
		p.add(m.st.dim("reading the brief's text"))
	case t.brief.Err != "":
		p.add(m.st.dim("the brief's text is not available: " + t.brief.Err))
	}
	headW := 0
	for _, s := range b.Sections {
		headW = max(headW, width(s.Heading))
	}
	headW = min(headW, max(12, m.width/2))
	for i, s := range b.Sections {
		cited := m.st.dim("no slice")
		if len(s.Slices) > 0 {
			cited = "slice " + strings.Join(s.Slices, ", ")
		}
		p.addRow(rowRef{kind: rowStop, id: s.Heading, ticket: id}, pad(s.Heading, headW)+"  "+cited)
		for _, line := range wrap(t.brief.summaryOf(i, s.Heading), m.width-8, 2) {
			p.add("    " + m.st.dim(line))
		}
	}

	p.add("")
	p.add(m.st.bold("slices"))
	for _, s := range t.slices(m.zone) {
		line := m.st.icon(s.State) + " " + s.ID + "  " + firstOf(t.goals()[s.ID], m.st.dim("no goal"))
		line, below := m.withFacts(line, sliceFacts(s, true))
		p.addRow(rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID}, line)
		if below != "" {
			p.add("    " + spaces(width(s.ID)) + below)
		}
	}

	p.add("")
	m.activityPanel(&p, t, stepBrief)
	return p
}

// approvalRecord is the lines that say how the brief came to be approved: when
// and by whom, how it was recorded, and which brief it was. They are empty
// for a brief nobody approved.
func (m *Model) approvalRecord(t *ticket, b *statusBrief) []string {
	if b == nil || b.ApprovedAt == "" {
		return nil
	}
	when := b.ApprovedAt
	if at, err := time.Parse(time.RFC3339, b.ApprovedAt); err == nil {
		when = at.In(m.zone).Format("2006-01-02 15:04")
	}
	first := "approved " + when
	if b.ApprovedBy != "" {
		first += " by " + b.ApprovedBy
	}
	how := ""
	switch b.Provenance {
	case "person":
		how = "at a terminal"
	case "agent":
		how = "recorded by the session that drafted it"
	case "jig":
		how = "recorded by jig"
	}
	var rest []string
	if how != "" {
		rest = append(rest, how)
	}
	if i := t.latest("", journal.EventApproval); i >= 0 && t.lines[i].Ref != "" {
		rest = append(rest, "brief "+t.lines[i].Ref)
	}
	out := []string{first}
	if len(rest) > 0 {
		out = append(out, strings.Join(rest, " · "))
	}
	return out
}

// withFacts puts facts, drawn back, at the end of a row when they fit in the
// width the screen has, else under it: the row, and what goes on the line below
// ("" when the facts are on the row or there are none).
func (m *Model) withFacts(row, facts string) (string, string) {
	if facts == "" {
		return row, ""
	}
	if 2+width(row)+2+width(facts) <= m.width {
		return row + m.st.dim("  "+facts), ""
	}
	return row, m.st.dim(facts)
}

// sliceFacts is what slices.yaml says of a slice, in a line: its workspace and
// oracle, the round a fix slice came from, and, when after is set, the slices it
// waits for.
func sliceFacts(s sliceRow, after bool) string {
	var parts []string
	if s.Workspace != "" {
		parts = append(parts, "workspace "+s.Workspace)
	}
	if s.Oracle != "" {
		parts = append(parts, "oracle "+s.Oracle)
	}
	if s.FromGate > 0 {
		parts = append(parts, "fix slice of round "+strconv.Itoa(s.FromGate))
	}
	if after && s.BlockedBy != "" {
		parts = append(parts, "after "+strings.ReplaceAll(s.BlockedBy, "+", ", "))
	}
	return strings.Join(parts, " · ")
}
