package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// `/` narrows the focused pane as it narrows the project's list: the query is
// the one matcher of search.go (every term starts a word of the row), the pane
// keeps the rows it matches and counts them in its title, up and down move over
// those, enter opens one as the pane does and esc clears the query and puts the
// cursor back on the row it stood on. The rows are the ones the pane already
// built: the search reads nothing.

// paneSearch is the search of one pane of the screen on its entry: the pane, the
// query, whether the line that edits it is open, and the row the pane's cursor
// stood on when the search began, which esc puts it back on.
type paneSearch struct {
	pane    string
	open    bool
	query   []rune
	from    paneCursor
	fromSet bool
}

// applied says a pane is narrowed: its line is open, or a query is set.
func (s paneSearch) applied() bool { return s.pane != "" && (s.open || len(s.query) > 0) }

// paneTerms are the terms of the query narrowing the pane titled title, none
// when it is not narrowed.
func (m *Model) paneTerms(title string) []string {
	s := m.top().psearch
	if s.pane != title || !s.applied() {
		return nil
	}
	return searchTerms(string(s.query))
}

// plainWords are the words of text as a person reads it: its colors gone.
func plainWords(text string) []string { return wordsOf(ansi.Strip(text)) }

// narrowIdx are the places among the n rows of the pane titled title whose text
// every term of its query starts a word of; every place when the pane is not
// narrowed. The second result says it is.
func (m *Model) narrowIdx(title string, n int, text func(i int) string) ([]int, bool) {
	terms := m.paneTerms(title)
	keep := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if len(terms) == 0 || matches(terms, plainWords(text(i))) {
			keep = append(keep, i)
		}
	}
	return keep, len(terms) > 0
}

// pick are the elements of s at the places keep names.
func pick[T any](s []T, keep []int) []T {
	out := make([]T, 0, len(keep))
	for _, i := range keep {
		out = append(out, s[i])
	}
	return out
}

// narrowPage is pg with the pane that has a query cut down to the rows that
// match: each row with the lines that belong to it (what wrapped onto, and what
// a list draws under it), and the pane's title counting them. A pane that draws
// only a window of its rows was built with the query (narrowIdx) and is left.
func (m *Model) narrowPage(pg page) page {
	s := m.top().psearch
	if !s.applied() || len(s.query) == 0 {
		return pg
	}
	terms := searchTerms(string(s.query))
	if len(terms) == 0 {
		return pg
	}
	ps := pg.panes()
	i := slices.IndexFunc(ps, func(p pane) bool { return p.title == s.pane })
	if i < 0 || ps[i].rows == 0 {
		return pg
	}
	if _, ok := pg.windowed[s.pane]; ok {
		return pg
	}
	p := ps[i]
	// The lines before the pane and after it stay as they were; the pane keeps its
	// top edge, the lines before its first row (the names of the columns), and each
	// row that matches with the lines under it until the next row.
	out := slices.Clone(pg.lines[:p.top+1])
	j := p.top + 1
	for ; j < p.bottom && pg.lines[j].ref == nil; j++ {
		out = append(out, pg.lines[j])
	}
	total, kept := 0, 0
	for j < p.bottom {
		k := j + 1
		for k < p.bottom && pg.lines[k].ref == nil {
			k++
		}
		var texts []string
		for _, l := range pg.lines[j:k] {
			texts = append(texts, l.text)
		}
		total++
		if matches(terms, plainWords(strings.Join(texts, " "))) {
			kept++
			out = append(out, pg.lines[j:k]...)
		}
		j = k
	}
	if kept == 0 {
		out = append(out, pline{text: m.st.dim("no row matches"), box: boxMid})
	}
	out = append(out, pg.lines[p.bottom])
	out[p.top].right = fmt.Sprintf("%d of %d", kept, total)
	pg.lines = append(out, pg.lines[p.bottom+1:]...)
	return pg
}

// beginPaneSearch is `/` on a screen of panes: the focused pane is narrowed, and
// the row its cursor stands on is noted so esc can put it back.
func (m *Model) beginPaneSearch() {
	e := m.top()
	m.syncPanes(m.page())
	if e.paneFocus == "" || e.textFocus {
		return
	}
	if !e.psearch.applied() || e.psearch.pane != e.paneFocus {
		e.psearch = paneSearch{pane: e.paneFocus, from: e.paneCurs[e.paneFocus], fromSet: true}
	}
	e.psearch.open = true
	e.follow = false
}

// clearPaneSearch ends the search and puts the pane's cursor back on the row it
// began on.
func (m *Model) clearPaneSearch() {
	e := m.top()
	s := e.psearch
	e.psearch = paneSearch{}
	m.seen = nil
	if s.pane != "" && s.fromSet {
		if e.paneCurs == nil {
			e.paneCurs = map[string]paneCursor{}
		}
		e.paneCurs[s.pane] = s.from
	}
	e.seenSel, e.seenSet = e.sel, true
}

// paneSearchCursor puts the pane's cursor on its first row that matches.
func (m *Model) paneSearchCursor() {
	e := m.top()
	pg := m.page()
	ps := pg.panes()
	i := slices.IndexFunc(ps, func(p pane) bool { return p.title == e.psearch.pane })
	if i < 0 || ps[i].rows == 0 {
		return
	}
	first := pg.refs()[ps[i].first]
	if full := pg.windowed[ps[i].title]; len(full) > 0 {
		first = full[0]
	}
	if e.paneCurs == nil {
		e.paneCurs = map[string]paneCursor{}
	}
	e.paneCurs[ps[i].title] = paneCursor{id: first.identity(), idx: 0, set: true}
	e.seenSel, e.seenSet = e.sel, true
	e.lead, e.follow = 0, false
	m.seen = nil
}

// paneSearchHint is what the search line says while it is open.
const paneSearchHint = "words of the row · ↑↓ move · enter open · esc clears"

// paneSearchKey takes a key while a pane's search line is open: every printable
// key is text, the moves move over the rows kept, enter opens one and esc clears.
func (m *Model) paneSearchKey(k tea.KeyMsg) tea.Cmd {
	e := m.top()
	s := &e.psearch
	switch k.Type {
	case tea.KeyEsc:
		m.clearPaneSearch()
	case tea.KeyEnter:
		if m.selected() == nil {
			return nil
		}
		s.open = false
		return m.open()
	case tea.KeyUp:
		m.move(-1)
	case tea.KeyDown:
		m.move(1)
	case tea.KeyPgUp:
		m.move(-5)
	case tea.KeyPgDown:
		m.move(5)
	case tea.KeyHome:
		m.move(-1 << 20)
	case tea.KeyEnd:
		m.move(1 << 20)
	case tea.KeyBackspace:
		if n := len(s.query); n > 0 {
			s.query = s.query[:n-1]
			m.paneSearchCursor()
		}
	case tea.KeyCtrlU:
		if len(s.query) > 0 {
			s.query = nil
			m.paneSearchCursor()
		}
	case tea.KeyRunes, tea.KeySpace:
		if k.Alt {
			return nil
		}
		for _, r := range k.Runes {
			if r == '\n' || r == '\r' || r == '\t' {
				r = ' '
			}
			s.query = append(s.query, r)
		}
		m.paneSearchCursor()
	}
	return nil
}

// paneSearchFooter is the search line and its hint, below the rule.
func (m *Model) paneSearchFooter(w int) []string {
	s := m.top().psearch
	out := []string{fit("  "+m.st.key("/")+" "+string(s.query)+m.st.dim("▏")+"  "+m.st.dim("in "+s.pane), w)}
	for _, l := range wrapWhole(paneSearchHint, w-2) {
		out = append(out, fit("  "+m.st.dim(l), w))
	}
	return out
}
