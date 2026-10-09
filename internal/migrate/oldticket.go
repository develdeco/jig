package migrate

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/store"
)

// A v1 store's real tickets sit directly at the store root (brief.md#Context:
// "Today every ticket folder sits at the store root"), not under tickets/ -
// the layout Store.TicketDir and every other store method after it assume.
// `jig store migrate` is the one reader of that older shape, since turning
// it into the new one is its whole job; everywhere else in this package, an
// old id's own folder and files are read through oldTicketDir, never through
// Store.TicketDir.

// oldTicketDir returns oldID's folder at the v1 store's root.
func oldTicketDir(root, oldID string) string {
	return filepath.Join(root, oldID)
}

// OldTicketIDs lists every id-shaped folder directly at root (a v1 store's
// own tickets), sorted by key then number, the same order Store.TicketIDs
// sorts a v2 store's.
func OldTicketIDs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	type idEntry struct {
		id  string
		key string
		n   int
	}
	var ids []idEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key, n, ok := store.ParseTicketID(e.Name())
		if !ok {
			continue
		}
		ids = append(ids, idEntry{id: e.Name(), key: key, n: n})
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].key != ids[j].key {
			return ids[i].key < ids[j].key
		}
		return ids[i].n < ids[j].n
	})
	out := make([]string, len(ids))
	for i, e := range ids {
		out[i] = e.id
	}
	return out, nil
}
