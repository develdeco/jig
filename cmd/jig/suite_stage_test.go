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

// TestSuiteLineOfAnIssueWithNoPushedBranch: an issue whose branch is not in the
// clone waits for it, and is never told to fetch it and run the suite.
func TestSuiteLineOfAnIssueWithNoPushedBranch(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	dropBranch(t, s)
	row := suiteRowOf(t, s)
	if !strings.HasPrefix(row, "app,unrun,") || !strings.Contains(row, "waits for its branch") || strings.Contains(row, "fetch") {
		t.Errorf("an unpushed issue's suite row is %q", row)
	}
}

// TestSuiteLineOfARepoWithNoSuite: a repo whose target declares no suite reads
// none recorded whether or not the issue's branch is pushed.
func TestSuiteLineOfARepoWithNoSuite(t *testing.T) {
	s := newSuitePublishRig(t, "")
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
