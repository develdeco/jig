package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
)

// scopeStream is one fast-import stream of two commits on main: a base, and a
// head that adds, modifies, deletes and changes the type of files, among them
// paths with a space and a non-ASCII letter, and a rename (which counts as the old
// path deleted and the new one added).
const scopeStream = `commit refs/heads/main
committer t <t@example.invalid> 1700000000 +0000
data 4
base
M 100644 inline keep.txt
data 4
keep
M 100644 inline mod.txt
data 3
one
M 100644 inline del.txt
data 3
del
M 100644 inline "sp ace.txt"
data 3
del
M 100644 inline "sp ace two.txt"
data 3
del
M 100644 inline tc.txt
data 4
file
M 100644 inline old-name.txt
data 7
renamed

commit refs/heads/main
committer t <t@example.invalid> 1700000100 +0000
data 4
head
M 100644 inline mod.txt
data 3
two
D del.txt
D "sp ace.txt"
D "sp ace two.txt"
M 100644 inline "new sp ace.txt"
data 3
new
M 100644 inline "new ünï.txt"
data 3
new
M 100644 inline added.txt
data 3
new
M 120000 inline tc.txt
data 8
keep.txt
D old-name.txt
M 100644 inline new-name.txt
data 7
renamed

`

func scopeRepo(t *testing.T) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	if _, err := gitx.Run(dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunInput(dir, []byte(scopeStream), "fast-import", "--quiet"); err != nil {
		t.Fatalf("fast-import: %v", err)
	}
	head, err := gitx.Run(dir, "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	base, err = gitx.Run(dir, "rev-parse", "main~1")
	if err != nil {
		t.Fatal(err)
	}
	return dir, base, head
}

// TestComputeScopeDiffSplitsAddsModifiesDeletesAndTypeChanges: whatever way
// the diff is asked of git, the changed files are the added, modified and
// type-changed ones and the deleted are the removed ones (a rename is both),
// each sorted, paths with a space or a non-ASCII letter in them whole.
func TestComputeScopeDiffSplitsAddsModifiesDeletesAndTypeChanges(t *testing.T) {
	t.Parallel()
	dir, base, head := scopeRepo(t)
	diff, err := computeScopeDiff(dir, base, head, nil)
	if err != nil {
		t.Fatalf("computeScopeDiff: %v", err)
	}
	wantChanged := []string{"added.txt", "mod.txt", "new sp ace.txt", "new ünï.txt", "new-name.txt", "tc.txt"}
	wantDeleted := []string{"del.txt", "old-name.txt", "sp ace two.txt", "sp ace.txt"}
	if !equalStrings(diff.Changed, wantChanged) {
		t.Errorf("Changed = %q, want %q", diff.Changed, wantChanged)
	}
	if !equalStrings(diff.Deleted, wantDeleted) {
		t.Errorf("Deleted = %q, want %q", diff.Deleted, wantDeleted)
	}
	if !equalStrings(diff.MustReview, wantChanged) {
		t.Errorf("MustReview = %q, want the changed files", diff.MustReview)
	}
	if strings.Contains(strings.Join(diff.Changed, ""), "\x00") {
		t.Error("a path carries a NUL: the -z output was split wrongly")
	}
}

// TestComputeScopeDiffAsksGitOnce: the changed and the deleted files of a
// round are one diff, not two, when there are no open finding files to look
// up. Not parallel: it counts through the process's trace.
func TestComputeScopeDiffAsksGitOnce(t *testing.T) {
	dir, base, head := scopeRepo(t)
	n := gittest.CountGit(t, func() {
		if _, err := computeScopeDiff(dir, base, head, nil); err != nil {
			t.Error(err)
		}
	})
	if n != 1 {
		t.Errorf("computeScopeDiff started %d git processes, want 1", n)
	}
}
