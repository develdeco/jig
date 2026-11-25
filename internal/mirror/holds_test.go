package mirror

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

// needNode is the node id the fake client gives the need's issue.
const needNode = "NODE_The need"

func (w *tasksWorld) close(id, reason string) {
	w.t.Helper()
	closeRuleTicket(w.t, w.st, id, reason, "example/demo#1", "")
}

// TestIssueStaysOpenUntilItLands: with Auto-close on, an issue goes from its
// last task's landing into the issue's branch, through its own pull request,
// to its landing. It is not closed before it lands, it closes once, and it is
// never reopened.
func TestIssueStaysOpenUntilItLands(t *testing.T) {
	w := newTasksWorld(t)
	_, client := w.sync(true)
	held := func(step, card string, open bool, closes int) {
		t.Helper()
		is := client.issue(needNode)
		if is.Open != open {
			t.Errorf("%s: the need's issue open = %v, want %v", step, is.Open, open)
		}
		if got := placedStatus(client, "DEMO-1"); got != card {
			t.Errorf("%s: the need's card = %q, want %q", step, got, card)
		}
		if got := client.closes(needNode); got != closes {
			t.Errorf("%s: the need's issue was closed %d times, want %d", step, got, closes)
		}
		if len(client.reopened) != 0 {
			t.Errorf("%s: reopened = %v, want nothing reopened", step, client.reopened)
		}
	}

	for _, id := range []string{"DEMO-2", "DEMO-3"} {
		url := "https://github.com/example/demo/pull/" + id
		appendLines(t, w.st, id, prLine("demo", url, "DEMO-1"), landedLine("demo", url, "DEMO-1"))
	}
	w.sync(true)
	held("both tasks landed into the issue's branch", "In progress", true, 0)

	branch, err := w.st.TicketBranch("DEMO-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/example/demo/pull/7"
	appendLines(t, w.st, "DEMO-1", prLine("demo", url, ""))
	pr := github.PullRequest{Number: 7, NodeID: "PR_7", State: "OPEN"}
	client.prs = map[string][]github.PullRequest{"example/demo@" + branch: {pr}}
	w.sync(true)
	held("the issue's pull request is open", "In review", true, 0)

	pr.State = "MERGED"
	client.prs["example/demo@"+branch] = []github.PullRequest{pr}
	appendLines(t, w.st, "DEMO-1", landedLine("demo", url, ""))
	w.sync(true)
	held("the issue landed", "Done", false, 1)
	if got := client.issue(needNode).Reason; got != "completed" {
		t.Errorf("the need's issue closed as %q, want completed", got)
	}

	again, _ := w.sync(true)
	held("a sync after the landing", "Done", false, 1)
	if len(again.Updated)+len(again.Placed) != 0 {
		t.Errorf("a sync after the landing wrote %+v, want nothing", again)
	}
}

// TestClosedIssueTakesTheStoresReason: a closed issue whose reason no longer
// matches the store gets the store's reason without a reopen, and an issue
// closed as not planned along with its only open tasks closes once, as not
// planned.
func TestClosedIssueTakesTheStoresReason(t *testing.T) {
	w := newTasksWorld(t)
	_, client := w.sync(true)

	t.Run("a reason the store no longer gives", func(t *testing.T) {
		node := "NODE_Legacy ticket"
		w.close("DEMO-4", store.CloseCovered)
		w.sync(true)
		if err := w.st.ReopenTicket("DEMO-4"); err != nil {
			t.Fatal(err)
		}
		w.close("DEMO-4", store.CloseNotPlanned)
		report, _ := w.sync(true)
		if is := client.issue(node); is.Open || is.Reason != "not planned" {
			t.Errorf("issue open = %v, closed as %q, want closed as not planned", is.Open, is.Reason)
		}
		if len(client.reopened) != 0 {
			t.Errorf("reopened = %v, want nothing reopened", client.reopened)
		}
		var fields []string
		for _, u := range report.Updated {
			if u.What == "DEMO-4" {
				fields = u.Fields
			}
		}
		if !containsString(fields, "close reason") {
			t.Errorf("DEMO-4's updated fields = %v, want the close reason", fields)
		}
	})

	t.Run("an issue closed as not planned with its tasks", func(t *testing.T) {
		for _, id := range []string{"DEMO-2", "DEMO-3"} {
			w.close(id, store.CloseNotPlanned)
		}
		w.sync(true)
		w.close("DEMO-1", store.CloseNotPlanned)
		w.sync(true)
		w.sync(true)
		is := client.issue(needNode)
		if is.Open || is.Reason != "not planned" {
			t.Errorf("the need's issue open = %v, closed as %q, want closed as not planned", is.Open, is.Reason)
		}
		if got := client.closes(needNode); got != 1 {
			t.Errorf("the need's issue was closed %d times, want once", got)
		}
	})
}

// planChart writes chart demo planning issue DEMO-1, with one entry per id; an
// empty id is an entry not graduated yet.
func (w *ruleWorld) planChart(ids ...string) {
	w.t.Helper()
	w.chart("# Chart: demo\n")
	yaml := "issue: DEMO-1\ntickets:\n"
	for _, id := range ids {
		if id == "" {
			yaml += "  - title: not graduated\n"
		} else {
			yaml += "  - id: " + id + "\n    title: " + id + "\n"
		}
	}
	if err := os.WriteFile(w.st.ChartFile("demo"), []byte(yaml), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// pull makes the host answer ticket's branch with one pull request in state.
func (w *ruleWorld) pull(ticket string, n int, state string) {
	w.t.Helper()
	branch, err := w.st.TicketBranch(ticket, "main")
	if err != nil {
		w.t.Fatal(err)
	}
	if w.client.prs == nil {
		w.client.prs = map[string][]github.PullRequest{}
	}
	w.client.prs["example/demo@"+branch] = []github.PullRequest{{Number: n, NodeID: "PR_" + ticket, State: state}}
}

// TestTheIssuesCardNeverReadsAFinishedColumnWhileItIsOpen: an open issue's
// card is In review while its own pull request is open and else what its
// chart's tickets say, Done reading In progress and Not planned Backlog; a
// closed or finished issue shows its own Status.
func TestTheIssuesCardNeverReadsAFinishedColumnWhileItIsOpen(t *testing.T) {
	building := func(w *ruleWorld, id string) {
		appendLines(w.t, w.st, id, journal.Line{Event: "adopt"})
	}
	published := func(w *ruleWorld, id string) {
		appendLines(w.t, w.st, id, prLine("demo", "https://github.com/example/demo/pull/"+id, "DEMO-1"))
	}
	landed := func(w *ruleWorld, ids ...string) {
		for _, id := range ids {
			url := "https://github.com/example/demo/pull/" + id
			appendLines(w.t, w.st, id, prLine("demo", url, "DEMO-1"), landedLine("demo", url, "DEMO-1"))
		}
	}
	cut := func(w *ruleWorld, ids ...string) {
		for _, id := range ids {
			closeRuleTicket(w.t, w.st, id, store.CloseNotPlanned, "", "")
		}
	}
	two := []string{"DEMO-2", "DEMO-3"}
	for _, tc := range []struct {
		name   string
		chart  []string // nil: no chart plans the issue
		tasks  bool     // the mirror carries the tasks
		setup  func(w *ruleWorld)
		card   string
		reason string // "" for an issue that stays open
	}{
		{"no chart", nil, false, func(w *ruleWorld) { building(w, "DEMO-1") }, "In progress", ""},
		{"no graduated ticket", []string{""}, false, func(*ruleWorld) {}, "Backlog", ""},
		{"tasks not started", two, false, func(*ruleWorld) {}, "Backlog", ""},
		{"a task building", two, false, func(w *ruleWorld) { building(w, "DEMO-2") }, "In progress", ""},
		{"a task published", two, false, func(w *ruleWorld) { published(w, "DEMO-2") }, "In progress", ""},
		{"a carried task in review", two, true, func(w *ruleWorld) { w.pull("DEMO-2", 5, "OPEN") }, "In progress", ""},
		{"every task landed into the issue's branch", two, false, func(w *ruleWorld) { landed(w, two...) }, "In progress", ""},
		{"tasks landed and cut", two, false, func(w *ruleWorld) { landed(w, "DEMO-2"); cut(w, "DEMO-3") }, "In progress", ""},
		{"every task cut", two, false, func(w *ruleWorld) { cut(w, two...) }, "Backlog", ""},
		{"its pull request open", two, false, func(w *ruleWorld) { landed(w, two...); w.pull("DEMO-1", 7, "OPEN") }, "In review", ""},
		{"its pull request open, every task cut", two, false, func(w *ruleWorld) { cut(w, two...); w.pull("DEMO-1", 7, "OPEN") }, "In review", ""},
		{"its pull request closed unmerged", two, false, func(w *ruleWorld) {
			landed(w, two...)
			published(w, "DEMO-1")
			w.pull("DEMO-1", 7, "CLOSED")
		}, "In progress", ""},
		{"its pull request merged on the host, not recorded", two, false, func(w *ruleWorld) { w.pull("DEMO-1", 7, "MERGED") }, "Done", "completed"},
		{"landed", two, false, func(w *ruleWorld) { landed(w, "DEMO-1") }, "Done", "completed"},
		{"closed as covered", two, false, func(w *ruleWorld) {
			closeRuleTicket(w.t, w.st, "DEMO-1", store.CloseCovered, "example/demo#1", "")
		}, "Done", "completed"},
		{"closed as superseded", two, false, func(w *ruleWorld) {
			closeRuleTicket(w.t, w.st, "DEMO-1", store.CloseSuperseded, "DEMO-2", "")
		}, "Not planned", "not planned"},
		{"closed as not planned with its pull request open", two, false, func(w *ruleWorld) {
			w.pull("DEMO-1", 7, "OPEN")
			cut(w, "DEMO-1")
		}, "Not planned", "not planned"},
		{"reopened", two, false, func(w *ruleWorld) {
			cut(w, "DEMO-1", "DEMO-2", "DEMO-3")
			w.plan(planOpts{})
			if err := w.st.ReopenTicket("DEMO-1"); err != nil {
				w.t.Fatal(err)
			}
		}, "Backlog", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newRuleWorld(t, true)
			w.ticket("DEMO-1", store.Ticket{Title: "The need", Kind: store.KindIssue})
			node := w.adopt("DEMO-1", 1)
			for i, id := range two {
				w.ticket(id, store.Ticket{Title: "Task " + id})
				if tc.tasks {
					w.adopt(id, i+2)
				}
			}
			if tc.chart != nil {
				w.planChart(tc.chart...)
			}
			tc.setup(w)
			w.plan(planOpts{tasks: tc.tasks})
			if got := placedStatus(w.client, "DEMO-1"); got != tc.card {
				t.Errorf("card = %q, want %q", got, tc.card)
			}
			is := w.client.issue(node)
			if is.Open != (tc.reason == "") || is.Reason != tc.reason {
				t.Errorf("issue open = %v, closed as %q; want closed as %q", is.Open, is.Reason, tc.reason)
			}
			if len(w.client.cardAhead) != 0 {
				t.Errorf("cards moved ahead of their issues: %q", w.client.cardAhead)
			}
		})
	}
}

// TestAStandaloneChartsCardFollowsItsIssue: a chart with its own issue takes
// its tickets' Status for its card, open state and close reason, read the same
// in every step of a sync: an issue it lists, a ticket with no issue yet and
// every ticket cut included.
func TestAStandaloneChartsCardFollowsItsIssue(t *testing.T) {
	world := func(t *testing.T) (*ruleWorld, string) {
		w := newRuleWorld(t, false)
		w.ticket("DEMO-1", store.Ticket{Title: "First"})
		w.ticket("DEMO-2", store.Ticket{Title: "Second", Kind: store.KindIssue})
		w.adopt("DEMO-1", 1)
		return w, w.adopt("charts/demo", 3)
	}
	check := func(t *testing.T, w *ruleWorld, node, card, reason string) {
		t.Helper()
		if got := placedStatus(w.client, "charts/demo"); got != card {
			t.Errorf("chart's card = %q, want %q", got, card)
		}
		if is := w.client.issue(node); is.Open != (reason == "") || is.Reason != reason {
			t.Errorf("chart's issue open = %v, closed as %q; want closed as %q", is.Open, is.Reason, reason)
		}
		if len(w.client.cardAhead) != 0 {
			t.Errorf("cards moved ahead of their issues: %q", w.client.cardAhead)
		}
	}

	t.Run("every ticket cut", func(t *testing.T) {
		w, node := world(t)
		w.chart("# Chart: demo\n", "DEMO-1", "DEMO-2")
		w.adopt("DEMO-2", 2)
		closeRuleTicket(t, w.st, "DEMO-1", store.CloseNotPlanned, "", "")
		closeRuleTicket(t, w.st, "DEMO-2", store.CloseNotPlanned, "", "")
		w.plan(planOpts{})
		check(t, w, node, "Not planned", "not planned")
	})

	t.Run("an issue it lists", func(t *testing.T) {
		w, node := world(t)
		w.chart("# Chart: demo\n", "DEMO-2")
		w.adopt("DEMO-2", 2)
		closeRuleTicket(t, w.st, "DEMO-2", store.CloseCovered, "example/demo#1", "")
		w.plan(planOpts{})
		check(t, w, node, "Done", "completed")
	})

	t.Run("a dry run with a ticket that has no issue yet", func(t *testing.T) {
		w, node := world(t)
		w.chart("# Chart: demo\n", "DEMO-1", "DEMO-2")
		closeRuleTicket(t, w.st, "DEMO-1", store.CloseCovered, "example/demo#1", "")
		report := w.plan(planOpts{dryRun: true})
		for _, u := range report.Updated {
			if u.What == "charts/demo" && containsString(u.Fields, "state") {
				t.Errorf("the dry run would close the chart's issue: %v", u.Fields)
			}
		}
		if !containsPlaced(report.Placed, PlacedItem{What: "charts/demo", Status: "In progress"}) {
			t.Errorf("placed = %+v, want the chart In progress", report.Placed)
		}
		if is := w.client.issue(node); !is.Open || len(w.client.placedItems) != 0 {
			t.Errorf("the dry run wrote: open = %v, placed = %+v", is.Open, w.client.placedItems)
		}
	})
}

func containsPlaced(placed []PlacedItem, want PlacedItem) bool {
	for _, p := range placed {
		if p == want {
			return true
		}
	}
	return false
}

// TestARecreatedIssueIsClosedBeforeItsCardMoves: an issue gone from GitHub is
// opened again, closed as the store holds it, and only then its card moves; a
// dry run reports the same close and card and writes nothing.
func TestARecreatedIssueIsClosedBeforeItsCardMoves(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "Built"})
	node := w.adopt("DEMO-1", 1)
	closeRuleTicket(t, w.st, "DEMO-1", store.CloseNotPlanned, "", "")
	w.client.goneNodeIDs = map[string]bool{node: true}
	report := w.plan(planOpts{dryRun: true})
	if len(report.Updated) != 1 || !containsString(report.Updated[0].Fields, "state") ||
		!containsPlaced(report.Placed, PlacedItem{What: "DEMO-1", Status: "Not planned"}) {
		t.Errorf("dry run reported %+v placed %+v, want the new issue closed and placed Not planned", report.Updated, report.Placed)
	}
	if len(w.client.calls)+len(w.client.closed)+len(w.client.placedItems) != 0 {
		t.Fatalf("the dry run wrote to GitHub")
	}

	w.client.goneNodeIDs = map[string]bool{node: true}
	w.plan(planOpts{})
	if is := w.client.issue("NODE_Built"); is.Open || is.Reason != "not planned" {
		t.Errorf("recreated issue open = %v, closed as %q", is.Open, is.Reason)
	}
	if got := placedStatus(w.client, "DEMO-1"); got != "Not planned" || len(w.client.cardAhead) != 0 {
		t.Errorf("card = %q, moved ahead: %q", got, w.client.cardAhead)
	}
}

// TestAClosedIssuesReasonIsCorrected: for an issue held closed that GitHub
// already holds closed, a reason that says otherwise than the store's is set to
// the store's, without a reopen and with no drift line, however it came to be
// closed; an agreeing one stands. A dry run reports the write and makes none.
func TestAClosedIssuesReasonIsCorrected(t *testing.T) {
	for _, tc := range []struct {
		held   string // the store's close reason
		github string // GitHub's reason, "" for none
		write  string // the reason jig sets, "" for none
	}{
		{store.CloseCovered, "completed", ""},
		{store.CloseCovered, "", ""},
		{store.CloseCovered, "not planned", "completed"},
		{store.CloseCovered, "duplicate", "completed"},
		{store.CloseNotPlanned, "completed", "not_planned"},
		{store.CloseNotPlanned, "", "not_planned"},
		{store.CloseNotPlanned, "not planned", ""},
		{store.CloseNotPlanned, "duplicate", ""},
	} {
		t.Run(tc.held+" over "+tc.github, func(t *testing.T) {
			w := newRuleWorld(t, false)
			w.ticket("DEMO-1", store.Ticket{Title: "Closed"})
			node := w.adopt("DEMO-1", 4)
			closeRuleTicket(t, w.st, "DEMO-1", tc.held, "example/demo#1", "")
			closedAs := func() {
				is := w.client.issue(node)
				is.Open, is.Reason = false, tc.github
			}
			var want []fakeReasonWrite
			if tc.write != "" {
				want = []fakeReasonWrite{{Owner: "example", Repo: "tracking", Number: 4, Reason: tc.write}}
			}
			reportsReason := func(r SyncReport) bool {
				return len(r.Updated) == 1 && containsString(r.Updated[0].Fields, "close reason")
			}

			closedAs()
			if report := w.plan(planOpts{dryRun: true}); reportsReason(report) != (tc.write != "") {
				t.Errorf("dry run updated = %+v", report.Updated)
			}
			if len(w.client.reasonWrites) != 0 {
				t.Fatalf("the dry run wrote %+v", w.client.reasonWrites)
			}

			// The second round, over a record an earlier sync wrote, is an edit
			// made on GitHub: corrected the same, with no drift line.
			for round := 1; round <= 2; round++ {
				closedAs()
				w.client.reasonWrites = nil
				report := w.plan(planOpts{})
				if !reflect.DeepEqual(w.client.reasonWrites, want) {
					t.Fatalf("round %d: reason writes = %+v, want %+v", round, w.client.reasonWrites, want)
				}
				if round == 1 && reportsReason(report) != (tc.write != "") {
					t.Errorf("updated = %+v", report.Updated)
				}
				if len(report.Drift) != 0 || len(w.client.reopened) != 0 || len(w.client.cardAhead) != 0 {
					t.Errorf("round %d: drift %+v, reopened %v, cards ahead %q", round, report.Drift, w.client.reopened, w.client.cardAhead)
				}
			}
			// The third sync finds what the second left.
			w.client.reasonWrites = nil
			if report := w.plan(planOpts{}); len(report.Updated)+len(w.client.reasonWrites) != 0 {
				t.Errorf("a sync with the reason held wrote again: %+v %+v", report.Updated, w.client.reasonWrites)
			}
		})
	}
}

// TestAReasonWriteIsSkippedAndFailsAsOtherWrites: a publish-safety hit leaves
// the item with neither its reason nor its card written, and a failed reason
// write fails the sync naming the item.
func TestAReasonWriteIsSkippedAndFailsAsOtherWrites(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "Mail " + publishSafetyTestEmail})
	node := w.adopt("DEMO-1", 1)
	closeRuleTicket(t, w.st, "DEMO-1", store.CloseNotPlanned, "", "")
	*w.client.issue(node) = fakeIssueState{Reason: "completed"}
	report := w.plan(planOpts{public: true})
	if len(report.Skipped) != 1 || len(w.client.reasonWrites)+len(w.client.placedItems) != 0 {
		t.Fatalf("skipped = %+v, reason writes %+v, placed %+v", report.Skipped, w.client.reasonWrites, w.client.placedItems)
	}

	w.client.reasonErr = errors.New("boom")
	items, _, err := collectItems(w.st, w.cfg.GitHub.Repo, false)
	if err != nil {
		t.Fatal(err)
	}
	var failed SyncReport
	_, _, _, _, err = updateIssues(context.Background(), w.st, w.cfg, w.client, items, &failed, nil, false, false)
	if err == nil || !strings.Contains(err.Error(), "DEMO-1's issue close reason") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want it to name DEMO-1's close reason", err)
	}
}
