package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
)

// The chart fixtures: a chart "checkout" of five tickets in the store of project
// demo. JIG-4 is published (its pull request is open, not merged), JIG-5 is being
// built, JIG-6 is stacked on JIG-5, JIG-7 waits for JIG-4 to merge, JIG-8 can
// start; one more entry has no ticket yet.

// asTicket is lines relabelled as another ticket's.
func asTicket(envs []events.Envelope, to string) []events.Envelope {
	out := make([]events.Envelope, len(envs))
	for i, e := range envs {
		e.Ticket = to
		out[i] = e
	}
	return out
}

// relabelled is text written for one ticket id, written for another.
func relabelled(text, from, to string) string { return strings.ReplaceAll(text, from, to) }

// untouchedStatus is `jig status` of a ticket nobody has started.
func untouchedStatus(id string) string { return relabelled(draftStatus(nil), "SHOP-1", id) }

// chartTickets are the lines and the statuses of the chart's tickets.
func chartTickets() (lines []events.Envelope, status map[string]string) {
	building, buildLines := bigBuild(3, 1)
	lines = append(lines, asTicket(jig2(), "JIG-4")...)
	lines = append(lines, asTicket(buildLines, "JIG-5")...)
	status = map[string]string{
		"JIG-4": relabelled(statusJIG2Done(), "JIG-2", "JIG-4"),
		"JIG-5": relabelled(building, "BIG-1", "JIG-5"),
		"JIG-6": untouchedStatus("JIG-6"),
		"JIG-7": untouchedStatus("JIG-7"),
		"JIG-8": untouchedStatus("JIG-8"),
	}
	return lines, status
}

// pull is a pull request of the chart's repo.
func pull(n, state string) map[string]any {
	return map[string]any{"repo": "shop", "url": "https://example.test/acme/shop/pull/" + n, "state": state}
}

// need is an unmet need of a ticket in the verb's JSON.
func need(ticket, kind, fact string, prs ...map[string]any) map[string]any {
	return map[string]any{"ticket": ticket, "kind": kind, "fact": fact, "prs": prs}
}

// entry is a chart entry in the verb's JSON; steps are the four phases' states.
func entry(id, title, phase string, steps []string, ready bool, waits ...map[string]any) map[string]any {
	e := map[string]any{"id": id, "title": title, "phase": phase, "ready": ready, "reach": "here"}
	if steps != nil {
		e["steps"] = steps
	}
	if len(waits) > 0 {
		e["waits_for"] = waits
	}
	return e
}

// checkoutMap is the chart's map.md as the verb sections it.
func checkoutMap() map[string]any {
	return map[string]any{
		"destination":  []string{"A customer takes a coupon code from the cart to a discounted order."},
		"decided":      []string{"Codes are single use", "Discounts apply before tax"},
		"fog":          []string{"Stacking two codes", "Codes in other currencies"},
		"out_of_scope": []string{"Gift cards"},
	}
}

// checkoutJSON is `jig next checkout --json`: a run in progress that stops at JIG-7.
func checkoutJSON(mod ...func(map[string]any)) string {
	done := []string{"done", "done", "done", "done"}
	fresh := []string{"current", "pending", "pending", "pending"}
	building := []string{"done", "current", "pending", "pending"}
	v := map[string]any{
		"chart": "checkout", "title": "Coupons at checkout", "ready": true, "code": "", "reason": "",
		"prompt": "Continue checkout", "started": true, "ungraduated": 1,
		"briefs": []string{},
		"run":    [][]string{{"JIG-8"}, {"JIG-6"}},
		"tickets": []map[string]any{
			entry("JIG-4", "Coupon model", "publish", done, false),
			entry("JIG-5", "Apply a coupon code", "build", building, false),
			entry("JIG-8", "Show the discount", "brief", fresh, true),
			entry("JIG-6", "Remove a coupon", "brief", fresh, true, need("JIG-5", "stacked", "its gate has no clean round")),
			entry("JIG-7", "Coupons in receipts", "brief", fresh, true, need("JIG-4", "merged", "its PR is open", pull("7", "OPEN"))),
			{"id": "", "title": "Stack two coupons", "reach": "blocked"},
		},
		"ends": []map[string]any{{"ticket": "JIG-7", "blocker": "JIG-4", "kind": "merged", "fact": "its PR is open", "prs": []map[string]any{pull("7", "OPEN")}}},
		"map":  checkoutMap(),
	}
	for _, f := range mod {
		f(v)
	}
	return jsonLine(v)
}

// jsonLine is v as the one line of JSON a verb prints.
func jsonLine(v any) string {
	data, _ := json.Marshal(v)
	return string(data) + "\n"
}

// chartRoster is `jig next --json` of the store: the five tickets of the chart,
// with the blockers that are not met, and the chart.
func chartRoster(charts ...map[string]any) string {
	blocked := func(b ...map[string]any) []map[string]any { return b }
	v := map[string]any{
		"tickets": []map[string]any{
			{"id": "JIG-4", "title": "Coupon model", "state": "done"},
			{"id": "JIG-5", "title": "Apply a coupon code", "state": "active"},
			{"id": "JIG-6", "title": "Remove a coupon", "state": "blocked", "blocked_by": blocked(map[string]any{"ticket": "JIG-5", "kind": "stacked", "fact": "its gate has no clean round"})},
			{"id": "JIG-7", "title": "Coupons in receipts", "state": "blocked", "blocked_by": blocked(map[string]any{"ticket": "JIG-4", "kind": "merged", "fact": "its PR is open"})},
			{"id": "JIG-8", "title": "Show the discount", "state": "ready"},
		},
	}
	if len(charts) > 0 {
		v["charts"] = charts
	}
	return jsonLine(v)
}

// checkoutListing is the chart as the store listing names it.
func checkoutListing() map[string]any {
	return map[string]any{"name": "checkout", "title": "Coupons at checkout", "tickets": []string{"JIG-4", "JIG-5", "JIG-6", "JIG-7", "JIG-8"}, "ungraduated": 1}
}

// chartRig is the project of the fixtures on the main screen, the stream read.
func chartRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(), status)
	return g.feed(lines...)
}

// answerChart sets what the store listing, a chart's verb and the tickets'
// statuses print.
func answerChart(g *rig, roster, chart, verb string, status map[string]string) {
	g.r.answer("next --json --store /s", roster, 0)
	g.r.answer("next "+chart+" --json --store /s", verb, 0)
	for id, out := range status {
		g.r.answer("status "+id+" --store /s", out, 0)
	}
}

// ran is how many times the command line ran.
func (f *fakeRunner) ran(line string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Line == line {
			n++
		}
	}
	return n
}

// chartScreenRig is chartRig with the chart screen open.
func chartScreenRig(t *testing.T) *rig {
	t.Helper()
	g := chartRig(t)
	g.press("end", "enter")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want chart:\n%s", g.m.Screen(), g.view())
	}
	return g
}
