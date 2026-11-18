package verify

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// branchShape is a branch past its start sha: a1 (slice a), b1 (slice b), an
// untrailed commit u, a merge m of the target branch (parents u and x, x a
// commit of the target) and a2 (slice a again), each with its parents.
const (
	a1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b1 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	u  = "cccccccccccccccccccccccccccccccccccccccc"
	x  = "dddddddddddddddddddddddddddddddddddddddd"
	m  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	a2 = "ffffffffffffffffffffffffffffffffffffffff"
)

func shape() map[string][]string {
	return map[string][]string{a1: {"start"}, b1: {a1}, u: {b1}, x: {"start"}, m: {u, x}, a2: {m}}
}

// TestARunAtALaterCommitCoversTheSlicesLastCommit: slices verified one after
// the other at HEAD. A run at the slice's last commit or at any commit that
// descends from it covers it: a commit of another slice, one with no trailer,
// a merge of the target branch. A slice commit made after the run does not
// get covered by it, and a commit of the target branch merged in is no
// descendant of the slice's commit.
func TestARunAtALaterCommitCoversTheSlicesLastCommit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, commit, at string
		want             bool
	}{
		{"itself", a1, a1, true},
		{"another slice's commit", a1, b1, true},
		{"an untrailed commit", a1, u, true},
		{"a merge commit", a1, m, true},
		{"a later commit of its own", a1, a2, true},
		{"the commit before it", b1, a1, false},
		{"a commit after the run", a2, m, false},
		{"a target-branch commit merged in", x, u, false},
		{"merged in, then covered by the merge", x, m, true},
		{"no commit", "", a1, false},
		{"not on the branch", a1, "9999", false},
	} {
		if got := AtOrAfter(shape(), c.commit, c.at); got != c.want {
			t.Errorf("%s: AtOrAfter(%.1s, %.1s) = %v, want %v", c.name, c.commit, c.at, got, c.want)
		}
	}

	oracle := func(slice, commit string) journal.Line {
		return journal.Line{Event: journal.EventOracle, Slice: slice, Commit: commit, Outcome: "pass"}
	}
	for _, at := range []string{b1, u, m} {
		truth := Truth{Ticket: "T-1", Slices: []string{"a"}, BranchKnown: true, Parents: shape(),
			Commits: []phase.CommitRef{{SHA: a1, Slice: "a"}, {SHA: b1, Slice: "b"}},
			Lines:   []journal.Line{oracle("a", at)}}
		if r := truth.LastRuns(); len(r) != 1 || r[0].Covers != "yes" {
			t.Errorf("a pass at %.1s covers %+v, want yes", at, r)
		}
	}
	truth := Truth{Ticket: "T-1", Slices: []string{"a"}, BranchKnown: true, Parents: shape(),
		Commits: []phase.CommitRef{{SHA: a1, Slice: "a"}, {SHA: a2, Slice: "a"}},
		Lines:   []journal.Line{oracle("a", m)}}
	if r := truth.LastRuns(); len(r) != 1 || r[0].Covers != "no: slice's last commit is "+a2[:12] {
		t.Errorf("a pass before the slice's next commit covers %+v", r)
	}
}

// TestHistoryIsWhatTheStopHookAndTheStatesLeaveOut: a slice's older commit
// once a later one exists, and a commit its slice has a verified line at or
// after (at an untrailed commit or a merge as well); the slice's building tip
// with no recorded result is not history.
func TestHistoryIsWhatTheStopHookAndTheStatesLeaveOut(t *testing.T) {
	t.Parallel()
	verified := func(commit string) journal.Line {
		return journal.Line{Event: journal.EventVerified, Slice: "a", Commit: commit}
	}
	truth := Truth{Ticket: "T-1", Slices: []string{"a", "b"}, BranchKnown: true, Parents: shape(),
		Commits: []phase.CommitRef{{SHA: a1, Slice: "a"}, {SHA: b1, Slice: "b"}, {SHA: a2, Slice: "a"}}}
	if truth.History(a2, "a") || truth.History(b1, "b") {
		t.Error("a slice's building tip is history")
	}
	if !truth.History(a1, "a") {
		t.Error("a slice's older commit, with a later one made, is not history")
	}
	truth.Commits = truth.Commits[:2] // a1 is now a's tip
	if truth.History(a1, "a") {
		t.Error("a1 is a's tip and has no verified line, but is history")
	}
	for _, at := range []string{b1, u, m} {
		truth.Lines = []journal.Line{verified(at)}
		if !truth.History(a1, "a") {
			t.Errorf("a1 with a verified line at %.1s is not history", at)
		}
	}
	truth.Lines = []journal.Line{verified(a1)}
	if truth.History(a1, "a") {
		t.Error("a verified line at the commit itself is history of that commit; it is a pass, not history")
	}

	// Outstanding leaves the pass and the history out, and names the tip.
	truth.Lines = nil
	truth.Commits = []phase.CommitRef{{SHA: a1, Slice: "a"}, {SHA: a2, Slice: "a"}}
	rows := truth.Outstanding()
	if len(rows) != 1 || rows[0].Commit != a2 || rows[0].State != phase.VerifyNoResult {
		t.Errorf("outstanding = %+v, want only a's tip with no recorded result", rows)
	}
}
