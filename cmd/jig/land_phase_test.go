package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// nextPhaseOf is the phase block of `jig next <ticket> --json`.
func nextPhaseOf(t *testing.T, r chartRig, ticket string) nextPhase {
	t.Helper()
	out, code := r.run(t, "next", ticket, "--json")
	var n struct {
		Phase *nextPhase `json:"phase"`
	}
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil || n.Phase == nil {
		t.Fatalf("jig next %s --json: exit %d, %v\n%s", ticket, code, err, out)
	}
	return *n.Phase
}

// TestLandPhaseNext: a published, unlanded ticket names Land as its prompt (the
// code stays PHASE_DONE), says merged on the host once the host reports it
// merged, and is a published ticket again once `jig land` has recorded it.
func TestLandPhaseNext(t *testing.T) {
	r := landRig(t)
	r.host(t, false, false)
	p := nextPhaseOf(t, r, "TK-2")
	if p.Name != "land" || p.Code != "PHASE_DONE" || p.Prompt != "Land TK-2" {
		t.Fatalf("published: %+v, want land, PHASE_DONE, Land TK-2", p)
	}
	if len(p.Phases) != 5 || p.Phases[4].Name != "land" || p.Phases[4].Text != "waits for a merge" {
		t.Errorf("phases = %+v", p.Phases)
	}
	if out, _ := r.run(t, "next", "TK-2"); !strings.Contains(out, "Land TK-2") {
		t.Errorf("jig next TK-2 does not name Land TK-2:\n%s", out)
	}

	r.host(t, true, false)
	p = nextPhaseOf(t, r, "TK-2")
	if p.Name != "land" || p.Phases[4].Text != "merged on the host, not recorded" {
		t.Errorf("merged on the host: %+v", p)
	}
	if out, _ := r.run(t, "next", "TK-2"); !strings.Contains(out, "jig land TK-2") || !strings.Contains(out, "not recorded") {
		t.Errorf("jig next TK-2 does not say jig land TK-2 records it:\n%s", out)
	}

	if out, code := r.run(t, "land", "TK-2"); code != 0 {
		t.Fatalf("land TK-2: exit %d\n%s", code, out)
	}
	p = nextPhaseOf(t, r, "TK-2")
	if p.Name != "publish" || p.Code != "PHASE_DONE" || p.Prompt != "" || p.Phases[4].State != "done" {
		t.Errorf("landed: %+v, want a published ticket", p)
	}
}

// TestLandPhaseNextNamesEachIssueOfASharedTask: a shared task published into
// two issues names one `jig land T --issue I` for each issue not landed, and a
// landing with no issue is refused.
func TestLandPhaseNextNamesEachIssueOfASharedTask(t *testing.T) {
	r := landRig(t)
	r.ticket(t, "TK-7", "kind: issue\n", false)
	r.chart(t, "second", "issue: TK-7\ntickets:\n  - id: TK-2\n    title: Two\n", "")
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "TK-7", URL: "https://github.example/owner/repo/pull/12"},
		{Event: journal.EventPublishDone, Into: "TK-7"},
	} {
		if err := journal.Append(r.st, "TK-2", l); err != nil {
			t.Fatal(err)
		}
	}
	r.host(t, false, false)
	pulls := os.Getenv("GH_STUB_PULLS")
	t.Setenv("GH_STUB_PULLS", strings.TrimSuffix(pulls, "]")+","+fixture.GhPulls(t, fixture.GhPull{Number: 12, Base: "main"})[1:])
	out, _ := r.run(t, "next", "TK-2")
	for _, want := range []string{"jig land TK-2 --issue TK-1", "jig land TK-2 --issue TK-7"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig next TK-2 does not name %q:\n%s", want, out)
		}
	}
	if out, code := r.run(t, "land", "TK-2"); code == 0 || !strings.Contains(out, "SHARED_TASK_ISSUE_NEEDED") {
		t.Errorf("land TK-2 with no issue: exit %d, want SHARED_TASK_ISSUE_NEEDED\n%s", code, out)
	}
}
