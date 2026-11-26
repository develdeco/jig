package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
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
	switch {
	case t.brief == nil:
		p.add(m.st.dim("reading the brief's text"))
	case t.brief.Err != "":
		p.add(m.st.dim("the brief's text is not available: " + t.brief.Err))
	}
	cells := make([][]string, len(b.Sections))
	for i, s := range b.Sections {
		cited := m.st.dim("no slice")
		if len(s.Slices) > 0 {
			cited = "slice " + strings.Join(s.Slices, ", ")
		}
		cells[i] = []string{s.Heading, cited}
	}
	p.boxTop("Sections", strconv.Itoa(len(b.Sections)))
	if len(cells) == 0 {
		p.boxAdd(m.st.dim("no sections"))
	} else {
		refs := make([]*rowRef, len(b.Sections))
		for i, s := range b.Sections {
			refs[i] = &rowRef{kind: rowStop, id: s.Heading, ticket: id}
		}
		laid := m.laidListFor(&p, sectionColumns, dimmed(m.st, sectionColumnNames), cells, 0, refs)
		for i, s := range b.Sections {
			p.boxRow(refs[i], laid[i])
			m.underWrap(&p, refs[i], "    ", "    ", t.brief.summaryOf(i, s.Heading), m.innerWidth()-6, 2, m.st.dim)
		}
	}
	p.boxEnd()

	slices := t.slices(m.zone)
	idW := width(briefSliceColumnNames[1])
	cells = make([][]string, len(slices))
	for i, s := range slices {
		idW = max(idW, width(s.ID))
		cells[i] = []string{m.st.icon(s.State), s.ID, firstOf(t.goals()[s.ID], m.st.dim("no goal"))}
	}
	p.boxTop("Slices", strconv.Itoa(len(slices)))
	if len(cells) == 0 {
		p.boxAdd(m.st.dim("no slices yet"))
	} else {
		refs := make([]*rowRef, len(slices))
		for i, s := range slices {
			refs[i] = &rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID}
		}
		laid := m.laidListFor(&p, briefSliceColumns, dimmed(m.st, briefSliceColumnNames), cells, 0, refs)
		for i, s := range slices {
			p.boxRow(refs[i], laid[i])
			if facts := sliceFacts(s, true); facts != "" {
				p.boxUnder("    " + spaces(idW) + m.st.dim(facts))
			}
		}
	}
	p.boxEnd()

	m.stepActivityPanel(&p, t, stepBrief)
	return p
}

// The columns of the brief's lists: a section and the slices that cite it, a
// slice and its goal.
var (
	sectionColumns        = []column{{free: true}, {gap: 2}}
	sectionColumnNames    = []string{"section", "cited by"}
	briefSliceColumns     = []column{{}, {id: true, gap: 1}, {free: true, gap: 2}}
	briefSliceColumnNames = []string{"", "slice", "goal"}
)

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
		first += " by " + phase.Approver(b.ApprovedBy, b.Provenance)
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
