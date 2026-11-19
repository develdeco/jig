package verifydeliver

import (
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
)

// The functions below are spellings the tests use of what a round does over
// the files of one repo: product code reaches the same work through applyRound,
// OutstandingFindings and resolveScope, with the round's own files.

// ApplyRound is applyRound over the files of one repo with manifest man.
func ApplyRound(round int, known map[string]Finding, result ReviewResult, existingSlices []store.Slice, sliceGreen func(sliceID string) (bool, error), man manifest.Manifest, fixRisks []string, budgetReached bool) (reported []Finding, err error) {
	return applyRound(round, known, result, existingSlices, sliceGreen, oneRepoFiles(man), fixRisks, budgetReached)
}

// oneRepoFiles is the files of a round over one repo with manifest man, whose
// files are paths in the repo.
func oneRepoFiles(man manifest.Manifest) roundFiles {
	return roundFiles{repos: []roundRepo{{Man: man}}}
}

// OutstandingAsks is the asks of OutstandingFindings.
func OutstandingAsks(st *store.Store, ticket string) (asks []Finding, unreadable []int, err error) {
	asks, _, unreadable, err = OutstandingFindings(st, ticket)
	return asks, unreadable, err
}

// resolveScopeBase is resolveScope's scope and base over origin/<target>.
func resolveScopeBase(st *store.Store, ticket, cloneDir, repoName, target string, round int, head string) (scope, base string, err error) {
	scope, base, _, _, _, err = resolveScope(st, ticket, cloneDir, repoName, "origin/"+target, target, round, head)
	return scope, base, err
}
