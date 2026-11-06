package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// gate7Rounds writes the shape of GATE-7's first two rounds into a store and
// returns the ticket's directory, the way a gate leaves them: round 1 files a
// medium fix (r1-f1) that stays open, and a low fix the risk floor routes to
// the notes (r1-f3) beside a note the reviewer itself labeled (r1-f5); round 2
// changed and read r1-f3's file and r1-f1's, continued r1-f1 with --prior as
// a low fix (noted), and did not report r1-f3 again. Round 2's cleared list is
// what ClearingAfterTriage computes from the round-1 fold and the round-2
// report, as the gate records it.
func gate7Rounds(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	ticketDir := st.TicketDir("T-1")

	r1f1 := Finding{ID: "r1-f1", File: "publish.go", Title: "The notes name only the last round's reviewer", Action: ActionFix, Risk: RiskMedium, RiskRationale: "the merger weighs it", Status: StatusOpen}
	r1f3 := Finding{ID: "r1-f3", File: "render.go", Title: "Two note paths survive mutation", Action: ActionFix, Risk: RiskLow, RiskRationale: "wording only", Status: StatusNoted, RoutedAs: ActionNote}
	r1f5 := Finding{ID: "r1-f5", File: "render.go", Title: "Nothing to change here", Action: ActionNote, Risk: RiskLow, RiskRationale: "an aside", Status: StatusNoted}
	writeFindingsYAMLAt(t, ticketDir, 1, findingsYAML{
		Scope:         "full",
		ReviewedPaths: []string{"publish.go", "render.go"},
		Findings:      []Finding{r1f1, r1f3, r1f5},
		Summary:       "round 1",
	})

	r2f1 := Finding{ID: "r1-f1", File: "publish.go", Title: "Publish's every-round list is unproven", Action: ActionFix, Risk: RiskLow, RiskRationale: "only a gap", Status: StatusNoted, RoutedAs: ActionNote, Recurrences: 1}
	known := map[string]Finding{"r1-f1": r1f1, "r1-f3": r1f3, "r1-f5": r1f5}
	cleared, err := ClearingAfterTriage(known, []Finding{r2f1}, []string{"publish.go", "render.go"}, alwaysExists)
	if err != nil {
		t.Fatalf("ClearingAfterTriage: %v", err)
	}
	writeFindingsYAMLAt(t, ticketDir, 2, findingsYAML{
		Scope:         "delta",
		ReviewedPaths: []string{"publish.go", "render.go"},
		Findings:      []Finding{r2f1},
		Cleared:       cleared,
		Summary:       "round 2: every earlier finding is fixed",
	})
	return st, ticketDir
}

// TestReviewNotesReadAddressedAndKeepFirstVersion is GATE-7's record: a note
// the builder fixed read "noted", and a finding continued as a lower risk lost
// its first title. The notes must say r1-f3 was addressed at round 2 (and the
// body count it apart from fixed and noted), show r1-f1's medium round-1 title
// beside its low round-2 version, and leave the reviewer's own note noted.
func TestReviewNotesReadAddressedAndKeepFirstVersion(t *testing.T) {
	t.Parallel()
	st, ticketDir := gate7Rounds(t)

	outcomes, reviewedPaths, lastSummary, err := collectFindingsWithOutcomes(ticketDir, 2, nil)
	if err != nil {
		t.Fatalf("collectFindingsWithOutcomes: %v", err)
	}
	if err := writeReviewNotes(st, "T-1", "review-notes.md", 2, nil, lastSummary, outcomes, nil, reviewedPaths, nil, nil); err != nil {
		t.Fatal(err)
	}
	notes := readNotes(t, st, "T-1", "review-notes.md")

	for _, want := range []string{
		"- **Two note paths survive mutation** (low): wording only\n  - Status: addressed at round 2\n",
		"medium at round 1: The notes name only the last round's reviewer; continued at round 2 as low: Publish's every-round list is unproven",
		"- **Nothing to change here** (low): an aside\n  - Status: noted\n",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("review notes lack %q:\n%s", want, notes)
		}
	}

	body := renderVerificationSection(reportYAML{}, "oracles-only", nil, outcomes)
	if want := "Findings: 0 fixed, 1 addressed, 0 dismissed, 2 noted, 0 asked."; !strings.Contains(body, want) {
		t.Errorf("verification section = %q, want %q", body, want)
	}
}
