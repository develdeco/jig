package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// longTitle is a 60-cell title made of words that name their ticket.
func longTitle(i int) string {
	title := fmt.Sprintf("Reconcile%d invoices%d against%d ledger%d entries%d nightly%d", i, i, i, i, i, i)
	return title + strings.Repeat("x", 60-width(title))
}

// longRows are tickets whose ids and titles are as long as the screens meet:
// a 24-cell id and a 60-cell title.
func longRows() (ids []string, titles map[string]string, rows []rosterRow) {
	titles = map[string]string{}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("JIG-%020d", i)
		ids = append(ids, id)
		titles[id] = longTitle(i)
		rows = append(rows, rosterRow{ID: id, Title: titles[id],
			Digest: task("build", true, "done|approved", "current|ready to build", "pending|after the build", "pending|after a clean review", "pending|after the publish")})
	}
	return ids, titles, rows
}

// rowBlock is the lines of a row on a screen: the one where id is followed by
// its title, and the continuation rows after it, which have nothing in the
// icon's column; it ends at the next row, a blank line or the edge of a panel.
func rowBlock(lines []string, id string) []string {
	for i, l := range lines {
		at := strings.Index(l, id+"  ")
		if at < 0 {
			continue
		}
		icon := utf8.RuneCountInString(l[:at]) - 2
		block := []string{l}
		for _, next := range lines[i+1:] {
			cells := []rune(next)
			if strings.TrimSpace(next) == "" || strings.ContainsAny(next, "╭╰") || icon >= len(cells) || cells[icon] != ' ' {
				break
			}
			block = append(block, next)
		}
		return block
	}
	return nil
}

// wantRowWhole checks that the screen draws id whole and title whole (its words
// in order across the row and its continuation rows) or ending in an ellipsis
// only once it goes past three rows, and that on a terminal wide enough the
// title is on the row itself.
func wantRowWhole(t *testing.T, lines []string, id, title string, w int) {
	t.Helper()
	block := rowBlock(lines, id)
	if block == nil {
		t.Errorf("%s is not drawn whole:\n%s", id, strings.Join(lines, "\n"))
		return
	}
	text := strings.Join(block, "\n")
	pos, whole := 0, true
	for _, word := range strings.Fields(title) {
		i := strings.Index(text[pos:], word)
		if i < 0 {
			whole = false
			break
		}
		pos += i + len(word)
	}
	if !whole && (!strings.Contains(text, "…") || len(block) < 3) {
		t.Errorf("%s's title is neither whole nor ends in an ellipsis past three rows:\n%s", id, text)
	}
	if w >= 230 && (!whole || len(block) != 1) {
		t.Errorf("at %d wide %s's title is not on one row:\n%s", w, id, text)
	}
}

// wantNoRowWider checks that no line of the screen is wider than the terminal.
func wantNoRowWider(t *testing.T, lines []string, w int) {
	t.Helper()
	for _, l := range lines {
		if width(l) > w {
			t.Errorf("a row is %d cells in a %d wide terminal: %q", width(l), w, l)
		}
	}
}

// layoutSizes are the terminals the layout tests draw: narrow, standard, wide.
var layoutSizes = [][2]int{{60, 20}, {80, 24}, {230, 50}}

// tallSizes are the same widths with a height that shows a screen of several
// panels whole, for the screens that are longer than a terminal's usual height.
// A panel's frame and the age column take five cells more than a loose list did,
// so the narrow terminal is as wide as a long id needs beside them.
var tallSizes = [][2]int{{68, 80}, {80, 70}, {230, 50}}

// TestMainLaidOutAtThreeSizes: main draws every id whole and every title whole
// (its words in order across the row and its continuation rows), or ending in
// an ellipsis only once it goes past three rows, and no row is wider than the
// terminal, in a narrow, a standard and a wide one.
func TestMainLaidOutAtThreeSizes(t *testing.T) {
	t.Parallel()
	for _, size := range tallSizes {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := newRig(t, "")
			ids, titles, rows := longRows()
			g.m.w.setRoster(rows)
			g.m.w.gen++
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			lines := strings.Split(stripANSI(g.view()), "\n")
			wantNoRowWider(t, lines, w)
			for _, id := range ids {
				wantRowWhole(t, lines, id, titles[id], w)
			}
		})
	}
}
