package gitx

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// worktreesLockName is the file in a repository's git common directory that
// jig's adds and removals of the repository's worktrees lock (filelock adds
// ".lock"): beside git's own records of the worktrees, and shared by every
// linked worktree of the clone and every process that works in it.
const worktreesLockName = "jig-worktrees"

// worktreesLockWait is how long an add or a removal waits for another
// process's to finish. A holder holds the lock only while git writes or
// deletes records, never for a checkout or a tree's deletion, and the lock is
// let go when its holder exits, so only a holder that is stuck makes the wait
// run out, even behind many others.
const worktreesLockWait = 2 * time.Minute

// errWorktreesBusy is a lock that another process held for the whole wait.
var errWorktreesBusy = errors.New("another process is adding or removing a worktree")

// Worktree is one work tree of a repository, as `git worktree list
// --porcelain` names it.
type Worktree struct {
	// Dir is the work tree's directory, as git prints it (forward slashes,
	// whatever the platform).
	Dir string
	// Branch is the short name of the branch checked out there ("main"); ""
	// for a detached HEAD or a bare repository.
	Branch string
	// Main says it is the repository's own work tree, the first git lists.
	Main bool
	// Prunable says git would prune its record: the directory is gone.
	Prunable bool
}

// Worktrees lists the work trees of repoDir's repository: its own and every
// linked one, each with the branch it holds. It asks the git program once.
func Worktrees(repoDir string) ([]Worktree, error) {
	out, err := Run(repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

// parseWorktrees reads the output of `git worktree list --porcelain`: blocks
// separated by a blank line, each a `worktree <path>` line followed by the
// attributes git has for it (`HEAD`, `branch refs/heads/<name>`, `detached`,
// `bare`, `locked`, `prunable <reason>`). The first block is the repository's
// own work tree. A block without a `worktree` line is skipped.
func parseWorktrees(out string) []Worktree {
	var trees []Worktree
	block := Worktree{}
	found := false
	flush := func() {
		if found {
			block.Main = len(trees) == 0
			trees = append(trees, block)
		}
		block, found = Worktree{}, false
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			block.Dir, found = value, true
		case "branch":
			block.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "prunable":
			block.Prunable = true
		}
	}
	flush()
	return trees
}

// trailerKeyPattern is what a trailer key may be: a token of letters, digits
// and dashes, so it can be written into a --format without changing it.
var trailerKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Trailer returns the value of the commit trailer key (such as Jig-Slice) on
// commit sha in dir, as git interprets trailers: the last paragraph of the
// message, when it is all trailers. The key matches without regard to case.
// With several trailers of that key it returns the last; with none, "". It
// needs git 2.22 for the trailers format.
func Trailer(dir, sha, key string) (string, error) {
	if !trailerKeyPattern.MatchString(key) {
		return "", fmt.Errorf("gitx: trailer key %q must be letters, digits and dashes", key)
	}
	if strings.HasPrefix(sha, "-") {
		return "", fmt.Errorf("gitx: trailer: %q is not a revision", sha)
	}
	out, err := Run(dir, "show", "-s", "--format=%(trailers:key="+key+",valueonly,unfold)", sha)
	if err != nil {
		return "", err
	}
	value := ""
	for _, line := range strings.Split(out, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			value = v
		}
	}
	return value, nil
}
