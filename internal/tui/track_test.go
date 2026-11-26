package tui

import (
	"reflect"
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
// as building. A verified line makes it green; a failure stalls it. An old
// pending line is no commit, and the screen speaks of the commit it names only
// as the status's table does: with no recorded result.
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
	want := map[string]string{b: StateBuilding, c: StateGreen}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the commits are %v, want %v: an old pending line is no commit", got, want)
	}

	// On the build screen the commit a pending line names reads the state the
	// status's table gives it.
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"})+axi.Table("verification_states", []string{"slice", "commit", "state", "clear"}, [][]string{{"a", a[:12], "no recorded result", "jig verify JIG-1 --slice a"}}), 0)
	g.feed(jl(at(0, 1), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", a, "", "first", "", ""))).press("enter")
	text := pageText(g)
	if !strings.Contains(text, "no recorded result") || strings.Contains(text, "verifying") {
		t.Errorf("the build screen does not say the commit has no recorded result:\n%s", text)
	}
}
