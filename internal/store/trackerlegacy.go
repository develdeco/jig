package store

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
)

// LegacyTrackerRecord is a record still in the folder of the ticket or chart it
// belongs to, where A6's mirror first kept them.
type LegacyTrackerRecord struct {
	// Owner is the ticket or chart whose folder holds it.
	Owner TrackerOwner
	// Rel is its path relative to the store root.
	Rel string
}

// legacyRecordRel is the old place of o's record, relative to the store root:
// <ticket>/tracker/github.yaml or charts/<name>/github.yaml.
func (s *Store) legacyRecordRel(o TrackerOwner) string {
	if o.Chart != "" {
		return filepath.Join(s.ownerRelDir(o), legacyRecordFile)
	}
	return filepath.Join(s.ownerRelDir(o), "tracker", legacyRecordFile)
}

// LegacyTrackerRecord returns o's record in its old place, when it has one.
func (s *Store) LegacyTrackerRecord(o TrackerOwner) (LegacyTrackerRecord, bool, error) {
	rel := s.legacyRecordRel(o)
	if _, err := os.Stat(filepath.Join(s.Root, rel)); err != nil {
		if os.IsNotExist(err) {
			return LegacyTrackerRecord{}, false, nil
		}
		return LegacyTrackerRecord{}, false, err
	}
	return LegacyTrackerRecord{Owner: o, Rel: rel}, true, nil
}

// LegacyTrackerRecords lists the records still in a ticket's or chart's own
// folder: every ticket's, then every chart's.
func (s *Store) LegacyTrackerRecords() ([]LegacyTrackerRecord, error) {
	ids, err := s.TicketIDs()
	if err != nil {
		return nil, err
	}
	charts, err := s.ChartNames()
	if err != nil {
		return nil, err
	}
	var out []LegacyTrackerRecord
	for _, o := range ownersOf(ids, charts) {
		l, ok, err := s.LegacyTrackerRecord(o)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, l)
		}
	}
	return out, nil
}

// ownersOf lists the tickets, then the charts, as owners.
func ownersOf(tickets, charts []string) []TrackerOwner {
	out := make([]TrackerOwner, 0, len(tickets)+len(charts))
	for _, id := range tickets {
		out = append(out, TrackerOwner{Ticket: id})
	}
	for _, name := range charts {
		out = append(out, TrackerOwner{Chart: name})
	}
	return out
}

// ReadLegacyTrackerRecord reads a record in its old place as the mapping it
// is: every key, in file order.
func (s *Store) ReadLegacyTrackerRecord(l LegacyTrackerRecord) (yaml.Node, error) {
	data, err := readFile(filepath.Join(s.Root, l.Rel))
	if err != nil {
		return yaml.Node{}, err
	}
	_, _, fields, err := decodeTrackerRecord(data)
	if err != nil {
		return yaml.Node{}, fmt.Errorf("store: %s could not be decoded: %w", filepath.ToSlash(l.Rel), err)
	}
	return fields, nil
}

// MoveLegacyTrackerRecord moves a record from its old place to entry e: the
// tree record names the owner and keeps every key of the old one, then the
// old file goes (and its tracker/ folder, once empty). An entry the tree
// already has for the same owner (in its folder, or still in the flat layout) is
// the same record, and the old file is simply dropped; one it has for another
// owner is refused, TRACKER_ENTRY_TAKEN, with both left as they are.
func (s *Store) MoveLegacyTrackerRecord(l LegacyTrackerRecord, e TrackerEntry, header string) error {
	if err := e.check(); err != nil {
		return err
	}
	fields, err := s.ReadLegacyTrackerRecord(l)
	if err != nil {
		return err
	}
	rels := []string{s.flatRecordRelPath(e)}
	if s.entryIsFolder(e) {
		rels = []string{s.TrackerRecordRelPath(e), s.flatRecordRelPath(e)}
	}
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(s.Root, rel)); err != nil {
			continue
		}
		have := s.readTrackerRecord(filepath.ToSlash(rel))
		if have.Err == nil && have.Owner() == l.Owner {
			return s.DropLegacyTrackerRecord(l)
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("%s says its issue is %s, but %s holds the record of another ticket or chart", filepath.ToSlash(l.Rel), e, filepath.ToSlash(rel)),
			Code: "TRACKER_ENTRY_TAKEN",
			Help: []string{"Two items cannot be one remote item: remove the record that is wrong, then run `jig trackers sync` again"},
		}
	}
	if err := s.WriteTrackerRecord(e, l.Owner, &fields, header); err != nil {
		return err
	}
	return s.DropLegacyTrackerRecord(l)
}

// DropLegacyTrackerRecord removes an old-place record and the tracker/ folder
// it leaves empty.
func (s *Store) DropLegacyTrackerRecord(l LegacyTrackerRecord) error {
	abs := filepath.Join(s.Root, l.Rel)
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	if l.Owner.Chart == "" {
		_ = os.Remove(filepath.Dir(abs))
	}
	return nil
}
