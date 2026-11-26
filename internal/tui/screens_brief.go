package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// briefPage is the brief: the record of its approval, a list of its sections (each
// with the slices that cite it) and of the slices it was cut into, one cursor
// down the sections and then the slices, the detail of the row it is on, and the
// brief's activity. A section's detail has its heading, the slices that cite it
// and the text the brief read gives; a slice's its goal and oracle whole, where
// it builds, what it waits for and the sections that cite it. A section opens
// nothing, and a slice opens its slice screen.
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
	slices := t.slices(m.zone)
	goals := t.goals()
	sections := make([]listRow, len(b.Sections))
	for i, s := range b.Sections {
		cited := m.st.dim("no slice")
		if len(s.Slices) > 0 {
			cited = "slice " + strings.Join(s.Slices, ", ")
		}
		sections[i] = listRow{ref: &rowRef{kind: rowSection, id: s.Heading, ticket: id}, cells: []string{s.Heading, cited}}
	}
	rows := make([]listRow, len(slices))
	for i, s := range slices {
		rows[i] = listRow{
			ref:   &rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID},
			cells: []string{m.st.icon(s.State), s.ID, m.st.state(s.State, s.State), firstOf(goals[s.ID], m.st.dim("no goal"))},
		}
	}
	var detail *detailSpec
	if at := m.cursorAmong(len(sections) + len(rows)); len(sections)+len(rows) > 0 {
		var d detailSpec
		if at < len(sections) {
			d = m.sectionDetail(t, at)
		} else {
			d = m.briefSliceDetail(t, at-len(sections))
		}
		detail = &d
	}
	m.listAndDetail(&p, detail,
		listSpec{title: "Sections", summary: strconv.Itoa(len(sections)) + " " + plural(len(sections), "section", "sections"), cols: sectionColumns, names: sectionColumnNames, rows: sections, empty: "no sections"},
		listSpec{title: "Slices", summary: strconv.Itoa(len(rows)) + " " + plural(len(rows), "slice", "slices"), cols: briefSliceColumns, names: briefSliceColumnNames, rows: rows, empty: "no slices yet"})

	m.stepActivityPanel(&p, t, stepBrief)
	return p
}

// The columns of the brief's lists: a section and the slices that cite it, a
// slice with its state and goal.
var (
	sectionColumns        = []column{{free: true}, {gap: 2}}
	sectionColumnNames    = []string{"section", "cited by"}
	briefSliceColumns     = []column{{}, {id: true, gap: 1}, {gap: 2}, {free: true, gap: 2}}
	briefSliceColumnNames = []string{"", "slice", "state", "goal"}
)

// sectionDetail is the detail of the brief's section at i: its heading, the
// slices that cite it with the state each is in, and the text the brief read
// gives of it, whole.
func (m *Model) sectionDetail(t *ticket, i int) detailSpec {
	s := t.status.Brief.Sections[i]
	d := detailSpec{title: s.Heading, summary: "no slice cites it"}
	if n := len(s.Slices); n > 0 {
		d.summary = "cited by " + strconv.Itoa(n) + " " + plural(n, "slice", "slices")
	}
	d.rows = append(d.rows, detailRow{label: "heading", text: s.Heading})
	states := map[string]string{}
	for _, sl := range t.slices(m.zone) {
		states[sl.ID] = sl.State
	}
	label := "cited by"
	for _, id := range s.Slices {
		d.rows = append(d.rows, detailRow{label: label, icon: states[id], text: id + " · " + firstOf(states[id], "not read")})
		label = ""
	}
	if text := t.brief.summaryOf(i, s.Heading); text != "" {
		d.rows = append(d.rows, detailRow{label: "text", text: text})
	}
	return d
}

// briefSliceDetail is the detail of the slice at i of the ticket's slices, in
// the brief's words: its goal and oracle whole, where it builds, what it waits
// for and the sections of the brief that cite it.
func (m *Model) briefSliceDetail(t *ticket, i int) detailSpec {
	row := t.slices(m.zone)[i]
	d := detailSpec{title: row.ID, summary: row.State}
	add := func(r detailRow) { d.rows = append(d.rows, r) }
	if i < len(t.status.Slices) && t.status.Slices[i].Goal != "" {
		add(detailRow{label: "goal", text: t.status.Slices[i].Goal})
	}
	if row.Oracle != "" {
		add(detailRow{label: "oracle", text: row.Oracle})
	}
	if row.Workspace != "" {
		add(detailRow{label: "workspace", text: row.Workspace})
	}
	if row.BlockedBy != "" {
		add(detailRow{label: "waits for", text: strings.ReplaceAll(row.BlockedBy, "+", ", ")})
	}
	label := "cited in"
	for _, s := range t.status.Brief.Sections {
		for _, id := range s.Slices {
			if id == row.ID {
				add(detailRow{label: label, text: s.Heading})
				label = ""
			}
		}
	}
	return d
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
