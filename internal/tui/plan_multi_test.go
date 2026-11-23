package tui

import (
	"strings"
	"testing"
)

// TestMultiTaskScreenIsItsPlanOneLevelDown: a multi-task opens on the plan one
// level down: its body as the need, the tasks under it each with where it
// landed, and its own branch, how many tasks landed, its pull request into the
// issue's branch and its landing, with no suite; c goes up to the issue.
func TestMultiTaskScreenIsItsPlanOneLevelDown(t *testing.T) {
	t.Parallel()
	g := planRig(t, planSpec{multi: true})
	g.m.height, g.m.width = 40, 100
	onRow(t, g, "SHOP-7")
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "SHOP-7" {
		t.Fatalf("enter opened %s of %q, want the multi-task's screen", g.m.Screen(), g.m.top().ticket)
	}
	v := stripANSI(g.view())
	for _, want := range []string{
		"Need", "What the multi-task is for.",
		"Tasks", "1 of 2 landed", "SHOP-5", "landed · into the issue's branch", "SHOP-6", "waits to land",
		"Multi-task", "503-the-multi-task", "PR #7 into the issue's branch · open",
	} {
		if !strings.Contains(strings.ReplaceAll(v, planBranch, "the issue's branch"), want) {
			t.Errorf("the multi-task's plan lacks %q:\n%s", want, v)
		}
	}
	if row := issueLine(v, "tasks"); !strings.Contains(row, "1 of 2 landed") {
		t.Errorf("the tasks line is %q", row)
	}
	if row := issueLine(v, "landing"); !strings.Contains(row, "waits for its pull request") && !strings.Contains(row, "waits to land") {
		t.Errorf("the landing line is %q", row)
	}
	for _, bad := range []string{"suite", "1 Brief", "Run"} {
		if strings.Contains(v, bad) {
			t.Errorf("the multi-task's screen has %q:\n%s", bad, v)
		}
	}
	if !strings.Contains(v, "c issue") {
		t.Errorf("the multi-task's hints lack c:\n%s", v)
	}

	// c goes up to the issue's plan, esc comes back.
	g.press("c")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" {
		t.Fatalf("c opened %s of %q, want the issue's plan", g.m.Screen(), g.m.top().chart)
	}
	g.press("esc")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "SHOP-7" {
		t.Errorf("esc went to %s of %q", g.m.Screen(), g.m.top().ticket)
	}
	// A task opens from the multi-task's screen, and c from it goes up to the
	// multi-task, whose c goes up to the issue.
	onRow(t, g, "SHOP-6")
	g.press("enter", "c")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "SHOP-7" {
		t.Fatalf("c from SHOP-6 opened %s of %q, want its multi-task", g.m.Screen(), g.m.top().ticket)
	}
	g.press("c")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" {
		t.Errorf("c from the multi-task opened %s of %q", g.m.Screen(), g.m.top().chart)
	}
}

// TestMultiTaskNestsUnderTheIssuesTasks: in the issue's plan a multi-task is a
// row of its own with its tasks one level in under it, expanded; the issue's
// count is of the tasks it lands, so a nested task is not among them; enter on
// the multi-task opens its own screen.
func TestMultiTaskNestsUnderTheIssuesTasks(t *testing.T) {
	t.Parallel()
	g := planRig(t, planSpec{multi: true, issue: &digestInfo{Kind: "issue", State: "waiting"}})
	g.m.height, g.m.width = 40, 100
	v := stripANSI(g.view())
	mt, t5, t6 := lineWith(v, "SHOP-7  "), lineWith(v, "SHOP-5  "), lineWith(v, "SHOP-6  ")
	if mt == "" || t5 == "" || t6 == "" {
		t.Fatalf("the plan lacks the multi-task or its tasks:\n%s", v)
	}
	// Columns are counted in cells, not bytes: the cursor and the icons are wider.
	col := func(line, sub string) int { return len([]rune(line[:strings.Index(line, sub)])) }
	if col(mt, "SHOP-") >= col(t5, "SHOP-") || col(t5, "SHOP-") != col(t6, "SHOP-") || strings.Index(v, "SHOP-7  ") > strings.Index(v, "SHOP-5  ") || strings.Index(v, "SHOP-5  ") > strings.Index(v, "SHOP-6  ") {
		t.Errorf("the multi-task's tasks are not one level in under it, in order:\n%s", v)
	}
	if !strings.Contains(mt, "multi-task · 1 of 2 landed · PR #7 open") {
		t.Errorf("the multi-task's row: %q", mt)
	}
	// Five top-level rows: SHOP-1 to SHOP-4 landed, the multi-task has not.
	if !strings.Contains(v, "4 of 5 landed") {
		t.Errorf("the count is not of the issue's own tasks:\n%s", v)
	}
	if row := issueLine(v, "PR"); !strings.Contains(row, "waits for 1 task") {
		t.Errorf("the issue's pull request waits for the multi-task: %q", row)
	}
	// A multi-task whose pull request landed counts as landed, with where; one
	// whose tasks all landed and has no pull request yet is ready; one closed is
	// closed.
	kids := &digestInfo{Kind: "multi-task", Parent: "SHOP-49", State: "landed", Children: []digestChild{{ID: "SHOP-5"}, {ID: "SHOP-6"}},
		PRs: []digestPR{{Repo: "demo", Number: 7, State: "merged", Target: planBranch}}}
	done := planRig(t, planSpec{multi: true, kidsLanded: true, multiDigest: kids})
	done.m.height, done.m.width = 40, 100
	dv := stripANSI(done.view())
	if row := lineWith(dv, "SHOP-7  "); !strings.Contains(row, "✓ SHOP-7") || !strings.Contains(row, "multi-task · landed · into the issue's branch at 09:25:00") {
		t.Errorf("a landed multi-task reads %q", row)
	}
	if !strings.Contains(dv, "5 of 5 landed") {
		t.Errorf("a landed multi-task is not counted landed:\n%s", dv)
	}
	ready := &digestInfo{Kind: "multi-task", Parent: "SHOP-49", State: "ready", Children: []digestChild{{ID: "SHOP-5"}, {ID: "SHOP-6"}}}
	r := planRig(t, planSpec{multi: true, kidsLanded: true, multiDigest: ready})
	r.m.height, r.m.width = 40, 100
	if row := lineWith(stripANSI(r.view()), "SHOP-7  "); !strings.Contains(row, "◆ SHOP-7") || !strings.Contains(row, "multi-task · 2 of 2 landed") {
		t.Errorf("a multi-task whose tasks all landed reads %q", row)
	}
	shut := &digestInfo{Kind: "multi-task", Parent: "SHOP-49", State: "waiting", Closed: "not planned", Children: []digestChild{{ID: "SHOP-5"}, {ID: "SHOP-6"}}}
	c := planRig(t, planSpec{multi: true, multiDigest: shut})
	c.m.height, c.m.width = 40, 100
	if row := lineWith(stripANSI(c.view()), "SHOP-7  "); !strings.Contains(row, "⊖ SHOP-7") || !strings.Contains(row, "multi-task · closed · not planned") {
		t.Errorf("a closed multi-task reads %q", row)
	}

	onRow(t, g, "SHOP-7")
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "SHOP-7" {
		t.Errorf("enter on the multi-task opened %s of %q", g.m.Screen(), g.m.top().ticket)
	}
	// A task nested under it opens like any other.
	g.press("esc")
	onRow(t, g, "SHOP-6")
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "SHOP-6" {
		t.Errorf("enter on a nested task opened %s of %q", g.m.Screen(), g.m.top().ticket)
	}
}

// TestASharedTaskShowsUnderEachIssueLandingPerIssue: a task two issues list
// shows in the plan of each, and each reads its own pull request and its own
// landing: landed into the first issue's branch, waiting to land into the
// second's, and neither names the other's.
func TestASharedTaskShowsUnderEachIssueLandingPerIssue(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{second: true})
	g.m.height, g.m.width = 40, 110
	g.press("end", "enter") // the plan of SHOP-50, the second chart
	if g.m.top().chart != "second" {
		t.Fatalf("opened %q, want second", g.m.top().chart)
	}
	second := stripANSI(g.view())
	row := lineWith(second, "SHOP-1  ")
	if !strings.Contains(row, `waits to land · say "Land SHOP-1"`) || strings.Contains(row, "landed ·") {
		t.Errorf("SHOP-50's plan reads SHOP-1 as %q, want it waiting to land", row)
	}
	if !strings.Contains(row, "PR #4 open") || strings.Contains(row, "#3") {
		t.Errorf("SHOP-50's plan names the wrong pull request: %q", row)
	}
	if lineWith(second, "SHOP-2  ") != "" {
		t.Errorf("SHOP-50's plan lists a task it does not have:\n%s", second)
	}
	if row := issueLine(second, "tasks"); !strings.Contains(row, "0 of 1 landed") {
		t.Errorf("SHOP-50's tasks line: %q", row)
	}

	g.press("esc", "home")
	onRow(t, g, "SHOP-49")
	g.press("enter")
	first := stripANSI(g.view())
	row = lineWith(first, "SHOP-1  ")
	if !strings.Contains(row, "landed · into the issue's branch at 09:10:00") || strings.Contains(row, "#4") || strings.Contains(row, "waits to land") {
		t.Errorf("SHOP-49's plan reads SHOP-1 as %q, want it landed there", row)
	}
	if row := issueLine(first, "tasks"); !strings.Contains(row, "4 of 4 landed") {
		t.Errorf("SHOP-49's tasks line: %q", row)
	}
}
