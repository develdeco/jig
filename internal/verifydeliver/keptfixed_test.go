package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestReviewNotesReadAKeptAskThatALaterRoundFixedAsKeptThenFixed: an ask kept
// by a person, never built as a slice, and cleared by a later round reads
// fixed, with who kept it, and a keep that did lead to a slice still reads as
// the slice's fix.
func TestReviewNotesReadAKeptAskThatALaterRoundFixedAsKeptThenFixed(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	dir := st.TicketDir("T-1")
	writeFindingsYAMLAt(t, dir, 1, findingsYAML{Findings: []Finding{
		{ID: "r1-f1", File: "a.go", Risk: RiskLow, Title: "Kept ask", Status: StatusAsked},
	}})
	writeFindingsYAMLAt(t, dir, 2, findingsYAML{Cleared: []string{"r1-f1"}})
	if err := journal.Append(st, "T-1", journal.Line{Event: journal.EventTriage, Ref: "r1-f1", Outcome: TriageKeep, Provenance: "person", Summary: "do it"}); err != nil {
		t.Fatal(err)
	}
	lines, err := journal.Read(st, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	outcomes, _, _, err := collectFindingsWithOutcomes(dir, TriageDecisions(lines), 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "kept by a person: do it, then fixed and cleared at round 2"
	if o := outcomes[0]; o.status != "fixed" || o.detail != want {
		t.Fatalf("outcome = (%q, %q), want (fixed, %q)", o.status, o.detail, want)
	}
}
