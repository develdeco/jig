package tui

import (
	"strings"
	"testing"
)

// taskBrief is the brief screen of the plan fixture's task, over the status and
// text of a brief with four sections and four slices, at 100x60.
func taskBrief(t *testing.T, spec planSpec, task string) *rig {
	t.Helper()
	g := planMain(t, spec)
	g.r.answer("status "+task+" --store /s", relabelled(statusJIG1Brief(), "JIG-1", task), 0)
	g.r.answer("brief show "+task+" --text --store /s", relabelled(briefTextJIG1(), "JIG-1", task), 0)
	g.sized(100, 60).onRow(task).press("enter", "1")
	if g.m.Screen() != "brief" || g.m.top().ticket != task {
		t.Fatalf("on %s of %s, want the brief of %s:\n%s", g.m.Screen(), g.m.top().ticket, task, g.view())
	}
	return g
}

// issueDetailOf is the words of the detail under the Parts list of g.
func issueDetailOf(g *rig, title string) string {
	return readWords(between(g.view(), "╭─ Detail · "+title, "╰"))
}

// TestAnIssueIsARowWithItsNeedWholeAndTheTasksPlace: a task of an issue has the
// issue as the first part of its brief, saying it is ready, and the detail of that
// row is the issue's need, whole and drawn as a section's text is, then what the
// task waits for and what waits for it, nothing for either when there is none.
func TestAnIssueIsARowWithItsNeedWholeAndTheTasksPlace(t *testing.T) {
	t.Parallel()
	g := taskBrief(t, planSpec{}, "SHOP-2")
	rows := rowsOf(g.view(), "Parts")
	if len(rows) < 2 || !strings.HasPrefix(strings.TrimPrefix(rows[1], "▸ "), "issue SHOP-49 ready Review rounds stop repeating work") {
		t.Fatalf("the first part is not the issue, ready:\n%s", g.view())
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowIssue || ref.id != "SHOP-49" {
		t.Errorf("the brief opens on %+v, want the issue's row, its first", ref)
	}
	d := issueDetailOf(g, "SHOP-49")
	for _, want := range []string{
		"issue Review rounds stop repeating work",
		"need Review rounds repeat the same findings.",
		"Stop them from coming back once a person has decided.",
		"waits for nothing",
		"needed by nothing",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the issue lacks %q:\n%s", want, g.view())
		}
	}
	// A task whose brief is not written yet still has its issue, above the line
	// that says so.
	nb := planMain(t, planSpec{})
	nb.r.answer("status SHOP-2 --store /s", "ticket: SHOP-2\nstate: queued\n", 0)
	nb.sized(100, 60).onRow("SHOP-2").press("enter", "1")
	nbRows := rowsOf(nb.view(), "Parts")
	if nb.m.Screen() != "brief" || len(nbRows) < 2 || !strings.Contains(nbRows[1], "issue SHOP-49") || !strings.Contains(nb.view(), "this ticket has no brief") {
		t.Errorf("a task with no brief has no issue row:\n%s", nb.view())
	}
	if d := issueDetailOf(nb, "SHOP-49"); !strings.Contains(d, "need Review rounds repeat the same findings.") {
		t.Errorf("the issue's need is not shown for a task with no brief:\n%s", nb.view())
	}
	// The sections follow it.
	if got := strings.Join(rows, "\n"); strings.Index(got, "issue SHOP-49") > strings.Index(got, "section Goal") {
		t.Errorf("the issue is not before the sections:\n%s", got)
	}
}

// TestWhatATaskWaitsForAndWhatWaitsForItComeFromTheRoster: the unmet blockers the
// roster carries for the task are what it waits for, each with its kind and fact,
// and the tickets whose blockers name the task are what waits for it; the row says
// how many it waits for.
func TestWhatATaskWaitsForAndWhatWaitsForItComeFromTheRoster(t *testing.T) {
	t.Parallel()
	g := taskBrief(t, planSpec{}, "SHOP-2")
	g.m.w.tickets["SHOP-2"].blockers = []chartWait{
		{Ticket: "SHOP-1", Kind: "merged", Fact: "its PR is open"},
		{Ticket: "SHOP-9", Kind: "stacked", Fact: "its gate has no clean round"},
	}
	g.m.w.tickets["SHOP-3"].blockers = []chartWait{{Ticket: "SHOP-2", Kind: "merged", Fact: "its PR is open"}}
	g.m.w.tickets["SHOP-4"].blockers = []chartWait{{Ticket: "SHOP-2", Kind: "stacked", Fact: "its gate has no clean round"}, {Ticket: "SHOP-3", Kind: "merged"}}
	g.m.w.gen++
	d := issueDetailOf(g, "SHOP-49")
	for _, want := range []string{
		"waits for SHOP-1 · merged · its PR is open",
		"SHOP-9 · stacked · its gate has no clean round",
		"needed by SHOP-3 SHOP-4",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the issue lacks %q:\n%s", want, g.view())
		}
	}
	if rows := rowsOf(g.view(), "Parts"); !strings.Contains(rows[1], "waits for 2") {
		t.Errorf("the issue's row does not say it waits for 2: %q", rows[1])
	}
}

// TestEnterOnTheIssueRowOpensItsPlan: enter on the issue row opens the issue's
// screen, which is its plan, the way a ticket row of the issue does.
func TestEnterOnTheIssueRowOpensItsPlan(t *testing.T) {
	t.Parallel()
	g := taskBrief(t, planSpec{}, "SHOP-2")
	g.press("enter")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" {
		t.Errorf("enter on the issue opened %s %q, want the plan of the issue's chart", g.m.Screen(), g.m.top().chart)
	}
	g.press("esc")
	if g.m.Screen() != "brief" {
		t.Errorf("esc from the plan went to %s, want the brief", g.m.Screen())
	}
}

// TestATaskOfTwoIssuesHasARowForEach: a task two charts list has a row for each
// issue, each with its own need, before the sections.
func TestATaskOfTwoIssuesHasARowForEach(t *testing.T) {
	t.Parallel()
	g := taskBrief(t, planSpec{second: true}, "SHOP-1")
	rows := rowsOf(g.view(), "Parts")
	if len(rows) < 3 || !strings.Contains(rows[1], "issue SHOP-49") || !strings.Contains(rows[2], "issue SHOP-50 ready Another issue") {
		t.Fatalf("the first two parts are not the two issues:\n%s", g.view())
	}
	g.press("down")
	if d := issueDetailOf(g, "SHOP-50"); !strings.Contains(d, "need Review rounds repeat the same findings.") {
		t.Errorf("the detail of the second issue lacks its need:\n%s", g.view())
	}
	g.press("enter")
	if g.m.Screen() != "chart" || g.m.top().chart != "second" {
		t.Errorf("enter on the second issue opened %s %q, want the chart that plans it", g.m.Screen(), g.m.top().chart)
	}
}

// TestAnIssueHasNoBriefScreenItsPlanIsItsScreen: an issue is no task of an issue,
// so no row of this kind is drawn for it, and opening it opens its plan.
func TestAnIssueHasNoBriefScreenItsPlanIsItsScreen(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{})
	if got := g.m.issuesOfTask("SHOP-49"); len(got) != 0 {
		t.Errorf("the issue is a task of %v", got)
	}
	g.sized(100, 40).onRow("SHOP-49").press("enter")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" {
		t.Fatalf("the issue opened %s, want its plan", g.m.Screen())
	}
	g.press("1")
	if g.m.Screen() == "brief" {
		t.Errorf("the issue's plan opened a brief screen:\n%s", g.view())
	}
}

// TestAnUnreadIssueSaysItIsBeingRead: until the issue's status is read the detail
// of its row says so, and shows the task's place in the plan meanwhile.
func TestAnUnreadIssueSaysItIsBeingRead(t *testing.T) {
	t.Parallel()
	g := taskBrief(t, planSpec{}, "SHOP-2")
	g.m.w.tickets["SHOP-49"].status = nil
	d := issueDetailOf(g, "SHOP-49")
	if !strings.Contains(d, "need reading the issue's need") || !strings.Contains(d, "waits for nothing") {
		t.Errorf("the detail of an issue not yet read:\n%s", g.view())
	}
}
