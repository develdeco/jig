package mirror

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

func bindDeps(st *store.Store, client github.Client) Deps {
	cfg := project.Config{
		Keys:   map[string]string{"DEMO": "everything in demo"},
		GitHub: &project.GitHubTracker{Repo: "example/tracking", Project: "https://github.com/users/example/projects/7"},
	}
	return Deps{Store: st, Cfg: cfg, Client: client}
}

func bindClient() *fakeClient {
	return &fakeClient{items: map[int]github.ItemDetail{
		48: {Number: 48, NodeID: "NODE_48", Title: "Coupons", Body: "  Body as written.\n", URL: "https://github.com/example/tracking/issues/48", Open: true},
	}}
}

// TestBindItemMakesTheIssueChartAndRecordInOneCommit: one commit lands the
// issue (the item's text unchanged), the chart that plans it and the record,
// the record has no synced: key, and the sync after it opens no second issue.
func TestBindItemMakesTheIssueChartAndRecordInOneCommit(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	client := bindClient()
	d := bindDeps(st, client)
	before := strings.TrimSpace(runGit(t, work, "rev-list", "--count", "HEAD"))

	res, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	if err != nil {
		t.Fatalf("BindItem: %v", err)
	}
	if res.Issue != "DEMO-1" || res.Heading != "Coupons" || len(res.Advisories) != 0 {
		t.Fatalf("result = %+v", res)
	}
	rec, err := st.ReadTicket("DEMO-1")
	if err != nil || rec.Body != "  Body as written.\n" || rec.Title != "Coupons" {
		t.Errorf("issue = %+v, %v, want the item's title and body unchanged", rec, err)
	}
	if after := strings.TrimSpace(runGit(t, work, "rev-list", "--count", "HEAD")); after == before {
		t.Fatalf("no commit landed")
	}
	if subject := strings.TrimSpace(runGit(t, work, "log", "-1", "--pretty=%s")); subject != "chart shop: new chart (issue DEMO-1 binds #48)" {
		t.Errorf("commit subject = %q", subject)
	}
	entry := githubEntry("example/tracking", 48)
	data, err := os.ReadFile(st.TrackerRecordPath(entry))
	if err != nil || !strings.Contains(string(data), "ticket: DEMO-1") || !strings.Contains(string(data), "node_id: NODE_48") || strings.Contains(string(data), "synced") {
		t.Errorf("record = %q, %v", data, err)
	}
	if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
		t.Errorf("store is dirty after the bind:\n%s", status)
	}

	if _, err := Sync(d, SyncOpts{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	for _, c := range client.calls {
		if c.Title == "Coupons" {
			t.Errorf("the sync opened a second issue for the item: %+v", client.calls)
		}
	}
	if len(client.updates) == 0 || client.updates[0].NodeID != "NODE_48" {
		t.Errorf("updates = %+v, want the item updated in place", client.updates)
	}

	_, err = BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "again"})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_TAKEN" || !strings.Contains(ae.Msg, "DEMO-1 (chart shop)") {
		t.Errorf("second bind err = %v, want TRACKER_ENTRY_TAKEN naming the issue and its chart", err)
	}
	if client.reads["ItemByNumber"] != 1 {
		t.Errorf("ItemByNumber ran %d times, want the refusal to come before the read", client.reads["ItemByNumber"])
	}
}

// TestBindItemLeavesNothingBehindWhenTheChartFails: the chart cannot be made
// after the issue was minted, and Claim undoes the issue's folder too.
func TestBindItemLeavesNothingBehindWhenTheChartFails(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	d := bindDeps(st, bindClient())
	// A plain file holds the chart's place: the write only finds out after
	// the mint, since BindOpts.ChartTaken is not given.
	if err := os.MkdirAll(filepath.Join(work, "charts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "charts", "shop"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "a file in the chart's way")
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	_, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	if err == nil {
		t.Fatal("BindItem: want the chart's refusal")
	}
	if store.IsUndoIncomplete(err) {
		t.Fatalf("undo incomplete: %v", err)
	}
	if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
		t.Errorf("store is dirty after the failed bind:\n%s", status)
	}
	for _, p := range []string{st.TicketRelDir("DEMO-1"), st.TrackerRelPath(githubEntry("example/tracking", 48))} {
		if _, err := os.Stat(filepath.Join(work, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived the failed bind (%v)", p, err)
		}
	}
	if got := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
}

// TestBindItemRefusalsMintNothing: an item GitHub lacks, another repo and a
// read that cannot be made are errors with nothing minted.
func TestBindItemRefusalsMintNothing(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	d := bindDeps(st, bindClient())
	_, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 49}, Chart: "shop"})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "ITEM_NOT_FOUND" {
		t.Errorf("missing item err = %v, want ITEM_NOT_FOUND", err)
	}
	_, err = BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Repo: "other/repo", Number: 48}, Chart: "shop"})
	if err == nil || !strings.Contains(err.Error(), "example/tracking") {
		t.Errorf("other repo err = %v", err)
	}
	for _, dir := range []string{"tickets", "charts", "trackers"} {
		if _, err := os.Stat(filepath.Join(work, dir)); err == nil {
			t.Errorf("a refusal left %s behind", dir)
		}
	}
}

// TestBindItemShowsAdvisories: a closed item and one with linked pull
// requests are bound, with both advisories.
func TestBindItemShowsAdvisories(t *testing.T) {
	st, _, _ := newTestRemoteStore(t)
	client := bindClient()
	client.items[48] = github.ItemDetail{Number: 48, NodeID: "NODE_48", Title: "T", Open: false, LinkedPullRequests: 1}
	res, err := BindItem(context.Background(), bindDeps(st, client), BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	if err != nil || len(res.Advisories) != 2 {
		t.Fatalf("BindItem = %+v, %v, want two advisories", res, err)
	}
	if res.Advisories[0] != "48 is closed; the first sync reopens it" {
		t.Errorf("advisory = %q", res.Advisories[0])
	}
}
