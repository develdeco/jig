package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestLandVerbMultiTaskLandsAfterItsTasks: a multi-task's pull request waits
// for its task (refused, naming it); landing the task says the multi-task is
// the next landing, and the multi-task then names its merge into the issue's
// branch.
func TestLandVerbMultiTaskLandsAfterItsTasks(t *testing.T) {
	r := landRig(t)
	for id, url := range map[string]string{"TK-4": "https://github.example/owner/repo/pull/4", "TK-5": "https://github.example/owner/repo/pull/5"} {
		into := "TK-1"
		if id == "TK-5" {
			into = "TK-4"
		}
		for _, l := range []journal.Line{
			{Event: journal.EventPr, Into: into, URL: url},
			{Event: journal.EventPublishDone, Into: into},
		} {
			if err := journal.Append(r.st, id, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	branch := func(id string) string {
		rec, err := r.st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.st.ResolveTicketBranch(id, rec, "main")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pulls := func(merged5 bool) {
		p5 := fixture.GhPull{Number: 5, Base: branch("TK-4")}
		if merged5 {
			p5.State, p5.Merged = "closed", true
		}
		t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 4, Base: branch("TK-1")}, p5))
	}

	pulls(false)
	if out, code := r.run(t, "land", "TK-4"); code == 0 || !strings.Contains(out, "LAND_WAITS") || !strings.Contains(out, "TK-5") {
		t.Fatalf("land TK-4 before its task: exit %d, want LAND_WAITS naming TK-5\n%s", code, out)
	}
	pulls(true)
	out, code := r.run(t, "land", "TK-5")
	if code != 0 || !strings.Contains(out, "jig land TK-4") {
		t.Fatalf("land TK-5: exit %d, want the multi-task named as the next landing\n%s", code, out)
	}
	out, code = r.run(t, "land", "TK-4")
	if code != 0 || !strings.Contains(out, "gh pr merge https://github.example/owner/repo/pull/4 --squash") {
		t.Errorf("land TK-4 after its task: exit %d, want its merge named\n%s", code, out)
	}
}
