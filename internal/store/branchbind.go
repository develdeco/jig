package store

import (
	"regexp"
	"strings"
)

// branchPrefix is the namespace jig's branches lived in before they were named
// for their titles: jig/<id>. A branch binds with or without it, so work
// begun on a jig/<id> branch keeps its ticket.
const branchPrefix = "jig/"

// branchLeadPattern matches a branch name's leading ticket id: a key and a
// number in ticketIDPattern's own grammar, followed by the end of the name
// or a "-" that starts the slug.
var branchLeadPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]{0,9}-[1-9][0-9]*)(?:-|$)`)

// branchIssuePattern matches a branch name that leads with a bare number: a
// tracker's issue number, as GitHub's own "create a branch" names one
// (102-upload-item-gallery), followed by the end of the name or a "-".
var branchIssuePattern = regexp.MustCompile(`^([1-9][0-9]*)(?:-|$)`)

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

// BranchLeadIssue returns the issue number a branch name leads with, or ""
// when it leads with none: jig/ is stripped, then the leading number (from
// 1, no leading zero) is taken, provided it is the whole name or is followed
// by "-". So 102 and 102-upload-item-gallery lead with 102, and JIG-1,
// 102x and 0102-x lead with nothing. Like BranchLeadID it reads the name
// alone.
func BranchLeadIssue(branch string) string {
	m := branchIssuePattern.FindStringSubmatch(strings.TrimPrefix(branch, branchPrefix))
	if m == nil {
		return ""
	}
	return m[1]
}

// TicketForBranch binds a branch name to the ticket it works on: the
// inverse of ResolveTicketBranch. A branch binds by the id it leads with, the
// longer lead first (the two shapes cannot both begin one name, so at most one
// is tried):
//
//   - KEY-N (BranchLeadID): the id of a ticket, else one of a ticket's aliases,
//     so a branch named for a ticket's old id still binds to the ticket that id
//     was renamed to, and a jig/<old id> branch begun before a migration keeps
//     its ticket;
//   - a bare number (BranchLeadIssue): the issue number a tracker gave a ticket
//     (the GitHub mirror's record), so the branch GitHub makes from an issue
//     binds with no claim and no recorded branch. A tracker whose ids are
//     KEY-N keys (Jira's) would be asked for the first shape too, through
//     remoteLeads; none is mirrored to yet.
//
// how says what bound it: "id" when the lead is a ticket's own id, "alias"
// when it is one of a ticket's aliases, "remote" when it is a tracker's id for
// the ticket. A branch that leads with no id, or with one no ticket claims,
// binds to nothing: id and how are empty and err is nil. A lead claimed by
// more than one ticket is refused: TICKET_ALIAS_COLLISION for an id or alias
// (the refusal ResolveTicket gives), TICKET_REMOTE_ID_COLLISION for a
// tracker's id.
func (s *Store) TicketForBranch(branch string) (id, how string, err error) {
	if lead := BranchLeadID(branch); lead != "" {
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
	if issue := BranchLeadIssue(branch); issue != "" {
		return s.ticketForRemoteLead(issue)
	}
	return "", "", nil
}

// ticketForRemoteLead is the ticket a tracker gave the id lead, "remote".
func (s *Store) ticketForRemoteLead(lead string) (id, how string, err error) {
	leads, err := s.remoteLeads()
	if err != nil {
		return "", "", err
	}
	switch owners := leads[lead]; len(owners) {
	case 0:
		return "", "", nil
	case 1:
		return owners[0], "remote", nil
	default:
		return "", "", remoteLeadCollisionError(lead, owners)
	}
}
