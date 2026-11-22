package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestSliceCountsComeFromTheDigestThenTheStatus: a ticket with a failed slice, a
// stopped one and a green one is read from the digest's counts until its status
// is read: main's row and the Build step show it failing, with the digest's
// words. The status's list takes over after, and the two agree.
func TestSliceCountsComeFromTheDigestThenTheStatus(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	d := task("build", false, "done|approved by Ada", "current|1 of 3 green · slice b failed its last run", "pending|after the build", "pending|after a clean review", "pending|after the publish")
	d.Slices = &digestSlices{Total: 3, Green: 1, Failed: 1, Stopped: 1}
	g.m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Ticket", State: "ready", Digest: d}})
	tk := g.m.w.tickets["JIG-1"]

	counts, n := tk.sliceCounts(g.m.zone)
	if n != 3 || counts[StateGreen] != 1 || counts[StateFail] != 1 || counts[StateBuilding] != 1 {
		t.Errorf("the digest's counts read %v of %d, want one green, one failed and the stopped one still building", counts, n)
	}
	build := g.m.steps(tk)[stepBuild]
	if build.State != StateFail || build.Text != "1 of 3 green · slice b failed its last run" {
		t.Errorf("the Build step before the status is %+v, want failing, in the digest's words", build)
	}
	main := pagesText(g.m.mainPage())
	if !strings.Contains(main, "1 of 3 green · slice b failed its last run") || !strings.Contains(main, g.m.st.icon(StateFail)) {
		t.Errorf("main before the status does not show the failing build:\n%s", main)
	}

	// The status is read: the store lists the same three slices.
	tk.status = parseStatus(slicesStatus(
		[]string{"a", "green", "1", "-", "-", "Greet", "app", "unit", "-"},
		[]string{"b", "building", "2", "-", "-", "Bye", "app", "unit", "-"},
		[]string{"c", "building", "1", "-", "-", "Wave", "app", "unit", "-"},
	))
	tk.lines = append(tk.lines,
		jl(at(0, 1), "JIG-1", "b", "claim"),
		jl(at(0, 5), "JIG-1", "b", "oracle", with("s", sha('b'), "fail", "boom", "", "")),
		jl(at(0, 1), "JIG-1", "c", "claim"),
		jl(at(0, 5), "JIG-1", "c", "oracle", with("s", sha('c'), "fail", "", "", ""), func(l *journal.Line) { l.Cause = journal.CauseStopped }),
	)
	after, n := tk.sliceCounts(g.m.zone)
	if n != 3 || after[StateGreen] != counts[StateGreen] || after[StateFail] != counts[StateFail] || after[StateBuilding] != counts[StateBuilding] {
		t.Errorf("the status's counts %v of %d differ from the digest's %v", after, n, counts)
	}
	if build := g.m.steps(tk)[stepBuild]; build.State != StateFail || build.Text != "1 of 3 green · slice b failed its last run" {
		t.Errorf("the Build step after the status is %+v, want the same", build)
	}
}

// TestSliceListsSayReadingUntilTheStatusIsRead: the screens that list a ticket's
// slices list none, and say the status is being read, until it has been: they
// guess no slice from the journal's lines.
func TestSliceListsSayReadingUntilTheStatusIsRead(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	d := task("build", false, "done|approved by Ada", "current|1 of 2 green", "pending|after the build", "pending|after a clean review", "pending|after the publish")
	g.m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Ticket", State: "ready", Digest: d}})
	g.m.w.apply(jig1())
	tk := g.m.w.tickets["JIG-1"]
	if tk.status != nil {
		t.Fatal("the status was read")
	}
	for name, page := range map[string]page{
		"brief":  g.m.briefPage("JIG-1"),
		"build":  g.m.buildPage("JIG-1"),
		"review": g.m.reviewPage("JIG-1"),
		"slice":  g.m.slicePage("JIG-1", "a"),
	} {
		text := pagesText(page)
		if !strings.Contains(text, "reading the ticket's status") {
			t.Errorf("the %s screen does not say the status is being read:\n%s", name, text)
		}
	}
}
