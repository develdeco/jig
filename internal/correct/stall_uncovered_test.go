package correct

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestStallsSkipAnUncoveredRun: a failed run that covers no commit (a dirty
// tree, another command tried on the side) is no failure of the slice's work,
// so two of them are no stall.
func TestStallsSkipAnUncoveredRun(t *testing.T) {
	t.Parallel()
	a, b := oracleFail("a", "x", 1), oracleFail("a", "x", 2)
	a.Uncovered, b.Uncovered = journal.UncoveredDirtyBefore, journal.UncoveredOtherCmd
	if got := Stalls([]journal.Line{a, b}); got != nil {
		t.Errorf("two uncovered failures = %+v, want no stall", got)
	}
	if got := Stalls([]journal.Line{a, oracleFail("a", "x", 3), b}); got != nil {
		t.Errorf("one covered failure between uncovered ones = %+v, want no stall", got)
	}
}
