package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestOutcomeNamesWhoDecided pins the review notes' wording of a dismissed or
// kept finding by the provenance of its `jig triage` line, with the line's
// reason, and the older wording when the finding has no line.
func TestOutcomeNamesWhoDecided(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		finding Finding
		line    *journal.Line
		status  string
		want    string
	}{
		{"dismissed by a person", Finding{Status: StatusDismissed}, &journal.Line{Outcome: "dismiss", Provenance: "person", Summary: "not a bug"}, "dismissed", "dismissed by a person: not a bug"},
		{"dismissed by an agent", Finding{Status: StatusDismissed}, &journal.Line{Outcome: "dismiss", Provenance: "agent"}, "dismissed", "dismissed by an agent"},
		{"dismissed, no line", Finding{Status: StatusDismissed, Triage: TriageHuman}, nil, "dismissed", "dismissed by a human"},
		{"kept by a person", Finding{Status: StatusOpen, Decision: "fix it"}, &journal.Line{Outcome: "keep", Provenance: "person", Summary: "fix it"}, "decided", "kept by a person: fix it"},
		{"kept by an agent, no reason", Finding{Status: StatusOpen}, &journal.Line{Outcome: "keep", Provenance: "agent"}, "decided", "kept by an agent"},
		{"kept, no line", Finding{Status: StatusOpen, Decision: "fix it"}, nil, "decided", "kept with the human's decision: fix it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &store.Store{Root: t.TempDir()}
			f := tc.finding
			f.ID, f.File, f.Risk = "r1-f1", "a.go", RiskLow
			writeFindingsYAMLAt(t, st.TicketDir("T-1"), 1, findingsYAML{Findings: []Finding{f}})
			var decisions map[string]TriageDecision
			if tc.line != nil {
				l := *tc.line
				l.Event, l.Ref = journal.EventTriage, "r1-f1"
				if err := journal.Append(st, "T-1", l); err != nil {
					t.Fatal(err)
				}
				lines, err := journal.Read(st, "T-1")
				if err != nil {
					t.Fatal(err)
				}
				decisions = TriageDecisions(lines)
			}
			outcomes, _, _, err := collectFindingsWithOutcomes(st.TicketDir("T-1"), decisions, 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			if o := outcomes[0]; o.status != tc.status || o.detail != tc.want {
				t.Fatalf("outcome = (%q, %q), want (%q, %q)", o.status, o.detail, tc.status, tc.want)
			}
		})
	}
}
