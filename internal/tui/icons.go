package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The states the screens draw. A slice is in one of the first seven (the
// store's own slice states plus verifying, which the journal shows before the
// store does); a ticket is in a slice state while it is being built, then in
// the phases that follow; an ask and a command have the last states.
const (
	StateQueued     = "queued"
	StateBuilding   = "building"
	StateVerifying  = "verifying"
	StateGreen      = "green"
	StateNeedsInput = "needs-input"
	StateStalled    = "stalled"
	StateEnvBlocked = "env-blocked"

	StateReviewing  = "reviewing"
	StateFindings   = "findings"
	StateReady      = "ready"
	StatePublishing = "publishing"
	StatePublished  = "published"

	StateAsk       = "ask"
	StateKept      = "kept"
	StateDismissed = "dismissed"

	StateOK   = "ok"
	StateFail = "fail"
)

// Look is how a state is drawn: one single-width glyph and a color (an ANSI
// 256 index). Every state has its own icon, so a screen read without color
// (a pipe, NO_COLOR) tells states apart as well as one read with it.
type Look struct {
	Icon  string
	Color string
}

// lookOf is the look of state; a state the screens do not know is drawn as a
// dim dot.
func lookOf(state string) Look {
	switch state {
	case StateQueued:
		return Look{"○", "8"}
	case StateBuilding:
		return Look{"◐", "3"}
	case StateVerifying:
		return Look{"◑", "6"}
	case StateGreen:
		return Look{"●", "2"}
	case StateNeedsInput:
		return Look{"?", "5"}
	case StateStalled:
		return Look{"✖", "1"}
	case StateEnvBlocked:
		return Look{"⊘", "1"}
	case StateReviewing:
		return Look{"◎", "4"}
	case StateFindings:
		return Look{"!", "3"}
	case StateReady:
		return Look{"◆", "2"}
	case StatePublishing:
		return Look{"▲", "6"}
	case StatePublished:
		return Look{"✔", "2"}
	case StateAsk:
		return Look{"!", "3"}
	case StateKept:
		return Look{"+", "2"}
	case StateDismissed:
		return Look{"-", "8"}
	case StateOK:
		return Look{"✔", "2"}
	case StateFail:
		return Look{"✖", "1"}
	}
	return Look{"·", "8"}
}

// styles paints text for a renderer: with a color profile the renderer found
// on its output, or none at all when the output is a pipe or a file, so the
// same code draws a colored screen on a terminal and plain text anywhere else.
type styles struct {
	r *lipgloss.Renderer
}

func (s styles) style() lipgloss.Style { return s.r.NewStyle() }

// icon is state's glyph in its color.
func (s styles) icon(state string) string {
	l := lookOf(state)
	return s.style().Foreground(lipgloss.Color(l.Color)).Render(l.Icon)
}

// state is text in state's color.
func (s styles) state(state, text string) string {
	if text == "" {
		return ""
	}
	return s.style().Foreground(lipgloss.Color(lookOf(state).Color)).Render(text)
}

// dim is text drawn back: ids of lines, times, hints.
func (s styles) dim(text string) string {
	if text == "" {
		return ""
	}
	return s.style().Foreground(lipgloss.Color("8")).Render(text)
}

// bold is text drawn forward: titles and the selected row.
func (s styles) bold(text string) string {
	if text == "" {
		return ""
	}
	return s.style().Bold(true).Render(text)
}

// key is a key name in a hint line.
func (s styles) key(text string) string {
	return s.style().Foreground(lipgloss.Color("6")).Render(text)
}

// fit cuts text to at most w cells, ending in an ellipsis when it cut, and
// never splits an escape sequence. Text with no room left is empty.
func fit(text string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(text, w, "…")
}

// pad fills text with spaces to w cells; text wider than w is cut.
func pad(text string, w int) string {
	text = fit(text, w)
	if gap := w - ansi.StringWidth(text); gap > 0 {
		text += spaces(gap)
	}
	return text
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// width is the cells text takes.
func width(text string) int { return ansi.StringWidth(text) }
