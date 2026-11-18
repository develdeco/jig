package phase

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestASliceWhoseLastCoveringRunFailedReadsFailed: a slice the store still has
// building, whose latest run that covered a commit failed, is a failed
// sub-step and the build's focus, not one being built. A run a stop or a
// timeout ended judged nothing, an uncovered run says nothing of a commit, and
// a later pass or verified line clears it.
func TestASliceWhoseLastCoveringRunFailedReadsFailed(t *testing.T) {
	t.Parallel()
	f := approved()
	f.Slices = []Slice{{ID: "a", State: "building"}, {ID: "b", State: "building"}, {ID: "c", State: "building"}, {ID: "d", State: "building"},
		{ID: "e", State: "building"}, {ID: "f", State: "env-blocked"}, {ID: "g", State: "queued"}}
	oracle := func(slice, outcome string, mod ...func(*journal.Line)) journal.Line {
		return ln(journal.EventOracle, append([]func(*journal.Line){onSlice(slice), commit("abc1234def"), func(l *journal.Line) { l.Outcome = outcome }}, mod...)...)
	}
	lines := []journal.Line{
		ln(journal.EventClaim, onSlice("a"), by("s-1")), oracle("a", "fail"),
		ln(journal.EventClaim, onSlice("b"), by("s-1")), oracle("b", "fail", func(l *journal.Line) { l.Cause = journal.CauseStopped }),
		ln(journal.EventClaim, onSlice("c"), by("s-1")), oracle("c", "fail"), oracle("c", "pass"),
		ln(journal.EventClaim, onSlice("d"), by("s-1")), oracle("d", "fail"), oracle("d", "pass", func(l *journal.Line) { l.Uncovered = journal.UncoveredDirtyBefore }),
		// A failure, then a run a stop ended: the last covering run judged nothing.
		ln(journal.EventClaim, onSlice("e"), by("s-1")), oracle("e", "fail"), oracle("e", "fail", func(l *journal.Line) { l.Cause = journal.CauseStopped }),
		// A failure, then the environment did not come up; a failure, then a release.
		ln(journal.EventClaim, onSlice("f"), by("s-1")), oracle("f", "fail"),
		ln(journal.EventClaim, onSlice("g"), by("s-1")), oracle("g", "fail"), ln(journal.EventRelease, onSlice("g"), by("s-1")),
	}
	b := Derive(f, lines).Of(Build)
	got := map[string]Sub{}
	for _, s := range b.Subs {
		got[s.Key] = s
	}
	if s := got["slice:a"]; s.State != SubFailed || s.Detail != "failed its last run" {
		t.Errorf("a = %+v, want failed", s)
	}
	if s := got["slice:b"]; s.State != SubActive {
		t.Errorf("b (a stopped run judged nothing) = %+v, want active", s)
	}
	if s := got["slice:c"]; s.State != SubActive {
		t.Errorf("c (passed after failing) = %+v, want active", s)
	}
	if s := got["slice:d"]; s.State != SubFailed {
		t.Errorf("d (an uncovered pass after a covering failure) = %+v, want failed", s)
	}
	if s := got["slice:e"]; s.State != SubActive {
		t.Errorf("e (a failure, then a stopped run) = %+v, want active: its last run judged nothing", s)
	}
	if s := got["slice:f"]; s.State != SubWaiting || s.Detail != WaitsForEnvironment {
		t.Errorf("f (a failure, then env-blocked) = %+v, want waiting for its environment", s)
	}
	if s := got["slice:g"]; s.State == SubFailed {
		t.Errorf("g (a failure, then a release) = %+v, want its queued state", s)
	}
	if !strings.Contains(b.Text, "slice a failed its last run") {
		t.Errorf("build text = %q, want the failed slice named", b.Text)
	}
}
