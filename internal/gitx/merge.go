package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
)

// CodeGitTooOld is the code of the error MergeTree returns for a git that does
// not know `merge-tree --write-tree` (git 2.38 added it).
const CodeGitTooOld = "GIT_TOO_OLD"

// MergeTree is git's own merge of a and b in dir, without touching a work tree
// or an index: `git merge-tree --write-tree --no-messages a b`. tree is the tree
// of the merge. clean says git merged without a conflict. A merge with conflicts
// is not an error: tree is then the tree git wrote, with the conflict markers
// in the files, and clean is false. An object dir lacks (a bad ref) is an
// error. A git older than 2.38, which does not know --write-tree, gives an
// *axi.Error with the code GIT_TOO_OLD. The call writes tree objects into dir's
// object database.
func MergeTree(dir, a, b string) (tree string, clean bool, err error) {
	args := []string{"merge-tree", "--write-tree", "--no-messages", a, b}
	var stdout, stderr bytes.Buffer
	err = run(dir, nil, false, &stdout, &stderr, args)
	return mergeTreeOutcome(args, stdout.String(), stderr.String(), err)
}

// MergeConflicts is the files git reports unmerged when it merges a and b in
// dir without touching a work tree or an index (`git merge-tree --write-tree
// --name-only --no-messages a b`), sorted. A clean merge has none. A call that
// fails (a bad ref, a git older than 2.38) is an error, as for MergeTree.
func MergeConflicts(dir, a, b string) ([]string, error) {
	args := []string{"merge-tree", "--write-tree", "--name-only", "--no-messages", a, b}
	var stdout, stderr bytes.Buffer
	err := run(dir, nil, false, &stdout, &stderr, args)
	_, clean, rerr := mergeTreeOutcome(args, stdout.String(), stderr.String(), err)
	if rerr != nil || clean {
		return nil, rerr
	}
	var files []string
	seen := map[string]bool{}
	for i, l := range strings.Split(strings.ReplaceAll(stdout.String(), "\r\n", "\n"), "\n") {
		if i == 0 {
			continue
		}
		if l == "" {
			break
		}
		if !seen[l] {
			seen[l] = true
			files = append(files, l)
		}
	}
	sort.Strings(files)
	return files, nil
}

// mergeTreeOutcome reads what a `git merge-tree --write-tree` call left: its
// arguments, its two outputs and the error of the run (nil for exit 0).
func mergeTreeOutcome(args []string, stdout, stderr string, err error) (tree string, clean bool, rerr error) {
	first := firstLine(stdout)
	if err == nil {
		if !isObjectID(first) {
			return "", false, fmt.Errorf("git %s: no tree on the first line of %q", strings.Join(args, " "), stdout)
		}
		return first, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch exitErr.ExitCode() {
		case 1:
			// Conflicts leave the tree git wrote on the first line; an unknown
			// object leaves nothing there.
			if isObjectID(first) {
				return first, false, nil
			}
		case 129:
			return "", false, &axi.Error{
				Msg:  fmt.Sprintf("this git cannot compare a merge with its own: %s", firstLine(stderr)),
				Code: CodeGitTooOld,
				Help: []string{"jig needs git 2.38 or later to compare a merge with git's own (`git merge-tree --write-tree`); update git"},
			}
		}
	}
	return "", false, callError(args, stderr, err)
}

// isObjectID says s is a full object id: 40 hex digits, or 64 for a SHA-256
// repository.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// firstLine is the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// LineCommit is one commit of a first-parent line: its id, its tree and its
// parents in order (none for a root commit).
type LineCommit struct {
	SHA, Tree string
	Parents   []string
}

// FirstParentLine is the commits of base..head along first parents, oldest
// first, as `git log --first-parent --reverse` lists them: the line a branch
// grew by, where a merge counts as one commit whose second parent is not walked.
// It is empty when head is base or an ancestor of it.
func FirstParentLine(dir, base, head string) ([]LineCommit, error) {
	out, err := Run(dir, "log", "--first-parent", "--reverse", "--format=%H %T %P", base+".."+head)
	if err != nil {
		return nil, err
	}
	var line []LineCommit
	for _, l := range strings.Split(out, "\n") {
		fields := strings.Fields(l)
		if len(fields) < 2 {
			continue
		}
		line = append(line, LineCommit{SHA: fields[0], Tree: fields[1], Parents: fields[2:]})
	}
	return line, nil
}
