package journal

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func sharedStore(t *testing.T, charts map[string]string) *store.Store {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("T-1", store.Ticket{Title: "Task"}); err != nil {
		t.Fatal(err)
	}
	for name, content := range charts {
		if err := store.AtomicWrite(st.ChartFile(name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// TestSharedJournal: a publish line of another issue is dropped by ReadFor and
// nothing else is; a line with no into is the one container's of a ticket that
// has one, and of neither issue of a shared task; with no issue it is Read.
func TestSharedJournal(t *testing.T) {
	t.Parallel()
	write := func(st *store.Store) {
		for _, l := range []Line{
			{Event: EventGateClean, Commit: "h1"},
			{Event: EventPrBody, Into: "I-1", Commit: "h1"},
			{Event: EventPr, Into: "I-2", URL: "u2"},
			{Event: EventPublishApproved, Into: "I-1"},
			{Event: EventPublishDone, Into: "I-2"},
			{Event: EventPublishDone},
		} {
			if err := Append(st, "T-1", l); err != nil {
				t.Fatal(err)
			}
		}
	}
	events := func(lines []Line, err error) []string {
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range lines {
			out = append(out, l.Event+">"+l.Into)
		}
		return out
	}

	shared := sharedStore(t, map[string]string{
		"a": "issue: I-1\ntickets:\n  - id: T-1\n    title: t\n",
		"b": "issue: I-2\ntickets:\n  - id: T-1\n    title: t\n",
	})
	write(shared)
	if got, want := events(ReadFor(shared, "T-1", "I-1")), []string{"gate-clean>", "pr-body>I-1", "publish-approved>I-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("shared, I-1: %v, want %v", got, want)
	}
	if got, want := events(ReadFor(shared, "T-1", "I-2")), []string{"gate-clean>", "pr>I-2", "publish-done>I-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("shared, I-2: %v, want %v", got, want)
	}
	all, _ := Read(shared, "T-1")
	if got, _ := ReadFor(shared, "T-1", ""); !reflect.DeepEqual(got, all) {
		t.Errorf("no issue: %d lines, want Read's %d", len(got), len(all))
	}

	one := sharedStore(t, map[string]string{"a": "issue: I-1\ntickets:\n  - id: T-1\n    title: t\n"})
	write(one)
	oneAll, _ := Read(one, "T-1")
	if got, _ := ReadFor(one, "T-1", "I-1"); !reflect.DeepEqual(got, oneAll) {
		t.Errorf("one container: %d lines, want Read's %d", len(got), len(oneAll))
	}
}
