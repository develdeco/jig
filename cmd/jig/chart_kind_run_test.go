package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestKindChartBriefsAndRunsOnlyTasks: a chart of an issue, a multi-task of two
// tasks and a task graduates, and its standing briefs and runs the three tasks
// only; graduate's closing help names the tasks, and jig validate of the issue
// and of the multi-task passes with no brief.
func TestKindChartBriefsAndRunsOnlyTasks(t *testing.T) {
	t.Parallel()
	jig, root := setupGraduateStore(t)
	if code, out := jig("ticket", "new", "--title", "Need", "--kind", "issue"); code != 0 {
		t.Fatalf("issue: exit %d\n%s", code, out)
	}
	writeChart(t, root, "plan", "issue: DEMO-1\ntickets:\n  - title: Group\n    kind: multi-task\n  - title: First\n    under: \"#1\"\n  - title: Second\n    under: \"#1\"\n  - title: Alone\n")
	code, grad := jig("graduate", "plan")
	if code != 0 {
		t.Fatalf("graduate: exit %d\n%s", code, grad)
	}
	var help string
	for _, l := range strings.Split(grad, "\n") {
		if strings.Contains(l, "Write the brief.md") {
			help = l
		}
	}
	if !strings.Contains(help, "DEMO-3, DEMO-4, DEMO-5") || strings.Contains(help, "DEMO-2") {
		t.Errorf("graduate's help = %q, want it to name the tasks DEMO-3, DEMO-4, DEMO-5 and not DEMO-2", help)
	}

	code, out := jig("next", "plan", "--json")
	if code != 0 {
		t.Fatalf("jig next plan: exit %d\n%s", code, out)
	}
	var c struct {
		Briefs []string   `json:"briefs"`
		Run    [][]string `json:"run"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err)
	}
	if want := []string{"DEMO-3", "DEMO-4", "DEMO-5"}; !reflect.DeepEqual(c.Briefs, want) {
		t.Errorf("briefs = %v, want %v", c.Briefs, want)
	}
	for _, wave := range c.Run {
		for _, id := range wave {
			if id == "DEMO-2" {
				t.Errorf("run = %v holds the multi-task DEMO-2", c.Run)
			}
		}
	}
	if code, out := jig("status", "plan"); code != 0 || strings.Contains(out, "Plan DEMO-2") {
		t.Errorf("jig status plan: exit %d, offers DEMO-2 for a brief:\n%s", code, out)
	}

	for _, id := range []string{"DEMO-1", "DEMO-2"} {
		code, out := jig("validate", id)
		if code != 0 || strings.Contains(out, "brief.md") {
			t.Errorf("jig validate %s: exit %d\n%s", id, code, out)
		}
	}
}
