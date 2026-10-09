package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// publishSafetyTestEmail is a sample publish-safety email hit: a reserved
// .test domain (RFC 2606) names no real company, but the mirror's scanner
// exempts only example.com/.org/.net/.invalid, so this still trips the
// built-in email pattern. Built from parts so this line itself does not
// read as a contiguous email address.
const publishSafetyTestEmail = "someone" + "@" + "example.test"

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

// newTestRemoteStore creates a bare remote and a working clone with a
// committed project.yaml declaring the github entry, returning a Store
// rooted at the clone.
func newTestRemoteStore(t *testing.T) (st *store.Store, work, remote string) {
	t.Helper()
	dir := t.TempDir()
	remote = filepath.Join(dir, "remote.git")
	work = filepath.Join(dir, "work")

	runGit(t, "", "init", "--bare", "-b", "main", remote)
	runGit(t, "", "clone", remote, work)
	runGit(t, work, "config", "user.name", "tester")
	runGit(t, work, "config", "user.email", "tester@example.invalid")

	projectYAML := `schema_version: 2
name: demo
keys:
  DEMO: everything in demo
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
repos: []
platform: platform/
`
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte(projectYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitattributes"), []byte(gitx.StoreAttributes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("*.lock\n.*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "push", "origin", "main")

	st, err := store.Open(work)
	if err != nil {
		t.Fatal(err)
	}
	return st, work, remote
}

// mintTestTicket creates <id>/ticket.yaml with title, committed (not
// through Store.Claim - Sync's own tests don't need a minted id, only a
// ticket folder on disk).
func mintTestTicket(t *testing.T, st *store.Store, id, title string) {
	t.Helper()
	if err := st.CreateTicketRecord(id, store.Ticket{Title: title}); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", id+": test ticket")
}

// fakeClient is a github.Client stub: it hands out sequential issue numbers
// and node ids, and records every call it saw.
type fakeClient struct {
	calls    []fakeCall
	closed   []string // node ids CloseIssueNotPlanned was called on
	comments []fakeComment
	next     int
	err      error // if set, every CreateIssue call fails with it
	prs      map[string][]github.PullRequest
	// prsByID simulates PullRequestsByIDs: a recorded pull request's node id
	// GitHub still has, with its current state and repository. A node id
	// with no entry here is one GitHub no longer resolves to a pull request.
	prsByID           map[string]github.PullRequest
	subIssues         []fakeSubIssue
	blockedBys        []fakeBlockedBy
	removedBlockedBys []fakeBlockedBy
	ensureProject     []fakeEnsureProject
	placedItems       []fakePlacedItem
	nextItem          int
	// projectErr, if set, is what EnsureProject fails with - a missing
	// project or a token without the project scope (brief.md#The board).
	projectErr error
	// private, when true, makes RepositoryIsPublic report false, as a store
	// whose issue repo is private (brief.md#Publish safety: "A private
	// issue repo is not scanned").
	private bool

	// issues simulates every issue's current GitHub state, keyed by node id:
	// CreateIssue, UpdateIssue, CloseIssueCompleted/CloseIssueNotPlanned and
	// ReopenIssue all mutate it, FetchIssue reads it back, and a test can
	// mutate it directly to simulate an edit made on GitHub
	// (brief.md#Ownership and drift).
	issues map[string]*fakeIssueState
	// subIssuesOf maps a parent issue's node id to its sub-issues', in the
	// order AddSubIssue added them; parentOf is its inverse.
	subIssuesOf map[string][]string
	parentOf    map[string]string
	// blockedByOf maps an issue's node id to the node ids it is blocked by.
	blockedByOf map[string][]string
	// goneNodeIDs are node ids FetchIssue reports github.ErrIssueNotFound
	// for once (brief.md#Adoption: "a recorded issue that is gone from
	// GitHub gets a new issue").
	goneNodeIDs map[string]bool
	updates     []fakeUpdate
	closedDone  []string // node ids CloseIssueCompleted was called on
	reopened    []string // node ids ReopenIssue was called on
	removedSubs []fakeSubIssue
	// itemFields simulates a board item's current Status and Store ID,
	// keyed by item id: PlaceItem writes it, ItemFieldValues reads it back,
	// and a test can mutate it directly to simulate an edit made on GitHub.
	itemFields map[string]fakeItemFields
}

// fakeIssueState is one issue's simulated GitHub state.
type fakeIssueState struct {
	Title string
	Body  string
	Open  bool
}

// fakeUpdate is one UpdateIssue call fakeClient saw.
type fakeUpdate struct {
	NodeID, Title, Body string
}

// fakeItemFields is one board item's simulated Status and Store ID.
type fakeItemFields struct {
	Status, StoreID string
}

// issue returns nodeID's simulated state, creating a default (open, empty)
// entry on first touch so FetchIssue never panics on a node id this fake
// never itself created (an adopted record naming another clone's issue).
func (c *fakeClient) issue(nodeID string) *fakeIssueState {
	if c.issues == nil {
		c.issues = map[string]*fakeIssueState{}
	}
	st, ok := c.issues[nodeID]
	if !ok {
		st = &fakeIssueState{Open: true}
		c.issues[nodeID] = st
	}
	return st
}

// fakeEnsureProject is one EnsureProject call fakeClient saw.
type fakeEnsureProject struct {
	OwnerLogin          string
	IsOrg               bool
	Number              int
	RepoOwner, RepoName string
}

// fakePlacedItem is one PlaceItem call fakeClient saw.
type fakePlacedItem struct {
	ContentID, Status, StoreID string
}

type fakeSubIssue struct {
	ParentID, SubIssueID string
}

type fakeBlockedBy struct {
	IssueID, BlockingID string
}

type fakeCall struct {
	Owner, Repo, Title, Body string
}

type fakeComment struct {
	SubjectID, Body string
}

func (c *fakeClient) CreateIssue(_ context.Context, owner, repo, title, body string) (github.Issue, error) {
	c.calls = append(c.calls, fakeCall{Owner: owner, Repo: repo, Title: title, Body: body})
	if c.err != nil {
		return github.Issue{}, c.err
	}
	c.next++
	nodeID := "NODE_" + title
	*c.issue(nodeID) = fakeIssueState{Title: title, Body: body, Open: true}
	return github.Issue{Number: c.next, NodeID: nodeID}, nil
}

func (c *fakeClient) CloseIssueNotPlanned(_ context.Context, nodeID string) error {
	c.closed = append(c.closed, nodeID)
	return nil
}

func (c *fakeClient) AddComment(_ context.Context, subjectID, body string) error {
	c.comments = append(c.comments, fakeComment{SubjectID: subjectID, Body: body})
	return nil
}

func (c *fakeClient) PullRequestsByHead(_ context.Context, owner, repo, branch string) ([]github.PullRequest, error) {
	return c.prs[owner+"/"+repo+"@"+branch], nil
}

func (c *fakeClient) PullRequestsByIDs(_ context.Context, nodeIDs []string) ([]github.PullRequest, error) {
	var prs []github.PullRequest
	for _, id := range nodeIDs {
		if id == "" {
			// The real GraphQL client sends nodes(ids: [""]) to GitHub, which
			// answers with a non-empty errors array and a hard error
			// (client.go's do); status.go must never hand this an empty id.
			return nil, fmt.Errorf("github: nodes: Could not resolve to a node with the global id of ''")
		}
		if pr, ok := c.prsByID[id]; ok {
			prs = append(prs, pr)
		}
	}
	return prs, nil
}

func (c *fakeClient) AddSubIssue(_ context.Context, parentID, subIssueID string) error {
	c.subIssues = append(c.subIssues, fakeSubIssue{ParentID: parentID, SubIssueID: subIssueID})
	if c.subIssuesOf == nil {
		c.subIssuesOf = map[string][]string{}
	}
	if c.parentOf == nil {
		c.parentOf = map[string]string{}
	}
	if old, ok := c.parentOf[subIssueID]; ok {
		c.subIssuesOf[old] = removeString(c.subIssuesOf[old], subIssueID)
	}
	if !containsString(c.subIssuesOf[parentID], subIssueID) {
		c.subIssuesOf[parentID] = append(c.subIssuesOf[parentID], subIssueID)
	}
	c.parentOf[subIssueID] = parentID
	return nil
}

func (c *fakeClient) AddBlockedBy(_ context.Context, issueID, blockingID string) error {
	c.blockedBys = append(c.blockedBys, fakeBlockedBy{IssueID: issueID, BlockingID: blockingID})
	if c.blockedByOf == nil {
		c.blockedByOf = map[string][]string{}
	}
	if !containsString(c.blockedByOf[issueID], blockingID) {
		c.blockedByOf[issueID] = append(c.blockedByOf[issueID], blockingID)
	}
	return nil
}

func (c *fakeClient) RemoveBlockedBy(_ context.Context, issueID, blockingID string) error {
	c.removedBlockedBys = append(c.removedBlockedBys, fakeBlockedBy{IssueID: issueID, BlockingID: blockingID})
	c.blockedByOf[issueID] = removeString(c.blockedByOf[issueID], blockingID)
	return nil
}

func (c *fakeClient) RemoveSubIssue(_ context.Context, parentID, subIssueID string) error {
	c.removedSubs = append(c.removedSubs, fakeSubIssue{ParentID: parentID, SubIssueID: subIssueID})
	c.subIssuesOf[parentID] = removeString(c.subIssuesOf[parentID], subIssueID)
	if c.parentOf[subIssueID] == parentID {
		delete(c.parentOf, subIssueID)
	}
	return nil
}

func (c *fakeClient) FetchIssue(_ context.Context, nodeID string) (github.IssueState, error) {
	if c.goneNodeIDs[nodeID] {
		delete(c.goneNodeIDs, nodeID)
		return github.IssueState{}, github.ErrIssueNotFound
	}
	st := c.issue(nodeID)
	return github.IssueState{
		Title: st.Title, Body: st.Body, Open: st.Open,
		ParentID: c.parentOf[nodeID], SubIssueIDs: c.subIssuesOf[nodeID], BlockedByIDs: c.blockedByOf[nodeID],
	}, nil
}

func (c *fakeClient) UpdateIssue(_ context.Context, nodeID, title, body string) error {
	c.updates = append(c.updates, fakeUpdate{NodeID: nodeID, Title: title, Body: body})
	st := c.issue(nodeID)
	st.Title, st.Body = title, body
	return nil
}

func (c *fakeClient) CloseIssueCompleted(_ context.Context, nodeID string) error {
	c.closedDone = append(c.closedDone, nodeID)
	c.issue(nodeID).Open = false
	return nil
}

func (c *fakeClient) ReopenIssue(_ context.Context, nodeID string) error {
	c.reopened = append(c.reopened, nodeID)
	c.issue(nodeID).Open = true
	return nil
}

func (c *fakeClient) ItemFieldValues(_ context.Context, itemID string) (status, storeID string, err error) {
	f := c.itemFields[itemID]
	return f.Status, f.StoreID, nil
}

func removeString(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// fakeProjectV2 is the project fakeClient's EnsureProject hands back:
// already-complete Status and Store ID fields, so a test exercising Sync's
// board step need not re-derive field ids of its own.
var fakeProjectV2 = github.ProjectV2{
	ID:            "PROJECT_1",
	StatusFieldID: "FIELD_STATUS",
	StatusOptions: map[string]string{
		"Backlog": "OPT_Backlog", "Briefed": "OPT_Briefed", "In progress": "OPT_In_progress",
		"In review": "OPT_In_review", "Done": "OPT_Done",
	},
	StoreIDFieldID: "FIELD_STOREID",
}

func (c *fakeClient) EnsureProject(_ context.Context, ownerLogin string, isOrg bool, number int, repoOwner, repoName string) (github.ProjectV2, error) {
	c.ensureProject = append(c.ensureProject, fakeEnsureProject{OwnerLogin: ownerLogin, IsOrg: isOrg, Number: number, RepoOwner: repoOwner, RepoName: repoName})
	if c.projectErr != nil {
		return github.ProjectV2{}, c.projectErr
	}
	return fakeProjectV2, nil
}

func (c *fakeClient) LookupProject(_ context.Context, _ string, _ bool, _ int) (bool, error) {
	if c.projectErr != nil {
		return false, c.projectErr
	}
	return true, nil
}

func (c *fakeClient) PlaceItem(_ context.Context, _ github.ProjectV2, contentID, status, storeID string) (string, error) {
	c.placedItems = append(c.placedItems, fakePlacedItem{ContentID: contentID, Status: status, StoreID: storeID})
	c.nextItem++
	itemID := fmt.Sprintf("ITEM_%d", c.nextItem)
	if c.itemFields == nil {
		c.itemFields = map[string]fakeItemFields{}
	}
	c.itemFields[itemID] = fakeItemFields{Status: status, StoreID: storeID}
	return itemID, nil
}

func (c *fakeClient) RepositoryIsPublic(_ context.Context, _, _ string) (bool, error) {
	return !c.private, nil
}

func loadCfg(t *testing.T, st *store.Store) project.Config {
	t.Helper()
	cfg, err := project.Load(filepath.Join(st.Root, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestSyncNoTrackerIsANoOp checks that a project.yaml with no trackers:
// github: entry reports NoTracker and creates nothing, touching no client.
func TestSyncNoTrackerIsANoOp(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	cfg := project.Config{Keys: map[string]string{"DEMO": "everything in demo"}}

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: &fakeClient{}}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !report.NoTracker {
		t.Fatal("report.NoTracker = false, want true")
	}
	if len(report.Created) != 0 {
		t.Fatalf("Created = %v, want none", report.Created)
	}
}

// TestSyncCreatesAnIssuePerTicketAndChart is the tracer's own end-to-end
// chain: a store whose origin is a bare repo, two tickets and one chart,
// none with a record yet. Sync creates one issue each, titled and footed
// per brief.md#What an issue shows, writes each record where the bridge
// keeps it, and pushes each as its own commit (Store.Claim).
func TestSyncCreatesAnIssuePerTicketAndChart(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	mintTestTicket(t, st, "DEMO-2", "")
	runGit(t, work, "push", "origin", "main")

	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add chart")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(report.Created) != 3 {
		t.Fatalf("Created = %+v, want 3 issues", report.Created)
	}
	wantWhat := []string{"DEMO-1", "DEMO-2", "charts/demo"}
	for i, c := range report.Created {
		if c.What != wantWhat[i] {
			t.Fatalf("Created[%d].What = %q, want %q (tickets in id order, then charts)", i, c.What, wantWhat[i])
		}
		if c.Owner != "example" || c.Repo != "tracking" {
			t.Fatalf("Created[%d] = %+v, want owner/repo example/tracking", i, c)
		}
	}

	if client.calls[0].Title != "First ticket" {
		t.Fatalf("DEMO-1's title = %q, want its ticket.yaml title", client.calls[0].Title)
	}
	if client.calls[1].Title != "DEMO-2" {
		t.Fatalf("DEMO-2's title = %q, want its id (no ticket.yaml title set)", client.calls[1].Title)
	}
	if client.calls[2].Title != "Chart: demo" {
		t.Fatalf("chart demo's title = %q, want %q", client.calls[2].Title, "Chart: demo")
	}
	wantFooter1 := "---\n<sub>Mirrored from the jig ticket store (`DEMO-1`). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n<!-- jig:DEMO-1 -->"
	if client.calls[0].Body != wantFooter1 {
		t.Fatalf("DEMO-1's body = %q, want %q", client.calls[0].Body, wantFooter1)
	}
	wantChartFooter := "---\n<sub>Mirrored from the jig ticket store (`charts/demo/`). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n<!-- jig:chart:demo -->"
	if client.calls[2].Body != wantChartFooter {
		t.Fatalf("chart demo's body = %q, want %q", client.calls[2].Body, wantChartFooter)
	}

	// Each record landed where the bridge keeps it, on the remote too (one
	// commit per issue, pushed at once - Store.Claim).
	for i, what := range []string{"DEMO-1", "DEMO-2"} {
		data, err := os.ReadFile(filepath.Join(st.TicketDir(what), "tracker", "github.yaml"))
		if err != nil {
			t.Fatalf("read %s's record: %v", what, err)
		}
		var rec githubRecord
		if err := yaml.Unmarshal(data, &rec); err != nil {
			t.Fatalf("decode %s's record: %v", what, err)
		}
		if rec.Repo != "example/tracking" || rec.Issue != i+1 || rec.NodeID == "" {
			t.Fatalf("%s's record = %+v, unexpected", what, rec)
		}
		if !strings.HasPrefix(string(data), "#") {
			t.Fatalf("%s's record does not start with a comment line: %s", what, data)
		}
		out := runGit(t, "", "--git-dir", remote, "log", "--oneline", "--all", "--", st.TicketRelDir(what)+"/tracker/github.yaml")
		if !strings.Contains(out, what+": github issue example/tracking#"+string(rune('1'+i))) {
			t.Fatalf("remote history for %s = %q, want a commit naming the issue", what, out)
		}
	}
	if _, err := os.Stat(filepath.Join(st.Root, "charts", "demo", "github.yaml")); err != nil {
		t.Fatalf("chart demo's record was not written: %v", err)
	}

	// A second Sync does nothing more: every item now has a record.
	report2, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(report2.Created) != 0 {
		t.Fatalf("second Sync created %+v, want none (every item already has a record)", report2.Created)
	}
	if len(client.calls) != 3 {
		t.Fatalf("client saw %d calls after the second Sync, want still 3 (no issue created twice)", len(client.calls))
	}
}

// TestSyncDryRunTouchesNeitherGitHubNorTheStore checks --dry-run's contract:
// it reports what it would create, without calling the client or writing
// any record - dry run must work with no client at all, since it reads
// everything and writes nothing (brief.md#jig trackers sync).
func TestSyncDryRunTouchesNeitherGitHubNorTheStore(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	report, err := Sync(Deps{Store: st, Cfg: cfg}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}
	if len(report.Created) != 1 || report.Created[0].What != "DEMO-1" || report.Created[0].Number != 0 {
		t.Fatalf("Created = %+v, want one would-create entry for DEMO-1 with no issue number", report.Created)
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml")); !os.IsNotExist(err) {
		t.Fatalf("--dry-run wrote a record (stat err %v), want nothing written", err)
	}
}

// TestSyncDryRunPreviewsAgainstExistingRecords drives --dry-run over a
// store whose item already has a record, against a client whose GitHub
// state now differs from what the last sync wrote - the cutover's own gate
// (brief.md#Cutover's "jig trackers sync --dry-run must report 0 changes
// and no drift"). The preview must report the would-be update, its drift
// and the would-be board placement, while making none of the client's
// mutating calls and writing no record.
func TestSyncDryRunPreviewsAgainstExistingRecords(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "A clean title")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	before, err := os.ReadFile(filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read DEMO-1's record: %v", err)
	}
	var rec githubRecord
	if err := yaml.Unmarshal(before, &rec); err != nil {
		t.Fatal(err)
	}

	// Simulate edits made on GitHub since the last sync: the issue's title
	// and the board item's Status now differ from synced:.
	client.issue(rec.NodeID).Title = "Edited on GitHub"
	client.itemFields[rec.Item] = fakeItemFields{Status: "Done", StoreID: client.itemFields[rec.Item].StoreID}

	calls, updates, placed := len(client.calls), len(client.updates), len(client.placedItems)

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}

	if len(report.Updated) != 1 || report.Updated[0].What != "DEMO-1" {
		t.Fatalf("Updated = %+v, want DEMO-1 previewed as updated (title)", report.Updated)
	}
	var sawTitleDrift, sawStatusDrift bool
	for _, d := range report.Drift {
		if d.What != "DEMO-1" {
			continue
		}
		switch d.Field {
		case "title":
			sawTitleDrift = true
		case "status":
			sawStatusDrift = true
		}
	}
	if !sawTitleDrift || !sawStatusDrift {
		t.Fatalf("Drift = %+v, want a title and a status drift line for DEMO-1", report.Drift)
	}
	if len(report.Placed) != 1 || report.Placed[0].What != "DEMO-1" {
		t.Fatalf("Placed = %+v, want DEMO-1 previewed as placed (its Status no longer matches)", report.Placed)
	}

	if len(client.calls) != calls || len(client.updates) != updates || len(client.placedItems) != placed {
		t.Fatalf("dry run mutated the client: CreateIssue calls %d->%d, UpdateIssue calls %d->%d, PlaceItem calls %d->%d",
			calls, len(client.calls), updates, len(client.updates), placed, len(client.placedItems))
	}

	after, err := os.ReadFile(filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read DEMO-1's record after the dry run: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("dry run wrote DEMO-1's record:\nbefore: %s\nafter: %s", before, after)
	}
}

// TestSyncReportsAClientFailure checks that a GitHub failure (network,
// token, a GitHub error) comes back as a plain Go error, naming the item it
// happened on: the caller (the checkpoint hook) decides this is
// best-effort, not Sync itself.
func TestSyncReportsAClientFailure(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{err: context.DeadlineExceeded}
	_, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err == nil {
		t.Fatal("Sync: want an error when the client fails")
	}
	if !strings.Contains(err.Error(), "DEMO-1") {
		t.Fatalf("err = %v, want it to name DEMO-1", err)
	}
}

// TestSyncBoundedTimeoutNamesRemainingItemsAndTrackersSync is gate finding
// r2-f5's own case: a sync from inside a command (SyncOpts{}, the checkpoint
// hook's default) that runs out of its bound names how many tickets or
// charts still have no GitHub issue and points at `jig trackers sync`,
// rather than just surfacing the bare context.DeadlineExceeded every
// client call already reported on its own (TestSyncReportsAClientFailure).
func TestSyncBoundedTimeoutNamesRemainingItemsAndTrackersSync(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	mintTestTicket(t, st, "DEMO-2", "Second ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{err: context.DeadlineExceeded}
	_, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err == nil {
		t.Fatal("Sync: want an error when the bound runs out")
	}
	if !strings.Contains(err.Error(), "2 ticket(s) or chart(s)") {
		t.Fatalf("err = %v, want it to name how many items are left", err)
	}
	if !strings.Contains(err.Error(), "jig trackers sync") {
		t.Fatalf("err = %v, want it to point at `jig trackers sync`", err)
	}
}

// TestSyncUnboundedRunsPastTheBound is `jig trackers sync`'s own case
// (SyncOpts{Unbounded: true}): a timeout the client itself reports (not the
// sync's own bound, since it set none) is returned as-is, with none of the
// bounded path's "ran out of its bound" wording, which would be false here.
func TestSyncUnboundedRunsPastTheBound(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{err: context.DeadlineExceeded}
	_, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{Unbounded: true})
	if err == nil {
		t.Fatal("Sync: want an error when the client fails")
	}
	if strings.Contains(err.Error(), "jig trackers sync") {
		t.Fatalf("err = %v, want no `jig trackers sync` pointer on an already-unbounded sync", err)
	}
}

// TestSyncAdoptsAnotherClonesIssueOnARejectedPush is the "two clones creating
// the same ticket's issue" critical path (brief.md#Syncing at every
// checkpoint): another clone claims DEMO-1's record and pushes it first, so
// this Sync's own push for the issue it just opened is rejected; the pull
// that follows brings the other clone's record in, so this clone must close
// its own issue as not planned (naming the other in a comment), drop its own
// record and adopt the other's - ending with one open issue and one record.
func TestSyncAdoptsAnotherClonesIssueOnARejectedPush(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	otherRecord := recordHeader + "repo: example/tracking\nissue: 99\nnode_id: NODE_OTHER\n"
	if err := os.MkdirAll(filepath.Join(other, "tickets", "DEMO-1", "tracker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "tickets", "DEMO-1", "tracker", "github.yaml"), []byte(otherRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "DEMO-1: github issue example/tracking#99")
	runGit(t, other, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("client saw %d CreateIssue calls, want exactly 1 (one issue opened, then closed as a duplicate)", len(client.calls))
	}
	if len(report.Created) != 1 || report.Created[0].What != "DEMO-1" || report.Created[0].Number != 99 {
		t.Fatalf("Created = %+v, want one entry for DEMO-1 naming the other clone's issue #99", report.Created)
	}

	if len(client.closed) != 1 || client.closed[0] != "NODE_First ticket" {
		t.Fatalf("closed = %v, want this clone's own issue (NODE_First ticket) closed as not planned", client.closed)
	}
	if len(client.comments) != 1 || client.comments[0].SubjectID != "NODE_First ticket" || client.comments[0].Body != "Duplicate of #99" {
		t.Fatalf("comments = %+v, want one comment on the own issue naming #99", client.comments)
	}

	data, err := os.ReadFile(filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read DEMO-1's record: %v", err)
	}
	var rec githubRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode DEMO-1's record: %v", err)
	}
	if rec.Issue != 99 || rec.NodeID != "NODE_OTHER" {
		t.Fatalf("DEMO-1's record = %+v, want the other clone's own issue (#99, NODE_OTHER) adopted, not this clone's", rec)
	}
	if rec.Item == "" {
		t.Fatalf("DEMO-1's record = %+v, want it placed on the board too (item: set)", rec)
	}

	remoteLog := runGit(t, "", "--git-dir", remote, "log", "--oneline", "--all", "--", "tickets/DEMO-1/tracker/github.yaml")
	if n := strings.Count(remoteLog, "DEMO-1: github issue"); n != 1 {
		t.Fatalf("remote history for DEMO-1's record has %d claiming commits, want exactly 1 (the rejected attempt left nothing behind)", n)
	}

	// The claim itself left nothing behind beyond the adopted record; the
	// board placement that follows is an ordinary working-tree write (not a
	// Claim), going in with the next checkpoint's commit
	// (brief.md#Syncing at every checkpoint), so the only dirty file is
	// DEMO-1's own record, carrying the item: the board step just set.
	status := runGit(t, work, "status", "--porcelain")
	if strings.TrimSpace(status) != "M tickets/DEMO-1/tracker/github.yaml" {
		t.Fatalf("store dirty after Sync = %q, want only DEMO-1's record (the board step's item: write)", status)
	}
}

// TestSyncSkipsAPublishSafetyHit is the "a publish-safety hit skips that
// one ticket or chart" critical path (brief.md#Seams, #Publish safety):
// DEMO-1's title trips the built-in email pattern against its public issue
// repo, so Sync never opens or writes a record for it, while DEMO-2
// (clean) syncs normally.
func TestSyncSkipsAPublishSafetyHit(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Contact "+publishSafetyTestEmail+" for this")
	mintTestTicket(t, st, "DEMO-2", "A clean title")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(report.Skipped) != 1 || report.Skipped[0].What != "DEMO-1" {
		t.Fatalf("Skipped = %+v, want one entry naming DEMO-1", report.Skipped)
	}
	if !strings.Contains(report.Skipped[0].Text, publishSafetyTestEmail) {
		t.Fatalf("Skipped[0].Text = %q, want it to carry the hit", report.Skipped[0].Text)
	}
	if len(report.Created) != 1 || report.Created[0].What != "DEMO-2" {
		t.Fatalf("Created = %+v, want only DEMO-2", report.Created)
	}
	if len(client.calls) != 1 || client.calls[0].Title != "A clean title" {
		t.Fatalf("client saw %+v, want only DEMO-2's CreateIssue call (DEMO-1 never written)", client.calls)
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml")); !os.IsNotExist(err) {
		t.Fatalf("DEMO-1 got a record (stat err %v), want none: the hit must skip the write", err)
	}
}

// TestSyncDoesNotScanAPrivateIssueRepo checks brief.md#Publish safety's "A
// private issue repo is not scanned": the same title that TestSyncSkipsA
// PublishSafetyHit skips on a public repo is created as normal once the
// fake client reports the repo private.
func TestSyncDoesNotScanAPrivateIssueRepo(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Contact "+publishSafetyTestEmail+" for this")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{private: true}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("Skipped = %+v, want none (the issue repo is private)", report.Skipped)
	}
	if len(report.Created) != 1 || report.Created[0].What != "DEMO-1" {
		t.Fatalf("Created = %+v, want DEMO-1 created despite the hit", report.Created)
	}
	if len(client.calls) != 1 {
		t.Fatalf("client saw %d CreateIssue calls, want 1", len(client.calls))
	}
}

// TestSyncScansWithPublishTermsFromHome checks that Sync loads
// publish-terms.txt from Deps.Home and skips a ticket whose title or body
// contains one of its terms, case-insensitively.
func TestSyncScansWithPublishTermsFromHome(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "The CodeName launch")
	runGit(t, work, "push", "origin", "main")

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "publish-terms.txt"), []byte("codename\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client, Home: home}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].What != "DEMO-1" {
		t.Fatalf("Skipped = %+v, want DEMO-1 skipped on its publish-terms.txt hit", report.Skipped)
	}
	if len(client.calls) != 0 {
		t.Fatalf("client saw %+v, want no CreateIssue call", client.calls)
	}
}

// TestSyncDryRunReportsAPublishSafetyHit checks that --dry-run previews a
// hit as skipped (rather than would-create), without reaching the client
// (dry run cannot learn the issue repo's visibility, so it scans
// unconditionally - brief.md#Publish safety).
func TestSyncDryRunReportsAPublishSafetyHit(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Contact "+publishSafetyTestEmail+" for this")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	report, err := Sync(Deps{Store: st, Cfg: cfg}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].What != "DEMO-1" {
		t.Fatalf("Skipped = %+v, want DEMO-1 previewed as skipped", report.Skipped)
	}
	if len(report.Created) != 0 {
		t.Fatalf("Created = %+v, want none", report.Created)
	}
}

// TestSyncDryRunPreviewsFullBodyForANewItem is r1-f3's own gap: on a store
// whose items are all new, the live sync's create step writes only the
// marker as the issue body (brief.md#Syncing at every checkpoint, step 3)
// and relies on updateIssues rendering the full body once claimIssue's
// record lands it in the same run (step 4). A dry run claims nothing, so
// without this fix the new item never reaches updateIssues and its real
// body - here, a hit in the description, which the footer-only render
// never carries - goes neither previewed nor scanned.
func TestSyncDryRunPreviewsFullBodyForANewItem(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	if err := st.CreateTicketRecord("DEMO-1", store.Ticket{
		Title: "A clean title",
		Body:  "Contact " + publishSafetyTestEmail + " for this",
	}); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", "DEMO-1: test ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	report, err := Sync(Deps{Store: st, Cfg: cfg}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].What != "DEMO-1" {
		t.Fatalf("Skipped = %+v, want DEMO-1 previewed as skipped on its description's hit", report.Skipped)
	}
	if len(report.Created) != 0 {
		t.Fatalf("Created = %+v, want none", report.Created)
	}
}

// TestSyncRefusesACreateOnABodyHitLikeTheDryRunPreviewsIt is r4-f1: a new
// item's hit lives only in its description, which the live create's
// footer-only render (it.render) never carries, so without scanning
// previewCreateRender's full render first, the live sync used to create the
// issue and claim a record, then have updateIssues skip the very next
// update on the same hit - disagreeing with the dry run, which already
// previews (and scans) the full body (r1-f3). Both paths must land on the
// same outcome: skipped, no CreateIssue, no record.
func TestSyncRefusesACreateOnABodyHitLikeTheDryRunPreviewsIt(t *testing.T) {
	newStore := func(t *testing.T) (*store.Store, string) {
		st, work, _ := newTestRemoteStore(t)
		if err := st.CreateTicketRecord("DEMO-1", store.Ticket{
			Title: "A clean title",
			Body:  "Contact " + publishSafetyTestEmail + " for this",
		}); err != nil {
			t.Fatal(err)
		}
		runGit(t, st.Root, "add", "-A")
		runGit(t, st.Root, "commit", "-m", "DEMO-1: test ticket")
		runGit(t, work, "push", "origin", "main")
		return st, work
	}

	st, _ := newStore(t)
	cfg := loadCfg(t, st)
	report, err := Sync(Deps{Store: st, Cfg: cfg}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("dry run Sync: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].What != "DEMO-1" {
		t.Fatalf("dry run Skipped = %+v, want DEMO-1", report.Skipped)
	}
	if len(report.Created) != 0 {
		t.Fatalf("dry run Created = %+v, want none", report.Created)
	}

	st2, _ := newStore(t)
	cfg2 := loadCfg(t, st2)
	client := &fakeClient{}
	liveReport, err := Sync(Deps{Store: st2, Cfg: cfg2, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("live Sync: %v", err)
	}
	if len(liveReport.Skipped) != 1 || liveReport.Skipped[0].What != "DEMO-1" {
		t.Fatalf("live Skipped = %+v, want DEMO-1", liveReport.Skipped)
	}
	if len(liveReport.Created) != 0 {
		t.Fatalf("live Created = %+v, want none", liveReport.Created)
	}
	if len(client.calls) != 0 {
		t.Fatalf("CreateIssue calls = %+v, want none", client.calls)
	}
	if has, err := hasRecord(filepath.Join(st2.TicketDir("DEMO-1"), "tracker", "github.yaml")); err != nil {
		t.Fatalf("check DEMO-1's record: %v", err)
	} else if has {
		t.Fatalf("DEMO-1 got a record, want none written on a publish-safety hit")
	}
}

// TestSyncRefusesACreateOnAPullRequestHitLikeUpdateIssuesWouldSkip is r4-f1's
// narrower case: previewCreateRender left a new item's pull requests out of
// its render, so a term matching a pull request's repo passed the
// create-time scan, CreateIssue wrote the issue, claimIssue committed and
// pushed the record, and updateIssues then hit the same term in the
// `## Pull requests` block it renders and skipped the update - the same
// created-plus-skipped, permanently bodyless issue r4-f1 named, now for a
// pull request rather than a description. DEMO-1's own title and body carry
// no hit; only its one pull request's repo (example/demo) does, via
// publish-terms.txt. Both a dry run and a live sync must refuse the create.
func TestSyncRefusesACreateOnAPullRequestHitLikeUpdateIssuesWouldSkip(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "publish-terms.txt"), []byte("example/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	newStoreWithPR := func(t *testing.T) *store.Store {
		st, work, _ := newTestRemoteStore(t)
		addRepoToProjectYAML(t, work)
		if err := st.CreateTicketRecord("DEMO-1", store.Ticket{
			Title: "A clean title",
			Body:  "A clean description",
		}); err != nil {
			t.Fatal(err)
		}
		runGit(t, st.Root, "add", "-A")
		runGit(t, st.Root, "commit", "-m", "DEMO-1: test ticket")
		runGit(t, work, "push", "origin", "main")
		return st
	}
	prs := map[string][]github.PullRequest{
		"example/demo@jig/DEMO-1": {{Number: 5, NodeID: "PR_5", State: "OPEN"}},
	}

	st := newStoreWithPR(t)
	cfg := loadCfg(t, st)
	dryClient := &fakeClient{prs: prs}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: dryClient, Home: home}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("dry run Sync: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].What != "DEMO-1" {
		t.Fatalf("dry run Skipped = %+v, want DEMO-1", report.Skipped)
	}
	if len(report.Created) != 0 {
		t.Fatalf("dry run Created = %+v, want none", report.Created)
	}

	st2 := newStoreWithPR(t)
	cfg2 := loadCfg(t, st2)
	liveClient := &fakeClient{prs: prs}
	liveReport, err := Sync(Deps{Store: st2, Cfg: cfg2, Client: liveClient, Home: home}, SyncOpts{})
	if err != nil {
		t.Fatalf("live Sync: %v", err)
	}
	if len(liveReport.Skipped) != 1 || liveReport.Skipped[0].What != "DEMO-1" {
		t.Fatalf("live Skipped = %+v, want DEMO-1", liveReport.Skipped)
	}
	if len(liveReport.Created) != 0 {
		t.Fatalf("live Created = %+v, want none", liveReport.Created)
	}
	if len(liveClient.calls) != 0 {
		t.Fatalf("CreateIssue calls = %+v, want none", liveClient.calls)
	}
	if has, err := hasRecord(filepath.Join(st2.TicketDir("DEMO-1"), "tracker", "github.yaml")); err != nil {
		t.Fatalf("check DEMO-1's record: %v", err)
	} else if has {
		t.Fatalf("DEMO-1 got a record, want none written on its pull request's publish-safety hit")
	}
}

// TestSyncSkipsAPublishSafetyHitOnUpdate is the update half of "a
// publish-safety hit skips that one ticket or chart" (brief.md#Seams,
// #Publish safety): DEMO-1 already has a record from an earlier sync, and
// its rendered body - the only text that ever reaches GitHub through
// UpdateIssue - is edited to carry a hit its title never did. The hit must
// be reported in Skipped, make no UpdateIssue call, write no record change,
// and win no new board placement either, while DEMO-2 (clean) still syncs
// normally. A chart ("demo") whose own map.md is edited to carry a hit gets
// the same treatment on its *link* writes (r1-f1, r2-f2): a second ticket
// (DEMO-4) newly added to its tickets.yaml in the same commit must not
// become a sub-issue of it, since the chart's own update is skipped - the
// proof r2-f2 needs, since links.go used to probe the skip set by the
// chart's bare name while updateIssues keys it by what() ("charts/<name>").
func TestSyncSkipsAPublishSafetyHitOnUpdate(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "A clean title")
	mintTestTicket(t, st, "DEMO-2", "Another clean title")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if len(client.calls) != 2 {
		t.Fatalf("client saw %d CreateIssue calls after the first sync, want 2", len(client.calls))
	}

	mintTestTicket(t, st, "DEMO-3", "Chart ticket")
	mintTestTicket(t, st, "DEMO-4", "Another chart ticket")
	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets:\n  - id: DEMO-3\n    title: Chart ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", "add chart with one ticket")

	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("second Sync (chart setup): %v", err)
	}
	if len(client.subIssues) != 1 || client.subIssues[0].SubIssueID != "NODE_Chart ticket" {
		t.Fatalf("subIssues after chart setup = %+v, want DEMO-3 linked under the chart", client.subIssues)
	}

	before, err := os.ReadFile(filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read DEMO-1's record: %v", err)
	}

	ticketYAML := "schema_version: 1\ntitle: A clean title\nbody: |\n  Contact " + publishSafetyTestEmail + " for this\n"
	if err := os.WriteFile(filepath.Join(st.TicketDir("DEMO-1"), "ticket.yaml"), []byte(ticketYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n\nContact "+publishSafetyTestEmail+" for this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets:\n  - id: DEMO-3\n    title: Chart ticket\n  - id: DEMO-4\n    title: Another chart ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", "DEMO-1 and the chart's map now carry emails; DEMO-4 joins the chart")

	placedBefore := len(client.placedItems)
	updatesBefore := len(client.updates)
	subIssuesBefore := len(client.subIssues)

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("third Sync: %v", err)
	}

	if len(report.Skipped) != 2 {
		t.Fatalf("Skipped = %+v, want two entries, DEMO-1 and the chart", report.Skipped)
	}
	var sawDemo1, sawChart bool
	for _, s := range report.Skipped {
		switch s.What {
		case "DEMO-1":
			sawDemo1 = true
			if !strings.Contains(s.Text, publishSafetyTestEmail) {
				t.Fatalf("DEMO-1's Skipped.Text = %q, want it to carry the hit", s.Text)
			}
		case "charts/demo":
			sawChart = true
			if !strings.Contains(s.Text, publishSafetyTestEmail) {
				t.Fatalf("charts/demo's Skipped.Text = %q, want it to carry the hit", s.Text)
			}
		}
	}
	if !sawDemo1 || !sawChart {
		t.Fatalf("Skipped = %+v, want entries naming DEMO-1 and charts/demo", report.Skipped)
	}
	if len(client.updates) != updatesBefore {
		t.Fatalf("client saw %d new UpdateIssue calls %+v, want none: DEMO-1's and the chart's hits must skip their updates, nothing else changed", len(client.updates)-updatesBefore, client.updates)
	}
	if len(client.placedItems) != placedBefore {
		t.Fatalf("placedItems went from %d to %d, want no new board write", placedBefore, len(client.placedItems))
	}
	if len(client.subIssues) != subIssuesBefore {
		t.Fatalf("subIssues went from %d to %d (%+v), want no AddSubIssue for the chart's skipped update: DEMO-4 must not become its sub-issue", subIssuesBefore, len(client.subIssues), client.subIssues)
	}

	after, err := os.ReadFile(filepath.Join(st.TicketDir("DEMO-1"), "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read DEMO-1's record after the third sync: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("DEMO-1's record changed on the hit:\nbefore: %s\nafter: %s", before, after)
	}
}

// mintTestTicketWithBlockers is mintTestTicket, with blocked_by recorded in
// the ticket's own ticket.yaml.
func mintTestTicketWithBlockers(t *testing.T, st *store.Store, id, title string, blockedBy []store.TicketBlockedBy) {
	t.Helper()
	if err := st.CreateTicketRecord(id, store.Ticket{Title: title, BlockedBy: blockedBy}); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", id+": test ticket")
}

// TestSyncLinksChartSubIssuesAndBlockedBy is brief.md#Links's own critical
// path: a chart's ticket becomes a sub-issue of the chart's issue, a
// blocked_by edge whose blocker has an issue becomes a native blocked-by
// link, and one whose blocker has none is skipped and reported as not
// linked. Each ticket's record keeps the node ids of the links this sync
// wrote, and a second Sync repeats none of the mutations.
func TestSyncLinksChartSubIssuesAndBlockedBy(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Blocker")
	mintTestTicketWithBlockers(t, st, "DEMO-2", "Chart ticket", []store.TicketBlockedBy{
		{Ticket: "DEMO-1", Kind: "merged"},
		{Ticket: "DEMO-9", Kind: "merged"}, // no issue: not linked
	})
	runGit(t, work, "push", "origin", "main")

	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets:\n  - id: DEMO-2\n    title: Chart ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add chart")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(report.LinkedParents) != 1 || report.LinkedParents[0] != (LinkedParent{Ticket: "DEMO-2", Chart: "demo"}) {
		t.Fatalf("LinkedParents = %+v, want one entry linking DEMO-2 under chart demo", report.LinkedParents)
	}
	if len(report.LinkedBlockedBy) != 1 || report.LinkedBlockedBy[0] != (LinkedBlockedBy{Ticket: "DEMO-2", Blocker: "DEMO-1"}) {
		t.Fatalf("LinkedBlockedBy = %+v, want one entry linking DEMO-2 to its blocker DEMO-1", report.LinkedBlockedBy)
	}
	if len(report.NotLinked) != 1 || report.NotLinked[0] != (NotLinked{Ticket: "DEMO-2", Blocker: "DEMO-9"}) {
		t.Fatalf("NotLinked = %+v, want one entry for DEMO-2's blocker DEMO-9, which has no issue", report.NotLinked)
	}

	if len(client.subIssues) != 1 || client.subIssues[0] != (fakeSubIssue{ParentID: "NODE_Chart: demo", SubIssueID: "NODE_Chart ticket"}) {
		t.Fatalf("subIssues = %+v, want DEMO-2's issue added as a sub-issue of the chart's", client.subIssues)
	}
	if len(client.blockedBys) != 1 || client.blockedBys[0] != (fakeBlockedBy{IssueID: "NODE_Chart ticket", BlockingID: "NODE_Blocker"}) {
		t.Fatalf("blockedBys = %+v, want DEMO-2's issue blocked by DEMO-1's", client.blockedBys)
	}

	data, err := os.ReadFile(filepath.Join(st.TicketDir("DEMO-2"), "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read DEMO-2's record: %v", err)
	}
	var rec githubRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode DEMO-2's record: %v", err)
	}
	if rec.Synced == nil || rec.Synced.Links.Parent != "NODE_Chart: demo" {
		t.Fatalf("DEMO-2's record = %+v, want synced.links.parent naming the chart's issue", rec)
	}
	if len(rec.Synced.Links.BlockedBy) != 1 || rec.Synced.Links.BlockedBy[0] != "NODE_Blocker" {
		t.Fatalf("DEMO-2's record = %+v, want synced.links.blocked_by naming DEMO-1's issue", rec)
	}

	// A second Sync repeats no mutation: every link it would write is
	// already recorded.
	report2, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(report2.LinkedParents) != 0 || len(report2.LinkedBlockedBy) != 0 {
		t.Fatalf("second Sync linked %+v / %+v, want nothing (already linked)", report2.LinkedParents, report2.LinkedBlockedBy)
	}
	if len(client.subIssues) != 1 || len(client.blockedBys) != 1 {
		t.Fatalf("client saw %d AddSubIssue and %d AddBlockedBy calls after the second Sync, want still 1 each", len(client.subIssues), len(client.blockedBys))
	}
	// DEMO-9 still has no issue, so it is reported as not linked every sync.
	if len(report2.NotLinked) != 1 {
		t.Fatalf("second Sync NotLinked = %+v, want DEMO-9 reported again", report2.NotLinked)
	}
}

// TestSyncLinksBlockedByThroughAnAlias covers the refresh's own rule: a
// blocked_by ref naming a blocker's alias rather than its current id still
// gets its native blocked-by link, reported (and recorded) under the
// blocker's current id.
func TestSyncLinksBlockedByThroughAnAlias(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	if err := st.CreateTicketRecord("DEMO-1", store.Ticket{Title: "Blocker", Aliases: []string{"DEMO-0"}}); err != nil {
		t.Fatal(err)
	}
	mintTestTicketWithBlockers(t, st, "DEMO-2", "Waiting ticket", []store.TicketBlockedBy{
		{Ticket: "DEMO-0", Kind: "merged"},
	})
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(report.LinkedBlockedBy) != 1 || report.LinkedBlockedBy[0] != (LinkedBlockedBy{Ticket: "DEMO-2", Blocker: "DEMO-1"}) {
		t.Fatalf("LinkedBlockedBy = %+v, want one entry naming the blocker's current id DEMO-1", report.LinkedBlockedBy)
	}
	if len(client.blockedBys) != 1 || client.blockedBys[0] != (fakeBlockedBy{IssueID: "NODE_Waiting ticket", BlockingID: "NODE_Blocker"}) {
		t.Fatalf("blockedBys = %+v, want DEMO-2's issue blocked by DEMO-1's", client.blockedBys)
	}

	ticket, err := st.ReadTicket("DEMO-2")
	if err != nil {
		t.Fatalf("ReadTicket DEMO-2: %v", err)
	}
	line, err := waitsForLine(st, ticket.BlockedBy)
	if err != nil {
		t.Fatalf("waitsForLine: %v", err)
	}
	if !strings.HasPrefix(line, "**Waits for:** DEMO-1 ") {
		t.Fatalf("waitsForLine = %q, want it to name the current id DEMO-1, not the alias DEMO-0", line)
	}
}

// TestSyncDryRunPreviewsLinksWithoutMutating is gate finding r2-f3's own
// case: TestSyncDryRunPreviewsAgainstExistingRecords's store carries one
// chartless, blocker-free ticket, so links.go's own dryRun branches
// (linkParent, linkBlockedBy and the RemoveSubIssue/RemoveBlockedBy guards)
// are never entered. This test reuses TestSyncLinksChartSubIssuesAndBlockedBy's
// own fixture (a chart ticket blocked by one linked and one unlinked
// blocker), but gives each item an adopted record (the bridge's shape, no
// links recorded) against a fresh fakeClient that already shows an extra
// hand-added sub-issue and blocked-by edge, so a dry run must preview a new
// parent link, a new blocked-by link, a not-linked blocker and both removal
// drift lines - without ever calling AddSubIssue, AddBlockedBy, RemoveSubIssue
// or RemoveBlockedBy.
func TestSyncDryRunPreviewsLinksWithoutMutating(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Blocker")
	mintTestTicketWithBlockers(t, st, "DEMO-2", "Chart ticket", []store.TicketBlockedBy{
		{Ticket: "DEMO-1", Kind: "merged"},
		{Ticket: "DEMO-9", Kind: "merged"}, // no issue: not linked
	})
	runGit(t, work, "push", "origin", "main")

	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte("# Chart: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets:\n  - id: DEMO-2\n    title: Chart ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add chart")
	runGit(t, work, "push", "origin", "main")

	// Each item already has an issue (the bridge's own shape, no synced:
	// links yet), so linkParentsAndBlockers has something to link - without
	// any CreateIssue, AddSubIssue or AddBlockedBy call ever having been
	// made on this fakeClient.
	writeAdoptedRecord(t, st.Root, filepath.Join("tickets", "DEMO-1", "tracker", "github.yaml"), "example/tracking", 1, "NODE_BLOCKER", "")
	writeAdoptedRecord(t, st.Root, filepath.Join("tickets", "DEMO-2", "tracker", "github.yaml"), "example/tracking", 2, "NODE_CHART_TICKET", "")
	writeAdoptedRecord(t, st.Root, filepath.Join("charts", "demo", "github.yaml"), "example/tracking", 3, "NODE_CHART_DEMO", "")

	client := &fakeClient{
		subIssuesOf: map[string][]string{"NODE_CHART_DEMO": {"NODE_HAND_ADDED"}},
		parentOf:    map[string]string{"NODE_HAND_ADDED": "NODE_CHART_DEMO"},
		blockedByOf: map[string][]string{"NODE_CHART_TICKET": {"NODE_HAND_ADDED_BLOCKER"}},
	}

	cfg := loadCfg(t, st)
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}

	if len(report.LinkedParents) != 1 || report.LinkedParents[0] != (LinkedParent{Ticket: "DEMO-2", Chart: "demo"}) {
		t.Fatalf("LinkedParents = %+v, want DEMO-2 previewed as linked under chart demo", report.LinkedParents)
	}
	if len(report.LinkedBlockedBy) != 1 || report.LinkedBlockedBy[0] != (LinkedBlockedBy{Ticket: "DEMO-2", Blocker: "DEMO-1"}) {
		t.Fatalf("LinkedBlockedBy = %+v, want DEMO-2 previewed as blocked by DEMO-1", report.LinkedBlockedBy)
	}
	if len(report.NotLinked) != 1 || report.NotLinked[0] != (NotLinked{Ticket: "DEMO-2", Blocker: "DEMO-9"}) {
		t.Fatalf("NotLinked = %+v, want DEMO-2's blocker DEMO-9 reported, which has no issue", report.NotLinked)
	}

	var sawSubIssueDrift, sawBlockedByDrift bool
	for _, d := range report.Drift {
		if d.What == "charts/demo" && d.Field == "sub-issue" {
			sawSubIssueDrift = true
		}
		if d.What == "DEMO-2" && d.Field == "blocked_by" {
			sawBlockedByDrift = true
		}
	}
	if !sawSubIssueDrift {
		t.Fatalf("Drift = %+v, want a sub-issue drift line previewing NODE_HAND_ADDED's removal", report.Drift)
	}
	if !sawBlockedByDrift {
		t.Fatalf("Drift = %+v, want a blocked_by drift line previewing NODE_HAND_ADDED_BLOCKER's removal", report.Drift)
	}

	if len(client.subIssues) != 0 || len(client.blockedBys) != 0 || len(client.removedSubs) != 0 || len(client.removedBlockedBys) != 0 {
		t.Fatalf("dry run mutated a link: subIssues=%v blockedBys=%v removedSubs=%v removedBlockedBys=%v, want none",
			client.subIssues, client.blockedBys, client.removedSubs, client.removedBlockedBys)
	}

	for _, rel := range []string{
		filepath.Join(st.TicketRelDir("DEMO-1"), "tracker", "github.yaml"),
		filepath.Join(st.TicketRelDir("DEMO-2"), "tracker", "github.yaml"),
		filepath.Join("charts", "demo", "github.yaml"),
	} {
		rec := readTestRecord(t, filepath.Join(st.Root, rel))
		if rec.Synced != nil {
			t.Fatalf("%s's record = %+v, want synced still unset (dry run wrote nothing)", rel, rec)
		}
	}
}
