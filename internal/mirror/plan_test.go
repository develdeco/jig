package mirror

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func commentsOn(c *fakeClient) []string {
	var out []string
	for _, cm := range c.comments {
		out = append(out, cm.Body)
	}
	return out
}

// TestMirrorPlanCommentBothCriticalPaths: with the setting off the comment
// names the two tasks as descriptions; with it on, as links, once; a second
// sync posts nothing; a changed title posts a second comment.
func TestMirrorPlanCommentBothCriticalPaths(t *testing.T) {
	w := newTasksWorld(t)
	_, client := w.sync(false)
	if len(client.comments) != 1 {
		t.Fatalf("comments = %q, want one", commentsOn(client))
	}
	off := client.comments[0].Body
	for _, want := range []string{
		"- DEMO-2 First task: Build the first part.\n",
		"- DEMO-3 Second task: Build the second part.\n",
		"(`charts/plan/`)",
	} {
		if !strings.Contains(off, want) {
			t.Errorf("setting-off comment = %q, want it to contain %q", off, want)
		}
	}
	if strings.Contains(off, "#") {
		t.Errorf("setting-off comment = %q, want no issue links", off)
	}
	if rec := w.record("DEMO-1"); rec.Synced == nil || rec.Synced.PlanSHA256 != bodyHash(off) {
		t.Errorf("record = %+v, want synced.plan_sha256 = the posted comment's hash", rec.Synced)
	}

	// The same sync, again, posts nothing.
	w.sync(false)
	if len(client.comments) != 1 {
		t.Fatalf("a repeat sync posted: %q", commentsOn(client))
	}

	// The setting on: each task has an issue now, so the lines are links.
	report, _ := w.sync(true)
	if len(client.comments) != 2 {
		t.Fatalf("comments = %q, want a second once the tasks have issues", commentsOn(client))
	}
	on := client.comments[1].Body
	want2 := fmt.Sprintf("- #%d First task\n", numberOf(report, "DEMO-2"))
	if !strings.Contains(on, want2) || !strings.Contains(on, fmt.Sprintf("- #%d Second task\n", numberOf(report, "DEMO-3"))) {
		t.Errorf("setting-on comment = %q, want links to the tasks' issues", on)
	}
	w.sync(true)
	if len(client.comments) != 2 {
		t.Fatalf("a repeat sync posted: %q", commentsOn(client))
	}

	// A title changes what the comment shows.
	path := w.st.TicketFilePath("DEMO-2")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "First task", "Renamed task", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	w.sync(true)
	if len(client.comments) != 3 || !strings.Contains(client.comments[2].Body, "Renamed task") {
		t.Fatalf("comments = %q, want a third naming the renamed task", commentsOn(client))
	}
	// A changed map decision does not post.
	mapPath := w.st.ChartMapFile("plan")
	if err := os.WriteFile(mapPath, []byte("# Chart: plan\n\nnew words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.sync(true)
	if len(client.comments) != 3 {
		t.Fatalf("a changed map posted: %q", commentsOn(client))
	}
}

// TestMirrorPlanCommentNestsAndOrders: a multi-task's tasks are indented under
// it, a ticket with blockers ends with its waits, a ticket with no issue
// names its id or "not graduated".
func TestMirrorPlanCommentNestsAndOrders(t *testing.T) {
	w := newTasksWorld(t)
	if err := w.st.CreateTicketRecord("DEMO-5", store.Ticket{Title: "A multi-task", Kind: store.KindMultiTask}); err != nil {
		t.Fatal(err)
	}
	if err := w.st.SetTicketKind("DEMO-3", store.KindTask, "DEMO-5"); err != nil {
		t.Fatal(err)
	}
	if err := w.st.SetTicketDep("DEMO-3", "DEMO-2", "merged"); err != nil {
		t.Fatal(err)
	}
	tickets := "issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: First task\n  - id: DEMO-5\n    title: A multi-task\n    kind: multi-task\n  - id: DEMO-3\n    title: Second task\n    under: DEMO-5\n  - title: Later task\n    body: Not made yet.\n    under: \"#2\"\n    blocked_by:\n      - ref: \"#1\"\n"
	if err := os.WriteFile(w.st.ChartFile("plan"), []byte(tickets), 0o644); err != nil {
		t.Fatal(err)
	}
	_, client := w.sync(false)
	if len(client.comments) != 1 {
		t.Fatalf("comments = %q", commentsOn(client))
	}
	got := client.comments[0].Body
	want := "- DEMO-2 First task: Build the first part.\n" +
		"- DEMO-5 A multi-task\n" +
		"  - DEMO-3 Second task: Build the second part. (waits for DEMO-2)\n" +
		"  - not graduated Later task: Not made yet. (waits for DEMO-2)\n\n---\n"
	if !strings.HasPrefix(got, want) {
		t.Errorf("comment = %q, want it to start %q", got, want)
	}
}

// TestMirrorPlanCommentDryRunWritesNothing: a dry run reports the comment and
// posts and records none.
func TestMirrorPlanCommentDryRunWritesNothing(t *testing.T) {
	w := ruleWorldWithPlan(t)
	report := w.plan(planOpts{dryRun: true})
	_ = report
	rep := SyncReport{}
	items, _, err := collectItems(w.st, w.cfg.GitHub.Repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := postPlanComments(context.Background(), w.st, w.cfg, w.client, items, nil, nil, false, true, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.PlanComments) != 1 || rep.PlanComments[0] != (PlanComment{Ticket: "DEMO-1", Tickets: 1}) {
		t.Errorf("PlanComments = %+v, want the one DEMO-1 would get", rep.PlanComments)
	}
	if len(w.client.comments) != 0 {
		t.Errorf("a dry run posted %q", commentsOn(w.client))
	}
	if rec := w.record("DEMO-1"); rec.Synced != nil && rec.Synced.PlanSHA256 != "" {
		t.Errorf("a dry run wrote synced.plan_sha256 = %q", rec.Synced.PlanSHA256)
	}
}

// TestMirrorPlanCommentIsNotPostedOnASafetyHit: a comment that trips the
// publish-safety scan is a Skipped item and is not posted or recorded.
func TestMirrorPlanCommentIsNotPostedOnASafetyHit(t *testing.T) {
	w := ruleWorldWithPlan(t)
	w.plan(planOpts{})
	items, _, err := collectItems(w.st, w.cfg.GitHub.Repo, false)
	if err != nil {
		t.Fatal(err)
	}
	rep := SyncReport{}
	if err := postPlanComments(context.Background(), w.st, w.cfg, w.client, items, nil, []string{"first task"}, true, false, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0].What, "DEMO-1") || len(rep.PlanComments) != 0 || len(w.client.comments) != 0 {
		t.Errorf("report = %+v, comments = %q, want one Skipped item and no post", rep, commentsOn(w.client))
	}
}

// ruleWorldWithPlan is a rule world with an issue ticket that has an issue and
// a chart of one task.
func ruleWorldWithPlan(t *testing.T) *ruleWorld {
	t.Helper()
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "The need", Kind: store.KindIssue})
	w.ticket("DEMO-2", store.Ticket{Title: "First task", Body: "Build it."})
	w.chart("the plan\n", "DEMO-2")
	data, err := os.ReadFile(w.st.ChartFile("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.st.ChartFile("demo"), append([]byte("issue: DEMO-1\n"), data...), 0o644); err != nil {
		t.Fatal(err)
	}
	w.adopt("DEMO-1", 7)
	w.client.issues = map[string]*fakeIssueState{"NODE_DEMO-1": {Title: "The need", Open: true}}
	return w
}
