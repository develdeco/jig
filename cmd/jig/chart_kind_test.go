package main

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

func chartKindLines(t *testing.T, r chartRig, chart string) []string {
	t.Helper()
	entries, err := r.st.ReadChart(chart)
	if err != nil {
		t.Fatal(err)
	}
	cfg := project.Config{Keys: map[string]string{"SHOP": "everything"}}
	_, problems := checkChart(r.st, cfg, chart, entries)
	var lines []string
	for _, p := range problems {
		lines = append(lines, p.Line)
	}
	return lines
}

// TestChartKindProblemsInTheBriefsOrder: every problem of an issue and of the
// kinds the entries mint, in the order listed, and graduate's own checks
// refuse the first.
func TestChartKindProblemsInTheBriefsOrder(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "kind: issue\n", false)
	r.ticket(t, "SHOP-2", "", false)
	r.chart(t, "other", "issue: SHOP-1\ntickets:\n  - title: x\n", "")

	r.chart(t, "a", "issue: SHOP-1\ntickets:\n  - title: A\n    kind: epic\n  - title: B\n    under: \"#9\"\n  - title: C\n    under: \"#4\"\n  - title: D\n  - title: E\n    kind: multi-task\n    under: \"#6\"\n  - title: F\n    kind: multi-task\n  - title: G\n    kind: multi-task\n", "")
	want := []string{
		`issue "SHOP-1" is also the issue of chart "other"`,
		`entry 1 ("A"): has kind "epic"; must be "task" or "multi-task"`,
		`entry 2 ("B"): is under "#9", which is not an entry of this chart`,
		`entry 3 ("C"): is under entry 4, which is not a multi-task`,
		`entry 5 ("E"): is a multi-task under entry 6; a multi-task holds tasks only`,
		`entry 5 ("E"): is a multi-task with no task under it`,
		`entry 7 ("G"): is a multi-task with no task under it`,
	}
	if got := chartKindLines(t, r, "a"); !reflect.DeepEqual(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	r.chart(t, "b", "issue: SHOP-2\ntickets:\n  - title: A\n", "")
	if got := chartKindLines(t, r, "b"); len(got) != 1 || got[0] != `issue "SHOP-2" is a task, not an issue` {
		t.Errorf("issue that is a task: %v", got)
	}
	r.chart(t, "c", "issue: SHOP-9\ntickets:\n  - title: A\n", "")
	if got := chartKindLines(t, r, "c"); len(got) != 1 || got[0] != `issue "SHOP-9" is not a ticket of the store` {
		t.Errorf("issue the store lacks: %v", got)
	}
	r.chart(t, "d", "tickets:\n  - title: A\n    kind: multi-task\n  - title: B\n    under: \"#1\"\n", "")
	if got := chartKindLines(t, r, "d"); len(got) != 2 || !strings.HasSuffix(got[0], "kind or under needs the chart's issue") {
		t.Errorf("kind with no issue: %v", got)
	}

	entries, _ := r.st.ReadChart("a")
	cfg := project.Config{Keys: map[string]string{"SHOP": "everything"}}
	err := validateChartEntries(r.st, cfg, entries, "a")
	if err == nil || !strings.Contains(err.Error(), "also the issue of chart") {
		t.Errorf("graduate's checks = %v, want the first problem", err)
	}
}

// TestChartKindALegacyAndAModelChartAreClean: a chart with no issue lists no
// kind problem, and a chart of an issue, a multi-task and two tasks (under it
// by position and by ticket id) lists none.
func TestChartKindALegacyAndAModelChartAreClean(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "kind: issue\n", false)
	r.chart(t, "old", "tickets:\n  - title: A\n  - title: B\n    blocked_by:\n      - ref: \"#1\"\n", "")
	if got := chartKindLines(t, r, "old"); len(got) != 0 {
		t.Errorf("legacy chart: %v", got)
	}
	r.ticket(t, "SHOP-7", "kind: multi-task\n", false)
	r.chart(t, "new", "issue: SHOP-1\ntickets:\n  - title: M\n    kind: multi-task\n  - title: T1\n    under: \"#1\"\n  - id: SHOP-7\n    title: M2\n    kind: multi-task\n  - title: T2\n    under: SHOP-7\n", "")
	if got := chartKindLines(t, r, "new"); len(got) != 0 {
		t.Errorf("model chart: %v", got)
	}
}

// TestChartKindNewRecordsTheIssue: jig chart new --issue records issue:,
// refuses a ticket that is not an issue and an issue that has a chart.
func TestChartKindNewRecordsTheIssue(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginClone(t, clone)
	jig := func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := run(e, append(args, "--store", clone), &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("ticket", "new", "--title", "Need", "--kind", "issue"); code != 0 {
		t.Fatalf("issue: exit %d\n%s", code, out)
	}
	if code, out := jig("ticket", "new", "--title", "Work"); code != 0 {
		t.Fatalf("task: exit %d\n%s", code, out)
	}

	if code, out := jig("chart", "new", "plan", "--title", "Plan", "--issue", "DEMO-2"); code == 0 || !strings.Contains(out, "is a task, not an issue") {
		t.Fatalf("issue that is a task: exit %d\n%s", code, out)
	}
	if code, out := jig("chart", "new", "plan", "--title", "Plan", "--issue", "DEMO-1"); code != 0 {
		t.Fatalf("chart new --issue: exit %d\n%s", code, out)
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.ChartIssue("plan"); err != nil || got != "DEMO-1" {
		t.Fatalf("ChartIssue = %q, %v", got, err)
	}
	if got, _ := st.ChartOfIssue("DEMO-1"); got != "plan" {
		t.Errorf("ChartOfIssue = %q", got)
	}
	if code, out := jig("chart", "new", "again", "--title", "Again", "--issue", "DEMO-1"); code == 0 || !strings.Contains(out, `already has chart "plan"`) {
		t.Fatalf("second chart for an issue: exit %d\n%s", code, out)
	}
}
