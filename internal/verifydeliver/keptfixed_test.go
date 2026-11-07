package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
	"gopkg.in/yaml.v3"
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

func TestKeptAndFixedSpendsAKeepOnlyWhenEveryGuardHolds(t *testing.T) {
	t.Parallel()
	keep := journal.Line{Event: journal.EventTriage, Ref: "r1-f1", Outcome: TriageKeep, Provenance: "person"}
	dismiss := journal.Line{Event: journal.EventTriage, Ref: "r1-f1", Outcome: TriageDismiss, Provenance: "person"}
	closed := journal.Line{Event: journal.EventGateRound, Attempt: 2}
	type rd = struct {
		head  string
		holds bool
	}
	cases := []struct {
		name     string
		rounds   []rd
		journal  []journal.Line
		now      string
		reviewed []string
		want     []string
	}{
		{"a pending keep at a moved head the round read", []rd{{"A", true}}, []journal.Line{keep}, "B", []string{"a.go"}, []string{"r1-f1"}},
		{"the same head", []rd{{"A", true}}, []journal.Line{keep}, "A", []string{"a.go"}, nil},
		{"a pending dismiss", []rd{{"A", true}}, []journal.Line{dismiss}, "B", []string{"a.go"}, nil},
		{"a keep an earlier round already spent", []rd{{"A", true}}, []journal.Line{keep, closed}, "B", []string{"a.go"}, nil},
		{"no decision", []rd{{"A", true}}, nil, "B", []string{"a.go"}, nil},
		{"a raising head not recorded", []rd{{"", true}}, []journal.Line{keep}, "B", []string{"a.go"}, nil},
		{"this round's head not recorded", []rd{{"A", true}}, []journal.Line{keep}, "", []string{"a.go"}, nil},
		{"the finding's file not read", []rd{{"A", true}}, []journal.Line{keep}, "B", []string{"b.go"}, nil},
		// Round 2 does not hold the finding, so round 3 compares with round 1's
		// head (A), not round 2's (B): at B the code moved.
		{"round 3, the finding held by round 1 only", []rd{{"A", true}, {"B", false}}, []journal.Line{keep}, "B", []string{"a.go"}, []string{"r1-f1"}},
		// Round 2 reported it again at B, so at B nothing moved since.
		{"round 3, the finding reported again at round 2", []rd{{"A", true}, {"B", true}}, []journal.Line{keep}, "B", []string{"a.go"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := &store.Store{Root: t.TempDir()}
			dir := st.TicketDir("T-1")
			for i, r := range c.rounds {
				fy := findingsYAML{}
				if r.holds {
					fy.Findings = []Finding{{ID: "r1-f1", File: "a.go", Title: "T", Status: StatusAsked}}
				}
				writeFindingsYAMLAt(t, dir, i+1, fy)
				rep := reportYAML{Round: i + 1, Verdict: "fix-slices"}
				if r.head != "" {
					rep.ReviewedSHA = map[string]string{"r": r.head}
				}
				data, err := yaml.Marshal(rep)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "gate", fmt.Sprintf("round-%d", i+1), "report.yaml"), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, l := range c.journal {
				if err := journal.Append(st, "T-1", l); err != nil {
					t.Fatal(err)
				}
			}
			files := roundFiles{repos: []roundRepo{{Name: "r"}}}
			rv := &Review{HeadSHA: c.now, Result: ReviewResult{ReviewedPaths: c.reviewed}}
			out := []Finding{{ID: "r1-f1", File: "a.go", Title: "T", Status: StatusAsked}}
			got, err := keptAndFixed(st, "T-1", len(c.rounds)+1, out, files, rv)
			if err != nil {
				t.Fatal(err)
			}
			ids := keptFixedIDs(got)
			if fmt.Sprint(ids) != fmt.Sprint(c.want) {
				t.Fatalf("keptAndFixed = %v, want %v", ids, c.want)
			}
			if len(got) == 1 && got[0].By != "person" {
				t.Fatalf("kept by %q, want person", got[0].By)
			}
		})
	}
}

// A kept note the risk floor routed, then cleared, reads kept then fixed, not
// addressed; a keep that did lead to a slice reads as the slice's fix.
func TestReviewNotesReadAKeptFloorNoteAndASlicedKeepByTheirOwnWording(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	dir := st.TicketDir("T-1")
	writeFindingsYAMLAt(t, dir, 1, findingsYAML{Findings: []Finding{
		{ID: "r1-f1", File: "a.go", Risk: RiskLow, Title: "Floor note", Status: StatusNoted, RoutedAs: ActionNote},
		{ID: "r1-f2", File: "b.go", Risk: RiskLow, Title: "Sliced keep", Status: StatusOpen},
	}})
	writeFindingsYAMLAt(t, dir, 2, findingsYAML{Cleared: []string{"r1-f1", "r1-f2"}})
	for _, id := range []string{"r1-f1", "r1-f2"} {
		if err := journal.Append(st, "T-1", journal.Line{Event: journal.EventTriage, Ref: id, Outcome: TriageKeep, Provenance: "person", Summary: "do it"}); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := journal.Read(st, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	slices := []store.Slice{{ID: "fix-1-r1-f2", Findings: []string{"r1-f2"}}}
	outcomes, _, _, err := collectFindingsWithOutcomes(dir, TriageDecisions(lines), 2, slices)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range outcomes {
		got[o.finding.ID] = o.detail
	}
	if want := "kept by a person: do it, then fixed and cleared at round 2"; got["r1-f1"] != want {
		t.Errorf("floor note = %q, want %q", got["r1-f1"], want)
	}
	if want := "fixed by slice fix-1-r1-f2 and cleared at round 2"; got["r1-f2"] != want {
		t.Errorf("sliced keep = %q, want %q", got["r1-f2"], want)
	}
}
