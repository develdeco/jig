package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// The tests here ask the readers of a chart's standing what they say: `jig next
// <chart>` and `jig status <chart>` over a store made of files alone (no test
// here starts git), and deriveChart with a fake merge source where a pull
// request's state matters. The rules behind each answer are internal/phase's
// (DeriveChart); this is the seam where what is read from the store is laid in,
// and the words and JSON the verbs print are pinned. The end-to-end chain with
// real verbs and a gh stub is e2e's TestChartFlow.

// chartRig is a store with the tickets and charts a test writes into it.
type chartRig struct {
	st   *store.Store
	root string
	home string
}

func newChartRig(t *testing.T) chartRig {
	t.Helper()
	root := t.TempDir()
	r := chartRig{root: root, home: t.TempDir()}
	r.put(t, "project.yaml", "schema_version: 2\nname: demo\nkeys:\n  SHOP: everything in demo\ntrackers: []\n")
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r.st = st
	return r
}

func (r chartRig) put(t *testing.T, name, content string) {
	t.Helper()
	path := filepath.Join(r.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ticket writes ticket id with a brief and one slice; extra is more of its
// ticket.yaml (rank, blocked_by). approved says a person approved the brief.
func (r chartRig) ticket(t *testing.T, id, extra string, approved bool) {
	t.Helper()
	r.put(t, "tickets/"+id+"/ticket.yaml", "schema_version: 1\ntitle: Ticket "+id+"\n"+extra)
	r.put(t, "tickets/"+id+"/brief.md", "# "+id+"\n\n## Goal\n\nDo it.\n")
	r.put(t, "tickets/"+id+"/slices.yaml", "slices:\n  - id: a\n    workspace: alpha\n    goal: Build a.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n")
	if approved {
		approveBrief(t, r.st, id)
	}
}

func (r chartRig) chart(t *testing.T, name, tickets, mapMD string) {
	t.Helper()
	r.put(t, "charts/"+name+"/tickets.yaml", tickets)
	if mapMD != "" {
		r.put(t, "charts/"+name+"/map.md", mapMD)
	}
}

func (r chartRig) publish(t *testing.T, id string) {
	t.Helper()
	if err := journal.Append(r.st, id, journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
}

func (r chartRig) env() env {
	return envFrom(map[string]string{"JIG_HOME": r.home, "HOME": r.home, "USERPROFILE": r.home})
}

// run runs jig on the rig's store.
func (r chartRig) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return runMain(t, r.env(), "", append(args, "--store", r.root)...)
}

// next is `jig next <word> --json`, decoded.
func (r chartRig) next(t *testing.T, word string) nextChart {
	t.Helper()
	out, code := r.run(t, "next", word, "--json")
	var n nextChart
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		t.Fatalf("jig next %s --json: exit %d, %v\n%s", word, code, err, out)
	}
	return n
}

// shop is the chart of the mockup: SHOP-1 and SHOP-2 independent, SHOP-3 waits
// for SHOP-1 to merge and SHOP-4 is stacked on SHOP-2.
func (r chartRig) shop(t *testing.T, approved bool) {
	t.Helper()
	r.ticket(t, "SHOP-1", "rank: 2\n", approved)
	r.ticket(t, "SHOP-2", "", false)
	r.ticket(t, "SHOP-3", "blocked_by:\n  - ticket: SHOP-1\n    kind: merged\n", false)
	r.ticket(t, "SHOP-4", "blocked_by:\n  - ticket: SHOP-2\n    kind: stacked\n", false)
	r.chart(t, "checkout", "tickets:\n  - id: SHOP-1\n    title: Apply a coupon code\n  - id: SHOP-2\n    title: Total with coupons\n  - id: SHOP-3\n    title: Receipt shows the coupon\n  - id: SHOP-4\n    title: Coupon analytics\n",
		"# Coupons at checkout\n\n## Destination\n\nCoupons take money off an order.\n\n## Decisions so far\n\n- One coupon per order\n\n## Not yet specified (fog)\n\n- Stacking with sale prices\n\n## Out of scope\n\n- Gift cards\n")
}

func rowOf(t *testing.T, n nextChart, id string) nextChartTicket {
	t.Helper()
	for _, row := range n.Tickets {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("no ticket %s in %+v", id, n.Tickets)
	return nextChartTicket{}
}

// TestNextChartSaysWhereTheChartStands: the JSON of `jig next <chart>` carries
// the verdict, the tickets in order with their steps and reach, the waves, the
// briefs and the map; once a brief is approved the chart reads as started.
func TestNextChartSaysWhereTheChartStands(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)

	n := r.next(t, "checkout")
	if n.Chart != "checkout" || n.Title != "Coupons at checkout" || !n.Ready || n.Prompt != "Take checkout to PR" || n.Started || n.Code != "" {
		t.Errorf("verdict = %+v", n)
	}
	if want := []string{"SHOP-1", "SHOP-2", "SHOP-3", "SHOP-4"}; !reflect.DeepEqual(n.Briefs, want) {
		t.Errorf("briefs = %v, want %v", n.Briefs, want)
	}
	if want := [][]string{{"SHOP-1", "SHOP-2"}, {"SHOP-4"}}; !reflect.DeepEqual(n.Run, want) {
		t.Errorf("run = %v, want %v", n.Run, want)
	}
	// The ranked ticket leads its wave, then the unranked in order.
	var order []string
	for _, row := range n.Tickets {
		order = append(order, row.ID)
	}
	if want := []string{"SHOP-1", "SHOP-2", "SHOP-3", "SHOP-4"}; !reflect.DeepEqual(order, want) {
		t.Errorf("ticket order = %v, want %v", order, want)
	}
	three := rowOf(t, n, "SHOP-3")
	if three.Reach != "after-merge" || three.Phase != "brief" || three.Prompt != "Plan SHOP-3" || !reflect.DeepEqual(three.Steps, []string{"current", "pending", "pending", "pending"}) {
		t.Errorf("SHOP-3 = %+v", three)
	}
	if len(three.WaitsFor) != 1 || three.WaitsFor[0].Ticket != "SHOP-1" || three.WaitsFor[0].Kind != "merged" || three.WaitsFor[0].Code != "CLAIM_BLOCKED_MERGED" {
		t.Errorf("SHOP-3 waits for %+v, want SHOP-1 merged", three.WaitsFor)
	}
	if four := rowOf(t, n, "SHOP-4"); four.Reach != "here" || four.Wave != 1 || four.WaitsFor[0].Kind != "stacked" {
		t.Errorf("SHOP-4 = %+v, want here, wave 1, waiting on a stacked edge", four)
	}
	if n.Map == nil || !reflect.DeepEqual(n.Map.Fog, []string{"Stacking with sale prices"}) || !reflect.DeepEqual(n.Map.Decided, []string{"One coupon per order"}) ||
		!reflect.DeepEqual(n.Map.Destination, []string{"Coupons take money off an order."}) || !reflect.DeepEqual(n.Map.OutOfScope, []string{"Gift cards"}) {
		t.Errorf("map = %+v", n.Map)
	}
	if len(n.Ends) != 1 || n.Ends[0].Ticket != "SHOP-3" || n.Ends[0].Blocker != "SHOP-1" {
		t.Errorf("ends = %+v, want SHOP-3 waiting on SHOP-1", n.Ends)
	}

	r.shop(t, true)
	n = r.next(t, "checkout")
	if !n.Started || n.Prompt != "Continue checkout" {
		t.Errorf("with a brief approved: started %v prompt %q, want Continue checkout", n.Started, n.Prompt)
	}
	if one := rowOf(t, n, "SHOP-1"); one.Phase != "build" || one.Prompt != "Build SHOP-1" || !one.Ready {
		t.Errorf("SHOP-1 = %+v, want the build ready", one)
	}
}

// TestNextChartTextAndJSONAgree: the text form names the same verdict, the same
// tickets (steps as four letters, reach, what each waits for), the same waves,
// and ends in the help for that verdict; `jig status <chart>` prints the same
// tickets table.
func TestNextChartTextAndJSONAgree(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)

	text, code := r.run(t, "next", "checkout")
	if code != 0 {
		t.Fatalf("jig next checkout: exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"chart: checkout", "title: Coupons at checkout", "ready: yes", "prompt: Take checkout to PR", "started: no", "ungraduated: 0",
		"tickets[4]{id,title,steps,reach,waits_for}:",
		"SHOP-1,Ticket SHOP-1,cppp,here,-",
		"SHOP-3,Ticket SHOP-3,cppp,after-merge,SHOP-1(merged)",
		"SHOP-4,Ticket SHOP-4,cppp,here,SHOP-2(stacked)",
		"briefs[4]{ticket}:", "run[2]{wave,tickets}:", "1,SHOP-1+SHOP-2", "2,SHOP-4",
		"waits[1]{ticket,blocker,kind,pr,state,fact}:",
		"Ask: Take checkout to PR",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}

	status, code := r.run(t, "status", "checkout")
	if code != 0 {
		t.Fatalf("jig status checkout: exit %d\n%s", code, status)
	}
	// A table is its header and the indented rows under it.
	table := func(s string) string {
		var out []string
		in := false
		for _, line := range strings.Split(s, "\n") {
			switch {
			case strings.HasPrefix(line, "tickets["):
				in = true
			case in && !strings.HasPrefix(line, "  "):
				in = false
			}
			if in {
				out = append(out, line)
			}
		}
		if len(out) == 0 {
			t.Fatalf("no tickets table in:\n%s", s)
		}
		return strings.Join(out, "\n")
	}
	if table(status) != table(text) || !strings.HasPrefix(status, "status:\n") {
		t.Errorf("status = \n%s\nwant the tickets table of next:\n%s", status, table(text))
	}
}

// TestNextResolvesATicketBeforeAChart: a ticket (by id or alias) wins over a
// chart of the same name; a word that is neither names both kinds in the
// refusal; --for and --topology refuse a chart.
func TestNextResolvesATicketBeforeAChart(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	r.ticket(t, "SHOP-9", "aliases:\n  - OLD-9\n", false)
	// A chart named like a ticket.
	r.chart(t, "SHOP-1", "tickets:\n  - id: SHOP-2\n    title: x\n", "")

	var asTicket struct {
		Ticket string `json:"ticket"`
		Chart  string `json:"chart"`
	}
	for word, want := range map[string]string{"SHOP-1": "SHOP-1", "OLD-9": "SHOP-9"} {
		out, code := r.run(t, "next", word, "--json")
		if err := json.Unmarshal([]byte(out), &asTicket); code != 0 || err != nil || asTicket.Ticket != want || asTicket.Chart != "" {
			t.Errorf("jig next %s = exit %d, %v, %+v, want the ticket %s", word, code, err, asTicket, want)
		}
	}

	out, code := r.run(t, "next", "nosuch")
	if code == 0 || !strings.Contains(out, "no ticket or chart named") || !strings.Contains(out, "checkout") || !strings.Contains(out, "VALIDATION_ERROR") {
		t.Errorf("jig next nosuch = exit %d\n%s\nwant a refusal naming tickets and the charts", code, out)
	}
	if out, code := r.run(t, "status", "nosuch"); code == 0 || !strings.Contains(out, "no ticket or chart named") {
		t.Errorf("jig status nosuch = exit %d\n%s", code, out)
	}
	for _, flag := range [][]string{{"--for", "build"}, {"--topology"}} {
		out, code := r.run(t, append([]string{"next", "checkout"}, flag...)...)
		if code == 0 || !strings.Contains(out, "is a chart") {
			t.Errorf("jig next checkout %v = exit %d\n%s, want a refusal", flag, code, out)
		}
	}

	// A store with no charts refuses an unknown word as it always did.
	bare := newChartRig(t)
	bare.ticket(t, "SHOP-1", "", false)
	if out, code := bare.run(t, "next", "nosuch"); code == 0 || !strings.Contains(out, "ticket nosuch not found in the store") {
		t.Errorf("jig next nosuch with no charts = exit %d\n%s", code, out)
	}
}

// TestChartEdgesComeFromTheTicket: once an entry is graduated its ticket.yaml
// holds the edges; the chart's own refs are not read, so a change of the ticket
// moves the standing and a stale ref cannot refuse the chart.
func TestChartEdgesComeFromTheTicket(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	// The chart still says SHOP-3 waits on a ticket that is gone.
	r.chart(t, "checkout", "tickets:\n  - id: SHOP-1\n    title: One\n  - id: SHOP-3\n    title: Three\n    blocked_by:\n      - ref: NOPE-1\n", "")
	if err := os.Remove(filepath.Join(r.root, "charts", "checkout", "map.md")); err != nil {
		t.Fatal(err)
	}

	n := r.next(t, "checkout")
	three := rowOf(t, n, "SHOP-3")
	if three.Reach != "after-merge" || len(three.WaitsFor) != 1 || three.WaitsFor[0].Ticket != "SHOP-1" {
		t.Errorf("SHOP-3 = %+v, want it to wait on SHOP-1, as its ticket says", three)
	}
	if n.Map != nil || n.Title != "" {
		t.Errorf("a chart with no map.md has map %+v title %q", n.Map, n.Title)
	}

	r.ticket(t, "SHOP-3", "", false)
	if three := rowOf(t, r.next(t, "checkout"), "SHOP-3"); three.Reach != "here" || len(three.WaitsFor) != 0 {
		t.Errorf("SHOP-3 after its ticket lost the edge = %+v, want here", three)
	}
}

// TestChartRefsOfEntriesWithNoTicket: an entry with no id takes its edges from
// the chart's refs, which resolve as graduate resolves them: one on an entry
// that has none either waits on its position, and a ref that does not resolve
// is graduate's refusal.
func TestChartRefsOfEntriesWithNoTicket(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", false)
	r.chart(t, "c", "tickets:\n  - id: SHOP-1\n    title: One\n  - title: Two\n    blocked_by:\n      - ref: SHOP-1\n        kind: stacked\n  - title: Three\n    blocked_by:\n      - ref: \"#2\"\n", "")

	n := r.next(t, "c")
	if n.Ungraduated != 2 || !n.Ready || n.Prompt != "Take c to PR" {
		t.Errorf("verdict = ungraduated %d ready %v prompt %q", n.Ungraduated, n.Ready, n.Prompt)
	}
	if want := [][]string{{"SHOP-1"}}; !reflect.DeepEqual(n.Run, want) {
		t.Errorf("run = %v, want %v: an entry with no ticket is no part of a run", n.Run, want)
	}
	var two, three nextChartTicket
	for _, row := range n.Tickets {
		switch row.Pos {
		case 2:
			two = row
		case 3:
			three = row
		}
	}
	if two.ID != "" || two.Steps != nil || two.Reach != "here" || len(two.WaitsFor) != 1 || two.WaitsFor[0].Ticket != "SHOP-1" || two.WaitsFor[0].Kind != "stacked" {
		t.Errorf("entry 2 = %+v, want no id or steps, here, stacked on SHOP-1", two)
	}
	// A merged edge on an entry that builds in this run ends the run at it.
	if three.Reach != "after-merge" || len(three.WaitsFor) != 1 || three.WaitsFor[0].Pos != 2 || three.WaitsFor[0].Ticket != "" {
		t.Errorf("entry 3 = %+v, want a wait on the entry at position 2", three)
	}
	if text, _ := r.run(t, "next", "c"); !strings.Contains(text, "Run `jig graduate c` to create the 2 missing ticket(s)") || !strings.Contains(text, "-,Two,-,here,SHOP-1(stacked)") {
		t.Errorf("text:\n%s", text)
	}

	r.chart(t, "bad", "tickets:\n  - title: One\n    blocked_by:\n      - ref: \"#9\"\n", "")
	if out, code := r.run(t, "next", "bad"); code == 0 || !strings.Contains(out, `blocked_by ref "#9" is out of range`) {
		t.Errorf("jig next bad = exit %d\n%s, want graduate's refusal", code, out)
	}
}

// TestChartRowWithAnUnreadableTicketKeepsTheChart: a ticket whose files cannot
// be read is a row with its problem; the chart still names the rest.
func TestChartRowWithAnUnreadableTicketKeepsTheChart(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	r.put(t, "tickets/SHOP-2/slices.yaml", "slices: [not: {valid")

	n := r.next(t, "checkout")
	two := rowOf(t, n, "SHOP-2")
	if two.Problem == "" || two.Reach != "blocked" || two.Steps != nil {
		t.Errorf("SHOP-2 = %+v, want a problem, blocked, no steps", two)
	}
	if four := rowOf(t, n, "SHOP-4"); four.Reach != "blocked" {
		t.Errorf("SHOP-4, stacked on the unreadable one = %+v, want blocked", four)
	}
	if one := rowOf(t, n, "SHOP-1"); one.Reach != "here" || one.Problem != "" {
		t.Errorf("SHOP-1 = %+v, want it untouched", one)
	}
	if want := [][]string{{"SHOP-1"}}; !reflect.DeepEqual(n.Run, want) {
		t.Errorf("run = %v, want %v", n.Run, want)
	}
}

// TestChartWaitsOnThePullRequestTheMergeReaderNames: a published blocker whose
// pull request is open ends the run at it, with the pull request on the end.
func TestChartWaitsOnThePullRequestTheMergeReaderNames(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, true)
	r.ticket(t, "SHOP-3", "blocked_by:\n  - ticket: SHOP-1\n    kind: merged\n", true)
	r.chart(t, "checkout", "tickets:\n  - id: SHOP-1\n    title: One\n  - id: SHOP-3\n    title: Three\n", "")
	r.publish(t, "SHOP-1")
	pr := "https://example.test/shop/pull/7"
	open := ready.Merge{Published: true, Repos: []ready.RepoMerge{{Repo: "shop", PR: pr, State: ready.PROpen, Why: "its PR is open (" + pr + ")"}}}

	c, _, err := deriveChart(r.st, project.Config{}, "checkout", mergesOf{"SHOP-1": open})
	if err != nil {
		t.Fatal(err)
	}
	if c.Ready || c.Code != phase.CodeChartWaitsMerge || c.Prompt != "Continue checkout" {
		t.Fatalf("chart = ready %v code %q prompt %q", c.Ready, c.Code, c.Prompt)
	}
	if len(c.Ends) != 1 || c.Ends[0].ID != "SHOP-3" || !reflect.DeepEqual(c.Ends[0].Because.PRs, []phase.MergePR{{Repo: "shop", URL: pr, State: "OPEN"}}) {
		t.Errorf("ends = %+v, want SHOP-3 on the open pull request", c.Ends)
	}
	if !strings.Contains(c.Reason, pr) {
		t.Errorf("reason = %q, want the pull request", c.Reason)
	}
	// The text help names what to merge and what to ask afterwards.
	help := chartHelp(c)
	if len(help) != 1 || help[0] != "Merge "+pr+", then ask: Continue checkout" {
		t.Errorf("help = %v", help)
	}
	// The ticket's own phase carries the same pull request on its blocker.
	tk, err := derivePhaseWith(r.st, "SHOP-3", mergesOf{"SHOP-1": open})
	if err != nil {
		t.Fatal(err)
	}
	if bs := tk.Blockers(); len(bs) != 1 || bs[0].Code != "CLAIM_BLOCKED_MERGED" || len(bs[0].PRs) != 1 || bs[0].PRs[0].URL != pr {
		t.Errorf("blockers = %+v, want the code and the pull request", bs)
	}

	merged := ready.Merge{Published: true, Repos: []ready.RepoMerge{{Repo: "shop", PR: pr, State: ready.PRMerged, Met: true, By: "pr"}}}
	c, _, err = deriveChart(r.st, project.Config{}, "checkout", mergesOf{"SHOP-1": merged})
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"SHOP-3"}}; !c.Ready || !reflect.DeepEqual(c.Run, want) || c.Prompt != "Continue checkout" {
		t.Errorf("merged: ready %v run %v prompt %q, want [[SHOP-3]] under Continue", c.Ready, c.Run, c.Prompt)
	}
}

// TestNextListsTheCharts: the listing of the store names each chart with its
// title and tickets, in text and JSON, and says nothing when there are none.
func TestNextListsTheCharts(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	r.chart(t, "later", "tickets:\n  - title: One\n  - title: Two\n", "")

	var listing struct {
		Charts []nextChartRow `json:"charts"`
	}
	out, code := r.run(t, "next", "--json")
	if err := json.Unmarshal([]byte(out), &listing); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
	}
	want := []nextChartRow{
		{Name: "checkout", Title: "Coupons at checkout", Tickets: []string{"SHOP-1", "SHOP-2", "SHOP-3", "SHOP-4"}},
		{Name: "later", Tickets: []string{}, Ungraduated: 2},
	}
	if !reflect.DeepEqual(listing.Charts, want) {
		t.Errorf("charts = %+v, want %+v", listing.Charts, want)
	}
	text, _ := r.run(t, "next")
	if !strings.Contains(text, "charts[2]{name,title,tickets}:") || !strings.Contains(text, "checkout,Coupons at checkout,SHOP-1+SHOP-2+SHOP-3+SHOP-4") {
		t.Errorf("text lacks the charts table:\n%s", text)
	}

	bare := newChartRig(t)
	bare.ticket(t, "SHOP-1", "", false)
	if out, _ := bare.run(t, "next", "--json"); strings.Contains(out, `"charts"`) {
		t.Errorf("a store with no charts lists %s", out)
	}
	if out, _ := bare.run(t, "next"); strings.Contains(out, "charts[") {
		t.Errorf("a store with no charts prints a charts table:\n%s", out)
	}
}

// TestChartWavesAndRankOrderMatchTheTicketOrder: for random graphs of entries,
// a row's wave is what ready.Waves gives the same graph as slices, and the
// order within a wave is ready.ByPriority's.
func TestChartWavesAndRankOrderMatchTheTicketOrder(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 300; round++ {
		n := 1 + rng.Intn(7)
		var entries []phase.ChartEntry
		var slices []store.Slice
		var ranked []ready.Ranked
		for i := 1; i <= n; i++ {
			id := fmt.Sprintf("T-%d", i)
			e := phase.ChartEntry{Pos: i, ID: id, Title: id}
			sl := store.Slice{ID: id}
			if rng.Intn(2) == 0 {
				rank := 1 + rng.Intn(3)
				e.Rank = &rank
			}
			for j := 1; j <= n; j++ {
				// Mostly edges to earlier entries, now and then any (a cycle).
				if j == i || rng.Intn(4) != 0 || (j > i && rng.Intn(5) != 0) {
					continue
				}
				blocker := fmt.Sprintf("T-%d", j)
				e.Needs = append(e.Needs, phase.ChartNeed{Ticket: blocker, Kind: "merged", Code: "CLAIM_BLOCKED_MERGED", Why: "waits"})
				sl.BlockedBy = append(sl.BlockedBy, blocker)
			}
			ranked = append(ranked, ready.Ranked{ID: id, Rank: e.Rank})
			entries = append(entries, e)
			slices = append(slices, sl)
		}
		c := phase.DeriveChart(phase.ChartFacts{Name: "c", Entries: entries})
		waves := ready.Waves(slices)
		byWave := map[int][]ready.Ranked{}
		for _, rk := range ranked {
			byWave[waves[rk.ID]] = append(byWave[waves[rk.ID]], rk)
		}
		got := map[int][]string{}
		for _, row := range c.Rows {
			if row.Wave != waves[row.ID] {
				t.Fatalf("round %d: %s wave = %d, want %d (graph %+v)", round, row.ID, row.Wave, waves[row.ID], slices)
			}
			got[row.Wave] = append(got[row.Wave], row.ID)
		}
		for w, group := range byWave {
			if want := ready.ByPriority(group); !reflect.DeepEqual(got[w], want) {
				t.Fatalf("round %d: wave %d order = %v, want %v", round, w, got[w], want)
			}
		}
	}
}
