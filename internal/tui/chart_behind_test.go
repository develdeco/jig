package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/phase"
)

// behindJSON is `jig next checkout --json` once the parent of the published
// JIG-4 merged: the verb reads the stack plan, so JIG-4's publish is current
// again (it owes a merge and a publish) and the run lists it as the first wave.
func behindJSON() string {
	return checkoutJSON(func(v map[string]any) {
		tickets := v["tickets"].([]map[string]any)
		tickets[0] = entry("JIG-4", "Coupon model", "publish", []string{"done", "done", "done", "current"}, true)
		tickets[0]["prompt"] = "Publish JIG-4"
		v["run"] = [][]string{{"JIG-4", "JIG-8"}, {"JIG-6"}}
	})
}

// behindChartRig is the chart screen of the fixtures whose verb lists JIG-4 as
// behind, on a terminal of 100 by 36. The screens' own derivation of JIG-4 is
// published: its journal says so and no stack plan is read.
func behindChartRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", behindJSON(), status)
	g.feed(lines...)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 36})
	return onChart(g)
}

// TestAChartRowFollowsTheVerbForATicketThatIsBehind: the verb is stack-aware and
// the screens' derivation is not, so a published ticket whose parent merged is
// done to the screens and at its publish again to the verb. The chart's row
// takes the verb's word: it is not done, its publish is the step it stands at,
// the cursor starts on it, and the Detail panel says what it owes and not that
// it is published.
func TestAChartRowFollowsTheVerbForATicketThatIsBehind(t *testing.T) {
	t.Parallel()
	g := behindChartRig(t)
	c := g.m.w.charts["checkout"]
	if own := g.m.phaseOf(g.m.w.tickets["JIG-4"]).Of(phase.Publish).State; own != phase.StateDone {
		t.Fatalf("the screens' own derivation of JIG-4 has its publish %s, want done: the fixture does not reproduce the gap", own)
	}
	row := g.m.chartRows(c)[0]
	if row.id != "JIG-4" || row.done || !row.behind || row.steps[stepPublish].Done || row.state() != StateReady {
		t.Errorf("JIG-4 = done %v behind %v publish done %v state %s, want a row at its publish, ready", row.done, row.behind, row.steps[stepPublish].Done, row.state())
	}
	for i := stepBrief; i < stepPublish; i++ {
		if !row.steps[i].Done {
			t.Errorf("step %s of the behind ticket is not done: the verb says it is", stepNames[i])
		}
	}
	if got := g.m.chartFirstOpen(c); got != 0 {
		t.Errorf("the cursor follows to row %d, want 0, the ticket that is behind", got)
	}

	v := g.view()
	if line := cursorLine(v); !strings.Contains(line, "JIG-4") || !strings.Contains(line, "behind · owes a merge, then a publish") {
		t.Errorf("the cursor is not on JIG-4 with its note, it is on %q:\n%s", line, v)
	}
	if !strings.Contains(v, "0 of 5 published") {
		t.Errorf("the Tickets panel counts the ticket that is behind as published:\n%s", v)
	}
	panel := strings.Join(detailPanelOf(t, v), "\n")
	if !strings.Contains(panel, "state    behind · owes a merge, then a publish") || strings.Contains(panel, "published") {
		t.Errorf("the Detail panel does not say JIG-4 owes a merge and a publish, or says it is published:\n%s", panel)
	}
	golden(t, "chart-behind", v)
}

// cursorLine is the line of a screen the cursor is on, "" when there is none.
func cursorLine(view string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "▸") {
			return l
		}
	}
	return ""
}

// TestAChartRowOfATicketThatIsNotBehindKeepsItsOwnWords: a verb that agrees with
// the screens' derivation changes nothing about a row: JIG-4 stays published and
// the cursor starts on JIG-5, as it does with no word from the verb on a stack.
func TestAChartRowOfATicketThatIsNotBehindKeepsItsOwnWords(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	c := g.m.w.charts["checkout"]
	row := g.m.chartRows(c)[0]
	if !row.done || row.behind || row.state() != StatePublished {
		t.Errorf("JIG-4 = done %v behind %v state %s, want published", row.done, row.behind, row.state())
	}
	if got := g.m.chartFirstOpen(c); got != 1 {
		t.Errorf("the cursor follows to row %d, want 1, JIG-5", got)
	}
	g.press("up")
	if panel := strings.Join(detailPanelOf(t, g.view()), "\n"); !strings.Contains(panel, "state    published") || strings.Contains(panel, "behind") {
		t.Errorf("the Detail panel of a published ticket does not say so:\n%s", panel)
	}
}

// TestFollowVerbTakesEachStepsDoneFromTheVerb: each step is done as the verb
// says, whichever way it differs from the screens' rows, and a step the two
// agree on keeps its row whole.
func TestFollowVerbTakesEachStepsDoneFromTheVerb(t *testing.T) {
	t.Parallel()
	done := func(step int, state, text string) stepRow {
		return stepRow{Step: step, State: state, Text: text, Done: true}
	}
	open := func(step int, state, text string) stepRow {
		return stepRow{Step: step, State: state, Text: text}
	}
	own := func() []stepRow {
		return []stepRow{
			done(stepBrief, StateOK, "approved"),
			open(stepBuild, StateBuilding, "1 of 3 green"),
			open(stepReview, StateQueued, "after a clean build"),
			open(stepPublish, StateQueued, "after a clean review"),
		}
	}
	published := []stepRow{
		done(stepBrief, StateOK, "approved"), done(stepBuild, StateOK, "3 of 3 green"),
		done(stepReview, StateOK, "clean"), done(stepPublish, StatePublished, "pull request opened"),
	}
	for _, tc := range []struct {
		name   string
		rows   []stepRow
		verb   chartTicket
		want   []stepRow
		behind bool
	}{
		{
			name: "the verb agrees",
			rows: own(),
			verb: chartTicket{Steps: []string{"done", "current", "pending", "pending"}},
			want: own(),
		},
		{
			name: "the verb is ahead of the screens",
			rows: own(),
			verb: chartTicket{Steps: []string{"done", "done", "current", "pending"}, Ready: true},
			want: []stepRow{
				done(stepBrief, StateOK, "approved"),
				done(stepBuild, StateOK, ""),
				open(stepReview, StateQueued, "after a clean build"),
				open(stepPublish, StateQueued, "after a clean review"),
			},
		},
		{
			name: "the verb has a published ticket at its publish again",
			rows: published,
			verb: chartTicket{Steps: []string{"done", "done", "done", "current"}, Ready: true},
			want: []stepRow{
				published[0], published[1], published[2],
				open(stepPublish, StateReady, behindText),
			},
			behind: true,
		},
		{
			name: "the verb has a published ticket back at its build, not ready",
			rows: published,
			verb: chartTicket{Steps: []string{"done", "current", "pending", "pending"}},
			want: []stepRow{
				published[0],
				open(stepBuild, StateQueued, "waiting to build"),
				open(stepReview, StateQueued, "waiting to review"),
				open(stepPublish, StateQueued, "waiting to publish"),
			},
		},
		{
			name: "the verb says nothing of the steps",
			rows: own(),
			verb: chartTicket{},
			want: own(),
		},
		{
			name: "the verb says fewer steps than there are",
			rows: published,
			verb: chartTicket{Steps: []string{"done", "done"}},
			want: published,
		},
	} {
		got, behind := followVerb(tc.rows, tc.verb)
		if !reflect.DeepEqual(got, tc.want) || behind != tc.behind {
			t.Errorf("%s:\n got %+v behind %v\nwant %+v behind %v", tc.name, got, behind, tc.want, tc.behind)
		}
	}
}
