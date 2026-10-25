package store

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// TicketExists reports whether the store holds ticket id to work: its folder
// holds a file other than jig's own lock sidecars and temporary files. A git
// checkout or reset that removes a ticket leaves those behind, since the store
// ignores them, and what is left is no ticket: the commands that work one
// refuse it and point at `jig ticket new`. The id scan still counts the folder
// (scanTicketIDs), so its number, which a ticket on another branch may hold,
// is never minted again.
func (s *Store) TicketExists(id string) bool {
	errHolds := errors.New("holds a file")
	err := filepath.WalkDir(s.TicketDir(id), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || scratchFile(d.Name()) {
			return nil
		}
		return errHolds
	})
	return errors.Is(err, errHolds)
}

// Tickets lists the tickets the store holds to show and work, in TicketIDs'
// order: TicketIDs less the folders that hold nothing of a ticket's own
// (TicketExists). The listings a person or the mirror reads go through it;
// the id scan and the writers' claims keep counting every folder.
func (s *Store) Tickets() ([]string, error) {
	ids, err := s.TicketIDs()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if s.TicketExists(id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// scratchFile is whether name is a file jig writes beside the store's own and
// the store ignores: a lock sidecar ("<path>.lock", which Lock creates and
// never removes) or an atomic write's temporary file.
func scratchFile(name string) bool {
	return strings.HasSuffix(name, ".lock") || (strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp"))
}
