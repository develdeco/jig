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
