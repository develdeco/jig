package github

import (
	"context"
	"errors"
	"fmt"

	"github.com/develdeco/jig/internal/axi"
)

// itemByNumberQuery reads one issue by number. issue(number:) answers null
// with a NOT_FOUND error for a pull request's number, so a pull request is
// never taken for an item.
const itemByNumberQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      id number title body url closed stateReason
      author { login }
      labels(first: 100) { nodes { name } }
      subIssues { totalCount }
      blockedBy(first: 50) { nodes { id number } }
      closedByPullRequestsReferences(first: 1, includeClosedPrs: true) { totalCount }
    }
  }
}`

// ItemByNumber reads the issue owner/repo#number. LinkedPullRequests counts
// the pull requests that reference the issue as closing it, open, merged or
// closed, which is what GitHub lists as the issue's linked pull requests.
func (c *GraphQLClient) ItemByNumber(ctx context.Context, owner, repo string, number int) (ItemDetail, error) {
	var resp struct {
		Repository *struct {
			Issue *struct {
				ID          string `json:"id"`
				Number      int    `json:"number"`
				Title       string `json:"title"`
				Body        string `json:"body"`
				URL         string `json:"url"`
				Closed      bool   `json:"closed"`
				StateReason string `json:"stateReason"`
				Author      *struct {
					Login string `json:"login"`
				} `json:"author"`
				Labels struct {
					Nodes []struct {
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"labels"`
				BlockedBy struct {
					Nodes []struct {
						ID     string `json:"id"`
						Number int    `json:"number"`
					} `json:"nodes"`
				} `json:"blockedBy"`
				SubIssues struct {
					TotalCount int `json:"totalCount"`
				} `json:"subIssues"`
				ClosedBy struct {
					TotalCount int `json:"totalCount"`
				} `json:"closedByPullRequestsReferences"`
			} `json:"issue"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": repo, "number": number}
	err := c.do(ctx, itemByNumberQuery, vars, &resp)
	var nf *notFoundError
	if errors.As(err, &nf) || (err == nil && (resp.Repository == nil || resp.Repository.Issue == nil)) {
		return ItemDetail{}, &axi.Error{
			Msg:  fmt.Sprintf("%s/%s#%d is not an issue this token can read", owner, repo, number),
			Code: "ITEM_NOT_FOUND",
			Help: []string{"Check the number: a pull request, a missing number and a repository the token cannot see all read as not found"},
		}
	}
	if err != nil {
		return ItemDetail{}, fmt.Errorf("github: read %s/%s#%d: %w", owner, repo, number, err)
	}
	it := resp.Repository.Issue
	d := ItemDetail{
		Number: it.Number, NodeID: it.ID, Title: it.Title, Body: it.Body, URL: it.URL,
		Open: !it.Closed, StateReason: it.StateReason,
		SubItems: it.SubIssues.TotalCount, LinkedPullRequests: it.ClosedBy.TotalCount,
	}
	if it.Author != nil {
		d.Author = it.Author.Login
	}
	for _, b := range it.BlockedBy.Nodes {
		d.BlockedBy = append(d.BlockedBy, BlockerRef{Number: b.Number, NodeID: b.ID})
	}
	for _, l := range it.Labels.Nodes {
		d.Labels = append(d.Labels, l.Name)
	}
	return d, nil
}
