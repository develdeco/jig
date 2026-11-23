package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// digestOfFacts is the digest of a ticket as internal/phase tells it from facts
// and lines: each phase's state and words exactly as the verb gives them.
func digestOfFacts(f phase.Facts, lines []journal.Line) *digestInfo {
	d := &digestInfo{Kind: "task"}
	for _, p := range phase.Derive(f, lines).Phases {
		d.Phases = append(d.Phases, digestPhase{Name: string(p.Name), State: p.State, Text: p.Text})
	}
	return d
}

// TestStepsShowThePhaseSentencesWhole: the frame of a review step with an ask
// to decide and of a brief changed after its approval carries each sentence
// whole, as internal/phase wrote it, in a frame of the usual width.
func TestStepsShowThePhaseSentencesWhole(t *testing.T) {
	t.Parallel()

	changed := phase.Facts{Ticket: "JIG-9", BriefExists: true, Sections: 3, Approval: phase.ApprovalChanged, ApprovedBy: "Dana Quintero-Vasquez", ApprovedHow: "person"}
	review := phase.Facts{Ticket: "JIG-9", BriefExists: true, Sections: 3, Approval: phase.ApprovalCurrent, ApprovedBy: "Dana", ApprovedHow: "person",
		Slices: []phase.Slice{{ID: "a", State: "green"}, {ID: "r1-fix-1", State: "green", FromGate: 1}},
		Asks:   []phase.Ask{{ID: "r1-f2"}, {ID: "r1-f3"}}}
	reviewLines := []journal.Line{
		{Event: journal.EventApproval, Provenance: "person"},
		{Event: journal.EventClaim, Slice: "a", Session: "s-build"}, {Event: journal.EventVerified, Slice: "a", Commit: "abcdef0123"},
		{Event: journal.EventGateOpen, Attempt: 1}, {Event: journal.EventGateRound, Attempt: 1},
	}

	for _, tc := range []struct {
		name  string
		d     *digestInfo
		phase string
		want  string
	}{
		{"a brief changed after its approval", digestOfFacts(changed, nil), "brief", "brief changed after approval: approve again"},
		{"a round that sent a fix back and left two asks", digestOfFacts(review, reviewLines), "review", "round 1: 1 fix sent back, 2 asks wait for you"},
	} {
		var text string
		for _, p := range tc.d.Phases {
			if p.Name == tc.phase {
				text = p.Text
			}
		}
		if text != tc.want {
			t.Fatalf("%s: phase text = %q, want %q", tc.name, text, tc.want)
		}
		g := newRig(t, "JIG-9")
		g.m.w.setRoster([]rosterRow{{ID: "JIG-9", Title: "Ticket", State: "ready", Digest: tc.d}})
		if page := pagesText(g.m.ticketPage("JIG-9")); !strings.Contains(page, text) {
			t.Errorf("%s: the ticket screen does not carry %q whole:\n%s", tc.name, text, page)
		}
		if view := g.view(); !strings.Contains(view, text) {
			t.Errorf("%s: the frame does not carry %q whole:\n%s", tc.name, text, view)
		}
	}
}
