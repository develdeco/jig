package store

import (
	"fmt"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
)

// ResolveTicket turns idOrAlias - an id or one of a ticket's aliases
// (ticket.yaml's aliases:) - into that ticket's current id. An idOrAlias
// that names no ticket and claims no alias either is returned unchanged, so
// a caller's own "not found" check (requireTicket and the like) still fires
// on it with its own message; ResolveTicket itself only ever turns a known
// alias into the id it belongs to, or refuses a collision.
//
// Every command that takes a ticket id, the blocked_by refs jig graduate and
// jig validate check, and the GitHub mirror's own blocked_by link and
// "Waits for" line all resolve through this one function, so an alias is
// read the same way everywhere jig follows a ticket id.
//
// idOrAlias claimed by more than one ticket - two tickets list it under
// aliases, or it is both one ticket's own id and another ticket's alias - is
// refused, naming every ticket that claims it: nothing here guesses which
// one was meant.
func (s *Store) ResolveTicket(idOrAlias string) (string, error) {
	claims, err := s.aliasClaims()
	if err != nil {
		return "", err
	}
	owners := claims[idOrAlias]
	switch len(owners) {
	case 0:
		return idOrAlias, nil
	case 1:
		return owners[0], nil
	default:
		return "", aliasCollisionError(idOrAlias, owners)
	}
}

// aliasClaims maps every id and every alias found in the store to the
// ticket ids that claim it: a ticket always claims its own id, plus each of
// its aliases. An id or alias claimed by exactly one ticket is what it
// resolves to; more than one is the collision ResolveTicket and CheckAliases
// both refuse.
//
// The scan (one TicketIDs plus one ReadTicket per ticket) runs at most once
// per Store value: the result is memoized on s.aliasClaimsCache, since a
// single run resolves many refs (jig validate's blocked_by checks and cycle
// DFS, the mirror's blocker links and "Waits for" line) and re-scanning the
// whole store for each would cost O(N) ticket.yaml reads per ref rather than
// per run. invalidateAliasClaims drops the cache after any write that could
// change it.
func (s *Store) aliasClaims() (map[string][]string, error) {
	s.aliasClaimsMu.Lock()
	defer s.aliasClaimsMu.Unlock()
	if s.aliasClaimsCache != nil {
		return s.aliasClaimsCache, nil
	}
	ids, err := s.TicketIDs()
	if err != nil {
		return nil, err
	}
	claims := make(map[string][]string, len(ids))
	for _, id := range ids {
		claims[id] = append(claims[id], id)
	}
	for _, id := range ids {
		// A ticket.yaml that cannot be read or decoded is that ticket's own
		// problem (jig validate already reports it via validateTicketDeps);
		// resolving some other id or alias must not fail just because one
		// unrelated ticket's record is broken, so a bad record here only
		// costs its own aliases' claims, never the id's.
		rec, err := s.ReadTicket(id)
		if err != nil {
			continue
		}
		for _, alias := range rec.Aliases {
			claims[alias] = append(claims[alias], id)
		}
	}
	s.aliasClaimsCache = claims
	return claims, nil
}

// CheckAliases reports every alias collision across the whole store: an
// alias claimed by two tickets, or equal to another ticket's own id. jig
// validate runs it alongside resolving the ticket under validation's own
// blocked_by refs, since a collision can involve two tickets neither of
// which is the one being validated.
func (s *Store) CheckAliases() ([]string, error) {
	claims, err := s.aliasClaims()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(claims))
	for k := range claims {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var problems []string
	for _, k := range keys {
		if owners := claims[k]; len(owners) > 1 {
			problems = append(problems, aliasCollisionError(k, owners).Msg)
		}
	}
	return problems, nil
}

// aliasCollisionError is the one refusal both ResolveTicket and CheckAliases
// report for idOrAlias claimed by owners (more than one ticket), naming every
// one of them.
func aliasCollisionError(idOrAlias string, owners []string) *axi.Error {
	sorted := append([]string(nil), owners...)
	sort.Strings(sorted)
	return &axi.Error{
		Msg:  fmt.Sprintf("%q is claimed by more than one ticket: %s", idOrAlias, strings.Join(sorted, ", ")),
		Code: "TICKET_ALIAS_COLLISION",
		Help: []string{fmt.Sprintf("Edit one of %s's ticket.yaml so only one ticket claims %q", strings.Join(sorted, ", "), idOrAlias)},
	}
}
