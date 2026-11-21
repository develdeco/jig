package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
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

	// The multi-task's issue is the task's container too.
	r.ticket(t, "SHOP-9", "kind: issue\n", false)
	r.chart(t, "b", "issue: SHOP-9\ntickets:\n  - id: SHOP-2\n    title: Multi\n", "")
	r.ticket(t, "SHOP-5", "parent: SHOP-2\nblocked_by:\n  - ticket: SHOP-9\n    kind: merged\n", false)
	if got := validateTicketOwnContainer(r.st, "SHOP-5"); len(got) != 1 || !strings.Contains(got[0], `blocked_by "SHOP-9"`) {
		t.Errorf("validate of a task under a multi-task blocked by its issue = %v, want one problem", got)
	}
}

func publishLines(t *testing.T, r chartRig, id string) []string {
	t.Helper()
	lines, err := journal.Read(r.st, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range lines {
		if journal.IsPublishEvent(l.Event) {
			out = append(out, l.Event)
		}
	}
	return out
}

// TestContainerReadyAnUnmetPreconditionHoldsNextAndPublish: with every task landed
// and a registered precondition unmet, `jig next` says waiting with the
// precondition's row and prompt (the first prompt of the unmet ones), its help
// names the precondition and not the children, and `jig publish --yes` refuses
// with the precondition's code and the prompt, writing nothing.
func TestContainerReadyAnUnmetPreconditionHoldsNextAndPublish(t *testing.T) {
	// Not parallel: the registry is the package's.
	r := landRig(t)
	rec, err := r.st.ReadTicket("TK-4")
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.st.ResolveTicketBranch("TK-4", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "TK-4", URL: "https://github.example/owner/repo/pull/5"},
		{Event: journal.EventPublishDone, Into: "TK-4"},
	} {
		if err := journal.Append(r.st, "TK-5", l); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 5, Base: base, State: "closed", Merged: true}))
	saved := land.Preconditions
	defer func() { land.Preconditions = saved }()
	land.Preconditions = []land.Precondition{
		func(land.Container) ([]land.Unmet, error) {
			return []land.Unmet{{Code: "FAKE_NOT_MET", Why: "the fake suite has not run", Prompt: "Run the fake suite"}}, nil
		},
		func(land.Container) ([]land.Unmet, error) {
			return []land.Unmet{{Code: "FAKE_TWO", Why: "another", Prompt: "Do the other"}}, nil
		},
	}

	out, code := r.run(t, "next", "TK-4", "--json")
	var got struct {
		State  string     `json:"state"`
		Prompt string     `json:"prompt"`
		Waits  []nextWait `json:"waits"`
		Unmet  []nextUnmet
	}
	if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil {
		t.Fatalf("jig next TK-4 --json: exit %d, %v\n%s", code, err, out)
	}
	if got.State != "waiting" || got.Prompt != "Run the fake suite" || len(got.Waits) != 0 || len(got.Unmet) != 2 || got.Unmet[0].Code != "FAKE_NOT_MET" {
		t.Fatalf("jig next TK-4 = %+v, want waiting on the fake precondition with its prompt", got)
	}
	text, _ := r.run(t, "next", "TK-4")
	if !strings.Contains(text, "FAKE_NOT_MET,the fake suite has not run,Run the fake suite") || !strings.Contains(text, "TK-4 waits for the fake suite has not run: ask `Run the fake suite`") || strings.Contains(text, "waits for its children") {
		t.Errorf("jig next TK-4 does not name the precondition:\n%s", text)
	}

	out, code = r.run(t, "publish", "TK-4", "--yes")
	if code == 0 || !strings.Contains(out, "FAKE_NOT_MET") || !strings.Contains(out, "Run the fake suite") || strings.Contains(out, "no children") {
		t.Errorf("publish TK-4: exit %d, want the precondition's code and prompt\n%s", code, out)
	}
	if got := publishLines(t, r, "TK-4"); len(got) != 0 {
		t.Errorf("a refused publish wrote %v", got)
	}
}

// TestContainerPublishVerb:`jig publish TK-4 --yes` of a multi-task refuses
// PUBLISH_NOT_READY naming its task while it has not landed, writing nothing; it
// takes none of the task forms; once the task has landed it records the
// multi-task's pull request and publish-done, and `jig next` then says
// published, with the prompt Land TK-4.
func TestContainerPublishVerb(t *testing.T) {
	r := landRig(t)
	rec, err := r.st.ReadTicket("TK-4")
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.st.ResolveTicketBranch("TK-4", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "TK-4", URL: "https://github.example/owner/repo/pull/5"},
		{Event: journal.EventPublishDone, Into: "TK-4"},
	} {
		if err := journal.Append(r.st, "TK-5", l); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 5, Base: base}))

	out, code := r.run(t, "publish", "TK-4", "--yes")
	if code == 0 || !strings.Contains(out, "PUBLISH_NOT_READY") || !strings.Contains(out, "TK-5 (published)") {
		t.Fatalf("publish TK-4 before TK-5 landed: exit %d, want PUBLISH_NOT_READY naming TK-5\n%s", code, out)
	}
	if got := publishLines(t, r, "TK-4"); len(got) != 0 {
		t.Fatalf("a refused publish wrote %v", got)
	}
	for _, flag := range []string{"--body", "--approve", "--repo=app"} {
		if out, code := r.run(t, "publish", "TK-4", flag); code == 0 || !strings.Contains(out, "is a multi-task") {
			t.Errorf("publish TK-4 %s: exit %d, want a refusal of the form\n%s", flag, code, out)
		}
	}

	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 5, Base: base, State: "closed", Merged: true}))
	out, code = r.run(t, "publish", "TK-4", "--yes")
	if code != 0 {
		t.Fatalf("publish TK-4 once TK-5 landed: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "TK-4 has its pull request: ask `Land TK-4` to merge it") {
		t.Errorf("the publish does not say what comes next:\n%s", out)
	}
	if subject, err := gitx.Run(r.root, "log", "-1", "--format=%s"); err != nil || strings.TrimSpace(subject) != "TK-4: publish" {
		t.Errorf("the store's last commit = %q, %v, want the publish's", subject, err)
	}
	if dirty, err := gitx.Run(r.root, "status", "--porcelain"); err != nil || strings.TrimSpace(dirty) != "" {
		t.Errorf("the store is not clean after the publish: %q, %v", dirty, err)
	}
	got := publishLines(t, r, "TK-4")
	if want := []string{journal.EventPrBody, journal.EventPublishApproved, journal.EventPr, journal.EventPublishDone}; !reflect.DeepEqual(got, want) {
		t.Errorf("TK-4's publish lines = %v, want %v", got, want)
	}
	if next := r.containerReady(t, "TK-4"); next.State != "published" || next.Prompt != "Land TK-4" {
		t.Errorf("jig next TK-4 after publishing = %+v, want published, Land TK-4", next)
	}
}
