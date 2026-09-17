package verifydeliver

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// reconcilePolicy names how a ticket branch was reconciled against its
// target before publish: "local-rebase" when the branch never left this
// machine (rebase keeps history linear), or "merge" when it was already
// pushed to origin (rewriting it would orphan anyone building on it).
const (
	policyLocalRebase = "local-rebase"
	policyMerge       = "merge"
)

// conflictErr is returned whenever a reconcile merge or rebase hits a real
// conflict: resolving it is new code, so publish defers back to gate. cause
// carries git's own stderr (via gitx.RunEnv's wrapped error) so a conflict
// is never indistinguishable from some other git failure, such as a missing
// pinned identity, that also makes merge/rebase exit non-zero.
func conflictErr(cause error) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("resolution is new code: run `jig gate`: %v", cause),
		Code: "CONFLICT",
	}
}

// reconcile brings dir's checked-out ticket branch up to date with
// origin/target: a rebase when the branch has never been pushed (keeping
// history linear), or a merge when it has (so pushed history is never
// rewritten). It reports which policy it used.
//
// Both merge and a rebase's replayed commits create new commits, which git
// refuses without an author/committer identity; every history-creating call
// here runs through gitx.RunEnv with pinnedGitEnv (the same mechanism
// verifydeliver already uses for its own squash/memorize commits) so
// reconcile works on a machine with no global git identity, such as a CI
// runner.
func reconcile(dir, ticket, target string) (string, error) {
	branch := ticketBranch(ticket)
	out, err := gitx.Run(dir, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: reconcile: ls-remote: %w", err)
	}

	if strings.TrimSpace(out) != "" {
		if _, err := gitx.RunEnv(dir, pinnedGitEnv, "merge", "origin/"+target); err != nil {
			_, _ = gitx.RunEnv(dir, pinnedGitEnv, "merge", "--abort")
			return "", conflictErr(err)
		}
		return policyMerge, nil
	}

	if _, err := gitx.RunEnv(dir, pinnedGitEnv, "rebase", "origin/"+target); err != nil {
		_, _ = gitx.RunEnv(dir, pinnedGitEnv, "rebase", "--abort")
		return "", conflictErr(err)
	}
	return policyLocalRebase, nil
}

// RebaseOnto rebases branch from oldBase onto newBase in dir: the v0.1
// helper for a stacked-branch base-branch-config policy, unit-tested but
// not yet wired into Publish. The rebase replays commits, so it runs
// through the same pinned-identity mechanism as reconcile.
func RebaseOnto(dir, newBase, oldBase, branch string) error {
	if _, err := gitx.Run(dir, "checkout", branch); err != nil {
		return fmt.Errorf("verifydeliver: rebase onto: checkout %s: %w", branch, err)
	}
	if _, err := gitx.RunEnv(dir, pinnedGitEnv, "rebase", "--onto", newBase, oldBase, branch); err != nil {
		_, _ = gitx.RunEnv(dir, pinnedGitEnv, "rebase", "--abort")
		return fmt.Errorf("verifydeliver: rebase onto: rebase --onto %s %s %s: %w", newBase, oldBase, branch, err)
	}
	return nil
}
