package pool

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// HoldsUnpushedBuilt reports whether the local copy of branch in checkDir
// (refs/heads/<branch>) holds a commit jig built on it (built, as for
// RequireBuilt) that origin's copy (refs/remotes/origin/<branch>) lacks:
// whether the copy holds jig's work that exists nowhere else. The one rule for
// it, wherever a diverged copy of an adopted branch is judged: the build
// re-cuts a lease that does not (RecutUnlessBuilt), and the gate takes such a
// lease for one that does not hold the branch (verifydeliver's choice of
// copy). A diverged copy with no such commit has, of its own, only what origin
// can replace: the history an author's rewrite replaced, an attempt's
// leftovers, or leftovers on top of jig's commits that origin already has, and
// the branch is still origin's to follow. A copy with one holds work that
// nothing here may reset, re-cut or merge over, and neither copy holds both it
// and what the author added. With nothing built, no copy holds any. A commit
// this repository has never seen is not held (gitx.Missing). Both refs must
// exist.
func HoldsUnpushedBuilt(checkDir, branch string, built []string) (bool, error) {
	notInCopy, err := gitx.Missing(checkDir, "refs/heads/"+branch, built)
	if err != nil {
		return false, fmt.Errorf("pool: does %s hold the commits jig built: %w", branch, err)
	}
	notOnOrigin, err := gitx.Missing(checkDir, "refs/remotes/origin/"+branch, built)
	if err != nil {
		return false, fmt.Errorf("pool: does origin/%s hold the commits jig built: %w", branch, err)
	}
	absent := make(map[string]bool, len(notInCopy))
	for _, commit := range notInCopy {
		absent[commit] = true
	}
	for _, commit := range notOnOrigin {
		if !absent[commit] {
			return true, nil
		}
	}
	return false, nil
}

// RequireBuilt refuses, with BUILD_LEASE_MISSING, a copy of a branch that lacks
// a commit jig built on it. built is the commits the journal records jig built
// and verified on the ticket's branch (journal.BuiltCommits), and ref is the
// copy in checkDir that a command is about to review or build on: the gate
// lease's HEAD, or the build lease's own branch.
//
// jig's commits stay in the build lease of the machine that built them until
// someone pushes them, so a copy that lacks one is a copy that would drop it
// without saying so: a review of it says clean over less than the ticket built,
// and a build on it puts the next commits on a branch that lacks the earlier
// ones. The predicate is the same wherever the copy came from, whether origin's
// or a lease's, cut afresh or kept: it holds every commit or the command
// refuses. A commit this repository has never seen counts as lacking
// (gitx.Missing). The commits are found by id, so a human who rewrites them - a
// rebase, a squash - hides them from jig, and the refusal says to integrate
// with a merge instead. buildDir is this machine's build lease, named in the
// message; command is the command the help names for the machine that built
// them.
func RequireBuilt(checkDir, ref, buildDir, ticket, branch, command string, built []string) error {
	missing, err := gitx.Missing(checkDir, ref, built)
	if err != nil {
		return fmt.Errorf("pool: does %s hold the commits jig built: %w", branch, err)
	}
	if len(missing) == 0 {
		return nil
	}
	short := make([]string, len(missing))
	for i, commit := range missing {
		short[i] = commit
		if len(commit) > 7 {
			short[i] = commit[:7]
		}
	}
	return &axi.Error{
		Msg: fmt.Sprintf("%d of the %d commit(s) the journal records jig built on branch %s are on neither origin nor this machine's build lease at %s: %s",
			len(missing), len(built), branch, buildDir, strings.Join(short, " ")),
		Code: "BUILD_LEASE_MISSING",
		Help: []string{
			fmt.Sprintf("Run `%s` on the machine that built them: they stay in its build lease until pushed", command),
			fmt.Sprintf("Or push them to %s from there, and this machine follows origin's copy (if %s moved meanwhile, integrate it in that lease first)", branch, branch),
			"jig finds them by commit id, so rebasing or squashing them hides them from it: integrate with a merge instead",
		},
	}
}
