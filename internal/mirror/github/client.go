// Package github is the GitHub mirror's own GraphQL client: a thin wrapper
// over the API the bridge proved (createIssue first; the rest of its
// mutations and queries land with the slices that use them). A test points
// it at a fake GraphQL server (httptest); production points it at
// https://api.github.com/graphql with the token `gh auth token` prints. No
// environment variable redirects either (develdeco/jig#84): both are
// arguments, so no test ever reaches github.com.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/develdeco/jig/internal/axi"
)

// Issue is the identity GitHub hands back for a created issue.
type Issue struct {
	Number int
	NodeID string
}

// PullRequest is one pull request: its number, node id, and GitHub's own
// state spelling ("OPEN", "CLOSED" or "MERGED"). Owner and Repo are filled
// in only by PullRequestsByIDs, which (unlike PullRequestsByHead, whose
// caller already knows the repo it queried) has to look them up.
type PullRequest struct {
	Number int
	NodeID string
	State  string
	Owner  string
	Repo   string
}

// IssueState is an issue's current state on GitHub, as FetchIssue reads it
// before the "drift" slice decides whether to overwrite anything
// (brief.md#Ownership and drift).
type IssueState struct {
	Title string
	Body  string
	// Open is false when the issue is closed, whatever its close reason.
	Open bool
	// ParentID is the node id of the issue this one is a sub-issue of, or ""
	// when it has none.
	ParentID string
	// SubIssueIDs are this issue's own sub-issues' node ids, GitHub's own
	// order.
	SubIssueIDs []string
	// BlockedByIDs are the node ids of the issues this one is blocked by.
	BlockedByIDs []string
}

// ErrIssueNotFound is what FetchIssue returns when nodeID no longer
// resolves to anything: the issue was deleted, or its repository was
// (brief.md#Adoption: "a recorded issue that is gone from GitHub gets a new
// issue").
var ErrIssueNotFound = errors.New("github: issue not found")

// ProjectV2 is a GitHub Project's resolved identity and the field/option ids
// PlaceItem writes items through: looked up fresh by EnsureProject at every
// sync and recorded nowhere (brief.md#The board: "records none of them").
type ProjectV2 struct {
	ID string
	// StatusFieldID is the project's "Status" single-select field.
	StatusFieldID string
	// StatusOptions maps each Status option's name (Backlog, Briefed, In
	// progress, In review, Done) to its own option id.
	StatusOptions map[string]string
	// StoreIDFieldID is the project's "Store ID" text field.
	StoreIDFieldID string
}

// Client is what internal/mirror needs from GitHub. A test hands its own
// stub; production uses *GraphQLClient.
type Client interface {
	// CreateIssue opens an issue in owner/repo titled title with body, and
	// returns its number and node id.
	CreateIssue(ctx context.Context, owner, repo, title, body string) (Issue, error)
	// CloseIssueNotPlanned closes the issue nodeID identifies with the
	// "not planned" state reason, as Claim does when another clone's push
	// landed the same ticket or chart's issue first (brief.md#Syncing at
	// every checkpoint).
	CloseIssueNotPlanned(ctx context.Context, nodeID string) error
	// AddComment adds a comment with body to the issue (or other
	// commentable) subjectID identifies.
	AddComment(ctx context.Context, subjectID, body string) error
	// PullRequestsByHead returns owner/repo's pull requests, in any state,
	// whose head branch is exactly branch (brief.md#Status and pull
	// requests), ordered by number.
	PullRequestsByHead(ctx context.Context, owner, repo, branch string) ([]PullRequest, error)
	// PullRequestsByIDs returns the current number, state and repository of
	// each pull request nodeIDs identifies, skipping any id GitHub no
	// longer resolves to a pull request (brief.md#Status and pull requests:
	// "then any recorded in its prs: that this query no longer finds"). A
	// gone id comes back from GitHub as a NOT_FOUND "errors" entry alongside
	// a null in data.nodes, not a null alone; this is tolerated rather than
	// treated as a fatal error.
	PullRequestsByIDs(ctx context.Context, nodeIDs []string) ([]PullRequest, error)
	// AddSubIssue makes subIssueID a sub-issue of parentID, replacing
	// whatever parent it had (brief.md#Links: "a ticket that becomes a
	// sub-issue goes last").
	AddSubIssue(ctx context.Context, parentID, subIssueID string) error
	// AddBlockedBy records that issueID is blocked by blockingID
	// (brief.md#Links).
	AddBlockedBy(ctx context.Context, issueID, blockingID string) error
	// RemoveBlockedBy removes blockingID from issueID's blocked-by edges:
	// brief.md#Links's own drift rule for a blocked-by edge added on GitHub
	// to a ticket that jig's own store does not list (brief.md#Ownership
	// and drift).
	RemoveBlockedBy(ctx context.Context, issueID, blockingID string) error
	// EnsureProject resolves the GitHub Project at ownerLogin (a user when
	// isOrg is false, an org when true), number number: creating its Status
	// field (with every option brief.md#The board lists, keeping any
	// existing option's id, name, color and description) and its "Store ID"
	// text field when either is missing, and linking repoOwner/repoName to
	// it when that repo is not linked yet (brief.md#The board). A project
	// GraphQL cannot resolve - gone, or a token without the project scope,
	// which looks identical from here - is refused with
	// MIRROR_PROJECT_NOT_FOUND.
	EnsureProject(ctx context.Context, ownerLogin string, isOrg bool, number int, repoOwner, repoName string) (ProjectV2, error)
	// PlaceItem adds contentID (an issue's or a pull request's node id) to
	// proj, idempotently, then sets its Status and Store ID fields, and
	// returns the item's node id (brief.md#The board).
	PlaceItem(ctx context.Context, proj ProjectV2, contentID, status, storeID string) (itemID string, err error)
	// RepositoryIsPublic reports whether owner/repo is public, the gate
	// brief.md#Publish safety scans a title and body against before they are
	// written ("A private issue repo is not scanned").
	RepositoryIsPublic(ctx context.Context, owner, repo string) (bool, error)
	// FetchIssue reads nodeID's current title, body, open/closed state,
	// parent, sub-issues and blocked-by links, the baseline the "drift" slice
	// compares the store's own value against (brief.md#Ownership and drift).
	// It returns ErrIssueNotFound when nodeID no longer resolves.
	FetchIssue(ctx context.Context, nodeID string) (IssueState, error)
	// UpdateIssue sets nodeID's title and body (brief.md#Syncing at every
	// checkpoint, step 4).
	UpdateIssue(ctx context.Context, nodeID, title, body string) error
	// CloseIssueCompleted closes nodeID with the "completed" state reason,
	// the state a Done ticket's or chart's issue gets (brief.md#Status and
	// pull requests: "A Done issue is closed as completed").
	CloseIssueCompleted(ctx context.Context, nodeID string) error
	// ReopenIssue reopens nodeID, for an issue the store now says is not Done
	// but GitHub (or an earlier sync) left closed.
	ReopenIssue(ctx context.Context, nodeID string) error
	// RemoveSubIssue removes subIssueID from parentID's sub-issues: brief.md
	// #Links's own drift rule, "a sub-issue added on GitHub to a chart's
	// issue that is no ticket's issue is taken out again".
	RemoveSubIssue(ctx context.Context, parentID, subIssueID string) error
	// ItemFieldValues reads itemID's current Status (the option's name) and
	// Store ID (the text) field values, the baseline brief.md#Ownership and
	// drift compares a board item's owned fields against. Either return is ""
	// when the project carries no value for that field yet.
	ItemFieldValues(ctx context.Context, itemID string) (status, storeID string, err error)
	// LookupProject reports whether the GitHub Project at ownerLogin/number
	// resolves at all, with no mutation: a dry run's own read of brief.md
	// #The board's project, which cannot call EnsureProject without risking
	// the field or link mutation that a plain preview must withhold.
	LookupProject(ctx context.Context, ownerLogin string, isOrg bool, number int) (bool, error)
}

// defaultMutationInterval and defaultBackoffBase are brief.md#The GitHub
// client's own defaults - "one mutation per second, as GitHub asks of
// clients that create content" and the first wait of maxAttempts's own
// retry with backoff (1s, 2s, 4s between the 4 attempts the brief names,
// "defaulted, as the bridge") - used whenever a GraphQLClient's own
// MutationInterval or BackoffBase field is zero (production never sets
// either; this package's own tests do, to run at full speed).
const (
	defaultMutationInterval = time.Second
	defaultBackoffBase      = time.Second
)

// maxAttempts is how many times do sends one query or mutation before
// giving up on a retryable failure (brief.md#The GitHub client: "4
// attempts in all").
const maxAttempts = 4

// GraphQLClient is Client over GitHub's GraphQL API.
type GraphQLClient struct {
	// Endpoint is the GraphQL URL: https://api.github.com/graphql in
	// production, a fake httptest server's URL in a test.
	Endpoint string
	// Token is sent as the bearer token on every request.
	Token string
	// HTTPClient makes the request. nil means http.DefaultClient.
	HTTPClient *http.Client
	// MutationInterval overrides defaultMutationInterval. Zero (New's own
	// default) means defaultMutationInterval; a test sets it small to run
	// without actually waiting a second between mutations.
	MutationInterval time.Duration
	// BackoffBase overrides defaultBackoffBase the same way.
	BackoffBase time.Duration

	repos map[string]repoInfo // "owner/repo" -> its node id and visibility, resolved once

	mu              sync.Mutex
	lastMutationAt  time.Time
	haveLastMutated bool
}

// repoInfo is one repository's node id and visibility, cached by repos so a
// sync that touches the same repo more than once (CreateIssue,
// RepositoryIsPublic) looks it up once.
type repoInfo struct {
	ID      string
	Private bool
}

// New returns a GraphQLClient against endpoint, authenticating with token.
func New(endpoint, token string) *GraphQLClient {
	return &GraphQLClient{Endpoint: endpoint, Token: token}
}

// graphQLError is one entry of a GraphQL response's top-level "errors".
// Type is "RATE_LIMITED" on the one GraphQL-level error do's retry treats
// as retryable (brief.md#The GitHub client); GitHub answers most REST-style
// rate limiting with an HTTP status instead (handled in doOnce directly),
// but a GraphQL mutation can also hit its own, reported this way. Type is
// "NOT_FOUND" with Path ["nodes", i] when a nodes(ids:) query is handed an
// id GitHub cannot resolve: the real API reports this alongside a null in
// data.nodes[i] rather than leaving data.nodes[i] null with no error
// (confirmed against the live API; PullRequestsByIDs is the one caller that
// tolerates it, via toleratingGoneNodes).
type graphQLError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Path    []any  `json:"path"`
}

// doOption adjusts one call's own tolerance for an otherwise-fatal "errors"
// entry; do's default (no options) is unchanged: any non-empty "errors"
// fails the call.
type doOption func(*doSettings)

type doSettings struct {
	tolerateGoneNodes bool
}

// toleratingGoneNodes makes do accept a response whose every "errors" entry
// is a NOT_FOUND on a nodes(ids:) element (path ["nodes", i]): the shape
// GitHub answers a gone node's id with, alongside the null already in
// data.nodes[i]. Any other error - a different type, or a NOT_FOUND mixed
// with one that is not - still fails the call.
func toleratingGoneNodes() doOption {
	return func(s *doSettings) { s.tolerateGoneNodes = true }
}

// allGoneNodeErrors reports whether every entry of errs is a NOT_FOUND whose
// path is ["nodes", i] for some index i - the shape toleratingGoneNodes
// accepts. An empty errs is not "all gone-node errors": do only calls this
// when len(envelope.Errors) > 0.
func allGoneNodeErrors(errs []graphQLError) bool {
	for _, e := range errs {
		if !strings.EqualFold(e.Type, "NOT_FOUND") {
			return false
		}
		if len(e.Path) != 2 {
			return false
		}
		if root, ok := e.Path[0].(string); !ok || root != "nodes" {
			return false
		}
		if _, ok := e.Path[1].(float64); !ok {
			return false
		}
	}
	return true
}

// graphQLRequest is the JSON body every GraphQL call sends.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// do posts query/variables to c.Endpoint and decodes the response's "data"
// into out, returning an error that names every message in a non-empty
// "errors" array - unless opts tolerates that array's exact shape (so far,
// only toleratingGoneNodes), in which case it decodes "data" anyway. A
// mutation (query's text starts with "mutation", the bridge's own
// convention every mutation* const above follows) is paced at most one per
// second (throttle) before it is sent; a request that fails with a server
// error or a rate limit is retried with backoff, up to maxAttempts in all
// (brief.md#The GitHub client).
func (c *GraphQLClient) do(ctx context.Context, query string, variables map[string]any, out any, opts ...doOption) error {
	mutation := strings.HasPrefix(strings.TrimSpace(query), "mutation")

	var settings doSettings
	for _, opt := range opts {
		opt(&settings)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if mutation {
			c.throttle()
		}
		retryable, err := c.doOnce(ctx, query, variables, out, settings)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable || attempt == maxAttempts {
			break
		}
		c.backoffSleep(attempt)
	}
	return lastErr
}

// mutationInterval is c.MutationInterval, or defaultMutationInterval when
// that is zero.
func (c *GraphQLClient) mutationInterval() time.Duration {
	if c.MutationInterval > 0 {
		return c.MutationInterval
	}
	return defaultMutationInterval
}

// backoffBase is c.BackoffBase, or defaultBackoffBase when that is zero.
func (c *GraphQLClient) backoffBase() time.Duration {
	if c.BackoffBase > 0 {
		return c.BackoffBase
	}
	return defaultBackoffBase
}

// throttle blocks until at least c.mutationInterval() has passed since the
// last mutation this client sent.
func (c *GraphQLClient) throttle() {
	interval := c.mutationInterval()
	c.mu.Lock()
	var wait time.Duration
	if c.haveLastMutated {
		wait = interval - time.Since(c.lastMutationAt)
	}
	if wait < 0 {
		wait = 0
	}
	c.lastMutationAt = time.Now().Add(wait)
	c.haveLastMutated = true
	c.mu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
}

// backoffSleep waits before do's (attempt+1)th try: c.backoffBase(),
// doubled each attempt (1s, 2s, 4s for its own default of 1s).
func (c *GraphQLClient) backoffSleep(attempt int) {
	time.Sleep(c.backoffBase() << (attempt - 1))
}

// doOnce sends query/variables once and decodes its response into out,
// reporting whether the failure it returns (if any) is worth retrying: a
// network error, a server error (5xx) or a status GitHub's rate limiting
// uses (429, or 403 - the status a secondary rate limit answers with) is
// retryable; anything else - a client error, a GraphQL-level error with no
// RATE_LIMITED type - is not, since retrying it would only repeat it. A
// non-empty "errors" array that settings.tolerateGoneNodes accepts (every
// entry a NOT_FOUND on a nodes(ids:) element) is not a failure at all: out
// still decodes from "data", null entries included.
func (c *GraphQLClient) doOnce(ctx context.Context, query string, variables map[string]any, out any, settings doSettings) (retryable bool, err error) {
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return false, fmt.Errorf("github: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "bearer "+c.Token)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return true, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return true, fmt.Errorf("github: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden
		return retryable, fmt.Errorf("github: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false, fmt.Errorf("github: decode response: %w", err)
	}
	if len(envelope.Errors) > 0 && !(settings.tolerateGoneNodes && allGoneNodeErrors(envelope.Errors)) {
		msgs := make([]string, len(envelope.Errors))
		retryable := false
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
			if strings.EqualFold(e.Type, "RATE_LIMITED") {
				retryable = true
			}
		}
		return retryable, fmt.Errorf("github: %s", strings.Join(msgs, "; "))
	}
	if out == nil || len(envelope.Data) == 0 {
		return false, nil
	}
	return false, json.Unmarshal(envelope.Data, out)
}

// repositoryQuery looks up a repository's own GraphQL node id and
// visibility: the id is what createIssue's input needs, and isPrivate is
// brief.md#Publish safety's gate ("A private issue repo is not scanned").
const repositoryQuery = `query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) { id isPrivate }
}`

// repository resolves owner/repo's node id and visibility, caching it on c
// so a sync that touches the same repo more than once looks it up once.
func (c *GraphQLClient) repository(ctx context.Context, owner, repo string) (repoInfo, error) {
	key := owner + "/" + repo
	if info, ok := c.repos[key]; ok {
		return info, nil
	}
	var resp struct {
		Repository struct {
			ID        string `json:"id"`
			IsPrivate bool   `json:"isPrivate"`
		} `json:"repository"`
	}
	if err := c.do(ctx, repositoryQuery, map[string]any{"owner": owner, "name": repo}, &resp); err != nil {
		return repoInfo{}, fmt.Errorf("github: look up repository %s: %w", key, err)
	}
	if resp.Repository.ID == "" {
		return repoInfo{}, fmt.Errorf("github: repository %s not found", key)
	}
	info := repoInfo{ID: resp.Repository.ID, Private: resp.Repository.IsPrivate}
	if c.repos == nil {
		c.repos = map[string]repoInfo{}
	}
	c.repos[key] = info
	return info, nil
}

// repositoryID resolves owner/repo's node id, caching it on c so a sync
// that creates several issues in the same repo looks it up once.
func (c *GraphQLClient) repositoryID(ctx context.Context, owner, repo string) (string, error) {
	info, err := c.repository(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

// RepositoryIsPublic reports whether owner/repo is public.
func (c *GraphQLClient) RepositoryIsPublic(ctx context.Context, owner, repo string) (bool, error) {
	info, err := c.repository(ctx, owner, repo)
	if err != nil {
		return false, err
	}
	return !info.Private, nil
}

// createIssueMutation is the one mutation the bridge proved first (brief.md
// #The GitHub client): the rest land with the slices that use them.
const createIssueMutation = `mutation($repositoryId: ID!, $title: String!, $body: String!) {
  createIssue(input: {repositoryId: $repositoryId, title: $title, body: $body}) {
    issue { number id }
  }
}`

// CreateIssue opens an issue in owner/repo titled title with body.
func (c *GraphQLClient) CreateIssue(ctx context.Context, owner, repo, title, body string) (Issue, error) {
	repoID, err := c.repositoryID(ctx, owner, repo)
	if err != nil {
		return Issue{}, err
	}
	var resp struct {
		CreateIssue struct {
			Issue struct {
				Number int    `json:"number"`
				ID     string `json:"id"`
			} `json:"issue"`
		} `json:"createIssue"`
	}
	vars := map[string]any{"repositoryId": repoID, "title": title, "body": body}
	if err := c.do(ctx, createIssueMutation, vars, &resp); err != nil {
		return Issue{}, fmt.Errorf("github: create issue in %s/%s: %w", owner, repo, err)
	}
	return Issue{Number: resp.CreateIssue.Issue.Number, NodeID: resp.CreateIssue.Issue.ID}, nil
}

// closeIssueMutation closes an issue as not planned: the bridge's own shape
// for the duplicate case, where another clone's push claimed the same
// ticket or chart's issue first and this one's is withdrawn instead of left
// open (brief.md#Syncing at every checkpoint).
const closeIssueMutation = `mutation($issueId: ID!) {
  closeIssue(input: {issueId: $issueId, stateReason: NOT_PLANNED}) {
    issue { id }
  }
}`

// CloseIssueNotPlanned closes the issue nodeID identifies as not planned.
func (c *GraphQLClient) CloseIssueNotPlanned(ctx context.Context, nodeID string) error {
	if err := c.do(ctx, closeIssueMutation, map[string]any{"issueId": nodeID}, nil); err != nil {
		return fmt.Errorf("github: close issue %s as not planned: %w", nodeID, err)
	}
	return nil
}

// addCommentMutation adds a comment to an issue (or any other commentable
// GitHub lets addComment target).
const addCommentMutation = `mutation($subjectId: ID!, $body: String!) {
  addComment(input: {subjectId: $subjectId, body: $body}) {
    subject { id }
  }
}`

// AddComment adds a comment with body to the issue (or other commentable)
// subjectID identifies.
func (c *GraphQLClient) AddComment(ctx context.Context, subjectID, body string) error {
	if err := c.do(ctx, addCommentMutation, map[string]any{"subjectId": subjectID, "body": body}, nil); err != nil {
		return fmt.Errorf("github: comment on %s: %w", subjectID, err)
	}
	return nil
}

// pullRequestsByHeadQuery finds owner/repo's pull requests whose head
// branch is exactly $branch, in any state, through Repository.pullRequests'
// own headRefName filter. GitHub's search (used until this query replaced
// it) matches head: as a prefix, so a search for "jig/T-2" also turns up a
// pull request whose head is "jig/T-23" - an exact match is what
// brief.md#Status and pull requests asks for.
const pullRequestsByHeadQuery = `query($owner: String!, $name: String!, $branch: String!) {
  repository(owner: $owner, name: $name) {
    pullRequests(headRefName: $branch, states: [OPEN, CLOSED, MERGED], first: 100) {
      nodes { number id state }
    }
  }
}`

// PullRequestsByHead returns owner/repo's pull requests, in any state,
// whose head branch is exactly branch, ordered by number.
func (c *GraphQLClient) PullRequestsByHead(ctx context.Context, owner, repo, branch string) ([]PullRequest, error) {
	var resp struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
					Number int    `json:"number"`
					ID     string `json:"id"`
					State  string `json:"state"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": repo, "branch": branch}
	if err := c.do(ctx, pullRequestsByHeadQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("github: find pull requests in %s/%s with head %s: %w", owner, repo, branch, err)
	}
	nodes := resp.Repository.PullRequests.Nodes
	prs := make([]PullRequest, len(nodes))
	for i, n := range nodes {
		prs[i] = PullRequest{Number: n.Number, NodeID: n.ID, State: n.State}
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })
	return prs, nil
}

// pullRequestsByIDsQuery fetches each node id's current number, state and
// owning repository in one round trip, for the recorded prs: entries
// PullRequestsByHead's exact head match no longer finds (brief.md#Status and
// pull requests: a merged pull request's head branch is usually gone by
// then). A null node means GitHub no longer has it: alongside that null,
// GitHub's real nodes(ids:) resolver also reports a NOT_FOUND "errors" entry
// at path ["nodes", i], which PullRequestsByIDs tolerates via
// toleratingGoneNodes rather than letting do treat it as fatal.
const pullRequestsByIDsQuery = `query($ids: [ID!]!) {
  nodes(ids: $ids) {
    ... on PullRequest {
      number
      id
      state
      repository { owner { login } name }
    }
  }
}`

// PullRequestsByIDs returns the current number, state and repository of
// each pull request nodeIDs identifies, skipping any id GitHub no longer
// resolves to a pull request.
func (c *GraphQLClient) PullRequestsByIDs(ctx context.Context, nodeIDs []string) ([]PullRequest, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var resp struct {
		Nodes []*struct {
			Number     int    `json:"number"`
			ID         string `json:"id"`
			State      string `json:"state"`
			Repository struct {
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name string `json:"name"`
			} `json:"repository"`
		} `json:"nodes"`
	}
	if err := c.do(ctx, pullRequestsByIDsQuery, map[string]any{"ids": nodeIDs}, &resp, toleratingGoneNodes()); err != nil {
		return nil, fmt.Errorf("github: fetch pull requests %v: %w", nodeIDs, err)
	}
	var prs []PullRequest
	for _, n := range resp.Nodes {
		if n == nil {
			continue
		}
		prs = append(prs, PullRequest{
			Number: n.Number,
			NodeID: n.ID,
			State:  n.State,
			Owner:  n.Repository.Owner.Login,
			Repo:   n.Repository.Name,
		})
	}
	return prs, nil
}

// addSubIssueMutation makes a sub-issue of a parent issue, replacing
// whatever parent it had (brief.md#The GitHub client).
const addSubIssueMutation = `mutation($issueId: ID!, $subIssueId: ID!) {
  addSubIssue(input: {issueId: $issueId, subIssueId: $subIssueId, replaceParent: true}) {
    issue { id }
  }
}`

// AddSubIssue makes subIssueID a sub-issue of parentID.
func (c *GraphQLClient) AddSubIssue(ctx context.Context, parentID, subIssueID string) error {
	vars := map[string]any{"issueId": parentID, "subIssueId": subIssueID}
	if err := c.do(ctx, addSubIssueMutation, vars, nil); err != nil {
		return fmt.Errorf("github: add sub-issue %s to %s: %w", subIssueID, parentID, err)
	}
	return nil
}

// addBlockedByMutation records that one issue is blocked by another
// (brief.md#The GitHub client).
const addBlockedByMutation = `mutation($issueId: ID!, $blockedByIssueId: ID!) {
  addBlockedBy(input: {issueId: $issueId, blockedByIssueId: $blockedByIssueId}) {
    issue { id }
  }
}`

// AddBlockedBy records that issueID is blocked by blockingID.
func (c *GraphQLClient) AddBlockedBy(ctx context.Context, issueID, blockingID string) error {
	vars := map[string]any{"issueId": issueID, "blockedByIssueId": blockingID}
	if err := c.do(ctx, addBlockedByMutation, vars, nil); err != nil {
		return fmt.Errorf("github: add blocked-by %s on %s: %w", blockingID, issueID, err)
	}
	return nil
}

// removeBlockedByMutation removes one issue from another's blocked-by
// edges (brief.md#The GitHub client).
const removeBlockedByMutation = `mutation($issueId: ID!, $blockedByIssueId: ID!) {
  removeBlockedBy(input: {issueId: $issueId, blockedByIssueId: $blockedByIssueId}) {
    issue { id }
  }
}`

// RemoveBlockedBy removes blockingID from issueID's blocked-by edges.
func (c *GraphQLClient) RemoveBlockedBy(ctx context.Context, issueID, blockingID string) error {
	vars := map[string]any{"issueId": issueID, "blockedByIssueId": blockingID}
	if err := c.do(ctx, removeBlockedByMutation, vars, nil); err != nil {
		return fmt.Errorf("github: remove blocked-by %s on %s: %w", blockingID, issueID, err)
	}
	return nil
}

// fetchIssueQuery reads an issue's title, body, open/closed state, parent,
// sub-issues and blocked-by links, the "drift" slice's own read of GitHub's
// current state (brief.md#Ownership and drift).
const fetchIssueQuery = `query($id: ID!) {
  node(id: $id) {
    ... on Issue {
      title
      body
      closed
      parent { id }
      subIssues(first: 100) { nodes { id } }
      blockedBy(first: 100) { nodes { id } }
    }
  }
}`

// FetchIssue reads nodeID's current state.
func (c *GraphQLClient) FetchIssue(ctx context.Context, nodeID string) (IssueState, error) {
	var resp struct {
		Node *struct {
			Title  string `json:"title"`
			Body   string `json:"body"`
			Closed bool   `json:"closed"`
			Parent *struct {
				ID string `json:"id"`
			} `json:"parent"`
			SubIssues struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"subIssues"`
			BlockedBy struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"blockedBy"`
		} `json:"node"`
	}
	if err := c.do(ctx, fetchIssueQuery, map[string]any{"id": nodeID}, &resp); err != nil {
		return IssueState{}, fmt.Errorf("github: fetch issue %s: %w", nodeID, err)
	}
	if resp.Node == nil {
		return IssueState{}, fmt.Errorf("%w: %s", ErrIssueNotFound, nodeID)
	}
	state := IssueState{Title: resp.Node.Title, Body: resp.Node.Body, Open: !resp.Node.Closed}
	if resp.Node.Parent != nil {
		state.ParentID = resp.Node.Parent.ID
	}
	for _, n := range resp.Node.SubIssues.Nodes {
		state.SubIssueIDs = append(state.SubIssueIDs, n.ID)
	}
	for _, n := range resp.Node.BlockedBy.Nodes {
		state.BlockedByIDs = append(state.BlockedByIDs, n.ID)
	}
	return state, nil
}

// updateIssueMutation sets an issue's title and body.
const updateIssueMutation = `mutation($issueId: ID!, $title: String!, $body: String!) {
  updateIssue(input: {id: $issueId, title: $title, body: $body}) {
    issue { id }
  }
}`

// UpdateIssue sets nodeID's title and body.
func (c *GraphQLClient) UpdateIssue(ctx context.Context, nodeID, title, body string) error {
	vars := map[string]any{"issueId": nodeID, "title": title, "body": body}
	if err := c.do(ctx, updateIssueMutation, vars, nil); err != nil {
		return fmt.Errorf("github: update issue %s: %w", nodeID, err)
	}
	return nil
}

// closeIssueCompletedMutation closes an issue as completed, the state a Done
// ticket's or chart's issue gets (brief.md#Status and pull requests).
const closeIssueCompletedMutation = `mutation($issueId: ID!) {
  closeIssue(input: {issueId: $issueId, stateReason: COMPLETED}) {
    issue { id }
  }
}`

// CloseIssueCompleted closes nodeID as completed.
func (c *GraphQLClient) CloseIssueCompleted(ctx context.Context, nodeID string) error {
	if err := c.do(ctx, closeIssueCompletedMutation, map[string]any{"issueId": nodeID}, nil); err != nil {
		return fmt.Errorf("github: close issue %s as completed: %w", nodeID, err)
	}
	return nil
}

// reopenIssueMutation reopens a closed issue.
const reopenIssueMutation = `mutation($issueId: ID!) {
  reopenIssue(input: {issueId: $issueId}) {
    issue { id }
  }
}`

// ReopenIssue reopens nodeID.
func (c *GraphQLClient) ReopenIssue(ctx context.Context, nodeID string) error {
	if err := c.do(ctx, reopenIssueMutation, map[string]any{"issueId": nodeID}, nil); err != nil {
		return fmt.Errorf("github: reopen issue %s: %w", nodeID, err)
	}
	return nil
}

// removeSubIssueMutation takes a sub-issue out of its parent, brief.md
// #Links's own drift rule for a sub-issue added on GitHub by hand.
const removeSubIssueMutation = `mutation($issueId: ID!, $subIssueId: ID!) {
  removeSubIssue(input: {issueId: $issueId, subIssueId: $subIssueId}) {
    issue { id }
  }
}`

// RemoveSubIssue removes subIssueID from parentID's sub-issues.
func (c *GraphQLClient) RemoveSubIssue(ctx context.Context, parentID, subIssueID string) error {
	vars := map[string]any{"issueId": parentID, "subIssueId": subIssueID}
	if err := c.do(ctx, removeSubIssueMutation, vars, nil); err != nil {
		return fmt.Errorf("github: remove sub-issue %s from %s: %w", subIssueID, parentID, err)
	}
	return nil
}

// itemFieldValuesQuery reads a project item's current field values, naming
// each by its field's own name so Status and Store ID need no field id to
// read back (brief.md#Ownership and drift).
const itemFieldValuesQuery = `query($id: ID!) {
  node(id: $id) {
    ... on ProjectV2Item {
      fieldValues(first: 20) {
        nodes {
          ... on ProjectV2ItemFieldSingleSelectValue { name field { ... on ProjectV2FieldCommon { name } } }
          ... on ProjectV2ItemFieldTextValue { text field { ... on ProjectV2FieldCommon { name } } }
        }
      }
    }
  }
}`

// ItemFieldValues reads itemID's current Status and Store ID field values.
func (c *GraphQLClient) ItemFieldValues(ctx context.Context, itemID string) (status, storeID string, err error) {
	var resp struct {
		Node *struct {
			FieldValues struct {
				Nodes []struct {
					Name  string `json:"name"`
					Text  string `json:"text"`
					Field struct {
						Name string `json:"name"`
					} `json:"field"`
				} `json:"nodes"`
			} `json:"fieldValues"`
		} `json:"node"`
	}
	if err := c.do(ctx, itemFieldValuesQuery, map[string]any{"id": itemID}, &resp); err != nil {
		return "", "", fmt.Errorf("github: read item %s field values: %w", itemID, err)
	}
	if resp.Node == nil {
		return "", "", nil
	}
	for _, n := range resp.Node.FieldValues.Nodes {
		switch n.Field.Name {
		case "Status":
			status = n.Name
		case "Store ID":
			storeID = n.Text
		}
	}
	return status, storeID, nil
}

// statusOptionSpec is one option every mirrored project's Status field must
// carry, in brief.md#The board's own order.
type statusOptionSpec struct {
	Name, Color, Description string
}

// statusOptions are brief.md#The board's five Status options.
var statusOptions = []statusOptionSpec{
	{"Backlog", "GRAY", "In the store, no brief yet"},
	{"Briefed", "BLUE", "Brief written, no slice has run"},
	{"In progress", "YELLOW", "Slices building or gate rounds running"},
	{"In review", "PURPLE", "Pull request open"},
	{"Done", "GREEN", "Pull request merged"},
}

// projectFieldOption is one option of a ProjectV2SingleSelectField, as the
// project lookup and every field mutation return it.
type projectFieldOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// projectFieldNode is one of a ProjectV2's fields: every field has an id and
// a name; Options is populated only for a ProjectV2SingleSelectField.
type projectFieldNode struct {
	ID      string               `json:"id"`
	Name    string               `json:"name"`
	Options []projectFieldOption `json:"options"`
}

// projectV2Node is the ProjectV2 EnsureProject's lookup resolves.
type projectV2Node struct {
	ID     string `json:"id"`
	Fields struct {
		Nodes []projectFieldNode `json:"nodes"`
	} `json:"fields"`
	Repositories struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	} `json:"repositories"`
}

// projectV2Query looks up a user's or an org's ProjectV2 by number, with its
// fields (id, name, and - for a single-select field - its options) and the
// repositories already linked to it. $root is substituted with "user" or
// "organization" (brief.md#The trackers entry: project: says which kind of
// owner it has).
const projectV2QueryFormat = `query($login: String!, $number: Int!) {
  %s(login: $login) {
    projectV2(number: $number) {
      id
      fields(first: 50) {
        nodes {
          ... on ProjectV2FieldCommon { id name }
          ... on ProjectV2SingleSelectField { id name options { id name color description } }
        }
      }
      repositories(first: 100) { nodes { id } }
    }
  }
}`

// lookupProjectV2 resolves ownerLogin's ProjectV2 number, returning nil when
// GraphQL resolved no project - a project that does not exist, or a token
// without the project scope, both of which GitHub answers with a null field
// (brief.md#The board).
func (c *GraphQLClient) lookupProjectV2(ctx context.Context, ownerLogin string, isOrg bool, number int) (*projectV2Node, error) {
	root := "user"
	if isOrg {
		root = "organization"
	}
	var resp map[string]json.RawMessage
	vars := map[string]any{"login": ownerLogin, "number": number}
	if err := c.do(ctx, fmt.Sprintf(projectV2QueryFormat, root), vars, &resp); err != nil {
		return nil, fmt.Errorf("github: look up project %s/%d: %w", ownerLogin, number, err)
	}
	raw, ok := resp[root]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var holder struct {
		ProjectV2 *projectV2Node `json:"projectV2"`
	}
	if err := json.Unmarshal(raw, &holder); err != nil {
		return nil, fmt.Errorf("github: decode project lookup: %w", err)
	}
	return holder.ProjectV2, nil
}

// createProjectV2FieldMutation creates a field on a project: $dataType is
// "TEXT" for Store ID, "SINGLE_SELECT" (with $options) for Status.
const createProjectV2FieldMutation = `mutation($projectId: ID!, $name: String!, $dataType: ProjectV2CustomFieldType!, $options: [ProjectV2SingleSelectFieldOptionInput!]) {
  createProjectV2Field(input: {projectId: $projectId, name: $name, dataType: $dataType, singleSelectOptions: $options}) {
    projectV2Field {
      ... on ProjectV2FieldCommon { id name }
      ... on ProjectV2SingleSelectField { id name options { id name color description } }
    }
  }
}`

// updateProjectV2FieldMutation rewrites a single-select field's options,
// keeping every option whose id is given and adding the rest
// (brief.md#The board: "an option's id keeps it").
const updateProjectV2FieldMutation = `mutation($fieldId: ID!, $options: [ProjectV2SingleSelectFieldOptionInput!]!) {
  updateProjectV2Field(input: {fieldId: $fieldId, singleSelectOptions: $options}) {
    projectV2Field {
      ... on ProjectV2SingleSelectField { id name options { id name color description } }
    }
  }
}`

// linkProjectV2ToRepositoryMutation links a repository to a project
// (brief.md#The board: "links the project to the issue repo when it is not
// linked").
const linkProjectV2ToRepositoryMutation = `mutation($projectId: ID!, $repositoryId: ID!) {
  linkProjectV2ToRepository(input: {projectId: $projectId, repositoryId: $repositoryId}) {
    repository { id }
  }
}`

// addProjectV2ItemByIdMutation adds content (an issue or a pull request) to
// a project, idempotently - GitHub returns the existing item when the
// content is on the project already.
const addProjectV2ItemByIdMutation = `mutation($projectId: ID!, $contentId: ID!) {
  addProjectV2ItemById(input: {projectId: $projectId, contentId: $contentId}) {
    item { id }
  }
}`

// updateProjectV2ItemFieldValueSingleSelectMutation sets a single-select
// field's value (Status) on a project item.
const updateProjectV2ItemFieldValueSingleSelectMutation = `mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
  updateProjectV2ItemFieldValue(input: {projectId: $projectId, itemId: $itemId, fieldId: $fieldId, value: {singleSelectOptionId: $optionId}}) {
    projectV2Item { id }
  }
}`

// updateProjectV2ItemFieldValueTextMutation sets a text field's value (Store
// ID) on a project item.
const updateProjectV2ItemFieldValueTextMutation = `mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $text: String!) {
  updateProjectV2ItemFieldValue(input: {projectId: $projectId, itemId: $itemId, fieldId: $fieldId, value: {text: $text}}) {
    projectV2Item { id }
  }
}`

// optionsMap turns field's own options into the name -> id map ProjectV2
// carries.
func optionsMap(field projectFieldNode) map[string]string {
	m := make(map[string]string, len(field.Options))
	for _, o := range field.Options {
		m[o.Name] = o.ID
	}
	return m
}

// missingStatusOptions returns the statusOptions entries field.Options has
// no same-named option for yet, in statusOptions's own order.
func missingStatusOptions(field projectFieldNode) []statusOptionSpec {
	have := map[string]bool{}
	for _, o := range field.Options {
		have[o.Name] = true
	}
	var missing []statusOptionSpec
	for _, spec := range statusOptions {
		if !have[spec.Name] {
			missing = append(missing, spec)
		}
	}
	return missing
}

// createProjectV2Field creates a field on projectID; options is nil for a
// text field.
func (c *GraphQLClient) createProjectV2Field(ctx context.Context, projectID, name, dataType string, options []statusOptionSpec) (projectFieldNode, error) {
	var optionInputs []map[string]any
	for _, o := range options {
		optionInputs = append(optionInputs, map[string]any{"name": o.Name, "color": o.Color, "description": o.Description})
	}
	var resp struct {
		CreateProjectV2Field struct {
			ProjectV2Field projectFieldNode `json:"projectV2Field"`
		} `json:"createProjectV2Field"`
	}
	vars := map[string]any{"projectId": projectID, "name": name, "dataType": dataType, "options": optionInputs}
	if err := c.do(ctx, createProjectV2FieldMutation, vars, &resp); err != nil {
		return projectFieldNode{}, fmt.Errorf("github: create project field %q: %w", name, err)
	}
	return resp.CreateProjectV2Field.ProjectV2Field, nil
}

// updateStatusOptions rewrites field's options to keep every one it already
// has (its id preserved) and add missing, returning the field as GitHub
// reports it afterward.
func (c *GraphQLClient) updateStatusOptions(ctx context.Context, field projectFieldNode, missing []statusOptionSpec) (projectFieldNode, error) {
	var optionInputs []map[string]any
	for _, o := range field.Options {
		optionInputs = append(optionInputs, map[string]any{"id": o.ID, "name": o.Name, "color": o.Color, "description": o.Description})
	}
	for _, o := range missing {
		optionInputs = append(optionInputs, map[string]any{"name": o.Name, "color": o.Color, "description": o.Description})
	}
	var resp struct {
		UpdateProjectV2Field struct {
			ProjectV2Field projectFieldNode `json:"projectV2Field"`
		} `json:"updateProjectV2Field"`
	}
	vars := map[string]any{"fieldId": field.ID, "options": optionInputs}
	if err := c.do(ctx, updateProjectV2FieldMutation, vars, &resp); err != nil {
		return projectFieldNode{}, fmt.Errorf("github: update Status field options: %w", err)
	}
	return resp.UpdateProjectV2Field.ProjectV2Field, nil
}

// linkRepository links repoID to projectID.
func (c *GraphQLClient) linkRepository(ctx context.Context, projectID, repoID string) error {
	vars := map[string]any{"projectId": projectID, "repositoryId": repoID}
	if err := c.do(ctx, linkProjectV2ToRepositoryMutation, vars, nil); err != nil {
		return fmt.Errorf("github: link repository %s to project: %w", repoID, err)
	}
	return nil
}

// ensureFields resolves node's Status and Store ID fields, creating or
// completing either as brief.md#The board requires.
func (c *GraphQLClient) ensureFields(ctx context.Context, node *projectV2Node) (statusField projectFieldNode, storeIDFieldID string, err error) {
	var haveStatus bool
	for _, f := range node.Fields.Nodes {
		switch f.Name {
		case "Status":
			statusField, haveStatus = f, true
		case "Store ID":
			storeIDFieldID = f.ID
		}
	}

	if !haveStatus {
		var full []statusOptionSpec
		full = append(full, statusOptions...)
		statusField, err = c.createProjectV2Field(ctx, node.ID, "Status", "SINGLE_SELECT", full)
		if err != nil {
			return projectFieldNode{}, "", err
		}
	} else if missing := missingStatusOptions(statusField); len(missing) > 0 {
		statusField, err = c.updateStatusOptions(ctx, statusField, missing)
		if err != nil {
			return projectFieldNode{}, "", err
		}
	}

	if storeIDFieldID == "" {
		field, err := c.createProjectV2Field(ctx, node.ID, "Store ID", "TEXT", nil)
		if err != nil {
			return projectFieldNode{}, "", err
		}
		storeIDFieldID = field.ID
	}
	return statusField, storeIDFieldID, nil
}

// LookupProject reports whether ownerLogin/number resolves to a project,
// reading only (brief.md#The board's project lookup, the read a dry run can
// make without EnsureProject's own field or link mutation).
func (c *GraphQLClient) LookupProject(ctx context.Context, ownerLogin string, isOrg bool, number int) (bool, error) {
	node, err := c.lookupProjectV2(ctx, ownerLogin, isOrg, number)
	if err != nil {
		return false, err
	}
	return node != nil, nil
}

// EnsureProject resolves the project, creates any missing field or option,
// and links repoOwner/repoName to it when it is not linked yet
// (brief.md#The board).
func (c *GraphQLClient) EnsureProject(ctx context.Context, ownerLogin string, isOrg bool, number int, repoOwner, repoName string) (ProjectV2, error) {
	node, err := c.lookupProjectV2(ctx, ownerLogin, isOrg, number)
	if err != nil {
		return ProjectV2{}, err
	}
	if node == nil {
		return ProjectV2{}, &axi.Error{
			Msg:  fmt.Sprintf("github project for %s (number %d) was not found, or this token lacks the project scope", ownerLogin, number),
			Code: "MIRROR_PROJECT_NOT_FOUND",
			Help: []string{"gh auth refresh -s project"},
		}
	}

	statusField, storeIDFieldID, err := c.ensureFields(ctx, node)
	if err != nil {
		return ProjectV2{}, err
	}

	repoID, err := c.repositoryID(ctx, repoOwner, repoName)
	if err != nil {
		return ProjectV2{}, err
	}
	linked := false
	for _, r := range node.Repositories.Nodes {
		if r.ID == repoID {
			linked = true
			break
		}
	}
	if !linked {
		if err := c.linkRepository(ctx, node.ID, repoID); err != nil {
			return ProjectV2{}, err
		}
	}

	return ProjectV2{
		ID:             node.ID,
		StatusFieldID:  statusField.ID,
		StatusOptions:  optionsMap(statusField),
		StoreIDFieldID: storeIDFieldID,
	}, nil
}

// addProjectItem adds contentID to projectID, idempotently.
func (c *GraphQLClient) addProjectItem(ctx context.Context, projectID, contentID string) (string, error) {
	var resp struct {
		AddProjectV2ItemById struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"addProjectV2ItemById"`
	}
	vars := map[string]any{"projectId": projectID, "contentId": contentID}
	if err := c.do(ctx, addProjectV2ItemByIdMutation, vars, &resp); err != nil {
		return "", fmt.Errorf("github: add project item %s: %w", contentID, err)
	}
	return resp.AddProjectV2ItemById.Item.ID, nil
}

// setItemSingleSelect sets a single-select field's value on a project item.
func (c *GraphQLClient) setItemSingleSelect(ctx context.Context, projectID, itemID, fieldID, optionID string) error {
	vars := map[string]any{"projectId": projectID, "itemId": itemID, "fieldId": fieldID, "optionId": optionID}
	if err := c.do(ctx, updateProjectV2ItemFieldValueSingleSelectMutation, vars, nil); err != nil {
		return fmt.Errorf("github: set item %s field %s: %w", itemID, fieldID, err)
	}
	return nil
}

// setItemText sets a text field's value on a project item.
func (c *GraphQLClient) setItemText(ctx context.Context, projectID, itemID, fieldID, text string) error {
	vars := map[string]any{"projectId": projectID, "itemId": itemID, "fieldId": fieldID, "text": text}
	if err := c.do(ctx, updateProjectV2ItemFieldValueTextMutation, vars, nil); err != nil {
		return fmt.Errorf("github: set item %s field %s: %w", itemID, fieldID, err)
	}
	return nil
}

// PlaceItem adds contentID to proj, then sets its Status and Store ID field
// values, returning the item's node id (brief.md#The board).
func (c *GraphQLClient) PlaceItem(ctx context.Context, proj ProjectV2, contentID, status, storeID string) (string, error) {
	itemID, err := c.addProjectItem(ctx, proj.ID, contentID)
	if err != nil {
		return "", err
	}
	optionID, ok := proj.StatusOptions[status]
	if !ok {
		return "", fmt.Errorf("github: project has no Status option %q", status)
	}
	if err := c.setItemSingleSelect(ctx, proj.ID, itemID, proj.StatusFieldID, optionID); err != nil {
		return "", err
	}
	if err := c.setItemText(ctx, proj.ID, itemID, proj.StoreIDFieldID, storeID); err != nil {
		return "", err
	}
	return itemID, nil
}
