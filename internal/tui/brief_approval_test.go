package tui

import (
	"strings"
	"testing"
)

// approvalDetailOf is the detail of the approval row of the brief screen the
// status and brief text give, in words, at 100x60.
func approvalDetailOf(t *testing.T, status string) (string, *rig) {
	t.Helper()
	g := briefRigWith(t, 100, 60, status, briefTextJIG1()).focus("approval")
	view := g.view()
	return readWords(between(view, "╭─ Detail · approval", "╰")), g
}

// TestTheApprovalIsARowAndItsDetailSaysWhoWhenHowAndWhichBrief: the approval is
// the last part of the list, and its detail says when it was approved, by whom
// (an agent's approval worded as an agent's, once), how it was recorded and which
// brief, the hash the journal's approval line carries; the words are no longer
// lines above the list.
func TestTheApprovalIsARowAndItsDetailSaysWhoWhenHowAndWhichBrief(t *testing.T) {
	t.Parallel()
	d, g := approvalDetailOf(t, statusJIG1Brief())
	for _, want := range []string{
		"approved 2026-10-23 09:00",
		"by an agent, as Ada Lovelace",
		"how recorded by the session that drafted it",
		"brief 0123456789ab · the brief is still this one",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the approval lacks %q:\n%s", want, g.view())
		}
	}
	if n := strings.Count(d, "as Ada Lovelace"); n != 1 {
		t.Errorf("the approver is worded %d times in the detail, want once:\n%s", n, d)
	}
	rows := rowsOf(g.view(), "Parts")
	if last := rows[len(rows)-1]; !strings.HasPrefix(strings.TrimPrefix(last, "▸ "), "approval") || !strings.Contains(last, "approved") {
		t.Errorf("the last part is %q, want the approval, saying it is approved", last)
	}
	if top := between(g.view(), "jig  demo", "╭─ Parts"); strings.Contains(top, "approved 2026-10-23 09:00") || strings.Contains(top, "recorded by the session") {
		t.Errorf("the approval's words are still lines over the list:\n%s", top)
	}
}

// TestAChangedBriefsApprovalNamesTheSectionsAddedChangedAndRemoved: the approval
// of a brief edited since says which sections changed or are new, which went,
// and what became of the illustration, as the status says them.
func TestAChangedBriefsApprovalNamesTheSectionsAddedChangedAndRemoved(t *testing.T) {
	t.Parallel()
	status := strings.Replace(sinceStatus("same", "changed", "same", "changed"), "approval: current", "approval: changed", 1)
	status = strings.Replace(status, "  provenance: agent\n", "  provenance: agent\n  illustration_since_approval: added\n", 1)
	status = strings.Replace(status, "  Tone,c+d,changed\n", "  Tone,c+d,changed\nbrief_removed[1]{heading}:\n  Old section\n", 1)
	d, g := approvalDetailOf(t, status)
	for _, want := range []string{"brief 0123456789ab · the brief has changed since", "changed Greeting, Tone", "removed Old section", "illustration added"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the approval lacks %q:\n%s", want, g.view())
		}
	}
	if rows := rowsOf(g.view(), "Parts"); !strings.Contains(rows[len(rows)-1], "the brief changed since") {
		t.Errorf("the approval's row does not say the brief changed: %q", rows[len(rows)-1])
	}
}

// TestAnApprovalThatRecordedNoSectionsSaysSo: an approval made before sections
// were recorded names none of them as changed, and says that it did not record
// them.
func TestAnApprovalThatRecordedNoSectionsSaysSo(t *testing.T) {
	t.Parallel()
	status := strings.Replace(statusJIG1Brief(), "approval: current", "approval: changed", 1)
	d, g := approvalDetailOf(t, status)
	if !strings.Contains(d, "which sections changed was not recorded for this approval") {
		t.Errorf("the detail does not say the sections were not recorded:\n%s", g.view())
	}
	for _, not := range []string{"changed Goal", "changed Greeting", "removed"} {
		if strings.Contains(d, not) {
			t.Errorf("the detail names %q for an approval that recorded none:\n%s", not, g.view())
		}
	}
}

// TestABriefNoOneApprovedSaysWhatItWaitsFor: a brief nobody approved still has the
// row, and its detail says what it waits for in the sentence the Brief step
// carries.
func TestABriefNoOneApprovedSaysWhatItWaitsFor(t *testing.T) {
	t.Parallel()
	none := "approval: none\n"
	status := strings.Replace(statusJIG1Brief(), "approval: current\n  approved_by: an agent (as Ada Lovelace)\n  identity: Ada Lovelace\n  approved_at: \"2026-10-23T09:00:00Z\"\n  provenance: agent\n", none, 1)
	if status == statusJIG1Brief() {
		t.Fatal("the status was not made one of an unapproved brief")
	}
	d, g := approvalDetailOf(t, status)
	step := g.m.steps(g.m.w.tickets["JIG-1"])[stepBrief].Text
	if step == "" || !strings.Contains(d, "waits "+readWords(step)) {
		t.Errorf("the detail does not say what the brief waits for (%q):\n%s", step, g.view())
	}
	if strings.Contains(d, "approved 20") || strings.Contains(d, "by an agent") {
		t.Errorf("the detail of an unapproved brief says who approved it:\n%s", g.view())
	}
	if rows := rowsOf(g.view(), "Parts"); !strings.Contains(rows[len(rows)-1], "waits for approval") {
		t.Errorf("the approval's row does not say it waits: %q", rows[len(rows)-1])
	}
}

// TestTheApprovalOpensNothing: enter on the approval row stays on the brief
// screen, and the approval's detail is its whole.
func TestTheApprovalOpensNothing(t *testing.T) {
	t.Parallel()
	_, g := approvalDetailOf(t, statusJIG1Brief())
	depth := len(g.m.nav)
	g.press("enter")
	if g.m.Screen() != "brief" || len(g.m.nav) != depth {
		t.Errorf("enter on the approval opened %s (%d screens, was %d)", g.m.Screen(), len(g.m.nav), depth)
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowApproval {
		t.Errorf("the cursor left the approval: %+v", ref)
	}
}
