package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// searchHint is what the search line says while it is open.
const searchHint = "id, number, title, chart, state or PR · ↑↓ move · enter open · esc clears"

// searchState is the search of the main screen, on its entry like the view by
// chart: the query, whether the line that edits it is open, and the row the
// cursor stood on when the search began, which esc puts it back on. A run does
// not keep it.
type searchState struct {
	open    bool
	query   []rune
	from    rowIdentity
	fromSet bool
}

// applied says the list is narrowed: the line is open, or a query is set.
func (s searchState) applied() bool { return s.open || len(s.query) > 0 }

// searchCache is the search's own cache beside the project's list it was built
// from: the words of every row, built on first need, and the lines of the rows
// drawn, so a key filters what is built and builds neither the queue nor a
// row's detail again.
type searchCache struct {
	words map[string][]string
	own   map[string]map[string]bool
	total int
	// chartsOf are the charts that list a ticket, issuesOf the issues whose
	// chart lists it, planOf the chart that plans an issue.
	chartsOf, issuesOf map[string][]string
	planOf             map[string]string

	// query and res are the last query's result.
	query string
	res   *searchResult
}

// searchResult is a query's result over the queue: the page drawn, its rows,
// the first row that matches by itself, and the counts the header says.
type searchResult struct {
	pg      page
	refs    []rowRef
	first   int
	matched int
	total   int
}

// searchApplied says main is narrowed by a search.
func (m *Model) searchApplied() bool { return m.nav[0].search.applied() }

// searchOf is the result of the search as it stands, over the list built.
func (m *Model) searchOf(c *mainCache) *searchResult {
	if c.srch == nil {
		c.srch = m.newSearchCache(c.q)
	}
	sc := c.srch
	query := string(m.nav[0].search.query)
	if sc.res == nil || sc.query != query {
		sc.query, sc.res = query, m.runSearch(c, sc, query)
	}
	return sc.res
}

// newSearchCache is the cache of a queue: the charts' lists read once, and the
// number of tickets and charts the roster holds.
func (m *Model) newSearchCache(q mainQueue) *searchCache {
	sc := &searchCache{
		words:    map[string][]string{},
		own:      map[string]map[string]bool{},
		chartsOf: map[string][]string{},
		issuesOf: map[string][]string{},
		planOf:   map[string]string{},
	}
	for _, name := range m.w.chartOrder {
		c := m.w.charts[name]
		if c == nil {
			continue
		}
		for _, id := range c.ids() {
			sc.chartsOf[id] = append(sc.chartsOf[id], name)
		}
		if issue := c.listing.Issue; issue != "" {
			if _, ok := sc.planOf[issue]; !ok {
				sc.planOf[issue] = name
			}
			for _, id := range c.listing.Tickets {
				sc.issuesOf[id] = append(sc.issuesOf[id], issue)
			}
		}
	}
	seen := map[string]bool{}
	var walk func(rows []queueRow)
	walk = func(rows []queueRow) {
		for _, r := range rows {
			seen[kindKey(r)] = true
			walk(r.kids)
		}
	}
	walk(q.rows)
	sc.total = len(seen)
	return sc
}

// wordsOf are the words of a row, built once for the queue.
func (sc *searchCache) wordsOf(m *Model, r queueRow) []string {
	key := kindKey(r)
	if w, ok := sc.words[key]; ok {
		return w
	}
	w := rowWords(m.rowFactsOf(sc, r))
	sc.words[key] = w
	return w
}

// rowFactsOf is what a row of the queue says of itself.
func (m *Model) rowFactsOf(sc *searchCache, r queueRow) rowFacts {
	f := rowFacts{id: r.id, chart: r.chart, group: r.group.name(), charts: sc.chartsOf[r.id]}
	if r.chart {
		if c := m.w.charts[r.id]; c != nil {
			f.title = c.title()
		}
		return f
	}
	t := r.t
	f.title, f.issues, f.plan = t.title, sc.issuesOf[r.id], sc.planOf[r.id]
	if d := t.digest; d != nil {
		f.kind, f.parent = d.Kind, d.Parent
		for _, pr := range d.PRs {
			f.prs = append(f.prs, pr.Number)
		}
	}
	if isIssue(t) || isMulti(t) {
		// A container's end is its own landing, the state its row draws.
		switch r.cur.State {
		case StateWaitsToLand:
			f.end = "waits to land"
		case StateLanded:
			f.end = "landed"
		case StateClosed:
			f.end = "closed"
		}
		return f
	}
	if end, ok := m.endOf(t); ok {
		f.end = end.Word
	}
	return f
}

// cellsOf are the row's cells as main built them; a row main did not build (one
// in a fold) is built once and kept with the rest.
func (c *mainCache) cellsOf(m *Model, r queueRow) []string {
	id := r.ref().identity()
	if cells, ok := c.cells[id]; ok {
		return cells
	}
	cells := m.queueCells(r)
	c.cells[id] = cells
	return cells
}

// ownOf are the row's words as a set: the words the letters lit may be of.
func (sc *searchCache) ownOf(m *Model, r queueRow) map[string]bool {
	key := kindKey(r)
	if own, ok := sc.own[key]; ok {
		return own
	}
	own := map[string]bool{}
	for _, w := range sc.wordsOf(m, r) {
		own[w] = true
	}
	sc.own[key] = own
	return own
}

// runSearch narrows the queue to what the query matches and draws it.
func (m *Model) runSearch(c *mainCache, sc *searchCache, query string) *searchResult {
	terms := searchTerms(query)
	run := newSearchRun(func(r queueRow) bool { return matches(terms, sc.wordsOf(m, r)) })
	q := c.q
	if q.byChart {
		q.sections = run.keep(c.q.sections)
	}
	for g := range q.byGroup {
		q.byGroup[g] = run.keep(c.q.byGroup[g])
	}
	p := &mainDraft{cells: map[int][]string{}, chart: map[int]bool{}}
	addRow := func(r queueRow) {
		at := len(p.lines)
		p.addRow(r.ref(), "")
		own := sc.ownOf(m, r)
		cells := make([]string, len(c.cellsOf(m, r)))
		for i, cell := range c.cellsOf(m, r) {
			cells[i] = m.st.lit(cell, terms, own)
		}
		p.cells[at], p.chart[at] = cells, r.chart
	}
	rows := func(rows []queueRow) {
		var add func(rows []queueRow)
		add = func(rows []queueRow) {
			for _, r := range rows {
				addRow(r)
				add(r.kids)
			}
		}
		add(rows)
	}
	if q.byChart {
		for _, s := range q.sections {
			if len(p.lines) > 0 {
				p.add("")
			}
			addRow(s)
			rows(s.kids)
		}
		if n := q.rowsIn(); n > 0 {
			if len(p.lines) > 0 {
				p.add("")
			}
			p.add(m.st.dim("no chart (" + strconv.Itoa(n) + ")"))
			for g := mainGroup(0); g < groupCount; g++ {
				rows(q.byGroup[g])
			}
		}
	} else {
		for g := mainGroup(0); g < groupCount; g++ {
			if len(q.byGroup[g]) == 0 {
				continue
			}
			if len(p.lines) > 0 {
				p.add("")
			}
			p.add(m.st.dim(g.heading(len(q.byGroup[g]))))
			rows(q.byGroup[g])
		}
	}
	if len(p.lines) == 0 {
		p.add(m.st.dim("nothing matches"))
	}
	// The rows are laid out by the allocator, as main's are; the letters lit are
	// style over the cells' text, so they move no column.
	pg := m.layMain(p)
	res := &searchResult{pg: pg, refs: pg.refs(), first: -1, matched: len(run.tickets), total: sc.total}
	for i, ref := range res.refs {
		if run.self[ref.identity()] {
			res.first = i
			break
		}
	}
	return res
}

// searchSummary is the header's words while main is narrowed.
func (m *Model) searchSummary() string {
	res := m.searchOf(m.mainBuilt())
	query := strings.TrimSpace(string(m.nav[0].search.query))
	if len(searchTerms(query)) == 0 {
		return "search · " + strconv.Itoa(res.total) + " rows"
	}
	return "search “" + query + "” · " + strconv.Itoa(res.matched) + " of " + strconv.Itoa(res.total) + " · esc clears"
}

// beginSearch opens the search line on main, noting the row the cursor stands
// on so esc can put it back.
func (m *Model) beginSearch() {
	e := &m.nav[0]
	if m.top().screen != screenMain || m.readingStore() {
		return
	}
	if !e.search.applied() {
		m.followSel()
		m.noteCursor()
		e.search.from, e.search.fromSet = e.cur, e.curSet
	}
	e.search.open = true
}

// clearSearch ends the search and puts the cursor back on the row it began on.
func (m *Model) clearSearch() {
	e := &m.nav[0]
	from, set := e.search.from, e.search.fromSet
	e.search = searchState{}
	// The cursor moved here is the reader's own: the next frame does not settle
	// it back onto the row the last frame held.
	m.seen = nil
	if set {
		e.cur, e.curSet = from, true
		refs := m.mainRefs()
		if i, ok := m.findRow(refs, from); ok {
			e.sel, e.cur = i, refs[i].identity()
		}
	}
}

// searchCursor puts the cursor on the first row that matches by itself.
func (m *Model) searchCursor() {
	res := m.searchOf(m.mainBuilt())
	if len(res.refs) == 0 {
		return
	}
	e := &m.nav[0]
	m.seen = nil
	i := max(res.first, 0)
	e.sel, e.cur, e.curSet, e.lead = i, res.refs[i].identity(), true, 0
}

// searchKey takes a key while the search line is open: every printable key is
// text, the moves move over the rows drawn, enter opens one and esc clears.
func (m *Model) searchKey(k tea.KeyMsg) tea.Cmd {
	e := &m.nav[0]
	// The keys are told by their type: letters typed together arrive as one key
	// whose text may be a key's name ("up").
	switch k.Type {
	case tea.KeyEsc:
		m.clearSearch()
	case tea.KeyEnter:
		if m.selected() == nil {
			return nil
		}
		e.search.open = false
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
		if n := len(e.search.query); n > 0 {
			e.search.query = e.search.query[:n-1]
			m.searchCursor()
		}
	case tea.KeyCtrlU:
		if len(e.search.query) > 0 {
			e.search.query = nil
			m.searchCursor()
		}
	case tea.KeyRunes, tea.KeySpace:
		if k.Alt {
			return nil
		}
		for _, r := range k.Runes {
			if r == '\n' || r == '\r' || r == '\t' {
				r = ' '
			}
			e.search.query = append(e.search.query, r)
		}
		m.searchCursor()
	}
	return nil
}

// searchFooter is the search line and its hint, below the rule.
func (m *Model) searchFooter(w int) []string {
	out := []string{fit("  "+m.st.key("/")+" "+string(m.nav[0].search.query)+m.st.dim("▏"), w)}
	for _, l := range wrapWhole(searchHint, w-2) {
		out = append(out, fit("  "+m.st.dim(l), w))
	}
	return out
}
