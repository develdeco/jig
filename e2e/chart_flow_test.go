package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// chartOut is the part of `jig next <chart> --json` these tests read.
type chartOut struct {
	Chart       string     `json:"chart"`
	Title       string     `json:"title"`
	Ready       bool       `json:"ready"`
	Code        string     `json:"code"`
	Reason      string     `json:"reason"`
	Prompt      string     `json:"prompt"`
	Started     bool       `json:"started"`
	Ungraduated int        `json:"ungraduated"`
	Briefs      []string   `json:"briefs"`
	Run         [][]string `json:"run"`
	Tickets     []struct {
		Pos      int      `json:"pos"`
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Wave     int      `json:"wave"`
		Phase    string   `json:"phase"`
		Steps    []string `json:"steps"`
		Reach    string   `json:"reach"`
		Prompt   string   `json:"prompt"`
		WaitsFor []struct {
			Ticket string `json:"ticket"`
			Kind   string `json:"kind"`
			Code   string `json:"code"`
		} `json:"waits_for"`
	} `json:"tickets"`
	Ends []struct {
		Ticket  string `json:"ticket"`
		Blocker string `json:"blocker"`
		Kind    string `json:"kind"`
		PRs     []struct {
			URL   string `json:"url"`
			State string `json:"state"`
		} `json:"prs"`
	} `json:"ends"`
	Map *struct {
		Destination []string `json:"destination"`
		Decided     []string `json:"decided"`
		Fog         []string `json:"fog"`
		OutOfScope  []string `json:"out_of_scope"`
	} `json:"map"`
}

// reachOf is the reach the standing gives ticket id.
func (c chartOut) reachOf(t *testing.T, id string) string {
	t.Helper()
	for _, row := range c.Tickets {
		if row.ID == id {
			return row.Reach
		}
	}
	t.Fatalf("the standing has no ticket %s: %+v", id, c.Tickets)
	return ""
}

// briefed gives ticket id a brief and one slice and records a person's approval
// of the brief, as `jig validate` does once the person confirmed it.
func briefed(t *testing.T, st *store.Store, id string) {
	t.Helper()
	dir := st.TicketDir(id)
	writeFileOrFatal(t, filepath.Join(dir, "brief.md"), []byte("# "+id+"\n\n## Goal\n\nBuild it.\n"))
	writeFileOrFatal(t, filepath.Join(dir, "slices.yaml"), []byte("slices:\n  - id: a\n    workspace: alpha\n    goal: Build it.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n"))
	sum, err := st.BriefSHA256(id)
	if err != nil {
		t.Fatal(err)
	}
	a := store.Approval{By: "Test Person", At: time.Now().UTC().Format(time.RFC3339), BriefSHA256: sum, Provenance: "person"}
	if err := st.WriteApproval(id, a); err != nil {
		t.Fatal(err)
	}
}

// TestChartFlow walks a chart of four tickets through the points a chart run
// passes, asking `jig next <chart>` at each: graduated with nothing written
// (every brief to do, two tickets and one stacked on them in the run, one
// behind a merge), briefs approved (started: "Continue"), a blocker published
// with its pull request open (the run waits, naming it), merged (the held
// ticket is free), everything published (done). The tickets' states are made
// as the other end-to-end tests make them: the real binary reads a store they
// were written into, and a fake gh holds the pull requests.
func TestChartFlow(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env, _ = ghOnPath(t, env.without("JIG_SLICE", "JIG_SESSION"))
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	// The chart: entries 1 and 2 are independent, 3 waits for 1 to merge, 4 is
	// stacked on 2.
	chartDir := filepath.Join(fx.StoreDir, "charts", "c")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(chartDir, "map.md"), []byte(
		"# Coupons at checkout\n\n## Destination\n\nA coupon takes money off an order.\n\n## Decisions so far\n\n- One coupon per order\n\n## Not yet specified (fog)\n\n- Stacking with sale prices\n\n## Out of scope\n\n- Gift cards\n"))
	writeFileOrFatal(t, filepath.Join(chartDir, "tickets.yaml"), []byte(`tickets:
  - title: Apply a coupon code
  - title: Total with coupons
  - title: Receipt shows the coupon
    blocked_by:
      - ref: "#1"
  - title: Coupon analytics
    blocked_by:
      - ref: "#2"
        kind: stacked
`))

	// Before graduating, nothing runs and the standing says so.
	before := nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if before.Ready || before.Code != "CHART_NOT_GRADUATED" || before.Prompt != "" || before.Ungraduated != 4 {
		t.Errorf("before graduating: ready %v code %q prompt %q ungraduated %d, want CHART_NOT_GRADUATED with 4 entries missing", before.Ready, before.Code, before.Prompt, before.Ungraduated)
	}
	if r := runJig(t, env, fx.StoreDir, "graduate", "c"); r.Code != 0 {
		t.Fatalf("jig graduate c: exit %d\n%s", r.Code, r.Stdout)
	}

	// 1. Graduated, no brief written: every brief is the run's first work, the
	// two independent tickets build together and the stacked one after them,
	// and the one behind a merge is not in the run.
	c := nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if len(c.Tickets) != 4 {
		t.Fatalf("the standing has %d tickets, want 4: %+v", len(c.Tickets), c.Tickets)
	}
	id := map[int]string{}
	for _, row := range c.Tickets {
		id[row.Pos] = row.ID
	}
	if c.Title != "Coupons at checkout" || !c.Ready || c.Prompt != "Take c to PR" || c.Code != "" || c.Started || c.Ungraduated != 0 {
		t.Errorf("graduated: title %q ready %v prompt %q code %q started %v, want Take c to PR, not started", c.Title, c.Ready, c.Prompt, c.Code, c.Started)
	}
	if want := [][]string{{id[1], id[2]}, {id[4]}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("run = %v, want %v", c.Run, want)
	}
	if want := []string{id[1], id[2], id[3], id[4]}; !reflect.DeepEqual(c.Briefs, want) {
		t.Errorf("briefs = %v, want every ticket %v", c.Briefs, want)
	}
	if got := c.reachOf(t, id[3]); got != "after-merge" {
		t.Errorf("ticket 3 reach = %s, want after-merge", got)
	}
	for _, row := range c.Tickets {
		if !reflect.DeepEqual(row.Steps, []string{"current", "pending", "pending", "pending"}) || row.Phase != "brief" || row.Prompt != "Plan "+row.ID {
			t.Errorf("%s = steps %v phase %s prompt %q, want the brief current", row.ID, row.Steps, row.Phase, row.Prompt)
		}
		if row.Pos == 3 && (len(row.WaitsFor) != 1 || row.WaitsFor[0].Ticket != id[1] || row.WaitsFor[0].Kind != "merged" || row.WaitsFor[0].Code != "CLAIM_BLOCKED_MERGED") {
			t.Errorf("ticket 3 waits for %+v, want %s merged", row.WaitsFor, id[1])
		}
	}
	if c.Map == nil || !reflect.DeepEqual(c.Map.Fog, []string{"Stacking with sale prices"}) || !reflect.DeepEqual(c.Map.Decided, []string{"One coupon per order"}) ||
		!reflect.DeepEqual(c.Map.OutOfScope, []string{"Gift cards"}) || !reflect.DeepEqual(c.Map.Destination, []string{"A coupon takes money off an order."}) {
		t.Errorf("map = %+v, want map.md's sections", c.Map)
	}
	// The text form says the same: one row per ticket, the run and the help.
	text := runJig(t, env, fx.RepoDir, "next", "c").Stdout
	for _, want := range []string{
		"prompt: Take c to PR", "tickets[4]{id,title,steps,reach,waits_for}:",
		id[1] + ",Apply a coupon code,cppp,here,-",
		id[3] + ",Receipt shows the coupon,cppp,after-merge," + id[1] + "(merged)",
		id[4] + ",Coupon analytics,cppp,here," + id[2] + "(stacked)",
		"run[2]{wave,tickets}:", "1," + id[1] + "+" + id[2], "2," + id[4], "Ask: Take c to PR",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("jig next c lacks %q:\n%s", want, text)
		}
	}
	// The listing of the store names the chart.
	listing := nextJSON[struct {
		Charts []struct {
			Name        string   `json:"name"`
			Title       string   `json:"title"`
			Tickets     []string `json:"tickets"`
			Ungraduated int      `json:"ungraduated"`
		} `json:"charts"`
	}](t, env, fx.RepoDir)
	if len(listing.Charts) != 1 || listing.Charts[0].Name != "c" || listing.Charts[0].Title != "Coupons at checkout" ||
		!reflect.DeepEqual(listing.Charts[0].Tickets, []string{id[1], id[2], id[3], id[4]}) {
		t.Errorf("the listing's charts = %+v", listing.Charts)
	}

	// 2. Every brief approved: nothing is left to brief, the chart was started,
	// and the run is what it was.
	for _, i := range []int{1, 2, 3, 4} {
		briefed(t, st, id[i])
	}
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if len(c.Briefs) != 0 || !c.Started || c.Prompt != "Continue c" || !c.Ready {
		t.Errorf("briefs approved: briefs %v started %v prompt %q ready %v, want none to brief, started, Continue c", c.Briefs, c.Started, c.Prompt, c.Ready)
	}
	if want := [][]string{{id[1], id[2]}, {id[4]}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("run = %v, want %v", c.Run, want)
	}
	for _, row := range c.Tickets {
		if !reflect.DeepEqual(row.Steps, []string{"done", "current", "pending", "pending"}) || row.Phase != "build" {
			t.Errorf("%s = steps %v phase %s, want the build current", row.ID, row.Steps, row.Phase)
		}
	}

	// 3. Ticket 1 published, its pull request open; 2 and 4 closed as covered.
	// Only 3 is left, behind that merge: the run waits and names the pull
	// request, and the prompt is the one to ask once it merged.
	pr := pullURL(7)
	publishedWith(t, st, id[1], pr)
	env = withPulls(t, env, fixture.GhPull{Number: 7})
	for _, i := range []int{2, 4} {
		if r := runJig(t, env, fx.RepoDir, "close", id[i], "--reason", "covered", "--by", "develdeco/jig#82"); r.Code != 0 {
			t.Fatalf("jig close %s: exit %d\n%s", id[i], r.Code, r.Stdout)
		}
	}
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if c.Ready || c.Code != "CHART_WAITS_MERGE" || c.Prompt != "Continue c" || !strings.Contains(c.Reason, pr) || len(c.Run) != 0 {
		t.Fatalf("pull request open: ready %v code %q prompt %q run %v reason %q, want CHART_WAITS_MERGE naming %s", c.Ready, c.Code, c.Prompt, c.Run, c.Reason, pr)
	}
	if got := c.reachOf(t, id[3]) + "/" + c.reachOf(t, id[1]); got != "after-merge/done" {
		t.Errorf("reach of 3 and 1 = %s, want after-merge/done", got)
	}
	if len(c.Ends) != 1 || c.Ends[0].Ticket != id[3] || c.Ends[0].Blocker != id[1] || c.Ends[0].Kind != "merged" ||
		len(c.Ends[0].PRs) != 1 || c.Ends[0].PRs[0].URL != pr || c.Ends[0].PRs[0].State != "OPEN" {
		t.Errorf("ends = %+v, want %s waiting on %s's open pull request", c.Ends, id[3], id[1])
	}
	text = runJig(t, env, fx.RepoDir, "next", "c").Stdout
	for _, want := range []string{"code: CHART_WAITS_MERGE", "waits[1]{ticket,blocker,kind,pr,state,fact}:", id[3] + "," + id[1] + `,merged,"` + pr + `",OPEN,`, "Merge " + pr + ", then ask: Continue c"} {
		if !strings.Contains(text, want) {
			t.Errorf("jig next c lacks %q:\n%s", want, text)
		}
	}
	// jig status prints the same tickets.
	status := runJig(t, env, fx.RepoDir, "status", "c")
	if status.Code != 0 || !strings.HasPrefix(status.Stdout, "status:\n") || !strings.Contains(status.Stdout, id[3]+",Receipt shows the coupon,dcpp,after-merge,"+id[1]+"(merged)") {
		t.Errorf("jig status c: exit %d\n%s", status.Code, status.Stdout)
	}

	// 4. The pull request merged: ticket 3 builds in this run.
	env = withPulls(t, env, fixture.GhPull{Number: 7, State: "closed", Merged: true})
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if want := [][]string{{id[3]}}; !c.Ready || !reflect.DeepEqual(c.Run, want) || c.Prompt != "Continue c" || c.Code != "" {
		t.Errorf("merged: ready %v run %v prompt %q code %q, want [[%s]] under Continue c", c.Ready, c.Run, c.Prompt, c.Code, id[3])
	}
	if got := c.reachOf(t, id[3]) + "/" + c.reachOf(t, id[1]); got != "here/done" {
		t.Errorf("reach of 3 and 1 = %s, want here/done", got)
	}

	// 5. Ticket 3 published and merged: the chart is done, with nothing to ask.
	pr8 := pullURL(8)
	publishedWith(t, st, id[3], pr8)
	env = withPulls(t, env, fixture.GhPull{Number: 7, State: "closed", Merged: true}, fixture.GhPull{Number: 8, State: "closed", Merged: true})
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if c.Ready || c.Code != "CHART_DONE" || c.Prompt != "" || len(c.Run) != 0 || len(c.Ends) != 0 {
		t.Errorf("published: ready %v code %q prompt %q run %v ends %v, want CHART_DONE with nothing to ask", c.Ready, c.Code, c.Prompt, c.Run, c.Ends)
	}

	// A word that is neither a ticket nor a chart names both kinds; --for asks
	// about a ticket, not a chart.
	r := runJig(t, env, fx.RepoDir, "next", "nosuchname")
	if r.Code != 2 || !strings.Contains(r.Stdout, "no ticket or chart named") || !strings.Contains(r.Stdout, "The charts are c") {
		t.Errorf("jig next nosuchname: exit %d, want a refusal naming tickets and charts\n%s", r.Code, r.Stdout)
	}
	if r := runJig(t, env, fx.RepoDir, "next", "c", "--for", "build"); r.Code != 2 || !strings.Contains(r.Stdout, "is a chart") {
		t.Errorf("jig next c --for build: exit %d, want a refusal\n%s", r.Code, r.Stdout)
	}
}
