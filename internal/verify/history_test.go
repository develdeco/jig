package verify

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// TestHistoryIsWhatTheStopHookAndTheStatesLeaveOut: a slice's older commit,
// once a later one of its own exists, is history; its building tip is not, and
// is judged by the slice's latest result at the tip or at a commit after it:
// a pass or a failure of a slice's oracle at another slice's later commit is
// the tip's result, never "no recorded result". Without the branch nothing is
// history.
func TestHistoryIsWhatTheStopHookAndTheStatesLeaveOut(t *testing.T) {
	t.Parallel()
	truth := Truth{Ticket: "T-1", Slices: []string{"a", "b"}, BranchKnown: true, Parents: shape(),
		Commits: []phase.CommitRef{{SHA: a1, Slice: "a"}, {SHA: b1, Slice: "b"}, {SHA: a2, Slice: "a"}}}
	if truth.History(a2, "a") || truth.History(b1, "b") {
		t.Error("a slice's building tip is history")
	}
	if !truth.History(a1, "a") {
		t.Error("a slice's older commit, with a later one made, is not history")
	}

	// a's tip is a1 from here, with b's commit after it.
	truth.Commits = truth.Commits[:2]
	line := func(event, outcome, commit string) journal.Line {
		return journal.Line{Event: event, Outcome: outcome, Slice: "a", Commit: commit, Summary: "FAIL TestA"}
	}
	for _, c := range []struct {
		name  string
		lines []journal.Line
		want  phase.VerifyState // "" = left out of Outstanding
		fail  string
	}{
		{"no line at all", nil, phase.VerifyNoResult, ""},
		{"a pass at b's commit", []journal.Line{line(journal.EventOracle, "pass", b1)}, "", ""},
		{"a verified line at an untrailed commit", []journal.Line{line(journal.EventVerified, "", u)}, "", ""},
		{"a verified line at a merge", []journal.Line{line(journal.EventVerified, "", m)}, "", ""},
		{"a failure at b's commit", []journal.Line{line(journal.EventOracle, "fail", b1)}, phase.VerifyFailed, "FAIL TestA"},
		{"a failure at a merge", []journal.Line{line(journal.EventOracle, "fail", m)}, phase.VerifyFailed, "FAIL TestA"},
		{"a failure at b's commit, then a pass at the tip", []journal.Line{line(journal.EventOracle, "fail", b1), line(journal.EventOracle, "pass", a1)}, "", ""},
		{"a pass at the tip, then a failure at b's commit", []journal.Line{line(journal.EventOracle, "pass", a1), line(journal.EventOracle, "fail", b1)}, phase.VerifyFailed, "FAIL TestA"},
		{"a pass at a commit before the tip", []journal.Line{line(journal.EventOracle, "pass", "9999")}, phase.VerifyNoResult, ""},
		{"an uncovered run at b's commit", []journal.Line{{Event: journal.EventOracle, Outcome: "fail", Slice: "a", Commit: b1, Uncovered: journal.UncoveredDirtyBefore}}, phase.VerifyNoResult, ""},
	} {
		truth.Lines = c.lines
		var rows []phase.CommitVerification
		for _, r := range truth.Outstanding() {
			if r.Slice == "a" { // b's own tip has no result in every case
				rows = append(rows, r)
			}
		}
		if c.want == "" {
			if len(rows) != 0 {
				t.Errorf("%s: outstanding = %+v, want none", c.name, rows)
			}
			continue
		}
		if len(rows) != 1 || rows[0].Commit != a1 || rows[0].State != c.want || rows[0].FailLine != c.fail {
			t.Errorf("%s: outstanding = %+v, want a1 as %s %q", c.name, rows, c.want, c.fail)
		}
	}

	// Outstanding names only a's tip, with no recorded result.
	truth.Lines = nil
	truth.Commits = []phase.CommitRef{{SHA: a1, Slice: "a"}, {SHA: a2, Slice: "a"}}
	rows := truth.Outstanding()
	if len(rows) != 1 || rows[0].Commit != a2 || rows[0].State != phase.VerifyNoResult {
		t.Errorf("outstanding = %+v, want only a's tip with no recorded result", rows)
	}
}

// TestWithoutTheBranchNothingIsHistory: the readers that judge the journal
// alone (`jig here` for a ticket not found by its branch, `jig status` with no
// clone, the Stop hook off its branch) still name a failed commit.
func TestWithoutTheBranchNothingIsHistory(t *testing.T) {
	t.Parallel()
	truth := Truth{Ticket: "T-1", Slices: []string{"a"},
		Lines: []journal.Line{{Event: journal.EventOracle, Outcome: "fail", Slice: "a", Commit: a1, Summary: "FAIL TestA"}}}
	if truth.History(a1, "a") {
		t.Error("a commit is history with no branch read")
	}
	rows := truth.Outstanding()
	if len(rows) != 1 || rows[0].Commit != a1 || rows[0].State != phase.VerifyFailed {
		t.Errorf("outstanding = %+v, want a1 failed", rows)
	}
}
