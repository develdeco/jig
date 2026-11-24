package tui

import "time"

// mainCache is the project's list built once: its rows and groups, and the
// page drawn from them. Building them reads every ticket's digest, steps and
// lines, which is the cost of a frame that moves nothing but the cursor, so a
// frame draws the one built for what it reads and nothing else.
type mainCache struct {
	// gen, width, bucket and folds are what the page was built for: the world's
	// count of moves (state.go), the terminal's width, the clock in seconds
	// counted in fives (a session is live for minutes), and which folds are open.
	gen    uint64
	width  int
	bucket int64
	folds  string
	// flip is the first moment a session's sign goes out of its window, which
	// moves a row between groups: until then a step of the clock changes the
	// ages only. Zero when nothing flips.
	flip time.Time

	q      mainQueue
	pg     page
	folded map[rowIdentity]rowIdentity
	refs   []rowRef

	// cells are the cells of the rows the page was built from, by identity.
	cells map[rowIdentity][]string

	// srch is the search over this list, built when a search first asks.
	srch *searchCache
}

// foldsOpen says which folds of the project's list are open, as a key.
func (m *Model) foldsOpen() (map[string]bool, string) {
	open := m.nav[0].open
	key := ""
	if m.nav[0].byChart {
		key = "by chart;"
	}
	for g := mainGroup(0); g < groupCount; g++ {
		if g.folds() && open[g.name()] {
			key += g.name() + ";"
		}
	}
	return open, key
}

// mainRefs are the rows of the project's list the cursor can stand on, read
// from the page built for the world as it stands (no page of a frame's own).
func (m *Model) mainRefs() []rowRef {
	if m.readingStore() {
		return nil
	}
	c := m.mainBuilt()
	if m.searchApplied() {
		m.folded = nil
		return m.searchOf(c).refs
	}
	m.folded = c.folded
	return c.refs
}

// mainBuilt is the project's list for the world as it stands, built again only
// when something it reads moved.
func (m *Model) mainBuilt() *mainCache {
	open, folds := m.foldsOpen()
	now := m.now()
	bucket := now.Unix() / 5
	if c := m.main; c != nil && c.gen == m.w.gen && c.width == m.width && c.folds == folds {
		if c.bucket == bucket {
			return c
		}
		if c.flip.IsZero() || now.Before(c.flip) {
			m.clockStep(c, open, bucket)
			return c
		}
	}
	m.queueBuilds++
	c := &mainCache{gen: m.w.gen, width: m.width, bucket: bucket, folds: folds, flip: m.nextFlip(now)}
	c.q = m.mainRows()
	c.pg, c.folded, c.cells = m.buildMain(c.q, open)
	c.refs = c.pg.refs()
	m.main = c
	return c
}

// nextFlip is the first moment after now a ticket's latest sign of a session at
// work leaves the window that says it is live, or that its steps read idle, which
// moves a row between the groups: zero when no ticket is live.
func (m *Model) nextFlip(now time.Time) time.Time {
	var next time.Time
	for _, t := range m.w.tickets {
		if age, ok := t.live(now, m.w.sessionEnd); ok {
			if at := now.Add(liveWindow - age); next.IsZero() || at.Before(next) {
				next = at
			}
		}
		if last := t.lastActivity(); !last.IsZero() && now.Sub(last) <= liveWindow {
			if at := last.Add(liveWindow); next.IsZero() || at.Before(next) {
				next = at
			}
		}
	}
	return next
}

// clockStep brings the list built to a later step of the clock when no row moves
// between groups: the ages are drawn again, in the cells the rows were built
// with, and the page is laid out again; no row is built again.
func (m *Model) clockStep(c *mainCache, open map[string]bool, bucket int64) {
	var age func(rows []queueRow)
	age = func(rows []queueRow) {
		for _, r := range rows {
			if cells := c.cells[r.ref().identity()]; len(cells) > 0 {
				cells[len(cells)-1] = m.st.dim(m.ageOf(r.at))
			}
			age(r.kids)
		}
	}
	age(c.q.sections)
	for g := range c.q.byGroup {
		age(c.q.byGroup[g])
	}
	p, folded := m.draftMain(c.q, open, &searchDraw{cellOf: func(r queueRow) []string { return c.cellsOf(m, r) }, keepFolds: true})
	c.pg, c.folded, c.bucket = m.layMain(p), folded, bucket
	c.refs = c.pg.refs()
	if c.srch != nil {
		c.srch.res = nil
	}
}
