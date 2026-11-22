package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestItemByNumber checks one query by number decodes the item, and that a
// missing number, a pull request and an unseen repository (each a NOT_FOUND
// answer) are ITEM_NOT_FOUND while any other error stays a plain read error.
func TestItemByNumber(t *testing.T) {
	answer := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, vars := decodeGraphQLRequest(t, r)
		if !strings.Contains(query, "issue(number: $number)") {
			t.Fatalf("unexpected query: %s", query)
		}
		if vars["owner"] != "o" || vars["name"] != "r" || vars["number"] != float64(48) {
			t.Fatalf("variables = %v", vars)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := newFastClient(srv.URL, "t")

	answer = `{"data":{"repository":{"issue":{"id":"I_1","number":48,"title":"T","body":"B","url":"https://github.com/o/r/issues/48",
	  "closed":true,"stateReason":"COMPLETED","author":{"login":"ann"},
	  "labels":{"nodes":[{"name":"bug"},{"name":"x"}]},"subIssues":{"totalCount":2},
	  "closedByPullRequestsReferences":{"totalCount":3}}}}}`
	d, err := c.ItemByNumber(context.Background(), "o", "r", 48)
	if err != nil {
		t.Fatalf("ItemByNumber: %v", err)
	}
	if d.Number != 48 || d.NodeID != "I_1" || d.Title != "T" || d.Body != "B" || d.URL != "https://github.com/o/r/issues/48" ||
		d.Open || d.StateReason != "COMPLETED" || d.Author != "ann" || len(d.Labels) != 2 || d.Labels[1] != "x" ||
		d.SubItems != 2 || d.LinkedPullRequests != 3 {
		t.Fatalf("item = %+v", d)
	}

	for name, body := range map[string]string{
		"missing or pull request": `{"data":{"repository":{"issue":null}},"errors":[{"type":"NOT_FOUND","path":["repository","issue"],"message":"Could not resolve to an Issue with the number of 48."}]}`,
		"unseen repository":       `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"message":"Could not resolve to a Repository with the name 'o/r'."}]}`,
		"null with no error":      `{"data":{"repository":{"issue":null}}}`,
	} {
		answer = body
		_, err := c.ItemByNumber(context.Background(), "o", "r", 48)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "ITEM_NOT_FOUND" || !strings.Contains(ae.Msg, "o/r#48") {
			t.Errorf("%s: err = %v; want ITEM_NOT_FOUND naming o/r#48", name, err)
		}
	}

	answer = `{"errors":[{"type":"FORBIDDEN","message":"nope"}]}`
	_, err = c.ItemByNumber(context.Background(), "o", "r", 48)
	var ae *axi.Error
	if err == nil || errors.As(err, &ae) || !strings.Contains(err.Error(), "nope") {
		t.Errorf("other error = %v; want the client's cause, not ITEM_NOT_FOUND", err)
	}
}
