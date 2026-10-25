package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// shop1 is a ticket in the middle of its build with three slices: round is
// green, clamp is being verified at a commit, cart waits for clamp.
func shop1() []events.Envelope {
	return []events.Envelope{
		jl(at(0, 0), "SHOP-1", "", journal.EventApproval, with("s-planner", "", "", "Dana <dana@example.invalid>", "0123456789ab", "person")),
		jl(at(1, 0), "SHOP-1", "round", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(1, 5), "SHOP-1", "round", journal.EventVerifyPending, with("s-builder", sha('a'), "", "Round totals to cents", "", "")),
		jl(at(1, 6), "SHOP-1", "round", journal.EventOracle, with("", sha('a'), "pass", "", "", "")),
		jl(at(1, 7), "SHOP-1", "round", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(2, 0), "SHOP-1", "clamp", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(2, 10), "SHOP-1", "clamp", journal.EventVerifyPending, with("s-builder", sha('b'), "", "Clamp negative quantities", "", "")),
	}
}

// statusSHOP1 is `jig status SHOP-1` for shop1.
func statusSHOP1() string {
	return strings.Join([]string{
		"ticket: SHOP-1",
		"state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round totals to cents"},
			{"clamp", "building", "1", "-", "-", "Clamp negative quantities"},
			{"cart", "queued", "0", "clamp", "-", "Show the cart total"},
		}),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Dana"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "person"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{{"Goal", "-"}, {"Totals", "round+clamp"}, {"Cart", "cart"}}),
		axi.Help("Run `jig next SHOP-1` to see what is ready, then `jig claim SHOP-1 --slice <slice>` to take one; ask: Build SHOP-1"),
	}, "\n") + "\n"
}

// shop1Rig is a screen on SHOP-1 with its status read.
func shop1Rig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", statusSHOP1(), 0)
	g.feed(shop1()...)
	return g
}

// TestTicketScreenShowsTheSubstepsOfTheStepUnderTheCursor: under the Steps
// panel is a checklist of the step's sub-steps, with a state, a label and a
// few words each; it follows the cursor, and the cursor's rows are the steps
// and what needs a person, as they were.
func TestTicketScreenShowsTheSubstepsOfTheStepUnderTheCursor(t *testing.T) {
	t.Parallel()
	g := shop1Rig(t)
	golden(t, "ticket-substeps", g.view())

	if refs := g.m.page().refs(); len(refs) != 4 {
		t.Errorf("the page has %d rows for the cursor, want the 4 steps and no sub-step", len(refs))
	}
	for _, tc := range []struct {
		keys  []string
		title string
		wants []string
	}{
		{[]string{"up"}, "1 Brief · sub-steps", []string{"✓ brief drafted", "✓ slices written · 3 slices", "✓ brief approved · Dana (person)"}},
		{[]string{"down", "down"}, "3 Review · sub-steps", []string{"○ round 1"}},
		{[]string{"down"}, "4 Publish · sub-steps", []string{"○ body", "○ pull request", "○ changelog", "○ published"}},
		{[]string{"up", "up"}, "2 Build · sub-steps", []string{"✓ slice round · verified aaaaaaa", "◐ slice clamp · verifying bbbbbbb", "○ slice cart · waits for clamp"}},
	} {
		g.press(tc.keys...)
		v := g.view()
		for _, want := range append([]string{tc.title}, tc.wants...) {
			if !strings.Contains(v, want) {
				t.Errorf("after %v the screen lacks %q:\n%s", tc.keys, want, v)
			}
		}
	}
}

// TestSubstepsListAtMostSixAndTheActivityKeepsThree: a build with more
// slices than the block has room for shows the ones that matter and counts
// the rest, and the screen keeps its height with the activity's three lines. On
// the 80x24 screen the room is six sub-steps; a taller terminal has room for
// more (substepCap), and counts only what it cannot list.
func TestSubstepsListAtMostSixAndTheActivityKeepsThree(t *testing.T) {
	t.Parallel()
	var rows [][]string
	var lines []events.Envelope
	for i := 1; i <= 9; i++ {
		id := fmt.Sprintf("s%d", i)
		state := "green"
		switch {
		case i == 6:
			state = "building"
		case i > 6:
			state = "queued"
		}
		rows = append(rows, []string{id, state, "1", "-", "-", "Slice " + id})
		if i <= 6 {
			lines = append(lines,
				jl(at(i, 0), "BIG-1", id, journal.EventClaim, with("s-builder", "", "", "", "", "")),
				jl(at(i, 30), "BIG-1", id, journal.EventVerifyPending, with("s-builder", sha(byte('0'+i)), "", "", "", "")))
		}
		if i < 6 {
			lines = append(lines, jl(at(i, 40), "BIG-1", id, journal.EventVerified, with("", sha(byte('0'+i)), "", "", "", "")))
		}
	}
	status := strings.Join([]string{"ticket: BIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, rows), "questions: none"}, "\n") + "\n"

	for _, height := range []int{24, 40} {
		g := newRig(t, "BIG-1")
		g.r.answer("status BIG-1 --store /s", status, 0)
		g.send(tea.WindowSizeMsg{Width: 80, Height: height})
		g.feed(lines...)
		v := g.view()
		rowsOf := strings.Split(v, "\n")
		if len(rowsOf) != height {
			t.Errorf("height %d: the screen has %d lines, want its height:\n%s", height, len(rowsOf), v)
		}
		block, act := panelLines(rowsOf, "2 Build · sub-steps"), panelLines(rowsOf, "2 Build · activity")
		room := substepCap(g.m.bodyRows())
		if height == 24 && room != 6 {
			t.Errorf("height 24: the block has room for %d sub-steps, want 6", room)
		}
		if len(block) < 3 || len(block) > min(9, room)+2 {
			t.Errorf("height %d: the sub-steps block takes %d lines, want its frame and 1 to %d sub-steps:\n%s", height, len(block), room, v)
		}
		if len(act) < 5 {
			t.Errorf("height %d: the activity panel takes %d lines, want its three and the frame:\n%s", height, len(act), v)
		}
		joined := strings.Join(block, "\n")
		if !strings.Contains(joined, "slice s6 · verifying") || strings.Contains(joined, "more") != (room < 9) {
			t.Errorf("height %d: the block does not show the slice under way and count the rest (room for %d of 9):\n%s", height, room, v)
		}
		if !strings.Contains(joined, "6 of 9 done") && !strings.Contains(joined, "5 of 9 done") {
			t.Errorf("height %d: the block does not count what is done:\n%s", height, v)
		}
	}
}

// panelLines are the lines of the panel titled title, from its top edge to its
// bottom edge; none when the screen cuts it.
func panelLines(rows []string, title string) []string {
	for i, r := range rows {
		if !strings.Contains(r, "╭─ "+title) {
			continue
		}
		for j := i; j < len(rows); j++ {
			if strings.Contains(rows[j], "╰") {
				return rows[i : j+1]
			}
		}
	}
	return nil
}

// TestTheTicketScreenFitsWhateverTheStepsAreDoing: at 80 by 24 the ticket
// screen keeps its height, and its newest activity line and the panel's edge in
// view, with the sub-steps block and with a question and asks waiting.
func TestTheTicketScreenFitsWhateverTheStepsAreDoing(t *testing.T) {
	t.Parallel()
	for name, g := range map[string]*rig{
		"a build with a question": func() *rig { g := newRig(t, "JIG-1"); g.feed(jig1()...); return g }(),
		"a round with asks":       func() *rig { g := newRig(t, "JIG-2"); g.feed(jig2Mid()...); return g }(),
		"a build in progress":     shop1Rig(t),
	} {
		v := g.view()
		if n := strings.Count(v, "\n") + 1; n != 24 {
			t.Errorf("%s: the screen has %d lines, want 24:\n%s", name, n, v)
		}
		if !strings.Contains(v, "· activity") || strings.Count(v, "╰") < 3 {
			t.Errorf("%s: the Steps, sub-steps and activity panels are not all drawn to their edges:\n%s", name, v)
		}
	}
}

// TestPickingSubstepsKeepsWhatNeedsAPersonAndWhatIsUnderWay: when the block
// has room for fewer sub-steps than there are, the ones shown are a person's
// turn and a failure first, then what is under way, what waits, what is next,
// and the latest of what is done, in their order.
func TestPickingSubstepsKeepsWhatNeedsAPersonAndWhatIsUnderWay(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	status := strings.Join([]string{"ticket: SHOP-2", "state: parked",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "A"}, {"b", "green", "1", "-", "-", "B"}, {"c", "needs-input", "1", "-", "q-001", "C"},
			{"d", "stalled", "1", "-", "-", "D"}, {"e", "building", "1", "-", "-", "E"}, {"f", "queued", "0", "e", "-", "F"},
		}),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}}),
	}, "\n") + "\n"
	tk := ticketOf("SHOP-2")
	tk.status = parseStatus(status)
	g.m.w.tickets["SHOP-2"] = tk
	subs := g.m.phaseOf(tk).Of("build").Subs
	if len(subs) != 6 {
		t.Fatalf("sub-steps = %+v, want one per slice", subs)
	}
	var got []string
	for _, i := range pickSubs(subs, 4) {
		got = append(got, subs[i].Key)
	}
	// Room for four: three sub-steps and the count. The slice that waits for
	// an answer and the stalled one come first, then the one under way.
	if want := "slice:c slice:d slice:e"; strings.Join(got, " ") != want {
		t.Errorf("picked %v, want %s", got, want)
	}
	got = nil
	for _, i := range pickSubs(subs, 1) {
		got = append(got, subs[i].Key)
	}
	if want := "slice:c"; strings.Join(got, " ") != want {
		t.Errorf("picked %v for one line, want %s alone", got, want)
	}
	if all := pickSubs(subs, 6); len(all) != 6 {
		t.Errorf("picked %v for six lines, want all of them", all)
	}
	// With nothing under way, the latest of what is done shows.
	var finished []phase.Sub
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		finished = append(finished, phase.Sub{Key: "slice:" + id, State: phase.SubDone})
	}
	if got := pickSubs(finished, 3); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("picked %v of five done, want the latest two (3 and 4) and a line for the count", got)
	}
}

// TestSubstepsOfAPublishInTwoReposListAPullRequestForEach: the sub-steps of the
// publish step have a pull request for each repo, each with its own url, and the
// activity names the repo a line is about.
func TestSubstepsOfAPublishInTwoReposListAPullRequestForEach(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Repos(), 0)
	g.feed(jig2Repos()...)
	v := g.view()
	for _, want := range []string{
		"4 Publish · sub-steps", "✓ pull request api · https://example.test/acme/api/pull/7",
		"✓ pull request web · https://example.test/acme/web/pull/3", "✓ published",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the ticket screen lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "○ pull request ·") {
		t.Errorf("the ticket screen lists the one pull request step beside the two:\n%s", v)
	}
}
