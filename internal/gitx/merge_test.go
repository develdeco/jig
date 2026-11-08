package gitx

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// mergeRepo is a repository with main and a branch "topic" that both went on from
// a common commit: main changed a.txt, topic added b.txt.
func mergeRepo(t *testing.T) string {
	t.Helper()
	dir := newRewriteRepo(t, map[string]string{"a.txt": "one\ntwo\nthree\n"}, "base")
	pinnedGit(t, dir, "switch", "-q", "-c", "topic")
	writeFileT(t, dir, "b.txt", "topic\n")
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", "topic")
	pinnedGit(t, dir, "switch", "-q", "main")
	writeFileT(t, dir, "a.txt", "one\ntwo\nthree\nfour\n")
	pinnedGit(t, dir, "commit", "-q", "-a", "-m", "main")
	return dir
}

// TestMergeTree: a merge without conflicts is clean and its tree is the tree a
// real merge commit gets; one with a conflict is not clean and its tree holds the
// markers; a ref the repository lacks is an error, and so is any other failure;
// a git that does not know --write-tree gives GIT_TOO_OLD.
func TestMergeTree(t *testing.T) {
	t.Parallel()
	dir := mergeRepo(t)

	tree, clean, err := MergeTree(dir, "main", "topic")
	if err != nil || !clean || !isObjectID(tree) {
		t.Fatalf("MergeTree(main, topic) = %q, %v, %v, want a clean tree", tree, clean, err)
	}
	pinnedGit(t, dir, "merge", "-q", "--no-edit", "topic")
	if want := pinnedGit(t, dir, "rev-parse", "HEAD^{tree}"); tree != want {
		t.Errorf("tree = %s, want the tree of the real merge, %s", tree, want)
	}
	if got := pinnedGit(t, dir, "cat-file", "-t", tree); got != "tree" {
		t.Errorf("the tree is a %s, want a tree object in the repository", got)
	}

	// A conflict: both sides change the same line of c.txt.
	pinnedGit(t, dir, "switch", "-q", "-c", "left")
	writeFileT(t, dir, "c.txt", "left\n")
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", "left")
	pinnedGit(t, dir, "switch", "-q", "-c", "right", "main")
	writeFileT(t, dir, "c.txt", "right\n")
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", "right")
	// left and right have no common line: add/add conflict on c.txt.
	tree, clean, err = MergeTree(dir, "left", "right")
	if err != nil || clean || !isObjectID(tree) {
		t.Fatalf("MergeTree(left, right) = %q, %v, %v, want a conflicted merge with its tree", tree, clean, err)
	}
	if body := pinnedGit(t, dir, "cat-file", "-p", tree+":c.txt"); !strings.Contains(body, "<<<<<<<") || !strings.Contains(body, ">>>>>>>") {
		t.Errorf("c.txt of the conflicted tree = %q, want the conflict markers", body)
	}

	// An unknown object is an error, not a conflict.
	if tree, clean, err = MergeTree(dir, "main", "nosuchref"); err == nil || clean || tree != "" {
		t.Errorf("MergeTree(main, nosuchref) = %q, %v, %v, want an error", tree, clean, err)
	}
	var axiErr *axi.Error
	if errors.As(err, &axiErr) {
		t.Errorf("an unknown ref gave %v, want a plain error, not %s", err, axiErr.Code)
	}
	// A directory that is no repository is an error too.
	if _, _, err = MergeTree(t.TempDir(), "main", "topic"); err == nil {
		t.Errorf("MergeTree outside a repository succeeded")
	}
}

// TestMergeTreeTooOld: exit 129 is how git before 2.38 answers an option it does
// not know (usage), and it is GIT_TOO_OLD with a Help line naming git 2.38; any
// other exit code is a plain error. The 129 is a real one: git's own answer to
// an option merge-tree lacks.
func TestMergeTreeTooOld(t *testing.T) {
	t.Parallel()
	dir := mergeRepo(t)
	args := []string{"merge-tree", "--no-such-option", "main", "topic"}
	var stdout, stderr bytes.Buffer
	err := run(dir, nil, false, &stdout, &stderr, args)
	if err == nil {
		t.Fatal("git merge-tree accepted an option it lacks")
	}
	tree, clean, err := mergeTreeOutcome(args, stdout.String(), stderr.String(), err)
	var axiErr *axi.Error
	if !errors.As(err, &axiErr) || axiErr.Code != "GIT_TOO_OLD" || clean || tree != "" {
		t.Fatalf("outcome = %q, %v, %v, want a GIT_TOO_OLD *axi.Error", tree, clean, err)
	}
	if len(axiErr.Help) != 1 || !strings.Contains(axiErr.Help[0], "2.38") {
		t.Errorf("help = %v, want a line naming git 2.38", axiErr.Help)
	}

	// Exit 0 with nothing on stdout is no tree.
	if _, _, err := mergeTreeOutcome(args, "", "", nil); err == nil {
		t.Errorf("an empty answer was taken for a tree")
	}
}

// TestFirstParentLine: the commits of base..head along first parents, oldest
// first, with their trees and parents; a merge is one commit of the line and the
// commits it brought in are not.
func TestFirstParentLine(t *testing.T) {
	t.Parallel()
	dir := mergeRepo(t)
	base := pinnedGit(t, dir, "rev-parse", "main")
	pinnedGit(t, dir, "merge", "-q", "--no-edit", "topic")
	merge := pinnedGit(t, dir, "rev-parse", "HEAD")
	writeFileT(t, dir, "d.txt", "after\n")
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", "after")
	tip := pinnedGit(t, dir, "rev-parse", "HEAD")

	line, err := FirstParentLine(dir, base, tip)
	if err != nil {
		t.Fatal(err)
	}
	topic := pinnedGit(t, dir, "rev-parse", "topic")
	want := []LineCommit{
		{SHA: merge, Tree: pinnedGit(t, dir, "rev-parse", merge+"^{tree}"), Parents: []string{base, topic}},
		{SHA: tip, Tree: pinnedGit(t, dir, "rev-parse", tip+"^{tree}"), Parents: []string{merge}},
	}
	if !reflect.DeepEqual(line, want) {
		t.Errorf("FirstParentLine = %+v,\nwant %+v", line, want)
	}

	// Nothing between a commit and itself, or its ancestor.
	if line, err = FirstParentLine(dir, tip, tip); err != nil || len(line) != 0 {
		t.Errorf("FirstParentLine(tip, tip) = %+v, %v, want nothing", line, err)
	}
	if line, err = FirstParentLine(dir, tip, base); err != nil || len(line) != 0 {
		t.Errorf("FirstParentLine(tip, base) = %+v, %v, want nothing", line, err)
	}
	// A root commit has no parent.
	root := pinnedGit(t, dir, "rev-list", "--max-parents=0", "main")
	line, err = FirstParentLine(dir, root+"~0", root)
	if err != nil || len(line) != 0 {
		t.Errorf("FirstParentLine(root, root) = %+v, %v", line, err)
	}
	// An object the repository lacks is an error.
	if _, err = FirstParentLine(dir, "nosuchref", tip); err == nil {
		t.Errorf("FirstParentLine from an unknown ref succeeded")
	}
}

// TestMergeConflicts: a clean merge lists no file; a merge that conflicts lists
// the files git reports unmerged and no other file the two sides changed; a
// ref the repository lacks is an error.
func TestMergeConflicts(t *testing.T) {
	t.Parallel()
	dir := mergeRepo(t)
	if files, err := MergeConflicts(dir, "main", "topic"); err != nil || len(files) != 0 {
		t.Fatalf("MergeConflicts(main, topic) = %v, %v, want none", files, err)
	}

	pinnedGit(t, dir, "switch", "-q", "-c", "left", "main")
	writeFileT(t, dir, "a.txt", "one\ntwo\nthree\nfour\nleft\n")
	writeFileT(t, dir, "c.txt", "left\n")
	writeFileT(t, dir, "d.txt", "only left\n")
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", "left")
	pinnedGit(t, dir, "switch", "-q", "-c", "right", "main")
	writeFileT(t, dir, "a.txt", "one\ntwo\nthree\nfour\nright\n")
	writeFileT(t, dir, "c.txt", "right\n")
	writeFileT(t, dir, "e.txt", "only right\n")
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", "right")

	files, err := MergeConflicts(dir, "left", "right")
	if err != nil || !reflect.DeepEqual(files, []string{"a.txt", "c.txt"}) {
		t.Errorf("MergeConflicts(left, right) = %v, %v, want a.txt and c.txt only", files, err)
	}
	if files, err = MergeConflicts(dir, "main", "nosuchref"); err == nil || len(files) != 0 {
		t.Errorf("MergeConflicts(main, nosuchref) = %v, %v, want an error", files, err)
	}
}
