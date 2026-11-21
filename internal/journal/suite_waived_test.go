package journal

import "testing"

// TestSuitePreconditionWaivedUnreadReadsTheLatestCommitlessWaiver: the unread
// waiver of a repo is its latest suite-waived line with no commit: not a
// tip-bound waiver, another repo's, or another event's.
func TestSuitePreconditionWaivedUnreadReadsTheLatestCommitlessWaiver(t *testing.T) {
	t.Parallel()
	lines := []Line{
		{Event: EventSuiteWaived, Repo: "api", Commit: "aaa", Summary: "tip"},
		{Event: EventSuiteWaived, Repo: "api", Summary: "first"},
		{Event: EventSuiteWaived, Repo: "api", Summary: "second"},
		{Event: EventSuiteWaived, Repo: "web", Summary: "web"},
		{Event: EventPr, Repo: "api", Summary: "not a waiver"},
	}
	if l, ok := SuiteWaivedUnread(lines, "api"); !ok || l.Summary != "second" {
		t.Errorf("SuiteWaivedUnread(api) = %+v, %v, want the latest commit-less waiver", l, ok)
	}
	if l, ok := SuiteWaivedUnread(lines[:1], "api"); ok {
		t.Errorf("a tip-bound waiver is unread: %+v", l)
	}
	if l, ok := SuiteWaivedUnread(lines, "other"); ok {
		t.Errorf("another repo's waiver is unread: %+v", l)
	}
	if l, ok := SuiteWaivedUnread(lines[4:], "api"); ok {
		t.Errorf("a non-waiver line is unread: %+v", l)
	}
}

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
