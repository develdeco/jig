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
	// Built is the commits the journal records jig built on the ticket's
	// branch and verified (journal.BuiltCommits), in journal order; none while
	// an adopted branch is only the author's. They stay in the build lease until
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

// chooseBuiltCopy points a gate or publish lease at the copy of the ticket's
// branch that command works on once jig has built on it: the copy that holds
// the commits jig built. The command (step, "gate" or "publish") has just
// acquired leaseDir on the branch, at origin's copy when origin has one. A gate
// round reviews the copy this chooses and publish ships it, so the two cannot
// disagree about which head is the branch's.
//
// The rule is the branch's, not the kind of ticket's: the ticket's own
// jig/<ticket> stands where an adopted branch does once a publish has pushed
// it, and is judged the same way. jig's commits stay in the build lease until
// someone pushes them - publish does - so which copy holds them all depends on
// where they are now, and is judged the way the pool's sync rule judges a
// lease against origin (pool.Compare), on the build lease's copy against
// origin's:
//
//   - origin has no copy of the branch: the build lease's is the only one. That
//     is the ticket's own jig/<ticket> until a publish pushes it (an adopted
//     branch is on origin by definition), and there is nothing to compare;
//   - the build lease's copy is InStep with origin's, or Behind it: origin
//     holds every commit the lease does, so it holds the ones jig built - an
//     earlier publish pushed them as they are, or the human did - and whatever
//     was added since, by the author or by a publish (the merge of the target,
//     the memorize commit). The command works on origin's copy;
//   - it is Ahead: jig's commits are not on origin yet, and origin has
//     nothing the lease lacks. The command works on the lease's copy;
//   - the two have Diverged, and the build lease's copy holds a commit jig
//     built that origin lacks: neither copy holds both that commit and what
//     origin has that the lease lacks - the author's commit, or the squash a
//     first publish pushed in place of the commits it was made of - and a round
//     or a publish over either would say nothing of the other's. The command is
//     refused with BRANCH_DIVERGED, as the next build is; jig merges nothing
//     that is not its own. Merging origin's branch into the build lease is the
//     way on, and leaves the lease Ahead;
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
// built (pool.RequireBuilt), or the command is refused (BUILD_LEASE_MISSING):
// they are on another machine, or lost, and reviewing or shipping less than
// the ticket built would say nothing of it. It is one rule for every copy, not
// a rule for the case of no lease: a build lease cut afresh on this machine, or
// one that built its own commits without the ones another machine built, lacks
// them too. The frontier applies the same rule before it builds on an adopted
// branch. A branch origin does not have is the build lease's alone, and holds
// what the lease does by definition.
func chooseBuiltCopy(d Deps, leaseDir, repoName, ticket, branch string, built []string, step string) (branchCopy, error) {
	buildDir, err := pool.Dir(d.Home, repoName, ticket, pool.Build)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: %s: resolve build lease: %w", step, err)
	}
	tip, err := originsTip(leaseDir, branch)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: %s: %w", step, err)
	}
	if tip == "" {
		return pointAtBuildLeasesCopy(d, leaseDir, repoName, ticket, branch, step)
	}
	copyOf, err := pointAtBuiltCopy(d, leaseDir, buildDir, repoName, ticket, branch, built, step)
	if err != nil {
		return "", err
	}
	if err := pool.RequireBuilt(leaseDir, "HEAD", buildDir, ticket, branch, "jig "+step+" "+ticket, built); err != nil {
		return "", err
	}
	return copyOf, nil
}

// originsTip is the sha branch has on origin as dir last fetched it, or "" when
// origin has no such branch.
func originsTip(dir, branch string) (string, error) {
	tip, err := gitx.Run(dir, "for-each-ref", "--format=%(objectname)", "refs/remotes/origin/"+branch)
	if err != nil {
		return "", fmt.Errorf("look up origin/%s: %w", branch, err)
	}
	return tip, nil
}

// branchCopy names whose copy of a ticket's branch a gate or publish lease was
// left at: the build lease's, where jig's commits wait until someone pushes
// them, or origin's. What is to be done about a refusal depends on it, since
// only the build lease is jig's to integrate anything in.
type branchCopy string

const (
	originsCopy     branchCopy = "origin's"
	buildLeasesCopy branchCopy = "the build lease's"
)

// pointAtBuildLeasesCopy points leaseDir at the build lease's copy of the
// branch, pristine: the copy of a branch origin does not have.
func pointAtBuildLeasesCopy(d Deps, leaseDir, repoName, ticket, branch, step string) (branchCopy, error) {
	if err := fetchTicketBranchFromBuildLease(d.Home, leaseDir, repoName, ticket, branch); err != nil {
		return "", err
	}
	if err := resetLeasePristine(leaseDir, "HEAD"); err != nil {
		return "", fmt.Errorf("verifydeliver: %s: restore lease before oracles: %w", step, err)
	}
	return buildLeasesCopy, nil
}

// pointAtBuiltCopy is chooseBuiltCopy's choice between the build lease's copy
// of a branch origin has and origin's; built is the commits the journal
// records jig built. It says which it chose.
func pointAtBuiltCopy(d Deps, leaseDir, buildDir, repoName, ticket, branch string, built []string, step string) (branchCopy, error) {
	origin := func() (branchCopy, error) {
		if err := resetLeasePristine(leaseDir, "origin/"+branch); err != nil {
			return "", fmt.Errorf("verifydeliver: %s: restore lease to origin/%s: %w", step, branch, err)
		}
		return originsCopy, nil
	}
	if !pool.Usable(buildDir) {
		return origin()
	}
	if _, err := gitx.RevParse(buildDir, "refs/heads/"+branch); err != nil {
		return origin()
	}
	if err := fetchTicketBranchFromBuildLease(d.Home, leaseDir, repoName, ticket, branch); err != nil {
		return "", err
	}
	standing, err := pool.Compare(leaseDir, branch)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: %s: compare the build lease's %s with origin's: %w", step, branch, err)
	}
	switch standing {
	case pool.InStep, pool.Behind:
		return origin()
	case pool.Ahead:
		if err := resetLeasePristine(leaseDir, "HEAD"); err != nil {
			return "", fmt.Errorf("verifydeliver: %s: restore lease before oracles: %w", step, err)
		}
		return buildLeasesCopy, nil
	default:
		// The lease's copy was just fetched into the gate lease as its own
		// branch, so it is there that it is asked what it holds.
		unpushed, err := pool.HoldsUnpushedBuilt(leaseDir, branch, built)
		if err != nil {
			return "", fmt.Errorf("verifydeliver: %s: judge the build lease's copy of %s: %w", step, branch, err)
		}
		if !unpushed {
			return origin()
		}
		return "", pool.DivergedError(leaseDir, pool.Build, buildDir, branch)
	}
}

// pointAtTicketBranch points a gate or publish lease, just acquired on the
// ticket's branch, at the copy of it that command works on. A gate round
// reviews that copy and publish ships it, and both come here, so they cannot
// disagree about which head is the branch's:
//
//   - an adopted branch jig has built nothing on is the author's: origin's
//     copy, exactly (neither lease ever commits to it);
//   - any other is whichever copy holds jig's commits (chooseBuiltCopy): the
//     build lease's while origin has no copy, as the ticket's own jig/<ticket>
//     does until a publish pushes it, and after that by comparing the two.
//
// The lease is left pristine at that copy: pool.Acquire never resets an
// existing local branch (a deliberate rule so a same-run slice's commits on it
// survive later acquires), so without this a lease an earlier, killed run left
// dirty or ahead would have its leftovers reviewed, or shipped, or on a branch
// that has reached origin would work from the stale local copy instead of
// origin's current tip. built is the commits the journal records jig built on
// the branch; step names the command, "gate" or "publish". It says whose copy
// the lease was left at.
func pointAtTicketBranch(d Deps, leaseDir, repoName, ticket, branch string, adopted bool, built []string, step string) (branchCopy, error) {
	if adopted && len(built) == 0 {
		if err := resetLeasePristine(leaseDir, "origin/"+branch); err != nil {
			return "", fmt.Errorf("verifydeliver: %s: restore lease to origin/%s: %w", step, branch, err)
		}
		return originsCopy, nil
	}
	return chooseBuiltCopy(d, leaseDir, repoName, ticket, branch, built, step)
}
