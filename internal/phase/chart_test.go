package phase

import (
	"reflect"
	"strings"
	"testing"
)

// ticketAt is a ticket whose brief is approved and built nothing yet (the build
// is its phase), with the blockers the reader found unmet.
func ticketAt(id string, blockers ...Blocker) *Ticket {
	f := approved()
	f.Ticket, f.Blockers = id, blockers
	t := Derive(f, nil)
	return &t
}

// ticketBriefing is a ticket with nothing yet: its brief is the phase.
func ticketBriefing(id string) *Ticket {
	t := Derive(Facts{Ticket: id}, nil)
	return &t
}

// ticketPublished is a published ticket.
func ticketPublished(id string) *Ticket {
	f := allGreen()
	f.Ticket, f.Published = id, true
	t := Derive(f, nil)
	return &t
}

// entry is a graduated chart entry at position pos whose ticket stands at t.
func entry(pos int, id string, t *Ticket, needs ...ChartNeed) ChartEntry {
	return ChartEntry{Pos: pos, ID: id, Title: "title " + id, Ticket: t, Needs: needs}
}

func mergedOn(blocker string, met bool, prs ...MergePR) ChartNeed {
	n := ChartNeed{Ticket: blocker, Kind: "merged", Met: met, PRs: prs}
	if !met {
		n.Code, n.Why = "CLAIM_BLOCKED_MERGED", "waits for "+blocker+", which has not merged"
	}
	return n
}

func stackedOn(blocker string) ChartNeed {
	return ChartNeed{Ticket: blocker, Kind: "stacked", Code: "CLAIM_STACKED_PARENT_NOT_CLEAN", Why: "stacked on " + blocker + ", whose gate has no clean round yet"}
}

func ids(rows []ChartRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func reachOfRow(t *testing.T, c Chart, id string) string {
	t.Helper()
	for _, r := range c.Rows {
		if r.ID == id {
			return r.Reach
		}
	}
	t.Fatalf("no row %s in %v", id, ids(c.Rows))
	return ""
}

func rankOf(n int) *int { return &n }

// TestChartTakesIndependentTicketsInOneWave: two entries with no edge run
// together, in rank order (the unranked last), under the "Take" prompt while
// nothing was started.
func TestChartTakesIndependentTicketsInOneWave(t *testing.T) {
	t.Parallel()
	a, b := entry(1, "T-1", ticketBriefing("T-1")), entry(2, "T-2", ticketBriefing("T-2"))
	b.Rank = rankOf(2)
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{a, b}})
	if got := ids(c.Rows); !reflect.DeepEqual(got, []string{"T-2", "T-1"}) {
		t.Errorf("row order = %v, want the ranked ticket first", got)
	}
	if want := [][]string{{"T-2", "T-1"}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("Run = %v, want %v", c.Run, want)
	}
	if !c.Ready || c.Code != "" || c.Prompt != "Take c to PR" || c.Started {
		t.Errorf("verdict = ready %v code %q prompt %q started %v, want ready, Take, not started", c.Ready, c.Code, c.Prompt, c.Started)
	}
	if want := []string{"T-2", "T-1"}; !reflect.DeepEqual(c.Briefs, want) {
		t.Errorf("Briefs = %v, want %v", c.Briefs, want)
	}
}

// TestChartBriefsEveryReachableTicketFirst: a ticket at its brief is listed to
// brief whether it builds here or after a merge, and a chart with only briefs
// to do is ready; a started chart is told to continue.
func TestChartBriefsEveryReachableTicketFirst(t *testing.T) {
	t.Parallel()
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketBriefing("T-1")),
		entry(2, "T-2", ticketBriefing("T-2"), mergedOn("T-1", false)),
		entry(3, "T-3", ticketAt("T-3")),
	}})
	if want := []string{"T-1", "T-2"}; !reflect.DeepEqual(c.Briefs, want) {
		t.Errorf("Briefs = %v, want %v", c.Briefs, want)
	}
	if want := [][]string{{"T-1", "T-3"}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("Run = %v, want %v", c.Run, want)
	}
	if !c.Started || c.Prompt != "Continue c" {
		t.Errorf("started %v prompt %q: T-3's brief is done, so the chart was started", c.Started, c.Prompt)
	}

	onlyBriefs := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketBriefing("T-1")),
		entry(2, "T-2", ticketBriefing("T-2"), mergedOn("T-1", false)),
	}})
	if !onlyBriefs.Ready || len(onlyBriefs.Briefs) != 2 || onlyBriefs.Prompt != "Take c to PR" {
		t.Errorf("a chart of briefs = %+v, want ready under Take", onlyBriefs)
	}
}

// TestChartRunEndsAtAMergedEdge: B needs A merged and A has not published: A
// builds in this run, B is after its merge, and the end names both.
func TestChartRunEndsAtAMergedEdge(t *testing.T) {
	t.Parallel()
	blocked := Blocker{Ticket: "T-1", Kind: "merged", Reason: "T-2 waits for T-1", Code: "CLAIM_BLOCKED_MERGED", Prompt: "Build T-1"}
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketAt("T-1")),
		entry(2, "T-2", ticketAt("T-2", blocked), mergedOn("T-1", false)),
	}})
	if got := reachOfRow(t, c, "T-1") + "/" + reachOfRow(t, c, "T-2"); got != "here/after-merge" {
		t.Errorf("reach = %s, want here/after-merge", got)
	}
	if want := [][]string{{"T-1"}}; !reflect.DeepEqual(c.Run, want) || !c.Ready {
		t.Errorf("Run = %v ready %v, want [[T-1]] ready", c.Run, c.Ready)
	}
	if len(c.Ends) != 1 || c.Ends[0].ID != "T-2" || c.Ends[0].Because.Ticket != "T-1" {
		t.Errorf("Ends = %+v, want T-2 waiting on T-1", c.Ends)
	}
	// The held row asks nothing of its own: the chart's prompt covers it.
	for _, r := range c.Rows {
		if r.ID == "T-2" && r.Prompt() != "" {
			t.Errorf("T-2's prompt = %q, want none while it waits for a merge", r.Prompt())
		}
		if r.ID == "T-1" && r.Prompt() != "Build T-1" {
			t.Errorf("T-1's prompt = %q, want Build T-1", r.Prompt())
		}
	}
}

// TestChartWaitsForAnOpenPullRequest: A published and its PR is open: nothing
// can start, the reason names the PR, and the prompt is the one to ask once it
// merged. When it merged the dependent builds here, under "Continue".
func TestChartWaitsForAnOpenPullRequest(t *testing.T) {
	t.Parallel()
	open := MergePR{Repo: "shop", URL: "https://example.test/shop/pull/7", State: "OPEN"}
	blocked := Blocker{Ticket: "T-1", Kind: "merged", Reason: "T-2 waits for T-1", Code: "CLAIM_BLOCKED_MERGED", PRs: []MergePR{open}}
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketPublished("T-1")),
		entry(2, "T-2", ticketAt("T-2", blocked), mergedOn("T-1", false, open)),
	}})
	if c.Ready || c.Code != CodeChartWaitsMerge || c.Prompt != "Continue c" {
		t.Fatalf("verdict = ready %v code %q prompt %q, want waits-merge under Continue", c.Ready, c.Code, c.Prompt)
	}
	if !strings.Contains(c.Reason, open.URL) || !strings.Contains(c.Reason, "OPEN") || !strings.Contains(c.Reason, "T-2 waits for T-1") {
		t.Errorf("Reason = %q, want the dependent, the blocker and the PR with its state", c.Reason)
	}
	if len(c.Ends) != 1 || len(c.Ends[0].Because.PRs) != 1 || c.Ends[0].Because.PRs[0].URL != open.URL {
		t.Errorf("Ends = %+v, want the PR on the end", c.Ends)
	}
	if reachOfRow(t, c, "T-1") != ReachDone {
		t.Errorf("a published ticket's reach is done")
	}

	merged := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketPublished("T-1")),
		entry(2, "T-2", ticketAt("T-2"), mergedOn("T-1", true)),
	}})
	if want := [][]string{{"T-2"}}; !reflect.DeepEqual(merged.Run, want) || !merged.Started || merged.Prompt != "Continue c" || merged.Code != "" {
		t.Errorf("after the merge: Run %v started %v prompt %q code %q, want [[T-2]] started under Continue", merged.Run, merged.Started, merged.Prompt, merged.Code)
	}
}

// TestChartBuildsAStackedTicketInALaterWave: a ticket stacked on one that
// builds here builds here too, in the wave after it; independent tickets share
// the parent's wave.
func TestChartBuildsAStackedTicketInALaterWave(t *testing.T) {
	t.Parallel()
	entries := func(extra ...ChartEntry) []ChartEntry {
		return append([]ChartEntry{
			entry(1, "T-1", ticketAt("T-1")),
			entry(3, "T-3", ticketAt("T-3", Blocker{Ticket: "T-1", Kind: "stacked", Code: "CLAIM_STACKED_PARENT_NOT_CLEAN", Reason: "stacked"}), stackedOn("T-1")),
		}, extra...)
	}
	c := DeriveChart(ChartFacts{Name: "c", Entries: entries()})
	if want := [][]string{{"T-1"}, {"T-3"}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("Run = %v, want %v", c.Run, want)
	}
	if reachOfRow(t, c, "T-3") != ReachHere {
		t.Errorf("a stacked ticket on one that builds here is here")
	}
	for _, r := range c.Rows {
		// The build is held on its parent's gate, but this run builds it: it asks
		// for its own phase, not its parent's.
		if r.ID == "T-3" && r.Prompt() != "Build T-3" {
			t.Errorf("T-3's prompt = %q, want Build T-3", r.Prompt())
		}
	}
	c = DeriveChart(ChartFacts{Name: "c", Entries: entries(entry(2, "T-2", ticketAt("T-2")))})
	if want := [][]string{{"T-1", "T-2"}, {"T-3"}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("Run = %v, want %v", c.Run, want)
	}
}

// TestChartAfterAMergeIsTransitiveButNotAnEnd: a ticket stacked on one that
// waits for a merge waits too, but only the first one is where the run ends.
func TestChartAfterAMergeIsTransitiveButNotAnEnd(t *testing.T) {
	t.Parallel()
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketAt("T-1")),
		entry(2, "T-2", ticketAt("T-2"), mergedOn("T-1", false)),
		entry(3, "T-3", ticketAt("T-3"), stackedOn("T-2")),
	}})
	if got := reachOfRow(t, c, "T-3"); got != ReachAfterMerge {
		t.Errorf("T-3 reach = %s, want after-merge", got)
	}
	if len(c.Ends) != 1 || c.Ends[0].ID != "T-2" {
		t.Errorf("Ends = %+v, want only T-2", c.Ends)
	}
	if want := [][]string{{"T-1"}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("Run = %v, want [[T-1]]", c.Run)
	}
}

// TestChartDoneAndBlocked: a chart of published tickets is done, with nothing
// to ask; a blocker closed as covered meets the need, one not planned never
// does, and the chart then says why it is blocked.
func TestChartDoneAndBlocked(t *testing.T) {
	t.Parallel()
	done := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketPublished("T-1")),
		entry(2, "T-2", ticketPublished("T-2"), mergedOn("T-1", true)),
	}})
	if done.Ready || done.Code != CodeChartDone || done.Prompt != "" || done.Reason != "" {
		t.Errorf("done chart = ready %v code %q prompt %q reason %q", done.Ready, done.Code, done.Prompt, done.Reason)
	}

	closed := func(reason string) *Ticket {
		tk := Derive(Facts{Ticket: "T-1", Closed: reason}, nil)
		return &tk
	}
	covered := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", closed("covered by x/y#1")),
		entry(2, "T-2", ticketAt("T-2"), mergedOn("T-1", true)),
	}})
	if want := [][]string{{"T-2"}}; !reflect.DeepEqual(covered.Run, want) || reachOfRow(t, covered, "T-1") != ReachDone {
		t.Errorf("covered closure: Run %v, want [[T-2]] with the closed ticket done", covered.Run)
	}

	notPlanned := ChartNeed{Ticket: "T-1", Kind: "merged", Code: "CLAIM_BLOCKER_NOT_PLANNED", Why: "T-2 waits for T-1, which was closed as not planned"}
	blocked := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", closed("not planned")),
		entry(2, "T-2", ticketAt("T-2"), notPlanned),
		entry(3, "T-3", ticketAt("T-3"), stackedOn("T-2")),
	}})
	if blocked.Ready || blocked.Code != CodeChartBlocked || blocked.Prompt != "" {
		t.Fatalf("blocked chart = ready %v code %q prompt %q", blocked.Ready, blocked.Code, blocked.Prompt)
	}
	if blocked.Reason != notPlanned.Why {
		t.Errorf("Reason = %q, want the first blocked row's: %q", blocked.Reason, notPlanned.Why)
	}
	if got := reachOfRow(t, blocked, "T-3"); got != ReachBlocked {
		t.Errorf("a ticket stacked on a blocked one is blocked, got %s", got)
	}
}

// TestChartNeedsOutsideTheChart: a merged need on a ticket outside waits for its
// merge, a stacked one outside can never be built by this run, and a blocker
// that is no ticket never resolves.
func TestChartNeedsOutsideTheChart(t *testing.T) {
	t.Parallel()
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketAt("T-1"), mergedOn("X-9", false)),
		entry(2, "T-2", ticketAt("T-2"), stackedOn("X-9")),
		entry(3, "T-3", ticketAt("T-3"), ChartNeed{Ticket: "X-8", Kind: "merged", Code: CodeNeedUnknown, Why: "X-8 is not a ticket of the store"}),
	}})
	for id, want := range map[string]string{"T-1": ReachAfterMerge, "T-2": ReachBlocked, "T-3": ReachBlocked} {
		if got := reachOfRow(t, c, id); got != want {
			t.Errorf("%s reach = %s, want %s", id, got, want)
		}
	}
	if c.Ready || c.Code != CodeChartWaitsMerge {
		t.Errorf("verdict = ready %v code %q, want waits-merge (T-1 is a merge away)", c.Ready, c.Code)
	}
}

// TestChartWithEntriesThatHaveNoTicketYet: an entry with no id gets a reach and
// is counted, but is never in the run; a chart with none graduated is not
// ready, and one with some says how many are missing.
func TestChartWithEntriesThatHaveNoTicketYet(t *testing.T) {
	t.Parallel()
	none := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		{Pos: 1, Title: "one"},
		{Pos: 2, Title: "two", Needs: []ChartNeed{{Pos: 1, Kind: "merged", Why: "waits for entry #1, which has no ticket yet"}}},
	}})
	if none.Ready || none.Code != CodeChartNotGraduated || none.Prompt != "" || none.Ungraduated != 2 || none.Reason != "run `jig graduate c`" {
		t.Errorf("nothing graduated = ready %v code %q prompt %q ungraduated %d reason %q", none.Ready, none.Code, none.Prompt, none.Ungraduated, none.Reason)
	}
	if none.Rows[0].Reach != ReachHere || none.Rows[1].Reach != ReachAfterMerge {
		t.Errorf("reach of entries with no id = %s, %s; want here, after-merge", none.Rows[0].Reach, none.Rows[1].Reach)
	}
	if len(none.Run) != 0 || len(none.Briefs) != 0 {
		t.Errorf("an entry with no ticket is no part of a run: Run %v Briefs %v", none.Run, none.Briefs)
	}

	some := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketBriefing("T-1")),
		{Pos: 2, Title: "later"},
	}})
	if !some.Ready || some.Ungraduated != 1 || some.Prompt != "Take c to PR" || some.Reason != "1 entry has no ticket yet" {
		t.Errorf("partly graduated = ready %v ungraduated %d prompt %q reason %q", some.Ready, some.Ungraduated, some.Prompt, some.Reason)
	}

	finished := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketPublished("T-1")),
		{Pos: 2, Title: "later"},
	}})
	if finished.Code != CodeChartNotGraduated || !strings.Contains(finished.Reason, "1 entry has no ticket yet") {
		t.Errorf("all graduated done, one missing = code %q reason %q, want not graduated", finished.Code, finished.Reason)
	}
}

// TestChartCycleIsBlockedAndLast: two entries that wait for each other are
// blocked, in wave -1, after every other row.
func TestChartCycleIsBlockedAndLast(t *testing.T) {
	t.Parallel()
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketAt("T-1"), stackedOn("T-2")),
		entry(2, "T-2", ticketAt("T-2"), stackedOn("T-1")),
		entry(3, "T-3", ticketAt("T-3")),
	}})
	if got := ids(c.Rows); !reflect.DeepEqual(got, []string{"T-3", "T-1", "T-2"}) {
		t.Errorf("row order = %v, want the cycle last", got)
	}
	for _, r := range c.Rows {
		wantWave, wantReach := -1, ReachBlocked
		if r.ID == "T-3" {
			wantWave, wantReach = 0, ReachHere
		}
		if r.Wave != wantWave || r.Reach != wantReach {
			t.Errorf("%s = wave %d reach %s, want %d %s", r.ID, r.Wave, r.Reach, wantWave, wantReach)
		}
	}
	if want := [][]string{{"T-3"}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("Run = %v, want [[T-3]]", c.Run)
	}
}

// TestChartWaveCountsEveryEdge: the wave is the longest chain of in-chart
// blockers over met edges as well as unmet ones, while the run's waves count
// only what is still to build.
func TestChartWaveCountsEveryEdge(t *testing.T) {
	t.Parallel()
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		entry(1, "T-1", ticketPublished("T-1")),
		entry(2, "T-2", ticketAt("T-2"), mergedOn("T-1", true)),
		entry(3, "T-3", ticketAt("T-3"), stackedOn("T-2"), mergedOn("T-1", true)),
	}})
	want := map[string]int{"T-1": 0, "T-2": 1, "T-3": 2}
	for _, r := range c.Rows {
		if r.Wave != want[r.ID] {
			t.Errorf("%s wave = %d, want %d", r.ID, r.Wave, want[r.ID])
		}
	}
	if wantRun := [][]string{{"T-2"}, {"T-3"}}; !reflect.DeepEqual(c.Run, wantRun) {
		t.Errorf("Run = %v, want %v", c.Run, wantRun)
	}
}

// TestChartRowWithAProblemDoesNotHideTheChart: a ticket that could not be read
// is a blocked row with its problem, and the tickets that wait for it are
// blocked behind it; the others still run.
func TestChartRowWithAProblemDoesNotHideTheChart(t *testing.T) {
	t.Parallel()
	broken := ChartEntry{Pos: 1, ID: "T-1", Title: "broken", Problem: "slices.yaml does not parse"}
	c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{
		broken,
		entry(2, "T-2", ticketAt("T-2"), mergedOn("T-1", false)),
		entry(3, "T-3", ticketAt("T-3")),
	}})
	if got := reachOfRow(t, c, "T-1") + "/" + reachOfRow(t, c, "T-2") + "/" + reachOfRow(t, c, "T-3"); got != "blocked/blocked/here" {
		t.Errorf("reach = %s, want blocked/blocked/here", got)
	}
	if want := [][]string{{"T-3"}}; !reflect.DeepEqual(c.Run, want) {
		t.Errorf("Run = %v, want [[T-3]]", c.Run)
	}
	for _, r := range c.Rows {
		if r.ID == "T-1" && r.Problem != broken.Problem {
			t.Errorf("the row lost its problem: %+v", r)
		}
	}
}

// TestChartPromptsAndNames: the two prompts that run a chart.
func TestChartPromptsAndNames(t *testing.T) {
	t.Parallel()
	if got := TakePrompt("uploads"); got != "Take uploads to PR" {
		t.Errorf("TakePrompt = %q", got)
	}
	if got := ContinuePrompt("uploads"); got != "Continue uploads" {
		t.Errorf("ContinuePrompt = %q", got)
	}
}

// TestTicketKeepsTheBlockersTheFactsNamed: the blockers a claim would refuse
// for come back with their code and the pull requests the merge waits on.
func TestTicketKeepsTheBlockersTheFactsNamed(t *testing.T) {
	t.Parallel()
	pr := MergePR{Repo: "shop", URL: "https://example.test/pull/1", State: "OPEN"}
	b := Blocker{Ticket: "T-1", Kind: "merged", Reason: "waits", Prompt: "Build T-1", Code: "CLAIM_BLOCKED_MERGED", PRs: []MergePR{pr}}
	if got := ticketAt("T-2", b).Blockers(); !reflect.DeepEqual(got, []Blocker{b}) {
		t.Errorf("Blockers() = %+v, want %+v", got, b)
	}
	if got := ticketAt("T-2").Blockers(); len(got) != 0 {
		t.Errorf("a ticket with no blockers has %+v", got)
	}
	if got := (Ticket{}).Blockers(); got != nil {
		t.Errorf("the zero Ticket has blockers %+v", got)
	}
}
