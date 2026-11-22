package store

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
)

// ReserveEntry reserves the tracker entry e for ticket in the claims log, as
// an id is reserved: a line for e from another ticket refuses
// TRACKER_ENTRY_TAKEN naming that ticket, and a line for the same ticket
// appends nothing. The latest line for e is the one that holds it, and one
// of this writer's own for a ticket this store does not hold is an undone
// claim (only this writer pushes its branch, so a ticket of its own that the
// tree lacks was rolled back): it is taken over by a line for ticket, so a
// bind that failed leaves nothing that refuses the next. In single-main mode
// there is no claims branch to race on, and the tracker tree's own check (a
// record is one owner's) is the guard, so nothing is appended.
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
		var holder *ClaimLine
		for i, l := range lines {
			if l.Kind != ClaimEntry || l.Entry != key {
				continue
			}
			if l.Ticket == ticket {
				return nil, nil
			}
			holder = &lines[i]
		}
		if holder != nil && !(holder.Writer == me && !s.TicketExists(holder.Ticket)) {
			return nil, &axi.Error{
				Msg:  fmt.Sprintf("tracker entry %s is reserved by %s", key, holder.Ticket),
				Code: "TRACKER_ENTRY_TAKEN",
				Help: []string{fmt.Sprintf("The item is %s's issue already: work on %s instead", holder.Ticket, holder.Ticket)},
			}
		}
		return &ClaimLine{Kind: ClaimEntry, Ticket: ticket, Entry: key, Writer: me}, nil
	})
	return err
}
