package verifydeliver

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// gateBranch is the branch one gate round works on, and how the ticket came
// to have it.
type gateBranch struct {
	// Name is the branch.
	Name string
	// Adopted is true when the ticket works on a branch it recorded (built
	// outside jig, adopted with `jig gate --branch`) or records this round:
	// its ticket.yaml, not the "jig/<ticket>" default, names it.
	Adopted bool
	// Adopting is true when this round is the one that records the branch.
	Adopting bool
	// Built is the commits the journal records jig built on the adopted
	// branch and verified (journal.BuiltCommits), in journal order; none while
	// the branch is only the author's. They stay in the build lease until
	// someone pushes them, so once there are any, origin's copy of the branch
	// may lack them, and the round chooses its copy by that (chooseBuiltCopy).
	Built []string
}

// resolveGateBranch resolves the branch a gate round works on, once, from the
// ticket's record, its slices (which the round has read) and the --branch flag
// (empty when not given):
//
//   - no flag, or the flag names the branch the ticket recorded: the
//     recorded branch, or "jig/<ticket>" when none is recorded;
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
		gb := gateBranch{Name: name, Adopted: adopted}
		if adopted {
			gb.Built = built
		}
		return gb, nil
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
//     version journals it before it marks the slice green, so a run that
//     stopped between the two writes has built a commit no slice shows yet;
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

// chooseBuiltCopy points the gate lease at the copy of an adopted branch a
// round reviews once jig has built on it: the copy that holds the commits jig
// built. Gate has just acquired leaseDir on the branch, at origin's copy.
//
// jig's commits stay in the build lease until someone pushes them, so which
// copy holds them all depends on where they are now, and is judged the way
// the pool's sync rule judges a lease against origin (pool.Compare), on the
// build lease's copy against origin's:
//
//   - the build lease's copy is InStep with origin's, or Behind it: origin
//     holds every commit the lease does, so it holds the ones jig built - the
//     human pushed them, as the publish refusal says to - and whatever the
//     author added since. The round reviews origin's copy;
//   - it is Ahead: jig's commits are not on origin yet, and origin has
//     nothing the lease lacks. The round reviews the lease's copy;
//   - the two have Diverged, and the build lease's copy holds a commit jig
//     built that origin lacks: neither copy holds both that commit and the
//     author's, and a round over either would say nothing of the other's. The
//     round is refused with BRANCH_DIVERGED, as the next build is; jig merges
//     nothing that is not its own;
//   - the two have Diverged, and the build lease's copy holds no commit jig
//     built that origin lacks (pool.HoldsUnpushedBuilt, the rule the build's
//     re-cut applies to the same lease): what it holds of its own is an
//     attempt's leftovers, the history an author's rewrite replaced, or
//     leftovers on top of jig's commits that origin already has, so it counts
//     as a build lease that does not hold the branch. Origin's copy holds
//     whatever of jig's has been pushed, and the check below says whether it
//     holds all of it;
//   - this machine's build lease does not hold the branch: origin's copy.
//
// Whichever copy that is must then hold every commit the journal records jig
// built (pool.RequireBuilt), or the round is refused (BUILD_LEASE_MISSING):
// they are on another machine, or lost, and reviewing less than the ticket
// built would say nothing of it. It is one rule for every copy, not a rule for
// the case of no lease: a build lease cut afresh on this machine, or one that
// built its own commits without the ones another machine built, lacks them
// too. The frontier applies the same rule before it builds.
func chooseBuiltCopy(d Deps, leaseDir, repoName, ticket, branch string, built []string) error {
	buildDir, err := pool.Dir(d.Home, repoName, ticket, pool.Build)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: resolve build lease: %w", err)
	}
	if err := pointAtBuiltCopy(d, leaseDir, buildDir, repoName, ticket, branch, built); err != nil {
		return err
	}
	return pool.RequireBuilt(leaseDir, "HEAD", buildDir, ticket, branch, "jig gate "+ticket, built)
}

// pointAtBuiltCopy is chooseBuiltCopy's choice between the build lease's copy
// of the branch and origin's; built is the commits the journal records jig
// built.
func pointAtBuiltCopy(d Deps, leaseDir, buildDir, repoName, ticket, branch string, built []string) error {
	origin := func() error {
		if err := resetLeasePristine(leaseDir, "origin/"+branch); err != nil {
			return fmt.Errorf("verifydeliver: gate: restore lease to origin/%s: %w", branch, err)
		}
		return nil
	}
	if !pool.Usable(buildDir) {
		return origin()
	}
	if _, err := gitx.RevParse(buildDir, "refs/heads/"+branch); err != nil {
		return origin()
	}
	if err := fetchTicketBranchFromBuildLease(d.Home, leaseDir, repoName, ticket, branch); err != nil {
		return err
	}
	standing, err := pool.Compare(leaseDir, branch)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: compare the build lease's %s with origin's: %w", branch, err)
	}
	switch standing {
	case pool.InStep, pool.Behind:
		return origin()
	case pool.Ahead:
		if err := resetLeasePristine(leaseDir, "HEAD"); err != nil {
			return fmt.Errorf("verifydeliver: gate: restore lease before oracles: %w", err)
		}
		return nil
	default:
		// The lease's copy was just fetched into the gate lease as its own
		// branch, so it is there that it is asked what it holds.
		unpushed, err := pool.HoldsUnpushedBuilt(leaseDir, branch, built)
		if err != nil {
			return fmt.Errorf("verifydeliver: gate: judge the build lease's copy of %s: %w", branch, err)
		}
		if !unpushed {
			return origin()
		}
		return pool.DivergedError(leaseDir, pool.Build, buildDir, branch)
	}
}

// PublishByHand says what to do in place of `jig publish` for an adopted
// branch, which publish cannot ship yet: the pull request is the human's to
// open, from a branch that holds everything the ticket reviewed. When jig
// built commits on the branch they wait in the build lease, so origin's copy
// of the branch lacks the fix a clean round reviewed until someone pushes
// them; leaseDir, when known, names that lease. That push is a fast-forward
// only while the branch has not moved since, so the sentence says to merge the
// branch into the lease first when it has (the gate's BRANCH_DIVERGED refusal
// gives the commands), and needs no read of the branch to say it. The one
// sentence `jig status`, the gate's hint and publish's own refusal all say, so
// they cannot disagree about it. It is kept short: `jig status` prints it
// after a clause of its own, and the demo's terminal is 160 columns wide.
func PublishByHand(branch string, built bool, leaseDir string) string {
	if !built {
		return fmt.Sprintf("open the pull request for %s yourself", branch)
	}
	where := "the build lease"
	if leaseDir != "" {
		where += " at " + leaseDir
	}
	return fmt.Sprintf("push %s to %s (merge it in if it moved), then open the pull request", where, branch)
}
