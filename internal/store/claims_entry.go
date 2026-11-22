package store

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
)

// entryHolder folds the claims log into who holds entry key: the latest line
// for it decides, an entry line holds it for its ticket and a release frees
// it. held is false for an entry no line holds.
func entryHolder(lines []ClaimLine, key string) (holder ClaimLine, held bool) {
	for _, l := range lines {
		if l.Entry != key {
			continue
		}
		switch l.Kind {
		case ClaimEntry:
			holder, held = l, true
		case ClaimEntryRelease:
			holder, held = ClaimLine{}, false
		}
	}
	return holder, held
}

// ReserveEntry reserves the tracker entry e for ticket in the claims log, as
// an id is reserved: the latest line for e decides. A line for the same
// ticket appends nothing; a release, or no line, reserves it; a line for
// another ticket refuses TRACKER_ENTRY_TAKEN naming that ticket and its
// writer, unless it is one of this writer's own for a ticket this store does
// not hold: an undone claim (only this writer pushes its branch, so a ticket
// of its own that the tree lacks was rolled back), which a line for ticket
// takes over, so a bind that failed leaves nothing that refuses its re-run.
// In single-main mode there is no claims branch to race on, and the tracker
// tree's own check (a record is one owner's) is the guard, so nothing is
// appended.
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
		holder, held := entryHolder(lines, key)
		if held && holder.Ticket == ticket {
			return nil, nil
		}
		if held && !(holder.Writer == me && !s.TicketExists(holder.Ticket)) {
			return nil, &axi.Error{
				Msg:  fmt.Sprintf("tracker entry %s is reserved by %s, written by %s", key, holder.Ticket, holder.Writer),
				Code: "TRACKER_ENTRY_TAKEN",
				Help: []string{fmt.Sprintf("If %s's bind failed, writer %s re-running the same bind takes the line over; otherwise work on %s instead", holder.Ticket, holder.Writer, holder.Ticket)},
			}
		}
		return &ClaimLine{Kind: ClaimEntry, Ticket: ticket, Entry: key, Writer: me}, nil
	})
	return err
}

// ReleaseEntry gives back the reservation ReserveEntry made for ticket, when
// it is still the latest line for e: a bind that failed after reserving its
// item. It appends nothing in single-main mode or when ticket does not hold
// the entry.
func (s *Store) ReleaseEntry(e TrackerEntry, ticket string) error {
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
		holder, held := entryHolder(lines, key)
		if !held || holder.Ticket != ticket {
			return nil, nil
		}
		return &ClaimLine{Kind: ClaimEntryRelease, Ticket: ticket, Entry: key, Writer: me}, nil
	})
	return err
}
