package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// landIssue records the landing of the rig's task into its issue and of the
// issue's own pull request into main, merged on the host.
func landIssue(t *testing.T, s *suitePublishRig) {
	t.Helper()
	const issuePR = "https://github.example/owner/repo/pull/9"
	s.green(t, "TK-2")
	landing := journal.Line{Event: journal.EventLanded, Into: "TK-1", Base: s.branch, URL: "https://github.example/owner/repo/pull/2", Commit: "c0ffee2"}
	if err := journal.Append(s.st, "TK-2", landing); err != nil {
		t.Fatal(err)
	}
	for _, l := range []journal.Line{
		{Event: journal.EventPr, URL: issuePR},
		{Event: journal.EventPublishDone},
		{Event: journal.EventLanded, Base: "main", URL: issuePR, Commit: "c0ffee9"},
	} {
		s.record(l)
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t,
		fixture.GhPull{Number: 2, Base: s.branch, State: "closed", Merged: true, MergeCommit: "c0ffee2"},
		fixture.GhPull{Number: 9, Base: "main", State: "closed", Merged: true, MergeCommit: "c0ffee9"}))
}

// dropBranch removes the clone's copy of the issue's branch, as a landed
// issue's, or one nobody pushed, has none.
func dropBranch(t *testing.T, s *suitePublishRig) {
	t.Helper()
	if _, err := gitx.Run(s.clone, "update-ref", "-d", "refs/remotes/origin/"+s.branch); err != nil {
		t.Fatal(err)
	}
}

func suiteRowOf(t *testing.T, s *suitePublishRig) string {
	t.Helper()
	out, code := s.run(t, "status", "TK-1")
	rows := suiteRowsOf(t, out)
	if code != 0 || len(rows) != 1 {
		t.Fatalf("jig status TK-1: exit %d, suite rows %q\n%s", code, rows, out)
	}
	return rows[0]
}

// TestSuiteLineOfALandedIssue: a landed issue's suite is what was recorded, even
// with its branch gone from the clone, else none recorded; never a prompt to
// fetch the branch and run the suite.
func TestSuiteLineOfALandedIssue(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	tip := s.tip()
	s.record(journal.Line{Event: journal.EventFinalOracle, Commit: tip, Outcome: "pass", Seconds: 7, Command: "go test ./..."})
	landIssue(t, s)
	dropBranch(t, s)
	if row := suiteRowOf(t, s); !strings.HasPrefix(row, "app,pass,go test ./...,"+tip+",7,") {
		t.Errorf("a landed issue's suite row is %q, want the recorded pass at %s", row, tip)
	}
}

// TestSuiteLineOfALandedIssueWithNoRecordedRun: nothing recorded reads none
// recorded, not a run to make.
func TestSuiteLineOfALandedIssueWithNoRecordedRun(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	landIssue(t, s)
	dropBranch(t, s)
	if row := suiteRowOf(t, s); !strings.HasPrefix(row, "app,undeclared,") || strings.Contains(row, "fetch") {
		t.Errorf("a landed issue with no recorded run reads %q, want none recorded", row)
	}
}

// addOpenTask lists a second task of the issue that has not landed, so the issue
// waits for its tasks.
func addOpenTask(t *testing.T, s *suitePublishRig) {
	t.Helper()
	s.ticket(t, "TK-3", "", true)
	s.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-3\n    title: Three\n", "")
}

// TestSuiteLineOfAnIssueWithNoPushedBranch: the suite line is the standing `jig
// next` derives. With a task not landed the issue waits for its tasks; once every
// task landed and its branch is not in the clone, jig next says to fetch it and
// run the suite, and so does the status.
func TestSuiteLineOfAnIssueWithNoPushedBranch(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	addOpenTask(t, s)
	dropBranch(t, s)
	row := suiteRowOf(t, s)
	if !strings.HasPrefix(row, "app,unrun,") || !strings.Contains(row, "waits for its tasks") {
		t.Errorf("an issue with a task not landed reads %q, want it waiting for its tasks", row)
	}

	all := newSuitePublishRig(t, suiteManifestYAML)
	dropBranch(t, all)
	row = suiteRowOf(t, all)
	_, codes, text := all.next()
	if !strings.HasPrefix(row, "app,unrun,") || !strings.Contains(row, "git fetch") || len(codes) != 1 || !strings.Contains(text, "git fetch") {
		t.Errorf("an issue whose tasks landed and whose branch is not fetched reads %q beside jig next %v\n%s", row, codes, text)
	}
}

// TestSuiteLineOfARepoWithNoSuite: a repo whose target declares no suite reads
// none recorded while the issue waits for its tasks, pushed or not, and at its
// tip once pushed.
func TestSuiteLineOfARepoWithNoSuite(t *testing.T) {
	s := newSuitePublishRig(t, "")
	addOpenTask(t, s)
	if _, err := gitx.Run(s.clone, "update-ref", "refs/remotes/origin/main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	dropBranch(t, s)
	if row := suiteRowOf(t, s); !strings.HasPrefix(row, "app,undeclared,") {
		t.Errorf("an unpushed issue of a repo with no suite reads %q", row)
	}
	s.moveTip("")
	if row := suiteRowOf(t, s); !strings.HasPrefix(row, "app,undeclared,") {
		t.Errorf("a pushed issue of a repo with no suite reads %q", row)
	}
}

// TestAWaivedSuiteReadsWaivedAfterTheLanding: a suite that failed and was waived
// reads waived at the tip, and still waived once the issue landed and its branch
// is gone, one row and not the failure beside it.
func TestAWaivedSuiteReadsWaivedAfterTheLanding(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	tip := s.tip()
	s.record(journal.Line{Event: journal.EventFinalOracle, Commit: tip, Outcome: "fail", Seconds: 4, Signature: "final:boom", Command: "go test ./..."})
	s.record(journal.Line{Event: journal.EventSuiteWaived, Commit: tip, Summary: "ci runs it", Provenance: "person"})
	if row := suiteRowOf(t, s); !strings.HasPrefix(row, "app,waived,") {
		t.Fatalf("before the landing the suite reads %q", row)
	}
	landIssue(t, s)
	dropBranch(t, s)
	if row := suiteRowOf(t, s); !strings.HasPrefix(row, "app,waived,") || !strings.Contains(row, "ci runs it") {
		t.Errorf("after the landing the suite reads %q, want waived", row)
	}
}

// TestStatusOfAContainerSaysWhatJigNextSays: the standing and the waits of a
// container's status are the ones jig next derives.
func TestStatusOfAContainerSaysWhatJigNextSays(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	addOpenTask(t, s)
	out, code := s.run(t, "status", "TK-1")
	if code != 0 || !strings.Contains(out, "standing: waiting") || !strings.Contains(out, "waits[") || !strings.Contains(out, "TK-3,building") {
		t.Errorf("jig status TK-1: exit %d\n%s", code, out)
	}

	// A ready issue prints its prompt, and an issue whose suite failed prints the
	// unmet row, as jig next does.
	ready := newSuitePublishRig(t, suiteManifestYAML)
	landTask(t, ready)
	ready.record(journal.Line{Event: journal.EventFinalOracle, Commit: ready.tip(), Outcome: "pass", Seconds: 3, Command: "go test ./..."})
	if out, _ := ready.run(t, "status", "TK-1"); !strings.Contains(out, "standing: ready") || !strings.Contains(out, "prompt: Publish TK-1") {
		t.Errorf("jig status of a ready issue:\n%s", out)
	}
	failed := newSuitePublishRig(t, suiteManifestYAML)
	landTask(t, failed)
	failed.record(journal.Line{Event: journal.EventFinalOracle, Commit: failed.tip(), Outcome: "fail", Seconds: 3, Signature: "final:boom", Command: "go test ./..."})
	if out, _ := failed.run(t, "status", "TK-1"); !strings.Contains(out, "unmet[1]{code,why,prompt}:") || !strings.Contains(out, "LAND_SUITE_FAILED") {
		t.Errorf("jig status of an issue whose suite failed:\n%s", out)
	}
}

// TestStatusOfAnIssueWithAnUnreadableSuite: a suite that cannot be read is a row
// that says why; the status still prints the kind, the chart and the children.
func TestStatusOfAnIssueWithAnUnreadableSuite(t *testing.T) {
	s := newSuitePublishRig(t, "oracles: [not a map\n")
	out, code := s.run(t, "status", "TK-1")
	if code != 0 || !strings.Contains(out, "kind: issue") || !strings.Contains(out, "chart: plan") || !strings.Contains(out, "children[") {
		t.Fatalf("jig status TK-1 of an unreadable suite: exit %d\n%s", code, out)
	}
	if rows := suiteRowsOf(t, out); len(rows) != 1 || !strings.HasPrefix(rows[0], "app,unrun,") || !strings.Contains(rows[0], "manifest") {
		t.Errorf("the suite row is %q, want one that says why", rows)
	}
}
