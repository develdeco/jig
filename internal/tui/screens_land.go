package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// landingNeedsYou is whether the ticket stands at the landing stop: its pull
// requests are ready to merge and the person's word ("Land T") is the next
// move. It is a moment that needs the person, beside an open question or an ask.
func (m *Model) landingNeedsYou(t *ticket) bool {
	end, ok := m.endOf(t)
	return ok && end.State == StateWaitsToLand
}

// landRow is a pull request of a ticket in the Land screen's panel: what is to
// be merged, or what landed, as the cells of its row: its state's icon, its repo
// when the ticket has several, the pull request and where it stands.
type landRow struct {
	state string
	cells []string
	// landed says a landed line records it.
	landed bool
}

// landColumns are the columns of the Land screen's list; landColumnNames name
// them. The repo's column is drawn when a row has one.
var (
	landColumns     = []column{{}, {gap: 1, keep: true}, {id: true, gap: 2}, {free: true, gap: 2}}
	landColumnNames = []string{"", "repo", "pull request", "state"}
)

// landPage is the land: the end state's words first, then a panel with a row
// for each pull request of the ticket (one for each repo, and for each issue of
// a shared task) saying the merge to make and what the host says of it, or where
// it landed and when and by whom, and the land step's own activity. The screen
// merges nothing and runs nothing: "Land T" is the person's word to the session.
func (m *Model) landPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	step := m.steps(t)[stepLand]
	end, atEnd := m.endOf(t)
	state, words := step.State, step.Text
	if atEnd {
		state, words = end.State, end.Words
	}
	p.add(m.st.icon(state) + " " + m.st.state(state, words))
	switch {
	case atEnd && end.State == StateWaitsToLand && strings.HasPrefix(end.Words, "merged on the host"):
		p.add(m.st.dim("  the session records it"))
	case atEnd && end.State == StateWaitsToLand:
		p.add(m.st.dim("  the session merges each pull request and records it"))
	}
	p.add("")

	rows := m.landRows(t, end, atEnd)
	landed := 0
	for _, r := range rows {
		if r.landed {
			landed++
		}
	}
	right := ""
	if len(rows) > 0 {
		right = fmt.Sprintf("%d of %d landed", landed, len(rows))
	}
	empty := ""
	switch {
	case len(rows) == 0 && atEnd && end.State == StateClosed:
		empty = "closed without a pull request to merge"
	case len(rows) == 0 && len(t.digest.prs()) > 0:
		// Published, and built again or behind: its pull requests merge once it
		// publishes again.
		empty = "no merge to make until it publishes again"
	case len(rows) == 0:
		empty = "no pull request to land yet"
	}
	items := make([]listRow, len(rows))
	for i, r := range rows {
		items[i] = listRow{cells: r.cells}
	}
	m.listPanel(&p, listSpec{title: "Pull requests", summary: right, cols: landColumns, names: landColumnNames, rows: items, empty: empty})
	m.stepActivityPanel(&p, t, stepLand)
	return p
}

// landRows are the ticket's pull requests as the Land screen's panel lists
// them, in the order the digest gives them: a landed one says where and when and
// by whom, the others the merge to make and what the host says of them. A landed
// line no pull request of the digest names is listed after them.
func (m *Model) landRows(t *ticket, end endState, atEnd bool) []landRow {
	if !atEnd || end.State == StateClosed {
		// Not at the landing: unpublished, behind or built again, nothing is
		// to be merged yet.
		return nil
	}
	lines := m.landedLines(t)
	used := make([]bool, len(lines))
	var rows []landRow
	several := len(t.digest.prs()) > 1
	for _, pr := range t.digest.prs() {
		repo := ""
		if several {
			repo = pr.Repo
		}
		// A pull request reads by number, the host's word and the branch it goes
		// into, as the digest has them.
		name := prWords([]digestPR{pr}, true)
		if i := landingOf(lines, used, pr); i >= 0 {
			used[i] = true
			rows = append(rows, landRow{StateLanded, []string{m.st.icon(StateLanded), repo, name, m.landedText(t, lines[i], pr.Target)}, true})
			continue
		}
		state := StateWaitsToLand
		if atEnd && end.State == StateOnLand {
			state = StateOnLand
		}
		host := "merge to make"
		switch strings.ToLower(pr.State) {
		case "merged":
			host = "merged on the host, not recorded"
		case "closed":
			state, host = StateStalled, "closed on the host, not merged"
		}
		rows = append(rows, landRow{state, []string{m.st.icon(state), repo, name, host}, false})
	}
	for i, l := range lines {
		if !used[i] {
			rows = append(rows, landRow{StateLanded, []string{m.st.icon(StateLanded), l.Repo, "", m.landedText(t, l, "")}, true})
		}
	}
	return rows
}

// landingOf is the index of the landed line, not used yet, that records pr: the
// one of its repo that merged into the branch it goes into. A line that names no
// repo (a project of one repo writes none, whatever name the digest gives it)
// stands for a pull request of any, where phase.LandedIn reads the same for the journal's own lines.
func landingOf(lines []landedAt, used []bool, pr digestPR) int {
	for i, l := range lines {
		if !used[i] && (l.Repo == pr.Repo || l.Repo == "") && (l.Into == pr.Target || l.Into == "" || pr.Target == "") {
			return i
		}
	}
	return -1
}

// landedText is when a landing was recorded and by whom, and where it went when
// that is not the branch the pull request names (target, "" when the row does
// not name one).
func (m *Model) landedText(t *ticket, l landedAt, target string) string {
	text := "landed"
	if target == "" || l.Into != target {
		text += " into " + firstOf(l.Into, "its target")
	}
	if l.At != "" {
		text += " at " + l.At
	}
	switch l.Provenance {
	case journal.ProvenanceHost:
		return text + " · by the host"
	case journal.ProvenancePerson:
		return text + " · by you"
	}
	if who := m.who(t, l.Session); who != "" {
		return text + " · by the " + who
	}
	return text + " · by an agent"
}

// landNeedsLine is the landing stop as a row of the ticket screen's needs-you
// block: the words the person says.
func (m *Model) landNeedsLine(t *ticket) string {
	end, _ := m.endOf(t)
	return m.st.icon(end.State) + " " + m.st.state(end.State, end.Words) + m.st.dim("  "+strconv.Itoa(stepLand+1)+" opens it")
}
