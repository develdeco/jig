package store

import (
	"regexp"
	"strings"
)

// branchPrefix is the namespace jig's branches have always lived in:
// jig/<id>. A branch binds with or without it.
const branchPrefix = "jig/"

// branchLeadPattern matches a branch name's leading ticket id: a key and a
// number in ticketIDPattern's own grammar, followed by the end of the name
// or a "-" that starts the slug.
var branchLeadPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]{0,9}-[1-9][0-9]*)(?:-|$)`)

// BranchLeadID returns the ticket id a branch name leads with, or "" when it
// leads with none: jig/ is stripped, then the leading KEY-N (a key, a "-", a
// number from 1) is taken, provided it is the whole name or is followed by
// "-". So jig/JIG-1, JIG-1 and JIG-1-fix-login all lead with JIG-1, and
// main, feature/JIG-1 and JIG-1x lead with nothing. It reads the name alone:
// whether a ticket by that id exists is TicketForBranch's question.
func BranchLeadID(branch string) string {
	m := branchLeadPattern.FindStringSubmatch(strings.TrimPrefix(branch, branchPrefix))
	if m == nil {
		return ""
	}
	return m[1]
}

// TicketForBranch binds a branch name to the ticket it works on: the
// inverse of ResolveTicketBranch. The name's leading id (BranchLeadID) is
// resolved with ResolveTicket, so a branch named for a ticket's old id still
// binds to the ticket that id was renamed to. how says what bound it: "id"
// when the lead is a ticket's own id, "alias" when it is one of a ticket's
// aliases. A branch that leads with no id, or with an id no ticket claims,
// binds to nothing: id and how are empty and err is nil. An id claimed by
// more than one ticket is the TICKET_ALIAS_COLLISION ResolveTicket refuses
// with.
func (s *Store) TicketForBranch(branch string) (id, how string, err error) {
	lead := BranchLeadID(branch)
	if lead == "" {
		return "", "", nil
	}
	claims, err := s.aliasClaims()
	if err != nil {
		return "", "", err
	}
	if len(claims[lead]) == 0 {
		return "", "", nil
	}
	id, err = s.ResolveTicket(lead)
	if err != nil {
		return "", "", err
	}
	if id == lead {
		return id, "id", nil
	}
	return id, "alias", nil
}
