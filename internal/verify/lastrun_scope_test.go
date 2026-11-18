package verify

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// TestLastRunOfASliceWhoseRepoWasNotReadIsNotChecked: in a project of several
// repos a reader may read one repo's branch only. A slice of another repo has
// no commit in the log because its branch was never looked at, so its run says
// "not checked", never that the slice has no commit.
func TestLastRunOfASliceWhoseRepoWasNotReadIsNotChecked(t *testing.T) {
	t.Parallel()
	const c1 = "1111111111111111111111111111111111111111"
	oracle := func(slice string) journal.Line {
		return journal.Line{Event: journal.EventOracle, Slice: slice, Commit: c1, Outcome: "pass"}
	}
	truth := Truth{
		Ticket:       "T-1",
		Slices:       []string{"here", "there"},
		Lines:        []journal.Line{oracle("here"), oracle("there")},
		Commits:      []phase.CommitRef{{SHA: c1, Slice: "here"}},
		BranchKnown:  true,
		BranchSlices: map[string]bool{"here": true},
	}
	got := map[string]string{}
	for _, r := range truth.LastRuns() {
		got[r.Slice] = r.Covers
	}
	if got["here"] != "yes" || got["there"] != "not checked" {
		t.Errorf("covers = %v, want yes for the read repo's slice and not checked for the other", got)
	}
	truth.BranchSlices = nil
	for _, r := range truth.LastRuns() {
		if r.Slice == "there" && r.Covers != "no: slice has no commit on the branch" {
			t.Errorf("with every branch read, a slice with no commit says %q", r.Covers)
		}
	}
}

// TestARunAtALaterCommitCoversTheSlicesLastCommit: slices verified one after
// the other at HEAD: a run at a commit after the slice's last one covers it,
// and a slice commit made after the run does not. The Stop hook's history rule
// is the same function.
func TestARunAtALaterCommitCoversTheSlicesLastCommit(t *testing.T) {
	t.Parallel()
	const (
		a1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		b1 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		a2 = "cccccccccccccccccccccccccccccccccccccccc"
	)
	log := []phase.CommitRef{{SHA: a1, Slice: "a"}, {SHA: b1, Slice: "b"}, {SHA: a2, Slice: "a"}}
	for _, c := range []struct {
		commit, at string
		want       bool
	}{
		{a1, a1, true}, {a1, b1, true}, {a1, a2, true}, {b1, a1, false}, {a2, b1, false}, {"", a1, false}, {a1, "dddd", false},
	} {
		if got := AtOrAfter(log, c.commit, c.at); got != c.want {
			t.Errorf("AtOrAfter(%.1s, %.1s) = %v, want %v", c.commit, c.at, got, c.want)
		}
	}
	oracle := func(slice, commit string) journal.Line {
		return journal.Line{Event: journal.EventOracle, Slice: slice, Commit: commit, Outcome: "pass"}
	}
	truth := Truth{Ticket: "T-1", Slices: []string{"a", "b"}, Commits: log[:2], BranchKnown: true,
		Lines: []journal.Line{oracle("a", b1), oracle("b", b1)}}
	for _, r := range truth.LastRuns() {
		if r.Covers != "yes" {
			t.Errorf("slice %s verified at HEAD (b's commit) covers %q, want yes", r.Slice, r.Covers)
		}
	}
	truth.Commits = log // a made another commit after the run
	for _, r := range truth.LastRuns() {
		want := "yes"
		if r.Slice == "a" {
			want = "no: slice's last commit is " + a2[:12]
		}
		if r.Covers != want {
			t.Errorf("slice %s after a later commit of a covers %q, want %q", r.Slice, r.Covers, want)
		}
	}
}
