package store

import "testing"

// TestContainerOf: a task's container is its parent, else the issue of the one
// chart that lists it; a multi-task's is its chart's issue; an issue, a ticket
// of a legacy chart, an unlisted one and one two charts list have none.
func TestContainerOf(t *testing.T) {
	t.Parallel()
	st := chartKindStore(t, map[string]string{
		"main":   "issue: I-1\ntickets:\n  - id: T-1\n    title: a\n  - id: M-1\n    title: m\n    kind: multi-task\n  - id: T-3\n    title: c\n  - id: T-4\n    title: d\n  - id: T-5\n    title: e\n",
		"second": "issue: I-2\ntickets:\n  - id: T-4\n    title: d\n",
		"legacy": "tickets:\n  - id: T-6\n    title: f\n",
	})
	for _, c := range []struct {
		name   string
		ticket string
		rec    Ticket
		want   string
		found  bool
	}{
		{"a task of one chart", "T-1", Ticket{}, "I-1", true},
		{"a multi-task", "M-1", Ticket{Kind: KindMultiTask}, "I-1", true},
		{"a task under a multi-task", "T-3", Ticket{Parent: "M-1"}, "M-1", true},
		{"the parent wins over the chart's issue", "T-5", Ticket{Parent: "M-1"}, "M-1", true},
		{"an issue", "I-1", Ticket{Kind: KindIssue}, "", false},
		{"a ticket two charts list", "T-4", Ticket{}, "", false},
		{"a ticket of a legacy chart", "T-6", Ticket{}, "", false},
		{"a ticket no chart lists", "T-9", Ticket{}, "", false},
	} {
		got, found, err := st.ContainerOf(c.ticket, c.rec)
		if err != nil || got != c.want || found != c.found {
			t.Errorf("%s: ContainerOf = %q, %v, %v; want %q, %v", c.name, got, found, err, c.want, c.found)
		}
	}
}
