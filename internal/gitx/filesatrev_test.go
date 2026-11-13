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

// filesRepos is a plain repository holding filesStream, a clone of it that
// borrows its objects (alternates), which gitx leaves to the git program, and
// the commit they hold: built once for the binary, as the tests only read them.
func filesRepos(t *testing.T) (plain, declined, sha string) {
	t.Helper()
	plain = streamRepo(t, "files", filesStream)
	declined = sharedRepo(t, "files-declined", func(t *testing.T, dir string) {
		if _, err := Run(filepath.Dir(dir), "clone", "-q", "--shared", plain, dir); err != nil {
			t.Fatal(err)
		}
	})
	sha = sharedSHA(t, "files", plain)
	return plain, declined, sha
}

var shas = map[string]string{}

// sharedSHA is the commit main names in dir, asked of git once per key.
func sharedSHA(t *testing.T, key, dir string) string {
	t.Helper()
	sharedRepos.Lock()
	defer sharedRepos.Unlock()
	if sha, ok := shas[key]; ok {
		return sha
	}
	sha, err := Run(dir, "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	shas[key] = sha
	return sha
}

// filesAtRevCases are the paths asked of the repository filesStream makes and
// what FileExistsAtRev says of each.
var filesAtRevCases = []struct {
	path string
	want bool
}{
	{"a.txt", true},
	{"sp ace.txt", true},
	{"link", true},
	{"dir", false},
	{"missing.txt", false},
	{"a.txt/under", false},
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
// repository, a repeated path and an unknown rev included; and where gitx
// leaves the repository to git, the paths cost one process, and where it reads
// the repository itself none. Not parallel: it counts through the process's
// trace.
func TestFilesAtRevAnswersWhatFileExistsAtRevDoes(t *testing.T) {
	plain, declined, sha := filesRepos(t)
	var asked []string
	for _, c := range filesAtRevCases {
		asked = append(asked, c.path)
	}
	asked = append(asked, "a.txt")
	for name, dir := range map[string]string{"plain": plain, "declined": declined} {
		var got map[string]bool
		var err error
		n := gittest.CountGit(t, func() { got, err = FilesAtRev(dir, sha, asked) })
		if err != nil {
			t.Fatalf("%s: FilesAtRev: %v", name, err)
		}
		for _, c := range filesAtRevCases {
			if got[c.path] != c.want {
				t.Errorf("%s: FilesAtRev(%q) = %v, want %v", name, c.path, got[c.path], c.want)
			}
		}
		want := 0
		if name == "declined" {
			want = 1
		}
		if n != want {
			t.Errorf("%s: %d git processes for %d paths, want %d", name, n, len(asked), want)
		}
		if _, err := FilesAtRev(dir, "no-such-rev", asked); err == nil {
			t.Errorf("%s: an unknown rev is not an error", name)
		}
		if got, err := FilesAtRev(dir, "no-such-rev", nil); err != nil || len(got) != 0 {
			t.Errorf("%s: no paths asked of an unknown rev = %v, %v; want nothing", name, got, err)
		}
	}
}
