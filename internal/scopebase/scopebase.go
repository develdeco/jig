// Package scopebase holds the base of a whole change, one rule for the gate
// and for the verifier. It is a leaf below internal/verify and
// internal/verifydeliver, which keep the store on disk as their interface.
package scopebase

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// Resolve is the base of the whole change: merge-base(origin/target, head),
// falling back to the ticket's recorded start sha only when the merge-base
// call itself fails (e.g. no such ref). A full-scope review starts here.
func Resolve(st *store.Store, ticket, cloneDir, repoName, target, head string) (string, error) {
	return ResolveOn(st, ticket, cloneDir, repoName, target, "", head)
}

// BranchFor is the branch whose origin ref a whole change is measured from:
// base (a container's branch, which a task's pull request goes into and whose
// merged work is not the task's change) when the clone holds origin/<base>,
// else target. A base the clone has no copy of reads as the target, as it did
// before the ticket had one.
func BranchFor(cloneDir, target, base string) string {
	if base == "" || base == target || cloneDir == "" {
		return target
	}
	if _, err := gitx.RevParse(cloneDir, "refs/remotes/origin/"+base); err != nil {
		return target
	}
	return base
}

// ResolveOn is Resolve for a ticket whose pull request goes into base (a
// container's branch) rather than the target: the merge base is with
// origin/<BranchFor>.
func ResolveOn(st *store.Store, ticket, cloneDir, repoName, target, base, head string) (string, error) {
	branch := BranchFor(cloneDir, target, base)
	mergeBase, mbErr := gitx.MergeBase(cloneDir, "origin/"+branch, head)
	if mbErr == nil {
		return mergeBase, nil
	}
	target = branch

	startPath := st.StartSHAPath(ticket, repoName)
	data, rerr := fileshare.ReadFile(startPath)
	if rerr != nil {
		return "", fmt.Errorf("verifydeliver: review: resolve scope base: merge-base origin/%s failed (%v) and no start sha: %w", target, mbErr, rerr)
	}
	return strings.TrimSpace(string(data)), nil
}
