package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// landingRow is a landing of the verb's lands table, as JSON.
func landingRow(ticket string, state string, after ...string) map[string]any {
	if after == nil {
		after = []string{}
	}
	return map[string]any{"ticket": ticket, "prompt": "Land " + ticket, "after": after, "state": state}
}

// atLanding is the chart as an issue's run leaves it at a landing stop: the
// verdict CHART_WAITS_LAND, the prompt that continues the run, and the landings.
func atLanding(lands ...map[string]any) func(map[string]any) {
	return func(v map[string]any) {
		v["ready"], v["code"], v["reason"] = false, phase.CodeChartWaitsLand, "JIG-4 waits to land"
		v["prompt"] = "Continue checkout"
		v["lands"], v["model"] = lands, true
		v["ends"] = []map[string]any{}
	}
}

// tallChart is the chart screen on a terminal tall enough for its whole Run
// panel, read as verb printed.
func tallChart(t *testing.T, verb string) *rig {
	t.Helper()
	g := chartScreenRig(t)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 60})
	info, err := parseChart(verb)
	if err != nil || info == nil {
		t.Fatalf("the chart does not parse: %v", err)
	}
	g.m.w.charts[g.m.top().chart].info = info
	return g
}

// landWords is the Land phase's text for a ticket published into the chart's
// issue, as internal/phase writes it, for each state the frame must repeat.
func landWords(t *testing.T) map[string]string {
	t.Helper()
	published := phase.Facts{Ticket: "JIG-4", BriefExists: true, Sections: 2, Approval: phase.ApprovalCurrent, ApprovedBy: "Dana", ApprovedHow: "person",
		Slices: []phase.Slice{{ID: "a", State: "green"}}, Published: true}
	lines := []journal.Line{
		{Event: journal.EventPr, Outcome: "opened", URL: "https://example.test/acme/shop/pull/4"},
	}
	waits := phase.Derive(published, lines).Of(phase.Land).Text
	published.MergedOnHost = true
	host := phase.Derive(published, lines).Of(phase.Land).Text
	return map[string]string{"waits": waits, "host": host}
}

// TestRunPanelAtALandingStopSaysLand: at a landing stop the Run panel's next row
// is the landing's word, not the run's, with the landing's state under it and
// the landings behind it listed after; with none left it is Continue again.
func TestRunPanelAtALandingStopSaysLand(t *testing.T) {
	t.Parallel()

	t.Run("the first landing", func(t *testing.T) {
		g := tallChart(t, checkoutJSON(atLanding(landingRow("JIG-4", "waits for a merge"))))
		v := g.view()
		for _, want := range []string{`say "Land JIG-4"`, "waits for a merge"} {
			if !strings.Contains(v, want) {
				t.Errorf("the Run panel lacks %q:\n%s", want, v)
			}
		}
		if strings.Contains(v, `"Continue checkout"`) {
			t.Errorf("the Run panel still says Continue at a landing stop:\n%s", v)
		}
	})

	t.Run("a landing behind another", func(t *testing.T) {
		g := tallChart(t, checkoutJSON(atLanding(landingRow("JIG-4", "waits for a merge"), landingRow("JIG-5", "waits for a merge", "JIG-4"))))
		v := g.view()
		for _, want := range []string{`say "Land JIG-4"`, "then Land JIG-5, after JIG-4"} {
			if !strings.Contains(v, want) {
				t.Errorf("the Run panel lacks %q:\n%s", want, v)
			}
		}
		if strings.Contains(v, `say "Land JIG-5"`) {
			t.Errorf("the Run panel asks for a landing that waits on another:\n%s", v)
		}
	})

	t.Run("two landings that wait for none, and one that waits for a landing", func(t *testing.T) {
		g := tallChart(t, checkoutJSON(atLanding(landingRow("JIG-6", "waits for a merge", "JIG-5"), landingRow("JIG-4", "waits for a merge"), landingRow("JIG-5", "waits for a merge"))))
		v := g.view()
		for _, want := range []string{`say "Land JIG-4"`, "also Land JIG-5", "then Land JIG-6, after JIG-5"} {
			if !strings.Contains(v, want) {
				t.Errorf("the Run panel lacks %q:\n%s", want, v)
			}
		}
		if strings.Contains(v, "after \n") || strings.Contains(v, "also Land JIG-4") || strings.Contains(v, "then Land JIG-4") {
			t.Errorf("the Run panel words a landing wrongly:\n%s", v)
		}
	})

	t.Run("a legacy chart's merge wait keeps Continue", func(t *testing.T) {
		g := tallChart(t, checkoutJSON(func(v map[string]any) { v["ready"], v["code"] = false, phase.CodeChartWaitsMerge }))
		if v := g.view(); !strings.Contains(v, `say "Continue checkout"`) {
			t.Errorf("a merge wait does not say Continue:\n%s", v)
		}
	})

	t.Run("none left", func(t *testing.T) {
		g := tallChart(t, checkoutJSON(func(v map[string]any) {
			v["code"], v["lands"] = "", []map[string]any{}
		}))
		v := g.view()
		if !strings.Contains(v, `say "Continue checkout"`) || strings.Contains(v, "next     say \"Land") {
			t.Errorf("with no landing left the Run panel does not say Continue:\n%s", v)
		}
	})
}

// TestChartHeaderAtALandingStop: the header reads that the landing waits for
// the person, in the needs-you state, and not "waiting".
func TestChartHeaderAtALandingStop(t *testing.T) {
	t.Parallel()
	g := tallChart(t, checkoutJSON(atLanding(landingRow("JIG-4", "waits for a merge"))))
	c := g.m.w.charts[g.m.top().chart]
	state, label := g.m.chartStatus(c, nil)
	if state != StateNeedsInput || label != "landing waits for you" {
		t.Errorf("chartStatus = %s, %q", state, label)
	}
	if v := g.view(); !strings.Contains(v, "landing waits for you") || strings.Contains(v, "waiting") {
		t.Errorf("the header does not say the landing waits for you:\n%s", v)
	}
}

// TestEndNoteNamesThePullRequestOnce: a merge wait names the pull request to
// merge and its state once, the address when no number can be read from it, the
// verb's fact only when the blocker has no pull request, and a stacked end
// keeps its retarget words.
func TestEndNoteNamesThePullRequestOnce(t *testing.T) {
	t.Parallel()
	pr := func(url, state string) []chartPR { return []chartPR{{URL: url, State: state}} }
	for _, tc := range []struct {
		name string
		e    chartEnd
		want string
	}{
		{"an open pull request", chartEnd{Ticket: "A-2", Blocker: "A-1", Kind: "merged", Fact: "A-2 waits for A-1 merged", PRs: pr("https://example.test/acme/shop/pull/7", "OPEN")}, "A-2 waits for A-1 to merge · PR #7 open"},
		{"no state", chartEnd{Ticket: "A-2", Blocker: "A-1", PRs: pr("https://example.test/acme/shop/pull/7", "")}, "A-2 waits for A-1 to merge · PR #7"},
		{"no number", chartEnd{Ticket: "A-2", Blocker: "A-1", PRs: pr("https://example.test/review/abc", "OPEN")}, "A-2 waits for A-1 to merge · https://example.test/review/abc open"},
		{"no pull request", chartEnd{Ticket: "A-2", Blocker: "A-1", Fact: "A-1 has not published"}, "A-2 waits for A-1 to merge · A-1 has not published"},
		{"stacked", chartEnd{Ticket: "A-2", Blocker: "A-1", Kind: "stacked", PRs: pr("https://example.test/acme/shop/pull/7", "OPEN")}, "A-2 retargets when A-1 merges · PR #7 open"},
	} {
		got := endNote(tc.e)
		if got != tc.want {
			t.Errorf("%s: endNote = %q, want %q", tc.name, got, tc.want)
		}
		if strings.Count(got, "A-1") > 2 || strings.Contains(got, "merged ·") {
			t.Errorf("%s: %q says a fact twice", tc.name, got)
		}
	}
}

// TestLandStatesReadAsPhaseWritesThem: each Land state on the chart screen is the
// text internal/phase wrote, character for character.
func TestLandStatesReadAsPhaseWritesThem(t *testing.T) {
	t.Parallel()
	words := landWords(t)
	for name, text := range words {
		if text == "" {
			t.Fatalf("phase has no words for %s", name)
		}
		g := tallChart(t, checkoutJSON(atLanding(landingRow("JIG-4", text))))
		if v := g.view(); !strings.Contains(v, text) {
			t.Errorf("the chart screen does not carry phase's %q whole:\n%s", text, v)
		}
		if c := g.m.w.charts[g.m.top().chart]; c.info.Lands[0].State != text {
			t.Errorf("the chart took %q for the landing, phase wrote %q", c.info.Lands[0].State, text)
		}
	}
	if words["waits"] == words["host"] {
		t.Errorf("phase words both states as %q", words["waits"])
	}
}
