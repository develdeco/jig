package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// The plan fixtures: an issue GATE-49 whose chart "repeat" lists tasks T-1 to
// T-4, all of them published into the issue's branch.

const (
	planBranch = "501-review-rounds-stop-repeating-work"
	planNeed   = "Review rounds repeat the same findings.\nStop them from coming back once a person has decided."
)

// planSpec is what a plan fixture shows; the zero value is the issue that
// landed everything with no suite declared and its pull request into main
// merged and landed.
type planSpec struct {
	// tasks are the chart's tasks in order (T-1 to T-4 when none); landed says
	// which of them landed into the branch (all when nil).
	tasks  []string
	landed map[string]bool
	// suite are the rows of the status's suite table, comma separated.
	suite []string
	// noSuiteTable leaves the table out, as an older jig's status does.
	noSuiteTable bool
	need         string
	issue        *digestInfo
	issueLines   []events.Envelope
	unmet        []map[string]any
	prompt       string
	// closed are the tasks closed without landing, each with its reason.
	closed map[string]string
}

func (s planSpec) taskIDs() []string {
	if len(s.tasks) == 0 {
		return []string{"T-1", "T-2", "T-3", "T-4"}
	}
	return s.tasks
}

func (s planSpec) isLanded(id string) bool { return s.landed == nil || s.landed[id] }

// landedIssue is the digest of an issue whose pull request merged into main and
// landed.
func landedIssue() *digestInfo {
	return &digestInfo{Kind: "issue", State: "landed", PRs: []digestPR{{Repo: "demo", Number: 512, URL: "https://example.test/acme/demo/pull/512", State: "merged", Target: "main"}}}
}

// planRig is the plan of GATE-49 on the screen: the store's list, the chart's
// verb and the issue's status are answered as spec says, the tasks' digests are
// pinned, and the chart screen is open.
func planRig(t *testing.T, spec planSpec) *rig {
	t.Helper()
	g := newRig(t, "")
	ids := spec.taskIDs()
	tickets := []map[string]any{{"id": "GATE-49", "title": "Review rounds stop repeating work", "state": "issue"}}
	entries := []map[string]any{}
	for _, id := range ids {
		tickets = append(tickets, map[string]any{"id": id, "title": "Task " + id, "state": "done"})
		entries = append(entries, entry(id, "Task "+id, "publish", []string{"done", "done", "done", "done"}, false))
	}
	roster := jsonLine(map[string]any{
		"tickets": tickets,
		"charts":  []map[string]any{{"name": "repeat", "title": "Repeat rounds", "tickets": ids, "ungraduated": 0}},
	})
	issueBlock := map[string]any{
		"ticket": "GATE-49", "stage": "landing", "state": "waiting", "branch": planBranch, "prompt": spec.prompt,
		"stack": []map[string]any{{"repo": "demo", "start_from": "origin/main", "pr_target": "main"}},
	}
	if spec.unmet != nil {
		issueBlock["unmet"] = spec.unmet
	}
	verb := jsonLine(map[string]any{
		"chart": "repeat", "title": "Repeat rounds", "ready": false, "code": "CHART_DONE", "started": true, "ungraduated": 0,
		"briefs": []string{}, "run": [][]string{}, "tickets": entries,
		"map":   map[string]any{"destination": []string{"Plans issue GATE-49."}},
		"issue": issueBlock,
	})
	status := map[string]string{"GATE-49": planStatus(spec)}
	for _, id := range ids {
		status[id] = untouchedStatus(id)
	}
	answerChart(g, roster, "repeat", verb, status)

	for _, id := range ids {
		d := task("land", false, "done|no brief on record", "done|1 slice green", "done|round 1 clean", "done|pull request opened", "current|waits for a merge")
		d.Parent = "GATE-49"
		if spec.isLanded(id) {
			d.Phases[4] = digestPhase{Name: "land", State: "done", Text: "landed"}
		}
		if reason, ok := spec.closed[id]; ok {
			d = task("publish", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
			d.Parent, d.Closed = "GATE-49", reason
		}
		g.pin(id, d)
	}
	issue := spec.issue
	if issue == nil {
		issue = landedIssue()
	}
	g.pin("GATE-49", issue)

	var lines []events.Envelope
	for i, id := range ids {
		if spec.isLanded(id) {
			lines = append(lines, jl(at(10+i, 0), id, "", journal.EventLanded, func(l *journal.Line) { l.Base, l.Into = planBranch, "GATE-49" }))
		}
	}
	lines = append(lines, spec.issueLines...)
	if spec.issueLines == nil && spec.issue == nil {
		lines = append(lines, jl(at(30, 0), "GATE-49", "", journal.EventLanded, func(l *journal.Line) { l.Base = "main" }))
	}
	g.feed(lines...)
	g.press("end", "enter")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want the chart screen:\n%s", g.m.Screen(), g.view())
	}
	return g
}

// planStatus is `jig status GATE-49`.
func planStatus(spec planSpec) string {
	need := spec.need
	if need == "" {
		need = planNeed
	}
	blocks := []string{"ticket: GATE-49", "state: issue", "kind: issue", "chart: repeat", "need: " + axi.Quote(need)}
	if !spec.noSuiteTable {
		suite := spec.suite
		if suite == nil {
			suite = []string{"demo,undeclared,-,abcdef1234567,-,-,-,-,-"}
		}
		// The rows are written as the verb prints them, quoting and all.
		lines := []string{"suite[" + strconv.Itoa(len(suite)) + "]{repo,standing,command,tip,seconds,signature,waiver,fixes,why}:"}
		for _, r := range suite {
			lines = append(lines, "  "+r)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n") + "\n"
}

// issueLine is the line of the Issue panel with label in v: its icon, then the
// label, then its words.
func issueLine(v, label string) string {
	for _, line := range strings.Split(v, "\n") {
		if f := strings.Fields(strings.Trim(line, " │▸")); len(f) > 2 && f[1] == label && strings.Contains(line, "│") {
			return line
		}
	}
	return ""
}

// TestIssuePlanListsItsTasksWithWhereEachLanded: the chart screen of a chart
// that plans an issue is the issue's plan: the need, then the tasks in the
// chart's order each with where it landed, and a task that has not landed says
// where it stands, not a landing.
func TestIssuePlanListsItsTasksWithWhereEachLanded(t *testing.T) {
	t.Parallel()
	g := planRig(t, planSpec{landed: map[string]bool{"T-1": true, "T-2": true, "T-3": true}})
	g.m.height = 40
	v := stripANSI(g.view())

	last := -1
	for i, want := range []string{
		"T-1  Task T-1", "T-2  Task T-2", "T-3  Task T-3", "T-4  Task T-4",
	} {
		at := strings.Index(v, want)
		if at < 0 || at < last {
			t.Fatalf("the plan lacks %q, or has it out of the chart's order (%d):\n%s", want, i, v)
		}
		last = at
	}
	for i, id := range []string{"T-1", "T-2", "T-3"} {
		want := "landed · into the issue's branch at 09:1" + strconv.Itoa(i) + ":00"
		if row := lineWith(v, id+"  Task"); !strings.Contains(row, want) {
			t.Errorf("%s's row lacks %q:\n%s", id, want, row)
		}
	}
	if row := lineWith(v, "T-4  Task"); strings.Contains(row, "landed") || !strings.Contains(row, "waits to land") {
		t.Errorf("T-4 has not landed, its row says:\n%s", row)
	}
	for _, want := range []string{"Need", "Review rounds repeat the same findings.", "Stop them from coming back once a person has decided.", "Tasks", "3 of 4 landed", "Issue"} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "task of GATE-49") {
		t.Errorf("a task row repeats the issue it is listed under:\n%s", v)
	}
	if strings.Contains(v, "Map") || strings.Contains(v, "Tickets") {
		t.Errorf("the plan draws the chart's own panels:\n%s", v)
	}
}

// TestIssuePlanStagesNameBranchSuitePullRequestAndLanding: for an issue that
// landed with no suite declared, its Issue panel names the branch, the suite as
// none recorded, the pull request by number and target, and the landing.
func TestIssuePlanStagesNameBranchSuitePullRequestAndLanding(t *testing.T) {
	t.Parallel()
	g := planRig(t, planSpec{})
	g.m.height = 40
	v := stripANSI(g.view())
	// A line carries the icon of its state: the tasks all landed, and the merged
	// pull request, are done; some tasks landed is under way.
	for label, icon := range map[string]string{"tasks": "✓", "PR": "✓", "landing": "✓", "branch": "✓", "suite": "○"} {
		if row := issueLine(v, label); !strings.Contains(row, icon+" "+label) {
			t.Errorf("the %s line lacks the icon %s: %q", label, icon, row)
		}
	}
	some := planRig(t, planSpec{issue: &digestInfo{Kind: "issue", State: "waiting"}, landed: map[string]bool{"T-1": true}})
	some.m.height = 40
	if row := issueLine(stripANSI(some.view()), "tasks"); !strings.Contains(row, "◐ tasks") || !strings.Contains(row, "1 of 4 landed") {
		t.Errorf("one task of four landed: %q", row)
	}
	closedPR := planRig(t, planSpec{issue: &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{{Number: 512, State: "closed", Target: "main"}}}})
	closedPR.m.height = 40
	if row := issueLine(stripANSI(closedPR.view()), "PR"); !strings.Contains(row, "✖ PR") || !strings.Contains(row, "PR #512 into main · closed") {
		t.Errorf("a pull request closed on the host: %q", row)
	}
	for label, want := range map[string]string{
		"branch":  planBranch + " · from origin/main",
		"tasks":   "4 of 4 landed",
		"suite":   "none recorded (the repo leaves it to CI)",
		"PR":      "PR #512 into main · merged",
		"landing": "landed · into main at 09:30:00",
	} {
		row := issueLine(v, label)
		if !strings.Contains(row, want) {
			t.Errorf("the %s line is %q, want it to say %q:\n%s", label, row, want, v)
		}
	}
}

// TestIssuePlanSuiteLineForEachStanding: each standing of the suite reads in its
// own words, one line for each repo, and a failure is drawn red and names the
// fix task it was followed by, which is marked on its own row.
func TestIssuePlanSuiteLineForEachStanding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		suite []string
		want  []string
	}{
		{"pass", []string{"demo,pass,go test ./...,4d577e2aaaaaa,41,-,-,-,-"}, []string{"passed at 4d577e2 in 41 s: `go test ./...`"}},
		{"fail with its fix task", []string{"demo,fail,go test ./...,4d577e2aaaaaa,9,\"final:boom\",-,T-5,-", "demo,pass,go test ./...,9999999aaaaaa,12,-,-,-,-"}, []string{"failed at 4d577e2: final:boom · fix: T-5", "passed at 9999999 in 12 s"}},
		{"waived", []string{"demo,waived,go test ./...,4d577e2aaaaaa,-,-,\"ci runs it\",-,-"}, []string{"waived: ci runs it"}},
		{"unrun", []string{"demo,unrun,go test ./...,4d577e2aaaaaa,-,-,-,-,-"}, []string{"not run at 4d577e2"}},
		{"no clone", []string{"demo,unrun,-,-,-,-,-,-,\"demo has no clone on this machine\""}, []string{"demo has no clone on this machine"}},
		{"two repos", []string{"api,pass,go test ./...,1111111aaaaaa,3,-,-,-,-", "web,undeclared,-,2222222aaaaaa,-,-,-,-,-"}, []string{"api: passed at 1111111 in 3 s", "web: none recorded"}},
	}
	for _, c := range cases {
		spec := planSpec{suite: c.suite}
		if c.name == "fail with its fix task" {
			spec.tasks = []string{"T-1", "T-2", "T-3", "T-4", "T-5"}
		}
		g := planRig(t, spec)
		g.m.height = 40
		v := stripANSI(g.view())
		for _, want := range c.want {
			if !strings.Contains(v, want) {
				t.Errorf("%s: the plan lacks %q:\n%s", c.name, want, v)
			}
		}
		if n := strings.Count(v, "suite "); n < len(c.suite) {
			t.Errorf("%s: %d suite lines, want one for each of %d rows:\n%s", c.name, n, len(c.suite), v)
		}
		if c.name == "fail with its fix task" {
			if row := lineWith(v, "↳"); !strings.Contains(row, "added after the failed suite at 4d577e2") {
				t.Errorf("the fix task is not marked under its row:\n%s", v)
			}
			if strings.Count(v, "↳") != 1 {
				t.Errorf("more than the fix task is marked:\n%s", v)
			}
		}
	}

	// A failure is drawn red, a pass is not.
	g := planRig(t, planSpec{})
	g.m.st = styles{r: colored()}
	for standing, red := range map[string]bool{"fail": true, "pass": false, "unrun": false} {
		state, _ := g.m.suiteWords(g.m.w.charts["repeat"].info.Issue, statusSuite{Standing: standing, Tip: "4d577e2aaaaaa", Command: "go test"}, true)
		if (state == StateStalled) != red {
			t.Errorf("a %s suite is drawn as %s", standing, state)
		}
	}
	_, words := g.m.suiteWords(g.m.w.charts["repeat"].info.Issue, statusSuite{Standing: "fail", Tip: "4d577e2aaaaaa", Signature: "x"}, true)
	if !strings.Contains(words, "\x1b[31m") {
		t.Errorf("a failed suite is not red: %q", words)
	}
	if !strings.Contains(words, "add a fix task for GATE-49") {
		t.Errorf("a failed suite with no fix task does not say to add one: %q", words)
	}
}

// TestIssuePlanPullRequestWaitsForTheSuiteAndTheTasks: the issue's pull request
// reads not opened yet and what it waits for, until the suite and every task
// landed; then the prompt that opens it.
func TestIssuePlanPullRequestWaitsForTheSuiteAndTheTasks(t *testing.T) {
	t.Parallel()
	waiting := &digestInfo{Kind: "issue", State: "waiting"}
	pr := func(g *rig) string {
		g.m.height = 40
		return issueLine(stripANSI(g.view()), "PR")
	}
	landedSome := map[string]bool{"T-1": true, "T-2": true}
	if row := pr(planRig(t, planSpec{issue: waiting, landed: landedSome, suite: []string{"demo,unrun,go test,abcdef1234567,-,-,-,-,-"}})); !strings.Contains(row, "not opened yet · waits for 2 tasks and the suite") {
		t.Errorf("two tasks not landed and the suite not run: %q", row)
	}
	if row := pr(planRig(t, planSpec{issue: waiting, landed: landedSome})); !strings.Contains(row, "not opened yet · waits for 2 tasks") || strings.Contains(row, "suite") {
		t.Errorf("two tasks not landed, no suite declared: %q", row)
	}
	if row := pr(planRig(t, planSpec{issue: waiting, suite: []string{"demo,fail,go test,abcdef1234567,5,x,-,-,-"}})); !strings.Contains(row, "not opened yet · waits for the suite") {
		t.Errorf("every task landed, the suite failed: %q", row)
	}
	ready := &digestInfo{Kind: "issue", State: "ready"}
	if row := pr(planRig(t, planSpec{issue: ready, prompt: "Publish GATE-49", suite: []string{"demo,pass,go test,abcdef1234567,5,-,-,-,-"}})); !strings.Contains(row, `not opened yet · say "Publish GATE-49"`) {
		t.Errorf("everything met: %q", row)
	}
	// A task closed without landing waits for nothing and lands nothing: it reads
	// closed with its reason, and the count of landed tasks leaves it out.
	closed := planRig(t, planSpec{issue: waiting, landed: map[string]bool{"T-1": true, "T-2": true, "T-3": true}, closed: map[string]string{"T-4": "not planned"}})
	closed.m.height = 40
	cv := stripANSI(closed.view())
	if row := lineWith(cv, "T-4  Task"); !strings.Contains(row, "⊖") || !strings.Contains(row, "closed · not planned") {
		t.Errorf("a closed task reads:\n%s", row)
	}
	if row := issueLine(cv, "PR"); !strings.Contains(row, "ready to open") || strings.Contains(row, "waits for") {
		t.Errorf("a closed task holds the pull request back: %q", row)
	}
	if row := issueLine(cv, "tasks"); !strings.Contains(row, "3 of 4 landed") {
		t.Errorf("a closed task counts as landed: %q", row)
	}
	// Before the status is read the issue's own unmet list says it.
	g := planRig(t, planSpec{issue: waiting, noSuiteTable: true, unmet: []map[string]any{{"code": "LAND_SUITE_NOT_RUN", "why": "no run"}}})
	if row := pr(g); !strings.Contains(row, "waits for the suite") {
		t.Errorf("a suite the chart says is unmet: %q", row)
	}
	gone := planRig(t, planSpec{issue: &digestInfo{Kind: "issue", State: "waiting", Closed: "not planned"}})
	gone.m.height = 40
	if row := issueLine(stripANSI(gone.view()), "landing"); !strings.Contains(row, "⊖ landing") || !strings.Contains(row, "closed · not planned") {
		t.Errorf("a closed issue's landing: %q", row)
	}
	// An open pull request reads open, and the landing waits for the person.
	open := &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{{Number: 512, State: "open", Target: "main"}}}
	g = planRig(t, planSpec{issue: open})
	g.m.height = 40
	v := stripANSI(g.view())
	if row := issueLine(v, "PR"); !strings.Contains(row, "PR #512 into main · open") {
		t.Errorf("an open pull request: %q", row)
	}
	if row := issueLine(v, "landing"); !strings.Contains(row, `waits to land · say "Land GATE-49"`) {
		t.Errorf("the landing of a published issue: %q", row)
	}
}

// TestALegacyChartScreenIsUnchanged: a chart whose verb prints no issue block is
// drawn as ever, with its Tickets panel and no plan panel.
func TestALegacyChartScreenIsUnchanged(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	if g.m.w.charts["checkout"].issueID() != "" {
		t.Fatal("the legacy chart has an issue")
	}
	v := g.view()
	for _, want := range []string{"Tickets", "Run", "Activity"} {
		if !strings.Contains(v, want) {
			t.Errorf("the legacy chart lacks %q:\n%s", want, v)
		}
	}
	for _, bad := range []string{"Need", "Issue", "Tasks"} {
		if strings.Contains(v, bad) {
			t.Errorf("the legacy chart draws %q:\n%s", bad, v)
		}
	}
	golden(t, "chart", v)
	if n := g.r.ran("status GATE-49 --store /s"); n != 0 {
		t.Errorf("a legacy chart read an issue %d times", n)
	}
}
