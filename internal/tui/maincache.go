package tui

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

	q      mainQueue
	pg     page
	folded map[rowIdentity]rowIdentity
	refs   []rowRef
}

// foldsOpen says which folds of the project's list are open, as a key.
func (m *Model) foldsOpen() (map[string]bool, string) {
	open := m.nav[0].open
	key := ""
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
	m.folded = c.folded
	return c.refs
}

// mainBuilt is the project's list for the world as it stands, built again only
// when something it reads moved.
func (m *Model) mainBuilt() *mainCache {
	open, folds := m.foldsOpen()
	bucket := m.now().Unix() / 5
	if c := m.main; c != nil && c.gen == m.w.gen && c.width == m.width && c.bucket == bucket && c.folds == folds {
		return c
	}
	c := &mainCache{gen: m.w.gen, width: m.width, bucket: bucket, folds: folds}
	c.q = m.mainRows()
	c.pg, c.folded = m.buildMain(c.q, open)
	c.refs = c.pg.refs()
	m.main = c
	return c
}
