package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// bigBuild is BIG-1 with slices s1 to s<n>, the first <green> of them green, the
// next one being verified and the rest queued: its `jig status` and the journal
// lines a build like that leaves.
func bigBuild(n, green int) (status string, lines []events.Envelope) {
	var rows [][]string
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s%d", i)
		state := "queued"
		switch {
		case i <= green:
			state = "green"
		case i == green+1:
			state = "building"
		}
		rows = append(rows, []string{id, state, "1", "-", "-", "Slice " + id})
		if i <= green+1 {
			lines = append(lines,
				jl(at(i, 0), "BIG-1", id, journal.EventClaim, with("s-builder", "", "", "", "", "")),
				jl(at(i, 20), "BIG-1", id, journal.EventVerifyPending, with("s-builder", sha(byte('0'+i%10)), "", "Slice "+id, "", "")))
		}
		if i <= green {
			lines = append(lines,
				jl(at(i, 30), "BIG-1", id, journal.EventOracle, with("", sha(byte('0'+i%10)), "pass", "", "", "")),
				jl(at(i, 40), "BIG-1", id, journal.EventVerified, with("", sha(byte('0'+i%10)), "", "", "", "")))
		}
	}
	status = strings.Join([]string{"ticket: BIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, rows), "questions: none",
		axi.Help("Run `jig next BIG-1` to see what is ready, then `jig claim BIG-1 --slice <slice>` to take one; ask: Build BIG-1"),
	}, "\n") + "\n"
	return status, lines
}

// bigRig is a screen of size width by height on BIG-1, a build in progress.
func bigRig(t *testing.T, width, height, n, green int) *rig {
	t.Helper()
	status, lines := bigBuild(n, green)
	g := newRig(t, "BIG-1")
	g.r.answer("status BIG-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: width, Height: height})
	g.feed(lines...)
	return g
}

// TestATallTerminalShowsMoreActivityAndSubstepsAndLeavesNoEmptyArea: the demo
// pane is 50 rows. The activity panel and the sub-steps block scale with the
// body, so a build in progress fills the screen instead of using half of it:
// more activity lines than the 80x24 screen's eight, more sub-steps before the
// count of the rest, and the panel's bottom edge on the row above the keys.
func TestATallTerminalShowsMoreActivityAndSubstepsAndLeavesNoEmptyArea(t *testing.T) {
	t.Parallel()
	g := bigRig(t, 100, 50, 12, 8)
	v := g.view()
	golden(t, "ticket-tall", v)

	rows := strings.Split(v, "\n")
	if len(rows) != 50 {
		t.Fatalf("the screen has %d lines, want its height of 50:\n%s", len(rows), v)
	}
	block, act := panelLines(rows, "Build ─"), panelLines(rows, "Activity")
	if shown := len(block) - 2; shown <= substepLines {
		t.Errorf("the sub-steps block lists %d, want more than the %d of the 80x24 screen:\n%s", shown, substepLines, v)
	}
	if shown := len(act) - 2; shown <= activityLines {
		t.Errorf("the activity panel shows %d lines, want more than the %d of the 80x24 screen:\n%s", shown, activityLines, v)
	}
	if strings.Contains(strings.Join(block, "\n"), "more") {
		t.Errorf("the block counts sub-steps it has room for (%d of 12):\n%s", len(block)-2, v)
	}
	// The last panel's edge is the row above the keys' rule: no large empty area.
	edge, rule := -1, -1
	for i, r := range rows {
		switch {
		case strings.Contains(r, "╰"):
			edge = i
		case strings.HasPrefix(r, "─"):
			rule = i
		}
	}
	if gap := rule - edge - 1; edge < 0 || gap > 1 {
		t.Errorf("%d empty rows between the last panel and the keys, want at most 1:\n%s", gap, v)
	}
}

// TestTheScreenOf80By24IsAsItWasWhateverTheCapsScaleTo: the scaling adds
// nothing to the screen the caps were set for.
func TestTheScreenOf80By24IsAsItWasWhateverTheCapsScaleTo(t *testing.T) {
	t.Parallel()
	if substepCap(bodyRef) != substepLines || activityCap(bodyRef) != activityLines {
		t.Errorf("caps at %d rows = %d and %d, want %d and %d", bodyRef, substepCap(bodyRef), activityCap(bodyRef), substepLines, activityLines)
	}
	if substepCap(bodyRef-5) != substepLines || activityCap(bodyRef-5) != activityLines {
		t.Errorf("a screen shorter than 80x24 has other caps than the screen's: %d and %d", substepCap(bodyRef-5), activityCap(bodyRef-5))
	}
	g := newRig(t, "JIG-1")
	if g.m.bodyRows() != bodyRef {
		t.Fatalf("the 80x24 body is %d rows, but the caps were set for %d", g.m.bodyRows(), bodyRef)
	}
	if substepCap(45) <= substepLines || activityCap(45) <= activityLines {
		t.Errorf("a body of 45 rows has caps %d and %d, want more than %d and %d", substepCap(45), activityCap(45), substepLines, activityLines)
	}
}

// TestTheNextLineWrapsToTwoLinesAndIsCutOnlyAfterTheSecond: what to do next is
// a sentence, and it is not cut in the middle at 80 columns; past two lines it
// ends in an ellipsis.
func TestTheNextLineWrapsToTwoLinesAndIsCutOnlyAfterTheSecond(t *testing.T) {
	t.Parallel()
	g := shop1Rig(t)
	rows := strings.Split(g.view(), "\n")
	var next []string
	for i, r := range rows {
		if strings.HasPrefix(strings.TrimLeft(r, " "), "next ") {
			next = rows[i : i+2]
		}
	}
	if next == nil {
		t.Fatalf("no next line:\n%s", g.view())
	}
	got := strings.TrimLeft(next[0], " ") + " " + strings.TrimLeft(next[1], " ")
	if want := "next  Run `jig next SHOP-1` to see what is ready, then `jig claim SHOP-1 --slice <slice>` to take one; ask: Build SHOP-1"; strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(want), " ") {
		t.Errorf("the next line reads %q, want the whole sentence on two lines", got)
	}
	if strings.Contains(next[0], "…") || strings.Contains(next[1], "…") {
		t.Errorf("a sentence that fits on two lines is cut: %q", next)
	}
	if !strings.HasPrefix(next[1], "        --slice") {
		t.Errorf("the second line is %q, want it under the words of the first", next[1])
	}

	// Past two lines it is cut, with an ellipsis after the second only.
	long := strings.Repeat("and then do one more thing ", 12)
	g = newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", strings.Replace(statusSHOP1(), "Run `jig next SHOP-1`", long, 1), 0)
	g.feed(shop1()...)
	rows = strings.Split(g.view(), "\n")
	var seen []string
	for i, r := range rows {
		if strings.HasPrefix(strings.TrimLeft(r, " "), "next ") {
			seen = rows[i : i+3]
		}
	}
	if seen == nil || !strings.HasSuffix(seen[1], "…") || strings.Contains(seen[0], "…") || strings.HasPrefix(strings.TrimLeft(seen[2], " "), "and") {
		t.Errorf("a next line of many words: %q, want two lines, the second ending in an ellipsis", seen)
	}
}
