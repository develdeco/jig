package store

import (
	"fmt"
	"regexp"
	"strconv"
)

// Blocker is one blocking reference on a Draft: the target ticket id, or (during
// graduation) a "#k" placeholder for the k-th sibling in the same
// Graduation.Tickets slice, 1-based, and its merge kind ("merged" or
// "stacked").
type Blocker struct {
	Ref  string
	Kind string
}

// Draft is a ticket to be minted: a title, a body, and blocking references.
type Draft struct {
	Title     string
	Body      string
	BlockedBy []Blocker
}

// Graduation is a chart of tickets to mint together: the chart's name plus the
// ordered ticket drafts. A draft's BlockedBy may reference an earlier
// sibling by its 1-based position ("#k").
type Graduation struct {
	Chart   string
	Tickets []Draft
}

// indexRefRE matches a graduation BlockedBy entry that references the k-th
// minted sibling rather than an existing ticket id.
var indexRefRE = regexp.MustCompile(`^#(\d+)$`)

// Graduate mints every ticket in g.Tickets in order, through format (a
// project.yaml ticket_format), resolving "#k" BlockedBy references to the id
// minted for the k-th ticket. Every minted ticket gets its
// <ticket>/ticket.yaml - the title and its resolved blockers, if any -
// written before the next ticket in g.Tickets is minted, the same record jig
// ticket new creates for a directly minted ticket. After each ticket is
// fully minted, onMinted (when non-nil) is called with its 0-based position
// in g.Tickets and its id, so a caller can record that ticket's id (e.g.
// write it back into a chart entry) before the next ticket is minted: a
// failure partway through then leaves every already-minted ticket recorded
// by the caller, and a re-run continues where it stopped instead of minting
// duplicates. Graduate returns the ids minted so far - complete on success,
// partial (with the error) on a failure partway through; an id is in the
// list only once that ticket's folder and record both exist, which Mint
// guarantees atomically (an id is never set to one Mint refused).
func (s *Store) Graduate(format string, g Graduation, onMinted func(i int, id string) error) ([]string, error) {
	ids := make([]string, len(g.Tickets))

	for i, d := range g.Tickets {
		resolved, err := resolveBlockedBy(d.BlockedBy, ids)
		if err != nil {
			return ids, fmt.Errorf("store: graduate: ticket %d: %w", i+1, err)
		}
		rec := Ticket{Title: d.Title}
		for _, b := range resolved {
			rec.BlockedBy = append(rec.BlockedBy, TicketBlockedBy{Ticket: b.Ref, Kind: b.Kind})
		}
		id, err := s.Mint(format, rec)
		if err != nil {
			return ids, fmt.Errorf("store: graduate: mint ticket %d: %w", i+1, err)
		}
		ids[i] = id
		if onMinted != nil {
			if err := onMinted(i, id); err != nil {
				return ids, err
			}
		}
	}

	return ids, nil
}

// resolveBlockedBy replaces any "#k" Ref in refs with ids[k-1] (the id
// minted for the k-th ticket so far), keeping each Blocker's Kind; anything
// else passes through unchanged, treated as an existing ticket id. It
// errors on a "#k" that names no minted ticket - out of range, or a sibling
// not minted yet - rather than writing the literal "#k" placeholder into the
// store as if it were a ticket id.
func resolveBlockedBy(refs []Blocker, ids []string) ([]Blocker, error) {
	if refs == nil {
		return nil, nil
	}
	out := make([]Blocker, len(refs))
	for i, r := range refs {
		if m := indexRefRE.FindStringSubmatch(r.Ref); m != nil {
			k, err := strconv.Atoi(m[1])
			if err != nil || k < 1 || k > len(ids) || ids[k-1] == "" {
				return nil, fmt.Errorf("blocked_by ref %q does not name an already-minted ticket", r.Ref)
			}
			out[i] = Blocker{Ref: ids[k-1], Kind: r.Kind}
			continue
		}
		out[i] = r
	}
	return out, nil
}
