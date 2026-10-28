package tui

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// The reader is checked against what internal/axi writes, so a change to the
// register that the screens would misread fails here.

func TestParseAxiReadsWhatAxiWrites(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	axi.Render(&out,
		"ticket: JIG-1",
		"state: building",
		axi.KV("claim", [][2]string{{"slice", "a"}, {"note", `says "hi", twice`}}),
		axi.Table("slices", []string{"id", "state", "question"}, [][]string{{"a", "green", "-"}, {"b", "building", "needs, a comma"}}),
		axi.Table("empty", []string{"id"}, nil),
		axi.Help("first: `jig x`", "second, with a comma"),
	)
	doc := parseAxi(out.String())

	if doc.Scalars["ticket"] != "JIG-1" || doc.Scalars["state"] != "building" {
		t.Errorf("scalars = %v", doc.Scalars)
	}
	if got := doc.KV["claim"]; got["slice"] != "a" || got["note"] != `says "hi", twice` {
		t.Errorf("claim block = %v, want the quoted value restored", got)
	}
	rows := doc.Tables["slices"].Maps()
	want := []map[string]string{
		{"id": "a", "state": "green", "question": "-"},
		{"id": "b", "state": "building", "question": "needs, a comma"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("slices = %v, want %v", rows, want)
	}
	if tbl, ok := doc.Tables["empty"]; !ok || len(tbl.Rows) != 0 || !reflect.DeepEqual(tbl.Cols, []string{"id"}) {
		t.Errorf("empty table = %+v, %v", tbl, ok)
	}
	if wantHelp := []string{"first: `jig x`", "second, with a comma"}; !reflect.DeepEqual(doc.Help, wantHelp) {
		t.Errorf("help = %q, want %q", doc.Help, wantHelp)
	}
	if doc.Err != nil {
		t.Errorf("a status is not a refusal: %+v", doc.Err)
	}
}

func TestParseAxiReadsARefusal(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	axi.RenderError(&out, &axi.Error{Msg: "ticket JIG-9 not found", Code: "VALIDATION_ERROR", Help: []string{"Run `jig ticket new`"}})
	doc := parseAxi(out.String())
	if doc.Err == nil || doc.Err.Msg != "ticket JIG-9 not found" || doc.Err.Code != "VALIDATION_ERROR" {
		t.Fatalf("err = %+v", doc.Err)
	}
	if !reflect.DeepEqual(doc.Err.Help, []string{"Run `jig ticket new`"}) {
		t.Errorf("help = %q", doc.Err.Help)
	}
}

func TestParseAxiLeavesWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()
	doc := parseAxi("a note before the blocks\r\nstate: parked\r\n  stray indented line\r\nodd[x]{y}: nope\r\n")
	if doc.Scalars["state"] != "parked" || len(doc.Tables) != 0 || doc.Err != nil {
		t.Errorf("doc = %+v", doc)
	}
}

func TestParseStatusReadsTheTablesJigStatusPrints(t *testing.T) {
	t.Parallel()
	info := parseStatus(statusJIG2())
	if info.Err != nil || info.State != "green" {
		t.Fatalf("info = %+v", info)
	}
	if want := (statusSlice{ID: "a", State: "green", Attempts: 1, Goal: "Load the config", Workspace: "alpha", Oracle: "unit"}); len(info.Slices) != 1 || info.Slices[0] != want {
		t.Errorf("slices = %+v, want %+v: green after 1 try, with no blocker or question", info.Slices, want)
	}
	if len(info.Asks) != 2 || info.Asks[0] != (statusAsk{ID: "r1-f1", Risk: "high", Where: "alpha/a.go:3", Title: "Unchecked error", Why: "needs a human"}) {
		t.Errorf("asks = %+v", info.Asks)
	}
	if len(info.Questions) != 0 {
		t.Errorf("questions = %+v", info.Questions)
	}

	one := parseStatus(statusJIG1())
	if got := one.Slices[3]; got.BlockedBy != "b+c" || got.State != "queued" {
		t.Errorf("slice d = %+v, want queued behind b+c", got)
	}
	if got := one.Slices[2]; got.Question != "q-001" {
		t.Errorf("slice c = %+v, want its question q-001", got)
	}
	if len(one.Questions) != 1 || one.Questions[0] != (statusQuestion{ID: "q-001", Slice: "c", Status: "open"}) {
		t.Errorf("questions = %+v", one.Questions)
	}

	refused := parseStatus("error: ticket JIG-9 not found\ncode: VALIDATION_ERROR\n")
	if refused.Err == nil || len(refused.Slices) != 0 {
		t.Errorf("a refused status = %+v", refused)
	}
}

func TestParseRoster(t *testing.T) {
	t.Parallel()
	rows, err := parseRoster("a checkpoint note\n" + rosterJSON())
	if err != nil {
		t.Fatal(err)
	}
	want := []rosterRow{{"JIG-1", "Greeting copy", "ready"}, {"JIG-2", "Config loader", "ready"}, {"JIG-3", "Release notes", "blocked"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}

	if _, err := parseRoster("error: no jig store found\ncode: ERROR\n"); err == nil || !strings.Contains(err.Error(), "no jig store found") {
		t.Errorf("a refusal: err = %v, want the verb's words", err)
	}
	if _, err := parseRoster("tickets: none\n"); err == nil {
		t.Errorf("a list that is not JSON read without error")
	}
	if _, err := parseRoster("{not json\n"); err == nil {
		t.Errorf("broken JSON read without error")
	}
}

// TestParseStatusReadsTheRecordsOfTheSections: the columns and blocks the
// rounds, findings and publish sections of `jig status` print are read by name,
// and a status that predates them reads as having none.
func TestParseStatusReadsTheRecordsOfTheSections(t *testing.T) {
	t.Parallel()
	info := parseStatus(statusJIG2Done())
	if info.FixBudget != "1 of 3 rounds used" {
		t.Errorf("fix budget = %q", info.FixBudget)
	}
	if len(info.Rounds) != 2 {
		t.Fatalf("rounds = %+v, want two", info.Rounds)
	}
	want := statusRound{
		N: 1, Verdict: "findings", Reviewer: "s-reviewer", Harness: "claude", Model: "claude-opus-5", Independent: "true",
		Fix: 1, Ask: 2, Note: 1, Scope: "full", Base: sha('0'), Head: sha('c'), Files: 4,
	}
	if info.Rounds[0] != want {
		t.Errorf("round 1 = %+v, want %+v", info.Rounds[0], want)
	}
	if info.Publish == nil || info.Publish.PR != "https://example.test/acme/demo/pull/7" || info.Publish.Head != sha('d') || info.Publish.Branch != "jig/JIG-2" {
		t.Fatalf("publish = %+v", info.Publish)
	}
	if got := info.Publish.Body; !strings.HasPrefix(got, "## Intent\nLoad the config") || strings.Count(got, "\n") != 6 {
		t.Errorf("publish body = %q, want its seven lines with the newlines restored", got)
	}
	if got := info.Publish.Changelog; !strings.HasSuffix(got, "- round 1: fix-slice fix-1") {
		t.Errorf("publish changelog = %q", got)
	}

	mid := parseStatus(statusJIG2())
	if len(mid.Findings) != 4 || mid.Findings[2].Title != "Close the file" || mid.Findings[2].Status != "open" || mid.Findings[0].Where != "alpha/a.go:3" {
		t.Errorf("findings = %+v", mid.Findings)
	}

	old := parseStatus(statusJIG1())
	if old.Err != nil || old.Rounds != nil || old.Findings != nil || old.Publish != nil || old.FixBudget != "" || old.Slices[0].Workspace != "" {
		t.Errorf("a status without the sections reads as %+v", old)
	}
}

// TestRoundsWithNoFindingsFileKeepTheirDash: a round with no findings.yaml prints
// "-" for its files, which is not a count of none.
func TestRoundsWithNoFindingsFileKeepTheirDash(t *testing.T) {
	t.Parallel()
	out := "ticket: JIG-9\nstate: green\n" + axi.Table("rounds", roundColumns, [][]string{
		{"1", "findings", "-", "-", "-", "-", "0", "0", "0", "-", "-", sha('5'), "-"},
		{"2", "clean", "s-rev", "-", "-", "true", "0", "0", "0", "delta", sha('5'), sha('6'), "0"},
	})
	got := parseStatus(out).Rounds
	if len(got) != 2 || got[0].Files != -1 || got[0].Reviewer != "" || got[0].Head != sha('5') || got[1].Files != 0 {
		t.Errorf("rounds = %+v", got)
	}
}

// TestParseStatusReadsTheClosureOfAClosedTicket: a ticket closed without a pull
// request says how in the closed block of its status, and an open one has none.
func TestParseStatusReadsTheClosureOfAClosedTicket(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		kv   [][2]string
		want string
	}{
		{"covered", [][2]string{{"reason", "covered"}, {"by", "develdeco/jig#82"}, {"at", "2026-10-25"}}, "covered by develdeco/jig#82"},
		{"superseded", [][2]string{{"reason", "superseded"}, {"by", "JIG-4"}}, "superseded by JIG-4"},
		{"not planned", [][2]string{{"reason", "not-planned"}, {"note", "Decided against it."}}, "not planned"},
	} {
		info := parseStatus(strings.Join([]string{"ticket: JIG-1", "state: closed", axi.KV("closed", tc.kv)}, "\n") + "\n")
		if info.Err != nil || info.State != "closed" || info.Closed != tc.want {
			t.Errorf("%s: info = %+v, want state closed, closed %q", tc.name, info, tc.want)
		}
	}
	if open := parseStatus(statusJIG1()); open.Closed != "" {
		t.Errorf("an open ticket reads as closed: %q", open.Closed)
	}
}
