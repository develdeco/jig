package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestGraduateKindRecordsOnExistingEntries: an existing ticket with no kind
// takes its entry's kind and parent; one that records a kind keeps it, and a
// disagreeing entry is a listed problem; a legacy chart changes nothing.
func TestGraduateKindRecordsOnExistingEntries(t *testing.T) {
	t.Parallel()
	jig, root := setupGraduateStore(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"ticket", "new", "--title", "Need", "--kind", "issue"},
		{"ticket", "new", "--title", "Old group"},
		{"ticket", "new", "--title", "Old part"},
	} {
		if code, out := jig(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
	}
	writeChart(t, root, "plan", "issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: Old group\n    kind: multi-task\n  - id: DEMO-3\n    title: Old part\n    under: DEMO-2\n  - title: New part\n    under: \"#1\"\n")
	if code, out := jig("graduate", "plan"); code != 0 {
		t.Fatalf("graduate: exit %d\n%s", code, out)
	}
	group, _ := st.ReadTicket("DEMO-2")
	part, _ := st.ReadTicket("DEMO-3")
	fresh, _ := st.ReadTicket("DEMO-4")
	if group.Kind != store.KindMultiTask || part.Parent != "DEMO-2" || part.Kind != "" || fresh.Parent != "DEMO-2" {
		t.Errorf("group %+v, part %+v, new %+v: want the multi-task recorded and both tasks under it", group, part, fresh)
	}

	// A ticket that records a kind keeps it: an entry that says otherwise is a problem.
	writeChart(t, root, "clash", "issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: Old group\n    kind: task\n")
	code, out := jig("validate", "clash")
	if code == 0 || !strings.Contains(out, `has kind "task" but its ticket DEMO-2 is a multi-task`) {
		t.Errorf("validate clash: exit %d\n%s", code, out)
	}

	// A task listed before the multi-task it sits under cannot be minted first.
	writeChart(t, root, "late", "issue: DEMO-1\ntickets:\n  - title: Part\n    under: \"#2\"\n  - title: Group\n    kind: multi-task\n")
	code, out = jig("validate", "late")
	if code == 0 || !strings.Contains(out, "is under entry 2, which must come before it") {
		t.Errorf("validate late: exit %d\n%s", code, out)
	}
}

// TestGraduateKindLegacyChartMintsAsBefore: a chart with no issue mints tickets
// with no kind and no parent.
func TestGraduateKindLegacyChartMintsAsBefore(t *testing.T) {
	t.Parallel()
	jig, root := setupGraduateStore(t)
	writeChart(t, root, "old", "tickets:\n  - title: A\n  - title: B\n")
	if code, out := jig("graduate", "old"); code != 0 {
		t.Fatalf("graduate: exit %d\n%s", code, out)
	}
	st, _ := store.Open(root)
	for _, id := range []string{"DEMO-1", "DEMO-2"} {
		rec, err := st.ReadTicket(id)
		if err != nil || rec.Kind != "" || rec.Parent != "" {
			t.Errorf("%s = %+v, %v, want no kind and no parent", id, rec, err)
		}
	}
}

// TestKindNextListsNoIssueOrMultiTaskAsReady: jig next lists an issue and a
// multi-task by kind, never in the order to start, and jig next <ticket> and
// jig status <ticket> print their kind and children with no prompt.
func TestKindNextListsNoIssueOrMultiTaskAsReady(t *testing.T) {
	t.Parallel()
	jig, root := setupGraduateStore(t)
	for _, args := range [][]string{
		{"ticket", "new", "--title", "Need", "--kind", "issue"},
		{"ticket", "new", "--title", "Group", "--kind", "multi-task"},
		{"ticket", "new", "--title", "Part"},
	} {
		if code, out := jig(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
	}
	st, _ := store.Open(root)
	if err := st.SetTicketKind("DEMO-3", store.KindTask, "DEMO-2"); err != nil {
		t.Fatal(err)
	}
	writeChart(t, root, "plan", "issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: Group\n    kind: multi-task\n  - id: DEMO-3\n    title: Part\n    under: DEMO-2\n")

	code, out := jig("next", "--json")
	if code != 0 {
		t.Fatalf("jig next: exit %d\n%s", code, out)
	}
	var list struct {
		Tickets []struct{ ID, State, Kind string } `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{"DEMO-1": {"issue", "issue"}, "DEMO-2": {"multi-task", "multi-task"}}
	for _, r := range list.Tickets {
		if w, ok := want[r.ID]; ok && (r.State != w[0] || r.Kind != w[1]) {
			t.Errorf("%s row = %+v, want state and kind %v", r.ID, r, w)
		}
	}

	for _, verb := range []string{"next", "status"} {
		code, out = jig(verb, "DEMO-2")
		if code != 0 || strings.Contains(out, "prompt:") || !strings.Contains(out, "kind: multi-task") || !strings.Contains(out, "DEMO-3") {
			t.Errorf("jig %s DEMO-2: exit %d\n%s", verb, code, out)
		}
	}
	code, out = jig("next", "DEMO-1")
	if code != 0 || !strings.Contains(out, "chart: plan") || !strings.Contains(out, "DEMO-2") {
		t.Errorf("jig next DEMO-1: exit %d\n%s", code, out)
	}
	code, out = jig("next", "DEMO-3")
	if code != 0 || !strings.Contains(out, "parent: DEMO-2") {
		t.Errorf("jig next DEMO-3: exit %d\n%s", code, out)
	}
}
