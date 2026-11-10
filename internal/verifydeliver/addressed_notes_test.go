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
	cleared, err := ClearingAfterTriage(known, []Finding{r2f1}, []string{"publish.go", "render.go"}, []string{"publish.go", "render.go"}, alwaysExists)
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

	outcomes, reviewedPaths, lastSummary, err := collectFindingsWithOutcomes(ticketDir, nil, 2, nil)
	if err != nil {
		t.Fatalf("collectFindingsWithOutcomes: %v", err)
	}
	if err := writeReviewNotes(st, "T-1", "review-notes.md", "", 2, nil, lastSummary, outcomes, nil, reviewedPaths, nil, nil, "", nil); err != nil {
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
}

// TestClearingAfterTriageClearsARoutedNoteWhoseFileChanged pins the rule for
// a note the risk floor routed (routed_as: note): it clears only when this
// round's change touched its file, the reviewer read it and did not report it
// again. A reviewer's own note, a file the round did not change, a file the
// reviewer did not read, a finding reported again and a file an open finding
// still blocks each leave it noted.
func TestClearingAfterTriageClearsARoutedNoteWhoseFileChanged(t *testing.T) {
	t.Parallel()

	routed := Finding{ID: "r1-f1", File: "a.go", Status: StatusNoted, RoutedAs: ActionNote}
	own := Finding{ID: "r1-f2", File: "a.go", Status: StatusNoted}
	for _, tc := range []struct {
		name     string
		known    map[string]Finding
		reported []Finding
		reviewed []string
		changed  []string
		want     []string
	}{
		{"changed, read and not reported", map[string]Finding{"r1-f1": routed}, nil, []string{"a.go"}, []string{"a.go"}, []string{"r1-f1"}},
		{"a reviewer's own note never clears", map[string]Finding{"r1-f2": own}, nil, []string{"a.go"}, []string{"a.go"}, nil},
		{"file read but not changed", map[string]Finding{"r1-f1": routed}, nil, []string{"a.go"}, []string{"b.go"}, nil},
		{"file changed but not read", map[string]Finding{"r1-f1": routed}, nil, []string{"b.go"}, []string{"a.go"}, nil},
		{"reported again by id", map[string]Finding{"r1-f1": routed}, []Finding{{ID: "r1-f1", File: "a.go", Status: StatusNoted, RoutedAs: ActionNote}}, []string{"a.go"}, []string{"a.go"}, nil},
		{"an open finding in the file blocks it", map[string]Finding{"r1-f1": routed}, []Finding{{ID: "r2-f1", File: "a.go", Status: StatusOpen}}, []string{"a.go"}, []string{"a.go"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleared, err := ClearingAfterTriage(tc.known, tc.reported, tc.reviewed, tc.changed, alwaysExists)
			if err != nil {
				t.Fatalf("ClearingAfterTriage: %v", err)
			}
			if !equalStrings(cleared, tc.want) {
				t.Fatalf("cleared = %v, want %v", cleared, tc.want)
			}
		})
	}
}

// TestFindingOutcomeAddressedNeedsNoSliceAndKeepsFirstVersion pins the
// outcome wording: a cleared routed note a slice fixed still reads fixed by
// that slice, one nothing credited reads addressed; a finding continued with
// the same title and risk shows no history, one continued with another
// shows both versions.
func TestFindingOutcomeAddressedNeedsNoSliceAndKeepsFirstVersion(t *testing.T) {
	t.Parallel()

	ticketDir := t.TempDir()
	note := func(id, title, risk string) Finding {
		return Finding{ID: id, File: id + ".go", Title: title, Risk: risk, Status: StatusNoted, RoutedAs: ActionNote}
	}
	writeFindingsYAMLAt(t, ticketDir, 1, findingsYAML{Findings: []Finding{
		note("r1-f1", "one", RiskLow), note("r1-f2", "two", RiskLow), note("r1-f3", "three", RiskLow), note("r1-f4", "four", RiskMedium),
	}})
	writeFindingsYAMLAt(t, ticketDir, 2, findingsYAML{
		Findings: []Finding{note("r1-f2", "two", RiskLow), note("r1-f3", "three, reworded", RiskLow), note("r1-f4", "four", RiskLow)},
		Cleared:  []string{"r1-f1"},
	})
	slices := []store.Slice{{ID: "fix-1", FromGate: 1, Findings: []string{"r1-f2"}}}

	outcomes, _, _, err := collectFindingsWithOutcomes(ticketDir, nil, 2, slices)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]findingOutcome{}
	for _, o := range outcomes {
		byID[o.finding.ID] = o
	}
	if o := byID["r1-f1"]; o.status != "addressed" || o.detail != "addressed at round 2" {
		t.Errorf("r1-f1 = %+v, want addressed at round 2", o)
	}
	if o := byID["r1-f2"]; o.status != "noted" || o.continuedWithChange() {
		t.Errorf("r1-f2 = %+v, want noted with no history: nothing changed when it was continued", o)
	}
	if o := byID["r1-f3"]; !o.continuedWithChange() || o.first.Title != "three" || o.finding.Title != "three, reworded" {
		t.Errorf("r1-f3 = %+v, want its first and latest title", o)
	}
	// A continuation that keeps its title and changes only the risk still
	// shows its history.
	if o := byID["r1-f4"]; !o.continuedWithChange() || o.first.Risk != RiskMedium || o.finding.Risk != RiskLow {
		t.Errorf("r1-f4 = %+v, want its first and latest risk", o)
	}
	st := &store.Store{Root: t.TempDir()}
	if err := writeReviewNotes(st, "T-1", "review-notes.md", "", 2, nil, "", outcomes, nil, nil, nil, nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if want := "  - History: medium at round 1: four; continued at round 2 as low: four\n"; !strings.Contains(readNotes(t, st, "T-1", "review-notes.md"), want) {
		t.Errorf("review notes lack the risk-only history line %q", want)
	}

	slices = []store.Slice{{ID: "fix-1", FromGate: 1, Findings: []string{"r1-f1"}}}
	outcomes, _, _, err = collectFindingsWithOutcomes(ticketDir, nil, 2, slices)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outcomes {
		if o.finding.ID == "r1-f1" && (o.status != "fixed" || o.detail != "fixed by slice fix-1 and cleared at round 2") {
			t.Errorf("r1-f1 = %+v, want fixed by slice fix-1: a slice credited it", o)
		}
	}
}
