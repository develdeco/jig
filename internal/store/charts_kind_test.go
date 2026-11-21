package store

import (
	"reflect"
	"strings"
	"testing"
)

func chartKindStore(t *testing.T, files map[string]string) *Store {
	t.Helper()
	st := &Store{Root: t.TempDir()}
	for name, content := range files {
		if err := AtomicWrite(st.ChartFile(name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// TestChartKindIssuesOfListsEveryChartOfTheTicketByChartName: a ticket two
// charts list has two issues in chart-name order; a chart with no issue, or
// that does not list the ticket, gives none.
func TestChartKindIssuesOfListsEveryChartOfTheTicketByChartName(t *testing.T) {
	t.Parallel()
	st := chartKindStore(t, map[string]string{
		"zeta":   "issue: I-2\ntickets:\n  - id: T-1\n    title: a\n",
		"alpha":  "issue: I-1\ntickets:\n  - id: T-1\n    title: a\n  - id: T-2\n    title: b\n",
		"legacy": "tickets:\n  - id: T-1\n    title: a\n",
		"other":  "issue: I-3\ntickets:\n  - id: T-9\n    title: a\n",
		"note":   "issue: I-4\ntickets:\n  - id: T-9\n    title: mentions T-1 only in a title\n",
	})
	got, err := st.IssuesOf("T-1")
	if err != nil || !reflect.DeepEqual(got, []string{"I-1", "I-2"}) {
		t.Fatalf("IssuesOf(T-1) = %v, %v, want [I-1 I-2]", got, err)
	}
	if got, _ := st.IssuesOf("T-2"); !reflect.DeepEqual(got, []string{"I-1"}) {
		t.Errorf("IssuesOf(T-2) = %v", got)
	}
	if got, _ := st.IssuesOf("T-404"); len(got) != 0 {
		t.Errorf("IssuesOf(T-404) = %v", got)
	}
}

// TestChartKindIssueAndOfIssue: ChartIssue reads the chart's issue, ChartOfIssue
// the chart of an issue.
func TestChartKindIssueAndOfIssue(t *testing.T) {
	t.Parallel()
	st := chartKindStore(t, map[string]string{
		"a": "issue: I-1\ntickets: []\n",
		"b": "tickets: []\n",
	})
	if got, _ := st.ChartIssue("a"); got != "I-1" {
		t.Errorf("ChartIssue(a) = %q", got)
	}
	if got, _ := st.ChartIssue("b"); got != "" {
		t.Errorf("ChartIssue(b) = %q", got)
	}
	if got, _ := st.ChartOfIssue("I-1"); got != "a" {
		t.Errorf("ChartOfIssue(I-1) = %q", got)
	}
	if got, _ := st.ChartOfIssue("I-9"); got != "" {
		t.Errorf("ChartOfIssue(I-9) = %q", got)
	}
}

// TestChartKindSurvivesWriteChart: an entry's kind and under, and the chart's
// issue, with the comments around them, stay through WriteChart's rewrite.
func TestChartKindSurvivesWriteChart(t *testing.T) {
	t.Parallel()
	src := "# header\nissue: I-1\ntickets:\n  # the multi-task\n  - title: M\n    kind: multi-task\n  - title: T\n    under: \"#1\"\n"
	st := chartKindStore(t, map[string]string{"c": src})
	entries, err := st.ReadChart("c")
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Kind != KindMultiTask || entries[1].Under != "#1" {
		t.Fatalf("read kind/under = %q / %q", entries[0].Kind, entries[1].Under)
	}
	entries[1].ID = "T-5"
	if err := st.WriteChart("c", entries); err != nil {
		t.Fatal(err)
	}
	raw, _ := ReadFile(st.ChartFile("c"))
	for _, want := range []string{"# header", "issue: I-1", "# the multi-task", "kind: multi-task", `under: "#1"`, "id: T-5"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("rewritten file lacks %q:\n%s", want, raw)
		}
	}
}
