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

// The tests here pin a chart's spend where it is laid into the standing: the
// fold over the tickets' latest lines (chartSpend), the JSON key and the text
// line `jig next <chart>` and `jig status <chart>` print. The lines of each
// ticket are made by spend.Line and spend.AgentLine, the same the note source
// writes; the end-to-end chain with the real hooks is e2e's
// TestChartSpendIsTheSumOfItsTickets.

// spendOn appends spend lines to ticket id's journal.
func (r chartRig) spendOn(t *testing.T, id string, lines ...journal.Line) {
	t.Helper()
	for _, l := range lines {
		if err := journal.Append(r.st, id, l); err != nil {
			t.Fatal(err)
		}
	}
}

// hasLine reports whether out has a line that is exactly want, indented or not.
func hasLine(out, want string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

// TestChartSpendFoldsEachTicketsLatestLines: a chart's spend is each ticket's
// latest line of every session and agent, added up. A session that wrote lines
// on two tickets counts on each, a key's older lines are ignored, a ticket whose
// journal cannot be read adds nothing, the JSON and the text say the same, and a
// chart with no spend has no spend key and no spend line.
func TestChartSpendFoldsEachTicketsLatestLines(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	m1 := spend.Totals{Input: 300, Output: 100, CacheRead: 2000, CacheWrite: 400, USD: 2.25, Models: []string{"m1"}}
	r.spendOn(t, "SHOP-1",
		spend.Line("2026-01-01T10:00:00Z", "s-1", "claude", "a", spend.Totals{Input: 100, USD: 99, Models: []string{"m1"}}),
		spend.Line("2026-01-01T11:00:00Z", "s-1", "claude", "a", m1),
		spend.AgentLine("2026-01-01T11:30:00Z", "s-1", "a-1", "claude", "a", spend.Totals{Input: 50, USD: 0.25, Models: []string{"m1"}}),
	)
	// The same session spent on a second ticket, and another session on it too.
	r.spendOn(t, "SHOP-2",
		spend.Line("2026-01-01T12:00:00Z", "s-1", "claude", "a", spend.Totals{Input: 1000, USD: 0.5, Models: []string{"m1"}}),
		spend.Line("2026-01-01T12:30:00Z", "s-2", "claude", "a", spend.Totals{Input: 150, Models: []string{"m2"}, Unpriced: []string{"m2"}}),
	)
	// A journal that cannot be read: it is a directory.
	if err := os.MkdirAll(filepath.Join(r.root, "tickets", "SHOP-3", "journal.ndjson"), 0o755); err != nil {
		t.Fatal(err)
	}

	n := r.next(t, "checkout")
	want := &nextChartSpend{
		Tickets: 2, Agents: 1, Tokens: 4000, Input: 1500, Output: 100, CacheRead: 2000, CacheWrite: 400, USD: 3, Unpriced: []string{"m2"},
	}
	if !reflect.DeepEqual(n.Spend, want) {
		t.Errorf("spend = %+v, want %+v (the newest line of each key, per ticket; SHOP-3 unreadable and SHOP-4 with none add nothing)", n.Spend, want)
	}

	out, code := r.run(t, "next", "checkout", "--json")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); code != 0 || err != nil {
		t.Fatalf("jig next checkout --json: exit %d, %v\n%s", code, err, out)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw["spend"], &keys); err != nil {
		t.Fatalf("spend key: %v\n%s", err, out)
	}
	for _, k := range []string{"tickets", "agents", "tokens", "input", "output", "cache_read", "cache_write", "usd", "unpriced"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("the spend object has no %q: %v", k, keys)
		}
	}

	line := "spend: 4k tokens ($3.00) over 2 tickets with 1 subagent; no price for m2"
	for _, verb := range []string{"next", "status"} {
		text, code := r.run(t, verb, "checkout")
		if code != 0 {
			t.Fatalf("jig %s checkout: exit %d\n%s", verb, code, text)
		}
		if !hasLine(text, line) {
			t.Errorf("jig %s checkout has no line %q:\n%s", verb, line, text)
		}
		if strings.Index(text, "ungraduated:") > strings.Index(text, "spend:") {
			t.Errorf("jig %s checkout puts spend before ungraduated:\n%s", verb, text)
		}
	}

	quiet := newChartRig(t)
	quiet.shop(t, false)
	if n := quiet.next(t, "checkout"); n.Spend != nil {
		t.Errorf("a chart with no spend has spend %+v, want none", n.Spend)
	}
	out, _ = quiet.run(t, "next", "checkout", "--json")
	if strings.Contains(out, `"spend"`) {
		t.Errorf("a chart with no spend has a spend key:\n%s", out)
	}
	for _, verb := range []string{"next", "status"} {
		if text, _ := quiet.run(t, verb, "checkout"); strings.Contains(text, "spend:") {
			t.Errorf("jig %s checkout of a chart with no spend has a spend line:\n%s", verb, text)
		}
	}
}

// TestChartSpendIgnoresATicketItCannotRead: chartSpend skips a ticket whose
// journal cannot be read, one with no sessions, and an entry that has no ticket
// yet, and says nothing when that leaves no spend.
func TestChartSpendIgnoresATicketItCannotRead(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", false)
	r.ticket(t, "SHOP-2", "", false)
	r.spendOn(t, "SHOP-1", spend.Line("2026-01-01T10:00:00Z", "s-1", "claude", "a", spend.Totals{Input: 10}))
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

	got := chartSpend(r.st, rows("SHOP-1", "SHOP-2", ""))
	if got == nil || got.Tickets != 1 || got.Tokens != 10 {
		t.Errorf("spend = %+v, want the one readable ticket's 10 tokens", got)
	}
	if got := chartSpend(r.st, rows("SHOP-2", "")); got != nil {
		t.Errorf("spend = %+v, want none when the only ticket with a journal cannot be read", got)
	}
	if got := chartSpend(r.st, phase.Chart{}); got != nil {
		t.Errorf("spend = %+v, want none for an empty chart", got)
	}
}

// TestChartSpendLine: the words of the spend line. The dollars show when above
// 0, the subagents when there are any, "1 ticket" and "1 subagent" are
// singular, and the models with no price end it.
func TestChartSpendLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		in   nextChartSpend
		want string
	}{
		{"all of it", nextChartSpend{Tickets: 3, Agents: 2, Tokens: 31_200_000, USD: 12.34}, "31.2M tokens ($12.34) over 3 tickets with 2 subagents"},
		{"singular", nextChartSpend{Tickets: 1, Agents: 1, Tokens: 1500, USD: 0.5}, "1.5k tokens ($0.50) over 1 ticket with 1 subagent"},
		{"no dollars and no subagents", nextChartSpend{Tickets: 2, Tokens: 900}, "900 tokens over 2 tickets"},
		{"one model with no price", nextChartSpend{Tickets: 1, Tokens: 10, Unpriced: []string{"m1"}}, "10 tokens over 1 ticket; no price for m1"},
		{"two models with no price", nextChartSpend{Tickets: 2, Agents: 3, Tokens: 2_000_000, USD: 1, Unpriced: []string{"a", "b"}}, "2M tokens ($1.00) over 2 tickets with 3 subagents; no price for a and b"},
	} {
		if got := c.in.line(); got != c.want {
			t.Errorf("%s: line = %q, want %q", c.name, got, c.want)
		}
	}
}
