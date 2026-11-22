package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/ready"
)

// TestDerivePhaseSettledAsksTheHostOnlyOfARebuildingTicket: the readers with no
// merge source of their own (`jig here`, the hooks, a refusal's prompt) settle a
// ticket the rule builds again against the host: a ticket whose pull request
// merged is done whatever its slices, and the host is never asked of a ticket
// that is not built again.
func TestDerivePhaseSettledAsksTheHostOnlyOfARebuildingTicket(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	approveBrief(t, r.st, r.ticket)
	r.green(t, "s-claim")
	r.line(t, journal.Line{Event: journal.EventPr, Outcome: "opened", URL: "https://example.test/pull/7"})
	r.line(t, journal.Line{Event: journal.EventPublishDone})

	asked := 0
	merged := mergesOf{r.ticket: ready.Merge{Published: true, Repos: []ready.RepoMerge{{PR: "https://example.test/pull/7", State: ready.PRMerged, Met: true, By: "pr"}}}}
	host := func() mergeSource { asked++; return merged }

	if got, err := derivePhaseSettled(r.st, r.ticket, host); err != nil || got.Rebuilding() || asked != 0 {
		t.Fatalf("published and built: rebuilding %v, host asked %d times (%v), want neither", got.Rebuilding(), asked, err)
	}

	path := filepath.Join(r.root, "tickets", r.ticket, "slices.yaml")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fix := "  - id: e\n    workspace: alpha\n    goal: Build e.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n"
	if err := os.WriteFile(path, append(old, fix...), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := derivePhase(r.st, r.ticket); err != nil || !got.Rebuilding() || got.Current != phase.Build {
		t.Fatalf("host-blind: rebuilding %v current %s (%v), want built again at the build", got.Rebuilding(), got.Current, err)
	}
	got, err := derivePhaseSettled(r.st, r.ticket, host)
	if err != nil || asked != 1 || got.Rebuilding() || got.Current != phase.Land {
		t.Errorf("merged on the host: rebuilding %v current %s, asked %d (%v), want done at the land after one question", got.Rebuilding(), got.Current, asked, err)
	}
	open := func() mergeSource { return mergesOf{} }
	if got, err := derivePhaseSettled(r.st, r.ticket, open); err != nil || !got.Rebuilding() || got.Current != phase.Build {
		t.Errorf("open on the host: rebuilding %v current %s (%v), want built again", got.Rebuilding(), got.Current, err)
	}
	if got, err := derivePhaseSettled(r.st, r.ticket, nil); err != nil || !got.Rebuilding() {
		t.Errorf("no merge source: rebuilding %v (%v), want it as the journal says", got.Rebuilding(), err)
	}
}
