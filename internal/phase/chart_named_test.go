package phase

import (
	"reflect"
	"strings"
	"testing"
)

// endsOf lists each end of a chart as "ticket>blocker(kind)".
func endsOf(c Chart) []string {
	out := []string{}
	for _, e := range c.Ends {
		out = append(out, e.ID+">"+e.Because.Ticket+"("+e.Because.Kind+")")
	}
	return out
}

// TestDeriveChartNamed: a run over the named tickets lists those rows only, in
// waves of their own, and an unmet need on an unnamed ticket that is not done is
// an end that names it.
func TestDeriveChartNamed(t *testing.T) {
	t.Parallel()

	// T-1 and T-2 are independent; T-3 waits on T-2, by the given need.
	chart := func(on func(string) ChartNeed, named ...string) Chart {
		return DeriveChart(ChartFacts{Name: "c", Named: named, Entries: []ChartEntry{
			entry(1, "T-1", ticketBriefing("T-1")),
			entry(2, "T-2", ticketBriefing("T-2")),
			entry(3, "T-3", ticketBriefing("T-3"), on("T-2")),
		}})
	}
	merged := func(b string) ChartNeed { return mergedOn(b, false) }
	const sentence = "T-3 waits for T-2, which this run does not take and is not done"

	for name, on := range map[string]func(string) ChartNeed{"stacked": stackedOn, "merged": merged} {
		on := on
		t.Run("a named ticket behind an unnamed one ends at it, "+name, func(t *testing.T) {
			t.Parallel()
			c := chart(on, "T-1", "T-3")
			if got := ids(c.Rows); !reflect.DeepEqual(got, []string{"T-1", "T-3"}) {
				t.Errorf("rows = %v, want the named tickets only", got)
			}
			if want := [][]string{{"T-1"}}; !reflect.DeepEqual(c.Run, want) {
				t.Errorf("Run = %v, want %v", c.Run, want)
			}
			if want := []string{"T-1", "T-3"}; !reflect.DeepEqual(c.Briefs, want) {
				t.Errorf("Briefs = %v, want %v", c.Briefs, want)
			}
			if got := reachOfRow(t, c, "T-3"); got != ReachAfterMerge {
				t.Errorf("T-3 reach = %s, want after-merge whatever the edge's kind", got)
			}
			wantEnd := []string{"T-3>T-2(" + name + ")"}
			if got := endsOf(c); !reflect.DeepEqual(got, wantEnd) {
				t.Errorf("ends = %v, want %v", got, wantEnd)
			}
			if c.Ends[0].Because.Code != CodeNeedNotNamed || c.Ends[0].Because.Why != sentence {
				t.Errorf("end = %+v, want code %s and %q", c.Ends[0].Because, CodeNeedNotNamed, sentence)
			}
			if !c.Ready || c.Code != "" || c.Prompt != "Take T-1 and T-3 to PR" || !reflect.DeepEqual(c.Named, []string{"T-1", "T-3"}) {
				t.Errorf("verdict = ready %v code %q prompt %q named %v, want ready with the tickets' Take prompt", c.Ready, c.Code, c.Prompt, c.Named)
			}
			if got := (&chartDeriver{}).waitsReason(c.Ends[0]); got != sentence {
				t.Errorf("waitsReason = %q, want %q", got, sentence)
			}
		})
	}

	t.Run("nothing to start and only unnamed blockers is its own verdict", func(t *testing.T) {
		t.Parallel()
		c := DeriveChart(ChartFacts{Name: "c", Named: []string{"T-3", "T-4"}, Entries: []ChartEntry{
			entry(2, "T-2", ticketAt("T-2")),
			entry(3, "T-3", ticketAt("T-3"), stackedOn("T-2")),
			entry(4, "T-4", ticketAt("T-4"), mergedOn("T-2", false)),
		}})
		if c.Ready || c.Code != CodeNeedNotNamed {
			t.Fatalf("ready %v code %q, want not ready, %s", c.Ready, c.Code, CodeNeedNotNamed)
		}
		if want := "T-3 waits for T-2, which this run does not take and is not done"; c.Reason != want {
			t.Errorf("reason = %q, want %q", c.Reason, want)
		}
		if c.Prompt != "Continue T-3 and T-4" {
			t.Errorf("prompt = %q, want the run's Continue prompt once started", c.Prompt)
		}
	})

	t.Run("a merge wait beside it keeps the merge verdict", func(t *testing.T) {
		t.Parallel()
		pr := MergePR{Repo: "app", URL: "https://example.test/pr/1", State: "OPEN"}
		c := DeriveChart(ChartFacts{Name: "c", Named: []string{"T-3", "T-4"}, Entries: []ChartEntry{
			entry(2, "T-2", ticketAt("T-2")),
			entry(3, "T-3", ticketAt("T-3"), stackedOn("T-2")),
			entry(4, "T-4", ticketAt("T-4"), mergedOn("X-9", false, pr)),
		}})
		if c.Ready || c.Code != CodeChartWaitsMerge || !strings.Contains(c.Reason, "T-4 waits for X-9 to merge") {
			t.Errorf("ready %v code %q reason %q, want the merge wait", c.Ready, c.Code, c.Reason)
		}
		if len(c.Ends) != 2 {
			t.Errorf("ends = %v, want both", endsOf(c))
		}
	})

	t.Run("one named ticket has its own prompt", func(t *testing.T) {
		t.Parallel()
		c := DeriveChart(ChartFacts{Name: "c", Named: []string{"T-3"}, Entries: []ChartEntry{
			entry(2, "T-2", ticketAt("T-2")),
			entry(3, "T-3", ticketAt("T-3"), stackedOn("T-2")),
		}})
		if c.Code != CodeNeedNotNamed || c.Prompt != Prompt(c.Rows[0].Ticket.Current, "T-3") {
			t.Errorf("code %q prompt %q, want %s and the ticket's own prompt", c.Code, c.Prompt, CodeNeedNotNamed)
		}
		one := chart(stackedOn, "T-1")
		if !one.Ready || one.Prompt != Prompt(one.Rows[0].Ticket.Current, "T-1") {
			t.Errorf("ready %v prompt %q, want the ticket's own prompt", one.Ready, one.Prompt)
		}
	})

	t.Run("an unnamed blocker that is done keeps today's rules", func(t *testing.T) {
		t.Parallel()
		pr := MergePR{Repo: "app", URL: "https://example.test/pr/2", State: "OPEN"}
		c := DeriveChart(ChartFacts{Name: "c", Named: []string{"T-1", "T-3"}, Entries: []ChartEntry{
			entry(1, "T-1", ticketBriefing("T-1")),
			entry(2, "T-2", ticketPublished("T-2")),
			entry(3, "T-3", ticketBriefing("T-3"), mergedOn("T-2", false, pr)),
			entry(4, "T-4", ticketBriefing("T-4"), mergedOn("T-1", true)),
		}})
		if got := endsOf(c); !reflect.DeepEqual(got, []string{"T-3>T-2(merged)"}) {
			t.Fatalf("ends = %v, want the merge wait on the published blocker", got)
		}
		if c.Ends[0].Because.Code == CodeNeedNotNamed || !reflect.DeepEqual(c.Ends[0].Because.PRs, []MergePR{pr}) {
			t.Errorf("end = %+v, want a merge wait with its pull request", c.Ends[0].Because)
		}
		if !c.Ready || c.Code != "" {
			t.Errorf("ready %v code %q, want ready", c.Ready, c.Code)
		}
	})

	t.Run("a met edge to an unnamed ticket is no wait", func(t *testing.T) {
		t.Parallel()
		c := DeriveChart(ChartFacts{Name: "c", Named: []string{"T-3"}, Entries: []ChartEntry{
			entry(2, "T-2", ticketAt("T-2")),
			entry(3, "T-3", ticketBriefing("T-3"), ChartNeed{Ticket: "T-2", Kind: "stacked", Met: true}),
		}})
		if len(c.Ends) != 0 || !reflect.DeepEqual(c.Run, [][]string{{"T-3"}}) {
			t.Errorf("ends %v run %v, want no end and T-3 to run", endsOf(c), c.Run)
		}
	})

	t.Run("the prompts name two, three and the whole chart", func(t *testing.T) {
		t.Parallel()
		entries := []ChartEntry{
			entry(1, "T-1", ticketBriefing("T-1")),
			entry(2, "T-2", ticketBriefing("T-2")),
			entry(3, "T-3", ticketBriefing("T-3")),
		}
		for _, tc := range []struct {
			named []string
			want  string
		}{
			{[]string{"T-2", "T-1"}, "Take T-1 and T-2 to PR"},
			{[]string{"T-3", "T-2", "T-1"}, "Take T-1, T-2 and T-3 to PR"},
		} {
			c := DeriveChart(ChartFacts{Name: "c", Named: tc.named, Entries: entries})
			if c.Prompt != tc.want {
				t.Errorf("named %v: prompt = %q, want %q", tc.named, c.Prompt, tc.want)
			}
		}
		if got := ContinueTicketsPrompt([]string{"A", "B", "C"}); got != "Continue A, B and C" {
			t.Errorf("ContinueTicketsPrompt = %q", got)
		}
		whole := DeriveChart(ChartFacts{Name: "c", Entries: entries})
		if whole.Prompt != "Take c to PR" || whole.Named != nil {
			t.Errorf("no names: prompt %q named %v, want the chart's", whole.Prompt, whole.Named)
		}
	})

	t.Run("an entry with no ticket yet is not done and unnamed", func(t *testing.T) {
		t.Parallel()
		c := DeriveChart(ChartFacts{Name: "c", Named: []string{"T-1"}, Entries: []ChartEntry{
			entry(1, "T-1", ticketAt("T-1"), ChartNeed{Pos: 2, Kind: "merged", Why: "T-1 waits for entry #2"}),
			{Pos: 2, Title: "later"},
		}})
		if c.Ungraduated != 0 || len(c.Rows) != 1 || c.Code != CodeNeedNotNamed {
			t.Fatalf("ungraduated %d rows %d code %q, want 0, 1, %s", c.Ungraduated, len(c.Rows), c.Code, CodeNeedNotNamed)
		}
		if want := "T-1 waits for entry #2, which this run does not take and is not done"; c.Reason != want {
			t.Errorf("reason = %q, want %q", c.Reason, want)
		}
	})
}
