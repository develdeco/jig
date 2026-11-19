package verifydeliver

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// gateBranch is the branch one gate round works on, and how the ticket came
// to have it.
type gateBranch struct {
	// Name is the branch.
	Name string
	// Adopted is true when the ticket works on a branch it recorded (built
	// outside jig, adopted with `jig gate --branch`) or records this round:
	// its ticket.yaml, not the "<ticket>-<slug>" default, names it.
	Adopted bool
	// Adopting is true when this round is the one that records the branch.
	Adopting bool
	// Built is the commits the journal records jig built on the ticket's
	// branch and verified (journal.BuiltCommits), in journal order; none while
	// an adopted branch is only the author's. They stay in the clone until
	// someone pushes them, so once there are any, origin's copy of the branch
	// may lack them, and the round refuses a copy that does (chooseCopy).
	Built []string
}

// resolveGateBranch resolves the branch a gate round works on, once, from the
// ticket's record, its slices (which the round has read) and the --branch flag
// (empty when not given):
//
//   - no flag, or the flag names the branch the ticket recorded: the
//     recorded branch, or the ticket's default branch when none is recorded;
//   - the flag names another branch than the one recorded: refused,
//     BRANCH_MISMATCH, since a ticket's branch is recorded, not chosen per
//     round;
//   - the flag on a ticket with no recorded branch: the round adopts it,
//     unless the name cannot be a ticket's branch (TICKET_BRANCH_INVALID) or
//     jig already built on the ticket's own branch (TICKET_ALREADY_BUILT,
//     jigBuilt), which adopting another branch would strand. The store is the
//     judge, never this machine's lease: a lease shows what one machine has,
//     the store what any machine did.
//
// Nothing is recorded here; Gate records an adoption once every later
// precondition of the round has passed.
func resolveGateBranch(d Deps, ticket, target, flag string, slices []store.Slice) (gateBranch, error) {
	rec, err := d.Store.ReadTicket(ticket)
	if err != nil {
		return gateBranch{}, fmt.Errorf("verifydeliver: gate: read ticket record: %w", err)
	}
	// A recorded branch is validated whatever the flag says: a hand-edited
	// ticket.yaml naming the target must not be adopted around.
	name, err := d.Store.ResolveTicketBranch(ticket, rec, target)
	if err != nil {
		return gateBranch{}, fmt.Errorf("verifydeliver: gate: resolve ticket branch: %w", err)
	}
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return gateBranch{}, fmt.Errorf("verifydeliver: gate: read journal: %w", err)
	}
	built := journal.BuiltCommits(lines)
	adopted := rec.Adopted()

	switch {
	case flag == "" || (adopted && flag == name):
		return gateBranch{Name: name, Adopted: adopted, Built: built}, nil
	case adopted:
		return gateBranch{}, &axi.Error{
			Msg:  fmt.Sprintf("ticket %s already works on branch %s, and --branch names %s", ticket, name, flag),
			Code: "BRANCH_MISMATCH",
			Help: []string{
				fmt.Sprintf("Gate the ticket's own branch with `jig gate %s`", ticket),
				fmt.Sprintf("For %s, mint a ticket of its own with `jig ticket new --title \"...\"`", flag),
			},
		}
	}

	if err := d.Store.CheckAdoptableBranch(ticket, flag, target); err != nil {
		return gateBranch{}, err
	}
	builtOn, err := jigBuilt(d.Store, ticket, slices, lines)
	if err != nil {
		return gateBranch{}, err
	}
	if builtOn {
		return gateBranch{}, &axi.Error{
			Msg:  fmt.Sprintf("the store records jig building on ticket %s's own branch %s, so the ticket cannot adopt %s", ticket, name, flag),
			Code: "TICKET_ALREADY_BUILT",
			Help: []string{
				fmt.Sprintf("Gate the ticket's own branch with `jig gate %s`", ticket),
				fmt.Sprintf("For %s, mint a ticket of its own with `jig ticket new --title \"...\"`", flag),
			},
		}
	}
	return gateBranch{Name: flag, Adopted: true, Adopting: true}, nil
}

// jigBuilt reports whether jig has built on the ticket's own branch, from what
// the store records, whichever machine built. Three facts say so:
//
//   - the journal records a verified commit (journal.BuiltCommits). This
//     version marks the slice green before it journals the verified line, so
//     a run that stopped between the two writes shows a green slice whose
//     commit no verified line names yet (the next jig verify of the commit
//     writes it), and the green slice below covers it;
//   - the journal holds a green result line naming a commit
//     (journal.GreenClaims). Every jig version journals one before it routes
//     the result, and nothing removes it, so it is the fact that survives for a
//     journal of any version: a v0.1.x one has no verified lines, and a requeue
//     (`jig requeue --from-brief-diff`) sets green slices back to queued, so
//     neither of the other facts is left of such a ticket. It is a claim, not
//     proof, so it decides only where there is no verified line to say better,
//     which is a journal from before them, or a ticket of this version whose
//     green claims all failed verification. That ticket is refused too: a
//     loud refusal it recovers from (mint a ticket for the branch) instead of
//     an adoption that is silent and permanent;
//   - a slice is now green. Every jig version writes green only after the
//     commit verified (frontier's route).
//
// Adoption asks only whether jig built, not which commits: the commit list,
// which the rest of the adopted path needs, is the verified lines', and an
// adopted ticket always has them, since only this version adopts.
func jigBuilt(st *store.Store, ticket string, slices []store.Slice, lines []journal.Line) (bool, error) {
	if len(journal.BuiltCommits(lines)) > 0 || len(journal.GreenClaims(lines)) > 0 {
		return true, nil
	}
	for _, sl := range slices {
		state, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return false, fmt.Errorf("verifydeliver: gate: read slice %s state: %w", sl.ID, err)
		}
		if state.State == "green" {
			return true, nil
		}
	}
	return false, nil
}

// originsTip is the sha branch has on origin as dir last fetched it, or "" when
// origin has no such branch.
func originsTip(dir, branch string) (string, error) {
	tip, err := gitx.RefTip(dir, "refs/remotes/origin/"+branch)
	if err != nil {
		return "", fmt.Errorf("look up origin/%s: %w", branch, err)
	}
	return tip, nil
}

// branchCopy names whose copy of a ticket's branch a gate or publish works on:
// the clone's, where jig's commits wait until someone pushes them, or origin's.
// What is to be done about a refusal depends on it, since only the clone's is
// the person's to integrate anything in.
type branchCopy string

const (
	originsCopy branchCopy = "origin's"
	// clonesCopy is the copy in the operator's own clone: committed work the
	// person (or the agent working for them) has not pushed yet.
	clonesCopy branchCopy = "the clone's"
)

// branchTipIn is the sha of refs/heads/<branch> in the repository at dir, or
// "" when it has no such branch.
func branchTipIn(dir, branch string) (string, error) {
	tip, err := gitx.RefTip(dir, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("look up %s in %s: %w", branch, dir, err)
	}
	return strings.TrimSpace(tip), nil
}
