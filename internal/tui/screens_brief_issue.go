package tui

import (
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A task of an issue has the issue as the first part of its brief: what the
// issue is for, whole, and where the task stands in the issue's plan.

// issuesOfTask are the issues that plan the task id: the parent its digest names
// when that is an issue (a multi-task's is not), then the issues whose charts
// list it, a task that two charts list having one of each.
func (m *Model) issuesOfTask(id string) []string {
	var out []string
	if t := m.w.tickets[id]; t != nil && t.digest != nil && t.digest.Parent != "" && !isMulti(m.w.tickets[t.digest.Parent]) {
		out = append(out, t.digest.Parent)
	}
	for _, issue := range m.issuesOf(id) {
		if !slices.Contains(out, issue) {
			out = append(out, issue)
		}
	}
	return out
}

// readIssues reads the status of each issue that plans the task, for the need
// its row shows, when the screens do not hold it.
func (m *Model) readIssues(id string) []tea.Cmd {
	var cmds []tea.Cmd
	for _, issue := range m.issuesOfTask(id) {
		if t, _ := m.w.ticketFor(issue); t.status == nil {
			cmds = append(cmds, m.refreshStatus(issue))
		}
	}
	return cmds
}

// issueRow is the row of the Parts list for the issue that plans t: its id and
// title, and whether the task waits in its plan.
func (m *Model) issueRow(t *ticket, issue string) listRow {
	stands := m.st.state(StateOK, "ready")
	if n := len(t.blockers); n > 0 {
		stands = m.st.state(StateNeedsInput, "waits for "+strconv.Itoa(n))
	}
	name := issue
	if it := m.w.tickets[issue]; it != nil && it.title != "" {
		name += "  " + m.st.dim(it.title)
	}
	return listRow{ref: &rowRef{kind: rowIssue, id: issue, ticket: t.id}, cells: []string{"issue", name, stands}}
}

// issueDetail is the detail of an issue's row: its need, whole, and the task's
// place in its plan: what the task waits for, each unmet blocker with its kind
// and fact as the roster carries it, and what waits for the task.
func (m *Model) issueDetail(t *ticket, issue string) detailSpec {
	d := detailSpec{title: issue, summary: "ready"}
	if n := len(t.blockers); n > 0 {
		d.summary = "waits for " + strconv.Itoa(n)
	}
	it := m.w.tickets[issue]
	if it != nil && it.title != "" {
		d.rows = append(d.rows, detailRow{label: "issue", text: it.title})
	}
	const labelW = len("needed by")
	need := detailRow{label: "need"}
	switch {
	case it == nil || it.status == nil:
		need.text, need.dim = "reading the issue's need", true
	case strings.TrimSpace(it.status.Need) == "":
		need.text, need.dim = "no need recorded", true
	default:
		need.lines = it.status.needLines(m.detailRoom(labelW), m.st)
	}
	d.rows = append(d.rows, need)

	waits := detailRow{label: "waits for", text: "nothing", dim: true}
	for i, b := range t.blockers {
		row := detailRow{label: waits.label, text: strings.Join(nonEmpty(b.Ticket, b.Kind, b.Fact), " · ")}
		if i > 0 {
			row.label = ""
		}
		d.rows = append(d.rows, row)
	}
	if len(t.blockers) == 0 {
		d.rows = append(d.rows, waits)
	}
	var after []string
	for _, id := range m.w.order {
		if other := m.w.tickets[id]; other != nil && id != t.id {
			for _, b := range other.blockers {
				if b.Ticket == t.id {
					after = append(after, id)
					break
				}
			}
		}
	}
	if len(after) == 0 {
		d.rows = append(d.rows, detailRow{label: "needed by", text: "nothing", dim: true})
	}
	for i, id := range after {
		row := detailRow{label: "needed by", text: id}
		if i > 0 {
			row.label = ""
		}
		d.rows = append(d.rows, row)
	}
	return d
}

// needLines is the issue's need drawn as markdown in lines of at most w cells,
// laid out once for the status read.
func (s *statusInfo) needLines(w int, st styles) []string {
	k := mdKey{section: -1, width: w, plain: st.plainFrame()}
	if lines, ok := s.md[k]; ok {
		return lines
	}
	lines := st.briefMarkdown(s.Need, w)
	if s.md == nil {
		s.md = map[mdKey][]string{}
	}
	s.md[k] = lines
	return lines
}
