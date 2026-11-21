package journal

import "testing"

// TestSuitePreconditionWaivedAtReadsTheTipAndRepo: a waiver stands for its
// repo at exactly its tip, the latest wins, and no tip finds none.
func TestSuitePreconditionWaivedAtReadsTheTipAndRepo(t *testing.T) {
	t.Parallel()
	lines := []Line{
		{Event: EventSuiteWaived, Repo: "api", Commit: "aaa", Summary: "first"},
		{Event: EventSuiteWaived, Repo: "api", Commit: "aaa", Summary: "second"},
		{Event: EventSuiteWaived, Repo: "web", Commit: "bbb", Summary: "web"},
		{Event: EventFinalOracle, Repo: "api", Commit: "ccc"},
	}
	if l, ok := SuiteWaivedAt(lines, "api", "aaa"); !ok || l.Summary != "second" {
		t.Errorf("SuiteWaivedAt(api, aaa) = %+v, %v, want the latest", l, ok)
	}
	for _, c := range [][2]string{{"api", "bbb"}, {"web", "aaa"}, {"api", "ccc"}, {"api", ""}, {"", "aaa"}} {
		if l, ok := SuiteWaivedAt(lines, c[0], c[1]); ok {
			t.Errorf("SuiteWaivedAt(%q, %q) = %+v, want none", c[0], c[1], l)
		}
	}
}
