package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// threeRoundsEvents is JIG-2 after three review rounds: round 1 raised four
// findings and sent a fix slice back, round 2 raised one more fix, round 3 was
// clean. carried, when it is not 0, is the round the third carries over.
func threeRoundsEvents(carried int) []events.Envelope {
	base := reviewFixRoundEvents()
	// The first fix slice's first try failed and its second passed: the two runs sit
	// between its claim and its verification.
	tries := []events.Envelope{
		jl(at(12, 25), "JIG-2", "fix-1-alpha", journal.EventOracle, with("", sha('b'), "fail", "TestClose: the handle leaked", "", ""), func(l *journal.Line) {
			l.Command, l.Seconds, l.Subject = "go test ./alpha/...", 7, "Close the file on the happy path"
		}),
		jl(at(12, 35), "JIG-2", "fix-1-alpha", journal.EventOracle, with("", sha('d'), "pass", "", "", ""), func(l *journal.Line) {
			l.Command, l.Seconds, l.Subject = "go test ./alpha/...", 8, "Close the file on every path"
		}),
	}
	base = append(base[:6:6], append(tries, base[6:]...)...)
	return append(base,
		jl(at(13, 50), "JIG-2", "fix-2-alpha", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(13, 55), "JIG-2", "fix-2-alpha", journal.EventVerified, with("", sha('e'), "", "", "", "")),
		jl(at(14, 0), "JIG-2", "", journal.EventGateOpen),
		jl(at(14, 30), "JIG-2", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session, l.Carried = 3, "s-reviewer", carried }),
	)
}

// threeRoundsStatus is `jig status JIG-2` for threeRoundsEvents: the findings of
// rounds 1 and 2, the fix slice each fix finding became, and a clean round 3.
func threeRoundsStatus(fix1 string) string {
	findingCols := findingColumns
	return strings.Join([]string{
		"ticket: JIG-2",
		"state: ready",
		"fix_budget: 2 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate", "findings"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "alpha", "unit", "-", "-"},
			{"fix-1-alpha", "green", "2", "-", "-", "Fix these gate findings:\n\nalpha/a.go:12 Close the file", "alpha", "unit", "1", fix1},
			{"fix-2-alpha", "green", "1", "-", "-", "Fix these gate findings:\n\nalpha/c.go:7 Check the returned error", "alpha", "unit", "2", "r2-f1"},
		}),
		"questions: none",
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "0", "3", "full", sha('0'), sha('c'), "4", "0", "-", "-"},
			{"2", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "0", "0", "delta", sha('c'), sha('d'), "3", "0", "-", "-"},
			{"3", "clean", "s-reviewer", "claude", "claude-opus-5", "true", "0", "0", "0", "delta", sha('d'), sha('e'), "2", "0", "-", "-"},
		}),
		axi.Table("findings", findingCols, [][]string{
			{"r1-f1", "1", "fix", "high", "alpha/a.go:12", "Close the file", "open", "a leaked handle fails the next open", "", "", "The file is opened and never closed.\nClose it on every path.", "auto"},
			{"r1-f2", "1", "note", "medium", "alpha/b.go:4", "Rename the helper", "noted", "a clearer name helps", "", "", "The name says nothing.", "auto"},
			{"r1-f3", "1", "note", "low", "alpha/b.go:9", "Trim the comment", "noted", "it is stale", "", "", "The comment repeats the code.", "auto"},
			{"r1-f4", "1", "note", "low", "alpha/c.go:2", "Order the imports", "noted", "tidier", "", "", "Group them.", "auto"},
			{"r2-f1", "2", "fix", "medium", "alpha/c.go:7", "Check the returned error", "open", "the error is dropped and the caller carries on", "", "", "The error from Write is ignored.", "human"},
		}),
		axi.Help("Run `jig publish JIG-2`"),
	}, "\n") + "\n"
}

// threeRounds is the review screen of JIG-2 after three rounds at w x h.
func threeRounds(t *testing.T, w, h, carried int) *rig {
	return threeRoundsFixing(t, w, h, carried, "r1-f1")
}

// threeRoundsFixing is threeRounds with the findings the first fix slice answers.
func threeRoundsFixing(t *testing.T, w, h, carried int, fix1 string) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", threeRoundsStatus(fix1), 0)
	g.feed(threeRoundsEvents(carried)...).press("3")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	return g.sized(w, h)
}

// cursorOn puts the cursor on the row of the page with that kind and id, giving
// the pane that holds it the focus, as moving the cursor there would.
func (g *rig) cursorOn(kind, id string) *rig {
	g.t.Helper()
	g.view()
	pg := g.m.page()
	for i, r := range pg.refs() {
		if r.kind == kind && r.id == id {
			e := g.m.top()
			e.sel, e.lead, e.follow = i, 0, false
			if at := paneOfRow(pg.panes(), i); at >= 0 {
				p := pg.panes()[at]
				e.paneFocus, e.textFocus = p.title, false
				e.rememberPane(pg, p, pg.refs())
			}
			g.m.noteCursor()
			g.view()
			return g
		}
	}
	g.t.Fatalf("no %s row %s on the screen:\n%s", kind, id, g.view())
	return g
}

// refsOfKind are the ids of the rows of the page with that kind, in order.
func (g *rig) refsOfKind(kind string) []string {
	var out []string
	for _, r := range g.m.page().refs() {
		if r.kind == kind {
			out = append(out, r.id)
		}
	}
	return out
}

// TestARoundDetailListsEveryFindingItRaisedAsRows: a round's detail has a row for
// each finding it raised, whichever round is the latest: round 1's four are rows
// while round 3 was clean, and the Detail is a pane the focus reaches.
func TestARoundDetailListsEveryFindingItRaisedAsRows(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 40, 0)
	g.tabTo("Rounds")
	if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "3" {
		t.Fatalf("the cursor is on %+v, want round 3", ref)
	}
	if got := g.refsOfKind(rowFinding); len(got) != 0 {
		t.Errorf("round 3 was clean and its detail has finding rows %v", got)
	}
	g.press("down", "down")
	if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "1" {
		t.Fatalf("two downs put the cursor on %+v, want round 1", ref)
	}
	view := g.view()
	if got := strings.Join(g.refsOfKind(rowFinding), " "); got != "r1-f1 r1-f2 r1-f3 r1-f4" {
		t.Errorf("finding rows = %q, want round 1's four in order:\n%s", got, view)
	}
	detail := readWords(between(view, "╭─ Detail · round 1", "╰"))
	for _, want := range []string{
		"high alpha/a.go:12 Close the file → to fix",
		"medium alpha/b.go:4 Rename the helper → noted",
		"low alpha/b.go:9 Trim the comment → noted",
		"low alpha/c.go:2 Order the imports → noted",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail of round 1 lacks %q:\n%s", want, view)
		}
	}
	g.tabTo("Detail · round 1")
	if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f1" {
		t.Errorf("tab into the Detail puts the cursor on %+v, want the first finding", ref)
	}
	g.press("down", "down")
	if ref := g.m.selected(); ref == nil || ref.id != "r1-f2" {
		t.Errorf("two downs in the Detail put the cursor on %+v, want r1-f2, the row after the fix slice", ref)
	}
	if !strings.Contains(between(g.view(), "╭─ Detail · round 1", "╰"), "▸") {
		t.Errorf("the cursor is not drawn in the Detail:\n%s", g.view())
	}
	g.tabTo("Rounds")
	if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "1" {
		t.Errorf("tab back to Rounds puts the cursor on %+v, want round 1 where it was left", ref)
	}
}

// TestAFindingRowSaysWhatBecameOfItAndItsFixSlicesState: under a finding that
// became a fix slice the detail names the slice and its state, as a row of its
// own that opens the slice.
func TestAFindingRowSaysWhatBecameOfItAndItsFixSlicesState(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 40, 0)
	g.cursorOn(rowRound, "1")
	detail := readWords(between(g.view(), "╭─ Detail · round 1", "╰"))
	if !strings.Contains(detail, "fix slice fix-1-alpha · green") {
		t.Errorf("the fix finding does not name its slice and its state:\n%s", detail)
	}
	if got := strings.Join(g.refsOfKind(rowSlice), " "); !strings.Contains(got, "fix-1-alpha") {
		t.Errorf("the fix slice is not a row: %q", got)
	}
	g.cursorOn(rowRound, "2")
	detail = readWords(between(g.view(), "╭─ Detail · round 2", "╰"))
	if !strings.Contains(detail, "medium alpha/c.go:7 Check the returned error → to fix") || !strings.Contains(detail, "fix slice fix-2-alpha · green") {
		t.Errorf("round 2's detail does not pair its finding and slice:\n%s", detail)
	}
}

// TestEnterOnAFindingOpensItWholeAndEscComesBack: enter on a finding row opens
// the finding's screen with every word of it, at the floor and at the common
// size, and esc comes back to the round with the cursor on the finding's row.
func TestEnterOnAFindingOpensItWholeAndEscComesBack(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {100, 30}} {
		g := threeRounds(t, size[0], size[1], 0)
		g.cursorOn(rowRound, "1").tabTo("Detail · round 1")
		g.press("enter")
		if g.m.Screen() != "finding" {
			t.Fatalf("%dx%d: enter opened %s, want the finding screen", size[0], size[1], g.m.Screen())
		}
		head := strings.Split(g.view(), "\n")[0]
		wants := []string{"JIG-2", "round 1", "r1-f1", "1 of 4"}
		if size[0] > 60 {
			wants = append(wants, "3 review") // the review step gives way at the floor
		}
		for _, want := range wants {
			if !strings.Contains(head, want) {
				t.Errorf("%dx%d: the breadcrumb %q lacks %q", size[0], size[1], head, want)
			}
		}
		// Read the screen from its top to its end: every word of the finding is on
		// it somewhere, none cut.
		seen := readWords(g.view())
		for i := 0; i < 8; i++ {
			g.send(tea.KeyMsg{Type: tea.KeyPgDown})
			seen += " " + readWords(g.view())
		}
		for _, want := range []string{
			"alpha/a.go:12", "Close the file", "high", "a leaked handle fails the next open", "fix", "The file is opened and never closed.", "Close it on every path.",
			"to fix", "the gate", "fix-1-alpha", "green",
		} {
			if !strings.Contains(seen, want) {
				t.Errorf("%dx%d: the finding screen lacks %q:\n%s", size[0], size[1], want, seen)
			}
		}
		g.press("esc")
		if g.m.Screen() != "review" {
			t.Fatalf("%dx%d: esc went to %s, want the review", size[0], size[1], g.m.Screen())
		}
		if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f1" {
			t.Errorf("%dx%d: esc leaves the cursor on %+v, want the finding's row", size[0], size[1], ref)
		}
	}
}

// TestEnterOnAFindingsFixSliceOpensItAndEscReturnsToTheFinding: the fix slice a
// finding became is a row on its screen; enter opens the slice and esc returns.
func TestEnterOnAFindingsFixSliceOpensItAndEscReturnsToTheFinding(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 30, 0)
	g.cursorOn(rowRound, "1").cursorOn(rowFinding, "r1-f1")
	g.press("enter")
	if g.m.Screen() != "finding" {
		t.Fatalf("on %s, want the finding", g.m.Screen())
	}
	g.press("tab")
	g.tabTo("Fix slices")
	if ref := g.m.selected(); ref == nil || ref.kind != rowSlice || ref.id != "fix-1-alpha" {
		t.Fatalf("the cursor is on %+v, want the fix slice", ref)
	}
	g.press("enter")
	if g.m.Screen() != "slice" || g.m.top().slice != "fix-1-alpha" {
		t.Fatalf("enter opened %s %q, want the slice fix-1-alpha", g.m.Screen(), g.m.top().slice)
	}
	g.press("esc")
	if g.m.Screen() != "finding" || g.m.top().finding != "r1-f1" {
		t.Errorf("esc went to %s %q, want the finding r1-f1", g.m.Screen(), g.m.top().finding)
	}
	g.press("esc")
	if g.m.Screen() != "review" {
		t.Errorf("a second esc went to %s, want the review", g.m.Screen())
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f1" {
		t.Errorf("the review keeps its cursor on %+v, want the finding's row", ref)
	}
}

// TestBracketsStepBetweenAFindingsSiblingsAndBetweenRounds: on a finding's screen
// [ and ] step to the round's previous and next finding, with the place in the
// breadcrumb and a reason on the first and the last; on the review screen they
// step the Rounds cursor.
func TestBracketsStepBetweenAFindingsSiblingsAndBetweenRounds(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 30, 0)
	g.press("[")
	if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "2" {
		t.Fatalf("[ from round 3 puts the cursor on %+v, want round 2", ref)
	}
	if head := strings.Split(g.view(), "\n")[0]; !strings.Contains(head, "3 review") || !strings.Contains(head, "2 of 3") {
		t.Errorf("the breadcrumb does not say the place among the rounds: %q", head)
	}
	g.press("[")
	g.press("[")
	if ref := g.m.selected(); ref == nil || ref.id != "1" || !strings.Contains(g.m.notice, "first round") {
		t.Errorf("[ at the first round: cursor %+v, notice %q, want round 1 and the reason", ref, g.m.notice)
	}
	g.press("]", "]", "]")
	if ref := g.m.selected(); ref == nil || ref.id != "3" || !strings.Contains(g.m.notice, "last round") {
		t.Errorf("] at the last round: cursor %+v, notice %q, want round 3 and the reason", ref, g.m.notice)
	}

	g.cursorOn(rowRound, "1").cursorOn(rowFinding, "r1-f1")
	g.press("enter")
	g.press("]")
	if g.m.top().finding != "r1-f2" {
		t.Fatalf("] opened %q, want r1-f2", g.m.top().finding)
	}
	if head := strings.Split(g.view(), "\n")[0]; !strings.Contains(head, "r1-f2") || !strings.Contains(head, "2 of 4") {
		t.Errorf("the breadcrumb does not say the place: %q", head)
	}
	g.press("[", "[")
	if g.m.top().finding != "r1-f1" || !strings.Contains(g.m.notice, "first finding") {
		t.Errorf("[ at the first finding: on %q, notice %q", g.m.top().finding, g.m.notice)
	}
	g.press("esc")
	if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f1" {
		t.Errorf("esc leaves the cursor on %+v, want the finding the screen was opened from", ref)
	}
}

// TestACarriedRoundNamesTheRoundItCarriesAndEnterMovesThere: a clean round that
// carries an earlier round says so in a row of its detail, and enter on it moves
// the Rounds cursor to that round without opening a screen.
func TestACarriedRoundNamesTheRoundItCarriesAndEnterMovesThere(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 30, 2)
	g.cursorOn(rowRound, "3")
	if !strings.Contains(readWords(between(g.view(), "╭─ Detail · round 3", "╰")), "carried over from round 2") {
		t.Fatalf("round 3's detail does not name the round it carries:\n%s", g.view())
	}
	g.tabTo("Detail · round 3")
	if ref := g.m.selected(); ref == nil || ref.kind != rowCarry || ref.id != "2" {
		t.Fatalf("the cursor is on %+v, want the carried row", ref)
	}
	depth := len(g.m.nav)
	g.press("enter")
	if len(g.m.nav) != depth {
		t.Errorf("enter on the carried row pushed a screen")
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "2" {
		t.Errorf("enter puts the cursor on %+v, want round 2", ref)
	}
	if !strings.Contains(g.view(), "╭─ Detail · round 2") {
		t.Errorf("the detail is not round 2's:\n%s", g.view())
	}
}

// TestTheFindingScreenIsDrawnAsTheGoldenFrame: the finding screen of a fix
// finding that became a green slice, at the common size.
func TestTheFindingScreenIsDrawnAsTheGoldenFrame(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 30, 0)
	g.cursorOn(rowRound, "1").cursorOn(rowFinding, "r1-f1")
	g.press("enter")
	golden(t, "finding", g.view())
}

// TestAFindingWaitingForAPersonSaysSoAndDecidesNothing: the screen of an ask
// names the Asks list, and the keys that decide an ask run nothing there.
func TestAFindingWaitingForAPersonSaysSoAndDecidesNothing(t *testing.T) {
	t.Parallel()
	g := reviewFixRound(t, 100, 40)
	g.cursorOn(rowRound, "2").cursorOn(rowFinding, "r2-f2")
	g.press("enter")
	if g.m.Screen() != "finding" {
		t.Fatalf("on %s, want the finding", g.m.Screen())
	}
	view := readWords(g.view())
	if !strings.Contains(view, "waits for a person") || !strings.Contains(view, "Asks list") {
		t.Errorf("the finding that waits does not say so:\n%s", g.view())
	}
	before := len(g.r.lines())
	g.press("k", "d")
	if after := g.r.lines(); len(after) != before {
		t.Errorf("a key on the finding's screen ran %v", after[before:])
	}
}
