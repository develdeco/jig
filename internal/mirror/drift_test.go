package mirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// writeAdoptedRecord writes rel (relative to root) with the bridge's own
// shape (repo, issue, node_id and item - no synced: key at all), the form a
// record jig never wrote carries until this sync adopts it
// (brief.md#Records, #Adoption). item is the board item the bridge had
// already placed the issue on; "" leaves the key out, for a record that
// names an issue never put on a board.
func writeAdoptedRecord(t *testing.T, root, rel, repo string, issue int, nodeID, item string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	body := recordHeader + "repo: " + repo + "\nissue: " + itoa(issue) + "\nnode_id: " + nodeID + "\n"
	if item != "" {
		body += "item: " + item + "\n"
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func readTestRecord(t *testing.T, abs string) githubRecord {
	t.Helper()
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	var rec githubRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestSyncAdoptsBridgeRecordWithNoMutation is brief.md#Seams's own critical
// path, in full: a record the bridge wrote (no synced: key) whose issue on
// GitHub already carries exactly the title and body the store's own
// renderer produces, already placed on a board item carrying the Status and
// Store ID this sync would set, syncs with *no* mutation at all - not the
// issue's title, body or state, not a link, and not the board item either
// (the placement and both field writes PlaceItem sends are skipped, not
// re-sent on every checkpoint). Only synced: gets populated, so the next
// sync has a baseline to compare against.
func TestSyncAdoptsBridgeRecordWithNoMutation(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	writeAdoptedRecord(t, st.Root, filepath.Join("tickets", "DEMO-1", "tracker", "github.yaml"), "example/tracking", 7, "NODE_ADOPTED", "ITEM_ADOPTED")

	client := &fakeClient{}
	*client.issue("NODE_ADOPTED") = fakeIssueState{Title: "First ticket", Body: ticketFooter("DEMO-1"), Open: true}
	// A ticket with no brief, no journal and no slice that has left queued
	// is Backlog (brief.md#Status and pull requests), and its Store ID is
	// its own id: exactly what the bridge had already set on this item.
	client.itemFields = map[string]fakeItemFields{"ITEM_ADOPTED": {Status: "Backlog", StoreID: "DEMO-1"}}

	cfg := loadCfg(t, st)
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(report.Drift) != 0 {
		t.Fatalf("Drift = %+v, want none (GitHub already matches)", report.Drift)
	}
	if len(report.Updated) != 0 {
		t.Fatalf("Updated = %+v, want none", report.Updated)
	}
	if len(report.Placed) != 0 || len(report.PlacedPRs) != 0 {
		t.Fatalf("Placed = %+v, PlacedPRs = %+v, want none (the board already matches)", report.Placed, report.PlacedPRs)
	}
	if len(client.updates) != 0 || len(client.closedDone) != 0 || len(client.reopened) != 0 {
		t.Fatalf("client mutated the issue (updates=%v closed=%v reopened=%v), want no mutation", client.updates, client.closedDone, client.reopened)
	}
	if len(client.calls) != 0 {
		t.Fatalf("CreateIssue called %+v, want none (the record already names an issue)", client.calls)
	}
	if len(client.placedItems) != 0 {
		t.Fatalf("PlaceItem called %+v, want none (the item already carries this Status and Store ID)", client.placedItems)
	}
	if len(client.subIssues) != 0 || len(client.removedSubs) != 0 || len(client.blockedBys) != 0 || len(client.removedBlockedBys) != 0 {
		t.Fatalf("client mutated a link (subIssues=%v removedSubs=%v blockedBys=%v removedBlockedBys=%v), want no mutation",
			client.subIssues, client.removedSubs, client.blockedBys, client.removedBlockedBys)
	}

	rec := readTestRecord(t, filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if rec.Synced == nil || rec.Synced.Title != "First ticket" || rec.Synced.State != "OPEN" {
		t.Fatalf("record = %+v, want synced populated from what GitHub already showed", rec.Synced)
	}
}

// TestSyncRestoresTitleBodyAndStateDrift checks brief.md#Ownership and
// drift's own mechanic: a title, body and open/closed state the last sync
// itself wrote, now found changed on GitHub, are reported as drift and
// overwritten with the store's value again on the next sync.
func TestSyncRestoresTitleBodyAndStateDrift(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	rec := readTestRecord(t, filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if rec.Synced == nil || rec.Synced.Title != "First ticket" {
		t.Fatalf("record after first sync = %+v, want synced.title populated", rec.Synced)
	}

	// Someone edits the issue directly on GitHub: title, body and state all
	// diverge from what the first sync itself wrote.
	edited := client.issue(rec.NodeID)
	edited.Title = "Somebody renamed this"
	edited.Body = "Somebody rewrote this body too"
	edited.Open = false

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	wantFields := map[string]bool{"title": false, "body": false, "state": false}
	for _, d := range report.Drift {
		if d.What != "DEMO-1" || d.Issue != 1 {
			t.Fatalf("drift line = %+v, want it naming DEMO-1's issue #1", d)
		}
		if _, ok := wantFields[d.Field]; !ok {
			t.Fatalf("unexpected drift field %q", d.Field)
		}
		wantFields[d.Field] = true
	}
	for field, saw := range wantFields {
		if !saw {
			t.Fatalf("Drift = %+v, missing %q", report.Drift, field)
		}
	}

	if edited.Title != "First ticket" {
		t.Fatalf("issue title = %q, want restored to the store's value", edited.Title)
	}
	if edited.Body != ticketFooter("DEMO-1") {
		t.Fatalf("issue body = %q, want restored to the store's value", edited.Body)
	}
	if !edited.Open {
		t.Fatalf("issue Open = false, want reopened (the store's own status keeps it open)")
	}
	if len(client.reopened) != 1 || client.reopened[0] != rec.NodeID {
		t.Fatalf("reopened = %v, want DEMO-1's issue reopened once", client.reopened)
	}
}

// TestSyncRemovesExtraChartSubIssueAsDrift checks brief.md#Links's own drift
// rule: a sub-issue added on GitHub to a chart's issue that is no ticket's
// issue is taken out again, reported as drift.
func TestSyncRemovesExtraChartSubIssueAsDrift(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Chart ticket")
	runGit(t, work, "push", "origin", "main")

	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets:\n  - id: DEMO-1\n    title: Chart ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add chart")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	chartRec := readTestRecord(t, filepath.Join(st.Root, "charts", "demo", "github.yaml"))

	// A human adds an unrelated issue as a sub-issue of the chart's by hand.
	client.subIssuesOf[chartRec.NodeID] = append(client.subIssuesOf[chartRec.NodeID], "NODE_HAND_ADDED")
	client.parentOf["NODE_HAND_ADDED"] = chartRec.NodeID

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	found := false
	for _, d := range report.Drift {
		if d.What == "charts/demo" && d.Field == "sub-issue" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Drift = %+v, want a sub-issue entry for charts/demo", report.Drift)
	}
	if len(client.removedSubs) != 1 || client.removedSubs[0] != (fakeSubIssue{ParentID: chartRec.NodeID, SubIssueID: "NODE_HAND_ADDED"}) {
		t.Fatalf("removedSubs = %+v, want the hand-added sub-issue removed", client.removedSubs)
	}
}

// TestSyncRestoresRemovedParentLinkAsDrift checks brief.md#Ownership and
// drift's own parent side: a sub-issue link to its chart that this sync
// itself wrote, now removed on GitHub, is restored and reported as drift -
// not silently left unlinked, the gap finding 5 of this slice closed.
func TestSyncRestoresRemovedParentLinkAsDrift(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Chart ticket")
	runGit(t, work, "push", "origin", "main")

	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets:\n  - id: DEMO-1\n    title: Chart ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add chart")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if len(client.subIssues) != 1 {
		t.Fatalf("subIssues after first Sync = %+v, want DEMO-1 linked once", client.subIssues)
	}

	// Someone removes DEMO-1's parent link on GitHub by hand.
	delete(client.parentOf, "NODE_Chart ticket")
	client.subIssuesOf["NODE_Chart: demo"] = nil

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	found := false
	for _, d := range report.Drift {
		if d.What == "DEMO-1" && d.Field == "parent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Drift = %+v, want a parent entry for DEMO-1", report.Drift)
	}
	if len(client.subIssues) != 2 {
		t.Fatalf("subIssues after second Sync = %+v, want the link restored", client.subIssues)
	}
	if client.parentOf["NODE_Chart ticket"] != "NODE_Chart: demo" {
		t.Fatalf("parentOf[NODE_Chart ticket] = %q, want it restored to the chart's issue", client.parentOf["NODE_Chart ticket"])
	}
}

// TestSyncRestoresRemovedBlockedByLinkAsDrift checks brief.md#Ownership and
// drift's own blocked-by side: a blocked-by edge this sync itself wrote,
// removed on GitHub, is restored and reported as drift.
func TestSyncRestoresRemovedBlockedByLinkAsDrift(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Blocker")
	mintTestTicketWithBlockers(t, st, "DEMO-2", "Blocked", []store.TicketBlockedBy{{Ticket: "DEMO-1", Kind: "merged"}})
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if len(client.blockedBys) != 1 {
		t.Fatalf("blockedBys after first Sync = %+v, want DEMO-2 blocked once", client.blockedBys)
	}

	// Someone removes DEMO-2's blocked-by edge on GitHub by hand.
	client.blockedByOf["NODE_Blocked"] = nil

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	found := false
	for _, d := range report.Drift {
		if d.What == "DEMO-2" && d.Field == "blocked_by" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Drift = %+v, want a blocked_by entry for DEMO-2", report.Drift)
	}
	if len(client.blockedBys) != 2 {
		t.Fatalf("blockedBys after second Sync = %+v, want the link restored", client.blockedBys)
	}
	if !containsString(client.blockedByOf["NODE_Blocked"], "NODE_Blocker") {
		t.Fatalf("blockedByOf[NODE_Blocked] = %v, want it restored to include DEMO-1's issue", client.blockedByOf["NODE_Blocked"])
	}
}

// TestSyncRemovesAHandAddedBlockedByLinkAsDrift checks brief.md#Ownership
// and drift's own blocked-by side for an edge added on GitHub rather than
// through jig: it is taken out again and reported as drift, the same
// ownership rule the chart's extra sub-issue already gets.
func TestSyncRemovesAHandAddedBlockedByLinkAsDrift(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Lone ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// A human blocks DEMO-1's issue by hand, on an unrelated issue.
	if client.blockedByOf == nil {
		client.blockedByOf = map[string][]string{}
	}
	client.blockedByOf["NODE_Lone ticket"] = []string{"NODE_HAND_ADDED"}

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	found := false
	for _, d := range report.Drift {
		if d.What == "DEMO-1" && d.Field == "blocked_by" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Drift = %+v, want a blocked_by entry for DEMO-1", report.Drift)
	}
	if len(client.removedBlockedBys) != 1 || client.removedBlockedBys[0] != (fakeBlockedBy{IssueID: "NODE_Lone ticket", BlockingID: "NODE_HAND_ADDED"}) {
		t.Fatalf("removedBlockedBys = %+v, want the hand-added blocker removed", client.removedBlockedBys)
	}
	if containsString(client.blockedByOf["NODE_Lone ticket"], "NODE_HAND_ADDED") {
		t.Fatalf("blockedByOf[NODE_Lone ticket] = %v, want the hand-added blocker gone", client.blockedByOf["NODE_Lone ticket"])
	}
}

// TestSyncRestoresStatusAndStoreIDDrift checks brief.md#Ownership and
// drift's own board side: a board item's Status or Store ID edited on GitHub
// is reported as drift and overwritten with the store's value again.
func TestSyncRestoresStatusAndStoreIDDrift(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	rec := readTestRecord(t, filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if rec.Item == "" {
		t.Fatalf("record = %+v, want an item id after the first sync", rec)
	}
	client.itemFields[rec.Item] = fakeItemFields{Status: "In review", StoreID: "SOMETHING-ELSE"}

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	wantFields := map[string]bool{"status": false, "store_id": false}
	for _, d := range report.Drift {
		if d.What != "DEMO-1" {
			continue
		}
		if _, ok := wantFields[d.Field]; ok {
			wantFields[d.Field] = true
		}
	}
	for field, saw := range wantFields {
		if !saw {
			t.Fatalf("Drift = %+v, missing %q for DEMO-1", report.Drift, field)
		}
	}

	last := client.placedItems[len(client.placedItems)-1]
	if last.Status != "Backlog" || last.StoreID != "DEMO-1" {
		t.Fatalf("last PlaceItem call = %+v, want the store's own Status and Store ID restored", last)
	}
}

// TestSyncRecreatesAnIssueGoneFromGitHub checks brief.md#Adoption: "a
// recorded issue that is gone from GitHub gets a new issue, and the sync
// prints that it did".
func TestSyncRecreatesAnIssueGoneFromGitHub(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	rec := readTestRecord(t, filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	delete(client.issues, rec.NodeID)
	if client.goneNodeIDs == nil {
		client.goneNodeIDs = map[string]bool{}
	}
	client.goneNodeIDs[rec.NodeID] = true

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(report.Recreated) != 1 || report.Recreated[0].What != "DEMO-1" {
		t.Fatalf("Recreated = %+v, want DEMO-1 recreated", report.Recreated)
	}

	newRec := readTestRecord(t, filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if newRec.Issue == rec.Issue {
		t.Fatalf("record after recreation = %+v, want a fresh issue number (was #%d)", newRec, rec.Issue)
	}
}

// TestSyncSucceedsWithARecordedPullRequestMissingANodeID is the owner's
// decision for gate finding r8-f2, and the human's r9-f1 decision on top of
// it: a prs: entry with no node_id (hand-edited, or written by a bridge
// version that omitted it; brief.md#Records says records the bridge wrote
// are adopted as they are) must never reach PullRequestsByIDs as an empty
// id - which the fake client here fails outright, as the real GitHub
// client's do() does on nodes(ids: [""]) - so the sync for this one
// malformed record, and the whole store, still succeeds. A sync never
// deletes store data it cannot check: the entry stays listed in the
// issue's body (with no state) and stays in the record afterward, not
// dropped from either just because it could not be re-checked by id.
func TestSyncSucceedsWithARecordedPullRequestMissingANodeID(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	abs := filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	body := recordHeader + "repo: example/tracking\nissue: 7\nnode_id: NODE_1\nprs:\n  - repo: example/demo\n    number: 31\n    node_id: \"\"\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &fakeClient{}
	*client.issue("NODE_1") = fakeIssueState{Title: "First ticket", Body: ticketFooter("DEMO-1"), Open: true}

	cfg := loadCfg(t, st)
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("Sync: %v, want it to succeed with the node_id-less prs: entry simply left out of the PullRequestsByIDs lookup", err)
	}

	if got := client.issue("NODE_1").Body; !strings.Contains(got, "example/demo#31") {
		t.Fatalf("issue body = %q, want it still listing example/demo#31", got)
	}

	rec, err := readRecord(abs)
	if err != nil {
		t.Fatalf("readRecord: %v", err)
	}
	found := false
	for _, p := range rec.PRs {
		if p.Repo == "example/demo" && p.Number == 31 {
			found = true
		}
	}
	if !found {
		t.Fatalf("record's prs: = %+v, want example/demo#31 still there", rec.PRs)
	}
}
