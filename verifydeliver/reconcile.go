package verifydeliver

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/gitx"
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
// conflict: resolving it is new code, so publish defers back to gate.
func conflictErr() error {
	return &axi.Error{
		Msg:  "resolution is new code: run `jig gate`",
		Code: "CONFLICT",
	}
}

// reconcile brings dir's checked-out ticket branch up to date with
// origin/target: a rebase when the branch has never been pushed (keeping
// history linear), or a merge when it has (so pushed history is never
// rewritten). It reports which policy it used.
func reconcile(dir, ticket, target string) (string, error) {
	branch := ticketBranch(ticket)
	out, err := gitx.Run(dir, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: reconcile: ls-remote: %w", err)
	}

	if strings.TrimSpace(out) != "" {
		if _, err := gitx.Run(dir, "merge", "origin/"+target); err != nil {
			_, _ = gitx.Run(dir, "merge", "--abort")
			return "", conflictErr()
		}
		return policyMerge, nil
	}

	if _, err := gitx.Run(dir, "rebase", "origin/"+target); err != nil {
		_, _ = gitx.Run(dir, "rebase", "--abort")
		return "", conflictErr()
	}
	return policyLocalRebase, nil
}

// RebaseOnto rebases branch from oldBase onto newBase in dir: the v0.1
// helper for a stacked-branch base-branch-config policy, unit-tested but
// not yet wired into Publish.
func RebaseOnto(dir, newBase, oldBase, branch string) error {
	if _, err := gitx.Run(dir, "checkout", branch); err != nil {
		return fmt.Errorf("verifydeliver: rebase onto: checkout %s: %w", branch, err)
	}
	if _, err := gitx.Run(dir, "rebase", "--onto", newBase, oldBase, branch); err != nil {
		_, _ = gitx.Run(dir, "rebase", "--abort")
		return fmt.Errorf("verifydeliver: rebase onto: rebase --onto %s %s %s: %w", newBase, oldBase, branch, err)
	}
	return nil
}
