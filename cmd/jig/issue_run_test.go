package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/phase"
)

// issueRunRig is an issue TK-1 of a chart of two dependent tasks, TK-2 and TK-3
// (which waits for TK-2 to merge), in a project of one repo, with the fake gh as
// the pull-request host. Both briefs are approved; nothing has published.
func issueRunRig(t *testing.T) chartRig {
	t.Helper()
	r := newChartRig(t)
	r.put(t, "project.yaml", "schema_version: 2\nname: demo\nkeys:\n  TK: everything in demo\ntrackers: []\nrepos:\n  - remote: /srv/app.git\n    label: app\n")
	r.ticket(t, "TK-1", "kind: issue\n", false)
	r.ticket(t, "TK-2", "", true)
	r.ticket(t, "TK-3", "blocked_by:\n  - ticket: TK-2\n    kind: merged\n", true)
	r.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-3\n    title: Three\n", "")
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "fixture@example.invalid"}, {"config", "user.name", "fixture"}, {"add", "-A"}, {"commit", "-m", "store"}} {
		if _, err := gitx.Run(r.root, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fixture.GhStub(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return r
}

// publishInto records ticket's pull request number n into container and its
// publish, as `jig publish` does.
func (r chartRig) publishInto(t *testing.T, ticket, container string, n string) {
	t.Helper()
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: container, URL: "https://github.example/owner/repo/pull/" + n},
		{Event: journal.EventPublishDone, Into: container},
	} {
		if err := journal.Append(r.st, ticket, l); err != nil {
			t.Fatal(err)
		}
	}
}

// issueHost sets the fake gh's pull requests: 2 and 3 into the issue's branch,
// 21 (the issue's own) into main, each open or merged.
func (r chartRig) issueHost(t *testing.T, merged2, merged3, merged21 bool) {
	t.Helper()
	rec, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := r.st.ResolveTicketBranch("TK-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	pull := func(n int, base string, merged bool) fixture.GhPull {
		p := fixture.GhPull{Number: n, Base: base}
		if merged {
			p.State, p.Merged, p.MergeCommit = "closed", true, "c0ffee"+string(rune('0'+n%10))
		}
		return p
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, pull(2, branch, merged2), pull(3, branch, merged3), pull(21, "main", merged21)))
}

// issueRun is `jig next TK-1 --json`: the issue asked for its run.
func (r chartRig) issueRun(t *testing.T) nextChart {
	t.Helper()
	out, code := r.run(t, "next", "TK-1", "--json")
	var n nextChart
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		t.Fatalf("jig next TK-1 --json: exit %d, %v\n%s", code, err, out)
	}
	return n
}

func landTickets(n nextChart) []string {
	var out []string
	for _, l := range n.Lands {
		out = append(out, l.Ticket)
	}
	return out
}

// TestIssueRunStopsAtEachLandingEndToEnd: an issue of two dependent tasks, read
// through `jig next <issue>` against the fake host. The run stops at Land TK-2
// with TK-3 an end behind it; a merge made on the host is a row to record, and
// TK-3 runs; after the record the run stops at Land TK-3; then the issue's
// suite, its publish, its own landing, and a landed issue is done.
func TestIssueRunStopsAtEachLandingEndToEnd(t *testing.T) {
	r := issueRunRig(t)
	r.publishInto(t, "TK-2", "TK-1", "2")
	r.issueHost(t, false, false, false)

	// The prompt is in the issue's id, and the folder name answers the same.
	n := r.issueRun(t)
	if n.Chart != "plan" || n.Code != phase.CodeChartWaitsLand || n.Ready || n.Prompt != "Continue TK-1" {
		t.Fatalf("stop at TK-2: chart %q code %s ready %v prompt %q", n.Chart, n.Code, n.Ready, n.Prompt)
	}
	if got := landTickets(n); len(got) != 1 || n.Lands[0].Prompt != "Land TK-2" || n.Lands[0].State != "waits for a merge" || len(n.Lands[0].After) != 0 {
		t.Errorf("lands = %+v", n.Lands)
	}
	if len(n.Ends) != 1 || n.Ends[0].Ticket != "TK-3" || !strings.Contains(n.Ends[0].Fact, "waits for TK-2 to land into") {
		t.Errorf("ends = %+v, want TK-3 behind the landing", n.Ends)
	}
	if n.Issue == nil || n.Issue.Stage != phase.IssueLanding || n.Issue.Ticket != "TK-1" || n.Issue.Branch == "" || len(n.Issue.Stack) != 1 {
		t.Errorf("issue = %+v, want the landing stage with its branch", n.Issue)
	}
	text, code := r.run(t, "next", "TK-1")
	for _, want := range []string{"code: CHART_WAITS_LAND", "lands[1]{ticket,prompt,after,state}:", "Land TK-2 (or merge it on the host), then ask: Continue TK-1", "TK-3 waits for TK-2 to land into " + n.Issue.Branch} {
		if code != 0 || !strings.Contains(text, want) {
			t.Errorf("jig next TK-1 (exit %d) lacks %q:\n%s", code, want, text)
		}
	}
	if byName, _ := r.run(t, "next", "plan"); !strings.Contains(byName, "prompt: Continue TK-1") {
		t.Errorf("jig next plan does not ask in the issue's id:\n%s", byName)
	}

	// A merge made on the host is a record to make, and the run goes on.
	r.issueHost(t, true, false, false)
	n = r.issueRun(t)
	if !n.Ready || len(n.Run) != 1 || n.Run[0][0] != "TK-3" || n.Prompt != "Continue TK-1" {
		t.Fatalf("merged on the host: ready %v run %v prompt %q", n.Ready, n.Run, n.Prompt)
	}
	if len(n.Lands) != 1 || n.Lands[0].State != "merged on the host, not recorded" {
		t.Errorf("lands = %+v, want TK-2 merged on the host, not recorded", n.Lands)
	}
	if out, code := r.run(t, "land", "TK-2"); code != 0 {
		t.Fatalf("jig land TK-2: exit %d\n%s", code, out)
	}
	n = r.issueRun(t)
	if len(n.Lands) != 0 || len(n.Run) != 1 || n.Run[0][0] != "TK-3" {
		t.Fatalf("after Land TK-2: lands %v run %v", n.Lands, n.Run)
	}

	// TK-3 published: the run stops at Land TK-3; a task that does not depend on
	// a held landing is not held (nothing else is left here).
	r.publishInto(t, "TK-3", "TK-1", "3")
	r.issueHost(t, true, false, false)
	n = r.issueRun(t)
	if n.Code != phase.CodeChartWaitsLand || n.Reason != "TK-3 waits for the person's word: Land TK-3" || len(n.Lands) != 1 {
		t.Fatalf("stop at TK-3: %s %q %v", n.Code, n.Reason, landTickets(n))
	}
	r.issueHost(t, true, true, false)
	if out, code := r.run(t, "land", "TK-3"); code != 0 {
		t.Fatalf("jig land TK-3: exit %d\n%s", code, out)
	}

	// Every ticket landed: the suite is due, its worktree named, and the run is
	// the main session's.
	n = r.issueRun(t)
	if !n.Ready || n.Issue == nil || n.Issue.Stage != phase.IssueSuite || n.Issue.Code != land.SuiteNotRun || !strings.Contains(n.Issue.Prompt, "jig verify TK-1 --final") {
		t.Fatalf("suite: ready %v issue %+v", n.Ready, n.Issue)
	}
	if text, _ := r.run(t, "next", "TK-1"); !strings.Contains(text, "issue:\n") || !strings.Contains(text, "stage: suite") {
		t.Errorf("jig next TK-1 has no issue block at the suite:\n%s", text)
	}

	// A waiver of the suite (a person's call) makes the publish ready.
	if err := journal.Append(r.st, "TK-1", journal.Line{Event: journal.EventSuiteWaived}); err != nil {
		t.Fatal(err)
	}
	n = r.issueRun(t)
	if !n.Ready || n.Issue.Stage != phase.IssuePublish || n.Issue.Prompt != "Publish TK-1" || n.Code == phase.CodeChartDone {
		t.Fatalf("publish: ready %v issue %+v code %s", n.Ready, n.Issue, n.Code)
	}

	// Published: the run ends, naming Land TK-1.
	r.publishInto(t, "TK-1", "", "21")
	n = r.issueRun(t)
	if n.Code != phase.CodeChartWaitsLand || n.Issue.Stage != phase.IssuePublished || len(n.Lands) != 1 || n.Lands[0].Ticket != "TK-1" || n.Lands[0].Prompt != "Land TK-1" {
		t.Fatalf("published: %s %+v lands %v", n.Code, n.Issue, n.Lands)
	}

	// Landed: the chart is done.
	r.issueHost(t, true, true, true)
	if out, code := r.run(t, "land", "TK-1"); code != 0 {
		t.Fatalf("jig land TK-1: exit %d\n%s", code, out)
	}
	n = r.issueRun(t)
	if n.Code != phase.CodeChartDone || n.Issue.Stage != phase.IssueLanded {
		t.Fatalf("landed: %s %+v", n.Code, n.Issue)
	}
}

// TestIssueRunLandsNameWhatMustLandFirst: two dependent tasks both published
// are two landings in chart order, the second after the first; the reason names
// the first landable and the other after it.
func TestIssueRunLandsNameWhatMustLandFirst(t *testing.T) {
	r := issueRunRig(t)
	r.publishInto(t, "TK-2", "TK-1", "2")
	r.publishInto(t, "TK-3", "TK-1", "3")
	r.issueHost(t, false, false, false)
	n := r.issueRun(t)
	if got := landTickets(n); len(got) != 2 || got[0] != "TK-2" || got[1] != "TK-3" {
		t.Fatalf("lands = %v, want TK-2 then TK-3", got)
	}
	if len(n.Lands[0].After) != 0 || len(n.Lands[1].After) != 1 || n.Lands[1].After[0] != "TK-2" {
		t.Errorf("after = %v and %v, want none and TK-2", n.Lands[0].After, n.Lands[1].After)
	}
	if want := "TK-2 waits for the person's word: Land TK-2; then Land TK-3, after TK-2"; n.Reason != want {
		t.Errorf("reason = %q, want %q", n.Reason, want)
	}
}

// TestIssueRunNamesTheSameWordsAsLand: the suite codes and the container
// states the phase package spells are the words internal/land says.
func TestIssueRunNamesTheSameWordsAsLand(t *testing.T) {
	t.Parallel()
	for got, want := range map[string]string{
		phase.CodeSuiteNotRun: land.SuiteNotRun, phase.CodeSuiteFailed: land.SuiteFailed,
		phase.TailWaiting: land.StateWaiting, phase.TailReady: land.StateReady,
		phase.TailPublished: land.StatePublished, phase.TailLanded: land.StateLanded,
	} {
		if got != want {
			t.Errorf("phase says %q, land says %q", got, want)
		}
	}
}

// TestIssueRunLegacyChartIsUnchanged: a chart with no issue prints none of the
// model's keys, and an issue with no chart prints as it did.
func TestIssueRunLegacyChartIsUnchanged(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	out, code := r.run(t, "next", "old", "--json")
	if code != 0 {
		t.Fatalf("jig next old: exit %d\n%s", code, out)
	}
	for _, key := range []string{`"lands"`, `"publish"`, `"issue"`} {
		if strings.Contains(out, key) {
			t.Errorf("a legacy chart prints %s:\n%s", key, out)
		}
	}
	r.ticket(t, "TK-8", "kind: issue\n", false)
	text, _ := r.run(t, "next", "TK-8")
	if !strings.Contains(text, "kind: issue") || strings.Contains(text, "lands[") {
		t.Errorf("an issue with no chart prints:\n%s", text)
	}
}
