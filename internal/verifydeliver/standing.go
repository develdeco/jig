package verifydeliver

import "fmt"

// Standing is a ticket's open gate round that stands at the branch's current
// head in every repo it covers: opening another round over it would only
// repeat it. A recorded round is never standing: a gate after it applies the
// decisions the person took and opens the next round.
type Standing struct {
	Round int
	// Prepared is the open round.
	Prepared Prepared
	// Heads is repo -> the commit the round stands at.
	Heads map[string]string
}

// StandingRound reads the ticket's open round and returns it when the branch
// has not moved off it: every repo the round covers has the ticket's branch,
// in the operator's clone, at the head the round opened at. It returns nil
// when no round is open, when the branch moved, and when a repo has no clone
// to look at, which jig cannot cheaply tell. It runs no oracle and writes
// nothing.
//
// The store is synced first: the round it asks about may be one a gate on
// another process has only just opened.
func StandingRound(d Deps, ticket string) (*Standing, error) {
	if err := d.Store.Sync(); err != nil {
		return nil, fmt.Errorf("verifydeliver: standing round: sync store: %w", err)
	}
	p, ok, err := CurrentRound(d, ticket)
	if err != nil || !ok {
		return nil, err
	}
	st := &Standing{Round: p.Round, Prepared: p, Heads: map[string]string{}}
	branch := p.Branch
	for _, r := range p.Covered() {
		st.Heads[r.Repo] = r.Head
	}
	if len(st.Heads) == 0 {
		return nil, nil
	}
	for repo, head := range st.Heads {
		clone := operatorClone(d, repo)
		if clone == "" {
			return nil, nil
		}
		tip, err := branchTipIn(clone, branch)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: standing round: %w", err)
		}
		if tip != head {
			return nil, nil
		}
	}
	return st, nil
}
