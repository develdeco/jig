package verify

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/outcome"
)

// verifyGreen checks a claimed-green result against the worktree on disk: the
// commit must exist there, be new (a descendant of startSHA, the ticket's
// recorded fork point - not startSHA itself) and reachable from HEAD, and
// every declared artifact must exist inside the commit's tree (checked with
// `git cat-file -e <commit>:<path>`, never the worktree, since an
// uncommitted scratch file must not satisfy it). Copied from the frontier;
// for a verification the worktree is detached at the commit, so HEAD is the
// commit and no artifact is declared, which leaves the descent from the
// start sha as the check that matters.
func verifyGreen(leaseDir, startSHA string, res outcome.Result) (reason string, ok bool) {
	if res.Commit == "" {
		return "commit missing", false
	}
	if res.Commit == startSHA {
		return "commit is the run's start sha: no new work", false
	}
	if _, err := gitx.Run(leaseDir, "cat-file", "-e", res.Commit+"^{commit}"); err != nil {
		return fmt.Sprintf("commit %s not found in lease", res.Commit), false
	}
	if _, err := gitx.Run(leaseDir, "merge-base", "--is-ancestor", startSHA, res.Commit); err != nil {
		return fmt.Sprintf("commit %s is not a descendant of start sha %s", res.Commit, startSHA), false
	}
	if _, err := gitx.Run(leaseDir, "merge-base", "--is-ancestor", res.Commit, "HEAD"); err != nil {
		return fmt.Sprintf("commit %s is not reachable from HEAD", res.Commit), false
	}
	for _, a := range res.Artifacts {
		path := filepath.ToSlash(a)
		if _, err := gitx.Run(leaseDir, "cat-file", "-e", res.Commit+":"+path); err != nil {
			return fmt.Sprintf("artifact %s missing at commit %s", a, res.Commit), false
		}
	}
	return "", true
}

// cleanHead returns dir's HEAD sha when its working tree is clean (nothing
// modified, staged or untracked), else "": an oracle run is evidence about a
// commit only when the tree it ran on was exactly that commit. Copied from
// the frontier.
func cleanHead(dir string) string {
	status, err := gitx.Run(dir, "status", "--porcelain")
	if err != nil || strings.TrimSpace(status) != "" {
		return ""
	}
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		return ""
	}
	return head
}

// dirtyTree returns dir's uncommitted changes (git status, untracked files
// included) for a message, or "" when there are none or git cannot say.
func dirtyTree(dir string) string {
	status, err := gitx.Run(dir, "status", "--porcelain")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(status)
}

// startSHA is the sha a verified commit must descend from, as far as it can
// be known: the ticket's recorded start sha for the repo (the file `jig claim`
// or a frontier dispatch wrote), else the merge base of the commit and
// origin's target branch in the operator's clone, else "" - a commit whose
// fork point is unknown is not held to a descent. repoFile is the contents of
// the start-sha file, "" when there is none.
func startSHA(clone, sha, repoFile, target string) string {
	if recorded := strings.TrimSpace(repoFile); recorded != "" {
		return recorded
	}
	if target == "" {
		return ""
	}
	base, err := gitx.MergeBase(clone, sha, "refs/remotes/origin/"+target)
	if err != nil {
		return ""
	}
	return base
}
