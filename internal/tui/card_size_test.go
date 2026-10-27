package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
)

// TestAGoalOfSeveralLinesReadsAsSentences: a fix slice's goal is a heading,
// then a finding's title and its reason on lines of their own; the card shows
// them as one paragraph, and a title that ends with no stop gets one, so it
// does not run into the reason.
func TestAGoalOfSeveralLinesReadsAsSentences(t *testing.T) {
	t.Parallel()
	goal := "Fix these gate findings:\n\ncart/cart_test.go:9 No test pins the rounding\nThe repro case cannot tell rounding from truncation.\nRisk (medium): money"
	want := "cart/cart_test.go:9 No test pins the rounding. The repro case cannot tell rounding from truncation. Risk (medium): money"
	if got := goalText(goal); got != want {
		t.Errorf("goalText = %q, want %q", got, want)
	}
	if got := goalLine(goal); got != "cart/cart_test.go:9 No test pins the rounding" {
		t.Errorf("goalLine = %q, want the finding's title alone", got)
	}
}

// TestATallTerminalGivesTheCardMoreMoves: the card lists eight moves on the
// 80 by 24 screen and more on a taller one, as the sub-steps block and the
// activity do, so a tall pane is not left empty below a card cut at eight.
func TestATallTerminalGivesTheCardMoreMoves(t *testing.T) {
	t.Parallel()
	moves := func(height int) int {
		g := cardRig(t, 100, height)
		var calls []events.Envelope
		for i := 0; i < 20; i++ {
			calls = append(calls, tool(at(2, 11+i), "s-b", fmt.Sprintf("go test ./cart/... -run T%d", i), said(fmt.Sprintf("Run case %d", i))))
		}
		g.feed(calls...)
		block := panelLines(strings.Split(g.view(), "\n"), "Build ─")
		n := 0
		for _, line := range block {
			if strings.Contains(line, "· Run case") || strings.Contains(line, "· $ `") || strings.Contains(line, "✗ ") || strings.Contains(line, "◑ verifying") {
				n++
			}
		}
		return n
	}
	if n := moves(24); n > cardMoves {
		t.Errorf("at 80 by 24 the card lists %d moves, want at most %d", n, cardMoves)
	}
	if short, tall := moves(30), moves(50); tall <= cardMoves || tall <= short {
		t.Errorf("a 50-row terminal lists %d moves and a 30-row one %d, want more than %d and more than the shorter one", tall, short, cardMoves)
	}
}
