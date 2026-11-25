package store

import "strings"

// ItemTicket is the ticket the record of item number of the GitHub repo names,
// when that one record settles it: it is read and no other record, and no
// ticket's file. ok is false for every other state, which a caller looks up
// further: no repo; no folder for the item under it (a record of the flat
// layout, in a ticket's own folder or under another repo is not looked for);
// a record that cannot be read, or that names a chart, no ticket, or an id the
// store holds no ticket under (an old one).
func (s *Store) ItemTicket(repo, number string) (ticket string, ok bool) {
	e := TrackerEntry{Provider: ProviderGitHub, Scope: strings.ToLower(repo), ID: number}
	// The folder is checked first: in the flat layout the entry's path is a
	// link into the ticket's own folder, which holds no tracker record.
	if e.check() != nil || !s.entryIsFolder(e) {
		return "", false
	}
	data, err := s.tReadFile(s.TrackerRecordPath(e))
	if err != nil {
		return "", false
	}
	named, chart, _, err := decodeTrackerRecord(data)
	if err != nil || chart != "" || !s.holdsID(named) {
		return "", false
	}
	return named, true
}

// TrackerRecordAt returns the record of entry e, when the tree has one: the
// by-entry index, whose Ticket is resolved through ResolveRecorded. A record
// that cannot be read is returned with its Err set.
func (s *Store) TrackerRecordAt(e TrackerEntry) (TrackerRecord, bool, error) {
	idx, err := s.trackerRecordIndex()
	if err != nil {
		return TrackerRecord{}, false, err
	}
	i, ok := idx.byEntry[e]
	if !ok {
		return TrackerRecord{}, false, nil
	}
	return idx.records[i], true, nil
}

// TicketOfEntry returns the ticket whose record sits at entry e, as
// ResolveRecorded gives it: the lookup from a tracker item to the ticket minted
// from it. ok is false when the entry has no record or its record names a
// chart.
func (s *Store) TicketOfEntry(e TrackerEntry) (ticket string, ok bool, err error) {
	rec, found, err := s.TrackerRecordAt(e)
	if err != nil || !found || rec.Err != nil || rec.Ticket == "" {
		return "", false, err
	}
	return rec.Ticket, true, nil
}
