package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// longRows are tickets whose ids and titles are as long as the screens meet:
// a 24-cell id and a 60-cell title made of words that name their ticket.
func longRows() (ids []string, titles map[string]string, rows []rosterRow) {
	titles = map[string]string{}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("JIG-%020d", i)
		title := fmt.Sprintf("Reconcile%d invoices%d against%d ledger%d entries%d nightly%d", i, i, i, i, i, i)
		title += strings.Repeat("x", 60-width(title))
		ids = append(ids, id)
		titles[id] = title
		rows = append(rows, rosterRow{ID: id, Title: title,
			Digest: task("build", false, "done|approved", "current|1 of 2 slices green", "pending|after the build", "pending|after a clean review", "pending|after the publish")})
	}
	return ids, titles, rows
}

// rowBlock is the lines of a row on a screen: the one that names id, and the
// continuation rows after it, up to the next row, the next heading or a blank.
func rowBlock(lines []string, id string) []string {
	for i, l := range lines {
		if !strings.Contains(l, id) {
			continue
		}
		block := []string{l}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" || strings.Contains(next, "JIG-") || strings.HasPrefix(strings.TrimSpace(next), "charts") {
				break
			}
			block = append(block, next)
		}
		return block
	}
	return nil
}

// TestMainLaidOutAtThreeSizes: main draws every id whole and every title whole
// (its words in order across the row and its continuation rows), or ending in
// an ellipsis only once it goes past three rows, and no row is wider than the
// terminal, in a narrow, a standard and a wide one.
func TestMainLaidOutAtThreeSizes(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {230, 50}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := newRig(t, "")
			ids, titles, rows := longRows()
			g.m.w.setRoster(rows)
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			view := g.view()
			lines := strings.Split(view, "\n")
			for _, l := range lines {
				if width(l) > w {
					t.Errorf("a row is %d cells in a %d wide terminal: %q", width(l), w, l)
				}
			}
			for _, id := range ids {
				block := rowBlock(lines, id)
				if block == nil {
					t.Errorf("%s is not drawn whole:\n%s", id, view)
					continue
				}
				text := strings.Join(block, "\n")
				pos, whole := 0, true
				for _, word := range strings.Fields(titles[id]) {
					i := strings.Index(text[pos:], word)
					if i < 0 {
						whole = false
						break
					}
					pos += i + len(word)
				}
				if !whole && (!strings.Contains(text, "…") || len(block) < 3) {
					t.Errorf("%s's title is neither whole nor ends in an ellipsis past three rows:\n%s", id, strings.Join(block, "\n"))
				}
				if w >= 230 && (!whole || len(block) != 1) {
					t.Errorf("at %d wide %s's title is not on one row:\n%s", w, id, strings.Join(block, "\n"))
				}
			}
		})
	}
}
