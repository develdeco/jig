package tui

import (
	"testing"
	"unicode/utf8"
)

// TestEveryIconIsATextGlyph: a terminal that draws a symbol as an emoji
// spends two cells on it while the screens count one, which pushes the rest
// of the row over. The looks may use only the glyphs written here, each of
// them text-presentation and one cell wide under the TUI's own count.
func TestEveryIconIsATextGlyph(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		"○": true, "◐": true, "●": true, "?": true, "✗": true, "⊘": true,
		"◎": true, "!": true, "◆": true, "▲": true, "✓": true, "◇": true,
		"⊖": true, "+": true, "-": true, "·": true,
	}
	states := []string{
		StateQueued, StateBuilding, StateGreen, StateNeedsInput, StateStalled, StateEnvBlocked,
		StateReviewing, StateFindings, StateReady, StatePublishing, StatePublished,
		StateOnLand, StateWaitsToLand, StateLanded, StateClosed,
		StateAsk, StateKept, StateDismissed, StateOK, StateFail, "no such state",
	}
	for _, s := range states {
		icon := lookOf(s).Icon
		if utf8.RuneCountInString(icon) != 1 || width(icon) != 1 {
			t.Errorf("the icon of %q is %q, want one rune that takes one cell", s, icon)
		}
		if !allowed[icon] {
			t.Errorf("the icon of %q is %q (U+%04X), which is not in the list of text glyphs the looks may use", s, icon, []rune(icon)[0])
		}
	}
}
