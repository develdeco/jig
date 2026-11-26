package tui

// The rows of the chart, plan and multi-task screens, and the lines of their
// activity, are readings of every ticket they list. A key moves a cursor and
// builds none of them: they are built once for the world as it stands (state.go:
// its count of moves), the clock counted in fives as the project's list is
// (maincache.go), and a screen draws from them.

// rowsMemo holds what was built for one count of the world and one step of the
// clock, by what it is of.
type rowsMemo struct {
	gen    uint64
	bucket int64
	rows   map[string][]chartRow
	logs   map[string]*chartLog
}

// memoNow is the memo for the world and the clock as they stand: the one kept
// when they have not moved, else a new, empty one.
func (m *Model) memoNow() *rowsMemo {
	bucket := m.now().Unix() / 5
	if c := m.rowMemo; c != nil && c.gen == m.w.gen && c.bucket == bucket {
		return c
	}
	m.rowMemo = &rowsMemo{gen: m.w.gen, bucket: bucket, rows: map[string][]chartRow{}, logs: map[string]*chartLog{}}
	return m.rowMemo
}

// rowsOf are the rows named name, built by build when the world and the clock
// have not been read for them yet.
func (m *Model) rowsOf(name string, build func() []chartRow) []chartRow {
	memo := m.memoNow()
	if rows, ok := memo.rows[name]; ok {
		return rows
	}
	rows := build()
	memo.rows[name] = rows
	return rows
}

// chartLog is a chart's activity as it stands: every line of every ticket and
// every commit of the chart's folder, newest first, each with its row.
type chartLog struct {
	all  []chartLine
	refs []rowRef
	ids  []rowIdentity
}

// shownPage is the page the last frame drew, with what it was drawn for. The
// keys that move the cursor or the focus read the panes of what the person sees:
// a key builds no page of its own, the frame it brings builds one.
type shownPage struct {
	pg     page
	gen    uint64
	w, h   int
	depth  int
	at     navKey
	zoom   string
	search string
	tail   bool
}

// geometry is the page the panes are read from: the one the last frame drew while
// nothing it was drawn for has moved, else a page built now.
func (m *Model) geometry() page {
	s := m.shown
	if s == nil || m.help != nil || m.input != nil || m.ask != nil || m.top().screen == screenMain {
		return m.page()
	}
	e := m.top()
	if s.gen != m.w.gen || s.w != m.width || s.h != m.height || s.depth != len(m.nav) || s.at != keyOf(*e) ||
		s.zoom != e.zoom || s.search != string(e.psearch.query) || s.tail != m.tail {
		return m.page()
	}
	return s.pg
}

// chartLogOf is the activity named name, built by build when it has not been
// read for the world and the clock as they stand.
func (m *Model) chartLogOf(name string, build func() *chartLog) *chartLog {
	memo := m.memoNow()
	if l, ok := memo.logs[name]; ok {
		return l
	}
	l := build()
	memo.logs[name] = l
	return l
}
