package mirror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
)

// addRepoToProjectYAML rewrites work's project.yaml to also declare a
// product repo, so findPullRequests has somewhere to search.
func addRepoToProjectYAML(t *testing.T, work string) {
	t.Helper()
	projectYAML := `schema_version: 2
name: demo
keys:
  DEMO: everything in demo
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
repos:
  - remote: https://github.com/example/demo
platform: platform/
`
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte(projectYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add repo")
	runGit(t, work, "push", "origin", "main")
}

// TestSyncPlacesIssuesAndPullRequestsOnTheBoard is the board's own
// critical path: a ticket and the chart it belongs to are placed on the
// project with their Status and Store ID, and the ticket's one open pull
// request is placed alongside it (In review) while its closed one is left
// off the board (a closed one is listed but not
// placed). Each record keeps the item id(s) PlaceItem handed back.
func TestSyncPlacesIssuesAndPullRequestsOnTheBoard(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	addRepoToProjectYAML(t, work)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets:\n  - id: DEMO-1\n    title: First ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add chart")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{
		prs: map[string][]github.PullRequest{
			// The ticket's branch is led by its issue's number once the
			// issue exists, and DEMO-1's is the first the fake hands out.
			"example/demo@1-first-ticket": {
				{Number: 5, NodeID: "PR_5", State: "OPEN"},
				{Number: 3, NodeID: "PR_3", State: "CLOSED"},
			},
		},
	}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(client.ensureProject) != 1 {
		t.Fatalf("EnsureProject called %d times, want 1", len(client.ensureProject))
	}
	ep := client.ensureProject[0]
	if ep.OwnerLogin != "example" || ep.IsOrg || ep.Number != 7 || ep.RepoOwner != "example" || ep.RepoName != "tracking" {
		t.Fatalf("EnsureProject call = %+v, want the project URL's owner/number and the issue repo's owner/name", ep)
	}

	if len(report.Placed) != 2 {
		t.Fatalf("Placed = %+v, want 2 (DEMO-1 and its chart)", report.Placed)
	}
	if report.Placed[0] != (PlacedItem{What: "DEMO-1", Status: "In review"}) {
		t.Fatalf("Placed[0] = %+v, want DEMO-1 at In review (its one open pull request)", report.Placed[0])
	}
	if report.Placed[1] != (PlacedItem{What: "charts/demo", Status: "In progress"}) {
		t.Fatalf("Placed[1] = %+v, want charts/demo at In progress (its one ticket is In review)", report.Placed[1])
	}
	if len(report.PlacedPRs) != 1 || report.PlacedPRs[0] != (PlacedPR{Ticket: "DEMO-1", Owner: "example", Repo: "demo", Number: 5, Status: "In review"}) {
		t.Fatalf("PlacedPRs = %+v, want only the open pull request #5 placed, not the closed #3", report.PlacedPRs)
	}
	for _, p := range client.placedItems {
		if p.StoreID == "" {
			t.Fatalf("placed item %+v carries no Store ID", p)
		}
	}

	data, err := os.ReadFile(treeRecordPath(t, st, "DEMO-1"))
	if err != nil {
		t.Fatal(err)
	}
	var rec githubRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Item == "" {
		t.Fatalf("DEMO-1's record = %+v, want an item id (the record keeps the item id in item)", rec)
	}
	if len(rec.PRs) != 1 || rec.PRs[0].Repo != "example/demo" || rec.PRs[0].Number != 5 || rec.PRs[0].NodeID != "PR_5" || rec.PRs[0].Item == "" {
		t.Fatalf("DEMO-1's record prs = %+v, want only the open pull request #5, with its own item id", rec.PRs)
	}

	chartData, err := os.ReadFile(treeRecordPath(t, st, "charts/demo"))
	if err != nil {
		t.Fatal(err)
	}
	var chartRec githubRecord
	if err := yaml.Unmarshal(chartData, &chartRec); err != nil {
		t.Fatal(err)
	}
	if chartRec.Item == "" {
		t.Fatalf("chart demo's record = %+v, want an item id", chartRec)
	}

	// A second Sync finds every item already carrying the Status and Store
	// ID this sync would set, so it places nothing again: a store
	// whose records match GitHub syncs with no mutation.
	placedBefore := len(client.placedItems)
	report2, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(report2.Placed) != 0 {
		t.Fatalf("second Sync Placed = %+v, want none (nothing changed)", report2.Placed)
	}
	if len(report2.PlacedPRs) != 0 {
		t.Fatalf("second Sync PlacedPRs = %+v, want none (nothing changed)", report2.PlacedPRs)
	}
	if len(client.placedItems) != placedBefore {
		t.Fatalf("second Sync called PlaceItem %d more time(s), want no mutation", len(client.placedItems)-placedBefore)
	}
}

// TestSyncReadsGitHubInBatches pins what keeps a sync inside its bound as
// a store grows: a sync over several tickets reads GitHub once for every
// issue, once per repo for every ticket's pull requests, and once for every
// board item - never once per ticket, which made a few dozen tickets
// outlast the sync's two minutes - and the batched answers still reach the
// right ticket. The steps share one store, each building on the last.
func TestSyncReadsGitHubInBatches(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	addRepoToProjectYAML(t, work)
	for i := 1; i <= 3; i++ {
		mintTestTicket(t, st, fmt.Sprintf("DEMO-%d", i), fmt.Sprintf("Ticket %d", i))
	}
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{
		prs: map[string][]github.PullRequest{
			// Each ticket's branch is led by its issue's number, handed out in
			// the tickets' order.
			"example/demo@1-ticket-1": {{Number: 5, NodeID: "PR_5", State: "OPEN"}},
			"example/demo@3-ticket-3": {{Number: 6, NodeID: "PR_6", State: "MERGED"}},
		},
	}
	sync := func() (SyncReport, error) {
		client.reads = nil
		return Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	}
	onceEach := func(t *testing.T, step string) {
		t.Helper()
		for _, name := range []string{"FetchIssues", "PullRequestsByHeads", "ItemFieldValues"} {
			if client.reads[name] != 1 {
				t.Fatalf("%s reads = %v, want %s called once for all three tickets", step, client.reads, name)
			}
		}
		if client.reads["PullRequestsByIDs"] > 1 {
			t.Fatalf("%s reads = %v, want PullRequestsByIDs at most once", step, client.reads)
		}
	}

	first, err := sync()
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	wantPRs := []PlacedPR{
		{Ticket: "DEMO-1", Owner: "example", Repo: "demo", Number: 5, Status: "In review"},
		{Ticket: "DEMO-3", Owner: "example", Repo: "demo", Number: 6, Status: "Done"},
	}
	if len(first.PlacedPRs) != 2 || first.PlacedPRs[0] != wantPRs[0] || first.PlacedPRs[1] != wantPRs[1] {
		t.Fatalf("first Sync PlacedPRs = %+v, want each ticket's own pull request: %+v", first.PlacedPRs, wantPRs)
	}
	// The issue repo is public, so each new ticket's pull requests are found
	// before its create, for the publish-safety scan: one head query for all
	// three, then one more for the update.
	if client.reads["PullRequestsByHeads"] != 2 {
		t.Fatalf("first Sync reads = %v, want two head queries: the creates' and the update's", client.reads)
	}

	second, err := sync()
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	onceEach(t, "second Sync")
	if len(second.Placed) != 0 || len(second.PlacedPRs) != 0 || len(second.Updated) != 0 {
		t.Fatalf("second Sync = %+v, want no write: the batched reads must still see what the first sync wrote", second)
	}

	// A board item deleted on GitHub reads as one with no values, so it is
	// placed again.
	delete(client.itemFields, readTestRecord(t, treeRecordPath(t, st, "DEMO-2")).Item)
	third, err := sync()
	if err != nil {
		t.Fatalf("third Sync: %v", err)
	}
	onceEach(t, "third Sync")
	if len(third.Placed) != 1 || third.Placed[0] != (PlacedItem{What: "DEMO-2", Status: "Backlog"}) {
		t.Fatalf("third Sync Placed = %+v, want DEMO-2 placed again", third.Placed)
	}

	// A record with no node_id fails the sync by name rather than reading as
	// a gone issue and opening a second one.
	abs := treeRecordPath(t, st, "DEMO-2")
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	edited := regexp.MustCompile(`(?m)^node_id: .*$`).ReplaceAllString(string(raw), `node_id: ""`)
	if edited == string(raw) {
		t.Fatalf("could not empty DEMO-2's node_id:\n%s", raw)
	}
	if err := os.WriteFile(abs, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	created := len(client.calls)
	if _, err := sync(); err == nil || !strings.Contains(err.Error(), "DEMO-2's record names no issue node_id") {
		t.Fatalf("Sync with no node_id = %v, want it refused by name", err)
	}
	if len(client.calls) != created {
		t.Fatalf("Sync with no node_id opened %d issue(s), want none", len(client.calls)-created)
	}
}

// TestSyncRefusesMissingProjectOrTokenScope checks the board's own
// refusal: EnsureProject failing with MIRROR_PROJECT_NOT_FOUND - the shape a
// project that does not exist and a token without the project scope both
// take, since GitHub answers either the same way - surfaces from Sync as
// that same *axi.Error, naming gh auth refresh -s project.
func TestSyncRefusesMissingProjectOrTokenScope(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{projectErr: &axi.Error{
		Msg:  "github project for example (number 7) was not found, or this token lacks the project scope",
		Code: "MIRROR_PROJECT_NOT_FOUND",
		Help: []string{"gh auth refresh -s project"},
	}}
	_, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err == nil {
		t.Fatal("Sync: want an error when the project cannot be resolved")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "MIRROR_PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want a MIRROR_PROJECT_NOT_FOUND *axi.Error", err)
	}
	if len(ae.Help) != 1 || ae.Help[0] != "gh auth refresh -s project" {
		t.Fatalf("help = %v, want [\"gh auth refresh -s project\"]", ae.Help)
	}
}

// TestParseProjectURL covers the two owner kinds of a project URL.
func TestParseProjectURL(t *testing.T) {
	cases := []struct {
		url        string
		wantLogin  string
		wantIsOrg  bool
		wantNumber int
		wantErr    bool
	}{
		{"https://github.com/users/example/projects/7", "example", false, 7, false},
		{"https://github.com/orgs/example-org/projects/12", "example-org", true, 12, false},
		{"not a url", "", false, 0, true},
	}
	for _, tc := range cases {
		login, isOrg, number, err := parseProjectURL(tc.url)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseProjectURL(%q): want an error", tc.url)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseProjectURL(%q): %v", tc.url, err)
		}
		if login != tc.wantLogin || isOrg != tc.wantIsOrg || number != tc.wantNumber {
			t.Fatalf("parseProjectURL(%q) = (%q, %v, %d), want (%q, %v, %d)", tc.url, login, isOrg, number, tc.wantLogin, tc.wantIsOrg, tc.wantNumber)
		}
	}
}
