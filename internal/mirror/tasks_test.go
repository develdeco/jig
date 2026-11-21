package mirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

// tasksWorld is a store with a model chart (an issue ticket DEMO-1 and two
// tasks DEMO-2 and DEMO-3) and a legacy chart (DEMO-4), committed and pushed.
type tasksWorld struct {
	t   *testing.T
	st  *store.Store
	cfg func(tasks bool) Deps
}

func newTasksWorld(t *testing.T) *tasksWorld {
	t.Helper()
	st, work, _ := newTestRemoteStore(t)
	addRepoToProjectYAML(t, work)
	mk := func(id string, tk store.Ticket) {
		if err := st.CreateTicketRecord(id, tk); err != nil {
			t.Fatal(err)
		}
	}
	mk("DEMO-1", store.Ticket{Title: "The need", Body: "Customers want a thing.", Kind: store.KindIssue})
	mk("DEMO-2", store.Ticket{Title: "First task", Body: "Build the first part."})
	mk("DEMO-3", store.Ticket{Title: "Second task", Body: "Build the second part."})
	mk("DEMO-4", store.Ticket{Title: "Legacy ticket"})
	writeChart := func(name, mapMD, tickets string) {
		dir := filepath.Join(st.Root, "charts", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "map.md"), []byte(mapMD), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tickets.yaml"), []byte(tickets), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeChart("plan", "# Chart: plan\n\nthe plan\n", "issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: First task\n  - id: DEMO-3\n    title: Second task\n")
	writeChart("old", "# Chart: old\n\nlegacy\n", "tickets:\n  - id: DEMO-4\n    title: Legacy ticket\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "model chart")
	runGit(t, work, "push", "origin", "main")
	w := &tasksWorld{t: t, st: st}
	base := loadCfg(t, st)
	var client *fakeClient
	w.cfg = func(tasks bool) Deps {
		cfg := base
		gh := *base.GitHub
		gh.Tasks = tasks
		cfg.GitHub = &gh
		if client == nil {
			client = &fakeClient{}
		}
		return Deps{Store: st, Cfg: cfg, Client: client}
	}
	return w
}

func (w *tasksWorld) sync(tasks bool) (SyncReport, *fakeClient) {
	w.t.Helper()
	d := w.cfg(tasks)
	report, err := Sync(d, SyncOpts{})
	if err != nil {
		w.t.Fatalf("Sync: %v", err)
	}
	return report, d.Client.(*fakeClient)
}

func createdWhats(r SyncReport) []string {
	var out []string
	for _, c := range r.Created {
		out = append(out, c.What)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMirrorTasksOffCarriesTheIssueOnly: with the setting off a model chart
// opens its issue ticket and no chart issue and no task; the legacy chart and
// its ticket are mirrored as before.
func TestMirrorTasksOffCarriesTheIssueOnly(t *testing.T) {
	w := newTasksWorld(t)
	report, client := w.sync(false)
	if want := []string{"DEMO-1", "DEMO-4", "charts/old"}; !sameStrings(createdWhats(report), want) {
		t.Fatalf("Created = %v, want %v", createdWhats(report), want)
	}
	// The issue's body is the need text and the footer, and nothing else.
	var body string
	for _, is := range client.issues {
		if is.Title == "The need" {
			body = is.Body
		}
	}
	if !strings.HasPrefix(body, "Customers want a thing.\n\n---\n") || strings.Contains(body, "## ") {
		t.Errorf("issue body = %q, want the need text then the footer", body)
	}
	// Only the legacy chart links a sub-issue.
	if len(client.subIssues) != 1 {
		t.Errorf("sub-issues = %+v, want only the legacy chart's one", client.subIssues)
	}
	if report.TasksNotCarried != 0 {
		t.Errorf("TasksNotCarried = %d, want 0 for a first sync", report.TasksNotCarried)
	}
}

// TestMirrorTasksOnCarriesEachTaskAsAFullIssue: with the setting on each task
// is an issue of its own, never a sub-issue, and the chart still has none.
func TestMirrorTasksOnCarriesEachTaskAsAFullIssue(t *testing.T) {
	w := newTasksWorld(t)
	report, client := w.sync(true)
	if want := []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4", "charts/old"}; !sameStrings(createdWhats(report), want) {
		t.Fatalf("Created = %v, want %v", createdWhats(report), want)
	}
	if len(client.subIssues) != 1 {
		t.Errorf("sub-issues = %+v, want only the legacy chart's one", client.subIssues)
	}
	for _, d := range report.Drift {
		t.Errorf("drift %+v on a first sync", d)
	}
	second, _ := w.sync(true)
	if len(second.Created)+len(second.Updated)+len(second.Drift)+len(second.LinkedParents) != 0 {
		t.Errorf("second sync did something: %+v", second)
	}
}

// TestMirrorTasksIssueStatusFollowsItsTasks: the issue ticket's board Status
// is ChartStatus over the chart's tickets, mirrored or not; its issue stays
// open until its own ticket closes, even when they are all Done.
func TestMirrorTasksIssueStatusFollowsItsTasks(t *testing.T) {
	w := newTasksWorld(t)
	_, client := w.sync(false)
	statusOf := func(what string) string {
		for i := len(client.placedItems) - 1; i >= 0; i-- {
			if client.placedItems[i].StoreID == what {
				return client.placedItems[i].Status
			}
		}
		return ""
	}
	if got := statusOf("DEMO-1"); got != "Backlog" {
		t.Fatalf("Status with no task started = %q, want Backlog", got)
	}
	for _, id := range []string{"DEMO-2", "DEMO-3"} {
		if err := w.st.CloseTicket(id, store.Closed{Reason: store.CloseCovered, By: "example/demo#1", At: "2026-01-01"}); err != nil {
			t.Fatal(err)
		}
		if id == "DEMO-2" {
			w.sync(false)
			if got := statusOf("DEMO-1"); got != "In progress" {
				t.Fatalf("Status with one task done = %q, want In progress", got)
			}
		}
	}
	w.sync(false)
	if got := statusOf("DEMO-1"); got != "Done" {
		t.Fatalf("Status with every task done = %q, want Done", got)
	}
	// The need's issue follows its own ticket, not its chart: it stays open
	// while DEMO-1 is open, and closes once DEMO-1 is closed.
	needOpen := func() bool {
		for _, is := range client.issues {
			if is.Title == "The need" {
				return is.Open
			}
		}
		t.Fatal("no issue for the need")
		return false
	}
	if !needOpen() {
		t.Errorf("the need's issue was closed while its ticket is open")
	}
	if err := w.st.CloseTicket("DEMO-1", store.Closed{Reason: store.CloseCovered, By: "example/demo#2", At: "2026-01-02"}); err != nil {
		t.Fatal(err)
	}
	w.sync(false)
	if needOpen() {
		t.Errorf("the need's issue stayed open after its ticket was closed")
	}
}

func placedStatus(c *fakeClient, what string) string {
	got := ""
	for _, p := range c.placedItems {
		if p.StoreID == what {
			got = p.Status
		}
	}
	return got
}

// TestMirrorTasksLegacyChartIssueClosesWithItsTickets: a legacy chart's issue
// is opened and closed by its own Status, as before.
func TestMirrorTasksLegacyChartIssueClosesWithItsTickets(t *testing.T) {
	w := newTasksWorld(t)
	_, client := w.sync(false)
	chartOpen := func() bool {
		for _, is := range client.issues {
			if is.Title == "Chart: old" {
				return is.Open
			}
		}
		t.Fatal("no issue for the legacy chart")
		return false
	}
	if !chartOpen() {
		t.Fatal("the legacy chart's issue starts closed")
	}
	if err := w.st.CloseTicket("DEMO-4", store.Closed{Reason: store.CloseCovered, By: "example/demo#1", At: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	w.sync(false)
	if chartOpen() {
		t.Error("the legacy chart's issue stayed open after its only ticket closed")
	}
}

// TestMirrorTasksIssueStatusSeesACarriedTasksPullRequest: with tasks on, a
// task's merged pull request counts toward the need's Status though the need
// is made before the task.
func TestMirrorTasksIssueStatusSeesACarriedTasksPullRequest(t *testing.T) {
	w := newTasksWorld(t)
	_, client := w.sync(true)
	client.prs = map[string][]github.PullRequest{
		"example/demo@2-first-task": {{Number: 5, NodeID: "PR_5", State: "MERGED"}},
	}
	if err := w.st.CloseTicket("DEMO-3", store.Closed{Reason: store.CloseCovered, By: "example/demo#1", At: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	w.sync(true)
	if got := placedStatus(client, "DEMO-2"); got != "Done" {
		t.Fatalf("the task's Status = %q, want Done from its merged pull request", got)
	}
	if got := placedStatus(client, "DEMO-1"); got != "Done" {
		t.Errorf("the need's Status = %q, want Done once both tasks are", got)
	}
}

// TestMirrorTasksClosedIssueTicketShowsItsOwnStatus: a closed issue ticket
// shows its own Status though its tasks are open.
func TestMirrorTasksClosedIssueTicketShowsItsOwnStatus(t *testing.T) {
	w := newTasksWorld(t)
	w.sync(false)
	if err := w.st.CloseTicket("DEMO-1", store.Closed{Reason: store.CloseCovered, By: "example/demo#1", At: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	_, client := w.sync(false)
	if got := placedStatus(client, "DEMO-1"); got != "Done" {
		t.Errorf("a closed need's Status = %q, want Done", got)
	}
}

// TestMirrorTasksLeavesTheOtherTicketsAlone: with the setting off a ticket no
// chart lists, a legacy chart's ticket that a chart with an issue also lists,
// and an issue ticket with no chart are all mirrored; the chartless issue
// ticket has its own Status and no plan comment.
func TestMirrorTasksLeavesTheOtherTicketsAlone(t *testing.T) {
	w := newTasksWorld(t)
	for id, tk := range map[string]store.Ticket{
		"DEMO-6": {Title: "Standalone"},
		"DEMO-7": {Title: "A need with no plan", Kind: store.KindIssue},
	} {
		if err := w.st.CreateTicketRecord(id, tk); err != nil {
			t.Fatal(err)
		}
	}
	both := "issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: First task\n  - id: DEMO-3\n    title: Second task\n  - id: DEMO-4\n    title: Legacy ticket\n"
	if err := os.WriteFile(w.st.ChartFile("plan"), []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	report, client := w.sync(false)
	if want := []string{"DEMO-1", "DEMO-4", "DEMO-6", "DEMO-7", "charts/old"}; !sameStrings(createdWhats(report), want) {
		t.Fatalf("Created = %v, want %v", createdWhats(report), want)
	}
	if len(client.comments) != 1 {
		t.Errorf("comments = %q, want only DEMO-1's plan", commentsOn(client))
	}
	var status string
	for _, p := range client.placedItems {
		if p.StoreID == "DEMO-7" {
			status = p.Status
		}
	}
	if status != "Backlog" {
		t.Errorf("chartless issue ticket's Status = %q, want its own (Backlog)", status)
	}
}

func (w *tasksWorld) record(what string) githubRecord {
	w.t.Helper()
	return readTestRecord(w.t, treeRecordPath(w.t, w.st, what))
}

// TestMirrorTasksOffStopsCarryingMirroredTasks: turning the setting off after
// tasks were mirrored leaves their issues open and untouched and reports how
// many the sync no longer carries.
func TestMirrorTasksOffStopsCarryingMirroredTasks(t *testing.T) {
	w := newTasksWorld(t)
	w.sync(true)
	path := w.st.TicketFilePath("DEMO-2")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "First task", "Renamed task", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	report, client := w.sync(false)
	if report.TasksNotCarried != 2 {
		t.Errorf("TasksNotCarried = %d, want 2", report.TasksNotCarried)
	}
	for _, u := range report.Updated {
		if u.What == "DEMO-2" || u.What == "DEMO-3" {
			t.Errorf("a task no longer carried was updated: %+v", u)
		}
	}
	for _, is := range client.issues {
		if strings.Contains(is.Title, "Renamed") {
			t.Errorf("a task's issue was retitled after the setting went off")
		}
	}
}

// TestMirrorTasksKeepsALegacyChartsRecordAlone: a chart with an issue: that
// already holds a record gets no update, link or placement.
func TestMirrorTasksKeepsAnIssueChartsRecordAlone(t *testing.T) {
	w := newTasksWorld(t)
	w.sync(false)
	writeAdoptedRecord(t, w.st, "charts/plan", "example/tracking", 99, "NODE_PLAN", "")
	w.st.ReloadTrackerTree()
	report, client := w.sync(false)
	for _, u := range report.Updated {
		if u.What == "charts/plan" {
			t.Errorf("chart with an issue updated: %+v", u)
		}
	}
	for _, p := range client.placedItems {
		if p.StoreID == "charts/plan" {
			t.Errorf("chart with an issue placed: %+v", p)
		}
	}
}
