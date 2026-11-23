package tui

import (
	"fmt"
	"slices"
	"sort"
	"time"
)

// Every list of what happened reads newest first. The derivations (the log's
// lines, the clocks, every "latest" lookup) keep time order; a list is turned
// here, at the draw, so that no screen has an order of its own.

// newestFirst is rows newest first by the instant at gives each. Of rows of
// one instant the later written is above. rows are not changed.
func newestFirst[T any](rows []T, at func(T) time.Time) []T {
	out := slices.Clone(rows)
	sort.SliceStable(out, func(i, j int) bool { return at(out[i]).Before(at(out[j])) })
	slices.Reverse(out)
	return out
}

// cutNewest is the rows of a newest-first list that fit room rows. A list that
// does not fit gives up its last row to the mark that counts what was left out
// (olderRow), so the panel does not grow: it keeps room-1 rows and older is the
// number left out, zero for a list that fits.
func cutNewest[T any](rows []T, room int) (kept []T, older int) {
	if len(rows) <= room {
		return rows, 0
	}
	keep := max(0, room-1)
	return rows[:keep], len(rows) - keep
}

// olderRow is the row that ends a cut list: how many older rows lie below.
func (m *Model) olderRow(older int) string {
	return m.st.dim(fmt.Sprintf("↓ %d older", older))
}
