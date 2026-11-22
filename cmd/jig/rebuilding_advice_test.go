package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// TestAdviceOfARebuildingStackedTicketNamesTheMerge: a published stacked ticket
// that is owed a merge and built again keeps the advice of its own phase while
// it is built, and names the merge before the publish once it is reviewed.
func TestAdviceOfARebuildingStackedTicketNamesTheMerge(t *testing.T) {
	t.Parallel()
	f := phase.Facts{
		Ticket: "JIG-1", BriefExists: true, Sections: 4, Approval: phase.ApprovalCurrent, ApprovedBy: "Dana", ApprovedHow: "person",
		Published: true,
		Slices:    []phase.Slice{{ID: "a", State: "green"}, {ID: "b", State: "queued"}},
		Restacks:  []phase.Restack{{Repo: "app", Kind: phase.RestackTarget, Ref: "origin/main"}},
	}
	lines := []journal.Line{
		{Event: journal.EventApproval, Provenance: "person"},
		{Event: journal.EventClaim, Slice: "a"}, {Event: journal.EventVerified, Slice: "a", Commit: "c1"},
		{Event: journal.EventGateOpen, Attempt: 1}, {Event: journal.EventGateClean, Attempt: 1},
		{Event: journal.EventPr, URL: "https://example.test/pull/7", Commit: "c1"}, {Event: journal.EventPublishDone},
	}
	got := phase.Derive(f, lines)
	if !got.Behind() || !got.Rebuilding() || got.Current != phase.Build {
		t.Fatalf("queued fix: behind %v rebuilding %v current %s, want behind, built again, at the build", got.Behind(), got.Rebuilding(), got.Current)
	}
	if advice := phaseAdvice(got, "JIG-1"); strings.Contains(advice, "git merge") || !strings.Contains(advice, "claim") {
		t.Errorf("advice at the build = %q, want the build's, not the merge", advice)
	}

	f.Slices[1].State = "green"
	lines = append(lines,
		journal.Line{Event: journal.EventClaim, Slice: "b"}, journal.Line{Event: journal.EventVerified, Slice: "b", Commit: "c2"},
		journal.Line{Event: journal.EventGateOpen, Attempt: 2}, journal.Line{Event: journal.EventGateClean, Attempt: 2})
	got = phase.Derive(f, lines)
	if got.Current != phase.Publish {
		t.Fatalf("fix reviewed: current %s, want the publish", got.Current)
	}
	if advice := phaseAdvice(got, "JIG-1"); !strings.Contains(advice, "`git merge origin/main` in app") || !strings.Contains(advice, "jig publish JIG-1") {
		t.Errorf("advice at the publish = %q, want the merge, then the publish", advice)
	}
}
