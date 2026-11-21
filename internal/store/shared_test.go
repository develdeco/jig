package store

import (
	"reflect"
	"testing"
)

// TestSharedChartsListing: a ticket two model charts list is listed by each in
// chart-name order with its entry and position; a legacy chart and a chart
// that names the id only in a title do not count.
func TestSharedChartsListing(t *testing.T) {
	t.Parallel()
	st := chartKindStore(t, map[string]string{
		"zeta":   "issue: I-2\ntickets:\n  - id: T-0\n    title: z\n  - id: T-1\n    title: a\n    blocked_by:\n      - ref: T-0\n",
		"alpha":  "issue: I-1\ntickets:\n  - id: T-1\n    title: a\n",
		"legacy": "tickets:\n  - id: T-1\n    title: a\n",
		"note":   "issue: I-4\ntickets:\n  - id: T-9\n    title: mentions T-1 only in a title\n",
	})
	got, err := st.ChartsListing("T-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("ChartsListing(T-1) = %+v, %v, want two", got, err)
	}
	if got[0].Chart != "alpha" || got[0].Issue != "I-1" || got[0].Position != 1 {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Chart != "zeta" || got[1].Issue != "I-2" || got[1].Position != 2 || len(got[1].Entry.BlockedBy) != 1 || got[1].Entry.BlockedBy[0].Ref != "T-0" {
		t.Errorf("second = %+v", got[1])
	}
	if none, _ := st.ChartsListing("T-7"); len(none) != 0 {
		t.Errorf("unlisted ticket listed: %+v", none)
	}
}

// TestSharedContainersOf: a parent is the one container; else every issue that
// lists the ticket, in chart order; an issue has none; ContainerOf is the one
// element and says none for a shared task.
func TestSharedContainersOf(t *testing.T) {
	t.Parallel()
	st := chartKindStore(t, map[string]string{
		"b": "issue: I-2\ntickets:\n  - id: T-1\n    title: a\n  - id: T-2\n    title: b\n",
		"a": "issue: I-1\ntickets:\n  - id: T-1\n    title: a\n  - id: T-3\n    title: c\n",
	})
	for _, c := range []struct {
		name   string
		ticket string
		rec    Ticket
		want   []string
	}{
		{"a shared task", "T-1", Ticket{}, []string{"I-1", "I-2"}},
		{"a task of one chart", "T-2", Ticket{}, []string{"I-2"}},
		{"a parent wins", "T-1", Ticket{Parent: "M-1"}, []string{"M-1"}},
		{"an issue", "I-1", Ticket{Kind: KindIssue}, nil},
		{"unlisted", "T-9", Ticket{}, nil},
	} {
		got, err := st.ContainersOf(c.ticket, c.rec)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ContainersOf = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if id, found, _ := st.ContainerOf("T-1", Ticket{}); found || id != "" {
		t.Errorf("ContainerOf(shared) = %q, %v, want none", id, found)
	}
	if id, found, _ := st.ContainerOf("T-2", Ticket{}); !found || id != "I-2" {
		t.Errorf("ContainerOf(T-2) = %q, %v, want I-2", id, found)
	}
}
