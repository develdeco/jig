package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// TestSharedChartProblems: an entry that two model charts list is a shared
// task; one that waits, is a multi-task or is under one is listed, in the
// order of the brief, and graduate refuses the first with nothing minted. A
// clean shared task, and a task a legacy chart shares, list none.
func TestSharedChartProblems(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "kind: issue\n", false)
	r.ticket(t, "SHOP-2", "kind: issue\n", false)
	r.ticket(t, "SHOP-3", "", false)
	r.ticket(t, "SHOP-4", "", false)
	r.ticket(t, "SHOP-5", "kind: multi-task\n", false)
	r.ticket(t, "SHOP-6", "parent: SHOP-5\n", false)
	r.ticket(t, "SHOP-7", "blocked_by:\n  - ticket: SHOP-3\n", false)
	r.chart(t, "one", "issue: SHOP-1\ntickets:\n  - id: SHOP-3\n    title: clean\n  - id: SHOP-4\n    title: waits\n    blocked_by:\n      - ref: \"#1\"\n  - id: SHOP-5\n    title: multi\n    kind: multi-task\n  - id: SHOP-6\n    title: child\n    under: \"#3\"\n  - id: SHOP-7\n    title: recorded\n", "")
	r.chart(t, "two", "issue: SHOP-2\ntickets:\n  - id: SHOP-3\n    title: clean\n  - id: SHOP-4\n    title: waits\n  - id: SHOP-5\n    title: multi\n  - id: SHOP-6\n    title: child\n  - id: SHOP-7\n    title: recorded\n", "")
	want := []string{
		`entry 2 ("waits"): is also an entry of chart "two" (issue SHOP-2) but waits on #1; a shared task starts from the target`,
		`entry 3 ("multi"): is also an entry of chart "two" but is a multi-task; only a task is shared`,
		`entry 4 ("child"): is also an entry of chart "two" and is under entry 3; a shared task sits directly under each issue`,
		`entry 5 ("recorded"): is also an entry of chart "two" (issue SHOP-2) but waits on SHOP-3; a shared task starts from the target`,
	}
	if got := chartKindLines(t, r, "one"); !reflect.DeepEqual(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	entries, _ := r.st.ReadChart("one")
	cfg := project.Config{Keys: map[string]string{"SHOP": "everything"}}
	if err := validateChartEntries(r.st, cfg, entries, "one"); err == nil || !strings.Contains(err.Error(), "but waits on #1") {
		t.Errorf("graduate's checks = %v, want the first problem", err)
	}

	// the ticket's recorded parent alone is under a multi-task.
	r.ticket(t, "SHOP-8", "parent: SHOP-5\n", false)
	r.ticket(t, "SHOP-9", "kind: issue\n", false)
	r.chart(t, "five", "issue: SHOP-9\ntickets:\n  - id: SHOP-8\n    title: kid\n", "")
	r.chart(t, "six", "tickets:\n  - id: SHOP-3\n    title: legacy\n    blocked_by:\n      - ref: SHOP-4\n  - id: SHOP-8\n    title: kid\n", "")
	if got := chartKindLines(t, r, "six"); len(got) != 0 {
		t.Errorf("legacy chart shares nothing: %v", got)
	}
	r.ticket(t, "SHOP-10", "kind: issue\n", false)
	r.chart(t, "seven", "issue: SHOP-10\ntickets:\n  - id: SHOP-8\n    title: kid\n", "")
	if got := chartKindLines(t, r, "seven"); len(got) != 1 || !strings.Contains(got[0], "is under SHOP-5; a shared task sits directly under each issue") {
		t.Errorf("recorded parent: %v", got)
	}
}
