package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/spend"
)

// The tests here pin a chart's rounds where they are laid into the standing:
// each ticket's reviewed rounds and return to build (chartFigures, read by the
// same recall.FactsOf `jig recall` shows in its receipts), the JSON key and the
// text line `jig next <chart>` and `jig status <chart>` print beside spend.

// TestChartRoundsAreEachTicketsReviewedRounds: a chart's rounds line has an
// entry for each ticket that took a reviewed round, in the chart's order, with
// the rounds it reviewed (a carried clean round ran no reviewer and is not one)
// and "back to build" when a slice was claimed after its first publish. A
// ticket with no round, one whose journal cannot be read and one with no
// journal have none; the JSON and the text say the same and the line follows
// spend; a chart with no round has no rounds key and no rounds line.
func TestChartRoundsAreEachTicketsReviewedRounds(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	add := func(id string, lines ...journal.Line) { r.spendOn(t, id, lines...) }
	round, clean := journal.Line{Event: journal.EventGateRound}, journal.Line{Event: journal.EventGateClean}
	carried := journal.Line{Event: journal.EventGateClean, Carried: 1}
	publish := journal.Line{Event: journal.EventPublishDone}
	claim := func(slice string) journal.Line {
		return journal.Line{Event: journal.EventClaim, Session: "s", Slice: slice}
	}
	add("SHOP-1", claim("a"), round, clean, carried, publish)
	add("SHOP-1", spend.Line("2026-01-01T10:00:00Z", "s-1", "claude", "a", spend.Totals{Input: 100}))
	add("SHOP-2", claim("a"), round, round, clean, publish, claim("fix-1"), clean, publish)
	// A journal that cannot be read: it is a directory.
	if err := os.MkdirAll(filepath.Join(r.root, "tickets", "SHOP-3", "journal.ndjson"), 0o755); err != nil {
		t.Fatal(err)
	}

	want := []nextChartRound{{Ticket: "SHOP-1", Reviewed: 2}, {Ticket: "SHOP-2", Reviewed: 4, BackToBuild: true}}
	if n := r.next(t, "checkout"); !reflect.DeepEqual(n.Rounds, want) {
		t.Errorf("rounds = %+v, want %+v (SHOP-3 unreadable and SHOP-4 with no journal have none)", n.Rounds, want)
	}

	out, code := r.run(t, "next", "checkout", "--json")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); code != 0 || err != nil {
		t.Fatalf("jig next checkout --json: exit %d, %v\n%s", code, err, out)
	}
	if got, w := strings.TrimSpace(string(raw["rounds"])), `[{"ticket":"SHOP-1","reviewed":2,"back_to_build":false},{"ticket":"SHOP-2","reviewed":4,"back_to_build":true}]`; got != w {
		t.Errorf("rounds json = %s, want %s", got, w)
	}

	line := "rounds: SHOP-1 2; SHOP-2 4 back to build"
	for _, verb := range []string{"next", "status"} {
		text, code := r.run(t, verb, "checkout")
		if code != 0 {
			t.Fatalf("jig %s checkout: exit %d\n%s", verb, code, text)
		}
		if !hasLine(text, line) {
			t.Errorf("jig %s checkout has no line %q:\n%s", verb, line, text)
		}
		if strings.Index(text, "spend:") > strings.Index(text, "rounds:") || !strings.Contains(text, "spend:") {
			t.Errorf("jig %s checkout does not put rounds after spend:\n%s", verb, text)
		}
	}

	quiet := newChartRig(t)
	quiet.shop(t, false)
	quiet.spendOn(t, "SHOP-1", claim("a"), clean, publish)
	if n := quiet.next(t, "checkout"); len(n.Rounds) != 1 {
		t.Fatalf("rounds = %+v, want the one clean round of SHOP-1", n.Rounds)
	}
	none := newChartRig(t)
	none.shop(t, false)
	none.spendOn(t, "SHOP-1", claim("a"), carried, publish)
	if n := none.next(t, "checkout"); n.Rounds != nil {
		t.Errorf("a chart with no reviewed round has rounds %+v, want none", n.Rounds)
	}
	out, _ = none.run(t, "next", "checkout", "--json")
	if strings.Contains(out, `"rounds"`) {
		t.Errorf("a chart with no reviewed round has a rounds key:\n%s", out)
	}
	for _, verb := range []string{"next", "status"} {
		if text, _ := none.run(t, verb, "checkout"); strings.Contains(text, "rounds:") {
			t.Errorf("jig %s checkout of a chart with no reviewed round has a rounds line:\n%s", verb, text)
		}
	}
}

// TestChartRoundsIgnoreATicketItCannotRead: chartFigures names no ticket whose
// journal cannot be read, one with no round, and an entry that has no ticket
// yet, and says nothing when that leaves no round.
func TestChartRoundsIgnoreATicketItCannotRead(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	for _, id := range []string{"SHOP-1", "SHOP-2", "SHOP-3"} {
		r.ticket(t, id, "", false)
	}
	r.spendOn(t, "SHOP-1", journal.Line{Event: journal.EventGateRound})
	r.spendOn(t, "SHOP-3", journal.Line{Event: journal.EventClaim, Session: "s", Slice: "a"})
	if err := os.MkdirAll(filepath.Join(r.root, "tickets", "SHOP-2", "journal.ndjson"), 0o755); err != nil {
		t.Fatal(err)
	}
	rows := func(ids ...string) phase.Chart {
		var c phase.Chart
		for i, id := range ids {
			c.Rows = append(c.Rows, phase.ChartRow{Pos: i + 1, ID: id})
		}
		return c
	}

	_, got := chartFigures(r.st, rows("SHOP-1", "SHOP-2", "SHOP-3", ""))
	if want := []nextChartRound{{Ticket: "SHOP-1", Reviewed: 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("rounds = %+v, want %+v", got, want)
	}
	if _, got := chartFigures(r.st, rows("SHOP-2", "SHOP-3", "")); got != nil {
		t.Errorf("rounds = %+v, want none when no readable ticket took a reviewed round", got)
	}
	if _, got := chartFigures(r.st, phase.Chart{}); got != nil {
		t.Errorf("rounds = %+v, want none for an empty chart", got)
	}
}

// TestChartRoundsLine: the words of the rounds line. A ticket's id and its
// reviewed rounds, then "back to build" when it went back, the entries told
// apart by semicolons so the line is not quoted, with no total.
func TestChartRoundsLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		in   []nextChartRound
		want string
	}{
		{"one ticket", []nextChartRound{{Ticket: "A-1", Reviewed: 1}}, "A-1 1"},
		{"back to build", []nextChartRound{{Ticket: "A-3", Reviewed: 3, BackToBuild: true}}, "A-3 3 back to build"},
		{"several in order", []nextChartRound{{Ticket: "A-1", Reviewed: 2}, {Ticket: "A-2", Reviewed: 1}, {Ticket: "A-3", Reviewed: 3, BackToBuild: true}}, "A-1 2; A-2 1; A-3 3 back to build"},
	} {
		if got := roundsLine(c.in); got != c.want {
			t.Errorf("%s: line = %q, want %q", c.name, got, c.want)
		}
	}
}
