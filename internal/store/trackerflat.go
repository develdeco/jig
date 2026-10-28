package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
)

// TrackerMove is one record MoveFlatTrackerRecords moved.
type TrackerMove struct {
	// Owner is the ticket or chart the record names.
	Owner TrackerOwner
	// From and To are the record's old and new path, relative to the store root
	// and in slash form.
	From, To string
}

// MoveFlatTrackerRecords moves every record still in the flat layout, where the
// link was named <remote id> and the record sat beside it as <remote id>.yaml,
// into its entry's folder as record.yaml, and returns what it moved. The
// record is renamed with its bytes untouched, the flat layout's link at the
// folder's path goes, and the link is made again inside the folder where the
// clone keeps symlinks, so the tree is whole after each move. A flat record
// whose entry already has a record.yaml of the same owner is a leftover, and is
// dropped; one whose entry has the record of another owner is refused,
// TRACKER_ENTRY_TAKEN, with both left as they are. A flat record that cannot be
// read stays where it is: it is the tree check's to report. The moves are
// working-tree changes, committed with the next checkpoint.
func (s *Store) MoveFlatTrackerRecords() ([]TrackerMove, error) {
	scan, err := s.scanTracker()
	if err != nil {
		return nil, err
	}
	var moved []TrackerMove
	for _, from := range scan.flat {
		e, _ := entryOfRecordPath(from)
		if err := e.check(); err != nil {
			continue
		}
		flat := s.readTrackerRecord(from)
		if flat.Err != nil || !flat.Owner().valid() {
			continue
		}
		to := filepath.ToSlash(s.TrackerRecordRelPath(e))
		if _, err := os.Stat(s.TrackerRecordPath(e)); err == nil {
			have := s.readTrackerRecord(to)
			if have.Err != nil || have.Owner() != flat.Owner() {
				s.invalidateTrackerIndex()
				return moved, &axi.Error{
					Msg:  fmt.Sprintf("%s says its issue is %s, but %s holds the record of another ticket or chart", from, e, to),
					Code: "TRACKER_ENTRY_TAKEN",
					Help: []string{"Two items cannot be one remote item: remove the record that is wrong, then run `jig trackers sync` again"},
				}
			}
			if err := os.Remove(filepath.Join(s.Root, filepath.FromSlash(from))); err != nil {
				s.invalidateTrackerIndex()
				return moved, err
			}
			continue
		}
		if err := s.makeEntryFolder(e); err != nil {
			s.invalidateTrackerIndex()
			return moved, err
		}
		if err := os.Rename(filepath.Join(s.Root, filepath.FromSlash(from)), s.TrackerRecordPath(e)); err != nil {
			s.invalidateTrackerIndex()
			return moved, err
		}
		moved = append(moved, TrackerMove{Owner: flat.Owner(), From: from, To: to})
		s.invalidateTrackerIndex()
		if rec := s.readTrackerRecord(to); s.linkable(rec) {
			if _, err := s.LinkTrackerEntry(e, rec.Owner()); err != nil {
				return moved, err
			}
		}
	}
	s.invalidateTrackerIndex()
	return moved, nil
}
