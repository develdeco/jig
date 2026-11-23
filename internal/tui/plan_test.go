package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// The plan fixtures: an issue SHOP-49 whose chart "repeat" lists tasks SHOP-1 to
// SHOP-4, all of them published into the issue's branch.

const (
	planBranch = "501-review-rounds-stop-repeating-work"
	planNeed   = "Review rounds repeat the same findings.\nStop them from coming back once a person has decided."
)

// planSpec is what a plan fixture shows; the zero value is the issue that
// landed everything with no suite declared and its pull request into main
// merged and landed.
type planSpec struct {
	// tasks are the chart's tasks in order (SHOP-1 to SHOP-4 when none); landed says
	// which of them landed into the branch (all when nil).
	tasks  []string
	landed map[string]bool
	// titles are the tasks' titles where they are not "Task" and the id.
	titles map[string]string
	// suite are the rows of the status's suite table, comma separated.
	suite []string
	// noSuiteTable leaves the table out, as an older jig's status does.
	noSuiteTable bool
	need         string
	status       string
	issue        *digestInfo
	issueLines   []events.Envelope
	unmet        []map[string]any
	waits        []map[string]any
	prompt       string
	// closed are the tasks closed without landing, each with its reason.
	closed map[string]string
	// second adds an issue SHOP-50 whose chart lists SHOP-1 too, and loose a task
	// SHOP-9 that no chart lists.
	second, loose bool
	// multi adds the multi-task SHOP-7 to the chart with its tasks SHOP-5, landed into
	// it, and SHOP-6, not.
	multi bool
	// multiDigest replaces the multi-task's digest (published, its pull request
	// open) and kidsLanded lands SHOP-6 too.
	multiDigest *digestInfo
	kidsLanded  bool
}

func (s planSpec) taskIDs() []string {
	if len(s.tasks) == 0 {
		return []string{"SHOP-1", "SHOP-2", "SHOP-3", "SHOP-4"}
	}
	return s.tasks
}

// titleOf is a task's title: the spec's own, else "Task" and its id.
func (s planSpec) titleOf(id string) string {
	if t, ok := s.titles[id]; ok {
		return t
	}
	return "Task " + id
}

func (s planSpec) isLanded(id string) bool { return s.landed == nil || s.landed[id] }

// landedIssue is the digest of an issue whose pull request merged into main and
// landed.
func landedIssue() *digestInfo {
	return &digestInfo{Kind: "issue", State: "landed", PRs: []digestPR{{Repo: "demo", Number: 512, URL: "https://example.test/acme/demo/pull/512", State: "merged", Target: "main"}}}
}

// planMain is the project of the plan fixtures on the main screen: the store's list, the chart's
// verb and the issue's status are answered as spec says, the tasks' digests are
// pinned, and the screen is the project's.
func planMain(t *testing.T, spec planSpec) *rig {
	t.Helper()
	g := newRig(t, "")
	ids := spec.taskIDs()
	if spec.multi {
		// A multi-task of the chart and its two tasks, listed after the others.
		ids = append(slices.Clone(ids), "SHOP-7", "SHOP-5", "SHOP-6")
	}
	tickets := []map[string]any{{"id": "SHOP-49", "title": "Review rounds stop repeating work", "state": "issue"}}
	entries := []map[string]any{}
	for _, id := range ids {
		tickets = append(tickets, map[string]any{"id": id, "title": spec.titleOf(id), "state": "done"})
		entries = append(entries, entry(id, spec.titleOf(id), "publish", []string{"done", "done", "done", "done"}, false))
	}
	charts := []map[string]any{{"name": "repeat", "title": "Repeat rounds", "tickets": ids, "ungraduated": 0, "issue": "SHOP-49"}}
	if spec.loose {
		tickets = append(tickets, map[string]any{"id": "SHOP-9", "title": "Task SHOP-9", "state": "done"})
	}
	if spec.second {
		// SHOP-1 is listed by a second issue's chart too: a shared task.
		tickets = append(tickets, map[string]any{"id": "SHOP-50", "title": "Another issue", "state": "issue"})
		charts = append(charts, map[string]any{"name": "second", "title": "Second plan", "tickets": []string{"SHOP-1"}, "ungraduated": 0, "issue": "SHOP-50"})
	}
	roster := jsonLine(map[string]any{"tickets": tickets, "charts": charts})
	issueBlock := map[string]any{
		"ticket": "SHOP-49", "stage": "landing", "state": "waiting", "branch": planBranch, "prompt": spec.prompt,
		"stack": []map[string]any{{"repo": "demo", "start_from": "origin/main", "pr_target": "main"}},
	}
	if spec.unmet != nil {
		issueBlock["unmet"] = spec.unmet
	}
	if spec.waits != nil {
		issueBlock["waits"] = spec.waits
	}
	verb := jsonLine(map[string]any{
		"chart": "repeat", "title": "Repeat rounds", "ready": false, "code": "CHART_DONE", "started": true, "ungraduated": 0,
		"briefs": []string{}, "run": [][]string{}, "tickets": entries,
		"map":   map[string]any{"destination": []string{"Plans issue SHOP-49."}},
		"issue": issueBlock,
	})
	status := map[string]string{"SHOP-49": planStatus(spec)}
	for _, id := range ids {
		status[id] = untouchedStatus(id)
	}
	answerChart(g, roster, "repeat", verb, status)
	if spec.second {
		second := jsonLine(map[string]any{
			"chart": "second", "title": "Second plan", "ready": false, "code": "CHART_DONE", "started": true, "ungraduated": 0,
			"briefs": []string{}, "run": [][]string{}, "tickets": []map[string]any{entries[0]},
			"map":   map[string]any{"destination": []string{"Plans issue SHOP-50."}},
			"issue": map[string]any{"ticket": "SHOP-50", "stage": "landing", "state": "waiting", "branch": "502-another", "stack": []map[string]any{{"repo": "demo", "start_from": "origin/main", "pr_target": "main"}}},
		})
		g.r.answer("next second --json --store /s", second, 0)
		g.r.answer("status SHOP-50 --store /s", strings.ReplaceAll(planStatus(spec), "SHOP-49", "SHOP-50"), 0)
		g.pin("SHOP-50", &digestInfo{Kind: "issue", State: "waiting"})
	}
	if spec.loose {
		g.r.answer("status SHOP-9 --store /s", untouchedStatus("SHOP-9"), 0)
		g.pin("SHOP-9", task("build", true, "done|no brief on record", "current|ready to build", "pending|after the build", "pending|after a clean review", "pending|after the publish"))
	}

	for _, id := range ids {
		d := task("land", false, "done|no brief on record", "done|1 slice green", "done|round 1 clean", "done|pull request opened", "current|waits for a merge")
		d.Parent = "SHOP-49"
		if spec.isLanded(id) {
			d.Phases[4] = digestPhase{Name: "land", State: "done", Text: "landed"}
		}
		if reason, ok := spec.closed[id]; ok {
			d = task("publish", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
			d.Parent, d.Closed = "SHOP-49", reason
		}
		if spec.second && id == "SHOP-1" {
			// A task two issues list has no one parent, and one pull request into
			// each issue's branch.
			d.Parent = ""
			d.PRs = []digestPR{
				{Repo: "demo", Number: 3, State: "merged", Target: planBranch},
				{Repo: "demo", Number: 4, State: "open", Target: "502-another"},
			}
		}
		switch id {
		case "SHOP-7":
			d = &digestInfo{Kind: "multi-task", Parent: "SHOP-49", State: "published", Children: []digestChild{{ID: "SHOP-5", Title: "Task SHOP-5"}, {ID: "SHOP-6", Title: "Task SHOP-6"}},
				PRs: []digestPR{{Repo: "demo", Number: 7, State: "open", Target: planBranch}}}
			if spec.multiDigest != nil {
				d = spec.multiDigest
			}
		case "SHOP-5", "SHOP-6":
			d.Parent = "SHOP-7"
			if id == "SHOP-6" && !spec.kidsLanded {
				d.Phases[4] = digestPhase{Name: "land", State: "current", Text: "waits for a merge"}
			}
		}
		g.pin(id, d)
	}
	if spec.multi {
		g.r.answer("status SHOP-7 --store /s", "ticket: SHOP-7\nstate: multi-task\nkind: multi-task\nbranch: 503-the-multi-task\nneed: \"What the multi-task is for.\"\n", 0)
	}
	issue := spec.issue
	if issue == nil {
		issue = landedIssue()
	}
	g.pin("SHOP-49", issue)

	var lines []events.Envelope
	for i, id := range ids {
		switch {
		case id == "SHOP-5":
			lines = append(lines, jl(at(20, 0), id, "", journal.EventLanded, func(l *journal.Line) { l.Base, l.Into = "503-the-multi-task", "SHOP-7" }))
		case id == "SHOP-6" && spec.kidsLanded:
			lines = append(lines, jl(at(21, 0), id, "", journal.EventLanded, func(l *journal.Line) { l.Base, l.Into = "503-the-multi-task", "SHOP-7" }))
		case id == "SHOP-7" && spec.multiDigest != nil && spec.multiDigest.State == "landed":
			lines = append(lines, jl(at(25, 0), id, "", journal.EventLanded, func(l *journal.Line) { l.Base, l.Into = planBranch, "SHOP-49" }))
		case id == "SHOP-7" || id == "SHOP-6":
		case spec.isLanded(id):
			lines = append(lines, jl(at(10+i, 0), id, "", journal.EventLanded, func(l *journal.Line) { l.Base, l.Into = planBranch, "SHOP-49" }))
		}
	}
	lines = append(lines, spec.issueLines...)
	if spec.issueLines == nil && spec.issue == nil {
		lines = append(lines, jl(at(30, 0), "SHOP-49", "", journal.EventLanded, func(l *journal.Line) { l.Base = "main" }))
	}
	g.feed(lines...)
	return g
}

// planRig is planMain with the chart screen of the issue's chart open, which is
// the issue's plan.
func planRig(t *testing.T, spec planSpec) *rig {
	t.Helper()
	g := planMain(t, spec)
	g.press("end", "enter")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" {
		t.Fatalf("on %s, want the chart screen:\n%s", g.m.Screen(), g.view())
	}
	return g
}

// planStatus is `jig status SHOP-49`.
func planStatus(spec planSpec) string {
	if spec.status != "" {
		return spec.status
	}
	need := spec.need
	if need == "" {
		need = planNeed
	}
	blocks := []string{"ticket: SHOP-49", "state: issue", "kind: issue", "chart: repeat", "need: " + axi.Quote(need)}
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
	g := planRig(t, planSpec{landed: map[string]bool{"SHOP-1": true, "SHOP-2": true, "SHOP-3": true}})
	g.m.height, g.m.width = 40, 100
	v := stripANSI(g.view())

	last := -1
	for i, want := range []string{
		"SHOP-1  Task SHOP-1", "SHOP-2  Task SHOP-2", "SHOP-3  Task SHOP-3", "SHOP-4  Task SHOP-4",
	} {
		at := strings.Index(v, want)
		if at < 0 || at < last {
			t.Fatalf("the plan lacks %q, or has it out of the chart's order (%d):\n%s", want, i, v)
		}
		last = at
	}
	for i, id := range []string{"SHOP-1", "SHOP-2", "SHOP-3"} {
		want := "landed · into the issue's branch at 09:1" + strconv.Itoa(i) + ":00"
		if row := lineWith(v, id+"  Task"); !strings.Contains(row, want) {
			t.Errorf("%s's row lacks %q:\n%s", id, want, row)
		}
	}
	if row := lineWith(v, "SHOP-4  Task"); strings.Contains(row, "landed") || !strings.Contains(row, "waits to land") {
		t.Errorf("SHOP-4 has not landed, its row says:\n%s", row)
	}
	for _, want := range []string{"Need", "Review rounds repeat the same findings.", "Stop them from coming back once a person has decided.", "Tasks", "3 of 4 landed", "Issue"} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "task of SHOP-49") {
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
	g.m.height, g.m.width = 40, 100
	v := stripANSI(g.view())
	// A line carries the icon of its state: the tasks all landed, and the merged
	// pull request, are done; some tasks landed is under way.
	for label, icon := range map[string]string{"tasks": "✓", "PR": "✓", "landing": "✓", "branch": "✓", "suite": "○"} {
		if row := issueLine(v, label); !strings.Contains(row, icon+" "+label) {
			t.Errorf("the %s line lacks the icon %s: %q", label, icon, row)
		}
	}
	some := planRig(t, planSpec{issue: &digestInfo{Kind: "issue", State: "waiting"}, landed: map[string]bool{"SHOP-1": true}})
	some.m.height, some.m.width = 40, 100
	if row := issueLine(stripANSI(some.view()), "tasks"); !strings.Contains(row, "◐ tasks") || !strings.Contains(row, "1 of 4 landed") {
		t.Errorf("one task of four landed: %q", row)
	}
	closedPR := planRig(t, planSpec{issue: &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{{Number: 512, State: "closed", Target: "main"}}}})
	closedPR.m.height = 40
	if row := issueLine(stripANSI(closedPR.view()), "PR"); !strings.Contains(row, "✗ PR") || !strings.Contains(row, "PR #512 into main · closed") {
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
		{"fail with its fix task", []string{"demo,fail,go test ./...,4d577e2aaaaaa,9,\"final:boom\",-,SHOP-5,-", "demo,pass,go test ./...,9999999aaaaaa,12,-,-,-,-"}, []string{"failed at 4d577e2: final:boom · fix: SHOP-5", "passed at 9999999 in 12 s"}},
		{"waived", []string{"demo,waived,go test ./...,4d577e2aaaaaa,-,-,\"ci runs it\",-,-"}, []string{"waived: ci runs it"}},
		{"unrun", []string{"demo,unrun,go test ./...,4d577e2aaaaaa,-,-,-,-,-"}, []string{"not run at 4d577e2"}},
		{"no clone", []string{"demo,unrun,-,-,-,-,-,-,\"demo has no clone on this machine\""}, []string{"demo has no clone on this machine"}},
		{"two repos", []string{"api,pass,go test ./...,1111111aaaaaa,3,-,-,-,-", "web,undeclared,-,2222222aaaaaa,-,-,-,-,-"}, []string{"api: passed at 1111111 in 3 s", "web: none recorded"}},
	}
	for _, c := range cases {
		spec := planSpec{suite: c.suite}
		if c.name == "fail with its fix task" {
			spec.tasks = []string{"SHOP-1", "SHOP-2", "SHOP-3", "SHOP-4", "SHOP-5"}
		}
		g := planRig(t, spec)
		g.m.height, g.m.width = 40, 100
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
	if !strings.Contains(words, "add a fix task for SHOP-49") {
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
		g.m.height, g.m.width = 40, 100
		return issueLine(stripANSI(g.view()), "PR")
	}
	landedSome := map[string]bool{"SHOP-1": true, "SHOP-2": true}
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
	if row := pr(planRig(t, planSpec{issue: ready, prompt: "Publish SHOP-49", suite: []string{"demo,pass,go test,abcdef1234567,5,-,-,-,-"}})); !strings.Contains(row, `not opened yet · say "Publish SHOP-49"`) {
		t.Errorf("everything met: %q", row)
	}
	// The words come from the status that derives them: its unmet suite is waited
	// for, its prompt is said when nothing is left.
	statusOf := func(extra string) planSpec {
		return planSpec{issue: waiting, noSuiteTable: true, status: "ticket: SHOP-49\nstate: issue\nkind: issue\nchart: repeat\nneed: x\n" + extra}
	}
	suiteUnmet := planRig(t, statusOf("standing: waiting\nunmet[1]{code,why,prompt}:\n  LAND_SUITE_FAILED,failed,Add a fix task for SHOP-49\n"))
	if row := pr(suiteUnmet); !strings.Contains(row, "not opened yet · waits for the suite") || strings.Contains(row, "Publish") {
		t.Errorf("an unmet suite in the status: %q", row)
	}
	publishReady := planRig(t, statusOf("standing: ready\nprompt: Publish SHOP-49\n"))
	if row := pr(publishReady); !strings.Contains(row, `not opened yet · say "Publish SHOP-49"`) {
		t.Errorf("a ready status: %q", row)
	}
	// A task closed without landing waits for nothing and lands nothing: it reads
	// closed with its reason, and the count of landed tasks leaves it out.
	closed := planRig(t, planSpec{issue: waiting, landed: map[string]bool{"SHOP-1": true, "SHOP-2": true, "SHOP-3": true}, closed: map[string]string{"SHOP-4": "not planned"}})
	closed.m.height, closed.m.width = 40, 100
	cv := stripANSI(closed.view())
	if row := lineWith(cv, "SHOP-4  Task"); !strings.Contains(row, "⊖") || !strings.Contains(row, "closed · not planned") {
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
	gone.m.height, gone.m.width = 40, 100
	if row := issueLine(stripANSI(gone.view()), "landing"); !strings.Contains(row, "⊖ landing") || !strings.Contains(row, "closed · not planned") {
		t.Errorf("a closed issue's landing: %q", row)
	}
	// An open pull request reads open, and the landing waits for the person.
	open := &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{{Number: 512, State: "open", Target: "main"}}}
	g = planRig(t, planSpec{issue: open})
	g.m.height, g.m.width = 40, 100
	v := stripANSI(g.view())
	if row := issueLine(v, "PR"); !strings.Contains(row, "PR #512 into main · open") {
		t.Errorf("an open pull request: %q", row)
	}
	if row := issueLine(v, "landing"); !strings.Contains(row, `waits to land · say "Land SHOP-49"`) {
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
	if n := g.r.ran("status SHOP-49 --store /s"); n != 0 {
		t.Errorf("a legacy chart read an issue %d times", n)
	}
}
