package fixture

import (
	"encoding/json"
	"fmt"
	"testing"
)

// GhPull is one pull request the fake gh (GhStub) holds: what the test says of
// it, and the rest the REST endpoint's row carries follows from that. Owner is
// the owner of the head repository (the repo's own owner for a branch of the
// repo itself, someone else for a fork), Head the branch and Base the branch
// it is into.
type GhPull struct {
	Number int
	// State is "open" (the default) or "closed".
	State string
	// Merged marks a closed pull request as merged, as the endpoint does: still
	// state "closed", with a merge time.
	Merged bool
	Owner  string
	Head   string
	Base   string
}

// GhPulls is the JSON the fake gh lists for GH_STUB_PULLS: pulls, in the
// endpoint's row shape, in order.
func GhPulls(t testing.TB, pulls ...GhPull) string {
	t.Helper()
	rows := make([]map[string]any, len(pulls))
	for i, p := range pulls {
		state := p.State
		if state == "" {
			state = "open"
		}
		row := map[string]any{
			"html_url": fmt.Sprintf("https://github.example/owner/repo/pull/%d", p.Number),
			"number":   p.Number,
			"state":    state,
			"head":     map[string]any{"ref": p.Head, "user": map[string]any{"login": p.Owner}},
			"base":     map[string]any{"ref": p.Base},
		}
		if p.Merged {
			row["merged_at"] = "2026-01-01T00:00:00Z"
		}
		rows[i] = row
	}
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("fixture: marshal the fake gh's pull requests: %v", err)
	}
	return string(data)
}
