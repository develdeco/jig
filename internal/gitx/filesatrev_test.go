package gitx

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// filesStream is one commit holding a file, a directory with a file in it, a
// path with a space, and a symlink.
const filesStream = `commit refs/heads/main
committer t <t@example.invalid> 1700000000 +0000
data 4
base
M 100644 inline a.txt
data 2
a
M 100644 inline dir/b.txt
data 2
b
M 100644 inline "sp ace.txt"
data 2
s
M 120000 inline link
data 5
a.txt

`

// filesRepos makes a plain repository holding filesStream, a clone of it
// that borrows its objects (alternates), which gitx leaves to the git
// program, and the commit they hold.
func filesRepos(t *testing.T) (plain, declined, sha string) {
	t.Helper()
	root := t.TempDir()
	plain = filepath.Join(root, "plain")
	declined = filepath.Join(root, "declined")
	if _, err := Run(root, "init", "-q", "-b", "main", plain); err != nil {
		t.Fatal(err)
	}
	if _, err := RunInput(plain, []byte(filesStream), "fast-import", "--quiet"); err != nil {
		t.Fatalf("fast-import: %v", err)
	}
	if _, err := Run(root, "clone", "-q", "--shared", plain, declined); err != nil {
		t.Fatal(err)
	}
	sha, err := Run(plain, "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	return plain, declined, sha
}

// filesAtRevCases are the paths asked of the repository filesStream makes and
// what FileExistsAtRev says of each.
var filesAtRevCases = []struct {
	path string
	want bool
}{
	{"a.txt", true},
	{"dir/b.txt", true},
	{"sp ace.txt", true},
	{"link", true},
	{"dir", false},
	{"dir/", false},
	{"missing.txt", false},
	{"dir/missing.txt", false},
	{"a.txt/under", false},
	{"A.txt", false},
}

// TestFileExistsAtRevAnswersTheSameInADeclinedRepository pins the per-path
// answers a round's open-finding files get, in a repository gitx reads in
// process and in one it leaves to the git program: a blob exists, a
// directory, a missing path and a path under a file do not, and an unknown rev
// is an error.
func TestFileExistsAtRevAnswersTheSameInADeclinedRepository(t *testing.T) {
	plain, declined, sha := filesRepos(t)
	for name, dir := range map[string]string{"plain": plain, "declined": declined} {
		for _, c := range filesAtRevCases {
			got, err := FileExistsAtRev(dir, sha, c.path)
			if err != nil || got != c.want {
				t.Errorf("%s: FileExistsAtRev(%q) = %v, %v; want %v", name, c.path, got, err, c.want)
			}
		}
		if _, err := FileExistsAtRev(dir, "no-such-rev", "a.txt"); err == nil {
			t.Errorf("%s: an unknown rev is not an error", name)
		}
	}
}

// TestFilesAtRevAnswersWhatFileExistsAtRevDoes: per path, in both kinds of
// repository, a repeated path and an unknown rev included.
func TestFilesAtRevAnswersWhatFileExistsAtRevDoes(t *testing.T) {
	plain, declined, sha := filesRepos(t)
	var asked []string
	for _, c := range filesAtRevCases {
		asked = append(asked, c.path)
	}
	asked = append(asked, "a.txt")
	for name, dir := range map[string]string{"plain": plain, "declined": declined} {
		got, err := FilesAtRev(dir, sha, asked)
		if err != nil {
			t.Fatalf("%s: FilesAtRev: %v", name, err)
		}
		for _, c := range filesAtRevCases {
			if got[c.path] != c.want {
				t.Errorf("%s: FilesAtRev(%q) = %v, want %v", name, c.path, got[c.path], c.want)
			}
		}
		if _, err := FilesAtRev(dir, "no-such-rev", asked); err == nil {
			t.Errorf("%s: an unknown rev is not an error", name)
		}
		if got, err := FilesAtRev(dir, "no-such-rev", nil); err != nil || len(got) != 0 {
			t.Errorf("%s: no paths asked of an unknown rev = %v, %v; want nothing", name, got, err)
		}
	}
}

// TestFilesAtRevStartsOneGitProcessInADeclinedRepository: where gitx leaves
// the repository to git, several paths cost one process, and where it reads
// the repository itself they cost none. Not parallel: it counts through the
// process's trace.
func TestFilesAtRevStartsOneGitProcessInADeclinedRepository(t *testing.T) {
	plain, declined, sha := filesRepos(t)
	paths := []string{"a.txt", "dir/b.txt", "sp ace.txt", "missing.txt", "dir"}
	if n := gittest.CountGit(t, func() {
		if _, err := FilesAtRev(declined, sha, paths); err != nil {
			t.Error(err)
		}
	}); n != 1 {
		t.Errorf("a declined repository: %d git processes for %d paths, want 1", n, len(paths))
	}
	if n := gittest.CountGit(t, func() {
		if _, err := FilesAtRev(plain, sha, paths); err != nil {
			t.Error(err)
		}
	}); n != 0 {
		t.Errorf("a plain repository: %d git processes, want 0 (answered in process)", n)
	}
}
