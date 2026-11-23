package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/home"
)

// prStateOf is the state the roster's row of ticket id gives its first pull
// request.
func prStateOf(t *testing.T, r chartRig, id string) string {
	t.Helper()
	out, code := r.run(t, "next", "--json", "--roster")
	if code != 0 {
		t.Fatalf("roster: exit %d\n%s", code, out)
	}
	rows, _ := rosterRows(t, out)
	for _, row := range rows {
		if row["id"] == id {
			d, _ := row["digest"].(map[string]any)
			if prs, _ := d["prs"].([]any); len(prs) > 0 {
				return prs[0].(map[string]any)["state"].(string)
			}
		}
	}
	return ""
}

// agedKept makes every pull request state this machine kept an hour old.
func agedKept(t *testing.T, jigHome string) {
	t.Helper()
	path := home.PullRequestStatesPath(jigHome)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		PullRequests map[string]map[string]any `json:"pull_requests"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	for _, rec := range f.PullRequests {
		rec["at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	}
	data, _ = json.Marshal(f)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMergeRefreshAsksTheHostOnlyForOldStates: the roster never asks the host,
// and the refresh the screens run in the background to learn what merged
// asks it only for the states kept longer ago than the listing's age. Not
// parallel: the host is the fake gh on the process's PATH.
func TestMergeRefreshAsksTheHostOnlyForOldStates(t *testing.T) {
	r := landRig(t)
	r.host(t, false, false)
	if out, code := r.run(t, "next", "--refresh-merges"); code != 0 || !strings.Contains(out, "refreshed: 2") {
		t.Fatalf("first refresh: exit %d\n%s", code, out)
	}
	if got := prStateOf(t, r, "TK-2"); got != "open" {
		t.Fatalf("TK-2's pull request reads %q, want open", got)
	}

	// It merges on the host. The roster does not ask, and a fresh kept state
	// stands for the listing too.
	r.host(t, true, false)
	if got := prStateOf(t, r, "TK-2"); got != "open" {
		t.Errorf("the roster asked the host: %q", got)
	}
	r.run(t, "next", "--refresh-merges")
	if got := prStateOf(t, r, "TK-2"); got != "open" {
		t.Errorf("the refresh asked for a state kept a moment ago: %q", got)
	}

	// An old state is asked again by the listing, and the roster reads it.
	agedKept(t, r.home)
	if got := prStateOf(t, r, "TK-2"); got != "open" {
		t.Errorf("the roster asked the host for an old state: %q", got)
	}
	r.run(t, "next", "--refresh-merges")
	if got := prStateOf(t, r, "TK-2"); got != "merged" {
		t.Errorf("after the refresh the roster reads %q, want merged", got)
	}
}
