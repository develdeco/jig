package store

// TrackerRecordAt returns the record of entry e, when the tree has one: the
// by-entry index, whose Ticket is resolved through ResolveTicket. A record
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
// ResolveTicket gives it: the lookup from a tracker item to the ticket minted
// from it. ok is false when the entry has no record or its record names a
// chart.
func (s *Store) TicketOfEntry(e TrackerEntry) (ticket string, ok bool, err error) {
	rec, found, err := s.TrackerRecordAt(e)
	if err != nil || !found || rec.Err != nil || rec.Ticket == "" {
		return "", false, err
	}
	return rec.Ticket, true, nil
}
