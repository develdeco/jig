package main

import (
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// reviewWorktreeSuffix follows the ticket's id in the directory of the
// reviewer's worktree, beside the builder's (`<clone>.worktrees/<TICKET>`): the
// builder's worktree holds the ticket's branch, which git lets one worktree
// hold, and the reviewer needs the head, not the branch (ADR 0039).
const reviewWorktreeSuffix = "-review"

// dirExists says dir is a directory.
func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// reviewerWorktreeBlocks is the worktree advice of an open round: for each repo
// that has a clone on this machine, the directory the reviewer's worktree goes
// in (`<clone>.worktrees/<TICKET>-review`) and the git command that makes it
// at the round's head, or moves one an earlier round left there to the head.
// jig makes no worktree: a harness that makes one starts the reviewer in it and
// the reviewer checks the head out inside (skills/review/references). A repo
// with no clone here has no row, and no rows is no table. exists says a
// directory is there.
func reviewerWorktreeBlocks(p verifydeliver.Prepared, clones map[string]string, exists func(string) bool) []string {
	type repoHead struct{ repo, head string }
	heads := []repoHead{{p.Repo, p.Head}}
	if len(p.Repos) > 0 {
		heads = heads[:0]
		for _, r := range p.Repos {
			heads = append(heads, repoHead{r.Repo, r.Head})
		}
	}
	var rows [][]string
	for _, h := range heads {
		clone := clones[h.repo]
		if clone == "" || h.head == "" {
			continue
		}
		clone = filepath.ToSlash(filepath.Clean(clone))
		dir := clone + worktreesDirSuffix + p.Ticket + reviewWorktreeSuffix
		command := "git -C " + gitPath(clone) + " worktree add --detach " + gitPath(dir) + " " + h.head
		if exists(dir) {
			command = "git -C " + gitPath(dir) + " checkout --detach " + h.head
		}
		rows = append(rows, []string{h.repo, clone, dir, command})
	}
	if len(rows) == 0 {
		return nil
	}
	return []string{axi.Table("worktree", []string{"repo", "clone", "dir", "command"}, rows)}
}
