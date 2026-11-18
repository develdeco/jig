package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// TestAFailedSliceReadsFailedNotBeingBuilt: the store sets a slice whose
// oracle failed back to building; the screens read the recorded last run, so
// the slice is failed on the ticket screen (its row, the build step and its
// sub-step) until a later covering run passes. A run a stop ended judged
// nothing and leaves it building.
func TestAFailedSliceReadsFailedNotBeingBuilt(t *testing.T) {
	t.Parallel()
	tk := ticketOf("T",
		jl(at(0, 1), "T", "a", journal.EventClaim),
		jl(at(0, 5), "T", "a", journal.EventOracle, with("s", sha('a'), "fail", "boom", "", "")),
		jl(at(0, 1), "T", "b", journal.EventClaim),
		jl(at(0, 5), "T", "b", journal.EventOracle, with("s", sha('b'), "fail", "", "", ""), func(l *journal.Line) { l.Cause = journal.CauseStopped }),
	)
	tk.status = &statusInfo{Slices: []statusSlice{{ID: "a", State: "building"}, {ID: "b", State: "building"}}}
	rows := tk.slices(time.UTC)
	if rows[0].State != StateFail || rows[1].State != StateBuilding {
		t.Errorf("states = %s, %s, want fail for the failed run and building for the stopped one", rows[0].State, rows[1].State)
	}

	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"}), 0)
	g.feed(
		jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(0, 5), "JIG-1", "a", journal.EventOracle, with("s-builder", sha('a'), "fail", "boom", "", "")),
	)
	if v := g.view(); !strings.Contains(v, "build failing") || !strings.Contains(v, "slice a failed its last run") || strings.Contains(v, "being built") {
		t.Errorf("the ticket screen does not read the slice as failed:\n%s", v)
	}
}
