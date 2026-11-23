package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// TestTheSliceTrackReadsClaimedRunVerified: the track of a slice under way has
// three stages. A claim reaches the first; an oracle line reaches the second,
// whether or not it covered a commit; an old verify-pending line reaches
// neither, and no stage is worded committed or verifying.
func TestTheSliceTrackReadsClaimedRunVerified(t *testing.T) {
	t.Parallel()
	g := cardRig(t, 100, 40)
	v := g.view()
	if !strings.Contains(v, "claimed ── run ── verified") {
		t.Errorf("a claimed slice with an old pending line does not read at its first stage:\n%s", v)
	}
	if strings.Contains(v, "committed") || strings.Contains(v, "verifying") {
		t.Errorf("the track still has a stage of a worker's:\n%s", v)
	}

	// A run that covered nothing is still a run.
	g.feed(jl(at(2, 20), "SHOP-1", "clamp", journal.EventOracle, with("s-b", sha('b'), "pass", "", "", ""), func(l *journal.Line) {
		l.Uncovered = journal.UncoveredDirtyBefore
	}))
	if v := g.view(); !strings.Contains(v, "claimed ━━ run ── verified") {
		t.Errorf("an oracle line does not reach the run stage:\n%s", v)
	}
}

// TestALoneOraclePassIsBuildingNotVerifying: the build screen's commit list
// starts a commit that has only a recorded oracle pass, and no verified line,
// as building, and one with only an old pending line as having no recorded
// result. A verified line makes it green; a failure stalls it.
func TestALoneOraclePassIsBuildingNotVerifying(t *testing.T) {
	t.Parallel()
	a, b, c := sha('a'), sha('b'), sha('c')
	tk := ticketOf("T",
		jl(at(0, 1), "T", "s", journal.EventVerifyPending, with("s-b", a, "", "first", "", "")),
		jl(at(0, 2), "T", "s", journal.EventOracle, with("s-b", b, "pass", "", "", "")),
		jl(at(0, 3), "T", "s", journal.EventOracle, with("s-b", c, "pass", "", "", "")),
		jl(at(0, 4), "T", "s", journal.EventVerified, with("s-b", c, "", "", "", "")),
	)
	got := map[string]string{}
	for _, r := range tk.commits("s") {
		got[r.SHA] = r.State
	}
	want := map[string]string{a: StateQueued, b: StateBuilding, c: StateGreen}
	for sha, state := range want {
		if got[sha] != state {
			t.Errorf("commit %s is %q, want %q (all: %v)", short(sha), got[sha], state, got)
		}
	}

	// On the build screen a commit with only a pending line says so.
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"})+axi.Table("verification_states", []string{"slice", "commit", "state", "clear"}, [][]string{{"a", a[:12], "no recorded result", "jig verify JIG-1 --slice a"}}), 0)
	g.feed(jl(at(0, 1), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", a, "", "first", "", ""))).press("enter")
	text := pageText(g)
	if !strings.Contains(text, "no recorded result") || strings.Contains(text, "verifying") {
		t.Errorf("the build screen does not say the commit has no recorded result:\n%s", text)
	}
}
