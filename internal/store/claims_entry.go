package store

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
)

// ReserveEntry reserves the tracker entry e for ticket in the claims log, as
// an id is reserved: a line for e from another ticket refuses
// TRACKER_ENTRY_TAKEN naming that ticket, and a line for the same ticket
// appends nothing. In single-main mode there is no claims branch to race on,
// and the tracker tree's own check (a record is one owner's) is the guard, so
// nothing is appended.
func (s *Store) ReserveEntry(e TrackerEntry, ticket string) error {
	writers, err := s.Mode()
	if err != nil {
		return err
	}
	if !writers {
		return nil
	}
	me, err := s.writerName()
	if err != nil {
		return err
	}
	key := e.String()
	_, err = s.claimsTx(func(lines []ClaimLine) (*ClaimLine, error) {
		for _, l := range lines {
			if l.Kind != ClaimEntry || l.Entry != key {
				continue
			}
			if l.Ticket == ticket {
				return nil, nil
			}
			return nil, &axi.Error{
				Msg:  fmt.Sprintf("tracker entry %s is reserved by %s", key, l.Ticket),
				Code: "TRACKER_ENTRY_TAKEN",
				Help: []string{fmt.Sprintf("The item is %s's issue already: work on %s instead", l.Ticket, l.Ticket)},
			}
		}
		return &ClaimLine{Kind: ClaimEntry, Ticket: ticket, Entry: key, Writer: me}, nil
	})
	return err
}
