package mirror

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestTicketStatus covers the Status precedence:
// Done (a merged pull request) outranks In review (an open one), which
// outranks In progress (journal activity or a slice that left queued),
// which outranks Briefed (a brief.md), which outranks Backlog.
func TestTicketStatus(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	const ticket = "DEMO-1"
	if err := st.CreateTicketRecord(ticket, store.Ticket{}); err != nil {
		t.Fatal(err)
	}

	assertStatus := func(t *testing.T, prs []PRRef, want string) {
		t.Helper()
		got, err := TicketStatus(st, ticket, prs)
		if err != nil {
			t.Fatalf("TicketStatus: %v", err)
		}
		if got != want {
			t.Fatalf("TicketStatus = %q, want %q", got, want)
		}
	}

	t.Run("Backlog with nothing recorded", func(t *testing.T) {
		assertStatus(t, nil, "Backlog")
	})

	t.Run("Briefed once it has a brief.md", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "brief.md"), []byte("# DEMO-1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, nil, "Briefed")
	})

	// A ticket whose slices nobody has started is `queued` to jig status; the
	// board keeps it in the column it was in, never In progress.
	t.Run("Briefed while its slices are all queued", func(t *testing.T) {
		if err := st.AppendSlices(ticket, []store.Slice{{ID: "a", Goal: "do it"}}); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, nil, "Briefed")
	})

	t.Run("In progress once a slice has left queued", func(t *testing.T) {
		if err := st.WriteSliceState(ticket, "a", store.SliceState{State: "building"}); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, nil, "In progress")
	})

	t.Run("In review outranks In progress", func(t *testing.T) {
		assertStatus(t, []PRRef{{Owner: "o", Repo: "r", Number: 1, State: "OPEN"}}, "In review")
	})

	t.Run("Done outranks In review", func(t *testing.T) {
		prs := []PRRef{
			{Owner: "o", Repo: "r", Number: 1, State: "OPEN"},
			{Owner: "o", Repo: "r", Number: 2, State: "MERGED"},
		}
		assertStatus(t, prs, "Done")
	})
}

// TestTicketStatusInProgressFromJournalAlone checks that a non-empty
// journal alone (no slice having left queued) is enough for In progress - a
// ticket that adopted a branch has slices only once a gate round queues fix
// slices, per the ticket folder it reuses from store's own status rendering.
func TestTicketStatusInProgressFromJournalAlone(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	const ticket = "DEMO-1"
	if err := st.CreateTicketRecord(ticket, store.Ticket{}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, ticket, journal.Line{Event: "adopt"}); err != nil {
		t.Fatal(err)
	}
	got, err := TicketStatus(st, ticket, nil)
	if err != nil {
		t.Fatalf("TicketStatus: %v", err)
	}
	if got != "In progress" {
		t.Fatalf("TicketStatus = %q, want %q", got, "In progress")
	}
}

// TestChartStatus covers the chart Status rule.
func TestChartStatus(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"no tickets", nil, "Backlog"},
		{"every ticket backlog", []string{"Backlog", "Backlog"}, "Backlog"},
		{"one in progress", []string{"Backlog", "In progress"}, "In progress"},
		{"one in review", []string{"Backlog", "In review"}, "In progress"},
		{"one done, one backlog", []string{"Done", "Backlog"}, "In progress"},
		{"every ticket done", []string{"Done", "Done"}, "Done"},
		{"done or closed", []string{"Done", "Not planned"}, "Done"},
		{"every ticket closed", []string{"Not planned", "Not planned"}, "Done"},
		{"closed, one backlog", []string{"Not planned", "Backlog"}, "Backlog"},
		{"closed, one in progress", []string{"Not planned", "In progress"}, "In progress"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChartStatus(tc.statuses); got != tc.want {
				t.Fatalf("ChartStatus(%v) = %q, want %q", tc.statuses, got, tc.want)
			}
		})
	}
}

// TestIsOpen covers this rule: a Done issue is
// closed, and so is one whose ticket was closed without being built (Not
// planned); every other status is open.
func TestIsOpen(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"Backlog", true},
		{"Briefed", true},
		{"In progress", true},
		{"In review", true},
		{"Done", false},
		{"Not planned", false},
	}
	for _, tc := range cases {
		if got := IsOpen(tc.status); got != tc.want {
			t.Fatalf("IsOpen(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// prFakeClient is a github.Client stub that hands out fixed pull requests
// per "owner/repo", for findPullRequests's own tests.
type prFakeClient struct {
	byRepo map[string][]github.PullRequest
	// byID simulates PullRequestsByIDs: a node id GitHub still resolves to a
	// pull request, with its current state and repository. A node id with
	// no entry here is one GitHub no longer has.
	byID map[string]github.PullRequest
}

func (c *prFakeClient) CreateIssue(context.Context, string, string, string, string) (github.Issue, error) {
	return github.Issue{}, nil
}
func (c *prFakeClient) CloseIssueNotPlanned(context.Context, string) error { return nil }
func (c *prFakeClient) AddComment(context.Context, string, string) error   { return nil }
func (c *prFakeClient) PullRequestsByHeads(_ context.Context, owner, repo string, branches []string) (map[string][]github.PullRequest, error) {
	byHead := map[string][]github.PullRequest{}
	for _, branch := range branches {
		byHead[branch] = c.byRepo[owner+"/"+repo]
	}
	return byHead, nil
}
func (c *prFakeClient) PullRequestsByIDs(_ context.Context, nodeIDs []string) ([]github.PullRequest, error) {
	var prs []github.PullRequest
	for _, id := range nodeIDs {
		if pr, ok := c.byID[id]; ok {
			prs = append(prs, pr)
		}
	}
	return prs, nil
}
func (c *prFakeClient) AddSubIssue(context.Context, string, string) error     { return nil }
func (c *prFakeClient) AddBlockedBy(context.Context, string, string) error    { return nil }
func (c *prFakeClient) RemoveBlockedBy(context.Context, string, string) error { return nil }
func (c *prFakeClient) RemoveSubIssue(context.Context, string, string) error {
	return nil
}
func (c *prFakeClient) EnsureProject(context.Context, string, bool, int, string, string) (github.ProjectV2, error) {
	return github.ProjectV2{}, nil
}
func (c *prFakeClient) LookupProject(context.Context, string, bool, int) (bool, error) {
	return true, nil
}
func (c *prFakeClient) PlaceItem(context.Context, github.ProjectV2, string, string, string) (string, error) {
	return "", nil
}
func (c *prFakeClient) RepositoryIsPublic(context.Context, string, string) (bool, error) {
	return true, nil
}
func (c *prFakeClient) FetchIssues(context.Context, []string) (map[string]github.IssueState, error) {
	return nil, nil
}
func (c *prFakeClient) UpdateIssue(context.Context, string, string, string) error { return nil }
func (c *prFakeClient) CloseIssueCompleted(context.Context, string) error         { return nil }
func (c *prFakeClient) ReopenIssue(context.Context, string) error                 { return nil }
func (c *prFakeClient) ItemFieldValues(context.Context, []string) (map[string]github.ItemFields, error) {
	return nil, nil
}

// TestFindPullRequestsOrdersByRepoThenNumber checks the ordering: per repo in the project's own order, by number
// within each.
func TestFindPullRequestsOrdersByRepoThenNumber(t *testing.T) {
	client := &prFakeClient{byRepo: map[string][]github.PullRequest{
		"example/first":  {{Number: 9, State: "OPEN"}, {Number: 2, State: "MERGED"}},
		"example/second": {{Number: 1, State: "CLOSED"}},
	}}
	cfg := project.Config{Repos: []project.Repo{
		{Remote: "git@github.com:example/first.git"},
		{Remote: "git@github.com:example/second.git"},
	}}
	got, _, err := findOne(client, cfg, "jig/DEMO-1", nil)
	if err != nil {
		t.Fatalf("findPullRequests: %v", err)
	}
	want := []PRRef{
		{Owner: "example", Repo: "first", Number: 2, State: "MERGED"},
		{Owner: "example", Repo: "first", Number: 9, State: "OPEN"},
		{Owner: "example", Repo: "second", Number: 1, State: "CLOSED"},
	}
	if len(got) != len(want) {
		t.Fatalf("findPullRequests = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("findPullRequests[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestFindPullRequestsRefetchesARecordedOneTheHeadQueryMisses checks the
// fallback: a pull request recorded in
// prs: that the head query no longer finds - typically a merged pull
// request whose head branch GitHub has since deleted - is re-fetched by
// node id for its current state and repository, rather than kept with the
// no-state record.go carries (prRecord has no state field at all), which
// would otherwise never let it count toward Done.
func TestFindPullRequestsRefetchesARecordedOneTheHeadQueryMisses(t *testing.T) {
	client := &prFakeClient{
		byRepo: map[string][]github.PullRequest{},
		byID:   map[string]github.PullRequest{"PR_99": {Number: 99, NodeID: "PR_99", State: "MERGED", Owner: "example", Repo: "demo"}},
	}
	cfg := project.Config{Repos: []project.Repo{{Remote: "git@github.com:example/demo.git"}}}
	recorded := []PRRef{{Owner: "example", Repo: "demo", Number: 99, NodeID: "PR_99"}}

	got, _, err := findOne(client, cfg, "jig/T-34", recorded)
	if err != nil {
		t.Fatalf("findPullRequests: %v", err)
	}
	want := PRRef{Owner: "example", Repo: "demo", Number: 99, NodeID: "PR_99", State: "MERGED"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("findPullRequests = %+v, want [%+v] with its current state", got, want)
	}
}

// TestFindPullRequestsKeepsARecordedOneGitHubNoLongerHas checks the human's
// decision: a recorded pull request neither the head query nor
// a fetch by its node id turns up - GitHub answered NOT_FOUND, which can
// mean the token lost access rather than that the pull request is truly
// gone - stays listed and in the record with no State, and is reported in
// gone so the caller can warn the operator.
func TestFindPullRequestsKeepsARecordedOneGitHubNoLongerHas(t *testing.T) {
	client := &prFakeClient{byRepo: map[string][]github.PullRequest{}, byID: map[string]github.PullRequest{}}
	cfg := project.Config{Repos: []project.Repo{{Remote: "git@github.com:example/demo.git"}}}
	recorded := []PRRef{{Owner: "example", Repo: "demo", Number: 3, NodeID: "PR_3"}}

	got, gone, err := findOne(client, cfg, "jig/DEMO-1", recorded)
	if err != nil {
		t.Fatalf("findPullRequests: %v", err)
	}
	want := PRRef{Owner: "example", Repo: "demo", Number: 3, NodeID: "PR_3"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("findPullRequests = %+v, want it kept with no State, [%+v]", got, want)
	}
	wantGone := GoneRecordedPR{Owner: "example", Repo: "demo", Number: 3}
	if len(gone) != 1 || gone[0] != wantGone {
		t.Fatalf("gone = %+v, want [%+v]", gone, wantGone)
	}
}

// TestFindPullRequestsKeepsARecordedOneWithNoNodeID checks that records the bridge wrote are
// "adopted as they are", and the human's decision: a recorded prs:
// entry with no node_id (hand-edited, or written by a bridge version that
// omitted it) stays listed and in the record with no State, never sent to
// PullRequestsByIDs as an empty id - client.PullRequestsByIDs would fail
// the call outright on an empty id, so this also checks the other recorded
// entries still get re-checked.
func TestFindPullRequestsKeepsARecordedOneWithNoNodeID(t *testing.T) {
	client := &prFakeClient{
		byRepo: map[string][]github.PullRequest{},
		byID:   map[string]github.PullRequest{"PR_99": {Number: 99, NodeID: "PR_99", State: "MERGED", Owner: "example", Repo: "demo"}},
	}
	cfg := project.Config{Repos: []project.Repo{{Remote: "git@github.com:example/demo.git"}}}
	recorded := []PRRef{
		{Owner: "example", Repo: "demo", Number: 3, NodeID: ""},
		{Owner: "example", Repo: "demo", Number: 99, NodeID: "PR_99"},
	}

	got, gone, err := findOne(client, cfg, "jig/DEMO-1", recorded)
	if err != nil {
		t.Fatalf("findPullRequests: %v", err)
	}
	wantNoID := PRRef{Owner: "example", Repo: "demo", Number: 3}
	wantMerged := PRRef{Owner: "example", Repo: "demo", Number: 99, NodeID: "PR_99", State: "MERGED"}
	if len(got) != 2 || got[0] != wantNoID || got[1] != wantMerged {
		t.Fatalf("findPullRequests = %+v, want [%+v %+v], the no-node_id entry kept with no State", got, wantNoID, wantMerged)
	}
	if len(gone) != 0 {
		t.Fatalf("gone = %+v, want none: a no-node_id entry is never sent to PullRequestsByIDs", gone)
	}
}

// TestFindPullRequestsDropsARecordedOneTheQueryFinds checks that a
// recorded pull request the query re-finds is not duplicated.
func TestFindPullRequestsDropsARecordedOneTheQueryFinds(t *testing.T) {
	client := &prFakeClient{byRepo: map[string][]github.PullRequest{
		"example/demo": {{Number: 3, State: "MERGED"}},
	}}
	cfg := project.Config{Repos: []project.Repo{{Remote: "git@github.com:example/demo.git"}}}
	recorded := []PRRef{{Owner: "example", Repo: "demo", Number: 3, State: "OPEN"}}

	got, _, err := findOne(client, cfg, "jig/DEMO-1", recorded)
	if err != nil {
		t.Fatalf("findPullRequests: %v", err)
	}
	if len(got) != 1 || got[0].State != "MERGED" {
		t.Fatalf("findPullRequests = %+v, want the query's own (fresher) state, not duplicated", got)
	}
}

// findOne is findPullRequests for one ticket's branch and recorded pull
// requests, the shape the single-ticket tests above ask in.
func findOne(client github.Client, cfg project.Config, branch string, recorded []PRRef) ([]PRRef, []GoneRecordedPR, error) {
	found, err := findPullRequests(context.Background(), client, cfg, []prLookup{{branch: branch, recorded: recorded}})
	if err != nil {
		return nil, nil, err
	}
	return found[0].prs, found[0].gone, nil
}

// TestFindPullRequestsKeepsEachTicketsOwnInOneBatch checks a batch of
// lookups hands each ticket its own pull requests: its own head matches,
// then its own recorded entries re-checked by id, with a gone one reported
// against it alone, all from one head query and one by-id query.
func TestFindPullRequestsKeepsEachTicketsOwnInOneBatch(t *testing.T) {
	client := &fakeClient{
		prs: map[string][]github.PullRequest{
			"example/demo@1-first":  {{Number: 7, NodeID: "PR_7", State: "OPEN"}},
			"example/demo@2-second": {{Number: 8, NodeID: "PR_8", State: "CLOSED"}},
		},
		prsByID: map[string]github.PullRequest{
			"PR_3": {Number: 3, NodeID: "PR_3", State: "MERGED", Owner: "example", Repo: "demo"},
		},
	}
	cfg := project.Config{Repos: []project.Repo{{Remote: "https://github.com/example/demo"}}}
	found, err := findPullRequests(context.Background(), client, cfg, []prLookup{
		{branch: "1-first", recorded: []PRRef{{Owner: "example", Repo: "demo", Number: 4, NodeID: "PR_4"}}},
		{branch: "2-second", recorded: []PRRef{{Owner: "example", Repo: "demo", Number: 3, NodeID: "PR_3"}}},
	})
	if err != nil {
		t.Fatalf("findPullRequests: %v", err)
	}
	wantFirst := []PRRef{
		{Owner: "example", Repo: "demo", Number: 7, NodeID: "PR_7", State: "OPEN"},
		{Owner: "example", Repo: "demo", Number: 4, NodeID: "PR_4"},
	}
	wantSecond := []PRRef{
		{Owner: "example", Repo: "demo", Number: 8, NodeID: "PR_8", State: "CLOSED"},
		{Owner: "example", Repo: "demo", Number: 3, NodeID: "PR_3", State: "MERGED"},
	}
	if len(found) != 2 || !equalPRRefs(found[0].prs, wantFirst) || !equalPRRefs(found[1].prs, wantSecond) {
		t.Fatalf("findPullRequests = %+v, want the first ticket %+v and the second %+v", found, wantFirst, wantSecond)
	}
	if len(found[0].gone) != 1 || found[0].gone[0].Number != 4 || len(found[1].gone) != 0 {
		t.Fatalf("gone = %+v and %+v, want #4 against the first ticket only", found[0].gone, found[1].gone)
	}
	if client.reads["PullRequestsByHeads"] != 1 || client.reads["PullRequestsByIDs"] != 1 {
		t.Fatalf("reads = %v, want one head query and one by-id query for both tickets", client.reads)
	}
}

func equalPRRefs(a, b []PRRef) bool {
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
