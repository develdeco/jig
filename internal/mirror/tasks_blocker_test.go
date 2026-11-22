package mirror

import "testing"

// TestMirrorTasksOffKeepsAListedTaskBlocker: a carried ticket whose blocked_by
// lists a task of a chart with an issue keeps the edge to the task's issue
// when tasks go off, though the sync no longer carries the task, and no
// blocked_by drift is reported.
func TestMirrorTasksOffKeepsAListedTaskBlocker(t *testing.T) {
	w := newTasksWorld(t)
	if err := w.st.SetTicketDep("DEMO-4", "DEMO-2", "merged"); err != nil {
		t.Fatal(err)
	}
	_, client := w.sync(true)
	if !containsString(client.blockedByOf["NODE_Legacy ticket"], "NODE_First task") {
		t.Fatalf("blockedByOf = %v, want the edge linked while tasks were on", client.blockedByOf)
	}

	report, client := w.sync(false)
	if driftHas(report.Drift, "DEMO-4", "blocked_by") {
		t.Errorf("Drift = %+v, want no blocked_by drift for a listed blocker", report.Drift)
	}
	if len(client.removedBlockedBys) != 0 || !containsString(client.blockedByOf["NODE_Legacy ticket"], "NODE_First task") {
		t.Errorf("removed %+v, blockedByOf = %v, want the edge kept", client.removedBlockedBys, client.blockedByOf)
	}
}
