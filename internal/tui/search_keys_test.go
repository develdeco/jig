package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// drawn presses a key and draws the frame after it, as bubbletea does, and
// says the row the frame marks.
func drawn(m *Model, msg tea.KeyMsg) string {
	_, _ = m.Update(msg)
	frame := stripANSI(m.View())
	title := ""
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "  ╭─ ") {
			t, _, _ := strings.Cut(strings.TrimPrefix(l, "  ╭─ "), " ─")
			title = strings.ToLower(t)
		}
		if l = strings.TrimPrefix(l, "  │"); strings.HasPrefix(l, "▸ ") {
			if strings.Contains(l, "enter shows them") || strings.Contains(l, "enter folds them") {
				// A fold's row is named by its panel.
				return title
			}
			if f := strings.Fields(l); len(f) > 2 {
				return f[2]
			}
		}
	}
	return ""
}

func typedDrawn(m *Model, s string) {
	for _, r := range s {
		_ = drawn(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// TestSearchFramesMarkTheRowKeysActOn: after esc, and after a backspace that
// widens the list, the frame marks the row the cursor is on, which enter opens.
func TestSearchFramesMarkTheRowKeysActOn(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	m.View()
	typedDrawn(m, "/for")
	if got := underCursor(m); got != "WAI-3" {
		t.Fatalf("the cursor is on %q, want WAI-3", got)
	}
	if got := drawn(m, tea.KeyMsg{Type: tea.KeyEsc}); got != "REL-11" || underCursor(m) != "REL-11" {
		t.Errorf("after esc the frame marks %q and keys act on %q, want REL-11 both", got, underCursor(m))
	}

	m = searchStore(t)
	m.View()
	typedDrawn(m, "/task iss dr")
	if underCursor(m) != "TSK-2" {
		t.Fatalf("the cursor is on %q, want TSK-2", underCursor(m))
	}
	_ = drawn(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := drawn(m, tea.KeyMsg{Type: tea.KeyBackspace}); got != underCursor(m) || got != "TSK-1" {
		t.Errorf("after a widening backspace the frame marks %q, keys act on %q, want TSK-1", got, underCursor(m))
	}
}

// TestSearchOpenLineKeysAsATerminalSendsThem: a space is KeySpace, up moves
// up, home then a query puts the first match under the cursor, and / waits for
// the store to be read.
func TestSearchOpenLineKeysAsATerminalSendsThem(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	typed(m, "/to")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	typed(m, "land")
	if got := string(m.nav[0].search.query); got != "to land" {
		t.Errorf("the line holds %q, want a KeySpace in it", got)
	}
	if got := strings.Join(shown(m), " "); got != "REL-11" {
		t.Errorf("/to land draws %q", got)
	}
	press(m, tea.KeyEsc)

	typed(m, "/t")
	rows := shown(m)
	press(m, tea.KeyDown)
	press(m, tea.KeyDown)
	press(m, tea.KeyUp)
	if got := underCursor(m); got != rows[1] {
		t.Errorf("up after two downs is on %q, want %q", got, rows[1])
	}
	press(m, tea.KeyEsc)

	// Home holds the page at its top; a query whose first match is further down
	// still has that row under the cursor.
	b := searchBig(t, 1000)
	b.View()
	press(b, tea.KeyHome)
	typed(b, "/t-09")
	typed(b, "9")
	want := underCursor(b)
	if want == "" || !strings.Contains(stripANSI(b.View()), want) {
		t.Errorf("after home and a query the row %q is not in the frame", want)
	}
	if sel := b.selected(); sel == nil || sel.id != want {
		t.Errorf("keys act on %+v, want %q", sel, want)
	}

	// Not while the store is being read.
	r := New(quietConfig())
	typed(r, "/")
	if r.nav[0].search.applied() {
		t.Errorf("/ opened the line while the store was read")
	}
	if strings.Contains(stripANSI(strings.Join(r.footer(100), "\n")), "search") {
		t.Errorf("the hint lists / while the store is read")
	}
}
