package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

type containerReadyOut struct {
	State  string     `json:"state"`
	Prompt string     `json:"prompt"`
	Waits  []nextWait `json:"waits"`
}

func (r chartRig) containerReady(t *testing.T, id string) containerReadyOut {
	t.Helper()
	out, code := r.run(t, "next", id, "--json")
	var n containerReadyOut
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		t.Fatalf("jig next %s --json: exit %d, %v\n%s", id, code, err, out)
	}
	return n
}

// TestContainerReadyNextSaysWaitingThenReady: `jig next` of a multi-task says
// waiting with a row for its task until the task has landed, then ready with the
// prompt Publish TK-4, in the blocks and in --json; a container nobody has
// graduated into is not ready.
func TestContainerReadyNextSaysWaitingThenReady(t *testing.T) {
	r := landRig(t)
	rec, err := r.st.ReadTicket("TK-4")
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.st.ResolveTicketBranch("TK-4", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 5, Base: base}))

	got := r.containerReady(t, "TK-4")
	if got.State != "waiting" || got.Prompt != "" || len(got.Waits) != 1 || got.Waits[0] != (nextWait{Ticket: "TK-5", Standing: "building"}) {
		t.Fatalf("jig next TK-4 = %+v, want waiting on TK-5 (building)", got)
	}
	text, code := r.run(t, "next", "TK-4")
	if code != 0 || !strings.Contains(text, "state: waiting") || !strings.Contains(text, "TK-5,building") || strings.Contains(text, "prompt:") {
		t.Errorf("jig next TK-4 (exit %d) lacks the waiting row:\n%s", code, text)
	}

	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "TK-4", URL: "https://github.example/owner/repo/pull/5"},
		{Event: journal.EventPublishDone, Into: "TK-4"},
	} {
		if err := journal.Append(r.st, "TK-5", l); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.containerReady(t, "TK-4"); got.State != "waiting" || len(got.Waits) != 1 || got.Waits[0].Standing != "published" {
		t.Fatalf("jig next TK-4 with TK-5 published = %+v, want it published and waited on", got)
	}

	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 5, Base: base, State: "closed", Merged: true}))
	got = r.containerReady(t, "TK-4")
	if got.State != "ready" || got.Prompt != "Publish TK-4" || len(got.Waits) != 0 {
		t.Fatalf("jig next TK-4 once TK-5 landed = %+v, want ready, Publish TK-4", got)
	}
	if text, _ := r.run(t, "next", "TK-4"); !strings.Contains(text, "state: ready") || !strings.Contains(text, "prompt: Publish TK-4") {
		t.Errorf("jig next TK-4 does not say ready with its prompt:\n%s", text)
	}

	// The issue still waits for the multi-task and its other tasks.
	if got := r.containerReady(t, "TK-1"); got.State != "waiting" || len(got.Waits) != 3 {
		t.Errorf("jig next TK-1 = %+v, want waiting on TK-2, TK-3 and TK-4", got)
	}
}

// TestContainerReadyValidateRefusesATaskBlockedByItsContainer: a task entry that
// names the multi-task it sits under, or the chart's issue, is a problem of
// `jig validate <chart>`, by position or by id; a ticket whose record names its
// own container is a problem of `jig validate <ticket>`; a task that waits for a
// sibling is none.
func TestContainerReadyValidateRefusesATaskBlockedByItsContainer(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "kind: issue\n", false)
	r.chart(t, "a", "issue: SHOP-1\ntickets:\n"+
		"  - title: Multi\n    kind: multi-task\n"+
		"  - title: Own\n    under: \"#1\"\n    blocked_by:\n      - ref: \"#1\"\n"+
		"  - title: Sibling\n    under: \"#1\"\n    blocked_by:\n      - ref: \"#2\"\n"+
		"  - title: Issue\n    blocked_by:\n      - ref: SHOP-1\n", "")
	got := chartKindLines(t, r, "a")
	want := []string{
		`entry 2 ("Own"): blocked_by ref "#1" names the multi-task (entry 1) this task is part of: it publishes only after its tasks have landed, so the wait never ends`,
		`entry 4 ("Issue"): blocked_by ref "SHOP-1" names the issue SHOP-1 this task is part of: it publishes only after its tasks have landed, so the wait never ends`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	r.ticket(t, "SHOP-2", "kind: multi-task\n", false)
	r.ticket(t, "SHOP-3", "parent: SHOP-2\nblocked_by:\n  - ticket: SHOP-2\n    kind: merged\n", false)
	r.ticket(t, "SHOP-4", "parent: SHOP-2\nblocked_by:\n  - ticket: SHOP-3\n    kind: merged\n", false)
	if got := validateTicketOwnContainer(r.st, "SHOP-3"); len(got) != 1 || !strings.Contains(got[0], `blocked_by "SHOP-2"`) {
		t.Errorf("validate of a task blocked by its multi-task = %v, want one problem", got)
	}
	if got := validateTicketOwnContainer(r.st, "SHOP-4"); len(got) != 0 {
		t.Errorf("validate of a task blocked by a sibling = %v, want none", got)
	}
}
