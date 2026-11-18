package verify

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// TestLastRunNamesItsResultAndWhetherItCoversTheLastCommit: a slice's last run
// is its latest oracle line, covering or not, with the line's outcome and
// cause in a word and whether the run is of the slice's last commit on the
// branch.
func TestLastRunNamesItsResultAndWhetherItCoversTheLastCommit(t *testing.T) {
	t.Parallel()
	const (
		c1 = "1111111111111111111111111111111111111111"
		c2 = "2222222222222222222222222222222222222222"
		c3 = "3333333333333333333333333333333333333333"
	)
	oracle := func(slice, commit, outcome string, mod ...func(*journal.Line)) journal.Line {
		l := journal.Line{Event: journal.EventOracle, Slice: slice, Commit: commit, Outcome: outcome, Seconds: 7}
		for _, m := range mod {
			m(&l)
		}
		return l
	}
	cause := func(c string) func(*journal.Line) { return func(l *journal.Line) { l.Cause = c } }
	uncovered := func(u string) func(*journal.Line) { return func(l *journal.Line) { l.Uncovered = u } }

	truth := Truth{
		Ticket: "T-1",
		Slices: []string{"a", "b", "c", "d", "e", "f"},
		Lines: []journal.Line{
			oracle("a", c1, "fail"),
			oracle("a", c2, "pass"), // a's last run is of its last commit
			oracle("b", c1, "pass"), // a commit after the run
			oracle("c", c3, "fail", cause(journal.CauseStopped)),
			oracle("d", c3, "fail", cause(journal.CauseTimeout)),
			oracle("e", c3, "pass", uncovered(journal.UncoveredDirtyBefore)), // the last thing witnessed, covers nothing
			oracle("f", c1, "pass"),                                          // no commit of f on the branch
			{Event: journal.EventVerified, Slice: "a", Commit: c2},           // not a run
			{Event: journal.EventOracle, Commit: c1, Outcome: "pass"},        // no slice
		},
		Commits:     []phase.CommitRef{{SHA: c1, Slice: "a"}, {SHA: c1, Slice: "b"}, {SHA: c2, Slice: "a"}, {SHA: c2, Slice: "b"}, {SHA: c3, Slice: "c"}, {SHA: c3, Slice: "d"}, {SHA: c3, Slice: "e"}},
		BranchKnown: true,
	}
	type want struct{ result, covers string }
	for slice, w := range map[string]want{
		"a": {"pass", "yes"},
		"b": {"pass", "no: slice's last commit is " + c2[:12]},
		"c": {"stopped", "yes"},
		"d": {"timed out", "yes"},
		"e": {"pass", "no: tree dirty before"},
		"f": {"pass", "no: slice has no commit on the branch"},
	} {
		var got *LastRun
		runs := truth.LastRuns()
		for i := range runs {
			if runs[i].Slice == slice {
				got = &runs[i]
			}
		}
		if got == nil || got.Result != w.result || got.Covers != w.covers {
			t.Errorf("slice %s: last run = %+v, want %+v", slice, got, w)
		}
	}
	if runs := truth.LastRuns(); len(runs) != 6 || runs[0].Slice != "a" || runs[0].Line.Commit != c2 {
		t.Errorf("runs = %+v, want one per slice with an oracle line, in the order they first ran, the latest line each", runs)
	}

	// Without the branch no commit is known to be the slice's last.
	truth.BranchKnown, truth.Commits = false, nil
	for _, r := range truth.LastRuns() {
		want := "not checked"
		if r.Slice == "e" {
			want = "no: tree dirty before"
		}
		if r.Covers != want {
			t.Errorf("slice %s with no branch read covers %q, want %q", r.Slice, r.Covers, want)
		}
	}
}
