package land

import (
	"fmt"
)

// The states of a predecessor an issue is suggested to land after.
const (
	// AfterLanded: the predecessor has a landed line in every repo it built in.
	AfterLanded = "landed"
	// AfterNotLanded: it has not; the suggestion stands.
	AfterNotLanded = "not landed"
	// AfterClosed: the predecessor was closed, and a closed issue is no wait.
	AfterClosed = "closed"
)

// Suggestion is one issue another is suggested to land after, with where it
// stands. It is a suggestion shown to the person, never a rule: Rules lists
// what must land first, and this is not in it.
type Suggestion struct {
	Ticket, Title, State string
}

// Pending says the suggestion still stands: the predecessor is not landed.
func (s Suggestion) Pending() bool { return s.State == AfterNotLanded }

// Suggestions are the rows of issue's `after` list in the order it records
// them. Every reader of the suggestion (`jig next`, `jig land`) reads this one
// list, so they agree. A ticket with no list has none.
func Suggestions(d Deps, issue string) ([]Suggestion, error) {
	rec, err := d.Store.ReadTicket(issue)
	if err != nil {
		return nil, err
	}
	var out []Suggestion
	for _, id := range rec.After {
		id = resolve(d.Store, id)
		s := Suggestion{Ticket: id}
		pred, err := d.Store.ReadTicket(id)
		if err != nil {
			return nil, fmt.Errorf("land: read %s, which %s is suggested to land after: %w", id, issue, err)
		}
		s.Title = pred.Title
		switch landed, err := landedEverywhere(d, id); {
		case err != nil:
			return nil, err
		case landed:
			s.State = AfterLanded
		case pred.Closed != nil:
			s.State = AfterClosed
		default:
			s.State = AfterNotLanded
		}
		out = append(out, s)
	}
	return out, nil
}

// Followers are the open issues suggested to land after ticket that have not
// landed themselves: what landing ticket lets them follow.
func Followers(d Deps, ticket string) ([]string, error) {
	ids, err := d.Store.Tickets()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		rec, err := d.Store.ReadTicket(id)
		if err != nil || rec.Closed != nil || len(rec.After) == 0 {
			continue
		}
		for _, a := range rec.After {
			if resolve(d.Store, a) != ticket {
				continue
			}
			if landed, err := landedEverywhere(d, id); err == nil && !landed {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// landedEverywhere says id has a landed line in each repo it built in: when
// the merge facts do not say its repos, any landed line does.
func landedEverywhere(d Deps, id string) (bool, error) {
	lines, err := landedLines(d.Store, id, "")
	if err != nil || len(lines) == 0 {
		return false, err
	}
	if d.Merges == nil {
		return true, nil
	}
	own, err := d.Merges.Of(id)
	if err != nil || len(own.Repos) == 0 {
		return true, nil
	}
	return landedAll(own, lines), nil
}

// PendingNote is what `jig land` repeats to the person for each predecessor
// not landed.
func PendingNote(ticket string, rows []Suggestion) []string {
	var notes []string
	for _, r := range rows {
		if r.Pending() {
			notes = append(notes, fmt.Sprintf("%s is suggested to land after %s, not landed: say \"Land %s first\" to keep the order; landing %s now is the person's word and breaks nothing", ticket, r.Ticket, r.Ticket, ticket))
		}
	}
	return notes
}

// FollowerNotes are what landing a ticket says of each issue suggested to
// follow it.
func FollowerNotes(followers []string) []string {
	var notes []string
	for _, f := range followers {
		notes = append(notes, f+" is suggested to land after this")
	}
	return notes
}
