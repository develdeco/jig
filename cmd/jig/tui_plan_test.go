package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestTheListingNamesTheIssueOfAChart: the charts of the store listing carry the
// issue each plans, which is how the screens open an issue on its plan, and
// carry none for a chart that plans none.
func TestTheListingNamesTheIssueOfAChart(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	out, code := r.run(t, "next", "--json")
	var roster struct {
		Charts []struct {
			Name  string `json:"name"`
			Issue string `json:"issue"`
		} `json:"charts"`
	}
	if err := json.Unmarshal([]byte(out), &roster); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
	}
	got := map[string]string{}
	for _, c := range roster.Charts {
		got[c.Name] = c.Issue
	}
	if want := map[string]string{"plan": "TK-1", "old": ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("the listing's charts name the issues %v, want %v", got, want)
	}
}

// TestTUIWithAnIssueOpensItsPlan: `jig tui <issue>` prints the plan of the
// issue, the screen of the chart that plans it, never the task-shaped ticket
// screen with a Brief, a Build, a Review and a Publish row; the plan opens as
// well when the chart is named.
func TestTUIWithAnIssueOpensItsPlan(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	r.put(t, "tickets/TK-1/ticket.yaml", "schema_version: 1\ntitle: Ticket TK-1\nkind: issue\nbody: |\n  The issue is that tickets of a chart read apart.\n")
	for _, word := range []string{"TK-1", "plan"} {
		keys, out, wait := startTUI(t, r.env(), word, "--store", r.root)
		out.until(t, "The issue is that tickets of a chart read apart.")
		out.until(t, "Tasks")
		out.until(t, "TK-2")
		frame := out.String()
		for _, want := range []string{"Need", "Issue", "branch", "suite", "landing"} {
			if !strings.Contains(frame, want) {
				t.Errorf("jig tui %s: the frames lack %q:\n%s", word, want, frame)
			}
		}
		for _, bad := range []string{"1 Brief", "2 Build", "3 Review", "4 Publish"} {
			if strings.Contains(frame, bad) {
				t.Errorf("jig tui %s: the frames have the task screen's %q:\n%s", word, bad, frame)
			}
		}
		keys.Write([]byte("q"))
		if code := wait(); code != 0 {
			t.Fatalf("jig tui %s: exit %d\n%s", word, code, out.String())
		}
	}
}
