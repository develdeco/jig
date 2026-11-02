package e2e

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestChartNewFlow walks a chart from its making to its tickets on the real
// binary: jig chart new commits the skeleton to the store's origin, an empty
// chart is told right and refused by jig validate, a chart with an entry that
// names a later entry and one with an out-of-range ref is listed by jig
// validate (exit 2, nothing minted, the store as it was), shown with both
// problems by jig next (text and JSON, exit 0) and still refused by jig
// graduate, and once the refs are fixed validate passes and graduate creates the
// tickets. The GitHub mirror's checkpoint is not part of it: the real binary
// would reach api.github.com (cmd/jig's TestChartNewRunsItsOwnCheckpointSync).
func TestChartNewFlow(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env, _ = ghOnPath(t, env.without("JIG_SLICE", "JIG_SESSION"))
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	storeHead := func() string { return gitLog(t, fx.StoreDir, "rev-parse", "HEAD") }
	storeStatus := func() string { return gitLog(t, fx.StoreDir, "status", "--porcelain") }
	ticketIDs := func() []string {
		ids, err := st.TicketIDs()
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	ticketsBefore := ticketIDs()

	// 1. jig chart new: the chart is on the origin as one commit, the store is
	// clean, and the output names the folder and the three next steps.
	r := runJig(t, env, fx.StoreDir, "chart", "new", "coupons", "--title", "Coupons at checkout")
	if r.Code != 0 {
		t.Fatalf("jig chart new coupons: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{
		"chart:", "name: coupons", "title: Coupons at checkout", filepath.Join(fx.StoreDir, "charts", "coupons"),
		"Check them with `jig validate coupons`: it lists every problem and mints nothing",
		"Run `jig graduate coupons` to create the tickets",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig chart new: output lacks %q:\n%s", want, r.Stdout)
		}
	}
	if subject := gitLog(t, fx.StoreRemote, "log", "-1", "--pretty=%s"); subject != "chart coupons: new chart" {
		t.Errorf("the origin's last commit = %q, want %q", subject, "chart coupons: new chart")
	}
	if remote := gitLog(t, fx.StoreRemote, "rev-parse", "HEAD"); remote != storeHead() {
		t.Errorf("the store is at %s, the origin at %s: the chart was not pushed", storeHead(), remote)
	}
	if status := storeStatus(); status != "" {
		t.Errorf("the store is not clean after jig chart new:\n%s", status)
	}

	// 2. A chart with no entries is told right, and the map's sections are empty.
	empty := nextJSON[chartOut](t, env, fx.RepoDir, "coupons")
	wantReason := "the chart has no entries yet: add them to charts/coupons/tickets.yaml and check them with `jig validate coupons`"
	if empty.Code != "CHART_NOT_GRADUATED" || empty.Reason != wantReason || empty.Ready || empty.Prompt != "" || empty.Title != "Coupons at checkout" || len(empty.Tickets) != 0 {
		t.Errorf("jig next coupons = code %q reason %q ready %v prompt %q title %q, want not graduated with %q", empty.Code, empty.Reason, empty.Ready, empty.Prompt, empty.Title, wantReason)
	}
	if m := empty.Map; m == nil || len(m.Destination)+len(m.Decided)+len(m.Fog)+len(m.OutOfScope) != 0 {
		t.Errorf("the new chart's map = %+v, want the title and four empty sections", m)
	}
	if r := runJig(t, env, fx.StoreDir, "validate", "coupons"); r.Code != 2 || !strings.Contains(r.Stdout, "charts/coupons/tickets.yaml has no entries") {
		t.Errorf("jig validate coupons on an empty chart: exit %d, want 2 naming the empty file\n%s", r.Code, r.Stdout)
	}

	// 3. Three entries: a good one, one naming the later entry #3, one whose ref
	// #9 is out of range.
	writeFileOrFatal(t, st.ChartFile("coupons"), []byte(`tickets:
  - title: Apply a coupon code
  - title: Total with coupons
    blocked_by:
      - ref: "#3"
  - title: Receipt shows the coupon
    blocked_by:
      - ref: "#9"
`))
	headBefore, statusBefore := storeHead(), storeStatus()

	// 4. jig validate lists both, exits 2 and changes nothing.
	r = runJig(t, env, fx.StoreDir, "validate", "coupons")
	if r.Code != 2 {
		t.Fatalf("jig validate coupons: exit %d, want 2\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	later := `entry 2 ("Total with coupons"): blocked_by ref "#3" names a later entry that is also being created; it must point at an earlier one`
	outOfRange := `entry 3 ("Receipt shows the coupon"): blocked_by ref "#9" is out of range`
	closing := "Fix them in charts/coupons/tickets.yaml, then run `jig validate coupons` again"
	for _, want := range []string{"chart coupons failed validation", "VALIDATION_ERROR", later, outOfRange, closing} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig validate coupons: output lacks %q:\n%s", want, r.Stdout)
		}
	}
	if strings.Index(r.Stdout, later) > strings.Index(r.Stdout, outOfRange) {
		t.Errorf("jig validate coupons lists the problems out of the entries' order:\n%s", r.Stdout)
	}
	if storeHead() != headBefore || storeStatus() != statusBefore || !reflect.DeepEqual(ticketIDs(), ticketsBefore) {
		t.Errorf("jig validate changed the store: head %s -> %s, status %q -> %q, tickets %v -> %v", headBefore, storeHead(), statusBefore, storeStatus(), ticketsBefore, ticketIDs())
	}

	// 5. jig next shows both as the rows' problems, the chart still standing.
	c := nextJSON[chartOut](t, env, fx.RepoDir, "coupons")
	if len(c.Tickets) != 3 || c.Ungraduated != 3 {
		t.Fatalf("jig next coupons: %d rows, %d ungraduated, want 3 and 3: %+v", len(c.Tickets), c.Ungraduated, c.Tickets)
	}
	byPos := map[int]string{}
	for _, row := range c.Tickets {
		byPos[row.Pos] = row.Problem
		switch row.Pos {
		case 1:
			if row.Problem != "" || row.Reach == "blocked" {
				t.Errorf("entry 1 = %+v, want no problem", row)
			}
		default:
			if row.Reach != "blocked" || row.ID != "" || row.Problem == "" || strings.Contains(row.Problem, "jig validate") {
				t.Errorf("entry %d = %+v, want a blocked row whose problem is the problem alone (the verdict names jig validate)", row.Pos, row)
			}
		}
	}
	if !strings.Contains(byPos[2], `blocked_by ref "#3" names a later entry`) || !strings.Contains(byPos[3], `blocked_by ref "#9" is out of range`) {
		t.Errorf("the rows' problems = %v, want the later-entry and the out-of-range ones", byPos)
	}
	if c.Code != "CHART_NOT_GRADUATED" || !strings.Contains(c.Reason, "run `jig validate coupons` first") {
		t.Errorf("verdict = code %q reason %q, want not graduated, validate first", c.Code, c.Reason)
	}
	text := runJig(t, env, fx.RepoDir, "next", "coupons")
	if text.Code != 0 {
		t.Fatalf("jig next coupons: exit %d\n%s%s", text.Code, text.Stdout, text.Stderr)
	}
	for _, want := range []string{
		"problems[2]{pos,id,problem}:", "\n  2,-,", "\n  3,-,",
		"Run `jig validate coupons` to list the problems of the entries that have no ticket yet, fix them in charts/coupons/tickets.yaml, then run `jig graduate coupons`",
	} {
		if !strings.Contains(strings.ReplaceAll(text.Stdout, "\r\n", "\n"), want) {
			t.Errorf("jig next coupons: text lacks %q:\n%s", want, text.Stdout)
		}
	}

	// 6. jig graduate still refuses, with the first problem, and mints nothing.
	r = runJig(t, env, fx.StoreDir, "graduate", "coupons")
	if r.Code != 2 || !strings.Contains(r.Stdout, `blocked_by ref "#3" names a later entry that is also being created`) {
		t.Errorf("jig graduate coupons: exit %d, want 2 on the first problem\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if !reflect.DeepEqual(ticketIDs(), ticketsBefore) {
		t.Errorf("jig graduate minted tickets despite the problems: %v -> %v", ticketsBefore, ticketIDs())
	}

	// 7. The refs fixed, jig validate passes and graduate creates the three.
	writeFileOrFatal(t, st.ChartFile("coupons"), []byte(`tickets:
  - title: Apply a coupon code
  - title: Total with coupons
    blocked_by:
      - ref: "#1"
  - title: Receipt shows the coupon
    blocked_by:
      - ref: "#2"
`))
	r = runJig(t, env, fx.StoreDir, "validate", "coupons")
	if r.Code != 0 {
		t.Fatalf("jig validate coupons after the fix: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{"valid: yes", "entries: 3", "graduated: 0", "ungraduated: 3", "Run `jig graduate coupons` to create the 3 entries that have no ticket yet"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig validate coupons after the fix: output lacks %q:\n%s", want, r.Stdout)
		}
	}
	if r := runJig(t, env, fx.StoreDir, "graduate", "coupons"); r.Code != 0 {
		t.Fatalf("jig graduate coupons: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	done := nextJSON[chartOut](t, env, fx.RepoDir, "coupons")
	if done.Ungraduated != 0 || len(done.Tickets) != 3 {
		t.Fatalf("after graduating: %d ungraduated, %d rows, want 0 and 3", done.Ungraduated, len(done.Tickets))
	}
	for _, row := range done.Tickets {
		if row.ID == "" || row.Problem != "" {
			t.Errorf("after graduating: entry %d = %+v, want a ticket and no problem", row.Pos, row)
		}
	}
	if got := len(ticketIDs()) - len(ticketsBefore); got != 3 {
		t.Errorf("graduate minted %d tickets, want 3", got)
	}
	if r := runJig(t, env, fx.StoreDir, "validate", "coupons"); r.Code != 0 || !strings.Contains(r.Stdout, "ungraduated: 0") || !strings.Contains(r.Stdout, "Every entry has a ticket") {
		t.Errorf("jig validate coupons once graduated: exit %d\n%s", r.Code, r.Stdout)
	}
}
