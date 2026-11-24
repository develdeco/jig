package tui

import (
	"github.com/develdeco/jig/internal/journal"
	"reflect"
	"strings"
	"testing"
)

// queueModel is a model with a runner and the roster set to rows, which draws
// the project's list.
func queueModel(rows ...rosterRow) *Model {
	m := bareMain(true)
	m.dropTicket("JIG-1")
	m.w.setRoster(rows)
	return m
}

// groupRows are the ids of each group of the list, by group name.
func groupRows(m *Model) map[string][]string {
	out := map[string][]string{}
	q := m.queue()
	for g := mainGroup(0); g < groupCount; g++ {
		for _, r := range q.byGroup[g] {
			out[g.name()] = append(out[g.name()], r.id)
		}
	}
	return out
}

var (
	pendingAfter = []string{"pending|after the brief", "pending|after the build", "pending|after a clean review", "pending|after the publish"}
	prOpen       = digestPR{Repo: "demo", Number: 7, URL: "https://example.test/acme/demo/pull/7", State: "open", Target: "main"}
)

// fresh is a ticket whose brief is being drafted: nothing began, and the roster
// says it is new.
func fresh(id string) rosterRow {
	return rosterRow{ID: id, Title: id, State: "new", Digest: task("brief", true, "current|being drafted", pendingAfter[0], pendingAfter[1], pendingAfter[2], "pending|after the publish")}
}

// TestMainGroupsAreTheQueue: each state of a ticket lands in one group, the
// groups are drawn in the order of what the person does next with their counts,
// a group with no rows is not drawn, and the header counts them.
func TestMainGroupsAreTheQueue(t *testing.T) {
	t.Parallel()
	approval := task("brief", false, "current|drafted · waiting for your approval", pendingAfter[0], pendingAfter[1], pendingAfter[2], "pending|after the publish")
	approval.Phases[0].NeedsYou = true
	building := task("build", false, "done|approved by Ada", "current|1 of 2 green · slice s2 is being built", pendingAfter[1], pendingAfter[2], "pending|after the publish")
	readyBuild := task("build", true, "done|approved by Ada", "current|ready to build", pendingAfter[1], pendingAfter[2], "pending|after the publish")
	owes := task("review", false, "done|approved by Ada", "done|2 slices green", "current|ready to review", pendingAfter[2], "pending|after the publish")
	owes.Behind = true
	published := func(land string) *digestInfo {
		d := task("land", false, "done|approved by Ada", "done|1 slice green", "done|round 1 clean", "done|pull request opened", land)
		d.PRs = []digestPR{prOpen}
		return d
	}
	landFirst := published("current|waits for a merge")
	landFirst.LandFirst = []string{"JIG-3"}
	closed := task("brief", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
	closed.Closed = "not planned"

	m := queueModel(
		rosterRow{ID: "A-1", Title: "waits for the person", State: "active", Digest: approval},
		rosterRow{ID: "A-2", Title: "being built", State: "active", Digest: building},
		rosterRow{ID: "A-3", Title: "ready", State: "ready", Digest: readyBuild, StartOrder: 1},
		rosterRow{ID: "A-4", Title: "blocked", State: "blocked", Blockers: []chartWait{{Ticket: "A-3", Kind: "merged"}}, Digest: readyBuild},
		rosterRow{ID: "A-5", Title: "owes a merge", State: "active", Digest: owes},
		rosterRow{ID: "A-6", Title: "lands after another", State: "done", Digest: landFirst},
		rosterRow{ID: "A-7", Title: "landing waits", State: "done", Digest: published("current|waits for a merge")},
		fresh("A-8"),
		rosterRow{ID: "A-9", Title: "closed", State: "closed", Digest: closed},
		rosterRow{ID: "A-10", Title: "landed", State: "done", Digest: published("done|landed")},
	)
	m.w.setCharts([]chartListing{{Name: "plan", Title: "A chart", Tickets: []string{"A-1"}}})
	// A-2's session is at work.
	m.w.tickets["A-2"].ledger = append(m.w.tickets["A-2"].ledger, ll(at(59, 0), "A-2", journal.EventTool, with("s-live", "", "", "", "", "")))

	// The chart lists A-1, which needs the person, so the chart does too: it is
	// in the first group, not the charts group.
	want := map[string][]string{
		"needs you":   {"A-1", "A-7", "plan"},
		"running":     {"A-2"},
		"ready":       {"A-3"},
		"waiting":     {"A-4", "A-5", "A-6"},
		"not started": {"A-8"},
		"done":        {"A-9", "A-10"},
	}
	got := groupRows(m)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the groups hold %v, want %v", got, want)
	}

	text := pagesText(m.mainPage())
	var seen []string
	for _, line := range strings.Split(text, "\n") {
		for _, name := range groupNames {
			if strings.Contains(line, name+" (") {
				seen = append(seen, name)
			}
		}
	}
	if order := []string{"needs you", "running", "ready", "waiting", "not started", "done"}; !reflect.DeepEqual(seen, order) {
		t.Errorf("the groups are drawn in the order %v, want %v (a group with no rows is hidden):\n%s", seen, order, text)
	}
	if !strings.Contains(text, "needs you (3)") || !strings.Contains(text, "▸ not started (1) · enter shows them") || !strings.Contains(text, "▸ done (2) · enter shows them") {
		t.Errorf("the headings and fold rows lack their counts:\n%s", text)
	}
	if got := m.queueSummary(); got != "3 need you · 1 running · 1 ready · 3 waiting · 1 not started · 2 done" {
		t.Errorf("the header counts %q", got)
	}

	// With the chart quiet it is a row of the charts group.
	m.w.tickets["A-1"].digest = readyBuild
	m.w.tickets["A-1"].state = "ready"
	if got := groupRows(m); !reflect.DeepEqual(got["charts"], []string{"plan"}) {
		t.Errorf("a chart no ticket of which needs the person is not in the charts group: %v", got)
	}
	// A chart that plans an issue has no row: the issue stands for it.
	m.w.setCharts([]chartListing{{Name: "plan", Title: "A chart", Tickets: []string{"A-1"}, Issue: "A-2"}})
	if got := groupRows(m); len(got["charts"]) != 0 {
		t.Errorf("a chart that plans an issue has a row: %v", got["charts"])
	}
}

// TestMainOrderWithinAGroup: running by latest activity, newest first; ready in
// the order `jig next` lists them to start, the ones it does not list after, by
// id; every other group by id, with numbers in number order.
func TestMainOrderWithinAGroup(t *testing.T) {
	t.Parallel()
	ready := func(id string, order int) rosterRow {
		d := task("build", true, "done|approved by Ada", "current|ready to build", pendingAfter[1], pendingAfter[2], "pending|after the publish")
		return rosterRow{ID: id, Title: id, State: "ready", Digest: d, StartOrder: order}
	}
	building := task("build", false, "done|approved by Ada", "current|1 of 2 green", pendingAfter[1], pendingAfter[2], "pending|after the publish")
	building.Slices = &digestSlices{Total: 2, Green: 1, Building: 1}
	m := queueModel(
		ready("T-10", 2), ready("T-9", 0), ready("T-22", 1), ready("T-23", 0), ready("T-100", 0),
		fresh("N-10"), fresh("N-9"), fresh("N-100"),
		rosterRow{ID: "R-1", Title: "R-1", State: "active", Digest: building},
		rosterRow{ID: "R-2", Title: "R-2", State: "active", Digest: building},
		rosterRow{ID: "R-3", Title: "R-3", State: "active", Digest: building},
	)
	// R-2 wrote last, then R-3; R-1 wrote nothing.
	m.w.tickets["R-2"].lines = append(m.w.tickets["R-2"].lines, jl(at(50, 0), "R-2", "", "claim"))
	m.w.tickets["R-3"].lines = append(m.w.tickets["R-3"].lines, jl(at(40, 0), "R-3", "", "claim"))
	got := groupRows(m)
	if want := []string{"R-2", "R-3", "R-1"}; !reflect.DeepEqual(got["running"], want) {
		t.Errorf("running reads %v, want newest activity first %v", got["running"], want)
	}
	if want := []string{"T-22", "T-10", "T-9", "T-23", "T-100"}; !reflect.DeepEqual(got["ready"], want) {
		t.Errorf("ready reads %v, want the start order, then the rest by number %v", got["ready"], want)
	}
	if want := []string{"N-9", "N-10", "N-100"}; !reflect.DeepEqual(got["not started"], want) {
		t.Errorf("not started reads %v, want numbers in number order %v", got["not started"], want)
	}
}

// TestMainFoldsOpenInPlace: not started and done are one fold row each; enter on
// it shows the rows under it and enter again folds them back, and the other rows
// stay where they were.
func TestMainFoldsOpenInPlace(t *testing.T) {
	t.Parallel()
	closed := task("brief", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
	closed.Closed = "not planned"
	m := queueModel(fresh("N-1"), fresh("N-2"), rosterRow{ID: "D-1", Title: "D-1", State: "closed", Digest: closed})
	m.View()
	before := pagesText(m.mainPage())
	for _, id := range []string{"N-1", "N-2", "D-1"} {
		if strings.Contains(before, id) {
			t.Errorf("a closed fold shows %s:\n%s", id, before)
		}
	}
	m.move(1) // the done fold row, after the not started one
	if ref := m.selected(); ref == nil || ref.kind != rowFold || ref.id != "done" {
		t.Fatalf("the cursor is on %+v, want the done fold row", ref)
	}
	m.open()
	after := pagesText(m.mainPage())
	if !strings.Contains(after, "▾ done (1) · enter folds them") || !strings.Contains(after, "D-1") || strings.Contains(after, "N-1") {
		t.Errorf("enter on the done fold shows:\n%s", after)
	}
	if ref := m.selected(); ref == nil || ref.kind != rowFold || ref.id != "done" {
		t.Errorf("the cursor left the fold row it opened: %+v", ref)
	}
	m.move(1)
	if ref := m.selected(); ref == nil || ref.id != "D-1" {
		t.Errorf("the cursor moved to %+v, want the row the fold shows", ref)
	}
	m.move(-1)
	m.open()
	if again := pagesText(m.mainPage()); again != before {
		t.Errorf("enter on an open fold does not fold it back:\n%s\nwant\n%s", again, before)
	}
}

// TestParseRosterReadsTheReadyOrder: the next list of `jig next --json` is read
// with the tickets, as each one's place in the order to start them.
func TestParseRosterReadsTheReadyOrder(t *testing.T) {
	t.Parallel()
	rows, _, err := parseRoster(`{"tickets":[{"id":"T-1","state":"ready"},{"id":"T-2","state":"ready"},{"id":"T-3","state":"active"}],"next":["T-2","T-1"]}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.ID] = r.StartOrder
	}
	if want := map[string]int{"T-1": 2, "T-2": 1, "T-3": 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("the start order is %v, want %v", got, want)
	}
}
