package verifydeliver

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// repoCopy is the copy of the ticket's branch a gate round works on in one
// repo: the mapped clone it is read in, whose copy it is, and the commit it is
// at. Nothing of the clone is changed to make it: its checkout and its local
// branches are where the person left them.
type repoCopy struct {
	// Dir is the repository the round reads: the repo's mapped clone, whose
	// object store holds Head and origin's refs as of the round's fetch.
	Dir  string
	Copy branchCopy
	Head string
}

// chooseCopy decides which copy of the ticket's branch a gate round reviews in
// the repo, in the repo's mapped clone, after fetching origin's refs into it
// (fetchOriginRefs). The rule is the branch's, and one for every ticket:
//
//   - the clone has the branch (a committed ref; whatever is dirty in its
//     working tree is not part of the branch and is never read) and origin has
//     none, or the clone's is ahead of origin's, or the two are one: the
//     clone's;
//   - origin's is ahead of the clone's: origin's, which holds everything the
//     clone's does;
//   - each has commits the other lacks: refused, BRANCH_DIVERGED, because jig
//     merges nothing that is not its own and the person decides how their
//     branch and origin's are joined;
//   - the clone has no copy of it: origin's, as an adopted branch jig built
//     nothing on always is;
//   - no copy anywhere: refused, BRANCH_NOT_FOUND. The branch is not cut from
//     the target, so a round is never made over a branch nobody has.
//
// Whichever copy that is must then hold every commit the journal records jig
// built (built), or the command is refused (BUILT_COMMITS_MISSING): they are in
// a clone on another machine, or lost, and reviewing less than the ticket built
// would say nothing of it. A repo with no clone mapped on this machine is
// refused CLONE_NOT_MAPPED: the work is built in a clone, and there is nothing
// to read or to push from without one. step names the command, "gate" or
// "publish".
func chooseCopy(d Deps, repoName, branch string, built []string, step string) (repoCopy, error) {
	clone := operatorClone(d, repoName)
	if clone == "" {
		return repoCopy{}, &axi.Error{
			Msg:  fmt.Sprintf("repo %s has no clone mapped on this machine, so the %s has no copy of its branch %s to read", repoName, step, branch),
			Code: "CLONE_NOT_MAPPED",
			Help: []string{fmt.Sprintf("Map your clone with `jig map %s=<path to your clone of the repo>`, then rerun", repoName)},
		}
	}
	if err := fetchOriginRefs(clone); err != nil {
		return repoCopy{}, fmt.Errorf("verifydeliver: %s: %w", step, err)
	}
	local, err := branchTipIn(clone, branch)
	if err != nil {
		return repoCopy{}, fmt.Errorf("verifydeliver: %s: %w", step, err)
	}
	origin, err := originsTip(clone, branch)
	if err != nil {
		return repoCopy{}, fmt.Errorf("verifydeliver: %s: %w", step, err)
	}
	var rc repoCopy
	switch {
	case local == "" && origin == "":
		return repoCopy{}, &axi.Error{
			Msg:  fmt.Sprintf("branch %q is neither in your clone nor on origin", branch),
			Code: "BRANCH_NOT_FOUND",
			Help: []string{
				fmt.Sprintf("Commit the work on %s in your clone (a branch's committed state is what the %s reads), or push it, then rerun", branch, step),
			},
		}
	case local == "":
		rc = repoCopy{Dir: clone, Copy: originsCopy, Head: origin}
	case origin == "":
		rc = repoCopy{Dir: clone, Copy: clonesCopy, Head: local}
	default:
		ahead, behind, err := gitx.Standing(clone, local, origin)
		if err != nil {
			return repoCopy{}, fmt.Errorf("verifydeliver: %s: compare the clone's %s with origin's: %w", step, branch, err)
		}
		switch {
		case ahead > 0 && behind > 0:
			return repoCopy{}, &axi.Error{
				Msg: fmt.Sprintf("branch %s has diverged: the clone at %s has %d commit(s) origin/%s lacks, and origin/%s has %d the clone lacks",
					branch, clone, ahead, branch, branch, behind),
				Code: "BRANCH_DIVERGED",
				Help: []string{
					"jig merges, rebases and resets neither side: each has commits the other lacks",
					fmt.Sprintf("Join them in your clone (for example `git -C %s fetch origin && git -C %s merge origin/%s`), then rerun", clone, clone, branch),
				},
			}
		case behind > 0:
			rc = repoCopy{Dir: clone, Copy: originsCopy, Head: origin}
		default:
			rc = repoCopy{Dir: clone, Copy: clonesCopy, Head: local}
		}
	}
	if err := requireBuiltCommits(clone, rc.Head, branch, built, step); err != nil {
		return repoCopy{}, err
	}
	return rc, nil
}

// reviewWorktreeSuffix follows the ticket's id in the directory of the
// reviewer's worktree, which sits beside the builder's (ADR 0039):
// `<clone>.worktrees/<TICKET>-review`. The builder's worktree holds the
// ticket's branch, which git lets one worktree hold, and the reviewer needs the
// head, not the branch.
const reviewWorktreeSuffix = "-review"

// ReviewWorktreeDir is the directory jig advises for the reviewer's worktree of
// a ticket in clone, with forward slashes. jig makes none: the harness does, or
// the reviewer runs the advised command.
func ReviewWorktreeDir(clone, ticket string) string {
	return filepath.ToSlash(filepath.Clean(clone)) + ".worktrees/" + ticket + reviewWorktreeSuffix
}

// reviewerWorktrees are the directories a reviewer of the ticket may read the
// head in, so that a reviewed path it reports as absolute is read as the file
// of the repo it is in: the directory jig advises (ReviewWorktreeDir) and every
// linked worktree of the clone, which is where a harness that makes the
// reviewer's worktree puts it (Claude Code's `isolation: worktree` does, under
// the clone). The clone's own work tree is not one: the reviewer never reads
// there. A clone that cannot list its worktrees has the advised directory
// alone.
func reviewerWorktrees(clone, ticket string) []string {
	dirs := []string{ReviewWorktreeDir(clone, ticket)}
	trees, err := gitx.Worktrees(clone)
	if err != nil {
		return dirs
	}
	for _, w := range trees {
		if !w.Main && !w.Prunable && w.Dir != "" {
			dirs = append(dirs, w.Dir)
		}
	}
	return dirs
}

// lazyWorktrees is reviewerWorktrees of the clone for the ticket, asked of git
// only when a reviewed path needs it (a path that is absolute and not in the
// advised directory) and then once.
func lazyWorktrees(clone, ticket string) func() []string {
	var (
		once sync.Once
		dirs []string
	)
	return func() []string {
		once.Do(func() { dirs = reviewerWorktrees(clone, ticket) })
		return dirs
	}
}

// fetchOriginRefs brings origin's branches into the clone's remote-tracking
// refs (refs/remotes/origin/*) and drops the ones origin no longer has, so a
// branch deleted there is not read from a stale tip. It touches nothing else of
// the clone: no local branch, no checkout, no tag.
func fetchOriginRefs(clone string) error {
	if _, err := gitx.Run(clone, "fetch", "--prune", "--no-tags", "origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return fmt.Errorf("fetch origin into %s: %w", clone, err)
	}
	return nil
}

// requireBuiltCommits refuses, with BUILT_COMMITS_MISSING, a copy of a branch
// that lacks a commit jig built on it: built is the commits the journal records
// jig built and verified on the ticket's branch (journal.BuiltCommits), and rev
// the copy in dir that a command is about to review or ship.
//
// jig's commits stay in the clone of the machine that built them until someone
// pushes them, so a copy that lacks one would drop it without saying so: a
// review of it says clean over less than the ticket built, and a publish ships
// less. The predicate is the same wherever the copy came from. A commit this
// repository has never seen counts as lacking (gitx.Missing). The commits are
// found by id, so a person who rewrites them - a rebase, a squash - hides them
// from jig, and the refusal says to integrate with a merge instead.
func requireBuiltCommits(dir, rev, branch string, built []string, step string) error {
	missing, err := gitx.Missing(dir, rev, built)
	if err != nil {
		return fmt.Errorf("verifydeliver: %s: does %s hold the commits jig built: %w", step, branch, err)
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
		Msg: fmt.Sprintf("%d of the %d commit(s) the journal records jig built on branch %s are on neither origin nor the clone at %s: %s",
			len(missing), len(built), branch, dir, strings.Join(short, " ")),
		Code: "BUILT_COMMITS_MISSING",
		Help: []string{
			fmt.Sprintf("Push them to %s from the machine that built them (they stay in its clone until pushed), then rerun `jig %s`", branch, step),
			"jig finds them by commit id, so rebasing or squashing them hides them from it: integrate with a merge instead",
		},
	}
}
