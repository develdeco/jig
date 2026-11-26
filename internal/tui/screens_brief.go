package tui

import (
	"strconv"
	"strings"
)

// briefPage is the brief: one list of its parts, the sections, then the slices,
// then the approval, one cursor down them, the detail of the part it is on, and
// the brief's activity. A section's detail has its heading, the slices that cite
// it (rows that open), whether it changed since the approval and the text the
// brief read gives, drawn as markdown; a slice's its goal and oracle whole, where
// it builds, what it waits for and the sections that cite it; the approval's who,
// when, how and which brief. A section and the approval open nothing, and a slice
// opens its slice screen.
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
	if b == nil {
		p.add("")
		p.add(m.st.dim("this ticket has no brief"))
		return p
	}

	slices := t.slices(m.zone)
	goals := t.goals()
	issues := m.issuesOfTask(id)
	hasIllustration := t.brief != nil && t.brief.Illustration
	rows := make([]listRow, 0, len(issues)+len(b.Sections)+len(slices)+1)
	for _, issue := range issues {
		rows = append(rows, m.issueRow(t, issue))
	}
	for i, s := range b.Sections {
		stands := m.st.dim("no slice")
		if len(s.Slices) > 0 {
			stands = "slice " + strings.Join(s.Slices, ", ")
		}
		if s.Since == sinceChangedWord {
			stands += " · " + m.st.state(StateFindings, "changed")
		}
		// A section is told by its place: headings may repeat.
		rows = append(rows, listRow{ref: &rowRef{kind: rowSection, id: sectionID(i), ticket: id}, cells: []string{"section", s.Heading, stands}})
	}
	for _, s := range slices {
		part := s.ID
		if g := goals[s.ID]; g != "" {
			part += "  " + m.st.dim(g)
		}
		rows = append(rows, listRow{
			ref:   &rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID},
			cells: []string{"slice", part, m.st.icon(s.State) + " " + m.st.state(s.State, s.State)},
		})
	}
	if hasIllustration {
		rows = append(rows, m.illustrationRow(t))
	}
	rows = append(rows, listRow{ref: &rowRef{kind: rowApproval, id: "approval", ticket: id}, cells: []string{"approval", "the approval of the brief", m.approvalStands(b)}})
	list := listSpec{title: partsTitle, summary: strconv.Itoa(len(rows)) + " " + plural(len(rows), "part", "parts"), cols: partColumns, names: partColumnNames, rows: rows, empty: "no parts yet"}
	var detail *detailSpec
	at := m.cursorAmong(list)
	if len(rows) > 0 {
		var d detailSpec
		switch {
		case at < len(issues):
			d = m.issueDetail(t, issues[at])
		case at-len(issues) < len(b.Sections):
			d = m.sectionDetail(t, at-len(issues))
		case at-len(issues) < len(b.Sections)+len(slices):
			d = m.briefSliceDetail(t, at-len(issues)-len(b.Sections))
		case hasIllustration && at-len(issues) == len(b.Sections)+len(slices):
			d = m.illustrationDetail(t)
		default:
			d = m.approvalDetail(t, step)
		}
		detail = &d
	}
	p.add("")
	m.listAndDetail(&p, detail, at, list)

	m.stepActivityPanel(&p, t, stepBrief)
	return p
}

// briefPartOf is the part of the ticket's brief the sub-step key is about: brief
// drafted is the first section, slices written the first slice, brief approved
// the approval. ok is false when the part does not exist (no section yet, no
// slice), and the brief then opens on its first row.
func (m *Model) briefPartOf(ticket, key string) (rowIdentity, bool) {
	t := m.w.tickets[ticket]
	if t == nil || t.status == nil || t.status.Brief == nil {
		return rowIdentity{}, false
	}
	switch key {
	case "drafted":
		if len(t.status.Brief.Sections) > 0 {
			return rowIdentity{kind: rowSection, id: sectionID(0)}, true
		}
	case "slices":
		if s := t.slices(m.zone); len(s) > 0 {
			return rowIdentity{kind: rowSlice, id: s[0].ID}, true
		}
	case "approved":
		return rowIdentity{kind: rowApproval, id: "approval"}, true
	}
	return rowIdentity{}, false
}

// partsTitle is the title of the brief screen's list of its parts.
const partsTitle = "Parts"

// sectionID is the identity of the brief's section at i, told by its place in
// the brief.
func sectionID(i int) string { return "section-" + strconv.Itoa(i+1) }

// What the status's brief block says: no approval yet, and of a section whose
// text was edited since the approval or is new, that it changed.
const (
	briefNone        = "none"
	sinceChangedWord = "changed"
)

// The columns of the brief's list: what the part is, its name and where it
// stands. A slice's name carries its goal, and a section's place says which
// slices cite it and whether it changed since the approval.
var (
	partColumns     = []column{{}, {free: true, gap: 2}, {gap: 2}}
	partColumnNames = []string{"kind", "part", "stands at"}
)

// sectionDetail is the detail of the brief's section at i: its heading, the
// slices that cite it with the state each is in, whether it changed since the
// approval and the text the brief read gives of it, drawn whole.
func (m *Model) sectionDetail(t *ticket, i int) detailSpec {
	s := t.status.Brief.Sections[i]
	d := detailSpec{title: s.Heading, summary: "no slice cites it", walk: true}
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
		d.rows = append(d.rows, detailRow{label: label, icon: states[id], text: id + " · " + firstOf(states[id], "not read"), ref: &rowRef{kind: rowSlice, id: id, ticket: t.id, slice: id}})
		label = ""
	}
	// Only an approval has something to be changed since.
	if t.status.Brief.Approval != briefNone {
		switch s.Since {
		case "same":
			d.rows = append(d.rows, detailRow{label: "approval", text: "this section is as it was approved"})
		case sinceChangedWord:
			d.rows = append(d.rows, detailRow{label: "approval", text: "this section changed since the approval", state: StateFindings})
		default:
			d.rows = append(d.rows, detailRow{label: "approval", text: "which sections changed was not recorded for this approval", dim: true})
		}
	}
	labelW := 0
	for _, r := range d.rows {
		labelW = max(labelW, width(r.label))
	}
	labelW = max(labelW, width("text"))
	text := detailRow{label: "text"}
	at, read := t.brief.sectionAt(i, s.Heading)
	switch {
	case t.brief == nil:
		text.text, text.dim = "reading the brief's text", true
	case t.brief.Err != "":
		text.text, text.dim = "the brief's text is not available: "+t.brief.Err, true
	case !read:
		text.text, text.dim = "the brief's text has no section of this heading", true
	case strings.TrimSpace(t.brief.Sections[at].Body) != "":
		text.lines = t.brief.rendered(at, m.detailRoom(labelW), m.st)
	default:
		text.text, text.dim = "this section has no text", true
	}
	d.rows = append(d.rows, text)
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
