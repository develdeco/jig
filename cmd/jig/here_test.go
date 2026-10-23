package main

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

// TestVerificationsIn: a verified commit is listed once per slice; a
// verify-pending line is pending until an oracle or verify-error line of the
// same commit and slice follows it.
func TestVerificationsIn(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		{Event: journal.EventVerified, Slice: "a", Commit: "aaaa", Attempt: 1},
		{Event: journal.EventVerified, Slice: "a", Commit: "aaaa", Attempt: 1},
		{Event: journal.EventVerified, Slice: "b", Commit: "aaaa", Attempt: 2},
		{Event: journal.EventVerified, Slice: "c"},
		{Event: journal.EventVerifyPending, Slice: "a", Commit: "p1", TS: "2026-01-01T00:00:00Z"},
		{Event: journal.EventVerifyPending, Slice: "b", Commit: "p2"},
		{Event: journal.EventOracle, Slice: "b", Commit: "p2"},
		{Event: journal.EventVerifyPending, Slice: "c", Commit: "p3"},
		{Event: journal.EventVerifyError, Slice: "c", Commit: "p3"},
		{Event: journal.EventVerifyPending, Slice: "d", Commit: "p4"},
		{Event: journal.EventOracle, Slice: "other", Commit: "p4"},
	}
	verified, pending := verificationsIn(lines)
	wantVerified := []hereVerified{{"a", "aaaa", 1}, {"b", "aaaa", 2}}
	if !reflect.DeepEqual(verified, wantVerified) {
		t.Errorf("verified = %+v, want %+v", verified, wantVerified)
	}
	wantPending := []herePending{{"a", "p1", "2026-01-01T00:00:00Z"}, {"d", "p4", ""}}
	if !reflect.DeepEqual(pending, wantPending) {
		t.Errorf("pending = %+v, want %+v", pending, wantPending)
	}
}

func TestParseWithPositionalsTakesThemAmongTheFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want []string
		json bool
	}{
		{nil, nil, false},
		{[]string{"JIG-1"}, []string{"JIG-1"}, false},
		{[]string{"JIG-1", "--json"}, []string{"JIG-1"}, true},
		{[]string{"--json", "JIG-1"}, []string{"JIG-1"}, true},
		{[]string{"--json", "JIG-1", "--json=false"}, []string{"JIG-1"}, false},
		{[]string{"a", "--json", "b"}, []string{"a", "b"}, true},
	}
	e := testEnv(t.TempDir())
	for _, c := range cases {
		fs := newFlagSet(e, "next")
		asJSON := fs.Bool("json", false, "")
		got, handled, err := parseWithPositionals(io.Discard, fs, c.args)
		if handled || err != nil {
			t.Errorf("%v: handled %v, err %v", c.args, handled, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) || *asJSON != c.json {
			t.Errorf("%v: positionals %v json %v, want %v and %v", c.args, got, *asJSON, c.want, c.json)
		}
	}
	fs := newFlagSet(e, "next")
	fs.Bool("json", false, "")
	if _, _, err := parseWithPositionals(io.Discard, fs, []string{"T-1", "--nope"}); err == nil || !strings.Contains(err.Error(), "--nope") {
		t.Errorf("an unknown flag after a positional: err %v, want it named", err)
	}

	if _, err := atMostOne([]string{"a", "b"}, "jig x", "ticket"); err == nil {
		t.Error("atMostOne of two: no error")
	}
	if got, err := atMostOne(nil, "jig x", "ticket"); got != "" || err != nil {
		t.Errorf("atMostOne of none = %q, %v", got, err)
	}
	if _, err := exactlyOne(nil, "jig x", "ticket"); err == nil || !strings.Contains(err.Error(), "missing required ticket") {
		t.Errorf("exactlyOne of none: err %v", err)
	}
}

// TestHereNamesQuestionsVerifiedAndPendingVerifications: the ticket's open
// question, the commits that verified and the verifications still running are
// in the text and in the JSON, and a section a later ticket registers adds to
// both.
func TestHereNamesQuestionsVerifiedAndPendingVerifications(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "Which tone?\nFormal or warm."}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-002", Slice: "a", Status: "answered", Body: "Old.", Answer: "yes"}); err != nil {
		t.Fatal(err)
	}
	for _, l := range []journal.Line{
		{Event: journal.EventVerified, Slice: "a", Commit: "0123456789abcdef0123", Attempt: 1},
		{Event: journal.EventVerifyPending, Slice: "b", Commit: "fedcba9876543210fedc"},
	} {
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}

	e := testEnv(fx.Home).inDir(fx.RepoDir)
	out, code := runMain(t, e, "", "here", fx.Ticket)
	if code != 0 {
		t.Fatalf("jig here: exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"  a,green,1,-,-\n",
		"  c,needs-input,1,-,q-001\n",
		"verified[1]{slice,commit}:\n  a,0123456789ab\n",
		"questions[1]{id,slice,question}:\n  q-001,c,Which tone?\n",
		"pending_verifications[1]{slice,commit}:\n  b,fedcba987654\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("jig here lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "q-002") {
		t.Errorf("jig here lists an answered question:\n%s", out)
	}

	jsonOut, code := runMain(t, e, "", "here", fx.Ticket, "--json")
	if code != 0 {
		t.Fatalf("jig here --json: exit %d\n%s", code, jsonOut)
	}
	var got struct {
		State struct {
			Verified []struct{ Slice, Commit string }   `json:"verified"`
			Pending  []struct{ Slice, Commit string }   `json:"pending_verifications"`
			Question []struct{ ID, Slice, Body string } `json:"questions"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("jig here --json: %v\n%s", err, jsonOut)
	}
	if len(got.State.Verified) != 1 || got.State.Verified[0].Commit != "0123456789abcdef0123" || len(got.State.Pending) != 1 || got.State.Pending[0].Slice != "b" || len(got.State.Question) != 1 || got.State.Question[0].ID != "q-001" {
		t.Errorf("--json state = %+v, want the verified commit in full, one pending verification and one question", got.State)
	}

	// Sections other tickets register add blocks after the ticket's, and a
	// JSON value under their name.
	where := locate.Where{How: locate.HowStore, Project: "fixture", StorePath: fx.StoreDir, Ticket: fx.Ticket, TicketHow: locate.HowArg}
	hc := hereCtx{e: e, Where: where, Store: st, JigHome: fx.Home}
	view, err := buildHereView(hc)
	if err != nil {
		t.Fatalf("buildHereView: %v", err)
	}
	sections := []hereSection{
		{Order: 20, Name: "second", Blocks: func(hereCtx) ([]string, error) { return []string{"second: 2"}, nil }},
		{Order: 10, Name: "first", Blocks: func(hereCtx) ([]string, error) { return []string{"first: 1"}, nil }, JSON: func(hereCtx) (any, error) { return map[string]int{"n": 1}, nil }},
	}
	sorted := newHereSections()
	for _, s := range sections {
		sorted.add(s)
	}
	blocks, err := view.blocks(hc, sorted.sorted())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(blocks); n < 2 || blocks[n-2] != "first: 1" || blocks[n-1] != "second: 2" {
		t.Errorf("blocks end %v, want the sections in Order after the ticket's blocks", blocks)
	}
	js, err := view.json(hc, sorted.sorted())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(js.Sections, map[string]any{"first": map[string]int{"n": 1}}) {
		t.Errorf("JSON sections = %v, want only first's, under its name", js.Sections)
	}
}

// TestNoStoreErrorNamesTheCloneCommand: a marker whose store
// is not cloned gets the command to clone it, and verbs say the same.
func TestNoStoreErrorNamesTheCloneCommand(t *testing.T) {
	t.Parallel()
	err := noStoreError(locate.Where{How: locate.HowMarker, Project: "alpha", Root: "/work/app", StoreRemote: "git@example.com:o/alpha-tickets.git"})
	if err.Code != "VALIDATION_ERROR" || !strings.Contains(err.Msg, "not cloned") || len(err.Help) != 1 || !strings.Contains(err.Help[0], "git clone git@example.com:o/alpha-tickets.git") || !strings.Contains(err.Help[0], "alpha-tickets") {
		t.Errorf("marker with a remote: %+v", err)
	}
	err = noStoreError(locate.Where{How: locate.HowNone})
	if err.Code != "VALIDATION_ERROR" || !strings.Contains(err.Msg, "no jig store found for this directory") {
		t.Errorf("none: %+v", err)
	}
}
