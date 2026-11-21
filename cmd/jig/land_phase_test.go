package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/ready"
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

// sharedLandRig is the land rig with a second issue TK-7 that lists TK-2 too,
// published into both issues: pull request 2 into TK-1 and 12 into TK-7.
func sharedLandRig(t *testing.T) chartRig {
	t.Helper()
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
	return r
}

// sharedHost sets pull request 2 (into TK-1) and 12 (into TK-7) of the fake gh,
// each open or merged.
func sharedHost(t *testing.T, r chartRig, merged2, merged12 bool) {
	t.Helper()
	rec, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	b1, err := r.st.ResolveTicketBranch("TK-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	rec7, err := r.st.ReadTicket("TK-7")
	if err != nil {
		t.Fatal(err)
	}
	b7, err := r.st.ResolveTicketBranch("TK-7", rec7, "main")
	if err != nil {
		t.Fatal(err)
	}
	pull := func(n int, base string, merged bool) fixture.GhPull {
		p := fixture.GhPull{Number: n, Base: base}
		if merged {
			p.State, p.Merged = "closed", true
		}
		return p
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, pull(2, b1, merged2), pull(12, b7, merged12)))
}

// TestLandPhaseNextNamesEachIssueOfASharedTask: a shared task published into
// two issues names one `jig land T --issue I` for each issue not landed, never a
// bare `jig land T`, says per issue where its landing stands (merged on the
// host in one, waiting in the other), and a landing with no issue is refused.
func TestLandPhaseNextNamesEachIssueOfASharedTask(t *testing.T) {
	r := sharedLandRig(t)
	sharedHost(t, r, false, false)
	out, _ := r.run(t, "next", "TK-2")
	for _, want := range []string{"jig land TK-2 --issue TK-1", "jig land TK-2 --issue TK-7"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig next TK-2 does not name %q:\n%s", want, out)
		}
	}
	bare := func(out string) {
		t.Helper()
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "jig land TK-2") && !strings.Contains(line, "--issue") {
				t.Errorf("a command for a shared task names no issue: %q", line)
			}
		}
	}
	bare(out)
	if status, _ := r.run(t, "status", "TK-2"); true {
		bare(status)
	}
	if out, code := r.run(t, "land", "TK-2"); code == 0 || !strings.Contains(out, "SHARED_TASK_ISSUE_NEEDED") {
		t.Errorf("land TK-2 with no issue: exit %d, want SHARED_TASK_ISSUE_NEEDED\n%s", code, out)
	}

	sharedHost(t, r, true, false)
	out, _ = r.run(t, "next", "TK-2")
	for _, want := range []string{"TK-2 into TK-1 merged on the host, not recorded", "TK-2 into TK-7 waits for a merge"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig next TK-2 does not say %q:\n%s", want, out)
		}
	}
}

// perIssueMerges says each issue's pull request of a shared task merged or not.
type perIssueMerges struct{ merged map[string]bool }

func (f perIssueMerges) Of(string) (ready.Merge, error) { return ready.Merge{}, nil }
func (f perIssueMerges) OfFor(_, issue string) (ready.Merge, error) {
	return ready.Merge{Published: true, Repos: []ready.RepoMerge{{Met: f.merged[issue], PR: "https://github.example/owner/repo/pull/2"}}}, nil
}

// TestLandPhaseOfASharedTaskIsReadForTheIssue: the phase of a shared task read
// for one issue is merged on the host, not recorded when that issue's pull
// request merged, and waits for a merge when the other's has not.
func TestLandPhaseOfASharedTaskIsReadForTheIssue(t *testing.T) {
	r := sharedLandRig(t)
	merges := perIssueMerges{merged: map[string]bool{"TK-1": true, "TK-7": false}}
	for issue, want := range map[string]bool{"TK-1": true, "TK-7": false} {
		tk, err := derivePhaseFor(r.st, "TK-2", merges, issue)
		if err != nil {
			t.Fatal(err)
		}
		if tk.Current != phase.Land || tk.MergedOnHost() != want {
			t.Errorf("TK-2 read for %s: current %s, merged on the host %v, want land and %v", issue, tk.Current, tk.MergedOnHost(), want)
		}
	}
}

// TestLandVerbSharedTaskLandsPerIssueAndNamesItsOwnMerge: a merge named for one
// issue is the session's own landing of that issue alone; the other's, merged on
// the host and never named, is recorded as the host's. The help of a merge named
// for an issue says to run `jig land` again with its --issue.
func TestLandVerbSharedTaskLandsPerIssueAndNamesItsOwnMerge(t *testing.T) {
	r := sharedLandRig(t)
	sharedHost(t, r, false, false)
	out, code := r.run(t, "land", "TK-2", "--issue", "TK-1")
	if code != 0 || !strings.Contains(out, "run `jig land TK-2 --issue TK-1` again") {
		t.Fatalf("land TK-2 --issue TK-1: exit %d, want its merge named and the issue repeated\n%s", code, out)
	}
	sharedHost(t, r, true, true)
	if out, code = r.run(t, "land", "TK-2", "--issue", "TK-7"); code != 0 {
		t.Fatalf("land TK-2 --issue TK-7: exit %d\n%s", code, out)
	}
	if out, code = r.run(t, "land", "TK-2", "--issue", "TK-1"); code != 0 {
		t.Fatalf("land TK-2 --issue TK-1: exit %d\n%s", code, out)
	}
	got := map[string]string{}
	for _, l := range landedLines(t, r, "TK-2") {
		got[l.Into] = l.Provenance
	}
	if got["TK-1"] != journal.ProvenanceAgent || got["TK-7"] != journal.ProvenanceHost || len(got) != 2 {
		t.Errorf("landed lines = %v, want TK-1 by the agent that named it and TK-7 by the host", got)
	}
}
