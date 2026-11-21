package mirror

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// The tests below decide the sync's rules by calling the planning functions
// (updateIssues, linkParentsAndBlockers, placeBoardItems, previewCreateRender)
// over a store and the fake GitHub view, with no remote and no commit: a
// rule is a function of the store, the records and what GitHub shows, and a
// full Sync through a bare remote adds git work that decides none of them.

// ruleWorld is a store with no remote, its config and a fake GitHub view.
type ruleWorld struct {
	t      *testing.T
	st     *store.Store
	cfg    project.Config
	client *fakeClient
}

// newRuleWorld makes a store (no git, no remote) whose config declares the
// tracker; repos adds a product repo, so pull requests have somewhere to be
// found.
func newRuleWorld(t *testing.T, repos bool) *ruleWorld {
	t.Helper()
	dir := t.TempDir()
	yamlText := `schema_version: 2
name: demo
keys:
  DEMO: everything in demo
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
`
	if repos {
		yamlText += "repos:\n  - remote: https://github.com/example/demo\n"
	} else {
		yamlText += "repos: []\n"
	}
	yamlText += "platform: platform/\n"
	if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &ruleWorld{t: t, st: st, cfg: loadCfg(t, st), client: &fakeClient{}}
}

// ticket writes a ticket's record.
func (w *ruleWorld) ticket(id string, tk store.Ticket) {
	w.t.Helper()
	if err := w.st.CreateTicketRecord(id, tk); err != nil {
		w.t.Fatal(err)
	}
}

// chart writes a chart demo whose map is mapMD and whose tickets are ids.
func (w *ruleWorld) chart(mapMD string, ids ...string) {
	w.t.Helper()
	dir := filepath.Join(w.st.Root, "charts", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "map.md"), []byte(mapMD), 0o644); err != nil {
		w.t.Fatal(err)
	}
	tickets := "tickets:\n"
	for _, id := range ids {
		tickets += "  - id: " + id + "\n    title: " + id + "\n"
	}
	if len(ids) == 0 {
		tickets = "tickets: []\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "tickets.yaml"), []byte(tickets), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// adopt gives what the bridge's own record of issue number n and returns its
// node id, the one the fake view keeps that issue's state under.
func (w *ruleWorld) adopt(what string, n int) string {
	w.t.Helper()
	node := "NODE_" + what
	writeAdoptedRecord(w.t, w.st, what, "example/tracking", n, node, "")
	return node
}

type planOpts struct {
	public, dryRun bool
	terms          []string
}

// plan runs the update, link and board steps of a sync, in the order doSync
// does, and returns what they reported.
func (w *ruleWorld) plan(o planOpts) SyncReport {
	w.t.Helper()
	w.st.ReloadTrackerTree()
	items, _, err := collectItems(w.st, w.cfg.GitHub.Repo, false)
	if err != nil {
		w.t.Fatal(err)
	}
	ctx := context.Background()
	var report SyncReport
	cur, statuses, prs, skip, err := updateIssues(ctx, w.st, w.cfg, w.client, items, &report, o.terms, o.public, o.dryRun)
	if err != nil {
		w.t.Fatalf("updateIssues: %v", err)
	}
	if err := linkParentsAndBlockers(ctx, w.st, w.client, items, cur, skip, &report, o.dryRun); err != nil {
		w.t.Fatalf("linkParentsAndBlockers: %v", err)
	}
	if err := placeBoardItems(ctx, w.st, w.cfg, w.client, items, statuses, prs, skip, &report, o.dryRun); err != nil {
		w.t.Fatalf("placeBoardItems: %v", err)
	}
	return report
}

// record reads what's one record.
func (w *ruleWorld) record(what string) githubRecord {
	w.t.Helper()
	return readTestRecord(w.t, treeRecordPath(w.t, w.st, what))
}

func driftHas(drift []DriftLine, what, field string) bool {
	for _, d := range drift {
		if d.What == what && d.Field == field {
			return true
		}
	}
	return false
}

// linkWorld is the links' own fixture: DEMO-1 (no blockers), DEMO-2 in chart
// demo, blocked by DEMO-1 and by DEMO-9, which has no issue.
func linkWorld(t *testing.T) *ruleWorld {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "Blocker"})
	w.ticket("DEMO-2", store.Ticket{Title: "Chart ticket", BlockedBy: []store.TicketBlockedBy{
		{Ticket: "DEMO-1", Kind: "merged"},
		{Ticket: "DEMO-9", Kind: "merged"},
	}})
	w.chart("# Chart: demo\n", "DEMO-2")
	w.adopt("DEMO-1", 1)
	w.adopt("DEMO-2", 2)
	w.adopt("charts/demo", 3)
	return w
}

// TestRuleAtAdoptedRecordWhoseIssueMatchesIsLeftAlone: a record the bridge
// wrote (no synced: key) whose issue and board item already hold what the
// store renders makes no mutation at all; only synced: gets filled, so the
// next sync has a baseline.
func TestRuleAtAdoptedRecordWhoseIssueMatchesIsLeftAlone(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	writeAdoptedRecord(t, w.st, "DEMO-1", "example/tracking", 7, "NODE_ADOPTED", "ITEM_ADOPTED")
	*w.client.issue("NODE_ADOPTED") = fakeIssueState{Title: "First ticket", Body: ticketFooter("DEMO-1"), Open: true}
	w.client.itemFields = map[string]fakeItemFields{"ITEM_ADOPTED": {Status: "Backlog", StoreID: "DEMO-1"}}

	report := w.plan(planOpts{})
	c := w.client
	if len(report.Drift)+len(report.Updated)+len(report.Placed)+len(report.PlacedPRs) != 0 {
		t.Fatalf("report = %+v, want nothing: GitHub already matches", report)
	}
	if len(c.updates)+len(c.closedDone)+len(c.reopened)+len(c.calls)+len(c.placedItems)+len(c.subIssues)+len(c.removedSubs)+len(c.blockedBys)+len(c.removedBlockedBys) != 0 {
		t.Fatalf("client mutated: %+v", c)
	}
	rec := w.record("DEMO-1")
	if rec.Synced == nil || rec.Synced.Title != "First ticket" || rec.Synced.State != "OPEN" {
		t.Fatalf("synced = %+v, want it populated from what GitHub showed", rec.Synced)
	}
}

// TestRuleAtDriftIsAFieldTheLastSyncWroteFoundEdited: title, body and state
// the last sync wrote, found changed on GitHub, are reported as drift and
// written back; the issue is reopened because the store keeps it open.
func TestRuleAtDriftIsAFieldTheLastSyncWroteFoundEdited(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	node := w.adopt("DEMO-1", 1)
	if report := w.plan(planOpts{}); len(report.Drift) != 0 {
		t.Fatalf("first plan Drift = %+v, want none: nothing was written before", report.Drift)
	}
	if rec := w.record("DEMO-1"); rec.Synced == nil || rec.Synced.Title != "First ticket" {
		t.Fatalf("synced after the first plan = %+v, want title populated", rec.Synced)
	}

	edited := w.client.issue(node)
	edited.Title, edited.Body, edited.Open = "Somebody renamed this", "Somebody rewrote this body too", false

	report := w.plan(planOpts{})
	for _, field := range []string{"title", "body", "state"} {
		if !driftHas(report.Drift, "DEMO-1", field) {
			t.Errorf("Drift = %+v, missing %q for DEMO-1", report.Drift, field)
		}
	}
	for _, d := range report.Drift {
		if d.Issue != 1 {
			t.Errorf("drift line %+v names issue %d, want 1", d, d.Issue)
		}
	}
	if edited.Title != "First ticket" || edited.Body != ticketFooter("DEMO-1") || !edited.Open {
		t.Fatalf("issue = %+v, want the store's title and body, reopened", *edited)
	}
	if len(w.client.reopened) != 1 || w.client.reopened[0] != node {
		t.Fatalf("reopened = %v, want DEMO-1's issue once", w.client.reopened)
	}
}

// TestRuleAtLinkDrift: a link the last sync wrote and GitHub lost is put
// back, and a link on GitHub the store does not hold is taken out, both as
// drift.
func TestRuleAtLinkDrift(t *testing.T) {
	const chart = "NODE_charts/demo"
	for _, tc := range []struct {
		name  string
		edit  func(c *fakeClient)
		what  string
		field string
		check func(t *testing.T, c *fakeClient)
	}{
		{
			name: "an extra chart sub-issue is removed",
			edit: func(c *fakeClient) {
				c.subIssuesOf[chart] = append(c.subIssuesOf[chart], "NODE_HAND_ADDED")
				c.parentOf["NODE_HAND_ADDED"] = chart
			},
			what: "charts/demo", field: "sub-issue",
			check: func(t *testing.T, c *fakeClient) {
				if want := []fakeSubIssue{{ParentID: chart, SubIssueID: "NODE_HAND_ADDED"}}; !reflect.DeepEqual(c.removedSubs, want) {
					t.Fatalf("removedSubs = %+v, want %+v", c.removedSubs, want)
				}
			},
		},
		{
			name: "a removed parent link is restored",
			edit: func(c *fakeClient) {
				delete(c.parentOf, "NODE_DEMO-2")
				c.subIssuesOf[chart] = nil
			},
			what: "DEMO-2", field: "parent",
			check: func(t *testing.T, c *fakeClient) {
				if len(c.subIssues) != 2 || c.parentOf["NODE_DEMO-2"] != chart {
					t.Fatalf("subIssues = %+v, parentOf = %v, want the link restored", c.subIssues, c.parentOf)
				}
			},
		},
		{
			name: "a removed blocked-by link is restored",
			edit: func(c *fakeClient) { c.blockedByOf["NODE_DEMO-2"] = nil },
			what: "DEMO-2", field: "blocked_by",
			check: func(t *testing.T, c *fakeClient) {
				if len(c.blockedBys) != 2 || !containsString(c.blockedByOf["NODE_DEMO-2"], "NODE_DEMO-1") {
					t.Fatalf("blockedBys = %+v, blockedByOf = %v, want the edge restored", c.blockedBys, c.blockedByOf)
				}
			},
		},
		{
			name: "a hand-added blocked-by link is removed",
			edit: func(c *fakeClient) { c.blockedByOf["NODE_DEMO-1"] = []string{"NODE_HAND_ADDED"} },
			what: "DEMO-1", field: "blocked_by",
			check: func(t *testing.T, c *fakeClient) {
				if want := []fakeBlockedBy{{IssueID: "NODE_DEMO-1", BlockingID: "NODE_HAND_ADDED"}}; !reflect.DeepEqual(c.removedBlockedBys, want) {
					t.Fatalf("removedBlockedBys = %+v, want %+v", c.removedBlockedBys, want)
				}
				if containsString(c.blockedByOf["NODE_DEMO-1"], "NODE_HAND_ADDED") {
					t.Fatalf("blockedByOf = %v, want the hand-added blocker gone", c.blockedByOf["NODE_DEMO-1"])
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := linkWorld(t)
			w.plan(planOpts{})
			if len(w.client.subIssues) != 1 || len(w.client.blockedBys) != 1 {
				t.Fatalf("first plan: subIssues %+v, blockedBys %+v, want one each", w.client.subIssues, w.client.blockedBys)
			}
			tc.edit(w.client)
			report := w.plan(planOpts{})
			if !driftHas(report.Drift, tc.what, tc.field) {
				t.Fatalf("Drift = %+v, want %s %s", report.Drift, tc.what, tc.field)
			}
			tc.check(t, w.client)
		})
	}
}

// TestRuleAtLinksAreSetOnceAndRecorded: a chart's ticket becomes a sub-issue
// of the chart's issue, a blocker with an issue becomes a native blocked-by
// link, one with none is reported as not linked, the node ids written are kept
// in the ticket's record, and a second plan repeats no mutation.
func TestRuleAtLinksAreSetOnceAndRecorded(t *testing.T) {
	w := linkWorld(t)
	report := w.plan(planOpts{})
	if want := []LinkedParent{{Ticket: "DEMO-2", Chart: "demo"}}; !reflect.DeepEqual(report.LinkedParents, want) {
		t.Fatalf("LinkedParents = %+v, want %+v", report.LinkedParents, want)
	}
	if want := []LinkedBlockedBy{{Ticket: "DEMO-2", Blocker: "DEMO-1"}}; !reflect.DeepEqual(report.LinkedBlockedBy, want) {
		t.Fatalf("LinkedBlockedBy = %+v, want %+v", report.LinkedBlockedBy, want)
	}
	if want := []NotLinked{{Ticket: "DEMO-2", Blocker: "DEMO-9"}}; !reflect.DeepEqual(report.NotLinked, want) {
		t.Fatalf("NotLinked = %+v, want %+v", report.NotLinked, want)
	}
	if want := []fakeSubIssue{{ParentID: "NODE_charts/demo", SubIssueID: "NODE_DEMO-2"}}; !reflect.DeepEqual(w.client.subIssues, want) {
		t.Fatalf("subIssues = %+v, want %+v", w.client.subIssues, want)
	}
	if want := []fakeBlockedBy{{IssueID: "NODE_DEMO-2", BlockingID: "NODE_DEMO-1"}}; !reflect.DeepEqual(w.client.blockedBys, want) {
		t.Fatalf("blockedBys = %+v, want %+v", w.client.blockedBys, want)
	}
	rec := w.record("DEMO-2")
	if rec.Synced == nil || rec.Synced.Links.Parent != "NODE_charts/demo" || !reflect.DeepEqual(rec.Synced.Links.BlockedBy, []string{"NODE_DEMO-1"}) {
		t.Fatalf("record = %+v, want synced.links naming the chart's issue and DEMO-1's", rec)
	}

	again := w.plan(planOpts{})
	if len(again.LinkedParents)+len(again.LinkedBlockedBy) != 0 || len(w.client.subIssues) != 1 || len(w.client.blockedBys) != 1 {
		t.Fatalf("second plan linked %+v / %+v (client: %d, %d), want nothing repeated", again.LinkedParents, again.LinkedBlockedBy, len(w.client.subIssues), len(w.client.blockedBys))
	}
	if len(again.NotLinked) != 1 {
		t.Fatalf("second plan NotLinked = %+v, want DEMO-9 reported again", again.NotLinked)
	}
}

// TestRuleAtBlockedByThroughAnAliasIsLinkedUnderTheCurrentID: a blocked_by
// ref naming an alias gets its native link, reported under the blocker's
// current id, and the "Waits for" line names the current id too.
func TestRuleAtBlockedByThroughAnAliasIsLinkedUnderTheCurrentID(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "Blocker", Aliases: []string{"DEMO-0"}})
	w.ticket("DEMO-2", store.Ticket{Title: "Waiting ticket", BlockedBy: []store.TicketBlockedBy{{Ticket: "DEMO-0", Kind: "merged"}}})
	w.adopt("DEMO-1", 1)
	w.adopt("DEMO-2", 2)

	report := w.plan(planOpts{})
	if want := []LinkedBlockedBy{{Ticket: "DEMO-2", Blocker: "DEMO-1"}}; !reflect.DeepEqual(report.LinkedBlockedBy, want) {
		t.Fatalf("LinkedBlockedBy = %+v, want %+v", report.LinkedBlockedBy, want)
	}
	if want := []fakeBlockedBy{{IssueID: "NODE_DEMO-2", BlockingID: "NODE_DEMO-1"}}; !reflect.DeepEqual(w.client.blockedBys, want) {
		t.Fatalf("blockedBys = %+v, want %+v", w.client.blockedBys, want)
	}
	ticket, err := w.st.ReadTicket("DEMO-2")
	if err != nil {
		t.Fatal(err)
	}
	line, err := waitsForLine(w.st, ticket.BlockedBy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "**Waits for:** DEMO-1 ") {
		t.Fatalf("waitsForLine = %q, want the current id DEMO-1, not the alias", line)
	}
}

// TestRuleAtDryRunPreviewsLinksAndWritesNone: a dry run over records with no
// links recorded, against a view that holds hand-added links, previews the new
// parent link, the new blocked-by link, the unlinked blocker and both removal
// drifts, and calls no link mutation nor writes a record.
func TestRuleAtDryRunPreviewsLinksAndWritesNone(t *testing.T) {
	w := linkWorld(t)
	w.client.subIssuesOf = map[string][]string{"NODE_charts/demo": {"NODE_HAND_ADDED"}}
	w.client.parentOf = map[string]string{"NODE_HAND_ADDED": "NODE_charts/demo"}
	w.client.blockedByOf = map[string][]string{"NODE_DEMO-2": {"NODE_HAND_ADDED_BLOCKER"}}

	report := w.plan(planOpts{dryRun: true})
	if want := []LinkedParent{{Ticket: "DEMO-2", Chart: "demo"}}; !reflect.DeepEqual(report.LinkedParents, want) {
		t.Fatalf("LinkedParents = %+v, want %+v", report.LinkedParents, want)
	}
	if want := []LinkedBlockedBy{{Ticket: "DEMO-2", Blocker: "DEMO-1"}}; !reflect.DeepEqual(report.LinkedBlockedBy, want) {
		t.Fatalf("LinkedBlockedBy = %+v, want %+v", report.LinkedBlockedBy, want)
	}
	if want := []NotLinked{{Ticket: "DEMO-2", Blocker: "DEMO-9"}}; !reflect.DeepEqual(report.NotLinked, want) {
		t.Fatalf("NotLinked = %+v, want %+v", report.NotLinked, want)
	}
	if !driftHas(report.Drift, "charts/demo", "sub-issue") || !driftHas(report.Drift, "DEMO-2", "blocked_by") {
		t.Fatalf("Drift = %+v, want the sub-issue and blocked_by removals previewed", report.Drift)
	}
	c := w.client
	if len(c.subIssues)+len(c.blockedBys)+len(c.removedSubs)+len(c.removedBlockedBys)+len(c.updates)+len(c.placedItems) != 0 {
		t.Fatalf("dry run mutated: %+v", c)
	}
	for _, what := range []string{"DEMO-1", "DEMO-2", "charts/demo"} {
		if rec := w.record(what); rec.Synced != nil {
			t.Fatalf("%s's record = %+v, want synced unset: a dry run writes nothing", what, rec)
		}
	}
}

// TestRuleAtDryRunPreviewsAgainstExistingRecords: a dry run over a record
// whose issue and board item were edited since the last plan reports the
// update, its drift and the placement, and neither mutates the client nor
// writes the record.
func TestRuleAtDryRunPreviewsAgainstExistingRecords(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "A clean title"})
	node := w.adopt("DEMO-1", 1)
	w.plan(planOpts{})
	rec := w.record("DEMO-1")
	before, err := os.ReadFile(treeRecordPath(t, w.st, "DEMO-1"))
	if err != nil {
		t.Fatal(err)
	}
	w.client.issue(node).Title = "Edited on GitHub"
	w.client.itemFields[rec.Item] = fakeItemFields{Status: "Done", StoreID: w.client.itemFields[rec.Item].StoreID}
	calls, updates, placed := len(w.client.calls), len(w.client.updates), len(w.client.placedItems)

	report := w.plan(planOpts{dryRun: true})
	if len(report.Updated) != 1 || report.Updated[0].What != "DEMO-1" {
		t.Fatalf("Updated = %+v, want DEMO-1 previewed (title)", report.Updated)
	}
	if !driftHas(report.Drift, "DEMO-1", "title") || !driftHas(report.Drift, "DEMO-1", "status") {
		t.Fatalf("Drift = %+v, want a title and a status line", report.Drift)
	}
	if len(report.Placed) != 1 || report.Placed[0].What != "DEMO-1" {
		t.Fatalf("Placed = %+v, want DEMO-1 previewed as placed", report.Placed)
	}
	if len(w.client.calls) != calls || len(w.client.updates) != updates || len(w.client.placedItems) != placed {
		t.Fatalf("dry run mutated the client")
	}
	after, err := os.ReadFile(treeRecordPath(t, w.st, "DEMO-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("dry run wrote the record:\nbefore: %s\nafter: %s", before, after)
	}
}

// TestRuleAtBoardDriftIsStatusOrStoreIDEditedOnGitHub: a board item's Status
// or Store ID edited on GitHub is reported as drift and written back.
func TestRuleAtBoardDriftIsStatusOrStoreIDEditedOnGitHub(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	w.adopt("DEMO-1", 1)
	w.plan(planOpts{})
	rec := w.record("DEMO-1")
	if rec.Item == "" {
		t.Fatalf("record = %+v, want an item id after the first plan", rec)
	}
	w.client.itemFields[rec.Item] = fakeItemFields{Status: "In review", StoreID: "SOMETHING-ELSE"}

	report := w.plan(planOpts{})
	for _, field := range []string{"status", "store_id"} {
		if !driftHas(report.Drift, "DEMO-1", field) {
			t.Errorf("Drift = %+v, missing %q for DEMO-1", report.Drift, field)
		}
	}
	last := w.client.placedItems[len(w.client.placedItems)-1]
	if last.Status != "Backlog" || last.StoreID != "DEMO-1" {
		t.Fatalf("last PlaceItem = %+v, want the store's Status and Store ID", last)
	}
}

// TestRuleAtNoDriftWhereGitHubHoldsWhatWouldBeWritten: an edit on GitHub that
// is already the store's value overwrites nothing, so it is not drift, and
// synced: records the value; a field synced: never held is a first write, not
// drift either.
func TestRuleAtNoDriftWhereGitHubHoldsWhatWouldBeWritten(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(w *ruleWorld, rec *githubRecord, node string)
		check func(t *testing.T, w *ruleWorld, got githubRecord)
	}{
		{
			name: "GitHub closed the issue and set Done before the store's closure",
			edit: func(w *ruleWorld, rec *githubRecord, node string) {
				is := w.client.issue(node)
				is.Open, is.Reason = false, "completed"
				w.client.itemFields[rec.Item] = fakeItemFields{Status: "Done", StoreID: "DEMO-1"}
				closeRuleTicket(t, w.st, "DEMO-1", store.CloseCovered, "example/product#82", "Shipped.")
			},
			check: func(t *testing.T, w *ruleWorld, got githubRecord) {
				if len(w.client.closedDone)+len(w.client.closed)+len(w.client.reopened) != 0 {
					t.Errorf("an open-state write was sent for a value GitHub already holds")
				}
				if got.Synced == nil || got.Synced.State != "CLOSED" || got.Synced.Status != "Done" || got.Synced.StoreID != "DEMO-1" {
					t.Errorf("synced = %+v, want CLOSED / Done / DEMO-1", got.Synced)
				}
			},
		},
		{
			name: "the Store ID on GitHub is the store's but not synced's",
			edit: func(w *ruleWorld, rec *githubRecord, node string) {
				rec.Synced.StoreID = "DEMO-OLD"
				rewriteTestRecord(t, w.st, "DEMO-1", *rec)
			},
		},
		{
			name: "synced holds no Status or Store ID to compare",
			edit: func(w *ruleWorld, rec *githubRecord, node string) {
				rec.Synced.Status, rec.Synced.StoreID = "", ""
				rewriteTestRecord(t, w.st, "DEMO-1", *rec)
				w.client.itemFields[rec.Item] = fakeItemFields{Status: "In progress", StoreID: "SOMEONE-ELSES"}
			},
			check: func(t *testing.T, w *ruleWorld, got githubRecord) {
				if got.Synced == nil || got.Synced.Status != "Backlog" || got.Synced.StoreID != "DEMO-1" {
					t.Errorf("synced = %+v, want Backlog / DEMO-1 recorded", got.Synced)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newRuleWorld(t, false)
			w.ticket("DEMO-1", store.Ticket{Title: "Covered by other work"})
			node := w.adopt("DEMO-1", 1)
			w.plan(planOpts{})
			rec := w.record("DEMO-1")
			tc.edit(w, &rec, node)
			placed := len(w.client.placedItems)

			report := w.plan(planOpts{})
			if len(report.Drift) != 0 {
				t.Errorf("Drift = %+v, want none", report.Drift)
			}
			if tc.name == "GitHub closed the issue and set Done before the store's closure" && len(w.client.placedItems) != placed {
				t.Errorf("a board write was sent for a value GitHub already holds")
			}
			if tc.check != nil {
				tc.check(t, w, w.record("DEMO-1"))
			}
		})
	}
}

// TestRuleAtAnIssueGoneFromGitHubIsRecreatedUnderTheNewNumber: a recorded
// issue GitHub no longer has gets a new issue, and the record moves with it:
// the tree holds one record, named for the new number.
func TestRuleAtAnIssueGoneFromGitHubIsRecreatedUnderTheNewNumber(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	node := w.adopt("DEMO-1", 50)
	w.plan(planOpts{})
	old := w.record("DEMO-1")
	delete(w.client.issues, node)
	w.client.goneNodeIDs = map[string]bool{node: true}

	report := w.plan(planOpts{})
	if len(report.Recreated) != 1 || report.Recreated[0].What != "DEMO-1" {
		t.Fatalf("Recreated = %+v, want DEMO-1", report.Recreated)
	}
	recs := treeRecords(t, w.st, "DEMO-1")
	if len(recs) != 1 {
		t.Fatalf("DEMO-1 has %d records after the recreation, want 1", len(recs))
	}
	fresh := readTestRecord(t, w.st.TrackerRecordPath(recs[0].Entry))
	if fresh.Issue == old.Issue || recs[0].Entry.ID != itoa(fresh.Issue) {
		t.Fatalf("record %v holds issue %d (was %d), want it named for the new issue", recs[0].Entry, fresh.Issue, old.Issue)
	}
	if _, err := os.Stat(w.st.TrackerRecordPath(githubEntry(old.Repo, old.Issue))); !os.IsNotExist(err) {
		t.Fatalf("the old record is still there (stat err %v)", err)
	}
	if _, err := os.Lstat(w.st.TrackerPath(githubEntry(old.Repo, old.Issue))); !os.IsNotExist(err) {
		t.Fatalf("the old entry folder is still there (lstat err %v)", err)
	}
}

// TestRuleAtARecordedPullRequestWithNoNodeIDIsNeverLookedUpByID: a prs: entry
// with no node_id (hand-edited, or from a bridge that omitted it) must not
// reach PullRequestsByIDs as an empty id, which GitHub refuses outright; the
// plan succeeds and the entry stays listed in the body and in the record, as a
// plan never deletes store data it cannot check.
func TestRuleAtARecordedPullRequestWithNoNodeIDIsNeverLookedUpByID(t *testing.T) {
	w := newRuleWorld(t, true)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	abs := w.st.TrackerRecordPath(githubEntry("example/tracking", 7))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	body := recordHeader + "ticket: DEMO-1\nrepo: example/tracking\nissue: 7\nnode_id: NODE_1\nprs:\n  - repo: example/demo\n    number: 31\n    node_id: \"\"\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	*w.client.issue("NODE_1") = fakeIssueState{Title: "First ticket", Body: ticketFooter("DEMO-1"), Open: true}

	w.plan(planOpts{})
	if got := w.client.issue("NODE_1").Body; !strings.Contains(got, "example/demo#31") {
		t.Fatalf("issue body = %q, want it still listing example/demo#31", got)
	}
	found := false
	for _, p := range w.record("DEMO-1").PRs {
		found = found || (p.Repo == "example/demo" && p.Number == 31)
	}
	if !found {
		t.Fatalf("record's prs: = %+v, want example/demo#31 still there", w.record("DEMO-1").PRs)
	}
}

var closedAt = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

// closeRuleTicket closes ticket the way `jig close` records it.
func closeRuleTicket(t *testing.T, st *store.Store, ticket, reason, by, note string) {
	t.Helper()
	if err := st.CloseTicket(ticket, store.NewClosed(reason, by, note, closedAt)); err != nil {
		t.Fatal(err)
	}
}

// TestRuleAtAClosedTicketClosesItsIssueAndReopensWithIt: a covered ticket's
// issue is closed as completed (Done), a superseded or not-planned one as not
// planned (Not planned), how it was closed is written into the body (with the
// superseding ticket's issue named), nothing is written again once the issue
// holds it, and a reopened ticket gets its issue back open under the Status
// its state derives.
func TestRuleAtAClosedTicketClosesItsIssueAndReopensWithIt(t *testing.T) {
	w := newRuleWorld(t, false)
	titles := map[string]string{"DEMO-1": "Covered by other work", "DEMO-2": "Replaced by another", "DEMO-3": "Decided against", "DEMO-4": "Still to build"}
	nodes := map[string]string{}
	for i, id := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4"} {
		w.ticket(id, store.Ticket{Title: titles[id]})
		nodes[id] = w.adopt(id, i+1)
	}
	w.plan(planOpts{})
	for id, node := range nodes {
		if !w.client.issue(node).Open {
			t.Fatalf("%s has a closed issue before anything was closed", id)
		}
	}

	closeRuleTicket(t, w.st, "DEMO-1", store.CloseCovered, "example/product#82", "The demo kit shipped it.\nNothing is left.")
	closeRuleTicket(t, w.st, "DEMO-2", store.CloseSuperseded, "DEMO-4", "")
	closeRuleTicket(t, w.st, "DEMO-3", store.CloseNotPlanned, "", "We record demos from the e2e runs instead.")

	report := w.plan(planOpts{})
	c := w.client
	if want := []string{nodes["DEMO-1"]}; !reflect.DeepEqual(c.closedDone, want) {
		t.Errorf("closed as completed = %v, want %v", c.closedDone, want)
	}
	if want := []string{nodes["DEMO-2"], nodes["DEMO-3"]}; !reflect.DeepEqual(c.closed, want) {
		t.Errorf("closed as not planned = %v, want %v", c.closed, want)
	}
	for id, want := range map[string]string{"DEMO-1": "completed", "DEMO-2": "not planned", "DEMO-3": "not planned"} {
		if is := c.issue(nodes[id]); is.Open || is.Reason != want {
			t.Errorf("%s: issue open %v, closed as %q, want closed as %q", id, is.Open, is.Reason, want)
		}
	}
	if !c.issue(nodes["DEMO-4"]).Open {
		t.Errorf("DEMO-4 is open in the store and its issue was closed")
	}
	if len(report.Drift) != 0 {
		t.Errorf("drift = %+v, want none: the store says closed, so the mirror agrees", report.Drift)
	}
	placed := map[string]string{}
	for _, p := range c.placedItems {
		placed[p.StoreID] = p.Status
	}
	for id, want := range map[string]string{"DEMO-1": "Done", "DEMO-2": "Not planned", "DEMO-3": "Not planned", "DEMO-4": "Backlog"} {
		if placed[id] != want {
			t.Errorf("%s placed under %q, want %q", id, placed[id], want)
		}
	}
	body := func(id string) string { return c.issue(nodes[id]).Body }
	for id, want := range map[string]string{
		"DEMO-1": "**Closed:** covered by example/product#82\n\nThe demo kit shipped it. Nothing is left.",
		"DEMO-2": "**Closed:** superseded by DEMO-4 (#4)",
		"DEMO-3": "**Closed:** not planned\n\nWe record demos from the e2e runs instead.",
	} {
		if !strings.Contains(body(id), want) || !strings.HasSuffix(body(id), ticketFooter(id)) {
			t.Errorf("%s body lacks %q or its footer:\n%s", id, want, body(id))
		}
	}
	if strings.Contains(body("DEMO-4"), "Closed:") {
		t.Errorf("DEMO-4 is open and its body says it is closed:\n%s", body("DEMO-4"))
	}

	closedBefore, notPlannedBefore, updatesBefore, placedBefore := len(c.closedDone), len(c.closed), len(c.updates), len(c.placedItems)
	report = w.plan(planOpts{})
	if len(c.closedDone) != closedBefore || len(c.closed) != notPlannedBefore || len(c.reopened) != 0 || len(c.updates) != updatesBefore || len(c.placedItems) != placedBefore {
		t.Errorf("a plan of closed tickets whose issues are closed wrote again")
	}
	if len(report.Drift)+len(report.Updated)+len(report.Placed) != 0 {
		t.Errorf("third plan reported %+v, want nothing", report)
	}

	if err := w.st.ReopenTicket("DEMO-3"); err != nil {
		t.Fatal(err)
	}
	w.plan(planOpts{})
	if want := []string{nodes["DEMO-3"]}; !reflect.DeepEqual(c.reopened, want) {
		t.Errorf("reopened = %v, want %v", c.reopened, want)
	}
	if is := c.issue(nodes["DEMO-3"]); !is.Open || strings.Contains(is.Body, "Closed:") {
		t.Errorf("reopened DEMO-3: open %v, body\n%s", is.Open, is.Body)
	}
	if last := c.placedItems[len(c.placedItems)-1]; last.StoreID != "DEMO-3" || last.Status != "Backlog" {
		t.Errorf("the reopened ticket was placed as %+v, want Backlog", last)
	}
}

// TestRuleAtUpdateScansOnlyAPublicRepoOrADryRun: the rendered title and body
// are scanned before any write when the issue repo is public or the run is a
// dry run, never for a private repo in a live run. A hit skips that item (and
// the chart's link writes with it): no update, no record change, no board
// placement and no new sub-issue, while the clean items sync as usual.
func TestRuleAtUpdateScansOnlyAPublicRepoOrADryRun(t *testing.T) {
	hit := "Contact " + publishSafetyTestEmail + " for this"
	for _, tc := range []struct {
		name string
		opts planOpts
		skip bool
	}{
		{"public repo", planOpts{public: true}, true},
		{"private repo, live", planOpts{}, false},
		{"private repo, dry run", planOpts{dryRun: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newRuleWorld(t, false)
			w.ticket("DEMO-1", store.Ticket{Title: "A clean title"})
			w.ticket("DEMO-2", store.Ticket{Title: "Another clean title"})
			w.ticket("DEMO-3", store.Ticket{Title: "Chart ticket"})
			w.ticket("DEMO-4", store.Ticket{Title: "Another chart ticket"})
			w.chart("# Chart: demo\n", "DEMO-3")
			for i, what := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4", "charts/demo"} {
				w.adopt(what, i+1)
			}
			w.plan(planOpts{})
			if len(w.client.subIssues) != 1 || w.client.subIssues[0].SubIssueID != "NODE_DEMO-3" {
				t.Fatalf("subIssues after the baseline = %+v, want DEMO-3 under the chart", w.client.subIssues)
			}

			ticketYAML := "schema_version: 1\ntitle: A clean title\nbody: |\n  " + hit + "\n"
			if err := os.WriteFile(filepath.Join(w.st.TicketDir("DEMO-1"), "ticket.yaml"), []byte(ticketYAML), 0o644); err != nil {
				t.Fatal(err)
			}
			w.chart("# Chart: demo\n\n"+hit+"\n", "DEMO-3", "DEMO-4")
			before, err := os.ReadFile(treeRecordPath(t, w.st, "DEMO-1"))
			if err != nil {
				t.Fatal(err)
			}
			updates, placed, subs := len(w.client.updates), len(w.client.placedItems), len(w.client.subIssues)

			report := w.plan(tc.opts)
			if !tc.skip {
				if len(report.Skipped) != 0 {
					t.Fatalf("Skipped = %+v, want none: a private issue repo is not scanned", report.Skipped)
				}
				if len(w.client.updates) == updates {
					t.Fatalf("no UpdateIssue call, want the unscanned edit written")
				}
				return
			}
			got := map[string]string{}
			for _, s := range report.Skipped {
				got[s.What] = s.Text
			}
			if len(got) != 2 || !strings.Contains(got["DEMO-1"], publishSafetyTestEmail) || !strings.Contains(got["charts/demo"], publishSafetyTestEmail) {
				t.Fatalf("Skipped = %+v, want DEMO-1 and charts/demo, each carrying the hit", report.Skipped)
			}
			if tc.opts.dryRun {
				return
			}
			if len(w.client.updates) != updates || len(w.client.placedItems) != placed || len(w.client.subIssues) != subs {
				t.Fatalf("a skipped item was written: updates %d->%d, placed %d->%d, subIssues %d->%d (DEMO-4 must not join a skipped chart)",
					updates, len(w.client.updates), placed, len(w.client.placedItems), subs, len(w.client.subIssues))
			}
			after, err := os.ReadFile(treeRecordPath(t, w.st, "DEMO-1"))
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("DEMO-1's record changed on the hit:\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

// TestRuleAtTheCreateScanSeesTheFullRender: a new item's hit can live in its
// description or in a pull request's repo, which the footer-only body a create
// writes never carries; scanning previewCreateRender's full render is what
// makes a create agree with the update that follows it.
func TestRuleAtTheCreateScanSeesTheFullRender(t *testing.T) {
	w := newRuleWorld(t, true)
	w.ticket("DEMO-1", store.Ticket{Title: "A clean title", Body: "Contact " + publishSafetyTestEmail + " for this"})
	w.ticket("DEMO-2", store.Ticket{Title: "Another clean title", Body: "A clean description"})
	items, _, err := collectItems(w.st, "example/tracking", false)
	if err != nil {
		t.Fatal(err)
	}
	hits := func(title, body string, terms []string) bool { return len(ScanTitleAndBody(title, body, terms)) > 0 }

	title, footerOnly, err := items[0].render(w.st)
	if err != nil {
		t.Fatal(err)
	}
	if hits(title, footerOnly, nil) {
		t.Fatalf("the footer-only create body %q hits, want the description's hit invisible to it", footerOnly)
	}
	title, full, err := previewCreateRender(w.st, w.cfg, items[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hits(title, full, nil) {
		t.Fatalf("the full render %q does not hit, want the description's hit seen", full)
	}

	prs := []PRRef{{Owner: "example", Repo: "demo", Number: 5, NodeID: "PR_5", State: "OPEN"}}
	title, withPR, err := previewCreateRender(w.st, w.cfg, items[1], prs)
	if err != nil {
		t.Fatal(err)
	}
	if !hits(title, withPR, []string{"example/demo"}) {
		t.Fatalf("the render with a pull request %q does not hit a term matching its repo", withPR)
	}
	title, noPR, err := previewCreateRender(w.st, w.cfg, items[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits(title, noPR, []string{"example/demo"}) {
		t.Fatalf("the render with no pull request %q hits, want it clean", noPR)
	}
}

// TestRuleAtACreateOnAHitIsRefusedLiveAndInADryRun: a new item whose title,
// description or pull request trips the scan is skipped by a dry run and by a
// live sync alike - reported in Skipped, never in Created, no CreateIssue call
// and no record - and publish-terms.txt is read from Deps.Home. (An item that
// trips nothing is created through a remote: the chain tests own that.)
func TestRuleAtACreateOnAHitIsRefusedLiveAndInADryRun(t *testing.T) {
	email := "Contact " + publishSafetyTestEmail + " for this"
	for _, tc := range []struct {
		name  string
		tk    store.Ticket
		terms string
		prs   map[string][]github.PullRequest
	}{
		{name: "title", tk: store.Ticket{Title: email}},
		{name: "description", tk: store.Ticket{Title: "A clean title", Body: email}},
		{name: "publish-terms.txt term, case-insensitive", tk: store.Ticket{Title: "The CodeName launch"}, terms: "codename\n"},
		{
			name:  "pull request repo",
			tk:    store.Ticket{Title: "A clean title", Body: "A clean description"},
			terms: "example/demo\n",
			// The create-time scan runs before the issue exists, so the
			// ticket's branch is still led by its store id.
			prs: map[string][]github.PullRequest{"example/demo@DEMO-1-a-clean-title": {{Number: 5, NodeID: "PR_5", State: "OPEN"}}},
		},
	} {
		for _, dry := range []bool{true, false} {
			name := tc.name + ", live"
			if dry {
				name = tc.name + ", dry run"
			}
			t.Run(name, func(t *testing.T) {
				w := newRuleWorld(t, tc.prs != nil)
				w.client.prs = tc.prs
				w.ticket("DEMO-1", tc.tk)
				home := t.TempDir()
				if tc.terms != "" {
					if err := os.WriteFile(filepath.Join(home, "publish-terms.txt"), []byte(tc.terms), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				report, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client, Home: home}, SyncOpts{DryRun: dry})
				if err != nil {
					t.Fatalf("Sync: %v", err)
				}
				if len(report.Skipped) != 1 || report.Skipped[0].What != "DEMO-1" || len(report.Created) != 0 {
					t.Fatalf("Skipped = %+v, Created = %+v, want DEMO-1 skipped and nothing created", report.Skipped, report.Created)
				}
				if len(w.client.calls) != 0 || hasTreeRecord(t, w.st, "DEMO-1") {
					t.Fatalf("CreateIssue calls %+v, record %v: a hit must write nothing", w.client.calls, hasTreeRecord(t, w.st, "DEMO-1"))
				}
			})
		}
	}
}

// TestRuleAtADryRunNeedsNoClientAndWritesNothing: a dry run over a store with
// no record reads no GitHub, reports one would-create entry with no number and
// writes no record.
func TestRuleAtADryRunNeedsNoClientAndWritesNothing(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	report, err := Sync(Deps{Store: w.st, Cfg: w.cfg}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}
	if len(report.Created) != 1 || report.Created[0].What != "DEMO-1" || report.Created[0].Number != 0 {
		t.Fatalf("Created = %+v, want one would-create entry for DEMO-1 with no number", report.Created)
	}
	if hasTreeRecord(t, w.st, "DEMO-1") {
		t.Fatalf("--dry-run wrote a record")
	}
}

// TestRuleAtAClientFailureNamesTheItemAndTheBoundPointerIsOnlyForABoundedSync:
// a GitHub failure comes back as a plain error naming the item it happened on;
// only a sync that ran under its own bound points at `jig trackers sync`.
func TestRuleAtAClientFailureNamesTheItemAndTheBoundPointerIsOnlyForABoundedSync(t *testing.T) {
	for _, unbounded := range []bool{false, true} {
		w := newRuleWorld(t, false)
		w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
		w.client.err = context.DeadlineExceeded
		_, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client}, SyncOpts{Unbounded: unbounded})
		if err == nil || !strings.Contains(err.Error(), "DEMO-1") {
			t.Fatalf("unbounded=%v: err = %v, want it to name DEMO-1", unbounded, err)
		}
		if got := strings.Contains(err.Error(), "jig trackers sync"); got == unbounded {
			t.Fatalf("unbounded=%v: err = %v, want the `jig trackers sync` pointer only on a bounded sync", unbounded, err)
		}
	}
}

// legacyWorld is a world whose DEMO-1, DEMO-2 and chart demo hold their
// records in their own folders, as a build from before the tracker tree kept
// them, with each issue's state on GitHub already the store's.
func legacyWorld(t *testing.T) *ruleWorld {
	t.Helper()
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	w.ticket("DEMO-2", store.Ticket{Title: "Second ticket"})
	w.chart("# Chart: demo\n", "DEMO-2")
	writeLegacyRecord(t, w.st, "DEMO-1", "example/tracking", 11, "NODE_A", "ITEM_A")
	writeLegacyRecord(t, w.st, "DEMO-2", "example/tracking", 12, "NODE_B", "")
	writeLegacyRecord(t, w.st, "charts/demo", "example/tracking", 13, "NODE_C", "")
	return w
}

// TestRuleAtRecordsInTheirOwnFoldersMoveIntoTheTree: a record kept in a
// ticket's or chart's own folder moves into the tracker tree, naming its owner
// and keeping every key it had, before any issue is looked for, so the issue
// is updated in place; a dry run reads the records where they are and moves
// none.
func TestRuleAtRecordsInTheirOwnFoldersMoveIntoTheTree(t *testing.T) {
	t.Run("a sync moves them", func(t *testing.T) {
		w := legacyWorld(t)
		items, _, err := collectItems(w.st, "example/tracking", false)
		if err != nil {
			t.Fatal(err)
		}
		moved, err := moveLegacyRecords(w.st, items, "example/tracking")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range moved {
			got = append(got, m.What+" "+m.From+" -> "+m.To)
		}
		want := []string{
			"DEMO-1 tickets/DEMO-1/tracker/github.yaml -> trackers/github/example/tracking/11/record.yaml",
			"DEMO-2 tickets/DEMO-2/tracker/github.yaml -> trackers/github/example/tracking/12/record.yaml",
			"charts/demo charts/demo/github.yaml -> trackers/github/example/tracking/13/record.yaml",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("moved = %v, want %v", got, want)
		}
		for what, issue := range map[string]int{"DEMO-1": 11, "DEMO-2": 12, "charts/demo": 13} {
			if _, err := os.Stat(legacyRecordPath(w.st, what)); !os.IsNotExist(err) {
				t.Errorf("%s: the record is still in its old place (stat err %v)", what, err)
			}
			abs := treeRecordPath(t, w.st, what)
			data, _ := os.ReadFile(abs)
			key := "ticket: " + what
			if strings.HasPrefix(what, "charts/") {
				key = "chart: demo"
			}
			if !strings.HasPrefix(string(data), recordHeader+key+"\nrepo: example/tracking\nissue: "+itoa(issue)+"\n") {
				t.Errorf("%s's record =\n%s\nwant the header, %q, then its own keys", what, data, key)
			}
		}
		if rec := w.record("DEMO-1"); rec.Item != "ITEM_A" {
			t.Errorf("DEMO-1's record = %+v, want its item kept", rec)
		}
		report := w.plan(planOpts{})
		if len(w.client.calls) != 0 || len(report.Created) != 0 {
			t.Errorf("CreateIssue calls %+v: every issue is updated in place", w.client.calls)
		}
		if rec := w.record("DEMO-2"); rec.Issue != 12 || rec.Synced == nil || rec.Synced.Title == "" {
			t.Errorf("DEMO-2's record = %+v, want issue 12 and what the plan wrote", rec)
		}
	})

	t.Run("a dry run moves none", func(t *testing.T) {
		w := newRuleWorld(t, false)
		w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
		writeLegacyRecord(t, w.st, "DEMO-1", "example/tracking", 11, "NODE_A", "")
		*w.client.issue("NODE_A") = fakeIssueState{Title: "First ticket", Body: ticketFooter("DEMO-1"), Open: true}
		report, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client}, SyncOpts{DryRun: true})
		if err != nil {
			t.Fatalf("Sync --dry-run: %v", err)
		}
		if len(report.Created) != 0 || len(report.MovedRecords) != 0 {
			t.Errorf("Created %+v, MovedRecords %+v; want none of either", report.Created, report.MovedRecords)
		}
		if _, err := os.Stat(legacyRecordPath(w.st, "DEMO-1")); err != nil {
			t.Errorf("the old record is gone after a dry run: %v", err)
		}
		if _, err := os.Stat(filepath.Join(w.st.Root, "trackers")); !os.IsNotExist(err) {
			t.Errorf("a dry run made trackers/ (stat err %v)", err)
		}
	})

	t.Run("a store.yaml store", func(t *testing.T) {
		w := legacyWorld(t)
		storeYAML := "schema_version: 3\nkeys:\n  DEMO: everything in demo\ntrackers:\n  - github:\n      repo: example/tracking\n      project: https://github.com/users/example/projects/7\nplatform: platform/\nprojects:\n  - demo\n"
		if err := os.Remove(filepath.Join(w.st.Root, "project.yaml")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.st.Root, "store.yaml"), []byte(storeYAML), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := project.Load(filepath.Join(w.st.Root, "store.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		w.cfg = cfg
		report, err := Sync(Deps{Store: w.st, Cfg: cfg, Client: w.client}, SyncOpts{})
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if len(report.MovedRecords) != 3 || len(w.client.calls) != 0 {
			t.Fatalf("MovedRecords %+v, CreateIssue calls %+v; want three moves and no create", report.MovedRecords, w.client.calls)
		}
		if problems, err := w.st.CheckTrackerTree(); err != nil || len(problems) != 0 {
			t.Errorf("CheckTrackerTree = %+v, %v; want nothing wrong", problems, err)
		}
	})
}

// TestRuleAtARecordOfAnotherRepoIsRefusedBeforeAnythingMoves: the repo check
// that stops a sync of a store whose trackers: entry changed repo runs before
// any record moves.
func TestRuleAtARecordOfAnotherRepoIsRefusedBeforeAnythingMoves(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	w.ticket("DEMO-2", store.Ticket{Title: "Second ticket"})
	writeLegacyRecord(t, w.st, "DEMO-1", "example/tracking", 11, "NODE_A", "")
	writeLegacyRecord(t, w.st, "DEMO-2", "someone/else", 12, "NODE_B", "")

	_, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client}, SyncOpts{})
	if err == nil || !strings.Contains(err.Error(), "DEMO-2's record names someone/else") {
		t.Fatalf("Sync = %v, want MIRROR_REPO_CHANGED naming DEMO-2", err)
	}
	if _, err := os.Stat(filepath.Join(w.st.Root, "trackers")); !os.IsNotExist(err) {
		t.Errorf("a refused sync moved a record (stat err %v)", err)
	}
	if _, err := os.Stat(legacyRecordPath(w.st, "DEMO-1")); err != nil {
		t.Errorf("DEMO-1's old record is gone after a refused sync: %v", err)
	}
}

// TestRuleAtARecordThatCannotBeReadRefusesTheSync: a record that cannot be
// decoded names no ticket, so the sync would take its ticket for one with no
// issue and open a second; it stops, naming the file.
func TestRuleAtARecordThatCannotBeReadRefusesTheSync(t *testing.T) {
	w := newRuleWorld(t, false)
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	broken := w.st.TrackerRecordPath(githubEntry("example/tracking", 5))
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("ticket: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client}, SyncOpts{})
	if err == nil || !strings.Contains(err.Error(), "trackers/github/example/tracking/5/record.yaml could not be read") {
		t.Fatalf("Sync = %v, want a refusal naming the record", err)
	}
	if len(w.client.calls) != 0 {
		t.Errorf("CreateIssue calls = %+v, want none: no second issue", w.client.calls)
	}
}

// treeWorld is a world kept in a directory git knows (no remote), whose clone
// makes symlinks or not, with DEMO-1, DEMO-2 and chart demo adopted.
func treeWorld(t *testing.T, symlinks bool) *ruleWorld {
	t.Helper()
	w := newRuleWorld(t, false)
	runGit(t, w.st.Root, "init", "-b", "main")
	if symlinks {
		if !enableSymlinks(t, w.st.Root) {
			t.Skip("this machine cannot make a symlink")
		}
	} else {
		runGit(t, w.st.Root, "config", "core.symlinks", "false")
	}
	w.ticket("DEMO-1", store.Ticket{Title: "First ticket"})
	w.ticket("DEMO-2", store.Ticket{Title: "Second ticket"})
	w.chart("# Chart: demo\n")
	w.adopt("DEMO-1", 1)
	w.adopt("DEMO-2", 2)
	w.adopt("charts/demo", 3)
	return w
}

// TestRuleAtTheTreesLinksFollowTheRecords: every sync makes each record's link
// to its ticket or chart, a store with no tracker gets them too, and links
// taken away come back from the records alone, by a rebuild or by the next
// sync.
func TestRuleAtTheTreesLinksFollowTheRecords(t *testing.T) {
	w := treeWorld(t, true)
	want := map[string]string{
		"trackers/github/example/tracking/1/DEMO-1": "tickets/DEMO-1",
		"trackers/github/example/tracking/2/DEMO-2": "tickets/DEMO-2",
		"trackers/github/example/tracking/3/demo":   "charts/demo",
	}
	if _, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client}, SyncOpts{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := treeLinks(t, w.st); !reflect.DeepEqual(got, want) {
		t.Fatalf("links after a sync = %v, want %v", got, want)
	}
	clearLinks := func() {
		t.Helper()
		for link := range want {
			if err := os.Remove(filepath.Join(w.st.Root, filepath.FromSlash(link))); err != nil {
				t.Fatal(err)
			}
		}
		if got := treeLinks(t, w.st); len(got) != 0 {
			t.Fatalf("links after taking them away = %v, want none", got)
		}
	}
	clearLinks()
	if _, err := w.st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	if got := treeLinks(t, w.st); !reflect.DeepEqual(got, want) {
		t.Errorf("links rebuilt from the records = %v, want %v", got, want)
	}
	clearLinks()
	report, err := Sync(Deps{Store: w.st, Cfg: project.Config{}, Client: w.client}, SyncOpts{})
	if err != nil || !report.NoTracker {
		t.Fatalf("Sync with no tracker = %+v, %v; want NoTracker", report, err)
	}
	if got := treeLinks(t, w.st); !reflect.DeepEqual(got, want) {
		t.Errorf("links made with no tracker = %v, want %v", got, want)
	}
}

// TestRuleAtACloneWithoutSymlinksSyncsWithOneWarningAndKeepsEveryRecord: a
// clone whose git keeps no symlinks syncs as usual, warns once however many
// syncs it runs, makes no link, and keeps every record.
func TestRuleAtACloneWithoutSymlinksSyncsWithOneWarningAndKeepsEveryRecord(t *testing.T) {
	w := treeWorld(t, false)
	var warnings []string
	w.st.Warn = func(format string, args ...any) { warnings = append(warnings, format) }
	for i := 0; i < 2; i++ {
		if _, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client}, SyncOpts{}); err != nil {
			t.Fatalf("Sync %d: %v", i+1, err)
		}
	}
	for _, what := range []string{"DEMO-1", "DEMO-2", "charts/demo"} {
		if !hasTreeRecord(t, w.st, what) {
			t.Errorf("%s has no record", what)
		}
	}
	if links := treeLinks(t, w.st); len(links) != 0 {
		t.Errorf("links = %v, want none", links)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "makes no links") {
		t.Errorf("warnings = %q, want exactly one saying this clone makes no links", warnings)
	}
}

// TestRuleAtAPrivateIssueRepoIsNotScanned: Sync scans the creates and the
// updates for publish-safety hits only when the issue repo is public, so a
// private store's item that trips a term is still created and still updated.
func TestRuleAtAPrivateIssueRepoIsNotScanned(t *testing.T) {
	hit := "Contact " + publishSafetyTestEmail + " for this"
	w := newRuleWorld(t, false)
	w.client.private = true
	// A create lands its record in a commit: a repository with no remote is enough.
	runGit(t, w.st.Root, "init", "-b", "main")
	runGit(t, w.st.Root, "config", "user.name", "tester")
	runGit(t, w.st.Root, "config", "user.email", "tester@example.invalid")
	enableSymlinks(t, w.st.Root)
	w.ticket("DEMO-1", store.Ticket{Title: "A clean title"})
	w.ticket("DEMO-2", store.Ticket{Title: hit})
	runGit(t, w.st.Root, "add", "-A")
	runGit(t, w.st.Root, "commit", "-m", "init")
	w.adopt("DEMO-1", 50)
	ticketYAML := "schema_version: 1\ntitle: A clean title\nbody: |\n  " + hit + "\n"
	if err := os.WriteFile(filepath.Join(w.st.TicketDir("DEMO-1"), "ticket.yaml"), []byte(ticketYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	updates := len(w.client.updates)

	report, err := Sync(Deps{Store: w.st, Cfg: w.cfg, Client: w.client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("Skipped = %+v, want none: a private issue repo is not scanned", report.Skipped)
	}
	if len(w.client.updates) == updates {
		t.Errorf("no UpdateIssue call, want the edit carrying the hit written")
	}
	created := false
	for _, c := range report.Created {
		created = created || c.What == "DEMO-2"
	}
	if !created || len(w.client.calls) == 0 {
		t.Errorf("Created = %+v, calls = %d, want DEMO-2 created despite the hit", report.Created, len(w.client.calls))
	}
}
