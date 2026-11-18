package phase

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestTheBuildTextNamesNoFailureForASliceTheStoreHasElsewhere: the text `jig
// next` and `jig here`'s prompt read says a slice failed its last run only
// while the store has it building. A slice that failed and then waits for its
// environment, or was released, is told as what the store says.
func TestTheBuildTextNamesNoFailureForASliceTheStoreHasElsewhere(t *testing.T) {
	t.Parallel()
	f := approved()
	f.Slices = []Slice{{ID: "f", State: "env-blocked"}, {ID: "g", State: "queued"}}
	fail := func(slice string) journal.Line {
		return ln(journal.EventOracle, onSlice(slice), commit("abc1234def"), func(l *journal.Line) { l.Outcome = "fail" })
	}
	lines := []journal.Line{
		ln(journal.EventClaim, onSlice("f"), by("s-1")), fail("f"),
		ln(journal.EventClaim, onSlice("g"), by("s-1")), fail("g"), ln(journal.EventRelease, onSlice("g"), by("s-1")),
	}
	b := Derive(f, lines).Of(Build)
	if !strings.Contains(b.Text, "slice f waits for its environment") || strings.Contains(b.Text, "failed") {
		t.Errorf("build text = %q, want the environment wait and no failure", b.Text)
	}
}
