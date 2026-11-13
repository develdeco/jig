package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// The scenario tests build their histories in process, as CONTRIBUTING asks
// (only the code under test runs the git program): each repository is one git
// fast-import, a stream that names every commit's files in full, and what a
// test reads back it reads with gitx's in-process reads. The git program stays
// where a test asks git itself: fsck and status of a copy.

// fcommit is a commit of a test history, named by its subject.
type fcommit struct {
	branch  string
	msg     string
	parents []string // subjects; the first is the branch's own line
	files   map[string]string
}

// fcommitIdent is the author and committer of every commit, the date the
// fixture pins.
const fcommitIdent = "jig-fixture <fixture@example.invalid> 1767225600 +0000"

// importRepo writes the commits into the git directory gitDir (a new
// repository, bare or not) with one git fast-import, HEAD on head, and returns
// the id of each commit by subject.
func importRepo(t *testing.T, gitDir, head string, bare bool, commits []fcommit) map[string]string {
	t.Helper()
	for _, d := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(gitDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeIn(t, gitDir, "HEAD", "ref: refs/heads/"+head+"\n")
	writeIn(t, gitDir, "config", fmt.Sprintf("[core]\n\trepositoryformatversion = 0\n\tbare = %v\n\tautocrlf = false\n", bare))

	var b strings.Builder
	marks, blobs := map[string]int{}, map[string]int{}
	next := 0
	for _, c := range commits {
		names := make([]string, 0, len(c.files))
		for name, body := range c.files {
			if _, ok := blobs[body]; !ok {
				next++
				blobs[body] = next
				fmt.Fprintf(&b, "blob\nmark :%d\ndata %d\n%s\n", next, len(body), body)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		next++
		marks[c.msg] = next
		msg := c.msg + "\n"
		fmt.Fprintf(&b, "commit refs/heads/%s\nmark :%d\nauthor %s\ncommitter %s\ndata %d\n%s\n", c.branch, next, fcommitIdent, fcommitIdent, len(msg), msg)
		for i, p := range c.parents {
			if i == 0 {
				fmt.Fprintf(&b, "from :%d\n", marks[p])
			} else {
				fmt.Fprintf(&b, "merge :%d\n", marks[p])
			}
		}
		b.WriteString("deleteall\n")
		for _, name := range names {
			fmt.Fprintf(&b, "M 100644 :%d %s\n", blobs[c.files[name]], name)
		}
		b.WriteString("\n")
	}
	marksFile := filepath.Join(t.TempDir(), "marks")
	if _, err := gitx.RunInputEnv(gitDir, append(append([]string{}, hermeticGitEnv...), identityEnv...), []byte(b.String()), "fast-import", "--quiet", "--export-marks="+marksFile); err != nil {
		t.Fatalf("fast-import into %s: %v", gitDir, err)
	}
	raw, err := os.ReadFile(marksFile)
	if err != nil {
		t.Fatal(err)
	}
	byMark := map[int]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		m, sha, _ := strings.Cut(l, " ")
		n, _ := strconv.Atoi(strings.TrimPrefix(m, ":"))
		byMark[n] = strings.TrimSpace(sha)
	}
	ids := map[string]string{}
	for msg, n := range marks {
		ids[msg] = byMark[n]
	}
	return ids
}

// revParse is the id rev names in the repository at dir.
func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	id, err := gitx.RevParse(dir, rev)
	if err != nil {
		t.Fatalf("rev-parse %s in %s: %v", rev, dir, err)
	}
	return id
}

// blobIn is the content of path at rev in the repository at dir.
func blobIn(t *testing.T, dir, rev, path string) string {
	t.Helper()
	data, found, err := gitx.BlobAt(dir, rev, path)
	if err != nil || !found {
		t.Fatalf("%s:%s in %s: found %v, %v", rev, path, dir, found, err)
	}
	return string(data)
}

// subjectIn is the subject of the commit rev in the repository at dir.
func subjectIn(t *testing.T, dir, rev string) string {
	t.Helper()
	s, err := gitx.Subject(dir, rev)
	if err != nil {
		t.Fatalf("subject of %s in %s: %v", rev, dir, err)
	}
	return s
}
