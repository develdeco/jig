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
// green, clamp is being built, cart waits for clamp.
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
	goldenCuts(t, "ticket-substeps", shop1Rig(t)) // a rig of its own: the check expands and folds its marks

	steps, subs := 0, 0
	for _, r := range g.m.page().refs() {
		switch r.kind {
		case rowLog:
		case rowSub:
			subs++
		default:
			steps++
		}
	}
	if steps != 5 || subs == 0 {
		t.Errorf("the page has %d rows for the steps and %d for the sub-steps, want the 5 steps and the sub-steps the pane draws", steps, subs)
	}
	// The names of the columns take a row of each panel, so the sub-steps are read
	// in a taller window than the golden's.
	g.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	for _, tc := range []struct {
		keys  []string
		title string
		wants []string
	}{
		{[]string{"up"}, "Brief ─", []string{"✓ brief drafted", "✓ slices written  3 slices", "✓ brief approved  Dana (person)"}},
		{[]string{"down", "down"}, "Review ─", []string{"○ round 1"}},
		{[]string{"down"}, "Publish ─", []string{"○ body", "○ pull request", "○ changelog", "○ published"}},
		{[]string{"up", "up"}, "Build ─", []string{"✓ slice round  verified aaaaaaa", "◐ slice clamp", "○ slice cart   waits for clamp"}},
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
// more (substepCap), and counts only what it cannot list. The slice under way
// opens into a card, which may take its extra rows beyond that room.
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

	for _, height := range []int{26, 42} {
		g := newRig(t, "BIG-1")
		g.r.answer("status BIG-1 --store /s", status, 0)
		g.send(tea.WindowSizeMsg{Width: 80, Height: height})
		g.feed(lines...)
		v := g.view()
		rowsOf := strings.Split(v, "\n")
		if len(rowsOf) != height {
			t.Errorf("height %d: the screen has %d lines, want its height:\n%s", height, len(rowsOf), v)
		}
		block, act := panelLines(rowsOf, "Build ─"), panelLines(rowsOf, "Activity")
		room := substepCap(g.m.bodyRows())
		if height == 26 && room != 6 {
			t.Errorf("height 26: the block has room for %d sub-steps, want 6", room)
		}
		tk := g.m.w.tickets["BIG-1"]
		subs := g.m.phaseOf(tk).Of(phase.Build).Subs
		extra := g.m.openWant(tk, stepBuild, subs[focusIndex(subs)]) - 1
		if len(block) < 3 || len(block) > min(9, room)+3+extra {
			t.Errorf("height %d: the sub-steps block takes %d lines, want its frame, 1 to %d sub-steps and the card's %d more:\n%s", height, len(block), room, extra, v)
		}
		if len(act) < 6 {
			t.Errorf("height %d: the activity panel takes %d lines, want its three, the names and the frame:\n%s", height, len(act), v)
		}
		joined := strings.Join(block, "\n")
		if !strings.Contains(joined, "slice s6") || strings.Contains(joined, "more") != (room < 9) {
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
		// The panel's bottom edge is at its own indent: a card inside it has
		// an edge of its own one level in.
		for j := i; j < len(rows); j++ {
			if strings.HasPrefix(rows[j], "  ╰") {
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
	for name, c := range map[string]struct {
		g      *rig
		panels int
	}{
		"a build with a question": {func() *rig { g := newRig(t, "JIG-1"); g.feed(jig1()...); return g }(), 3},
		// The asks are drawn whole, which leaves the sub-steps no room.
		"a round with asks":   {func() *rig { g := sized(newRig(t, "JIG-2"), 80, 25); g.feed(jig2Mid()...); return g }(), 2},
		"a build in progress": {shop1Rig(t), 3},
	} {
		v := c.g.view()
		if n := strings.Count(v, "\n") + 1; n != c.g.m.height {
			t.Errorf("%s: the screen has %d lines, want %d:\n%s", name, n, c.g.m.height, v)
		}
		if !strings.Contains(v, "╭─ Activity") || strings.Count(v, "╰") < c.panels {
			t.Errorf("%s: the Steps, sub-steps and activity panels are not all drawn to their edges:\n%s", name, v)
		}
	}
}

// TestSubstepsOfAPublishInTwoReposListAPullRequestForEach: the sub-steps of the
// publish step have a pull request for each repo, each with its own url, and the
// activity names the repo a line is about.
func TestSubstepsOfAPublishInTwoReposListAPullRequestForEach(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Repos(), 0)
	g.send(tea.WindowSizeMsg{Width: 80, Height: 33}) // the landing stop is a row of the needs-you block
	g.feed(jig2Repos()...).press("up")               // the cursor follows the land step
	v := g.view()
	for _, want := range []string{
		"Publish ─", "✓ pull request api  https://example.test/acme/api/pull/7",
		"✓ pull request web  https://example.test/acme/web/pull/3", "✓ published",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the ticket screen lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "○ pull request ·") {
		t.Errorf("the ticket screen lists the one pull request step beside the two:\n%s", v)
	}
}
