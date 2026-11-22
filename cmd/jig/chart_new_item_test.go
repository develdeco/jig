package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

const itemBody = "Shoppers want to enter a coupon.\n\n- one\n- two\n"

func itemStub() *stubGitHub {
	return &stubGitHub{items: map[int]github.ItemDetail{
		48: {Number: 48, NodeID: "NODE_48", Title: "Coupons at checkout", Body: itemBody, URL: "https://github.com/example/tracking/issues/48", Open: true},
	}}
}

// TestChartNewItemBindsTheItemAsTheIssuesOwn: the issue carries the item's
// title and text, the chart plans it, the record is the item's, one commit
// lands it, the checkpoint sync opens no second issue and updates the item in
// place, and a second bind of the same item is refused naming the issue.
func TestChartNewItemBindsTheItemAsTheIssuesOwn(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginCloneWithGitHubTracker(t, clone)
	stub := itemStub()
	e.mirrorClient = stub

	out, code := runMain(t, e, "", "chart", "new", "shop", "--item", "#48", "--store", clone)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"issue: DEMO-1", "item: 48", "item_url: ", "title: Coupons at checkout", "name: shop"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.ReadTicket("DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.KindOrDefault() != store.KindIssue || rec.Title != "Coupons at checkout" || rec.Body != itemBody || rec.Slug != "coupons-at-checkout" {
		t.Errorf("issue = %+v, want the item's title and text", rec)
	}
	if got, _ := st.ChartIssue("shop"); got != "DEMO-1" {
		t.Errorf("chart issue = %q, want DEMO-1", got)
	}
	data, ok := treeRecordOf(t, clone, "DEMO-1")
	if !ok || !strings.Contains(data, "issue: 48") || !strings.Contains(data, "node_id: NODE_48") {
		t.Errorf("record of DEMO-1 = %q, %v, want #48's", data, ok)
	}
	if ticket, ok, err := st.TicketOfEntry(store.TrackerEntry{Provider: store.ProviderGitHub, Scope: "example/tracking", ID: "48"}); err != nil || !ok || ticket != "DEMO-1" {
		t.Errorf("TicketOfEntry = %q, %v, %v; want DEMO-1", ticket, ok, err)
	}
	if stub.created != 0 {
		t.Errorf("the sync opened %d issues, want none: the item is the issue", stub.created)
	}
	if len(stub.updated) != 1 || stub.updated[0] != "NODE_48" {
		t.Errorf("updated = %v, want the item updated in place once", stub.updated)
	}
	log, err := gitx.Run("", "--git-dir", remote, "log", "-6", "--pretty=%s")
	if err != nil || !strings.Contains(log, "chart shop: new chart (issue DEMO-1 binds #48)") {
		t.Errorf("origin log = %q, %v, want the bind's one commit", log, err)
	}
	if dirty, err := gitx.Run(clone, "status", "--porcelain"); err != nil || dirty != "" {
		t.Errorf("clone not clean: %q, %v", dirty, err)
	}
	if vout, vcode := runMain(t, e, "", "validate", "DEMO-1", "--store", clone); vcode != 0 {
		t.Errorf("jig validate DEMO-1: exit %d\n%s", vcode, vout)
	}

	out, code = runMain(t, e, "", "chart", "new", "again", "--item", "48", "--store", clone)
	if code == 0 || !strings.Contains(out, "TRACKER_ENTRY_TAKEN") || !strings.Contains(out, "DEMO-1") || !strings.Contains(out, "chart shop") {
		t.Errorf("second bind: exit %d, want TRACKER_ENTRY_TAKEN naming DEMO-1 and its chart:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(clone, "tickets", "DEMO-2")); err == nil {
		t.Errorf("the refused bind minted DEMO-2")
	}
	if _, err := os.Stat(filepath.Join(clone, "charts", "again")); err == nil {
		t.Errorf("the refused bind made a chart")
	}
}

// TestChartNewItemRefusalsMintNothing: a bad ref, another repo, a missing
// item, a name taken, --item with --need and a project with no tracker each
// leave the store with no ticket, chart or record.
func TestChartNewItemRefusalsMintNothing(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	e.mirrorClient = itemStub()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"ok", "--item", "forty"}, "owner/repo#48"},
		{[]string{"ok", "--item", "other/repo#48"}, "example/tracking"},
		{[]string{"ok", "--item", "https://github.com/example/tracking/pull/48"}, "VALIDATION_ERROR"},
		{[]string{"ok", "--item", "49"}, "ITEM_NOT_FOUND"},
		{[]string{"Bad Name", "--item", "48"}, "not valid"},
		{[]string{"ok", "--item", "48", "--need", "n"}, "excludes"},
		{[]string{"ok", "--item", "48", "--issue", "DEMO-1"}, "excludes"},
		{[]string{"ok", "--item", "48", "--title", "a\nb"}, "one line"},
		{[]string{"ok", "--json"}, "--json is for --item"},
	} {
		out, code := runMain(t, e, "", append([]string{"chart", "new", "--store", clone}, tc.args...)...)
		if code == 0 || !strings.Contains(out, tc.want) {
			t.Errorf("%v: exit %d, want a refusal with %q:\n%s", tc.args, code, tc.want, out)
		}
	}
	for _, dir := range []string{"tickets", "charts", "trackers"} {
		if _, err := os.Stat(filepath.Join(clone, dir)); err == nil {
			t.Errorf("a refusal left %s behind", dir)
		}
	}

	bare := filepath.Join(t.TempDir(), "bare")
	newTestOriginClone(t, bare)
	out, code := runMain(t, e, "", "chart", "new", "ok", "--item", "48", "--store", bare)
	if code == 0 || !strings.Contains(out, "trackers:") {
		t.Errorf("no tracker: exit %d, want a refusal naming trackers::\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(bare, "tickets")); err == nil {
		t.Errorf("a project with no tracker minted a ticket")
	}
}

// TestChartNewItemShowsAdvisoriesAndNamesTheChartByTitle: a closed item with
// linked pull requests is bound, the advisories are in the output and --json,
// and --title names the chart while the issue keeps the item's title.
func TestChartNewItemShowsAdvisoriesAndNamesTheChartByTitle(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	stub := &stubGitHub{items: map[int]github.ItemDetail{
		7: {Number: 7, NodeID: "NODE_7", Title: "Old thing", URL: "https://github.com/example/tracking/issues/7", Open: false, LinkedPullRequests: 2},
	}}
	e.mirrorClient = stub

	out, code := runMain(t, e, "", "chart", "new", "old", "--item", "7", "--title", "A better name", "--json", "--store", clone)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var got struct {
		Chart      map[string]string `json:"chart"`
		Issue      string            `json:"issue"`
		Item       map[string]any    `json:"item"`
		Advisories []string          `json:"advisories"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out)
	}
	if got.Issue != "DEMO-1" || got.Chart["title"] != "A better name" || got.Item["number"] != float64(7) {
		t.Errorf("json = %+v", got)
	}
	if len(got.Advisories) != 2 || !strings.Contains(got.Advisories[0], "7 is closed; the first sync reopens it") || !strings.Contains(got.Advisories[1], "2 linked pull request") {
		t.Errorf("advisories = %v", got.Advisories)
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if rec, _ := st.ReadTicket("DEMO-1"); rec.Title != "Old thing" {
		t.Errorf("issue title = %q, want the item's", rec.Title)
	}
	mapMD, err := os.ReadFile(st.ChartMapFile("old"))
	if err != nil || !strings.HasPrefix(string(mapMD), "# A better name\n") {
		t.Errorf("map = %q, %v, want the --title heading", mapMD, err)
	}

	text, code := runMain(t, e, "", "chart", "new", "old2", "--item", "7", "--store", clone)
	if code == 0 {
		t.Fatalf("rebinding 7 succeeded:\n%s", text)
	}
}
