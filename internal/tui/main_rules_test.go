package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// builtGroups are the groups of the list as the model has them built, with no
// help from the test: what a message moved is what shows.
func builtGroups(m *Model) map[string][]string {
	out := map[string][]string{}
	q := m.mainBuilt().q
	for g := mainGroup(0); g < groupCount; g++ {
		for _, r := range q.byGroup[g] {
			out[g.name()] = append(out[g.name()], r.id)
		}
	}
	return out
}

// groupOf is the group the row of id is in.
func groupOf(m *Model, id string) string {
	for name, ids := range groupRows(m) {
		if contains(ids, id) {
			return name
		}
	}
	return ""
}

// TestMainABlockedTicketWithNoSessionIsWaitingNotRunning: a ticket whose brief is
// being drafted by no one, and which a blocker holds, waits; the brief being
// drafted is no work begun, so only a session at work makes the ticket running.
func TestMainABlockedTicketWithNoSessionIsWaitingNotRunning(t *testing.T) {
	t.Parallel()
	drafting := task("brief", true, "current|being drafted", pendingAfter[0], pendingAfter[1], pendingAfter[2], "pending|after the publish")
	blocked := rosterRow{ID: "B-1", Title: "B-1", State: "blocked", Blockers: []chartWait{{Ticket: "A-1", Kind: "merged"}}, Digest: drafting}
	m := queueModel(rosterRow{ID: "A-1", Title: "A-1", State: "ready", StartOrder: 1, Digest: task("build", true, "done|a", "current|ready to build", pendingAfter[1], pendingAfter[2], "pending|after the publish")}, blocked)
	if got := groupOf(m, "B-1"); got != "waiting" {
		t.Errorf("a blocked ticket whose brief is being drafted is in %q, want waiting", got)
	}
	// With a session at work on it, it is running.
	m.w.tickets["B-1"].ledger = append(m.w.tickets["B-1"].ledger, ll(at(59, 0), "B-1", journal.EventTool, with("s-live", "", "", "", "", "")))
	if got := groupOf(m, "B-1"); got != "running" {
		t.Errorf("a ticket a session works on is in %q, want running", got)
	}
}

// TestMainReadyStepsAreReadyNotRunning: a ticket whose build can start, its slices
// all queued, is ready, as every ready-to-build ticket of a store is drawn.
func TestMainReadyStepsAreReadyNotRunning(t *testing.T) {
	t.Parallel()
	d := task("build", true, "done|approved by Ada", "current|ready to build", pendingAfter[1], pendingAfter[2], "pending|after the publish")
	d.Slices = &digestSlices{Total: 2, Queued: 2}
	m := queueModel(rosterRow{ID: "A-1", Title: "A-1", State: "ready", StartOrder: 1, Digest: d})
	if got := groupOf(m, "A-1"); got != "ready" {
		t.Errorf("a ticket ready to build is in %q, want ready", got)
	}
}

// TestMainContainerRules: an issue or a multi-task no issue lists is grouped as a
// container, from its own standing and its tasks'.
func TestMainContainerRules(t *testing.T) {
	t.Parallel()
	building := task("build", false, "done|approved by Ada", "current|1 of 2 green", pendingAfter[1], pendingAfter[2], "pending|after the publish")
	building.Parent, building.Slices = "M-1", &digestSlices{Total: 2, Green: 1, Building: 1}
	multi := &digestInfo{Kind: "multi-task", State: "waiting", Children: []digestChild{{ID: "M-2"}}}
	m := queueModel(
		rosterRow{ID: "M-1", Title: "M-1", State: "multi-task", Digest: multi},
		rosterRow{ID: "M-2", Title: "M-2", State: "active", Digest: building},
		rosterRow{ID: "I-1", Title: "I-1", State: "issue", Digest: &digestInfo{Kind: "issue", State: "waiting", Closed: "not planned"}},
		rosterRow{ID: "I-2", Title: "I-2", State: "issue", Digest: &digestInfo{Kind: "issue", State: "waiting"}},
		rosterRow{ID: "I-3", Title: "I-3", State: "issue", Digest: &digestInfo{Kind: "issue", State: "waiting"}},
	)
	// A question of the issue's own, and a session at work on another.
	m.w.apply([]events.Envelope{jl(at(5, 0), "I-2", "a", journal.EventQuestion, with("s-1", "", "", "Which one?", "q-1", "agent"))})
	m.w.tickets["I-3"].ledger = append(m.w.tickets["I-3"].ledger, ll(at(59, 0), "I-3", journal.EventTool, with("s-live", "", "", "", "", "")))
	for id, want := range map[string]string{"M-1": "running", "I-1": "done", "I-2": "needs you", "I-3": "running"} {
		if got := groupOf(m, id); got != want {
			t.Errorf("%s is in %q, want %q", id, got, want)
		}
	}
	// The multi-task's task is under it, not a row of its own.
	for _, r := range m.mainBuilt().q.rows {
		if r.id == "M-2" {
			t.Errorf("a task a multi-task draws is a row of its own")
		}
		if r.id == "M-1" && (len(r.kids) != 1 || r.kids[0].id != "M-2") {
			t.Errorf("the multi-task's rows under it are %v", r.kids)
		}
	}
}

// TestMainAMultiTasksLandingStopIsNeedsYou: a multi-task no issue lists, whose own
// pull request is published and waits for the person's word, is in needs you in
// the waits-to-land icon, whatever its tasks say; landed it is done.
func TestMainAMultiTasksLandingStopIsNeedsYou(t *testing.T) {
	t.Parallel()
	open := []digestPR{{Repo: "demo", Number: 7, State: "open", Target: "main"}}
	m := queueModel(
		rosterRow{ID: "M-1", Title: "M-1", State: "multi-task", Digest: &digestInfo{Kind: "multi-task", State: "published", PRs: open, Children: []digestChild{{ID: "M-2"}}}},
		rosterRow{ID: "M-2", Title: "M-2", State: "new", Digest: task("brief", true, "current|being drafted", pendingAfter[0], pendingAfter[1], pendingAfter[2], "pending|after the publish")},
		rosterRow{ID: "M-3", Title: "M-3", State: "multi-task", Digest: &digestInfo{Kind: "multi-task", State: "landed", Children: []digestChild{{ID: "M-4"}}}},
		rosterRow{ID: "M-4", Title: "M-4", State: "new", Digest: task("brief", true, "current|being drafted", pendingAfter[0], pendingAfter[1], pendingAfter[2], "pending|after the publish")},
	)
	if got := groupOf(m, "M-1"); got != "needs you" {
		t.Errorf("a multi-task at its landing stop is in %q, want needs you", got)
	}
	if got := groupOf(m, "M-3"); got != "done" {
		t.Errorf("a landed multi-task is in %q, want done", got)
	}
	m.nav[0].open = map[string]bool{"done": true, "not started": true}
	m.w.gen++
	if row := lineWith(stripANSI(pagesText(m.mainPage())), "M-1 "); !strings.Contains(row, "◇ M-1") || !strings.Contains(row, `say "Land M-1"`) {
		t.Errorf("the multi-task's row at its landing stop: %q", row)
	}
}

// TestMainTheCursorOnATaskGoesToTheFoldThatHidesItsIssue: the cursor on a task of
// an issue that lands, and so folds into done with its tasks, is on the fold row.
func TestMainTheCursorOnATaskGoesToTheFoldThatHidesItsIssue(t *testing.T) {
	t.Parallel()
	ready := func(order int, id string) rosterRow {
		return rosterRow{ID: id, Title: id, State: "ready", StartOrder: order}
	}
	open := []digestPR{{Repo: "demo", Number: 7, State: "open", Target: "main"}}
	issue := func(state string, prs []digestPR) rosterRow {
		return rosterRow{ID: "I-1", Title: "I-1", State: "issue", Digest: &digestInfo{Kind: "issue", State: state, PRs: prs, Children: []digestChild{{ID: "K-1"}, {ID: "K-2"}}}}
	}
	landed := task("land", false, "done|a", "done|b", "done|c", "done|d", "done|landed")
	m := queueModel(issue("published", open), rosterRow{ID: "K-1", Title: "K-1", State: "done", Digest: landed}, rosterRow{ID: "K-2", Title: "K-2", State: "done", Digest: landed}, ready(1, "A-1"), ready(2, "A-2"), ready(3, "A-3"))
	m.w.setCharts([]chartListing{{Name: "plan", Title: "Plan", Tickets: []string{"K-1", "K-2"}, Issue: "I-1"}})
	m.View()
	m.move(1)
	if ref := m.selected(); ref == nil || ref.id != "K-1" || ref.container != "I-1" {
		t.Fatalf("the cursor is on %+v, want K-1 under I-1", ref)
	}
	m.w.setRoster([]rosterRow{issue("landed", nil), {ID: "K-1", Title: "K-1", State: "done", Digest: landed}, {ID: "K-2", Title: "K-2", State: "done", Digest: landed}, ready(1, "A-1"), ready(2, "A-2"), ready(3, "A-3")})
	m.View()
	if ref := m.selected(); ref == nil || ref.kind != rowFold || ref.id != "done" {
		t.Errorf("the cursor is on %+v, want the done fold row that holds the issue and its tasks", ref)
	}
}

// TestMainSwitchPutsTheCursorOnTheFoldThatHidesItsRow: s to a view that folds the
// cursor's row away puts the cursor on the fold row that hides it, not on the
// row now at its old number.
func TestMainSwitchPutsTheCursorOnTheFoldThatHidesItsRow(t *testing.T) {
	t.Parallel()
	closed := task("brief", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
	closed.Closed = "not planned"
	ready := func(order int, id string) rosterRow {
		return rosterRow{ID: id, Title: id, State: "ready", StartOrder: order}
	}
	m := queueModel(ready(1, "A-1"), ready(2, "A-2"), rosterRow{ID: "A-9", Title: "A-9", State: "closed", Digest: closed})
	m.w.setCharts([]chartListing{{Name: "plan", Title: "Plan", Tickets: []string{"A-9"}}})
	m.View()
	m.switchView()
	for i, r := range m.mainRefs() {
		if r.id == "A-9" {
			m.top().sel = i
		}
	}
	m.noteCursor()
	if ref := m.selected(); ref == nil || ref.id != "A-9" {
		t.Fatalf("by chart the cursor is on %+v, want A-9", ref)
	}
	m.switchView()
	m.View()
	if ref := m.selected(); ref == nil || ref.kind != rowFold || ref.id != "done" {
		t.Errorf("by state the cursor is on %+v, want the done fold row that hides A-9", ref)
	}
}

// TestMainADoneTicketWithAnOldBlockerIsDone: a blocker that was met, or an after
// edge, holds a ticket in waiting only while it has a move left: one that landed
// with nothing to merge is done, one that waits to land behind another is not.
func TestMainADoneTicketWithAnOldBlockerIsDone(t *testing.T) {
	t.Parallel()
	nothing := task("land", false, "done|a", "done|b", "done|c", "done|d", "done|none · no pull request to merge")
	behind := task("land", false, "done|a", "done|b", "done|c", "done|pull request opened", "current|waits for a merge")
	behind.LandFirst = []string{"A-1"}
	blocker := []chartWait{{Ticket: "A-1", Kind: "merged"}}
	m := queueModel(
		rosterRow{ID: "A-1", Title: "A-1", State: "done", Digest: nothing},
		rosterRow{ID: "A-2", Title: "A-2", State: "done", Blockers: blocker, Digest: nothing},
		rosterRow{ID: "A-3", Title: "A-3", State: "done", Blockers: blocker, Digest: behind},
	)
	for id, want := range map[string]string{"A-1": "done", "A-2": "done", "A-3": "waiting"} {
		if got := groupOf(m, id); got != want {
			t.Errorf("%s is in %q, want %q", id, got, want)
		}
	}
}

// colOf is the screen column text starts at in line, -1 when it is not in it.
func colOf(line, text string) int {
	i := strings.Index(line, text)
	if i < 0 {
		return -1
	}
	return width(line[:i])
}

// TestMainDoneRowsKeepTheirIcons: a landed issue in the open done fold is drawn in
// the landed icon, and the tasks under an issue start their steps in its column.
func TestMainDoneRowsKeepTheirIcons(t *testing.T) {
	t.Parallel()
	_, text := mainOf(t, planSpec{})
	if row := lineWith(text, "SHOP-49 "); !strings.Contains(row, "✓ SHOP-49") {
		t.Errorf("the landed issue's row is not drawn landed: %q", row)
	}
	g, text := mainOf(t, planSpec{issue: waitingIssue})
	_ = g
	issue, task := lineWith(text, "Review rounds"), lineWith(text, "Task SHOP-2")
	if colOf(issue, "◐ ○") != colOf(task, "✓ ✓") {
		t.Errorf("the steps of a task do not start in the column of its issue's:\n%s", text)
	}
}

// TestMainASharedTaskReadsAsEachPlanReadsIt: a task two issues list, under the
// issue whose branch is known, reads as that issue's plan reads it.
func TestMainASharedTaskReadsAndGroupsByEachIssuesBranch(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{issue: waitingIssue, second: true})
	g.m.height, g.m.width = 40, 110
	for id, branch := range map[string]string{"SHOP-49": planBranch, "SHOP-50": "502-another"} {
		d := *g.m.w.tickets[id].digest
		d.Branch = branch
		g.m.w.tickets[id].digest = &d
	}
	// As `jig next` prints a task that landed into one issue with its pull request
	// into the other still open: its own land phase is current.
	shared := *g.m.w.tickets["SHOP-1"].digest
	shared.Phases = append([]digestPhase(nil), shared.Phases...)
	shared.Phases[4] = digestPhase{Name: "land", State: "current", Text: "waits for a merge"}
	g.m.w.tickets["SHOP-1"].digest = &shared
	g.m.w.gen++
	var rows []queueRow
	for _, r := range g.m.mainBuilt().q.rows {
		for _, k := range r.kids {
			if k.id == "SHOP-1" {
				rows = append(rows, k)
			}
		}
	}
	if len(rows) != 2 {
		t.Fatalf("the shared task is under %d issues", len(rows))
	}
	under49 := stripANSI(g.m.queueLine(g.m.mainBuilt().q, rows[0]))
	under50 := stripANSI(g.m.queueLine(g.m.mainBuilt().q, rows[1]))
	// Each issue is grouped by its task as it stands there: the second issue's
	// task waits for the person's word, the first's landed.
	if got := groupOf(g.m, "SHOP-50"); got != "needs you" {
		t.Errorf("the second issue is in %q, want needs you", got)
	}
	if got := groupOf(g.m, "SHOP-49"); got == "needs you" {
		t.Errorf("the first issue is in needs you with every task landed there")
	}
	g.openChart("second")
	plan := stripANSI(g.view())
	if want := lineWith(plan, "SHOP-1 "); !strings.Contains(want, "waits to land") || !strings.Contains(under50, "waits to land") {
		t.Errorf("under the second issue the task reads %q, its plan reads %q", under50, want)
	}
	if !strings.Contains(under49, "landed") || strings.Contains(under49, "waits to land") {
		t.Errorf("under the first issue the task reads %q", under49)
	}
}

// TestMainAChartsRowFollowsItsVerb: a chart that plans no issue, whose verb says
// every ticket is published, is in done, and one quiet chart reads "1 chart" in
// the header; the verb's message moves the row on the next frame.
func TestMainAChartsRowFollowsItsVerb(t *testing.T) {
	t.Parallel()
	m := queueModel(rosterRow{ID: "A-1", Title: "A-1", State: "ready", StartOrder: 1})
	m.w.setCharts([]chartListing{{Name: "plain", Title: "A chart", Tickets: []string{"A-1"}}})
	m.View()
	if got := builtGroups(m); !reflect.DeepEqual(got["charts"], []string{"plain"}) {
		t.Fatalf("a quiet chart is in %v", got)
	}
	if head := m.header(100)[0]; !strings.Contains(head, "1 chart") || strings.Contains(head, "1 charts") {
		t.Errorf("the header counts one chart as %q", head)
	}
	m.w.setCharts([]chartListing{{Name: "plain", Title: "A chart", Tickets: []string{"A-1"}}, {Name: "two", Title: "Another", Tickets: []string{"A-1"}}})
	if head := m.header(100)[0]; !strings.Contains(head, "2 charts") {
		t.Errorf("the header counts two charts as %q", head)
	}
	m.Update(chartMsg{name: "plain", info: &chartInfo{Name: "plain", Code: phase.CodeChartDone}})
	m.View()
	if got := builtGroups(m); !reflect.DeepEqual(got["done"], []string{"plain"}) {
		t.Errorf("a chart whose verb says done is in %v, want done", got)
	}
}

// TestMainTheCursorGoesToTheFoldThatHidesItsRow: a row the roster moved into a
// closed fold leaves the cursor on the fold row, not on the row that is now at
// its number; and the row the cursor lands on after its own left is the one it
// follows from then on.
func TestMainTheCursorGoesToTheFoldThatHidesItsRow(t *testing.T) {
	t.Parallel()
	ready := func(order int, id string) rosterRow {
		return rosterRow{ID: id, Title: id, State: "ready", StartOrder: order}
	}
	m := queueModel(ready(1, "A-1"), ready(2, "A-2"), ready(3, "A-3"))
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-1" {
		t.Fatalf("the cursor is on %+v, want A-1", ref)
	}
	m.w.setRoster([]rosterRow{{ID: "A-1", Title: "A-1", State: "new"}, ready(2, "A-2"), ready(3, "A-3")})
	m.View()
	if ref := m.selected(); ref == nil || ref.kind != rowFold || ref.id != "not started" {
		t.Errorf("the cursor is on %+v, want the not started fold row", ref)
	}

	// After its row left, the cursor follows the row it landed on.
	m = queueModel(ready(1, "A-1"), ready(2, "A-2"), ready(3, "A-3"), ready(4, "A-4"))
	m.View()
	m.move(2)
	m.dropTicket("A-3")
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-4" {
		t.Fatalf("the cursor is on %+v after its row left, want the row at its place, A-4", ref)
	}
	m.w.setRoster([]rosterRow{ready(2, "A-1"), ready(3, "A-2"), ready(1, "A-4")})
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-4" {
		t.Errorf("the cursor is on %+v after a re-sort, want it on A-4", ref)
	}
}
