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
	enableSymlinks(t, work)

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
	// project or a token without the project scope.
	projectErr error
	// private, when true, makes RepositoryIsPublic report false, as a store
	// whose issue repo is private (a private
	// issue repo is not scanned).
	private bool

	// issues simulates every issue's current GitHub state, keyed by node id:
	// CreateIssue, UpdateIssue, CloseIssueCompleted/CloseIssueNotPlanned and
	// ReopenIssue all mutate it, FetchIssue reads it back, and a test can
	// mutate it directly to simulate an edit made on GitHub.
	issues map[string]*fakeIssueState
	// subIssuesOf maps a parent issue's node id to its sub-issues', in the
	// order AddSubIssue added them; parentOf is its inverse.
	subIssuesOf map[string][]string
	parentOf    map[string]string
	// blockedByOf maps an issue's node id to the node ids it is blocked by.
	blockedByOf map[string][]string
	// goneNodeIDs are node ids FetchIssues leaves out of its answer once, as
	// GitHub does for an issue it no longer has (a
	// recorded issue that is gone from GitHub gets a new issue).
	goneNodeIDs map[string]bool
	updates     []fakeUpdate
	closedDone  []string // node ids CloseIssueCompleted was called on
	reopened    []string // node ids ReopenIssue was called on
	// autoClosed is the node ids of the open issues PlaceItem closed by
	// moving their card to Done, as a project's built-in Auto-close issue
	// workflow does, which new projects have on.
	autoClosed []string
	// numbers maps an issue's number to its node id, what SetCloseReason, a
	// REST write, names an issue by: CreateIssue fills it, and a test fills it
	// for an adopted record.
	numbers map[int]string
	// cardAhead is every card of an issue moved to Done or Not planned before
	// the issue was closed, or before its close reason matched the card.
	cardAhead    []string
	reasonWrites []fakeReasonWrite
	reasonErr    error // if set, every SetCloseReason call fails with it
	removedSubs  []fakeSubIssue
	// itemFields simulates a board item's current Status and Store ID,
	// keyed by item id: PlaceItem writes it, ItemFieldValues reads it back,
	// and a test can mutate it directly to simulate an edit made on GitHub.
	itemFields map[string]fakeItemFields
	// reads counts each read call by method name, so a test can pin how
	// many round trips a sync makes.
	reads map[string]int
	// items simulates the tracker items ItemByNumber reads, by number.
	items map[int]github.ItemDetail
}

// read counts one call of the read method name.
func (c *fakeClient) read(name string) {
	if c.reads == nil {
		c.reads = map[string]int{}
	}
	c.reads[name]++
}

// fakeIssueState is one issue's simulated GitHub state.
type fakeIssueState struct {
	Title string
	Body  string
	Open  bool
	// Reason is why a closed issue was closed: "completed", "not planned" or,
	// set by a test, "duplicate"; empty while it is open, and for one closed
	// with no reason.
	Reason string
}

// fakeReasonWrite is one SetCloseReason call fakeClient saw.
type fakeReasonWrite struct {
	Owner, Repo string
	Number      int
	Reason      string
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
	c.number(c.next, nodeID)
	return github.Issue{Number: c.next, NodeID: nodeID}, nil
}

// number records that issue number n is nodeID.
func (c *fakeClient) number(n int, nodeID string) {
	if c.numbers == nil {
		c.numbers = map[int]string{}
	}
	c.numbers[n] = nodeID
}

// SetCloseReason refuses an open issue, which the REST write would close: jig
// sets the reason only of an issue GitHub already holds closed.
func (c *fakeClient) SetCloseReason(_ context.Context, owner, repo string, number int, reason string) error {
	c.reasonWrites = append(c.reasonWrites, fakeReasonWrite{Owner: owner, Repo: repo, Number: number, Reason: reason})
	if c.reasonErr != nil {
		return c.reasonErr
	}
	nodeID, ok := c.numbers[number]
	if !ok {
		return fmt.Errorf("fake: no issue %s/%s#%d", owner, repo, number)
	}
	is := c.issue(nodeID)
	if is.Open {
		return fmt.Errorf("fake: %s/%s#%d is open", owner, repo, number)
	}
	is.Reason = strings.ReplaceAll(reason, "_", " ")
	return nil
}

func (c *fakeClient) CloseIssueNotPlanned(_ context.Context, nodeID string) error {
	c.closed = append(c.closed, nodeID)
	st := c.issue(nodeID)
	st.Open, st.Reason = false, "not planned"
	return nil
}

func (c *fakeClient) AddComment(_ context.Context, subjectID, body string) error {
	c.comments = append(c.comments, fakeComment{SubjectID: subjectID, Body: body})
	return nil
}

func (c *fakeClient) PullRequestsByHeads(_ context.Context, owner, repo string, branches []string) (map[string][]github.PullRequest, error) {
	c.read("PullRequestsByHeads")
	byHead := map[string][]github.PullRequest{}
	for _, branch := range branches {
		if prs := c.prs[owner+"/"+repo+"@"+branch]; len(prs) > 0 {
			byHead[branch] = prs
		}
	}
	return byHead, nil
}

func (c *fakeClient) PullRequestsByIDs(_ context.Context, nodeIDs []string) ([]github.PullRequest, error) {
	c.read("PullRequestsByIDs")
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

func (c *fakeClient) FetchIssues(_ context.Context, nodeIDs []string) (map[string]github.IssueState, error) {
	c.read("FetchIssues")
	states := map[string]github.IssueState{}
	for _, nodeID := range nodeIDs {
		if c.goneNodeIDs[nodeID] {
			delete(c.goneNodeIDs, nodeID)
			continue
		}
		st := c.issue(nodeID)
		states[nodeID] = github.IssueState{
			Title: st.Title, Body: st.Body, Open: st.Open, StateReason: strings.ToUpper(strings.ReplaceAll(st.Reason, " ", "_")),
			ParentID: c.parentOf[nodeID], SubIssueIDs: c.subIssuesOf[nodeID], BlockedByIDs: c.blockedByOf[nodeID],
		}
	}
	return states, nil
}

func (c *fakeClient) UpdateIssue(_ context.Context, nodeID, title, body string) error {
	c.updates = append(c.updates, fakeUpdate{NodeID: nodeID, Title: title, Body: body})
	st := c.issue(nodeID)
	st.Title, st.Body = title, body
	return nil
}

func (c *fakeClient) CloseIssueCompleted(_ context.Context, nodeID string) error {
	c.closedDone = append(c.closedDone, nodeID)
	st := c.issue(nodeID)
	st.Open, st.Reason = false, "completed"
	return nil
}

func (c *fakeClient) ReopenIssue(_ context.Context, nodeID string) error {
	c.reopened = append(c.reopened, nodeID)
	st := c.issue(nodeID)
	st.Open, st.Reason = true, ""
	return nil
}

func (c *fakeClient) ItemFieldValues(_ context.Context, itemIDs []string) (map[string]github.ItemFields, error) {
	c.read("ItemFieldValues")
	fields := map[string]github.ItemFields{}
	for _, id := range itemIDs {
		if f, ok := c.itemFields[id]; ok {
			fields[id] = github.ItemFields{Status: f.Status, StoreID: f.StoreID}
		}
	}
	return fields, nil
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
		"In review": "OPT_In_review", "Done": "OPT_Done", "Not planned": "OPT_Not_planned",
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
	if is, ok := c.issues[contentID]; ok && !IsOpen(status) {
		wrong := status == StatusNotPlanned && (is.Reason == "completed" || is.Reason == "") ||
			status == StatusDone && (is.Reason == "not planned" || is.Reason == "duplicate")
		if is.Open || wrong {
			c.cardAhead = append(c.cardAhead, fmt.Sprintf("%s to %q with the issue open %v, closed as %q", storeID, status, is.Open, is.Reason))
		}
	}
	c.nextItem++
	itemID := fmt.Sprintf("ITEM_%d", c.nextItem)
	if c.itemFields == nil {
		c.itemFields = map[string]fakeItemFields{}
	}
	c.itemFields[itemID] = fakeItemFields{Status: status, StoreID: storeID}
	// A pull request has no entry in issues, so only an issue closes.
	if is, ok := c.issues[contentID]; ok && is.Open && status == StatusDone {
		is.Open, is.Reason = false, "completed"
		c.autoClosed = append(c.autoClosed, contentID)
	}
	return itemID, nil
}

// closes counts how often nodeID's issue was closed, by jig's mutations or by
// the Auto-close workflow.
func (c *fakeClient) closes(nodeID string) int {
	n := 0
	for _, list := range [][]string{c.closedDone, c.closed, c.autoClosed} {
		for _, id := range list {
			if id == nodeID {
				n++
			}
		}
	}
	return n
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
// per the issue rendering, writes each record where the bridge
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
		data, err := os.ReadFile(treeRecordPath(t, st, what))
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
		out := runGit(t, "", "--git-dir", remote, "log", "--oneline", "--all", "--", filepath.ToSlash(filepath.Join("trackers", "github", "example", "tracking", string(rune('1'+i)), "record.yaml")))
		if !strings.Contains(out, what+": github issue example/tracking#"+string(rune('1'+i))) {
			t.Fatalf("remote history for %s = %q, want a commit naming the issue", what, out)
		}
	}
	if _, err := os.Stat(treeRecordPath(t, st, "charts/demo")); err != nil {
		t.Fatalf("chart demo's record was not written: %v", err)
	}

	// A claim is one commit of the record and its link, pushed at once, so
	// the committed tree is whole at every step and the next clone has both.
	if st.SymlinksOn() {
		files := runGit(t, "", "--git-dir", remote, "log", "--name-only", "--format=%s", "-1", "main", "--", "trackers/github/example/tracking/1")
		for _, want := range []string{"DEMO-1: github issue example/tracking#1", "trackers/github/example/tracking/1/record.yaml", "trackers/github/example/tracking/1/DEMO-1\n"} {
			if !strings.Contains(files+"\n", want) {
				t.Errorf("the claim commit =\n%s\nwant it to hold %q", files, want)
			}
		}
		entry := runGit(t, "", "--git-dir", remote, "ls-tree", "main", "trackers/github/example/tracking/1/DEMO-1")
		if !strings.HasPrefix(entry, "120000 blob") {
			t.Errorf("the pushed entry = %q, want a symlink (mode 120000)", entry)
		}
		clone := t.TempDir()
		runGit(t, "", "clone", remote, clone)
		enableSymlinks(t, clone)
		runGit(t, clone, "checkout", "--", "trackers")
		cs, err := store.Open(clone)
		if err != nil {
			t.Fatal(err)
		}
		if problems, err := cs.CheckTrackerTree(); err != nil || len(problems) != 0 {
			t.Errorf("CheckTrackerTree of a fresh clone = %+v, %v; want nothing wrong", problems, err)
		}
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

// TestSyncBoundedTimeoutNamesRemainingItemsAndTrackersSync covers this case: a
// sync from inside a command (SyncOpts{}, the checkpoint hook's default) that
// runs out of its bound names how many tickets or charts still have no GitHub
// issue and points at `jig trackers sync`, rather than just surfacing the bare
// context.DeadlineExceeded every client call already reported on its own
// (TestSyncReportsAClientFailure).
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

// TestSyncAdoptsAnotherClonesIssueOnARejectedPush is the "two clones creating
// the same ticket's issue" critical path: another clone claims DEMO-1's record
// and pushes it first, so this Sync's own push for the issue it just opened is
// rejected; the pull that follows brings the other clone's record in, so this
// clone must close its own issue as not planned (naming the other in a
// comment), drop its own record and adopt the other's - ending with one open
// issue and one record.
func TestSyncAdoptsAnotherClonesIssueOnARejectedPush(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	otherRecord := recordHeader + "ticket: DEMO-1\nrepo: example/tracking\nissue: 99\nnode_id: NODE_OTHER\n"
	otherDir := filepath.Join(other, "trackers", "github", "example", "tracking")
	otherFile := filepath.Join(otherDir, "99", "record.yaml")
	if err := os.MkdirAll(filepath.Dir(otherFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherFile, []byte(otherRecord), 0o644); err != nil {
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

	data, err := os.ReadFile(treeRecordPath(t, st, "DEMO-1"))
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

	otherPath := "trackers/github/example/tracking/99/record.yaml"
	remoteLog := runGit(t, "", "--git-dir", remote, "log", "--oneline", "--all", "--", otherPath)
	if n := strings.Count(remoteLog, "DEMO-1: github issue"); n != 1 {
		t.Fatalf("remote history for DEMO-1's record has %d claiming commits, want exactly 1 (the rejected attempt left nothing behind)", n)
	}

	// The claim itself left nothing behind beyond the adopted record; the
	// board placement that follows is an ordinary working-tree write (not a
	// Claim), going in with the next checkpoint's commit, so the only dirty file is
	// DEMO-1's own record, carrying the item: the board step just set, and
	// its link, which the other clone's record came without.
	want := "M trackers/github/example/tracking/99/record.yaml"
	if st.SymlinksOn() {
		want += "\n?? trackers/github/example/tracking/99/DEMO-1"
	}
	status := runGit(t, work, "status", "--porcelain")
	if strings.TrimSpace(status) != want {
		t.Fatalf("store dirty after Sync = %q, want only DEMO-1's record (the board step's item: write) and its link: %q", status, want)
	}
}
