package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// prSubject is the ticket whose pull requests `o` opens: the ticket whose
// screen is open, the issue on its plan, and the ticket under the cursor on the
// project's list and on a chart that plans no issue.
func (m *Model) prSubject(sub subject) string {
	if e := m.top(); e.screen == screenChart {
		if c := m.w.charts[e.chart]; c != nil && c.issueID() != "" {
			return c.issueID()
		}
	}
	return sub.ticket
}

// prsOf are the pull requests of the subject, as the roster's digest records
// them; the reason when it has none to open. Nothing is asked of the host.
func (m *Model) prsOf(sub subject) ([]digestPR, string) {
	id := m.prSubject(sub)
	t := m.w.tickets[id]
	switch {
	case id == "" || t == nil:
		return nil, "select a ticket first"
	case t.awaiting:
		return nil, id + " does not exist yet"
	case t.digest == nil:
		return nil, readingText
	case len(t.digest.PRs) == 0:
		return nil, id + " has no pull request"
	}
	return t.digest.PRs, ""
}

// openPRs is `o`: each pull request of the subject goes to the opener the
// program was given, and the notice names each by number and target. With no
// opener (a pipe, a test) the notice carries the address instead, and an opener
// that refuses says why and carries it too.
func (m *Model) openPRs(sub subject) tea.Cmd {
	prs, why := m.prsOf(sub)
	if len(prs) == 0 {
		m.notice = why
		return nil
	}
	var said []string
	for _, pr := range prs {
		name := prWords([]digestPR{pr}, false)
		switch {
		case pr.URL == "":
			said = append(said, name+" has no address")
		case m.cfg.Open == nil:
			said = append(said, name+": "+pr.URL)
		default:
			if err := m.cfg.Open(pr.URL); err != nil {
				said = append(said, "could not open "+name+": "+err.Error()+": "+pr.URL)
				continue
			}
			said = append(said, "opened "+name)
		}
	}
	m.notice = strings.Join(said, " · ")
	return nil
}
