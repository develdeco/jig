package tui

import (
	"strings"
	"testing"
)

// mainOf is the project's list of a plan fixture, every fold open, as text.
func mainOf(t *testing.T, spec planSpec) (*rig, string) {
	t.Helper()
	g := planMain(t, spec)
	g.m.height, g.m.width = 40, 110
	return g, stripANSI(g.unfold().mainText())
}

// indentOf is the column the id of a row starts at: a task is indented under its
// issue in the id column.
func indentOf(text, id string) int {
	for _, line := range strings.Split(text, "\n") {
		if f := strings.Fields(line); len(f) > 2 && f[1] == id {
			return len([]rune(line[:strings.Index(line, id)]))
		}
	}
	return -1
}

var waitingIssue = &digestInfo{Kind: "issue", State: "waiting"}

// TestMainDrawsAnIssueWithItsTasks: an issue is a row with the tasks of its chart
// under it, indented, in the chart's order, a multi-task's own tasks one level
// further in; no task is a row of its own, the chart that plans the issue has no
// row, and the header leads with the count of issues.
func TestMainDrawsAnIssueWithItsTasks(t *testing.T) {
	t.Parallel()
	g, text := mainOf(t, planSpec{issue: waitingIssue, landed: map[string]bool{"SHOP-1": true}, multi: true})
	if got, want := strings.Join(rowIDs(text), " "), "SHOP-49 SHOP-1 SHOP-2 SHOP-3 SHOP-4 SHOP-7 SHOP-5 SHOP-6"; got != want {
		t.Errorf("the rows read %s, want the issue then its tasks in the chart's order %s:\n%s", got, want, text)
	}
	if issue, task, nested := indentOf(text, "SHOP-49"), indentOf(text, "SHOP-2"), indentOf(text, "SHOP-5"); !(issue < task && task < nested) {
		t.Errorf("indents %d, %d, %d: want the tasks in from the issue, a multi-task's in further:\n%s", issue, task, nested, text)
	}
	if strings.Contains(text, "  chart  ") {
		t.Errorf("the chart that plans the issue has a row:\n%s", text)
	}
	if head := g.m.header(110)[0]; !strings.Contains(head, "1 issue ·") {
		t.Errorf("the header does not lead with the issues: %q", head)
	}
	// A store with no issue has no count.
	plain := planMain(t, planSpec{loose: true})
	plain.m.w.tickets["SHOP-49"].digest = task("build", true, "done|a", "current|ready to build", "pending|b", "pending|c", "pending|d")
	plain.m.w.gen++
	if head := plain.m.header(110)[0]; strings.Contains(head, "issue") {
		t.Errorf("a store with no issue counts them: %q", head)
	}
}

// rowIDs are the ids of the rows of a list as text, top to bottom: the second
// word of a line that starts with an icon.
func rowIDs(text string) []string {
	var ids []string
	for _, line := range strings.Split(text, "\n") {
		if f := strings.Fields(line); len(f) > 2 && width(f[0]) == 1 && strings.Contains(f[1], "-") {
			ids = append(ids, f[1])
		}
	}
	return ids
}

// TestMainAnIssueTakesTheGroupOfItsNextMove: the issue is in the first group that
// holds for it or any of its tasks, as a ticket in that group would be.
func TestMainAnIssueTakesTheGroupOfItsNextMove(t *testing.T) {
	t.Parallel()
	groupOf := func(spec planSpec) string {
		g, _ := mainOf(t, spec)
		for name, ids := range groupRows(g.m) {
			for _, id := range ids {
				if id == "SHOP-49" {
					return name
				}
			}
		}
		return ""
	}
	// A task waits to land at the person's word.
	if got := groupOf(planSpec{issue: waitingIssue, landed: map[string]bool{"SHOP-1": true}}); got != "needs you" {
		t.Errorf("an issue with a task that waits to land is in %q, want needs you", got)
	}
	// Every task landed: the issue's own state says.
	for state, want := range map[string]string{"waiting": "waiting", "ready": "ready"} {
		if got := groupOf(planSpec{issue: &digestInfo{Kind: "issue", State: state}}); got != want {
			t.Errorf("an issue %s with its tasks landed is in %q, want %q", state, got, want)
		}
	}
	// A task is being built: the issue is running.
	g, _ := mainOf(t, planSpec{issue: waitingIssue})
	building := task("build", false, "done|approved by Ada", "current|1 of 2 green", "pending|after the build", "pending|after a clean review", "pending|after the publish")
	building.Parent, building.Slices = "SHOP-49", &digestSlices{Total: 2, Green: 1, Building: 1}
	g.pin("SHOP-2", building)
	g.m.w.gen++
	g.m.w.tickets["SHOP-2"].digest = building
	if got := groupRows(g.m); !contains(got["running"], "SHOP-49") {
		t.Errorf("an issue with a task being built is not running: %v", got)
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// TestMainALandingStopSaysWhatToSay: an issue whose own pull request waits for
// the person's word is in needs you and says what the plan says at its landing.
func TestMainALandingStopSaysWhatToSay(t *testing.T) {
	t.Parallel()
	published := &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{{Repo: "demo", Number: 9, State: "open", Target: "main"}}}
	g, text := mainOf(t, planSpec{issue: published})
	if row := lineWith(text, "SHOP-49 "); !strings.Contains(row, "◇ SHOP-49") || !strings.Contains(row, `waits to land · say "Land SHOP-49"`) {
		t.Errorf("the issue's row at its landing stop:\n%s", text)
	}
	if got := groupRows(g.m); !contains(got["needs you"], "SHOP-49") {
		t.Errorf("a landing stop is not in needs you: %v", got)
	}
	// The plan says the same words, from the one function.
	if _, words := g.m.planLanding("SHOP-49", g.m.w.tickets["SHOP-49"]); !strings.Contains(lineWith(text, "SHOP-49 "), words) {
		t.Errorf("main and the plan word the landing apart: %q", words)
	}
	// Its pull request merged on the host and nobody recorded it.
	merged := &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{{Repo: "demo", Number: 9, State: "merged", Target: "main"}}}
	if _, text := mainOf(t, planSpec{issue: merged}); !strings.Contains(lineWith(text, "SHOP-49 "), "merged on the host, not recorded") {
		t.Errorf("a merge nobody recorded:\n%s", text)
	}
}

// TestMainALandedIssueFoldsIntoDone: a landed issue is in done, its tasks folded in
// with it: the fold row counts the issue and says its tasks are in it, and opened
// it shows the issue and its tasks.
func TestMainALandedIssueFoldsIntoDone(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{})
	g.m.height, g.m.width = 40, 110
	closed := stripANSI(pagesText(g.m.mainPage()))
	if !strings.Contains(closed, "▸ done (1) · 4 tasks in them · enter shows them") || strings.Contains(closed, "SHOP-1") {
		t.Errorf("the closed fold:\n%s", closed)
	}
	if _, open := mainOf(t, planSpec{}); !strings.Contains(open, "SHOP-49") || !strings.Contains(open, "SHOP-4 ") {
		t.Errorf("the open fold shows no issue and tasks:\n%s", open)
	}
	// The cursor on a task of a landed issue goes to the fold when it folds.
	g.unfold().focus("SHOP-3")
	g.m.nav[0].open = nil
	g.m.w.gen++
	g.m.View()
	if ref := g.m.selected(); ref == nil || ref.kind != rowFold {
		t.Errorf("the cursor of a folded-away task is on %+v, want the fold row", ref)
	}
}

// TestMainATaskOfTwoIssuesShowsUnderEach: a task two issues list is under each, and
// the cursor's row is the one under the issue it was on, kept through a roster.
func TestMainATaskOfTwoIssuesShowsUnderEach(t *testing.T) {
	t.Parallel()
	g, text := mainOf(t, planSpec{issue: waitingIssue, second: true})
	n := 0
	for _, id := range rowIDs(text) {
		if id == "SHOP-1" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("the shared task is drawn %d times, want under each issue:\n%s", n, text)
	}
	var onSecond int
	for i, r := range g.m.mainRefs() {
		if r.kind == rowTicket && r.id == "SHOP-1" && r.container == "SHOP-50" {
			onSecond = i
		}
	}
	e := g.m.top()
	e.sel, e.follow = onSecond, false
	g.m.noteCursor()
	// The store moves: a roster changes the order of the issues' rows.
	g.m.w.gen++
	g.m.w.tickets["SHOP-49"].digest = &digestInfo{Kind: "issue", State: "ready"}
	g.m.View()
	if ref := g.m.selected(); ref == nil || ref.id != "SHOP-1" || ref.container != "SHOP-50" {
		t.Errorf("the cursor is on %+v, want SHOP-1 under SHOP-50", ref)
	}
}
