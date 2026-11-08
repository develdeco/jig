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
	Named       []string   `json:"named"`
	// WorkspaceOverlaps are the tickets of one wave that build in one workspace.
	WorkspaceOverlaps []struct {
		Repo      string   `json:"repo"`
		Workspace string   `json:"workspace"`
		Tickets   []string `json:"tickets"`
	} `json:"workspace_overlaps"`
	Tickets []struct {
		Pos      int      `json:"pos"`
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Wave     int      `json:"wave"`
		Phase    string   `json:"phase"`
		Steps    []string `json:"steps"`
		Reach    string   `json:"reach"`
		Prompt   string   `json:"prompt"`
		Problem  string   `json:"problem"`
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
	History []struct {
		SHA     string `json:"sha"`
		Time    string `json:"time"`
		Subject string `json:"subject"`
	} `json:"history"`
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

// alsoInBeta gives ticket id a second slice in workspace beta, so the project's
// slices name two workspaces and the workspace rule has something to say: with
// one workspace in the whole project it lists nothing.
func alsoInBeta(t *testing.T, st *store.Store, id string) {
	t.Helper()
	path := filepath.Join(st.TicketDir(id), "slices.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, path, append(data, []byte("  - id: b\n    workspace: beta\n    goal: Build it too.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n")...))
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

// TestChartSkillNesting runs the built binary and reads the chart skill a
// session gets: it carries the rule for a multi-repo ticket (a nested builder
// per repo) and the fallback when the ticket subagent has no Agent tool.
func TestChartSkillNesting(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	r := runJig(t, env, fx.RepoDir, "skill", "chart")
	if r.Code != 0 {
		t.Fatalf("jig skill chart exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{"nested builder", "in the background", "only its repo's slices, never gate or nest", "a hold saying to gate is yours", "wait for them before the gate", "the question, its id and default", "No Agent tool", "if you have the Agent tool", "A hand-back at green with no reviewer"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig skill chart lacks %q:\n%s", want, r.Stdout)
		}
	}
}

// TestChartWorkspaceOverlapBecomesAStackedEdge: two independent tickets of a
// chart that build in one workspace are listed as an overlap by `jig next
// <chart>` and `jig status <chart>`; once the later one is stacked on the earlier
// in its ticket.yaml and `jig validate` passes it, the list is empty and the
// later ticket is in the next wave. `jig next` with no ticket keeps its own
// warning for the two.
func TestChartWorkspaceOverlapBecomesAStackedEdge(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	chartDir := filepath.Join(fx.StoreDir, "charts", "c")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(chartDir, "map.md"), []byte("# Two in one workspace\n\n## Destination\n\nBoth change the same code.\n"))
	writeFileOrFatal(t, filepath.Join(chartDir, "tickets.yaml"), []byte("tickets:\n  - title: First\n  - title: Second\n"))
	if r := runJig(t, env, fx.StoreDir, "graduate", "c"); r.Code != 0 {
		t.Fatalf("jig graduate c: exit %d\n%s", r.Code, r.Stdout)
	}
	c := nextJSON[chartOut](t, env, fx.RepoDir, "c")
	id := map[int]string{}
	for _, row := range c.Tickets {
		id[row.Pos] = row.ID
	}
	briefed(t, st, id[1])
	briefed(t, st, id[2])
	alsoInBeta(t, st, id[1])

	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if want := [][]string{{id[1], id[2]}}; !reflect.DeepEqual(c.Run, want) {
		t.Fatalf("run = %v, want one wave %v", c.Run, want)
	}
	if len(c.WorkspaceOverlaps) != 1 || c.WorkspaceOverlaps[0].Repo != "fixture-repo" || c.WorkspaceOverlaps[0].Workspace != "alpha" ||
		!reflect.DeepEqual(c.WorkspaceOverlaps[0].Tickets, []string{id[1], id[2]}) {
		t.Fatalf("workspace_overlaps = %+v, want fixture-repo alpha %s+%s in Plan order", c.WorkspaceOverlaps, id[1], id[2])
	}
	for _, verb := range []string{"next", "status"} {
		out := runJig(t, env, fx.RepoDir, verb, "c").Stdout
		for _, want := range []string{
			"warning: tickets of one wave build in the same workspace\n",
			"workspace_overlap[1]{repo,workspace,tickets}:\n  fixture-repo,alpha," + id[1] + "+" + id[2] + "\n",
			"Stack the later ticket",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("jig %s c lacks %q:\n%s", verb, want, out)
			}
		}
	}

	// jig next with no ticket keeps its own warning while both are ready.
	out := runJig(t, env, fx.RepoDir, "next").Stdout
	for _, want := range []string{"warning: ready tickets build in the same workspace\n", "fixture-repo,alpha," + id[1] + "+" + id[2] + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig next lacks %q:\n%s", want, out)
		}
	}

	// The session stacks the later ticket on the earlier, in its own file.
	yaml := filepath.Join(st.TicketDir(id[2]), "ticket.yaml")
	data, err := os.ReadFile(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte("blocked_by:\n  - ticket: "+id[1]+"\n    kind: stacked\n")...)
	writeFileOrFatal(t, yaml, data)
	if r := runJig(t, env, fx.RepoDir, "validate", id[2], "--check"); r.Code != 0 || !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("jig validate %s --check: exit %d\n%s", id[2], r.Code, r.Stdout)
	}

	r := runJig(t, env, fx.RepoDir, "next", "c", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"workspace_overlaps":[]`) {
		t.Fatalf("jig next c --json after the edge: exit %d, want the empty list present\n%s", r.Code, r.Stdout)
	}
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if want := [][]string{{id[1]}, {id[2]}}; !reflect.DeepEqual(c.Run, want) || len(c.WorkspaceOverlaps) != 0 {
		t.Errorf("after the edge: run %v overlaps %+v, want %v and none", c.Run, c.WorkspaceOverlaps, want)
	}
	for _, row := range c.Tickets {
		if row.ID != id[2] {
			continue
		}
		if len(row.WaitsFor) != 1 || row.WaitsFor[0].Ticket != id[1] || row.WaitsFor[0].Kind != "stacked" {
			t.Errorf("%s waits for %+v, want %s stacked", id[2], row.WaitsFor, id[1])
		}
	}
	if out := runJig(t, env, fx.RepoDir, "next", "c").Stdout; strings.Contains(out, "workspace_overlap") {
		t.Errorf("jig next c after the edge still says overlap:\n%s", out)
	}
}

// TestChartTicketsSubset: a chart of three tickets (1 and 2 independent, 3
// stacked on 2) read with --tickets lists only the named waves, overlaps and
// prompt; a named ticket behind the unnamed one waits on it in the ends; a name
// outside the chart is refused with the chart's tickets listed; and the whole
// chart's standing is as it was.
func TestChartTicketsSubset(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	chartDir := filepath.Join(fx.StoreDir, "charts", "c")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(chartDir, "map.md"), []byte("# Three tickets\n\n## Destination\n\nTwo run apart, one rides on the second.\n"))
	writeFileOrFatal(t, filepath.Join(chartDir, "tickets.yaml"), []byte("tickets:\n  - title: First\n  - title: Second\n  - title: Third\n    blocked_by:\n      - ref: \"#2\"\n        kind: stacked\n"))
	if r := runJig(t, env, fx.StoreDir, "graduate", "c"); r.Code != 0 {
		t.Fatalf("jig graduate c: exit %d\n%s", r.Code, r.Stdout)
	}
	id := map[int]string{}
	for _, row := range nextJSON[chartOut](t, env, fx.RepoDir, "c").Tickets {
		id[row.Pos] = row.ID
	}
	for pos := 1; pos <= 3; pos++ {
		briefed(t, st, id[pos])
	}
	alsoInBeta(t, st, id[1])
	rowsOf := func(c chartOut) []string {
		out := []string{}
		for _, row := range c.Tickets {
			out = append(out, row.ID)
		}
		return out
	}

	// 1 and 3: only they are rows, 3 waits for the unnamed 2.
	c := nextJSON[chartOut](t, env, fx.RepoDir, "c", "--tickets", id[1]+","+id[3])
	if want := []string{id[1], id[3]}; !reflect.DeepEqual(c.Named, want) || !reflect.DeepEqual(rowsOf(c), want) {
		t.Fatalf("named %v rows %v, want %v", c.Named, rowsOf(c), want)
	}
	if want := [][]string{{id[1]}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("run = %v, want %v", c.Run, want)
	}
	if len(c.WorkspaceOverlaps) != 0 {
		t.Errorf("workspace_overlaps = %+v, want none: %s is not in the run", c.WorkspaceOverlaps, id[2])
	}
	if len(c.Ends) != 1 || c.Ends[0].Ticket != id[3] || c.Ends[0].Blocker != id[2] || c.Ends[0].Kind != "stacked" {
		t.Errorf("ends = %+v, want %s behind %s, stacked", c.Ends, id[3], id[2])
	}
	if !c.Ready || c.Code != "" || c.Prompt != "Continue "+id[1]+" and "+id[3] {
		t.Errorf("ready %v code %q prompt %q, want ready with the tickets' prompt", c.Ready, c.Code, c.Prompt)
	}
	text := runJig(t, env, fx.RepoDir, "next", "c", "--tickets", id[1]+","+id[3]).Stdout
	for _, want := range []string{"named: " + id[1] + "+" + id[3], "waits[1]", id[2]} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "title Second") || strings.Contains(text, "Second,") {
		t.Errorf("text lists the unnamed ticket as a row:\n%s", text)
	}

	// 1 and 2: one wave, the overlap of that pair, no end.
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c", "--tickets", id[1]+","+id[2])
	if want := [][]string{{id[1], id[2]}}; !reflect.DeepEqual(c.Run, want) || len(c.Ends) != 0 {
		t.Errorf("run %v ends %+v, want %v and none", c.Run, c.Ends, want)
	}
	if len(c.WorkspaceOverlaps) != 1 || c.WorkspaceOverlaps[0].Workspace != "alpha" || !reflect.DeepEqual(c.WorkspaceOverlaps[0].Tickets, []string{id[1], id[2]}) {
		t.Errorf("workspace_overlaps = %+v, want the pair %s+%s in alpha", c.WorkspaceOverlaps, id[1], id[2])
	}

	// 3 alone: nothing to start, the blocker is named, the prompt is the ticket's.
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c", "--tickets", id[3])
	if c.Ready || c.Code != "CHART_BLOCKER_NOT_NAMED" || !strings.Contains(c.Reason, id[2]) {
		t.Errorf("ready %v code %q reason %q, want CHART_BLOCKER_NOT_NAMED naming %s", c.Ready, c.Code, c.Reason, id[2])
	}
	if !strings.Contains(c.Prompt, id[3]) || strings.HasPrefix(c.Prompt, "Take ") {
		t.Errorf("prompt = %q, want %s's own ticket prompt", c.Prompt, id[3])
	}
	if out := runJig(t, env, fx.RepoDir, "next", "c", "--tickets", id[3]).Stdout; !strings.Contains(out, "Name "+id[2]+" too, or take "+id[2]+" first") {
		t.Errorf("text lacks the help naming %s:\n%s", id[2], out)
	}

	// A name outside the chart is refused, with the chart's tickets and no standing.
	r := runJig(t, env, fx.RepoDir, "next", "c", "--tickets", id[1]+",ZZ-9")
	for _, want := range []string{"VALIDATION_ERROR", "ZZ-9", id[1], id[2], id[3]} {
		if r.Code != 2 || !strings.Contains(r.Stdout, want) {
			t.Errorf("exit %d, output lacks %q:\n%s", r.Code, want, r.Stdout)
		}
	}
	if strings.Contains(r.Stdout, "ready:") {
		t.Errorf("a refusal printed a standing:\n%s", r.Stdout)
	}

	// The whole chart is as it was.
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if len(c.Tickets) != 3 || c.Named != nil || c.Prompt != "Continue c" {
		t.Errorf("whole chart: rows %d named %v prompt %q, want 3, none, Continue c", len(c.Tickets), c.Named, c.Prompt)
	}
}
