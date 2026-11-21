package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestKindChartMultiTaskRowOffersNothing: in the standing of a model chart the
// row of a multi-task offers no prompt and is not ready, the chart reads done
// once its tasks are, and jig validate of a multi-task still reports a problem
// of its record.
func TestKindChartMultiTaskRowOffersNothing(t *testing.T) {
	t.Parallel()
	jig, root := setupGraduateStore(t)
	if code, out := jig("ticket", "new", "--title", "Need", "--kind", "issue"); code != 0 {
		t.Fatalf("issue: exit %d\n%s", code, out)
	}
	writeChart(t, root, "plan", "issue: DEMO-1\ntickets:\n  - title: Group\n    kind: multi-task\n  - title: First\n    under: \"#1\"\n  - title: Second\n    under: \"#1\"\n  - title: Alone\n")
	if code, out := jig("graduate", "plan"); code != 0 {
		t.Fatalf("graduate: exit %d\n%s", code, out)
	}

	type standing struct {
		Code    string `json:"code"`
		Ready   bool   `json:"ready"`
		Tickets []struct {
			ID     string `json:"id"`
			Prompt string `json:"prompt"`
			Ready  bool   `json:"ready"`
		} `json:"tickets"`
	}
	read := func() standing {
		code, out := jig("next", "plan", "--json")
		if code != 0 {
			t.Fatalf("jig next plan: exit %d\n%s", code, out)
		}
		var s standing
		if err := json.Unmarshal([]byte(out), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := read()
	found := false
	for _, r := range s.Tickets {
		if r.ID == "DEMO-2" {
			found = true
			if r.Prompt != "" || r.Ready {
				t.Errorf("the multi-task row = %+v, want no prompt and not ready", r)
			}
		}
	}
	if !found {
		t.Fatalf("no row for DEMO-2: %+v", s.Tickets)
	}

	for _, id := range []string{"DEMO-3", "DEMO-4", "DEMO-5"} {
		if code, out := jig("close", id, "--reason", "covered", "--by", "somewhere#1"); code != 0 {
			t.Fatalf("close %s: exit %d\n%s", id, code, out)
		}
	}
	// A model chart is done once its issue has landed, not once its tasks are:
	// with every task closed the issue's own publish is the run's work.
	if s := read(); s.Code == "CHART_DONE" || !s.Ready {
		t.Errorf("code = %q ready %v after every task closed, want the issue's publish to be the run's work", s.Code, s.Ready)
	}

	// A record problem of a multi-task is reported: a parent on it is wrong.
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTicketKind("DEMO-2", "", "DEMO-1"); err != nil {
		t.Fatal(err)
	}
	code, out := jig("validate", "DEMO-2")
	if code == 0 || !strings.Contains(out, "only a task has a parent") {
		t.Errorf("jig validate DEMO-2 with a parent: exit %d\n%s", code, out)
	}
}
