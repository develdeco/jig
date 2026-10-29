package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFixture is a set of repositories built with the git program, to put
// the in-process reads next to it: the main repository (loose and packed
// refs, a ref in both, symbolic refs, annotated and lightweight tags, a tag
// of a tree, a missing object), two linked worktrees of it, a work tree whose
// .git file names a separate git directory, a hand-made submodule-style .git
// file, and repositories with a replace ref, alternates, an unborn branch and
// a conflicted index.
type readFixture struct {
	main    string // work tree, HEAD on main
	sub     string // main/dir/sub, a subdirectory of main
	wt      string // linked worktree of main, on branch wtb, with refs/bisect/bad
	wtd     string // linked worktree of main, HEAD detached at c1
	sep     string // work tree whose .git file names sep.git
	holder  string // directory whose .git file names the git directory of plain
	supr    string // repository with a real submodule at supr/subm
	subm    string // supr/subm: its .git file names a git directory with core.worktree
	unborn  string // fresh repository
	amb     string // branch main and tag main
	conf    string // index with unmerged entries
	repl    string // refs/replace/<c1> exists
	alt     string // clone of main that borrows its objects
	shas    map[string]string
	fakeSHA string // names no object
}

// mustGit runs git in dir and fails the test on error.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

// fixtureIdentity is appended to a repository's config so its commits have an
// author without the machine's.
const fixtureIdentity = "[user]\n\tname = fixture\n\temail = fixture@example.invalid\n"

// initFixtureRepo creates a repository at path with an author configured.
func initFixtureRepo(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	mustGit(t, parent, "init", "-q", "-b", "main", name)
	appendFile(t, filepath.Join(path, ".git", "config"), fixtureIdentity)
	return path
}

// fiData is a fast-import "data" command for s.
func fiData(s string) string { return fmt.Sprintf("data %d\n%s\n", len(s), s) }

const fiIdent = "A <a@example.invalid> 1700000000 +0000"

// readFixtureStream is the fast-import stream of the main repository's
// history: c1 (a.txt, dir/b.txt, dir/sub/c.txt, "sp ace.txt"), c2 (a.txt
// changed, a message of two lines and a body) and c3 (a message with
// trailing white space and CRLF line ends, and a gitlink), on main, with
// feature/x and both at c2 and c1, the lightweight tag light at c1 and the
// annotated tag ann at c2.
func readFixtureStream(fakeSHA string) string {
	var b strings.Builder
	blob := func(mark int, content string) {
		fmt.Fprintf(&b, "blob\nmark :%d\n%s", mark, fiData(content))
	}
	blob(1, "a1")
	blob(2, "b1")
	blob(3, "c1")
	blob(4, "s1")
	blob(5, "a2")
	fmt.Fprintf(&b, "commit refs/heads/main\nmark :10\nauthor %s\ncommitter %s\n%s", fiIdent, fiIdent, fiData("first"))
	b.WriteString("M 100644 :1 a.txt\nM 100644 :2 dir/b.txt\nM 100644 :3 dir/sub/c.txt\nM 100644 :4 sp ace.txt\n\n")
	fmt.Fprintf(&b, "commit refs/heads/main\nmark :11\nauthor %s\ncommitter %s\n%sfrom :10\nM 100644 :5 a.txt\n\n",
		fiIdent, fiIdent, fiData("second\nline two\n\nbody here"))
	fmt.Fprintf(&b, "commit refs/heads/main\nmark :12\nauthor %s\ncommitter %s\n%sfrom :11\nM 160000 %s subm\n\n",
		fiIdent, fiIdent, fiData("third   \r\nline b \t\n\nbody\n"), fakeSHA)
	b.WriteString("reset refs/heads/feature/x\nfrom :11\n\nreset refs/heads/both\nfrom :10\n\nreset refs/tags/light\nfrom :10\n\n")
	fmt.Fprintf(&b, "tag ann\nfrom :11\ntagger %s\n%s", fiIdent, fiData("annotated"))
	return b.String()
}

// buildReadFixture builds the repositories under a fresh temporary directory.
func buildReadFixture(t *testing.T) *readFixture {
	t.Helper()
	root := t.TempDir()
	fx := &readFixture{fakeSHA: "0123456789012345678901234567890123456789", shas: map[string]string{}}

	fx.main = initFixtureRepo(t, root, "main")
	if _, err := RunInput(fx.main, []byte(readFixtureStream(fx.fakeSHA)), "fast-import", "--quiet"); err != nil {
		t.Fatalf("fast-import: %v", err)
	}
	mustGit(t, fx.main, "reset", "-q", "--hard")
	c1 := mustGit(t, fx.main, "rev-parse", "refs/tags/light")
	c2 := mustGit(t, fx.main, "rev-parse", "refs/heads/feature/x")
	c3 := mustGit(t, fx.main, "rev-parse", "refs/heads/main")
	fx.shas["c1"], fx.shas["c2"], fx.shas["c3"] = c1, c2, c3
	fx.shas["ann"] = mustGit(t, fx.main, "rev-parse", "refs/tags/ann")
	fx.shas["tree3"] = mustGit(t, fx.main, "rev-parse", c3+"^{tree}")
	fx.shas["blobA"] = mustGit(t, fx.main, "rev-parse", c3+":a.txt")
	mustGit(t, fx.main, "tag", "-a", "-m", "tag of a tag", "ann2", "ann")
	mustGit(t, fx.main, "tag", "-a", "-m", "tag of a tree", "treetag", fx.shas["tree3"])
	// Every ref so far goes to packed-refs; the ones below are loose after
	// it, one of them over a packed ref of the same name.
	mustGit(t, fx.main, "pack-refs", "--all", "--prune")
	mustGit(t, fx.main, "commit", "-q", "--allow-empty", "-m", "loose")
	fx.shas["c4"] = mustGit(t, fx.main, "rev-parse", "HEAD")
	gitDir := filepath.Join(fx.main, ".git")
	ref := func(name, content string) {
		writeFile(t, filepath.Join(gitDir, filepath.FromSlash(name)), content+"\n")
	}
	ref("refs/heads/both", c2)
	ref("refs/heads/loose-only", c1)
	ref("refs/heads/sym", "ref: refs/heads/main")
	ref("refs/heads/sym2", "ref: refs/heads/sym")
	ref("refs/heads/d5", "ref: refs/heads/d4")
	ref("refs/heads/d4", "ref: refs/heads/d3")
	ref("refs/heads/d3", "ref: refs/heads/d2")
	ref("refs/heads/d2", "ref: refs/heads/d1")
	ref("refs/heads/d1", "ref: refs/heads/main")
	ref("refs/heads/d6", "ref: refs/heads/d5")
	ref("refs/heads/dangling", "ref: refs/heads/nothere")
	ref("refs/heads/broken", fx.fakeSHA)
	ref("refs/heads/garbage", "this is not a ref")
	ref("refs/remotes/origin/main", c3)
	ref("refs/remotes/origin/HEAD", "ref: refs/remotes/origin/main")

	// A commit whose tree is not in the repository.
	orphan := "tree " + fx.fakeSHA + "\nauthor " + fiIdent + "\ncommitter " + fiIdent + "\n\nno tree\n"
	var err error
	if fx.shas["notree"], err = RunInput(fx.main, []byte(orphan), "hash-object", "-t", "commit", "-w", "--stdin"); err != nil {
		t.Fatalf("hash-object: %v", err)
	}

	fx.sub = filepath.Join(fx.main, "dir", "sub")
	fx.wt = filepath.Join(root, "wt")
	mustGit(t, fx.main, "worktree", "add", "-q", fx.wt, "-b", "wtb")
	mustGit(t, fx.wt, "update-ref", "refs/bisect/bad", c1)
	mustGit(t, fx.wt, "commit", "-q", "--allow-empty", "-m", "on wtb")
	fx.shas["wtb"] = mustGit(t, fx.wt, "rev-parse", "HEAD")
	fx.wtd = filepath.Join(root, "wtd")
	mustGit(t, fx.main, "worktree", "add", "-q", "--detach", fx.wtd, c1)

	fx.sep = filepath.Join(root, "sep")
	mustGit(t, root, "init", "-q", "-b", "main", "--separate-git-dir", filepath.Join(root, "sep.git"), "sep")
	appendFile(t, filepath.Join(root, "sep.git", "config"), fixtureIdentity)
	mustGit(t, fx.sep, "commit", "-q", "--allow-empty", "-m", "separate")

	plain := initFixtureRepo(t, root, "plain")
	mustGit(t, plain, "commit", "-q", "--allow-empty", "-m", "plain")
	fx.holder = filepath.Join(root, "holder")
	writeFile(t, filepath.Join(fx.holder, ".git"), "gitdir: "+filepath.ToSlash(filepath.Join(plain, ".git"))+"\n")

	fx.supr = initFixtureRepo(t, root, "supr")
	mustGit(t, fx.supr, "commit", "-q", "--allow-empty", "-m", "super")
	mustGit(t, fx.supr, "-c", "protocol.file.allow=always", "submodule", "add", "-q", plain, "subm")
	fx.subm = filepath.Join(fx.supr, "subm")

	fx.unborn = initFixtureRepo(t, root, "unborn")

	fx.amb = initFixtureRepo(t, root, "amb")
	mustGit(t, fx.amb, "commit", "-q", "--allow-empty", "-m", "amb")
	mustGit(t, fx.amb, "tag", "main")

	fx.conf = initFixtureRepo(t, root, "conf")
	blob, err := RunInput(fx.conf, []byte("x\n"), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	index := fmt.Sprintf("100644 %s 1\tf.txt\n100644 %s 2\tf.txt\n100644 %s 3\tf.txt\n", blob, blob, blob)
	if _, err := RunInput(fx.conf, []byte(index), "update-index", "--index-info"); err != nil {
		t.Fatal(err)
	}

	fx.repl = initFixtureRepo(t, root, "repl")
	mustGit(t, fx.repl, "commit", "-q", "--allow-empty", "-m", "one")
	r1 := mustGit(t, fx.repl, "rev-parse", "HEAD")
	mustGit(t, fx.repl, "commit", "-q", "--allow-empty", "-m", "two")
	mustGit(t, fx.repl, "replace", r1, "HEAD")

	fx.alt = filepath.Join(root, "alt")
	mustGit(t, root, "clone", "-q", "--shared", plain, "alt")
	return fx
}
