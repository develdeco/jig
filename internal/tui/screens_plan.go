package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/land"
)

// The plan is the screen of an issue: what it is for, the tasks of its chart
// and where each landed, and its own stages, one line each. It is the chart
// screen of a chart that plans an issue (chartInfo.Issue), so the issue and its
// chart are one screen; a chart that plans none draws as it always did.
// Everything on it is read: the need and the suite from the issue's status, the
// pull request from the issue's digest, each task's end state from its own
// digest and landed lines. The screens run no verb for it and ask no host.

// planLabelW is the width of the label column of the Issue panel.
const planLabelW = 7

// planMapRows are the rows the Map panel of the plan has when m shows it: the
// plan is a page that scrolls, so the map takes what it needs.
const planMapRows = 12

// issueID is the issue the chart plans, "" for a chart that plans none.
func (c *chart) issueID() string {
	if c.info != nil && c.info.Issue != nil {
		return c.info.Issue.Ticket
	}
	// The store listing names the issue before the chart is read: the screen is
	// the plan from its first frame.
	return c.listing.Issue
}

// planIssue is the issue block of the chart that plans an issue: the verb's,
// else what the listing names of it before the chart is read.
func (c *chart) planIssue() *chartIssue {
	if c.info != nil && c.info.Issue != nil {
		return c.info.Issue
	}
	return &chartIssue{Ticket: c.listing.Issue}
}

// refreshIssue reads the status of the issue the chart plans, which holds the
// need and the suite the plan shows.
func (m *Model) refreshIssue(c *chart) tea.Cmd {
	id := c.issueID()
	if id == "" {
		return nil
	}
	m.w.ticketFor(id)
	return m.refreshStatus(id)
}

// ungraduated counts the entries of the chart that have no ticket yet, which the
// issue's own `jig next` lists among what it waits for.
func (is *chartIssue) ungraduated() int {
	n := 0
	for _, w := range is.Waits {
		if w.Why == "not graduated" {
			n++
		}
	}
	return n
}

// planOf is the name of the chart that plans issue, "" when no chart does: the
// screen an issue opens on.
func (m *Model) planOf(issue string) string {
	for _, name := range m.w.chartOrder {
		if c := m.w.charts[name]; c != nil && c.listing.Issue == issue {
			return name
		}
	}
	return ""
}

// issuesOf are the issues whose chart lists the ticket id, in the order the
// store lists the charts.
func (m *Model) issuesOf(id string) []string {
	var out []string
	for _, name := range m.w.chartOrder {
		c := m.w.charts[name]
		if c == nil || c.listing.Issue == "" {
			continue
		}
		if slices.Contains(c.listing.Tickets, id) {
			out = append(out, c.listing.Issue)
		}
	}
	return out
}

// upFrom is the ticket whose screen is on top when it is a task's (or a step's
// of one), "" on any other: the screens `c` goes up from.
func (m *Model) upFrom() string {
	e := m.top()
	switch e.screen {
	case screenTicket, screenBrief, screenBuild, screenReview, screenSlice, screenPublish, screenLand:
		if t := m.w.tickets[e.ticket]; t != nil && !t.awaiting {
			return e.ticket
		}
	}
	return ""
}

// upIssue is the issue `c` opens from the ticket id: the one the person reached
// it through, else the first that lists it; "" when none does.
func (m *Model) upIssue(id string) string {
	issues := m.issuesOf(id)
	if len(issues) == 0 {
		return ""
	}
	for i := len(m.nav) - 1; i >= 0; i-- {
		if m.nav[i].screen != screenChart {
			continue
		}
		if c := m.w.charts[m.nav[i].chart]; c != nil && slices.Contains(issues, c.listing.Issue) {
			return c.listing.Issue
		}
	}
	return issues[0]
}

// openIssue is `c`: the ticket's chart. A task of a multi-task goes up to it,
// and it goes up to the issue's plan; a task of an issue goes to the plan; any
// other ticket goes to the first chart that lists it.
func (m *Model) openIssue() tea.Cmd {
	id := m.upFrom()
	if id == "" {
		return nil
	}
	if parent := m.multiParent(id); parent != "" {
		return m.goTo(len(m.nav), nil, navEntry{screen: screenTicket, ticket: parent})
	}
	if issue := m.upIssue(id); issue != "" {
		return m.goTo(len(m.nav), nil, navEntry{screen: screenChart, chart: m.planOf(issue), follow: true})
	}
	if charts := m.chartsOf(id); len(charts) > 0 {
		return m.goTo(len(m.nav), nil, navEntry{screen: screenChart, chart: charts[0], follow: true})
	}
	m.notice = id + " is in no chart"
	return nil
}

// toggleMap is `m`: the plan's map shows and hides in place.
func (m *Model) toggleMap() {
	e := m.top()
	if e.screen != screenChart {
		return
	}
	if c := m.w.charts[e.chart]; c != nil && c.issueID() != "" {
		e.showMap = !e.showMap
	}
}

// kindIn is how a ticket's kind is said beside it on its screen: a task two
// issues list names both.
func (m *Model) kindIn(t *ticket) string {
	if d := t.digest; d != nil && d.Kind == "task" && d.Parent == "" {
		if issues := m.issuesOf(t.id); len(issues) > 1 {
			return "task of " + andList(issues)
		}
	}
	return t.digest.kindWords()
}

// planPage is the plan of the issue chart c plans: Need, Tasks, Issue, then the
// Run panel and the activity as the chart screen has them. The tasks are never
// cut; a page longer than the window scrolls with the cursor.
func (m *Model) planPage(c *chart) page {
	var p page
	is := c.planIssue()
	issue := m.w.tickets[is.Ticket]
	var suite []statusSuite
	if issue != nil && issue.status != nil {
		suite = issue.status.Suite
	}
	fixes := fixTasks(suite)
	rows := m.planRows(m.chartRows(c), is.Ticket, is.Branch)

	if e := m.top(); c.info != nil && e.showMap && e.screen == screenChart && e.chart == c.listing.Name {
		m.mapPanel(&p, c, planMapRows)
	}
	m.needPanel(&p, is.Ticket, issue)
	landed, total := planProgress(rows)
	right := fmt.Sprintf("%d of %d landed", landed, total)
	if total == 0 {
		right = "none graduated"
	}
	tasks := m.ticketRowsPanel(c, rows, "Tasks", right, m.planNote(is.Ticket, is.Branch, issueBranchWords),
		func(r chartRow) []string {
			if tip, ok := fixes[r.id]; ok {
				return []string{m.st.dim("    ↳ added after the failed suite" + tipWords(tip))}
			}
			return nil
		})
	p.lines = append(p.lines, tasks.lines...)
	m.issuePanel(&p, c, issue, suite, rows)
	if c.info == nil {
		// The chart is not read yet: the plan is drawn from what is known, and
		// says it is reading.
		return p
	}

	frame := func(n int) int {
		if n == 0 {
			return 0
		}
		return 2 + n
	}
	run := m.runRows(c.info)
	if spare := m.bodyRows() - len(p.lines) - frame(len(run)) - activityFloor; spare < 0 && len(run) > 1 {
		run = trimRuns(run, len(run)-min(-spare, len(run)-1))
	}
	p.boxTop("Run", runWord(c.info))
	for _, r := range run {
		p.boxAdd(m.st.dim(pad(r.label, 8)) + " " + r.text)
	}
	p.boxEnd()
	m.chartActivityPanel(&p, rows, c.info.History)
	return p
}

// issueBranchWords stand for the issue's own branch in a task's landing: the
// Issue panel names it once, and a row that spelled it out would be cut before
// its time on a narrow screen.
const issueBranchWords = "the issue's branch"

// planNote is the words after a task's steps in the plan of container, whose
// branch is branch: where it landed, with the branch said as such, else what the
// chart screen says of it, less the parent every row here shares. A
// multi-task's are its own, and a task two issues list says what its pull
// request into this issue and its landing there say, and nothing of the other's.
func (m *Model) planNote(container, branch, label string) func(chartRow) (string, bool) {
	return func(r chartRow) (string, bool) {
		switch t := r.t; {
		case t == nil || t.digest == nil:
		case isMulti(t):
			return m.multiNote(r, branch), false
		case m.shared(t):
			if r.atEnd {
				note := m.st.state(r.end.State, inBranch(r.end.Words, branch, label))
				if r.end.State == StateWaitsToLand {
					// Not landed here yet: the pull request into this issue is the
					// one that waits, and no other issue's is named.
					var mine []digestPR
					for _, pr := range t.digest.PRs {
						if pr.Target == branch {
							mine = append(mine, pr)
						}
					}
					note += m.st.dim(" · " + prShort(mine))
				}
				return note, false
			}
			return m.st.dim("not published into " + container + " yet"), false
		}
		note, own := m.chartNoteIn(r, false)
		return inBranch(note, branch, label), own
	}
}

// planProgress counts the tasks of the plan that landed and all of them: a
// closed task landed nothing, and still counts among the tasks.
func planProgress(rows []chartRow) (landed, total int) {
	for _, r := range rows {
		if r.depth > 0 {
			continue
		}
		total++
		if r.atEnd && r.end.State == StateLanded {
			landed++
		}
	}
	return landed, total
}

// fixTasks are the tasks the issue's status says were begun after a failed
// suite, each with the tip of the run it follows.
func fixTasks(suite []statusSuite) map[string]string {
	out := map[string]string{}
	for _, s := range suite {
		for _, id := range s.Fixes {
			out[id] = s.Tip
		}
	}
	return out
}

// tipWords is " at <tip>" for a tip, nothing for none.
func tipWords(tip string) string {
	if tip == "" {
		return ""
	}
	return " at " + short(tip)
}

// needPanel is what the issue is for, the body of its ticket, wrapped whole.
func (m *Model) needPanel(p *page, id string, issue *ticket) {
	p.boxTop("Need", id)
	switch {
	case issue == nil || issue.status == nil:
		p.boxAdd(m.st.dim(readingText))
	case strings.TrimSpace(issue.status.Need) == "":
		p.boxAdd(m.st.dim("no need recorded"))
	default:
		for _, line := range strings.Split(issue.status.Need, "\n") {
			if strings.TrimSpace(line) == "" {
				p.boxAdd("")
				continue
			}
			for _, l := range wrap(line, max(1, m.width-7), 1<<20) {
				p.boxAdd(l)
			}
		}
	}
	p.boxEnd()
}

// planLine is a line of the Issue panel: the icon of its state, its label and
// its words.
func (m *Model) planLine(state, label, words string) string {
	return m.st.icon(state) + " " + m.st.dim(pad(label, planLabelW)) + " " + words
}

// issuePanel is the issue's own stages, one line each: its branch, how many of
// its tasks landed, its suite (a line for each repo), its pull request and its
// landing. A stage not reached reads dim.
func (m *Model) issuePanel(p *page, c *chart, issue *ticket, suite []statusSuite, rows []chartRow) {
	is := c.planIssue()
	p.boxTop("Issue", is.Ticket)
	branch := m.st.dim("no branch yet")
	if c.info == nil {
		branch = m.st.dim(readingText)
	}
	if is.Branch != "" {
		branch = is.Branch
		if len(is.Stack) > 0 && is.Stack[0].StartFrom != "" {
			branch += m.st.dim(" · from " + is.Stack[0].StartFrom)
		}
	}
	m.stageLines(p, is.Ticket, issue, branch, rows, m.suiteLines(is, issue, suite), is.Prompt, suiteMet(is, issue, suite), is.ungraduated())
	p.boxEnd()
}

// stageLines are the lines of a container's panel: its branch (in words), its
// tasks landed, the suite lines it has (none for a multi-task), its own pull
// request and its landing. prompt is what opens the pull request, and suiteOK
// says no suite holds it back.
func (m *Model) stageLines(p *page, id string, t *ticket, branch string, rows []chartRow, suite []string, prompt string, suiteOK bool, ungraduated int) {
	p.boxAdd(m.planLine(StateOK, "branch", branch))

	landed, total := planProgress(rows)
	switch {
	case total == 0:
		p.boxAdd(m.planLine(StateQueued, "tasks", m.st.dim("none graduated yet")))
	case landed == total:
		p.boxAdd(m.planLine(StateLanded, "tasks", fmt.Sprintf("%d of %d landed", landed, total)))
	default:
		state := StateQueued
		if landed > 0 {
			state = StateBuilding
		}
		p.boxAdd(m.planLine(state, "tasks", fmt.Sprintf("%d of %d landed", landed, total)))
	}
	for _, l := range suite {
		p.boxAdd(l)
	}
	prState, prWords := m.planPR(prompt, t, suiteOK, ungraduated, rows)
	p.boxAdd(m.planLine(prState, "PR", prWords))
	landState, landWords := m.planLanding(id, t)
	p.boxAdd(m.planLine(landState, "landing", landWords))
}

// suiteLines are the suite of the issue in words, one line for each row of its
// status: the standing at the tip of its branch, and a failed run at an earlier
// tip as history. A project of several repos names each line's repo.
func (m *Model) suiteLines(is *chartIssue, issue *ticket, suite []statusSuite) []string {
	if issue == nil || issue.status == nil {
		return []string{m.planLine(StateQueued, "suite", m.st.dim(readingText))}
	}
	if len(suite) == 0 {
		return []string{m.planLine(StateQueued, "suite", m.st.dim("none recorded (the repo leaves it to CI)"))}
	}
	repos := map[string]bool{}
	for _, s := range suite {
		repos[s.Repo] = true
	}
	current := lastSuiteRows(suite)
	var out []string
	for i, s := range suite {
		state, words := m.suiteWords(is, s, current[s.Repo] == i)
		if len(repos) > 1 {
			words = s.Repo + ": " + words
		}
		out = append(out, m.planLine(state, "suite", words))
	}
	return out
}

// lastSuiteRows is, for each repo, the position of its last row: the one that
// stands at the tip of the issue's branch.
func lastSuiteRows(suite []statusSuite) map[string]int {
	out := map[string]int{}
	for i, s := range suite {
		out[s.Repo] = i
	}
	return out
}

// suiteWords is one suite row in words, with the state its line is drawn in.
// current says the row stands at the tip of the branch, and not at an earlier
// one.
func (m *Model) suiteWords(is *chartIssue, s statusSuite, current bool) (state, words string) {
	switch s.Standing {
	case land.SuitePass:
		return StateOK, fmt.Sprintf("passed at %s in %d s: `%s`", short(s.Tip), s.Seconds, s.Command)
	case land.SuiteFail:
		words = "failed at " + short(s.Tip)
		if s.Signature != "" {
			words += ": " + s.Signature
		}
		switch {
		case len(s.Fixes) > 0:
			words += " · fix: " + strings.Join(s.Fixes, ", ")
		case current:
			words += " · add a fix task for " + is.Ticket
		}
		if !current {
			return StateStalled, m.st.dim(words)
		}
		return StateStalled, m.st.state(StateStalled, words)
	case land.SuiteWaived:
		if s.Waiver != "" {
			return StateFindings, "waived: " + s.Waiver
		}
		return StateFindings, "waived"
	case land.SuiteUndeclared:
		return StateQueued, m.st.dim("none recorded (the repo leaves it to CI)")
	}
	// Not run, or not readable: the sentence says which.
	if s.Tip != "" {
		return StateQueued, m.st.dim("not run at " + short(s.Tip))
	}
	return StateQueued, m.st.dim(firstOf(s.Why, "not run"))
}

// suiteMet says the suite asks nothing more of the issue: every repo's row at
// the tip passed, was waived or declares none. Before the status is read the
// issue's own unmet list says it.
func suiteMet(is *chartIssue, issue *ticket, suite []statusSuite) bool {
	if issue == nil || issue.status == nil || len(suite) == 0 {
		for _, u := range is.Unmet {
			if u.Code == land.SuiteNotRun || u.Code == land.SuiteFailed {
				return false
			}
		}
		return true
	}
	for _, i := range lastSuiteRows(suite) {
		switch suite[i].Standing {
		case land.SuitePass, land.SuiteWaived, land.SuiteUndeclared:
		default:
			return false
		}
	}
	return true
}

// planPR is the issue's own pull request: by number, target and state once it
// is open; before that, what it waits for, which is never its tasks not landed
// or its suite unmet being passed over.
func (m *Model) planPR(prompt string, issue *ticket, suiteOK bool, ungraduated int, rows []chartRow) (state, words string) {
	if issue == nil || issue.digest == nil {
		return StateQueued, m.st.dim(readingText)
	}
	if prs := issue.digest.PRs; len(prs) > 0 {
		state = StateOK
		parts := make([]string, len(prs))
		for i, pr := range prs {
			parts[i] = prWordsOf(pr)
			switch strings.ToLower(pr.State) {
			case "merged":
			case "closed":
				state = StateStalled
			default:
				if state == StateOK {
					state = StatePublishing
				}
			}
		}
		return state, strings.Join(parts, ", ")
	}
	if st := issue.status; st != nil && st.Standing != "" {
		// What the container waits for is what `jig next` says it does, read from
		// the status that derives it, not counted again from the rows.
		var waits []string
		if n := len(st.Waits); n > 0 {
			waits = append(waits, fmt.Sprintf("%d %s", n, plural(n, "task", "tasks")))
		}
		for _, u := range st.Unmet {
			if u.Code == land.SuiteNotRun || u.Code == land.SuiteFailed {
				waits = append(waits, "the suite")
				break
			}
		}
		switch {
		case len(waits) > 0:
			return StateQueued, m.st.dim("not opened yet · waits for " + andList(waits))
		case st.Standing == "waiting":
			// Nothing to name: `jig next` says it has no children yet.
			return StateQueued, m.st.dim("not opened yet · waits for its tasks to be graduated")
		case st.Standing != "ready":
			// Published or landed, and the digest has no row for the pull request yet.
			return StateQueued, m.st.dim(readingText)
		case st.Prompt != "":
			return StateReady, "not opened yet · say " + m.st.key(strconv.Quote(st.Prompt))
		}
		return StateReady, "not opened yet · ready to open"
	}
	var waits []string
	unlanded := 0
	for _, r := range rows {
		if r.depth == 0 && !(r.atEnd && (r.end.State == StateLanded || r.end.State == StateClosed)) {
			unlanded++
		}
	}
	// An entry of the chart with no ticket yet is waited for too.
	unlanded += ungraduated
	if unlanded > 0 {
		waits = append(waits, fmt.Sprintf("%d %s", unlanded, plural(unlanded, "task", "tasks")))
	}
	if !suiteOK {
		waits = append(waits, "the suite")
	}
	if len(waits) > 0 {
		return StateQueued, m.st.dim("not opened yet · waits for " + andList(waits))
	}
	if prompt != "" {
		return StateReady, "not opened yet · say " + m.st.key(strconv.Quote(prompt))
	}
	return StateReady, "not opened yet · ready to open"
}

// prWordsOf is one pull request as the plan says it: "PR #512 into main · open".
func prWordsOf(pr digestPR) string {
	word := strings.ToLower(pr.State)
	switch word {
	case "open", "merged", "closed":
	default:
		word = "state not read"
	}
	var head string
	switch {
	case pr.Number > 0:
		head = "PR #" + strconv.Itoa(pr.Number)
	default:
		head = pr.URL
	}
	if pr.Target != "" {
		head += " into " + pr.Target
	}
	return head + " · " + word
}

// planLanding is where the issue's own landing stands, in the end states' words: it
// waits for its pull request, waits to land at the person's word, landed, or was
// closed.
func (m *Model) planLanding(id string, issue *ticket) (state, words string) {
	if issue == nil || issue.digest == nil {
		return StateQueued, m.st.dim(readingText)
	}
	d := issue.digest
	say := `say "Land ` + id + `"`
	switch {
	case d.Closed != "":
		return StateClosed, "closed · " + d.Closed
	case d.State == land.StateLanded:
		return StateLanded, landedWords("landed", m.landedLines(issue))
	case d.State == land.StatePublished:
		merged := len(d.PRs) > 0
		for _, pr := range d.PRs {
			merged = merged && strings.EqualFold(pr.State, "merged")
		}
		if merged {
			return StateWaitsToLand, "merged on the host, not recorded · " + say
		}
		return StateWaitsToLand, "waits to land · " + say
	}
	return StateQueued, m.st.dim("waits for its pull request")
}
