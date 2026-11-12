package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Standing is a ticket's last gate round that stands at the branch's current
// head in every repo it covers: opening another round over it would only
// repeat it.
type Standing struct {
	Round int
	// Open is whether the round is still open for a reviewer; Prepared is then
	// the open round.
	Open     bool
	Prepared Prepared
	// Verdict and CarriedFrom are a recorded round's, as report.yaml has them.
	Verdict     string
	CarriedFrom int
	// Heads is repo -> the commit the round stands at.
	Heads map[string]string
}

// StandingRound reads the ticket's last round, the open one else the latest
// recorded, and returns it when the branch has not moved off it: every repo
// the round covers has the ticket's branch, in the operator's clone, at the
// head the round reviewed. It returns nil when there is no round, when the
// branch moved, and when a repo has no clone to look at, which jig cannot
// cheaply tell. It runs no oracle and writes nothing.
//
// The store is synced first: the round it asks about may be one a gate on
// another process has only just opened.
func StandingRound(d Deps, ticket string) (*Standing, error) {
	if err := d.Store.Sync(); err != nil {
		return nil, fmt.Errorf("verifydeliver: standing round: sync store: %w", err)
	}
	st := &Standing{}
	var branch string
	if p, ok, err := CurrentRound(d, ticket); err != nil {
		return nil, err
	} else if ok {
		st.Round, st.Open, st.Prepared, branch = p.Round, true, p, p.Branch
		st.Heads = map[string]string{}
		for _, r := range p.Covered() {
			st.Heads[r.Repo] = r.Head
		}
	} else {
		n, err := existingGateRounds(d.Store, ticket)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: standing round: count rounds: %w", err)
		}
		if n == 0 {
			return nil, nil
		}
		data, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, ticket, n), "report.yaml"))
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: standing round: read round %d: %w", n, err)
		}
		var rep reportYAML
		if err := yaml.Unmarshal(data, &rep); err != nil {
			return nil, fmt.Errorf("verifydeliver: standing round: parse round %d: %w", n, err)
		}
		st.Round, st.Verdict, st.CarriedFrom = n, rep.Verdict, rep.CarriedFrom
		st.Heads = rep.ReviewedSHA
		if len(st.Heads) == 0 {
			st.Heads = rep.TargetSHA
		}
		slices, err := d.Store.ReadSlices(ticket)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: standing round: read slices: %w", err)
		}
		repos, err := gateRepos(d, ticket, slices)
		if err != nil {
			return nil, err
		}
		gb, err := resolveGateBranch(d, ticket, repos[0].target, "", slices)
		if err != nil {
			return nil, err
		}
		branch = gb.Name
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
