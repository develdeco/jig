package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
)

// newFastClient is New, with MutationInterval and BackoffBase set small
// enough that this package's own tests never actually wait a second: a
// test that wants to assert the real pacing or backoff (
// TestDoThrottlesMutationsOneAtATime, TestDoRetriesWithBackoff) builds its
// own *GraphQLClient instead, with whatever small duration it wants to
// measure against.
func newFastClient(endpoint, token string) *GraphQLClient {
	c := New(endpoint, token)
	c.MutationInterval = time.Nanosecond
	c.BackoffBase = time.Nanosecond
	return c
}

// fakeGraphQL is a minimal fake GraphQL server: a repository query and a
// createIssue mutation, matched by substring on the request's own query
// text, as real GitHub test doubles in this codebase do for other APIs.
// authHeader, when set, is the exact Authorization header every request
// must carry; a mismatch fails the request as GitHub itself would (401).
func fakeGraphQL(t *testing.T, authHeader string) *httptest.Server {
	t.Helper()
	nextIssue := 1
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authHeader != "" && r.Header.Get("Authorization") != authHeader {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Bad credentials"}]}`))
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request body %s: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "repository(owner"):
			owner, _ := req.Variables["owner"].(string)
			name, _ := req.Variables["name"].(string)
			if owner == "" || name == "" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"errors":[{"message":"missing owner or name"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"repository":{"id":"REPO_` + owner + "_" + name + `","isPrivate":false}}}`))
		case strings.Contains(req.Query, "createIssue"):
			n := nextIssue
			nextIssue++
			title, _ := req.Variables["title"].(string)
			resp := map[string]any{
				"data": map[string]any{
					"createIssue": map[string]any{
						"issue": map[string]any{
							"number": n,
							"id":     "ISSUE_" + title,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Fatalf("fake GraphQL server got an unrecognized query: %s", req.Query)
		}
	}))
}

// TestCreateIssueResolvesRepositoryThenCreates checks CreateIssue's two-step
// shape: it looks up the repository's node id first, then sends it as
// createIssue's repositoryId, returning the number and node id the fake
// server hands back.
func TestCreateIssueResolvesRepositoryThenCreates(t *testing.T) {
	srv := fakeGraphQL(t, "bearer test-token")
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	issue, err := c.CreateIssue(context.Background(), "example", "demo", "DEMO-7: Mirror", "marker body")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.Number != 1 {
		t.Fatalf("Number = %d, want 1", issue.Number)
	}
	if issue.NodeID != "ISSUE_DEMO-7: Mirror" {
		t.Fatalf("NodeID = %q, want it to carry the title the fake server echoed", issue.NodeID)
	}
}

// TestCreateIssueCachesRepositoryID checks that two CreateIssue calls in the
// same owner/repo resolve the repository id once: the fake server would
// otherwise mint a fresh (and here, identical, since it is keyed by
// owner/name) id on every call, so this is really checking repositoryID's
// own cache is consulted rather than skipped.
func TestCreateIssueCachesRepositoryID(t *testing.T) {
	var repoQueries int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "repository(owner") {
			repoQueries++
			_, _ = w.Write([]byte(`{"data":{"repository":{"id":"REPO_1"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"createIssue":{"issue":{"number":1,"id":"ISSUE_1"}}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if _, err := c.CreateIssue(context.Background(), "example", "demo", "one", "body"); err != nil {
		t.Fatalf("CreateIssue 1: %v", err)
	}
	if _, err := c.CreateIssue(context.Background(), "example", "demo", "two", "body"); err != nil {
		t.Fatalf("CreateIssue 2: %v", err)
	}
	if repoQueries != 1 {
		t.Fatalf("repository was queried %d times, want 1 (cached after the first)", repoQueries)
	}
}

// TestRepositoryIsPublicReportsVisibilityAndCaches checks that
// RepositoryIsPublic reads isPrivate off the repository query, inverts it,
// and (same as CreateIssue's own repositoryID lookup) resolves owner/repo
// once even across two calls.
func TestRepositoryIsPublicReportsVisibilityAndCaches(t *testing.T) {
	var repoQueries int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "repository(owner") {
			repoQueries++
			_, _ = w.Write([]byte(`{"data":{"repository":{"id":"REPO_1","isPrivate":true}}}`))
			return
		}
		t.Fatalf("unexpected query: %s", req.Query)
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	public, err := c.RepositoryIsPublic(context.Background(), "example", "secret")
	if err != nil {
		t.Fatalf("RepositoryIsPublic 1: %v", err)
	}
	if public {
		t.Fatalf("public = true, want false for a private repository")
	}
	if _, err := c.RepositoryIsPublic(context.Background(), "example", "secret"); err != nil {
		t.Fatalf("RepositoryIsPublic 2: %v", err)
	}
	if repoQueries != 1 {
		t.Fatalf("repository was queried %d times, want 1 (cached after the first)", repoQueries)
	}
}

// TestCreateIssueReportsGraphQLErrors checks that a GraphQL "errors" array
// (here, an unresolvable repository) surfaces as a Go error naming it,
// rather than CreateIssue silently returning a zero Issue.
func TestCreateIssueReportsGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Could not resolve to a Repository"}]}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if _, err := c.CreateIssue(context.Background(), "nobody", "ghost", "t", "b"); err == nil {
		t.Fatal("CreateIssue: want an error for a GraphQL errors response")
	} else if !strings.Contains(err.Error(), "Could not resolve to a Repository") {
		t.Fatalf("err = %v, want it to name the GraphQL error", err)
	}
}

// TestPullRequestsByHeadSortsByNumber checks PullRequestsByHead decodes the
// fake server's nodes and returns them ordered by number, whatever order
// the server listed them in.
func TestPullRequestsByHeadSortsByNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		if !strings.Contains(req.Query, "pullRequests(headRefName") {
			t.Fatalf("unexpected query: %s", req.Query)
		}
		owner, _ := req.Variables["owner"].(string)
		name, _ := req.Variables["name"].(string)
		branch, _ := req.Variables["branch"].(string)
		if owner != "example" || name != "demo" || branch != "jig/DEMO-7" {
			t.Fatalf("variables = owner=%q name=%q branch=%q, want example/demo jig/DEMO-7", owner, name, branch)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequests":{"nodes":[
			{"number":31,"id":"PR_31","state":"MERGED"},
			{"number":5,"id":"PR_5","state":"OPEN"}
		]}}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	prs, err := c.PullRequestsByHead(context.Background(), "example", "demo", "jig/DEMO-7")
	if err != nil {
		t.Fatalf("PullRequestsByHead: %v", err)
	}
	if len(prs) != 2 || prs[0].Number != 5 || prs[1].Number != 31 {
		t.Fatalf("PullRequestsByHead = %+v, want [5, 31] in number order", prs)
	}
	if prs[0].State != "OPEN" || prs[1].State != "MERGED" {
		t.Fatalf("PullRequestsByHead states = %+v, want OPEN then MERGED", prs)
	}
	if prs[1].NodeID != "PR_31" {
		t.Fatalf("PullRequestsByHead[1].NodeID = %q, want %q", prs[1].NodeID, "PR_31")
	}
}

// TestPullRequestsByHeadMatchesExactlyNotAsAPrefix pins the bug this slice
// fixes: GitHub's search (head: as a prefix) matched a branch like
// "jig/T-23" when querying "jig/T-2", wrongly handing T-2 another ticket's
// pull request. The fake server here holds one pull request whose head is
// "jig/T-23", which merely starts with the queried branch "jig/T-2": a
// query shaped like GitHub's search (matched here by "search(" in the
// query text) gets it back, as a prefix search really would, while
// Repository.pullRequests(headRefName:)'s own exact field does not. So this
// test fails if PullRequestsByHead ever regresses to a search-shaped query,
// and passes as long as it keeps asking headRefName for the exact branch.
func TestPullRequestsByHeadMatchesExactlyNotAsAPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		branch, _ := req.Variables["branch"].(string)
		w.Header().Set("Content-Type", "application/json")
		const actualHead = "jig/T-23"
		if strings.Contains(req.Query, "search(") {
			// GitHub's search really does match head: as a prefix.
			if strings.HasPrefix(actualHead, branch) {
				_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequests":{"nodes":[
					{"number":102,"id":"PR_102","state":"OPEN"}
				]}}}}`))
				return
			}
		} else if branch == actualHead {
			_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequests":{"nodes":[
				{"number":102,"id":"PR_102","state":"OPEN"}
			]}}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequests":{"nodes":[]}}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	prs, err := c.PullRequestsByHead(context.Background(), "develdeco", "jig", "jig/T-2")
	if err != nil {
		t.Fatalf("PullRequestsByHead: %v", err)
	}
	for _, pr := range prs {
		if pr.Number == 102 {
			t.Fatalf("PullRequestsByHead = %+v, want T-23's #102 excluded: its head merely starts with T-2's branch", prs)
		}
	}
}

// TestPullRequestsByIDsSkipsAGoneNode checks PullRequestsByIDs decodes a
// mix of resolved pull requests (with their own repository) and a null node
// (one GitHub no longer has), returning only the resolved ones. The fake
// server answers in the real API's own shape (confirmed against the live
// API): a NOT_FOUND "errors" entry at path ["nodes", 1] alongside the null,
// not a bare null with no "errors" array.
func TestPullRequestsByIDsSkipsAGoneNode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		if !strings.Contains(req.Query, "nodes(ids:") {
			t.Fatalf("unexpected query: %s", req.Query)
		}
		ids, _ := req.Variables["ids"].([]any)
		if len(ids) != 2 || ids[0] != "PR_99" || ids[1] != "PR_100" {
			t.Fatalf("ids = %v, want [PR_99, PR_100]", ids)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"nodes":[
			{"number":99,"id":"PR_99","state":"MERGED","repository":{"owner":{"login":"example"},"name":"demo"}},
			null
		]},"errors":[{"type":"NOT_FOUND","path":["nodes",1],"message":"Could not resolve to a node with the global id of 'PR_100'."}]}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	prs, err := c.PullRequestsByIDs(context.Background(), []string{"PR_99", "PR_100"})
	if err != nil {
		t.Fatalf("PullRequestsByIDs: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 99 || prs[0].State != "MERGED" || prs[0].Owner != "example" || prs[0].Repo != "demo" {
		t.Fatalf("PullRequestsByIDs = %+v, want one resolved pull request, the gone one dropped", prs)
	}
}

// TestPullRequestsByIDsFailsOnAnErrorThatIsNotAGoneNode checks
// toleratingGoneNodes only swallows the NOT_FOUND-on-nodes[i] shape: any
// other "errors" entry - here, a plain server-side error with no path -
// still fails the call, as do makes any non-empty "errors" array fatal by
// default.
func TestPullRequestsByIDsFailsOnAnErrorThatIsNotAGoneNode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"nodes":[null]},"errors":[{"message":"something went wrong"}]}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if _, err := c.PullRequestsByIDs(context.Background(), []string{"PR_100"}); err == nil {
		t.Fatal("PullRequestsByIDs: want an error, got nil")
	}
}

// TestAddSubIssueAndAddBlockedBySendTheirMutations checks AddSubIssue and
// AddBlockedBy post the mutation brief.md#The GitHub client names, with the
// node ids they were given as variables.
func TestAddSubIssueAndAddBlockedBySendTheirMutations(t *testing.T) {
	var queries []string
	var vars []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		queries = append(queries, req.Query)
		vars = append(vars, req.Variables)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if err := c.AddSubIssue(context.Background(), "PARENT", "CHILD"); err != nil {
		t.Fatalf("AddSubIssue: %v", err)
	}
	if err := c.AddBlockedBy(context.Background(), "ISSUE", "BLOCKER"); err != nil {
		t.Fatalf("AddBlockedBy: %v", err)
	}

	if !strings.Contains(queries[0], "addSubIssue") {
		t.Fatalf("AddSubIssue sent query %q, want it to call addSubIssue", queries[0])
	}
	if vars[0]["issueId"] != "PARENT" || vars[0]["subIssueId"] != "CHILD" {
		t.Fatalf("AddSubIssue vars = %v, want issueId=PARENT, subIssueId=CHILD", vars[0])
	}
	if !strings.Contains(queries[1], "addBlockedBy") {
		t.Fatalf("AddBlockedBy sent query %q, want it to call addBlockedBy", queries[1])
	}
	if vars[1]["issueId"] != "ISSUE" || vars[1]["blockedByIssueId"] != "BLOCKER" {
		t.Fatalf("AddBlockedBy vars = %v, want issueId=ISSUE, blockedByIssueId=BLOCKER", vars[1])
	}
}

// TestFetchIssueReadsCurrentState checks FetchIssue decodes an issue's
// title, body, closed state, parent and sub-issue/blocked-by node ids off
// the fake server's node query.
func TestFetchIssueReadsCurrentState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		if !strings.Contains(req.Query, "... on Issue") {
			t.Fatalf("unexpected query: %s", req.Query)
		}
		if req.Variables["id"] != "ISSUE_1" {
			t.Fatalf("id = %v, want ISSUE_1", req.Variables["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"node":{
			"title":"A title","body":"A body","closed":true,
			"parent":{"id":"PARENT_1"},
			"subIssues":{"nodes":[{"id":"SUB_1"}]},
			"blockedBy":{"nodes":[{"id":"BLOCKER_1"}]}
		}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	state, err := c.FetchIssue(context.Background(), "ISSUE_1")
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if state.Title != "A title" || state.Body != "A body" || state.Open {
		t.Fatalf("state = %+v, want title/body set and Open false", state)
	}
	if state.ParentID != "PARENT_1" || len(state.SubIssueIDs) != 1 || state.SubIssueIDs[0] != "SUB_1" {
		t.Fatalf("state = %+v, want parent PARENT_1 and sub-issue SUB_1", state)
	}
	if len(state.BlockedByIDs) != 1 || state.BlockedByIDs[0] != "BLOCKER_1" {
		t.Fatalf("state.BlockedByIDs = %v, want [BLOCKER_1]", state.BlockedByIDs)
	}
}

// TestFetchIssueReportsNotFound checks FetchIssue reports ErrIssueNotFound
// when node resolves to null - the issue (or its repository) is gone
// (brief.md#Adoption).
func TestFetchIssueReportsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"node":null}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if _, err := c.FetchIssue(context.Background(), "GONE"); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("FetchIssue err = %v, want ErrIssueNotFound", err)
	}
}

// TestUpdateIssueCloseReopenAndRemoveSubIssueSendTheirMutations checks each
// mutation posts the right shape with the node ids it was given.
func TestUpdateIssueCloseReopenAndRemoveSubIssueSendTheirMutations(t *testing.T) {
	var queries []string
	var vars []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		queries = append(queries, req.Query)
		vars = append(vars, req.Variables)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if err := c.UpdateIssue(context.Background(), "ISSUE", "new title", "new body"); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if err := c.CloseIssueCompleted(context.Background(), "ISSUE"); err != nil {
		t.Fatalf("CloseIssueCompleted: %v", err)
	}
	if err := c.ReopenIssue(context.Background(), "ISSUE"); err != nil {
		t.Fatalf("ReopenIssue: %v", err)
	}
	if err := c.RemoveSubIssue(context.Background(), "PARENT", "CHILD"); err != nil {
		t.Fatalf("RemoveSubIssue: %v", err)
	}

	if !strings.Contains(queries[0], "updateIssue") || vars[0]["title"] != "new title" || vars[0]["body"] != "new body" {
		t.Fatalf("UpdateIssue sent query %q vars %v, want updateIssue with the title and body", queries[0], vars[0])
	}
	if !strings.Contains(queries[1], "closeIssue") || !strings.Contains(queries[1], "COMPLETED") {
		t.Fatalf("CloseIssueCompleted sent query %q, want closeIssue with COMPLETED", queries[1])
	}
	if !strings.Contains(queries[2], "reopenIssue") {
		t.Fatalf("ReopenIssue sent query %q, want reopenIssue", queries[2])
	}
	if !strings.Contains(queries[3], "removeSubIssue") || vars[3]["issueId"] != "PARENT" || vars[3]["subIssueId"] != "CHILD" {
		t.Fatalf("RemoveSubIssue sent query %q vars %v, want removeSubIssue(PARENT, CHILD)", queries[3], vars[3])
	}
}

// TestItemFieldValuesReadsStatusAndStoreID checks ItemFieldValues matches
// each field value back to its own field's name, ignoring any other field
// value the project item might carry.
func TestItemFieldValuesReadsStatusAndStoreID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"node":{"fieldValues":{"nodes":[
			{"name":"In review","field":{"name":"Status"}},
			{"text":"DEMO-1","field":{"name":"Store ID"}},
			{"name":"Something","field":{"name":"Other Field"}}
		]}}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	status, storeID, err := c.ItemFieldValues(context.Background(), "ITEM_1")
	if err != nil {
		t.Fatalf("ItemFieldValues: %v", err)
	}
	if status != "In review" || storeID != "DEMO-1" {
		t.Fatalf("ItemFieldValues = (%q, %q), want (In review, DEMO-1)", status, storeID)
	}
}

// TestCreateIssueRefusesOnBadCredentials checks the client fails a request
// the fake server's auth check rejects, rather than reading a response
// shaped like success.
func TestCreateIssueRefusesOnBadCredentials(t *testing.T) {
	srv := fakeGraphQL(t, "bearer right-token")
	defer srv.Close()

	c := newFastClient(srv.URL, "wrong-token")
	if _, err := c.CreateIssue(context.Background(), "example", "demo", "t", "b"); err == nil {
		t.Fatal("CreateIssue: want an error on a bad token")
	}
}

// fakeProjectServer is a fake GraphQL server for brief.md#The board's own
// mutations and queries, matched the same way fakeGraphQL is, by substring
// on the request's query text. project, repo and linkedRepoIDs model the
// server's one project's current state, mutated as EnsureProject's own
// calls would change it on GitHub; calls records every operation's name, in
// order, for assertions that care about how many times (and in what order)
// something ran.
type fakeProjectServer struct {
	fields        []projectFieldNode
	repoID        string
	linkedRepoIDs map[string]bool
	calls         []string
	nextItem      int
}

func newFakeProjectServer() *fakeProjectServer {
	return &fakeProjectServer{repoID: "REPO_1", linkedRepoIDs: map[string]bool{}}
}

// decodeGraphQLRequest reads and decodes r's JSON body once, the shape
// every fake GraphQL server in this file needs.
func decodeGraphQLRequest(t *testing.T, r *http.Request) (query string, vars map[string]any) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode request body %s: %v", body, err)
	}
	return req.Query, req.Variables
}

// handle answers one already-decoded GraphQL request: query and vars are
// the request body's own fields, decoded once by the caller (a test may
// need to inspect them too, before or instead of handing them to a
// fakeProjectServer).
func (s *fakeProjectServer) handle(t *testing.T, w http.ResponseWriter, query string, vars map[string]any) {
	t.Helper()
	req := struct {
		Query     string
		Variables map[string]any
	}{query, vars}
	w.Header().Set("Content-Type", "application/json")

	switch {
	case strings.Contains(req.Query, "repository(owner"):
		s.calls = append(s.calls, "repository")
		_, _ = w.Write([]byte(`{"data":{"repository":{"id":"` + s.repoID + `"}}}`))
	case strings.Contains(req.Query, "projectV2(number"):
		s.calls = append(s.calls, "lookupProject")
		s.writeProjectLookup(w)
	case strings.Contains(req.Query, "createProjectV2Field"):
		s.calls = append(s.calls, "createField")
		s.handleCreateField(req.Variables, w)
	case strings.Contains(req.Query, "updateProjectV2Field"):
		s.calls = append(s.calls, "updateField")
		s.handleUpdateField(req.Variables, w)
	case strings.Contains(req.Query, "linkProjectV2ToRepository"):
		s.calls = append(s.calls, "linkRepo")
		repoID, _ := req.Variables["repositoryId"].(string)
		s.linkedRepoIDs[repoID] = true
		_, _ = w.Write([]byte(`{"data":{"linkProjectV2ToRepository":{"repository":{"id":"` + repoID + `"}}}}`))
	case strings.Contains(req.Query, "addProjectV2ItemById"):
		s.calls = append(s.calls, "addItem")
		s.nextItem++
		_, _ = fmt.Fprintf(w, `{"data":{"addProjectV2ItemById":{"item":{"id":"ITEM_%d"}}}}`, s.nextItem)
	case strings.Contains(req.Query, "updateProjectV2ItemFieldValue"):
		s.calls = append(s.calls, "setField")
		_, _ = w.Write([]byte(`{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"ITEM"}}}}`))
	default:
		t.Fatalf("fake project server got an unrecognized query: %s", req.Query)
	}
}

func (s *fakeProjectServer) writeProjectLookup(w http.ResponseWriter) {
	fieldsJSON, _ := json.Marshal(s.fields)
	var repoIDs []string
	for id, linked := range s.linkedRepoIDs {
		if linked {
			repoIDs = append(repoIDs, id)
		}
	}
	reposJSON, _ := json.Marshal(repoIDs)
	reposNodes := "[]"
	if len(repoIDs) > 0 {
		var nodes []string
		for _, id := range repoIDs {
			nodes = append(nodes, `{"id":"`+id+`"}`)
		}
		reposNodes = "[" + strings.Join(nodes, ",") + "]"
	}
	_ = reposJSON
	_, _ = fmt.Fprintf(w, `{"data":{"user":{"projectV2":{"id":"PROJECT_1","fields":{"nodes":%s},"repositories":{"nodes":%s}}}}}`, fieldsJSON, reposNodes)
}

func (s *fakeProjectServer) handleCreateField(vars map[string]any, w http.ResponseWriter) {
	name, _ := vars["name"].(string)
	id := "FIELD_" + strings.ReplaceAll(name, " ", "_")
	field := projectFieldNode{ID: id, Name: name}
	if opts, ok := vars["options"].([]any); ok {
		for _, raw := range opts {
			m, _ := raw.(map[string]any)
			optName, _ := m["name"].(string)
			optColor, _ := m["color"].(string)
			optDesc, _ := m["description"].(string)
			field.Options = append(field.Options, projectFieldOption{
				ID: "OPT_" + strings.ReplaceAll(optName, " ", "_"), Name: optName, Color: optColor, Description: optDesc,
			})
		}
	}
	s.fields = append(s.fields, field)
	data, _ := json.Marshal(field)
	_, _ = fmt.Fprintf(w, `{"data":{"createProjectV2Field":{"projectV2Field":%s}}}`, data)
}

func (s *fakeProjectServer) handleUpdateField(vars map[string]any, w http.ResponseWriter) {
	fieldID, _ := vars["fieldId"].(string)
	var options []projectFieldOption
	if opts, ok := vars["options"].([]any); ok {
		for _, raw := range opts {
			m, _ := raw.(map[string]any)
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			color, _ := m["color"].(string)
			desc, _ := m["description"].(string)
			if id == "" {
				id = "OPT_" + strings.ReplaceAll(name, " ", "_")
			}
			options = append(options, projectFieldOption{ID: id, Name: name, Color: color, Description: desc})
		}
	}
	for i, f := range s.fields {
		if f.ID == fieldID {
			s.fields[i].Options = options
		}
	}
	field := projectFieldNode{ID: fieldID, Name: "Status", Options: options}
	data, _ := json.Marshal(field)
	_, _ = fmt.Fprintf(w, `{"data":{"updateProjectV2Field":{"projectV2Field":%s}}}`, data)
}

// TestEnsureProjectCreatesMissingFieldsAndLinksRepo checks brief.md#The
// board's own creation path: an empty project gets its Status field (every
// one of the five options) and its Store ID field created, and the issue
// repo linked, since none of that exists yet.
func TestEnsureProjectCreatesMissingFieldsAndLinksRepo(t *testing.T) {
	fps := newFakeProjectServer()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, vars := decodeGraphQLRequest(t, r)
		fps.handle(t, w, query, vars)
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	proj, err := c.EnsureProject(context.Background(), "example", false, 7, "example", "tracking")
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if proj.ID != "PROJECT_1" {
		t.Fatalf("proj.ID = %q, want PROJECT_1", proj.ID)
	}
	if len(proj.StatusOptions) != 5 {
		t.Fatalf("StatusOptions = %v, want all 5", proj.StatusOptions)
	}
	for _, name := range []string{"Backlog", "Briefed", "In progress", "In review", "Done"} {
		if proj.StatusOptions[name] == "" {
			t.Fatalf("StatusOptions missing %q: %v", name, proj.StatusOptions)
		}
	}
	if proj.StoreIDFieldID == "" {
		t.Fatal("StoreIDFieldID is empty, want the Store ID field's id")
	}
	if !fps.linkedRepoIDs["REPO_1"] {
		t.Fatal("the issue repo was not linked to the project")
	}

	createFields := 0
	for _, c := range fps.calls {
		if c == "createField" {
			createFields++
		}
	}
	if createFields != 2 {
		t.Fatalf("createField called %d times, want 2 (Status and Store ID)", createFields)
	}
}

// TestEnsureProjectKeepsExistingOptionsAndAddsMissing checks that a Status
// field already on the project, with some of its options, is rewritten to
// add only the missing ones, keeping every existing option's id
// (brief.md#The board: "an option's id keeps it").
func TestEnsureProjectKeepsExistingOptionsAndAddsMissing(t *testing.T) {
	fps := newFakeProjectServer()
	fps.fields = []projectFieldNode{
		{ID: "FIELD_STATUS", Name: "Status", Options: []projectFieldOption{
			{ID: "OPT_EXISTING_BACKLOG", Name: "Backlog", Color: "GRAY", Description: "In the store, no brief yet"},
		}},
		{ID: "FIELD_STOREID", Name: "Store ID"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, vars := decodeGraphQLRequest(t, r)
		fps.handle(t, w, query, vars)
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	proj, err := c.EnsureProject(context.Background(), "example", false, 7, "example", "tracking")
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if proj.StatusFieldID != "FIELD_STATUS" || proj.StoreIDFieldID != "FIELD_STOREID" {
		t.Fatalf("proj = %+v, want the existing field ids kept", proj)
	}
	if proj.StatusOptions["Backlog"] != "OPT_EXISTING_BACKLOG" {
		t.Fatalf("Backlog option id = %q, want the existing one (OPT_EXISTING_BACKLOG) kept", proj.StatusOptions["Backlog"])
	}
	if len(proj.StatusOptions) != 5 {
		t.Fatalf("StatusOptions = %v, want all 5 after adding the missing 4", proj.StatusOptions)
	}

	var createFields, updateFields int
	for _, call := range fps.calls {
		switch call {
		case "createField":
			createFields++
		case "updateField":
			updateFields++
		}
	}
	if createFields != 0 { // Store ID already exists too
		t.Fatalf("createField called %d times, want 0 (both fields already exist)", createFields)
	}
	if updateFields != 1 {
		t.Fatalf("updateField called %d times, want 1 (adding Status's missing options)", updateFields)
	}
}

// TestEnsureProjectRefusesWhenProjectIsNil checks brief.md#The board's
// refusal: a project GraphQL resolves to null - gone, or a token without
// the project scope, which look the same from here - is refused with
// MIRROR_PROJECT_NOT_FOUND, naming `gh auth refresh -s project`.
func TestEnsureProjectRefusesWhenProjectIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user":{"projectV2":null}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	_, err := c.EnsureProject(context.Background(), "example", false, 7, "example", "tracking")
	if err == nil {
		t.Fatal("EnsureProject: want an error when the project resolves to null")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an *axi.Error", err)
	}
	if ae.Code != "MIRROR_PROJECT_NOT_FOUND" {
		t.Fatalf("code = %q, want MIRROR_PROJECT_NOT_FOUND", ae.Code)
	}
	if len(ae.Help) != 1 || ae.Help[0] != "gh auth refresh -s project" {
		t.Fatalf("help = %v, want [\"gh auth refresh -s project\"]", ae.Help)
	}
}

// TestPlaceItemAddsItemThenSetsStatusAndStoreID checks PlaceItem's own
// three-step shape: it adds the content to the project, then sets its
// Status and Store ID field values, returning the item id the fake server
// handed back.
func TestPlaceItemAddsItemThenSetsStatusAndStoreID(t *testing.T) {
	fps := newFakeProjectServer()
	var fieldIDs, optionIDs, texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, vars := decodeGraphQLRequest(t, r)
		if strings.Contains(query, "updateProjectV2ItemFieldValue") {
			fieldID, _ := vars["fieldId"].(string)
			fieldIDs = append(fieldIDs, fieldID)
			if optionID, ok := vars["optionId"].(string); ok {
				optionIDs = append(optionIDs, optionID)
			}
			if text, ok := vars["text"].(string); ok {
				texts = append(texts, text)
			}
		}
		fps.handle(t, w, query, vars)
	}))
	defer srv.Close()

	proj := ProjectV2{
		ID:             "PROJECT_1",
		StatusFieldID:  "FIELD_STATUS",
		StatusOptions:  map[string]string{"In review": "OPT_IN_REVIEW"},
		StoreIDFieldID: "FIELD_STOREID",
	}
	c := newFastClient(srv.URL, "test-token")
	itemID, err := c.PlaceItem(context.Background(), proj, "ISSUE_1", "In review", "DEMO-1")
	if err != nil {
		t.Fatalf("PlaceItem: %v", err)
	}
	if itemID != "ITEM_1" {
		t.Fatalf("itemID = %q, want ITEM_1", itemID)
	}
	if len(fieldIDs) != 2 || fieldIDs[0] != "FIELD_STATUS" || fieldIDs[1] != "FIELD_STOREID" {
		t.Fatalf("fieldIDs = %v, want Status then Store ID", fieldIDs)
	}
	if len(optionIDs) != 1 || optionIDs[0] != "OPT_IN_REVIEW" {
		t.Fatalf("optionIDs = %v, want the In review option", optionIDs)
	}
	if len(texts) != 1 || texts[0] != "DEMO-1" {
		t.Fatalf("texts = %v, want the ticket's Store ID", texts)
	}
}

// TestPlaceItemRefusesAnUnknownStatus checks that PlaceItem fails rather
// than silently skipping the Status write when proj carries no option for
// status - a project EnsureProject did not actually finish setting up.
func TestPlaceItemRefusesAnUnknownStatus(t *testing.T) {
	proj := ProjectV2{ID: "PROJECT_1", StatusFieldID: "FIELD_STATUS", StatusOptions: map[string]string{}, StoreIDFieldID: "FIELD_STOREID"}
	c := newFastClient("http://unused.invalid", "test-token")
	if _, err := c.PlaceItem(context.Background(), proj, "ISSUE_1", "In review", "DEMO-1"); err == nil {
		t.Fatal("PlaceItem: want an error when proj has no option for the status")
	}
}

// TestDoRetriesOnServerErrorThenSucceeds checks brief.md#The GitHub
// client's retry: a call that fails with a server error (5xx) twice is
// retried and succeeds on its third attempt, well within maxAttempts.
// RepositoryIsPublic sends exactly one query, so attempts counts do's own
// retries alone.
func TestDoRetriesOnServerErrorThenSucceeds(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"repository":{"id":"REPO_1","isPrivate":false}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if _, err := c.RepositoryIsPublic(context.Background(), "example", "demo"); err != nil {
		t.Fatalf("RepositoryIsPublic: %v, want the third attempt to succeed", err)
	}
	if attempts != 3 {
		t.Fatalf("server saw %d request(s), want exactly 3 (2 failures then a success)", attempts)
	}
}

// TestDoGivesUpAfterMaxAttempts checks that a call failing with a server
// error every time is retried exactly maxAttempts times in all, then
// reported as the failure it is - never retried forever.
func TestDoGivesUpAfterMaxAttempts(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	_, err := c.RepositoryIsPublic(context.Background(), "example", "demo")
	if err == nil {
		t.Fatal("RepositoryIsPublic: want an error, the server never succeeds")
	}
	if attempts != maxAttempts {
		t.Fatalf("server saw %d request(s), want exactly maxAttempts (%d)", attempts, maxAttempts)
	}
}

// TestDoDoesNotRetryAClientError checks that a client error (4xx, besides
// 403 and 429's own rate-limit shapes) is reported at once: retrying it
// would only repeat the same refusal.
func TestDoDoesNotRetryAClientError(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if _, err := c.RepositoryIsPublic(context.Background(), "example", "demo"); err == nil {
		t.Fatal("RepositoryIsPublic: want an error")
	}
	if attempts != 1 {
		t.Fatalf("server saw %d request(s), want exactly 1 (a client error is not retried)", attempts)
	}
}

// TestDoRetriesAGraphQLRateLimitedError checks that a 200 OK response whose
// "errors" array names a RATE_LIMITED type - GitHub's GraphQL-level rate
// limit shape - is retried the same as a 5xx or 429.
func TestDoRetriesAGraphQLRateLimitedError(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			_, _ = w.Write([]byte(`{"errors":[{"message":"API rate limit exceeded","type":"RATE_LIMITED"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"repository":{"id":"REPO_1","isPrivate":false}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	if _, err := c.RepositoryIsPublic(context.Background(), "example", "demo"); err != nil {
		t.Fatalf("RepositoryIsPublic: %v, want the second attempt to succeed", err)
	}
	if attempts != 2 {
		t.Fatalf("server saw %d request(s), want exactly 2 (one RATE_LIMITED, then a success)", attempts)
	}
}

// TestDoThrottlesMutationsOneAtATime checks brief.md#The GitHub client's
// pacing: two mutations sent back to back (AddComment, a single-request
// call with no query step ahead of it) are at least MutationInterval
// apart, timed from the fake server's own clock.
func TestDoThrottlesMutationsOneAtATime(t *testing.T) {
	const interval = 30 * time.Millisecond

	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"addComment":{"subject":{"id":"ISSUE_1"}}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	c.MutationInterval = interval
	if err := c.AddComment(context.Background(), "ISSUE_1", "one"); err != nil {
		t.Fatalf("AddComment 1: %v", err)
	}
	if err := c.AddComment(context.Background(), "ISSUE_1", "two"); err != nil {
		t.Fatalf("AddComment 2: %v", err)
	}

	if len(times) != 2 {
		t.Fatalf("server saw %d request(s), want exactly 2", len(times))
	}
	// A few percent under interval is sleep's own scheduling jitter, not a
	// pacing bug: seen as low as 27.7ms against a 30ms interval under load.
	const tolerance = 5 * time.Millisecond
	if gap := times[1].Sub(times[0]); gap < interval-tolerance {
		t.Fatalf("the two AddComment mutations were %v apart, want at least %v", gap, interval)
	}
}

// TestDoRetriesWithBackoff checks that do waits BackoffBase before its
// second attempt (and would double it again before a third), rather than
// retrying immediately.
func TestDoRetriesWithBackoff(t *testing.T) {
	const backoffBase = 20 * time.Millisecond

	var attempts int
	var firstAt time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			firstAt = time.Now()
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
			return
		}
		if gap := time.Since(firstAt); gap < backoffBase {
			t.Errorf("retry arrived after %v, want at least backoffBase (%v)", gap, backoffBase)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"addComment":{"subject":{"id":"ISSUE_1"}}}}`))
	}))
	defer srv.Close()

	c := newFastClient(srv.URL, "test-token")
	c.BackoffBase = backoffBase
	if err := c.AddComment(context.Background(), "ISSUE_1", "t"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
}
