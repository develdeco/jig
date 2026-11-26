package tui

import (
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// approvalWords are the words that say how the brief came to be approved: when,
// by whom, how it was recorded and which brief it was. They are empty for a
// brief nobody approved. This is the one place that words an approval: an
// agent's is worded as an agent's, once.
type approvalWords struct {
	when, by, how, brief string
}

// approvalRecord is the words of the approval of t's brief as the status and the
// journal say it.
func (m *Model) approvalRecord(t *ticket, b *statusBrief) approvalWords {
	if b == nil || b.ApprovedAt == "" {
		return approvalWords{}
	}
	w := approvalWords{when: b.ApprovedAt}
	if at, err := time.Parse(time.RFC3339, b.ApprovedAt); err == nil {
		w.when = at.In(m.zone).Format("2006-01-02 15:04")
	}
	if b.ApprovedBy != "" {
		w.by = phase.Approver(b.ApprovedBy, b.Provenance)
	}
	switch b.Provenance {
	case "person":
		w.how = "at a terminal"
	case "agent":
		w.how = "recorded by the session that drafted it"
	case "jig":
		w.how = "recorded by jig"
	}
	if i := t.latest("", journal.EventApproval); i >= 0 {
		w.brief = t.lines[i].Ref
	}
	return w
}

// approvalStands is where the approval stands, as the Parts list says it.
func (m *Model) approvalStands(b *statusBrief) string {
	switch b.Approval {
	case "current":
		return m.st.state(StateOK, "approved")
	case "changed":
		return m.st.state(StateFindings, "the brief changed since")
	}
	return m.st.state(StateNeedsInput, "waits for approval")
}

// approvalDetail is the detail of the approval: who approved it, when, how and
// which brief, and for a brief changed since the sections that changed or went
// and whether the illustration did; for a brief nobody approved, what it waits
// for. It opens nothing.
func (m *Model) approvalDetail(t *ticket, step stepRow) detailSpec {
	b := t.status.Brief
	d := detailSpec{title: "approval", summary: "waits for approval"}
	add := func(r detailRow) { d.rows = append(d.rows, r) }
	switch b.Approval {
	case "current":
		d.summary = "approved"
	case "changed":
		d.summary = "the brief changed since"
	}
	w := m.approvalRecord(t, b)
	if b.Approval == briefNone || w.when == "" {
		add(detailRow{label: "waits", text: step.Text})
		return d
	}
	add(detailRow{label: "approved", text: w.when})
	if w.by != "" {
		add(detailRow{label: "by", text: w.by})
	}
	if w.how != "" {
		add(detailRow{label: "how", text: w.how})
	}
	still := "the brief is still this one"
	if b.Approval == "changed" {
		still = "the brief has changed since"
	}
	add(detailRow{label: "brief", text: strings.TrimSpace(w.brief + " · " + still), state: briefState(b.Approval)})
	if b.Approval != "changed" {
		return d
	}
	var changed []string
	recorded := false
	for _, s := range b.Sections {
		if s.Since != "" {
			recorded = true
		}
		if s.Since == sinceChangedWord {
			changed = append(changed, s.Heading)
		}
	}
	switch {
	case !recorded && len(b.Removed) == 0:
		add(detailRow{label: "sections", text: "which sections changed was not recorded for this approval", dim: true})
	default:
		if len(changed) > 0 {
			add(detailRow{label: "changed", text: strings.Join(changed, ", ")})
		}
		if len(b.Removed) > 0 {
			add(detailRow{label: "removed", text: strings.Join(b.Removed, ", ")})
		}
		if len(changed) == 0 && len(b.Removed) == 0 {
			add(detailRow{label: "sections", text: "none changed", dim: true})
		}
	}
	if b.Illustration != "" && b.Illustration != "none" && b.Illustration != "same" {
		add(detailRow{label: "illustration", text: b.Illustration})
	}
	return d
}

// briefState is the color an approval's word on the brief is drawn in.
func briefState(approval string) string {
	if approval == "changed" {
		return StateFindings
	}
	return ""
}
