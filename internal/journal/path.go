package journal

import "github.com/develdeco/jig/internal/store"

// Path is the file ticket's journal is kept in, journal.ndjson in the
// ticket's folder. A reader that follows the file (jig events) opens it by
// this path on every read and never holds it: Append replaces the file.
func Path(st *store.Store, ticket string) string {
	return journalPath(st, ticket)
}
