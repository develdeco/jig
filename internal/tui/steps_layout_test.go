package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// longLand is a Land text as long as the screens meet: the pull request, the
// branch it goes into and what it waits for.
const longLand = "waits to land · PR #503 into 501-review-rounds-stop-repeating-work · lands after SHOP-3"

// longStepsRig is a ticket in the middle of its build whose slices have long
// names, whose builder's session has ended so the build says since when it is
// idle, and whose land step has a long text.
func longStepsRig(t *testing.T, w, h int, ids [3]string, land bool) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{ids[0], "green", "1", "-", "-", "Round totals to cents"},
			{ids[1], "building", "1", "-", "-", "Clamp negative quantities"},
			{ids[2], "queued", "0", ids[1], "-", "Show the cart total"},
		}),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Dana"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "person"}}),
	}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	atClock(g, "2026-10-23T09:14:00Z")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(
		jl(at(0, 0), "SHOP-1", "", journal.EventApproval, with("s-planner", "", "", "Dana <dana@example.invalid>", "0123456789ab", "person")),
		jl(at(1, 0), "SHOP-1", ids[0], journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(1, 5), "SHOP-1", ids[0], journal.EventVerifyPending, with("s-builder", sha('a'), "", "Round totals to cents", "", "")),
		jl(at(1, 6), "SHOP-1", ids[0], journal.EventOracle, with("", sha('a'), "pass", "", "", "")),
		jl(at(1, 7), "SHOP-1", ids[0], journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(2, 0), "SHOP-1", ids[1], journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(2, 10), "SHOP-1", ids[1], journal.EventVerifyPending, with("s-builder", sha('b'), "", "Clamp negative quantities", "", "")),
		ll("2026-10-23T09:03:00Z", "", journal.EventSessionEnd, func(l *journal.Line) { l.Session = "s-builder" }),
	)
	if land {
		g.pin("SHOP-1", task("land", false, "done|approved", "done|3 slices green", "done|round 1 clean", "done|pull request opened", "current|"+longLand))
		g.syncDigests()
	}
	return g
}

// TestTicketStepsLaidOutAtThreeSizes: a ticket's steps and sub-steps with a
// long sub-step label and a long Land text draw every label whole while the
// width allows, always show the clock, and wrap the words under their column.
func TestTicketStepsLaidOutAtThreeSizes(t *testing.T) {
	t.Parallel()
	ids := [3]string{"round-totals-to-cents-in-every-currency", "clamp-negative-quantities-in-the-cart", "show-the-cart-total-under-each-line"}
	for _, size := range layoutSizes {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("land %dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := longStepsRig(t, w, h, ids, true)
			view := stripANSI(g.view())
			lines := strings.Split(view, "\n")
			wantNoRowWider(t, lines, w)

			// The land row: its words, in order, across the row and the rows under it.
			block := rowBlockOf(lines, "5 Land")
			if block == nil {
				t.Fatalf("the Land step is not drawn:\n%s", view)
			}
			// A word wider than the column is broken at it, so the words are
			// compared with the breaks and the frame taken out.
			text := strings.Join(block, "\n")
			if !strings.Contains(squeeze(text), squeeze(longLand)) {
				t.Fatalf("the Land text is cut:\n%s", text)
			}
			if w <= 80 && len(block) < 2 {
				t.Errorf("at %d wide the Land words are not wrapped under their column:\n%s", w, text)
			}
			if w >= 230 && len(block) != 1 {
				t.Errorf("at %d wide the Land text is not on one row:\n%s", w, text)
			}
		})
		t.Run(fmt.Sprintf("build %dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := longStepsRig(t, w, h, ids, false)
			view := stripANSI(g.view())
			lines := strings.Split(view, "\n")
			wantNoRowWider(t, lines, w)
			// The build is idle and its row always says since when.
			if row := rowBlockOf(lines, "◐ 2 "); row == nil || !strings.Contains(strings.Join(row, "\n"), "idle since") {
				t.Errorf("the build step does not say it is idle:\n%s", view)
			}
			// A label that is drawn is whole while the width allows; the block
			// has the room it has, which may not hold every sub-step.
			drawn := 0
			for _, id := range ids {
				if strings.Contains(view, "slice "+id[:12]) {
					drawn++
					if w >= 80 && !strings.Contains(view, "slice "+id) {
						t.Errorf("at %d wide the label of slice %s is cut:\n%s", w, id, view)
					}
				}
			}
			if drawn == 0 && w >= 80 {
				t.Errorf("no sub-step is drawn:\n%s", view)
			}
		})
	}
}

// squeeze is s without its spaces, line breaks and frame, so text wrapped across
// rows can be compared with the text it came from.
func squeeze(s string) string {
	return strings.NewReplacer(" ", "", "\n", "", "│", "", "▸", "").Replace(s)
}

// rowBlockOf is the lines of a step on a screen: the one that names it, and the
// rows under it that wrap its words, which have nothing in the icon's column.
func rowBlockOf(lines []string, name string) []string {
	for i, l := range lines {
		at := strings.Index(l, name)
		if at < 0 || !strings.Contains(l, "│") {
			continue
		}
		icon := len([]rune(l[:at])) - 2
		block := []string{l}
		for _, next := range lines[i+1:] {
			cells := []rune(next)
			if strings.TrimSpace(next) == "" || strings.ContainsAny(next, "╭╰") || icon >= len(cells) || cells[icon] != ' ' || strings.Contains(next, "│  ") && strings.TrimSpace(string(cells[icon:icon+2])) != "" {
				break
			}
			block = append(block, next)
		}
		return block
	}
	return nil
}
