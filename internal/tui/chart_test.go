package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

func TestMainScreenWithCharts(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	golden(t, "main-charts", g.view())
}

func TestChartScreen(t *testing.T) {
	t.Parallel()
	golden(t, "chart", chartScreenRig(t).view())
}

func TestChartScreenOfATallTerminal(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.press("end", "enter")
	golden(t, "chart-tall", g.view())
}

// shopRig is a chart "shop" whose run ends at a merge: JIG-4 is published and
// its pull request is open, JIG-7 waits for it.
func shopRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	roster := jsonLine(map[string]any{
		"tickets": []map[string]any{
			{"id": "JIG-4", "title": "Coupon model", "state": "done"},
			{"id": "JIG-7", "title": "Coupons in receipts", "state": "blocked", "blocked_by": []map[string]any{{"ticket": "JIG-4", "kind": "merged", "fact": "its PR is open"}}},
		},
		"charts": []map[string]any{{"name": "shop", "title": "Shop", "tickets": []string{"JIG-4", "JIG-7"}, "ungraduated": 0}},
	})
	verb := jsonLine(map[string]any{
		"chart": "shop", "title": "Shop", "ready": false, "code": "CHART_WAITS_MERGE",
		"reason": "JIG-7 waits for JIG-4 to merge (https://example.test/acme/shop/pull/7 OPEN)", "prompt": "Continue shop",
		"started": true, "ungraduated": 0, "briefs": []string{}, "run": [][]string{},
		"tickets": []map[string]any{
			entry("JIG-4", "Coupon model", "publish", []string{"done", "done", "done", "done"}, false),
			entry("JIG-7", "Coupons in receipts", "brief", []string{"current", "pending", "pending", "pending"}, true, need("JIG-4", "merged", "its PR is open", pull("7", "OPEN"))),
		},
		"ends": []map[string]any{{"ticket": "JIG-7", "blocker": "JIG-4", "kind": "merged", "fact": "its PR is open", "prs": []map[string]any{pull("7", "OPEN")}}},
		"map":  map[string]any{"destination": []string{"Receipts show the coupon."}, "decided": []string{"One line per coupon"}},
	})
	answerChart(g, roster, "shop", verb, map[string]string{"JIG-4": status["JIG-4"], "JIG-7": status["JIG-7"]})
	g.feed(lines[:len(asTicket(jig2(), "JIG-4"))]...)
	g.press("end", "enter")
	return g
}

func TestChartScreenWhoseRunWaitsForAMerge(t *testing.T) {
	t.Parallel()
	g := shopRig(t)
	if v := g.view(); !strings.Contains(v, "waits for a merge") && !strings.Contains(v, "in progress") {
		t.Fatalf("the header says nothing of the run:\n%s", v)
	}
	golden(t, "chart-waits-merge", g.view())
}

// TestChartScreenOfAChartNothingIsGraduatedOf: the entries are fog, so the
// screen lists them as such, with no ticket to stand on and a run that says why
// it cannot start.
func TestChartScreenOfAChartNothingIsGraduatedOf(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	roster := jsonLine(map[string]any{
		"tickets": []map[string]any{},
		"charts":  []map[string]any{{"name": "ideas", "title": "Ideas", "tickets": []string{}, "ungraduated": 2}},
	})
	verb := jsonLine(map[string]any{
		"chart": "ideas", "title": "Ideas", "ready": false, "code": "CHART_NOT_GRADUATED",
		"reason": "run `jig graduate ideas`", "prompt": "", "started": false, "ungraduated": 2,
		"tickets": []map[string]any{{"id": "", "title": "Gift cards"}, {"id": "", "title": "Loyalty points"}},
		"map":     checkoutMap(),
	})
	answerChart(g, roster, "ideas", verb, nil)
	g.feed().press("enter")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want chart:\n%s", g.m.Screen(), g.view())
	}
	golden(t, "chart-fog-only", g.view())
}

func TestChartRowOpensTheChartScreenAndEscReturns(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	g.press("end", "enter")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want chart", g.m.Screen())
	}
	g.press("esc")
	if g.m.Screen() != "main" {
		t.Errorf("on %s after esc, want main", g.m.Screen())
	}
}

// TestTheChartCursorStartsOnTheFirstTicketNotPublishedAndATicketOpensAndReturns:
// opening a ticket from the chart is one step in, esc one step back to the chart
// with the cursor kept.
func TestTheChartCursorStartsOnTheFirstTicketNotPublishedAndATicketOpensAndReturns(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	if v := g.view(); !strings.Contains(v, "▸ ◑ JIG-5") {
		t.Fatalf("the cursor is not on JIG-5, the first ticket not published:\n%s", v)
	}
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "JIG-5" {
		t.Fatalf("on %s of %q, want the ticket JIG-5", g.m.Screen(), g.m.top().ticket)
	}
	g.press("esc")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s after esc, want chart", g.m.Screen())
	}
	if v := g.view(); !strings.Contains(v, "▸ ◑ JIG-5") {
		t.Errorf("the cursor did not stay on JIG-5:\n%s", v)
	}
}

// TestKeysOnTheChartActOnTheTicketUnderTheCursor: g and p are about the ticket
// the cursor is on, as on the project's list, not about the chart.
func TestKeysOnTheChartActOnTheTicketUnderTheCursor(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.press("down", "g")
	if got := g.r.lines(); !reflect.DeepEqual(got, []string{"gate JIG-8 --store /s"}) {
		t.Errorf("g ran %v, want the gate of JIG-8", got)
	}
	g.press("p")
	if v := g.view(); !strings.Contains(v, "publish JIG-8: approve the reviewed head") {
		t.Errorf("p did not ask to publish JIG-8:\n%s", v)
	}
	g.press("n")
	g.press("home", "p")
	if v := g.view(); !strings.Contains(v, "publish JIG-4: approve the reviewed head") {
		t.Errorf("home then p did not ask to publish the first ticket:\n%s", v)
	}
}

func TestMainScreenWithoutChartsDrawsNoChartAndReadsNone(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	v := g.view()
	if strings.Contains(v, "chart") {
		t.Errorf("a store with no charts draws one:\n%s", v)
	}
	g.press("end", "enter")
	if g.m.Screen() != "ticket" {
		t.Errorf("enter on the last row opened %s, want a ticket", g.m.Screen())
	}
}

// TestAChartVerbThatPrintsNoChartLeavesTheScreenWithTheListingAndNoNotice: an
// older jig, a refusal or text is no chart, and the screen says nothing of it.
func TestAChartVerbThatPrintsNoChartLeavesTheScreenWithTheListingAndNoNotice(t *testing.T) {
	t.Parallel()
	for _, out := range []string{"", "error: unknown verb\ncode: ERROR\n", "tickets: none\n", "{not json\n", `{"ticket":"checkout"}` + "\n"} {
		g := newRig(t, "")
		lines, status := chartTickets()
		answerChart(g, chartRoster(checkoutListing()), "checkout", out, status)
		g.feed(lines...).press("end", "enter")
		v := g.view()
		if g.m.notice != "" {
			t.Errorf("a verb that printed %q left the notice %q", out, g.m.notice)
		}
		for _, want := range []string{"▸ ◑ JIG-5", "stacked on JIG-5", "waits for JIG-4 merged", "1 entry not graduated"} {
			if !strings.Contains(v, want) {
				t.Errorf("with the chart verb printing %q the screen lacks %q:\n%s", out, want, v)
			}
		}
		if strings.Contains(v, "Run") {
			t.Errorf("a Run panel is drawn with no verdict to say:\n%s", v)
		}
	}
}

func TestAChartOfTheWrongShapeIsReadAgainAndThenSaid(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	lines, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", `{"chart":"checkout","tickets":"none"}`+"\n", status)
	g.feed(lines...).press("end", "enter")
	if !strings.HasPrefix(g.m.notice, "chart checkout: ") {
		t.Errorf("notice = %q, want the chart's read said", g.m.notice)
	}
	if n := g.r.ran("next checkout --json --store /s"); n != readRetries {
		t.Errorf("the chart was read %d times, want %d", n, readRetries)
	}
}

func TestTheChartScreenReadsItsChartOnOpenAndOnRefreshAndNotFromTheListScreen(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	read := "next checkout --json --store /s"
	if n := g.r.ran(read); n != 0 {
		t.Fatalf("the project's list read the chart %d times", n)
	}
	g.press("end", "enter")
	if n := g.r.ran(read); n != 1 {
		t.Fatalf("opening the chart read it %d times, want 1", n)
	}
	g.press("r")
	if n := g.r.ran(read); n != 2 {
		t.Errorf("r read the chart %d times in all, want 2", n)
	}
	if n := g.r.statusReads("JIG-7"); n != 2 {
		t.Errorf("JIG-7's status was read %d times, want once on open and once on r", n)
	}
}

// chartQuietRig is the chart screen with a clock the test moves.
func chartQuietRig(t *testing.T) (g *rig, advance func(time.Duration)) {
	t.Helper()
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	g = chartRig(t)
	g.m.now = func() time.Time { return now }
	g.press("end", "enter")
	return g, func(d time.Duration) { now = now.Add(d) }
}

// TestTheChartIsReadAgainWhenItsTicketsWereTouchedAndTheScreenWentQuiet: a merge
// on the host writes no journal line, so what a ticket waits to be merged is
// seen only by reading the verb; the screen reads it, and the ticket list that
// carries the blockers, once an event of one of its tickets has been followed by
// a quiet of three seconds, and not again until another one.
func TestTheChartIsReadAgainWhenItsTicketsWereTouchedAndTheScreenWentQuiet(t *testing.T) {
	t.Parallel()
	g, advance := chartQuietRig(t)
	chart, roster := "next checkout --json --store /s", "next --json --store /s"
	reads := func() (int, int) { return g.r.ran(chart), g.r.ran(roster) }
	c0, r0 := reads()

	advance(time.Minute)
	g.send(idleMsg{})
	if c, r := reads(); c != c0 || r != r0 {
		t.Fatalf("a quiet chart nothing happened to was read again: chart %d to %d, list %d to %d", c0, c, r0, r)
	}

	g.feed(jl(at(20, 0), "JIG-5", "s2", journal.EventOracle, with("", sha('2'), "pass", "", "", "")))
	advance(2 * time.Second)
	g.send(idleMsg{})
	if c, r := reads(); c != c0 || r != r0 {
		t.Fatalf("2s after an event the chart was read again")
	}
	advance(1500 * time.Millisecond)
	g.send(idleMsg{})
	if c, r := reads(); c != c0+1 || r != r0+1 {
		t.Fatalf("3.5s after an event the chart was read %d more times and the list %d, want 1 and 1", c-c0, r-r0)
	}
	advance(time.Minute)
	g.send(idleMsg{})
	if c, r := reads(); c != c0+1 || r != r0+1 {
		t.Errorf("a chart read after the event was read again with nothing new")
	}

	// A line of a ticket that is not the chart's is no reason.
	g.feed(jl(at(21, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	advance(time.Minute)
	g.send(idleMsg{})
	if c, _ := reads(); c != c0+1 {
		t.Errorf("an event of a ticket of no chart made the chart read again")
	}
}

// TestAKeyThatRanOnAChartsTicketMarksTheChartToBeRead: a gate or a publish from
// the chart screen changes what the chart's tickets wait for.
func TestAKeyThatRanOnAChartsTicketMarksTheChartToBeRead(t *testing.T) {
	t.Parallel()
	g, advance := chartQuietRig(t)
	before := g.r.ran("next checkout --json --store /s")
	g.press("down", "g")
	advance(10 * time.Second)
	g.send(idleMsg{})
	if n := g.r.ran("next checkout --json --store /s"); n != before+1 {
		t.Errorf("the chart was read %d more times after a gate and a quiet, want 1", n-before)
	}
}

// TestTicketsGraduatedIntoTheOpenChartAreReadAtOnce: jig graduate mints tickets
// the stream names for the first time; the list names them in the chart, and the
// chart screen reads the chart and their statuses without waiting for a quiet.
func TestTicketsGraduatedIntoTheOpenChartAreReadAtOnce(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	chart := "next checkout --json --store /s"
	before := g.r.ran(chart)
	listing := checkoutListing()
	listing["tickets"] = []string{"JIG-4", "JIG-5", "JIG-6", "JIG-7", "JIG-8", "JIG-9"}
	g.r.answer("next --json --store /s", chartRoster(listing), 0)
	g.r.answer("status JIG-9 --store /s", untouchedStatus("JIG-9"), 0)
	g.feed(jl(at(25, 0), "JIG-9", "", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	if n := g.r.ran(chart); n != before+1 {
		t.Errorf("the chart was read %d more times after a ticket was graduated into it, want 1", n-before)
	}
	if n := g.r.statusReads("JIG-9"); n == 0 {
		t.Errorf("the status of the new ticket was not read")
	}
	if !g.m.w.charts["checkout"].has("JIG-9") {
		t.Errorf("the chart does not hold JIG-9")
	}
}

// TestAChartScreenOpenedOnTheCommandLineWaitsForTheListing: jig tui <chart> opens
// on the chart one step from the list, reads it once the list names it, and puts
// the cursor where the open chart does.
func TestAChartScreenOpenedOnTheCommandLineWaitsForTheListing(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	g.m = New(Config{RetryDelay: time.Millisecond, IdleTick: -1, Run: g.r.run, Chart: "checkout", Scope: []string{"--store", "/s"}, Project: "demo", Zone: time.UTC, Width: 80, Height: 24,
		Now: func() time.Time { return rigNow }, Home: rigHome})
	lines, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(), status)
	if v := g.m.View(); !strings.Contains(v, "no chart checkout") {
		t.Fatalf("before the listing the screen is not waiting:\n%s", v)
	}
	g.feed(lines...)
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want chart", g.m.Screen())
	}
	if v := g.view(); !strings.Contains(v, "▸ ◑ JIG-5") || !strings.Contains(v, "say \"Continue checkout\"") {
		t.Errorf("the chart opened from the command line is not read and placed:\n%s", v)
	}
	g.press("esc")
	if g.m.Screen() != "main" {
		t.Errorf("esc went to %s, want main", g.m.Screen())
	}
}

func TestParseChart(t *testing.T) {
	t.Parallel()
	info, err := parseChart("a checkpoint note\n" + checkoutJSON())
	if err != nil || info == nil {
		t.Fatalf("info, err = %v, %v", info, err)
	}
	if info.Name != "checkout" || info.Title != "Coupons at checkout" || !info.Ready || !info.Started || info.Prompt != "Continue checkout" || info.Ungraduated != 1 {
		t.Errorf("the verdict = %+v", info)
	}
	if want := [][]string{{"JIG-8"}, {"JIG-6"}}; !reflect.DeepEqual(info.Run, want) {
		t.Errorf("run = %v, want %v", info.Run, want)
	}
	if len(info.Tickets) != 6 || info.Tickets[5].ID != "" || info.Tickets[5].Title != "Stack two coupons" {
		t.Errorf("the entry with no ticket is not kept: %+v", info.Tickets)
	}
	seven := info.Tickets[4]
	if seven.ID != "JIG-7" || seven.Phase != "brief" || len(seven.Steps) != 4 || !seven.Ready || len(seven.Waits) != 1 || seven.Waits[0].Ticket != "JIG-4" {
		t.Errorf("JIG-7 = %+v", seven)
	}
	if w := seven.Waits[0]; w.Kind != "merged" || len(w.PRs) != 1 || w.PRs[0] != (chartPR{Repo: "shop", URL: "https://example.test/acme/shop/pull/7", State: "OPEN"}) {
		t.Errorf("JIG-7's need = %+v", w)
	}
	if len(info.Ends) != 1 || info.Ends[0].Ticket != "JIG-7" || info.Ends[0].Blocker != "JIG-4" {
		t.Errorf("ends = %+v", info.Ends)
	}
	if info.Map == nil || len(info.Map.Decided) != 2 || len(info.Map.OutOfScope) != 1 || len(info.Map.Fog) != 2 || len(info.Map.Destination) != 1 {
		t.Errorf("map = %+v", info.Map)
	}

	bare, err := parseChart(checkoutJSON(func(v map[string]any) { delete(v, "map") }))
	if err != nil || bare == nil || bare.Map != nil {
		t.Errorf("a chart with no map.md: %+v, %v", bare, err)
	}
	for _, out := range []string{"", "\n", "error: x\ncode: ERROR\n", "tickets: none\n", "{not json\n", `{"ticket":"JIG-1"}` + "\n"} {
		if info, err := parseChart(out); info != nil || err != nil {
			t.Errorf("parseChart(%q) = %v, %v, want no chart and no error", out, info, err)
		}
	}
	if _, err := parseChart(`{"chart":"checkout","tickets":3}` + "\n"); err == nil {
		t.Errorf("a chart of the wrong shape read without error")
	}
}

func TestParseRosterKeepsTheBlockersAndTheCharts(t *testing.T) {
	t.Parallel()
	rows, charts, err := parseRoster(chartRoster(checkoutListing()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 || len(rows[0].Blockers) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
	if want := []chartWait{{Ticket: "JIG-5", Kind: "stacked", Fact: "its gate has no clean round"}}; !reflect.DeepEqual(rows[2].Blockers, want) {
		t.Errorf("JIG-6's blockers = %+v, want %+v", rows[2].Blockers, want)
	}
	if want := []chartWait{{Ticket: "JIG-4", Kind: "merged", Fact: "its PR is open"}}; !reflect.DeepEqual(rows[3].Blockers, want) {
		t.Errorf("JIG-7's blockers = %+v, want %+v", rows[3].Blockers, want)
	}
	want := []chartListing{{Name: "checkout", Title: "Coupons at checkout", Tickets: []string{"JIG-4", "JIG-5", "JIG-6", "JIG-7", "JIG-8"}, Ungraduated: 1}}
	if !reflect.DeepEqual(charts, want) {
		t.Errorf("charts = %+v, want %+v", charts, want)
	}
}

// TestTheChartRowsAreWhatTheTicketsListSaysUntilTheVerbIsRead: before the chart's
// verb has printed, the screen draws the tickets the listing names, with the
// blockers `jig next` gave them.
func TestTheChartRowsAreWhatTheTicketsListSaysUntilTheVerbIsRead(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	c := g.m.w.charts["checkout"]
	rows := g.m.chartRows(c)
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.id)
	}
	if want := []string{"JIG-4", "JIG-5", "JIG-6", "JIG-7", "JIG-8"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want the listing's %v", ids, want)
	}
	if note, own := g.m.chartNote(rows[2]); stripANSI(note) != "stacked on JIG-5" || !own {
		t.Errorf("JIG-6's note = %q, %v", note, own)
	}
	// With the verb read, its order is the screen's: what can start comes before what waits.
	g.press("end", "enter")
	var after []string
	for _, r := range g.m.chartRows(c) {
		after = append(after, r.id)
	}
	if want := []string{"JIG-4", "JIG-5", "JIG-8", "JIG-6", "JIG-7"}; !reflect.DeepEqual(after, want) {
		t.Errorf("ids after the verb = %v, want %v", after, want)
	}
}

// TestAChartTicketTheScreensKnowNothingOfIsDrawnFromTheVerb: a ticket graduated
// a moment ago, before the list or the stream names it, has its steps as the
// verb says them.
func TestAChartTicketTheScreensKnowNothingOfIsDrawnFromTheVerb(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	roster := jsonLine(map[string]any{"tickets": []map[string]any{}, "charts": []map[string]any{checkoutListing()}})
	answerChart(g, roster, "checkout", checkoutJSON(), nil)
	g.feed().press("enter")
	v := g.view()
	for _, want := range []string{"✓ JIG-4  Coupon model", "✓ ✓ ✓ ✓", "◐ JIG-5", "✓ ◐ ○ ○", "ready · brief next", "waits for JIG-4 merged"} {
		if !strings.Contains(v, want) {
			t.Errorf("the screen lacks %q:\n%s", want, v)
		}
	}
}

// TestChartStatusWords: the words the header and the list give a chart.
func TestChartStatusWords(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	c := g.m.w.charts["checkout"]
	status := func() (string, string) { return g.m.chartStatus(c, g.m.chartRows(c)) }

	if state, label := status(); state != StateBuilding || label != "in progress" {
		t.Errorf("a chart whose tickets were touched, before the verb: %s, %q", state, label)
	}
	for _, tc := range []struct {
		name         string
		mod          func(*chartInfo)
		state, label string
	}{
		{"ready and started", func(i *chartInfo) { i.Ready, i.Started = true, true }, StateBuilding, "in progress"},
		{"ready, not started", func(i *chartInfo) { i.Ready, i.Started = true, false }, StateReady, "ready"},
		{"waits for a merge", func(i *chartInfo) { i.Ready, i.Code = false, phase.CodeChartWaitsMerge }, StateQueued, "waits for a merge"},
		{"done", func(i *chartInfo) { i.Ready, i.Code = false, phase.CodeChartDone }, StatePublished, "done"},
		{"blocked", func(i *chartInfo) { i.Ready, i.Code = false, phase.CodeChartBlocked }, StateStalled, "blocked"},
		{"not graduated", func(i *chartInfo) { i.Ready, i.Code = false, phase.CodeChartNotGraduated }, StateQueued, "not graduated"},
	} {
		info, _ := parseChart(checkoutJSON())
		tc.mod(info)
		c.info = info
		if state, label := status(); state != tc.state || label != tc.label {
			t.Errorf("%s: %s, %q, want %s, %q", tc.name, state, label, tc.state, tc.label)
		}
	}

	// A ticket that needs the person comes before the verb's word.
	g.feed(jl(at(30, 0), "JIG-5", "c", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-009", "agent")))
	info, _ := parseChart(checkoutJSON())
	c.info = info
	if state, label := status(); state != StateNeedsInput || label != "run needs you" {
		t.Errorf("a chart with an open question: %s, %q", state, label)
	}
	if v := g.view(); !strings.Contains(v, "1 needs you") {
		t.Errorf("the list does not say a chart needs you:\n%s", v)
	}

	empty := &chart{listing: chartListing{Name: "x", Ungraduated: 1}}
	if state, label := g.m.chartStatus(empty, nil); state != StateQueued || label != "not graduated" {
		t.Errorf("a chart with no ticket: %s, %q", state, label)
	}
}

func TestWaitAndEndNotes(t *testing.T) {
	t.Parallel()
	open, merged := []chartPR{{URL: "https://example.test/pull/3", State: "OPEN"}}, []chartPR{{URL: "https://example.test/pull/3", State: "MERGED"}}
	for _, tc := range []struct {
		w    chartWait
		want string
	}{
		{chartWait{Ticket: "A-1", Kind: "stacked"}, "stacked on A-1"},
		{chartWait{Ticket: "A-1", Kind: "merged"}, "waits for A-1 merged"},
		{chartWait{Ticket: "A-1"}, "waits for A-1 merged"},
		{chartWait{Ticket: "A-1", Kind: "merged", PRs: open}, "waits for A-1 merged · its PR is open"},
		{chartWait{Ticket: "A-1", Kind: "merged", PRs: merged}, "waits for A-1 merged"},
	} {
		if got := waitNote(tc.w); got != tc.want {
			t.Errorf("waitNote(%+v) = %q, want %q", tc.w, got, tc.want)
		}
	}
	for _, tc := range []struct {
		e    chartEnd
		want string
	}{
		{chartEnd{Ticket: "A-2", Blocker: "A-1", Kind: "merged", PRs: open}, "A-2 waits for A-1 merged · https://example.test/pull/3 is open"},
		{chartEnd{Ticket: "A-2", Blocker: "A-1", PRs: []chartPR{{URL: "https://example.test/pull/3"}}}, "A-2 waits for A-1 merged · https://example.test/pull/3"},
		{chartEnd{Ticket: "A-2", Blocker: "A-1", Fact: "it is not published yet"}, "A-2 waits for A-1 merged · it is not published yet"},
		{chartEnd{Ticket: "A-2", Blocker: "A-1"}, "A-2 waits for A-1 merged"},
	} {
		if got := endNote(tc.e); got != tc.want {
			t.Errorf("endNote(%+v) = %q, want %q", tc.e, got, tc.want)
		}
	}
}

// TestTheMapGivesUpLinesInOrder: with fewer rows than the map has lines, the
// out-of-scope lines go first, then the decisions and the fog shrink to a line
// and a count, then the destination to one line, then the decisions, then the fog.
func TestTheMapGivesUpLinesInOrder(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	mp := &chartMap{
		Destination: []string{"Customers take a coupon code to a discounted order"},
		Decided:     []string{"One use", "Before tax"},
		Fog:         []string{"Stacking", "Currencies"},
		OutOfScope:  []string{"Gift cards"},
	}
	for _, tc := range []struct {
		rows int
		want []string // the labels that show, one per line (- for a continued one)
	}{
		{8, []string{"destination", "decided", "-", "fog", "-", "out of scope"}},
		{6, []string{"destination", "decided", "-", "fog", "-", "out of scope"}},
		{5, []string{"destination", "decided", "-", "fog", "-"}},
		{4, []string{"destination", "decided", "fog", "-"}},
		{3, []string{"destination", "decided", "fog"}},
		{2, []string{"destination", "fog"}},
		{1, []string{"destination"}},
	} {
		lines := g.m.mapLines(mp, 73, tc.rows)
		var got []string
		for _, l := range lines {
			l = stripANSI(l)
			label := strings.TrimSpace(l[:mapLabelW])
			if label == "" {
				label = "-"
			}
			got = append(got, label)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("in %d rows the map has %v, want %v:\n%s", tc.rows, got, tc.want, stripANSI(strings.Join(lines, "\n")))
		}
	}
	one := stripANSI(strings.Join(g.m.mapLines(mp, 73, 3), "\n"))
	for _, want := range []string{"✓ One use  +1 more", "~ Stacking  +1 more"} {
		if !strings.Contains(one, want) {
			t.Errorf("the shrunk map lacks %q:\n%s", want, one)
		}
	}
	long := &chartMap{Decided: []string{"a", "b", "c", "d", "e", "f"}}
	if got := stripANSI(strings.Join(g.m.mapLines(long, 73, 10), "\n")); !strings.Contains(got, "+3 more") || strings.Contains(got, "✓ d") {
		t.Errorf("six decisions in four lines:\n%s", got)
	}
}

// TestTheRunPanelGivesUpTheWavesBeforeTheEndAndThePrompt: a short screen keeps
// what to say and where the run ends.
func TestTheRunPanelGivesUpTheWavesBeforeTheEndAndThePrompt(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	info, _ := parseChart(checkoutJSON(func(v map[string]any) { v["briefs"] = []string{"JIG-6", "JIG-8"} }))
	rows := g.m.runRows(info)
	labels := func(rows []runRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.label)
		}
		return out
	}
	if want := []string{"next", "briefs", "wave 1", "wave 2", "ends at"}; !reflect.DeepEqual(labels(rows), want) {
		t.Fatalf("rows = %v, want %v", labels(rows), want)
	}
	for n, want := range map[int][]string{
		4: {"next", "briefs", "wave 1", "ends at"},
		3: {"next", "briefs", "ends at"},
		2: {"next", "ends at"},
		1: {"next"},
	} {
		if got := labels(trimRuns(rows, n)); !reflect.DeepEqual(got, want) {
			t.Errorf("%d rows left: %v, want %v", n, got, want)
		}
	}
	// A chart that cannot start says why, beside the prompt that resumes it.
	waits, _ := parseChart(checkoutJSON(func(v map[string]any) {
		v["ready"], v["code"], v["reason"], v["ends"] = false, "CHART_WAITS_MERGE", "JIG-7 waits for JIG-4 to merge", []map[string]any{}
	}))
	if got := labels(g.m.runRows(waits)); !reflect.DeepEqual(got, []string{"next", "why", "wave 1", "wave 2"}) {
		t.Errorf("rows of a chart with a reason and no end = %v", got)
	}
	none, _ := parseChart(checkoutJSON(func(v map[string]any) {
		v["ready"], v["prompt"], v["reason"], v["run"], v["ends"] = false, "", "run `jig graduate checkout`", [][]string{}, []map[string]any{}
	}))
	if r := g.m.runRows(none); len(r) != 1 || stripANSI(r[0].text) != "run `jig graduate checkout`" {
		t.Errorf("rows of a chart with no prompt = %+v", r)
	}
}

// TestAChartScreenDrawnWithColorHasTheTextOfOneWithout: color is only decoration
// on the chart screen and on the list's chart rows.
func TestAChartScreenDrawnWithColorHasTheTextOfOneWithout(t *testing.T) {
	t.Parallel()
	plain := chartScreenRig(t)
	paint := chartRig(t)
	paint.m.st = styles{r: colored()}
	paint.press("end", "enter")
	got := paint.view()
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("the colored screen has no escape sequence:\n%s", got)
	}
	if stripped := stripANSI(got); stripped != plain.view() {
		t.Errorf("color changed the text\n--- plain\n%s\n--- colored, stripped\n%s", plain.view(), stripped)
	}
	paint.press("esc")
	plain.press("esc")
	if stripped := stripANSI(paint.view()); stripped != plain.view() {
		t.Errorf("color changed the list\n--- plain\n%s\n--- colored, stripped\n%s", plain.view(), stripped)
	}
}

// TestTheTailOfAChartIsTheLinesOfItsTickets: t on the chart screen shows the
// stream of the chart's tickets, each line with its ticket.
func TestTheTailOfAChartIsTheLinesOfItsTickets(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.press("t")
	v := g.view()
	if !strings.Contains(v, "tail ") || !strings.Contains(v, "JIG-4") || strings.Contains(v, "JIG-1 ") {
		t.Errorf("the tail of the chart:\n%s", v)
	}
}
