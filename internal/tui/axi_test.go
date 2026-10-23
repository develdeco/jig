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
	if len(info.Slices) != 1 || info.Slices[0] != (statusSlice{ID: "a", State: "green", Attempts: 1}) {
		t.Errorf("slices = %+v, want slice a green after 1 try, with no blocker or question", info.Slices)
	}
	if len(info.Asks) != 2 || info.Asks[0] != (statusAsk{ID: "r1-f1", Risk: "high", Where: "alpha/a.go:3", Title: "Unchecked error", Why: "needs a human"}) {
		t.Errorf("asks = %+v", info.Asks)
	}
	if len(info.Questions) != 0 || len(info.Help) != 1 {
		t.Errorf("questions = %+v, help = %q", info.Questions, info.Help)
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
