package mirror

import (
	"testing"

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
