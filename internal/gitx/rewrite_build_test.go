package gitx

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// The rewrite tests build their histories in process, as CONTRIBUTING asks
// (only the code under test runs the git program): each repository is one
// git fast-import (a stream that names every commit's files in full) and one
// git reset that writes its index, a bare remote is a copy of the files, and
// what a test reads back is read with go-git. The git program stays where a
// test asks git itself: fsck of the result, a linked work tree.

// hcommit is a commit of a test history. Its subject names it, so a later
// commit names its parents by subject.
type hcommit struct {
	branch  string
	msg     string
	parents []string // subjects; the first is the branch's own line
	files   map[string]string
}

// hist is a built history: the id of each commit by subject.
type hist map[string]string

const histIdent = "pinned <pinned@example.invalid> 1767225600 +0000"

// buildHist writes the repository at dir (a work tree) with the commits in
// the order given, HEAD on head, the files of head's tip checked out and, unless
// noIndex, in the index (a test that reads no index skips the git process that
// writes it). Two calls with the same commits make the same ids.
func buildHist(t *testing.T, dir, head string, commits []hcommit, noIndex ...bool) hist {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	for _, d := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(gitDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFileT(t, gitDir, "HEAD", "ref: refs/heads/"+head+"\n")
	writeFileT(t, gitDir, "config", "[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tautocrlf = false\n")

	var b strings.Builder
	marks := map[string]int{}
	blobs := map[string]int{}
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
		fmt.Fprintf(&b, "commit refs/heads/%s\nmark :%d\nauthor %s\ncommitter %s\ndata %d\n%s\n", c.branch, next, histIdent, histIdent, len(msg), msg)
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
	if _, err := RunInputEnv(dir, append(append([]string{}, rewriteEnv...), rewritePinned...), []byte(b.String()), "fast-import", "--quiet", "--export-marks="+marksFile); err != nil {
		t.Fatalf("fast-import: %v", err)
	}
	raw, err := os.ReadFile(marksFile)
	if err != nil {
		t.Fatal(err)
	}
	byMark := map[int]string{}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, l := range lines {
		m, sha, _ := strings.Cut(l, " ")
		n, _ := strconv.Atoi(strings.TrimPrefix(m, ":"))
		byMark[n] = strings.TrimSpace(sha)
	}
	h := hist{}
	for msg, n := range marks {
		h[msg] = byMark[n]
	}

	// The tip's files on disk and in the index.
	var tip *hcommit
	for i := range commits {
		if commits[i].branch == head {
			tip = &commits[i]
		}
	}
	for name, body := range tip.files {
		writeFileT(t, dir, name, body)
	}
	if len(noIndex) == 0 || !noIndex[0] {
		if _, err := RunEnv(dir, rewriteEnv, "reset", "-q"); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	// What git would have logged for the tip's branch and HEAD.
	reflog := "0000000000000000000000000000000000000000 " + h[tip.msg] + " " + histIdent + "\tcommit: " + tip.msg + "\n"
	writeFileT(t, gitDir, "logs/HEAD", reflog)
	writeFileT(t, gitDir, "logs/refs/heads/"+head, reflog)
	return h
}

// bareOf makes a bare remote of the work tree repository: a copy of its git
// directory (objects, refs packed as a clone leaves them), and origin in the
// work tree pointing at it with its branches fetched. It returns the bare
// directory.
func bareOf(t *testing.T, work string) string {
	t.Helper()
	gitDir := filepath.Join(work, ".git")
	bare := filepath.Join(t.TempDir(), "remote.git")
	objects := filepath.Join(gitDir, "objects")
	err := filepath.WalkDir(objects, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(objects, p)
		if err != nil {
			return err
		}
		target := filepath.Join(bare, "objects", rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"refs/heads", "refs/tags"} {
		if err := os.MkdirAll(filepath.Join(bare, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	heads := refsOf(t, work, "refs/heads/")
	var names []string
	for name := range heads {
		names = append(names, name)
	}
	sort.Strings(names)
	packed := "# pack-refs with: peeled fully-peeled sorted \n"
	for _, name := range names {
		packed += heads[name] + " refs/heads/" + name + "\n"
	}
	writeFileT(t, bare, "packed-refs", packed)
	writeFileT(t, bare, "HEAD", readFileT(t, gitDir, "HEAD"))
	writeFileT(t, bare, "config", "[core]\n\trepositoryformatversion = 0\n\tbare = true\n")
	cfg := readFileT(t, gitDir, "config") + "[remote \"origin\"]\n\turl = " + filepath.ToSlash(bare) + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	writeFileT(t, gitDir, "config", cfg)
	for _, name := range names {
		writeFileT(t, gitDir, "refs/remotes/origin/"+name, heads[name]+"\n")
	}
	writeFileT(t, gitDir, "refs/remotes/origin/HEAD", "ref: refs/remotes/origin/main\n")
	return bare
}

// refsOf is the refs of the repository at dir (a work tree or a bare
// repository) under prefix, by name less the prefix.
func refsOf(t *testing.T, dir, prefix string) map[string]string {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if !exists(gitDir) {
		gitDir = dir
	}
	repo, done, err := openStorage(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	iter, err := repo.Storer.IterReferences()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	_ = iter.ForEach(func(r *plumbing.Reference) error {
		name := r.Name().String()
		if strings.HasPrefix(name, prefix) {
			if r.Type() == plumbing.HashReference {
				out[strings.TrimPrefix(name, prefix)] = r.Hash().String()
			} else {
				out[strings.TrimPrefix(name, prefix)] = "-> " + r.Target().String()
			}
		}
		return nil
	})
	return out
}

// repoState is what a test compares between a rewritten repository and one
// built with the final files: its refs, its objects, the entries of its index
// and its reflogs.
type repoState struct {
	refs    map[string]string
	objects []string
	index   []string
	logs    map[string]string
}

func stateOf(t *testing.T, dir string) repoState {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if !exists(gitDir) {
		gitDir = dir
	}
	repo, done, err := openStorage(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	st := repoState{refs: refsOf(t, dir, "refs/"), logs: map[string]string{}}
	iter, err := repo.Storer.IterEncodedObjects(plumbing.AnyObject)
	if err != nil {
		t.Fatal(err)
	}
	_ = iter.ForEach(func(o plumbing.EncodedObject) error {
		st.objects = append(st.objects, o.Hash().String())
		return nil
	})
	sort.Strings(st.objects)
	idx, _, err := readIndex(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx.Entries {
		st.index = append(st.index, fmt.Sprintf("%v %s %s", e.Mode, e.Hash, e.Name))
	}
	_ = filepath.WalkDir(filepath.Join(gitDir, "logs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, _ := filepath.Rel(gitDir, p)
			data, _ := os.ReadFile(p)
			st.logs[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	if p, err := os.ReadFile(filepath.Join(gitDir, "packed-refs")); err == nil {
		st.logs["packed-refs"] = string(p)
	}
	return st
}

// sameState fails t for each way the repository at got differs from the one
// at want.
func sameState(t *testing.T, what, got, want string) {
	t.Helper()
	g, w := stateOf(t, got), stateOf(t, want)
	if fmt.Sprint(g.refs) != fmt.Sprint(w.refs) {
		t.Errorf("%s: refs\n%v\nwant\n%v", what, g.refs, w.refs)
	}
	if strings.Join(g.objects, "\n") != strings.Join(w.objects, "\n") {
		t.Errorf("%s: objects\n%v\nwant\n%v", what, g.objects, w.objects)
	}
	if strings.Join(g.index, "\n") != strings.Join(w.index, "\n") {
		t.Errorf("%s: index\n%v\nwant\n%v", what, g.index, w.index)
	}
	if fmt.Sprint(g.logs) != fmt.Sprint(w.logs) {
		t.Errorf("%s: reflogs and packed-refs\n%v\nwant\n%v", what, g.logs, w.logs)
	}
}

// hasObject reports whether the repository at dir holds the object id.
func hasObject(t *testing.T, dir, id string) bool {
	t.Helper()
	for _, o := range stateOf(t, dir).objects {
		if o == id {
			return true
		}
	}
	return false
}

// fsck fails t unless git finds the repository at dir consistent.
func fsck(t *testing.T, dir string) {
	t.Helper()
	if _, err := RunEnv(dir, rewriteEnv, "fsck", "--strict", "--no-dangling"); err != nil {
		t.Fatalf("fsck %s: %v", dir, err)
	}
}

// blobAt is the content of path in the commit rev of the repository at dir.
func blobAt(t *testing.T, dir, rev, path string) string {
	t.Helper()
	data, found, err := BlobAt(dir, rev, path)
	if err != nil || !found {
		t.Fatalf("%s:%s: found %v, %v", rev, path, found, err)
	}
	return string(data)
}
