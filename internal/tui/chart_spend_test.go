package tui

import (
	"reflect"
	"strings"
	"testing"
)

// spendObject is the spend object `jig next checkout --json` prints when the
// chart's tickets have spend lines.
func spendObject() map[string]any {
	return map[string]any{
		"tickets": 3, "agents": 2, "tokens": 31_200_000, "input": 1_200_000, "output": 300_000,
		"cache_read": 29_000_000, "cache_write": 700_000, "usd": 12.34,
	}
}

// chartWithSpendRig is the chart screen of the fixtures whose verb says spend.
func chartWithSpendRig(t *testing.T, spend map[string]any) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(func(v map[string]any) {
		if spend != nil {
			v["spend"] = spend
		}
	}), status)
	g.feed(lines...)
	g.press("end", "enter")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want chart:\n%s", g.m.Screen(), g.view())
	}
	return g
}

// TestChartScreenShowsItsSpend: the Run panel ends with a row that says what the
// chart's run has cost, from the spend object of the verb; a chart whose verb
// says none draws what it drew before (the golden of TestChartScreen).
func TestChartScreenShowsItsSpend(t *testing.T) {
	t.Parallel()
	v := chartWithSpendRig(t, spendObject()).view()
	if want := "spend    31.2M tokens · $12.34 · 3 tickets · 2 subagents"; !strings.Contains(v, want) {
		t.Errorf("the chart screen has no row %q:\n%s", want, v)
	}
	golden(t, "chart-spend", v)

	if plain := chartWithSpendRig(t, nil).view(); strings.Contains(plain, "spend") || plain != chartScreenRig(t).view() {
		t.Errorf("a chart with no spend draws a spend row or differs from the chart screen:\n%s", plain)
	}
}

// TestChartSpendWords: the parts of the row, joined by a dot. Dollars show when
// above 0, subagents when there are any, "1 ticket" and "1 subagent" are
// singular and the models with no price end it.
func TestChartSpendWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   chartSpend
		want string
	}{
		{"all of it", chartSpend{Tickets: 3, Agents: 2, Tokens: 31_200_000, USD: 12.34}, "31.2M tokens · $12.34 · 3 tickets · 2 subagents"},
		{"singular", chartSpend{Tickets: 1, Agents: 1, Tokens: 25_400, USD: 0.5}, "25k tokens · $0.50 · 1 ticket · 1 subagent"},
		{"no dollars and no subagents", chartSpend{Tickets: 2, Tokens: 980}, "980 tokens · 2 tickets"},
		{"under half a cent", chartSpend{Tickets: 2, Agents: 1, Tokens: 1_300, USD: 0.0039}, "1k tokens · <$0.01 · 2 tickets · 1 subagent"},
		{"models with no price", chartSpend{Tickets: 1, Tokens: 4_000, Unpriced: []string{"a", "b"}}, "4k tokens · 1 ticket · no price for a and b"},
		{"all and unpriced", chartSpend{Tickets: 2, Agents: 1, Tokens: 2_000_000, USD: 1, Unpriced: []string{"m"}}, "2.0M tokens · $1.00 · 2 tickets · 1 subagent · no price for m"},
	} {
		if got := spendWords(&tc.in); got != tc.want {
			t.Errorf("%s: spendWords = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestParseChartReadsSpend: the spend object of the verb is read, and a verb
// that prints none (an older jig, a chart nothing was spent on) leaves it nil.
func TestParseChartReadsSpend(t *testing.T) {
	t.Parallel()
	with, err := parseChart(checkoutJSON(func(v map[string]any) {
		s := spendObject()
		s["unpriced"] = []string{"m1"}
		v["spend"] = s
	}))
	if err != nil || with == nil {
		t.Fatalf("info, err = %v, %v", with, err)
	}
	if want := (&chartSpend{Tickets: 3, Agents: 2, Tokens: 31_200_000, USD: 12.34, Unpriced: []string{"m1"}}); !reflect.DeepEqual(with.Spend, want) {
		t.Errorf("spend = %+v, want %+v", with.Spend, want)
	}
	without, err := parseChart(checkoutJSON())
	if err != nil || without == nil {
		t.Fatalf("info, err = %v, %v", without, err)
	}
	if without.Spend != nil {
		t.Errorf("spend = %+v, want nil when the verb prints none", without.Spend)
	}
}
