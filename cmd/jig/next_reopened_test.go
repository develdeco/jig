package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// reopenedFleet is `jig next --json`: the rows of the tickets and the ready
// ones, as these tests read them.
type reopenedFleet struct {
	Tickets []struct {
		ID    string   `json:"id"`
		State string   `json:"state"`
		Ready []string `json:"ready"`
	} `json:"tickets"`
	Next []string `json:"next"`
}

// queue puts slice id back to queued, as a fix queued after a build is.
func (r phaseRig) queue(t *testing.T, id string) {
	t.Helper()
	if err := r.st.WriteSliceState(r.ticket, id, store.SliceState{State: "queued"}); err != nil {
		t.Fatal(err)
	}
}

// TestNextNamesTheBuildOfAPublishedTicketsQueuedSlice: a published ticket that
// has not landed and gets a slice queued is at the build, ready, in `jig next
// <ticket>` and in the fleet's frontier; built green again it is green, at the
// review, and its worktree is not one to remove; once it landed, or the host
// reports its pull request merged, it is done whatever its slices.
func TestNextNamesTheBuildOfAPublishedTicketsQueuedSlice(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	e := r.env("")
	approveBrief(t, r.st, r.ticket)
	r.green(t, "s-claim")
	r.line(t, journal.Line{Event: journal.EventPr, Outcome: "opened", URL: "https://example.test/pull/7"})
	r.line(t, journal.Line{Event: journal.EventPublishDone})
	if p := r.nextJSON(t, e).Phase; p.Name != "land" || p.Code != "PHASE_DONE" {
		t.Fatalf("published: %+v, want it at the land", p)
	}

	// standing is the ticket's state in `jig next <ticket>` and its row in the
	// fleet: the state, the slices it offers, and whether the fleet's ready
	// list names it.
	standing := func(t *testing.T, merges mergeSource) (state string, offered []string, listed bool) {
		t.Helper()
		n, err := nextForTicket(r.st, project.Config{}, project.MachineProject{}, r.ticket, "", false, "", merges)
		if err != nil {
			t.Fatal(err)
		}
		fleet, err := nextForStore(r.st, project.Config{}, project.MachineProject{}, merges)
		if err != nil || len(fleet.Tickets) != 1 {
			t.Fatalf("the fleet: %+v, %v", fleet, err)
		}
		if row := fleet.Tickets[0]; row.State != n.State {
			t.Errorf("the fleet row says %q and jig next %s says %q", row.State, r.ticket, n.State)
		}
		for _, b := range n.Ready {
			offered = append(offered, b.Slices...)
		}
		return n.State, offered, len(fleet.Next) == 1 && fleet.Next[0] == r.ticket
	}
	// published is what the worktree advice reads of the ticket.
	published := func(t *testing.T, merges mergeSource) bool {
		t.Helper()
		f, err := newWorktreeSource(r.st, project.Config{}, project.MachineProject{}, merges).factsOf(r.ticket)
		if err != nil {
			t.Fatal(err)
		}
		return f.Published
	}
	if state, offered, listed := standing(t, nil); state != "done" || len(offered) != 0 || listed {
		t.Fatalf("published: state %q offers %v listed %v, want done with nothing", state, offered, listed)
	}
	if !published(t, nil) {
		t.Fatalf("published: the worktree advice reads it as not published")
	}

	// A fix queued after the publish.
	path := filepath.Join(r.root, "tickets", r.ticket, "slices.yaml")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fix := "  - id: e\n    workspace: alpha\n    goal: Build e.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n"
	if err := os.WriteFile(path, append(old, fix...), 0o644); err != nil {
		t.Fatal(err)
	}
	out, text := r.next(t, e)
	if p := out.Phase; p.Name != "build" || !p.Ready || p.Code != "" || p.Prompt != "Build JIG-1" {
		t.Errorf("a queued slice: %+v, want a ready build", p)
	}
	if !strings.Contains(text, "phase: build\n") || !strings.Contains(text, "prompt: Build JIG-1\n") {
		t.Errorf("jig next does not name the build:\n%s", text)
	}
	var fleet reopenedFleet
	asJSON, code := runMain(t, e, "", "next", "--store", r.root, "--json")
	if err := json.Unmarshal([]byte(asJSON), &fleet); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, asJSON)
	}
	if len(fleet.Tickets) != 1 || fleet.Tickets[0].State != "ready" || strings.Join(fleet.Tickets[0].Ready, ",") != "e" || strings.Join(fleet.Next, ",") != r.ticket {
		t.Errorf("the frontier = %+v, want JIG-1 ready with slice e", fleet)
	}
	if published(t, nil) {
		t.Errorf("a queued slice: the worktree advice reads the ticket as published, and would remove its worktree")
	}

	// Built green again: the review, and the ticket is green, not done.
	if err := r.st.WriteSliceState(r.ticket, "e", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	r.line(t, journal.Line{Event: journal.EventClaim, Slice: "e", Session: "s-claim"})
	r.line(t, journal.Line{Event: journal.EventVerified, Slice: "e", Session: "s-claim", Commit: sha40("f")})
	if p := r.nextJSON(t, e).Phase; p.Name != "review" || !p.Ready || p.Prompt != "Review JIG-1" {
		t.Errorf("fix built: %+v, want a ready review", p)
	}
	if state, offered, listed := standing(t, nil); state != "green" || len(offered) != 0 || listed {
		t.Errorf("fix built: state %q offers %v listed %v, want green with nothing to build", state, offered, listed)
	}
	if published(t, nil) {
		t.Errorf("fix built: the worktree advice reads the ticket as published, and would remove its worktree")
	}

	// The host reports the pull request merged, no landed line: done.
	r.queue(t, "e")
	merged := mergesOf{r.ticket: ready.Merge{Published: true, Repos: []ready.RepoMerge{{PR: "https://example.test/pull/7", State: ready.PRMerged, Met: true, By: "pr"}}}}
	if state, offered, listed := standing(t, merged); state != "done" || len(offered) != 0 || listed {
		t.Errorf("merged on the host with a queued slice: state %q offers %v listed %v, want done with nothing", state, offered, listed)
	}
	if !published(t, merged) {
		t.Errorf("merged on the host: the worktree advice reads the ticket as not published")
	}
	if state, offered, listed := standing(t, mergesOf{}); state != "ready" || strings.Join(offered, ",") != "e" || !listed {
		t.Errorf("the host reports it open: state %q offers %v listed %v, want ready with e", state, offered, listed)
	}

	// Landed: done again, at `jig next` and in the fleet.
	r.line(t, journal.Line{Event: journal.EventLanded})
	if p := r.nextJSON(t, e).Phase; p.Name != "publish" || p.Ready || p.Code != "PHASE_DONE" {
		t.Errorf("landed with a queued slice: %+v, want done", p)
	}
	if state, offered, listed := standing(t, nil); state != "done" || len(offered) != 0 || listed {
		t.Errorf("landed with a queued slice: state %q offers %v listed %v, want done with nothing", state, offered, listed)
	}
	if !published(t, nil) {
		t.Errorf("landed: the worktree advice reads the ticket as not published")
	}
	asJSON, code = runMain(t, e, "", "next", "--store", r.root, "--json")
	fleet = reopenedFleet{}
	if err := json.Unmarshal([]byte(asJSON), &fleet); code != 0 || err != nil || fleet.Tickets[0].State != "done" || len(fleet.Tickets[0].Ready) != 0 || len(fleet.Next) != 0 {
		t.Errorf("landed: the frontier = %+v (%v), want JIG-1 done and not next", fleet, err)
	}
}
