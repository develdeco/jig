package tracker_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/tracker"
)

func TestGraduation(t *testing.T) {
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := tracker.Graduation{
		Epic: "Do the epic thing",
		Tickets: []tracker.Draft{
			{Title: "Slice A", Body: "First slice"},
			{Title: "Slice B", Body: "Depends on A", BlockedBy: []string{"#1"}},
			{Title: "Slice C", Body: "Depends on A and B", BlockedBy: []string{"#1", "#2"}},
		},
	}

	ids, err := tracker.Graduate(a, st, g)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}
	want := []string{"JIG-1", "JIG-2", "JIG-3"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i, id := range ids {
		if id != want[i] {
			t.Fatalf("ids[%d] = %q, want %q", i, id, want[i])
		}
		if _, err := os.Stat(st.TicketDir(id)); err != nil {
			t.Fatalf("ticket folder missing for %s: %v", id, err)
		}
	}

	// Slice B's blocking link must resolve "#1" to JIG-1.
	subtasksB := readSubtasks(t, st, ids[1])
	if len(subtasksB) != 1 || len(subtasksB[0].BlockedBy) != 1 || subtasksB[0].BlockedBy[0] != "JIG-1" {
		t.Fatalf("JIG-2 subtasks = %+v, want blocked_by [JIG-1]", subtasksB)
	}

	// Slice C's blocking links must resolve "#1","#2" to JIG-1,JIG-2.
	subtasksC := readSubtasks(t, st, ids[2])
	if len(subtasksC) != 1 || len(subtasksC[0].BlockedBy) != 2 ||
		subtasksC[0].BlockedBy[0] != "JIG-1" || subtasksC[0].BlockedBy[1] != "JIG-2" {
		t.Fatalf("JIG-3 subtasks = %+v, want blocked_by [JIG-1 JIG-2]", subtasksC)
	}

	// Slice A has no blocked_by.
	subtasksA := readSubtasks(t, st, ids[0])
	if len(subtasksA) != 1 || len(subtasksA[0].BlockedBy) != 0 {
		t.Fatalf("JIG-1 subtasks = %+v, want empty blocked_by", subtasksA)
	}
}

func readSubtasks(t *testing.T, st interface{ TicketDir(string) string }, id string) []tracker.Subtask {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(st.TicketDir(id), "tracker", "subtasks.yaml"))
	if err != nil {
		t.Fatalf("read subtasks.yaml for %s: %v", id, err)
	}
	var sf struct {
		Subtasks []tracker.Subtask `yaml:"subtasks"`
	}
	if err := yaml.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parse subtasks.yaml for %s: %v", id, err)
	}
	return sf.Subtasks
}
