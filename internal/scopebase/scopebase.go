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
func Resolve(st *store.Store, ticket, leaseDir, repoName, target, head string) (string, error) {
	mergeBase, mbErr := gitx.MergeBase(leaseDir, "origin/"+target, head)
	if mbErr == nil {
		return mergeBase, nil
	}

	startPath := st.StartSHAPath(ticket, repoName)
	data, rerr := fileshare.ReadFile(startPath)
	if rerr != nil {
		return "", fmt.Errorf("verifydeliver: review: resolve scope base: merge-base origin/%s failed (%v) and no start sha: %w", target, mbErr, rerr)
	}
	return strings.TrimSpace(string(data)), nil
}
