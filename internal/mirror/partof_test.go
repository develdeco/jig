package mirror

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func bodyByTitle(c *fakeClient, title string) string {
	for _, is := range c.issues {
		if is.Title == title {
			return is.Body
		}
	}
	return "<no issue titled " + title + ">"
}

func numberOf(r SyncReport, what string) int {
	for _, c := range r.Created {
		if c.What == what {
			return c.Number
		}
	}
	return 0
}

// TestMirrorPartOfNamesTheNeedAndTheMultiTask: with the setting on, a task's
// issue starts with a Part of line for the need's issue, and for its
// multi-task's too, on the very first sync, though the multi-task is made
// after the task. The issue and a legacy ticket carry none.
func TestMirrorPartOfNamesTheNeedAndTheMultiTask(t *testing.T) {
	w := newTasksWorld(t)
	if err := w.st.CreateTicketRecord("DEMO-5", store.Ticket{Title: "A multi-task", Kind: store.KindMultiTask}); err != nil {
		t.Fatal(err)
	}
	if err := w.st.SetTicketKind("DEMO-3", store.KindTask, "DEMO-5"); err != nil {
		t.Fatal(err)
	}
	tickets := "issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: First task\n  - id: DEMO-5\n    title: A multi-task\n    kind: multi-task\n  - id: DEMO-3\n    title: Second task\n    under: DEMO-5\n"
	if err := os.WriteFile(w.st.ChartFile("plan"), []byte(tickets), 0o644); err != nil {
		t.Fatal(err)
	}

	report, client := w.sync(true)
	need, multi := numberOf(report, "DEMO-1"), numberOf(report, "DEMO-5")
	if need == 0 || multi == 0 {
		t.Fatalf("Created = %+v, want DEMO-1 and DEMO-5", report.Created)
	}
	first := fmt.Sprintf("Part of #%d\n\nBuild the first part.", need)
	if got := bodyByTitle(client, "First task"); !strings.HasPrefix(got, first) {
		t.Errorf("first task's body = %q, want it to start %q", got, first)
	}
	second := fmt.Sprintf("Part of #%d\nPart of #%d\n\nBuild the second part.", need, multi)
	if got := bodyByTitle(client, "Second task"); !strings.HasPrefix(got, second) {
		t.Errorf("second task's body = %q, want it to start %q", got, second)
	}
	for _, title := range []string{"The need", "Legacy ticket"} {
		if got := bodyByTitle(client, title); strings.Contains(got, "Part of") {
			t.Errorf("%s's body = %q, want no Part of line", title, got)
		}
	}
	// A second sync changes nothing.
	again, _ := w.sync(true)
	if len(again.Updated) != 0 {
		t.Errorf("second sync updated %+v", again.Updated)
	}
}

// TestMirrorPartOfWaitsForTheNeedsIssue: a task whose need has no issue yet
// has no line, and the next sync adds it.
func TestMirrorPartOfWaitsForTheNeedsIssue(t *testing.T) {
	w := newTasksWorld(t)
	st := w.st
	rec, err := st.ReadTicket("DEMO-2")
	if err != nil {
		t.Fatal(err)
	}
	line, err := partOfLine(st, w.cfg(true).Cfg, "DEMO-2", rec)
	if err != nil {
		t.Fatal(err)
	}
	if line != "" {
		t.Fatalf("line = %q before the need has an issue, want none", line)
	}
	report, _ := w.sync(true)
	line, err = partOfLine(st, w.cfg(true).Cfg, "DEMO-2", rec)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("Part of #%d", numberOf(report, "DEMO-1")); line != want {
		t.Errorf("line = %q, want %q", line, want)
	}
}
