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
// to look at, which jig cannot cheaply tell. A keep a person decided after the
// round opened is pending: the next gate queues it as a fix slice, as the
// triage help says, so the round does not stand over it. A slice short of
// green is code still to come, so no round stands over it either: the gate
// goes on and refuses as it would with no round open. It runs no oracle and
// writes nothing.
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
	decisions, err := ReadTriageDecisions(d.Store, ticket)
	if err != nil {
		return nil, err
	}
	for _, dec := range decisions {
		if dec.Pending && dec.Outcome == TriageKeep {
			return nil, nil
		}
	}
	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: standing round: read slices: %w", err)
	}
	for _, sl := range slices {
		state, err := d.Store.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: standing round: read slice %s: %w", sl.ID, err)
		}
		if state.State != "green" {
			return nil, nil
		}
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
