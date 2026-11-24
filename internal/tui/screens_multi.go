package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/phase"
)

// A plan is one level more than an issue's: a multi-task is listed in the
// issue's plan with its own tasks nested under it, and its own screen is the
// same plan one level down, with its body, its tasks, its branch, its pull
// request into the issue's branch and its landing, and no suite. A task two
// issues list shows under each of them, landing per issue.

// The kinds of ticket the plan tells apart, as the digest names them.
const (
	kindIssue     = "issue"
	kindTask      = "task"
	kindMultiTask = "multi-task"
)

// isMulti says t is a multi-task.
func isMulti(t *ticket) bool {
	return t != nil && t.digest != nil && t.digest.Kind == kindMultiTask
}

// shared says t is a task two or more issues list: it publishes a pull request
// into each issue's branch and lands per issue.
func (m *Model) shared(t *ticket) bool {
	d := t.digest
	return d != nil && d.Kind == kindTask && d.Parent == "" && len(m.issuesOf(t.id)) > 1
}

// multiParent is the multi-task the task id belongs to, "" for a task of none.
func (m *Model) multiParent(id string) string {
	if t := m.w.tickets[id]; t != nil && t.digest != nil && t.digest.Parent != "" && isMulti(m.w.tickets[t.digest.Parent]) {
		return t.digest.Parent
	}
	return ""
}

// planRows are the rows of a plan: the tickets listed, in their order, each
// multi-task followed by its own tasks one level in, and the end state of each
// as it stands in container, whose branch is branch.
func (m *Model) planRows(rows []chartRow, container, branch string) []chartRow {
	listed := map[string]bool{}
	for _, r := range rows {
		listed[r.id] = true
	}
	under := map[string][]chartRow{}
	var top []chartRow
	for _, r := range rows {
		if parent := m.multiParent(r.id); parent != "" && listed[parent] {
			under[parent] = append(under[parent], r)
			continue
		}
		top = append(top, r)
	}
	var out []chartRow
	for _, r := range top {
		out = append(out, m.rowInPlan(r, container, branch))
		for _, child := range under[r.id] {
			child.depth = 1
			out = append(out, m.rowInPlan(child, container, branch))
		}
	}
	return out
}

// rowInPlan is row r as the plan of container says it: a multi-task has no steps
// of its own and ends where its own pull request does, and a task two issues
// list ends where its pull request into container does.
func (m *Model) rowInPlan(r chartRow, container, branch string) chartRow {
	if r.t == nil || r.t.digest == nil {
		return r
	}
	d := r.t.digest
	switch {
	case isMulti(r.t):
		r.bare, r.end, r.atEnd = true, endState{}, false
		landed, total := m.childrenLanded(d)
		switch {
		case d.Closed != "":
			r.mark, r.end, r.atEnd = StateClosed, endState{StateClosed, "closed", "closed · " + d.Closed}, true
		case d.State == land.StateLanded:
			r.mark, r.end, r.atEnd = StateLanded, endState{StateLanded, "landed", landedWords("landed", m.landedLines(r.t))}, true
		case len(d.PRs) > 0:
			r.mark = StatePublishing
		case landed > 0 && landed == total:
			r.mark = StateReady
		case landed > 0:
			r.mark = StateBuilding
		default:
			r.mark = StateQueued
		}
	case m.shared(r.t):
		// Every part of the row, its icon, its five dots and its words, is drawn
		// from the task as it stands in this issue and from nothing of the other's.
		cut := m.cutFor(r.t, container, branch)
		r.t, r.d = cut, m.phaseOf(cut)
		r.steps, _ = m.rowSteps(cut, r.d)
		r.end, r.atEnd = m.endOf(cut)
		r.done = r.steps[stepPublish].Done
		r.mark = ""
		if r.atEnd {
			r.mark = r.end.State
		}
	}
	return r
}

// childrenLanded counts the tasks under a multi-task that landed, and all of
// them.
func (m *Model) childrenLanded(d *digestInfo) (landed, total int) {
	for _, c := range d.Children {
		if c.ID == "" {
			continue
		}
		total++
		if t := m.w.tickets[c.ID]; t != nil {
			if end, ok := m.endOf(t); ok && end.State == StateLanded {
				landed++
			}
		}
	}
	return landed, total
}

// landedInto are the landed lines of t that went into container, the issue or
// multi-task by id: what tells the landings of a task two issues list apart.
func (m *Model) landedInto(t *ticket, container string) []landedAt {
	var out []landedAt
	for _, l := range m.landedLines(t) {
		if l.Container == container {
			out = append(out, l)
		}
	}
	return out
}

// cutFor is the task t as it stands in issue container, whose branch is branch:
// a copy whose digest has only the pull request into that branch and whose
// Publish and Land phases say what that pull request and the landing recorded
// into that issue make of them (closed stays closed), and whose journal has no
// landed line of another issue. Whatever draws from it, an end state, the five
// steps or a note, can read nothing of the other issue.
func (m *Model) cutFor(t *ticket, container, branch string) *ticket {
	d := *t.digest
	d.PRs = nil
	for _, pr := range t.digest.PRs {
		if pr.Target == branch {
			d.PRs = append(d.PRs, pr)
		}
	}
	c := *t
	c.lines = nil
	for _, l := range t.lines {
		// What the task did into another issue (its pull request, its publish,
		// its landing) is no line of this one's.
		if (l.Event == journal.EventLanded || journal.IsPublishEvent(l.Event)) && l.Into != "" && l.Into != container {
			continue
		}
		c.lines = append(c.lines, l)
	}
	lines := m.landedInto(&c, container)
	merged := len(d.PRs) > 0
	for _, pr := range d.PRs {
		merged = merged && strings.EqualFold(pr.State, "merged")
	}
	d.Phases = slices.Clone(t.digest.Phases)
	// Publishing is for a task whose brief, build and review are done; one built
	// again after a publish elsewhere has not earned a publish here.
	// A landing is a fact of its issue: a task that landed here has every step
	// done here, whatever it has done since for another issue.
	landedHere := len(lines) > 0
	if landedHere {
		for i := range d.Phases {
			d.Phases[i].State = phase.StateDone
		}
	}
	built := true
	for _, p := range d.Phases {
		if p.Name == string(phase.Brief) || p.Name == string(phase.Build) || p.Name == string(phase.Review) {
			built = built && p.State == phase.StateDone
		}
	}
	published := built && (len(d.PRs) > 0 || landedHere)
	open := built && d.Closed == ""
	for i, p := range d.Phases {
		switch p.Name {
		case string(phase.Publish):
			switch {
			case published:
				d.Phases[i].State, d.Phases[i].Text = phase.StateDone, "pull request opened"
			case open:
				d.Phases[i].State, d.Phases[i].Text = phase.StateCurrent, "not published into "+container
			case p.State == phase.StateDone && d.Closed == "":
				d.Phases[i].State, d.Phases[i].Text = phase.StatePending, "after a clean review"
			}
		case string(phase.Land):
			switch {
			case published && len(lines) > 0:
				d.Phases[i].State, d.Phases[i].Text = phase.StateDone, "landed"
			case published && merged:
				d.Phases[i].State, d.Phases[i].Text = phase.StateCurrent, "merged on the host, not recorded"
			case published:
				d.Phases[i].State, d.Phases[i].Text = phase.StateCurrent, "waits for a merge"
			case d.Closed == "":
				d.Phases[i].State, d.Phases[i].Text = phase.StatePending, "after the publish"
			}
		}
	}
	switch {
	case published:
		d.Current, d.Ready = string(phase.Land), false
	case open:
		d.Current, d.Ready = string(phase.Publish), true
	case d.Current == string(phase.Land) || d.Current == string(phase.Publish):
		d.Current, d.Ready = string(phase.Review), true
	}
	c.digest = &d
	return &c
}

// inBranch says the issue's branch as such in words that name it, so a row stays
// whole on a narrow screen and the Issue panel names the branch once.
func inBranch(words, branch, label string) string {
	if branch == "" {
		return words
	}
	return strings.ReplaceAll(words, "into "+branch, "into "+label)
}

// multiLabel is a multi-task's state and word in its header: closed, landed,
// published (its pull request is open), or how many of its tasks have landed.
func (m *Model) multiLabel(t *ticket) (state, label string) {
	d := t.digest
	landed, total := m.childrenLanded(d)
	switch {
	case d.Closed != "":
		return StateClosed, "closed"
	case d.State == land.StateLanded:
		return StateLanded, "landed"
	case len(d.PRs) > 0:
		return StatePublishing, "published"
	case landed > 0 && landed == total:
		return StateReady, "ready to publish"
	}
	return StateBuilding, fmt.Sprintf("%d of %d landed", landed, total)
}

// prShort says pull requests by number and state, the target left out: a row of
// a plan is under the container they go into.
func prShort(prs []digestPR) string {
	parts := make([]string, len(prs))
	for i, pr := range prs {
		word := strings.ToLower(pr.State)
		switch word {
		case "open", "merged", "closed":
		default:
			word = "state not read"
		}
		head := pr.URL
		if pr.Number > 0 {
			head = "PR #" + strconv.Itoa(pr.Number)
		}
		parts[i] = head + " " + word
	}
	return strings.Join(parts, ", ")
}

// multiNote is the words after a multi-task's row in a plan: what it is, how
// many of its tasks landed, and where its own pull request stands.
func (m *Model) multiNote(r chartRow, branch string) string {
	d := r.t.digest
	landed, total := m.childrenLanded(d)
	kind := m.st.dim(kindMultiTask + " · ")
	switch {
	case d.Closed != "":
		return kind + m.st.state(StateClosed, "closed · "+d.Closed)
	case d.State == land.StateLanded:
		return kind + m.st.state(StateLanded, inBranch(landedWords("landed", m.landedLines(r.t)), branch, issueBranchWords))
	case len(d.PRs) > 0:
		return kind + fmt.Sprintf("%d of %d landed · ", landed, total) + prShort(d.PRs)
	}
	return kind + fmt.Sprintf("%d of %d landed", landed, total)
}

// multiPage is the screen of a multi-task: the plan one level down. Its need is
// its body, its tasks are the ones under it, and its panel names its branch, how
// many tasks landed, its pull request into the issue's branch and its landing;
// it has no suite, which is the issue's.
func (m *Model) multiPage(t *ticket) page {
	var p page
	branch := ""
	if t.status != nil {
		branch = t.status.Branch
	}
	m.needPanel(&p, t.id, t)
	var rows []chartRow
	for _, c := range t.digest.Children {
		if c.ID != "" {
			rows = append(rows, m.rowInPlan(m.rowOf(c.ID, c.Title, nil), t.id, branch))
		}
	}
	landed, total := planProgress(rows)
	right := fmt.Sprintf("%d of %d landed", landed, total)
	if total == 0 {
		right = "none graduated"
	}
	tasks := m.ticketRowsPanel(nil, rows, "Tasks", right, m.planNote(t.id, branch, "the multi-task's branch"), nil)
	p.lines = append(p.lines, tasks.lines...)

	p.boxTop("Multi-task", t.id)
	words := m.st.dim("no branch yet")
	if branch != "" {
		words = branch
	}
	m.stageLines(&p, t.id, t, words, rows, nil, phase.Prompt(phase.Publish, t.id), true, 0)
	p.boxEnd()
	m.chartActivityPanel(&p, rows, nil)
	return p
}
