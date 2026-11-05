package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/phase"
)

// tripleRig is the chart c of three tickets, all in workspace alpha: TK-1 and
// TK-2 independent, TK-3 stacked on TK-2 (TK-1 carries the alias first-one).
func tripleRig(t *testing.T) chartRig {
	t.Helper()
	r := newChartRig(t)
	r.overlapRig(t, "repos:\n  - remote: /srv/app.git\n    label: app\n", map[string][2]string{
		"TK-1": {"aliases:\n  - first-one\n", sliceFile("alpha")},
		"TK-2": {"", sliceFile("alpha")},
		"TK-3": {"blocked_by:\n  - ticket: TK-2\n    kind: stacked\n", sliceFile("alpha")},
	})
	return r
}

func rowIDs(n nextChart) []string {
	out := []string{}
	for _, row := range n.Tickets {
		out = append(out, row.ID)
	}
	return out
}

// TestNextChartTickets: --tickets reads the chart over the named tickets: the
// rows, the waves, the overlaps and the waits of those only, the set echoed
// (JSON named, text pair), a repeat counted once, an alias read, and the
// whole-chart standing left as it was.
func TestNextChartTickets(t *testing.T) {
	t.Parallel()
	r := tripleRig(t)

	whole := r.next(t, "c")
	if whole.Named != nil || len(whole.Tickets) != 3 || whole.Prompt != "Take c to PR" {
		t.Fatalf("whole chart = named %v rows %d prompt %q, want unchanged", whole.Named, len(whole.Tickets), whole.Prompt)
	}
	if out, _ := r.run(t, "next", "c", "--json"); strings.Contains(out, `"named"`) {
		t.Errorf("the whole chart's JSON has a named key:\n%s", out)
	}

	out, code := r.run(t, "next", "c", "--tickets", "first-one,TK-3,TK-1", "--json")
	var n nextChart
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		t.Fatalf("exit %d, %v\n%s", code, err, out)
	}
	if want := []string{"TK-1", "TK-3"}; !reflect.DeepEqual(n.Named, want) || !reflect.DeepEqual(rowIDs(n), want) {
		t.Errorf("named %v rows %v, want %v (the alias and the repeat read once)", n.Named, rowIDs(n), want)
	}
	if want := [][]string{{"TK-1"}}; !reflect.DeepEqual(n.Run, want) {
		t.Errorf("run = %v, want %v", n.Run, want)
	}
	if got := overlapsOf(n); len(got) != 0 {
		t.Errorf("overlaps = %v, want none (TK-2 is not in the run)", got)
	}
	if len(n.Ends) != 1 || n.Ends[0].Ticket != "TK-3" || n.Ends[0].Blocker != "TK-2" || n.Ends[0].Kind != "stacked" ||
		n.Ends[0].Fact != "TK-3 waits for TK-2, which this run does not take and is not done" {
		t.Errorf("ends = %+v, want TK-3 behind TK-2", n.Ends)
	}
	if !n.Ready || n.Code != "" || n.Prompt != "Take TK-1 and TK-3 to PR" {
		t.Errorf("verdict = ready %v code %q prompt %q", n.Ready, n.Code, n.Prompt)
	}

	text, code := r.run(t, "next", "c", "--tickets", "TK-1,TK-3")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	for _, want := range []string{"named: TK-1+TK-3", "waits[1]", "TK-2", "Take TK-1 and TK-3 to PR"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "workspace_overlap") || strings.Contains(text, "title TK-2") {
		t.Errorf("text lists what the run does not take:\n%s", text)
	}

	both := func() nextChart {
		out, code := r.run(t, "next", "c", "--tickets", "TK-1,TK-2", "--json")
		var n nextChart
		if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
			t.Fatalf("exit %d, %v\n%s", code, err, out)
		}
		return n
	}()
	if want := []string{"app/alpha:TK-1+TK-2"}; !reflect.DeepEqual(overlapsOf(both), want) || len(both.Ends) != 0 {
		t.Errorf("TK-1,TK-2: overlaps %v ends %+v, want the pair and no end", overlapsOf(both), both.Ends)
	}

	// jig status has no --tickets: it is the whole chart's standing.
	if st, code := r.run(t, "status", "c"); code != 0 || strings.Contains(st, "named:") {
		t.Errorf("status c: exit %d, want the whole chart:\n%s", code, st)
	}
}

// TestNextChartTicketsRefused: a name outside the chart is refused with every
// offender named and the chart's tickets listed, and --tickets with a ticket,
// no argument, --for, --topology or an empty list is refused too.
func TestNextChartTicketsRefused(t *testing.T) {
	t.Parallel()
	r := tripleRig(t)

	out, code := r.run(t, "next", "c", "--tickets", "TK-1,ZZ-9,YY-1")
	for _, want := range []string{"VALIDATION_ERROR", "ticket ZZ-9 is not a ticket of chart c", "ticket YY-1 is not a ticket of chart c", "The tickets of c are: TK-1, TK-2 and TK-3"} {
		if code == 0 || !strings.Contains(out, want) {
			t.Errorf("exit %d, output lacks %q:\n%s", code, want, out)
		}
	}
	if strings.Contains(out, "ready:") {
		t.Errorf("a refusal derived the chart:\n%s", out)
	}

	for name, args := range map[string][]string{
		"a ticket":      {"next", "TK-1", "--tickets", "TK-1"},
		"no argument":   {"next", "--tickets", "TK-1"},
		"--for":         {"next", "c", "--tickets", "TK-1", "--for", "build"},
		"--topology":    {"next", "c", "--tickets", "TK-1", "--topology"},
		"an empty list": {"next", "c", "--tickets", ""},
		"an empty item": {"next", "c", "--tickets", "TK-1,,TK-2"},
	} {
		out, code := r.run(t, args...)
		if code == 0 || !strings.Contains(out, "VALIDATION_ERROR") {
			t.Errorf("%s: exit %d, want VALIDATION_ERROR:\n%s", name, code, out)
		}
	}
	if out, _ := r.run(t, "next", "TK-1", "--tickets", "TK-1"); !strings.Contains(out, "--tickets names tickets of a chart: TK-1 is not one") {
		t.Errorf("a ticket's refusal:\n%s", out)
	}

	empty := newChartRig(t)
	empty.chart(t, "e", "tickets:\n  - title: Not yet\n", "")
	if out, code := empty.run(t, "next", "e", "--tickets", "TK-1"); code == 0 || !strings.Contains(out, "none yet: run `jig graduate e`") {
		t.Errorf("a chart with no ticket: exit %d\n%s", code, out)
	}
}

// TestNextChartTicketsOnlyTheBlockerIsLeft: naming a ticket whose blocker is not
// named leaves the run nothing to start, which is its own code with the help
// that names the blocker.
func TestNextChartTicketsOnlyTheBlockerIsLeft(t *testing.T) {
	t.Parallel()
	r := tripleRig(t)
	r.put(t, "tickets/TK-3/brief.md", "# TK-3\n")
	approveBrief(t, r.st, "TK-3")
	out, code := r.run(t, "next", "c", "--tickets", "TK-3,TK-1")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	n := r.nextTickets(t, "c", "TK-3")
	if n.Ready || n.Code != phase.CodeNeedNotNamed {
		t.Fatalf("ready %v code %q, want not ready, %s", n.Ready, n.Code, phase.CodeNeedNotNamed)
	}
	text, _ := r.run(t, "next", "c", "--tickets", "TK-3")
	if !strings.Contains(text, "Name TK-2 too, or take TK-2 first") {
		t.Errorf("help lacks the blocker to name:\n%s", text)
	}
}

func (r chartRig) nextTickets(t *testing.T, chart, list string) nextChart {
	t.Helper()
	out, code := r.run(t, "next", chart, "--tickets", list, "--json")
	var n nextChart
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		t.Fatalf("exit %d, %v\n%s", code, err, out)
	}
	return n
}
