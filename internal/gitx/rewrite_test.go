package gitx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// rewritePinned is the identity and date every commit of these tests is made
// with, so two repositories given the same files and message make the same
// commit.
var rewritePinned = []string{
	"GIT_AUTHOR_NAME=pinned", "GIT_AUTHOR_EMAIL=pinned@example.invalid", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_NAME=pinned", "GIT_COMMITTER_EMAIL=pinned@example.invalid", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// rewriteEnv is the hermetic environment the git program runs under here
// besides rewritePinned: no host config, so a developer's own signing or
// line-ending settings cannot reach a commit.
var rewriteEnv = []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}

// pinnedGit runs the git program in dir with the pinned identity and returns
// its output.
func pinnedGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := RunEnv(dir, append(append([]string{}, rewriteEnv...), rewritePinned...), args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}

// newRewriteRepo makes a repository in a fresh directory with one commit of
// files (name to body) on main, and returns its directory.
func newRewriteRepo(t *testing.T, files map[string]string, msg string) string {
	t.Helper()
	dir := t.TempDir()
	pinnedGit(t, dir, "init", "-q", "-b", "main")
	pinnedGit(t, dir, "config", "core.autocrlf", "false")
	for name, body := range files {
		writeFileT(t, dir, name, body)
	}
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", msg)
	return dir
}

// bareCloneOf clones work into a bare repository the way a fixture makes its
// remote, and points work's origin at it.
func bareCloneOf(t *testing.T, work string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "remote.git")
	pinnedGit(t, "", "clone", "-q", "--bare", work, bare)
	pinnedGit(t, work, "remote", "add", "origin", bare)
	return bare
}

// objectIDs lists every object of the repository at dir (a work tree or a
// bare repository), sorted.
func objectIDs(t *testing.T, dir string) []string {
	t.Helper()
	var ids []string
	for _, line := range strings.Split(pinnedGit(t, dir, "cat-file", "--batch-all-objects", "--batch-check"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	sort.Strings(ids)
	return ids
}

// withoutTree is the commit rev as `git cat-file -p` prints it, less the tree
// line: the part of a commit a rewrite keeps.
func withoutTree(t *testing.T, dir, rev string) string {
	t.Helper()
	var keep []string
	for _, line := range strings.Split(pinnedGit(t, dir, "cat-file", "-p", rev), "\n") {
		if !strings.HasPrefix(line, "tree ") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// mustBeSound fails t unless git finds the repository at dir consistent.
func mustBeSound(t *testing.T, dir string) {
	t.Helper()
	pinnedGit(t, dir, "fsck", "--strict", "--no-dangling")
}

// objectExists reports whether the repository at dir has the object id.
func objectExists(dir, id string) bool {
	_, err := RunEnv(dir, rewriteEnv, "cat-file", "-e", id)
	return err == nil
}

// TestRewriteHeadCommitIsTheCommitAFreshRepositoryMakes: the files of a
// repository's one commit change (a path inside them moves), the commit is
// rewritten, and what comes out is the commit a repository made from the
// final files would have made, with the same id, objects, refs, reflog and
// index, in the work tree and in its bare remote.
func TestRewriteHeadCommitIsTheCommitAFreshRepositoryMakes(t *testing.T) {
	before := map[string]string{
		"a.txt":            "path: /old/root\n",
		"dir/b.txt":        "path: /old/root/b\n",
		"dir/sub/c.txt":    "also /old/root here\n",
		"dir/sub/keep.txt": "not about any path\n",
		"top/only.txt":     "nothing\n",
	}
	after := map[string]string{}
	for name, body := range before {
		after[name] = strings.ReplaceAll(body, "/old/root", "/new/place/longer")
	}
	const msg = "fixture: initial"

	work := newRewriteRepo(t, before, msg)
	bare := bareCloneOf(t, work)
	oldID := pinnedGit(t, work, "rev-parse", "HEAD")
	for name, body := range after {
		writeFileT(t, work, name, body)
	}

	fresh := newRewriteRepo(t, after, msg)
	wantID := pinnedGit(t, fresh, "rev-parse", "HEAD")
	if wantID == oldID {
		t.Fatalf("the files did not change the commit")
	}

	paths := []string{"a.txt", "dir/b.txt", "dir/sub/c.txt", "dir/sub/keep.txt", "untracked.txt", ".git/config", "dir/sub/missing.txt"}
	writeFileT(t, work, "untracked.txt", "/old/root\n")
	if err := RewriteHeadCommit(work, paths, bare); err != nil {
		t.Fatal(err)
	}

	if got := pinnedGit(t, work, "rev-parse", "HEAD"); got != wantID {
		t.Errorf("work HEAD = %s, want the fresh commit %s", got, wantID)
	}
	if got := pinnedGit(t, bare, "rev-parse", "refs/heads/main"); got != wantID {
		t.Errorf("remote main = %s, want %s", got, wantID)
	}
	if got := pinnedGit(t, work, "reflog", "--format=%H", "HEAD"); got != wantID {
		t.Errorf("reflog of HEAD = %q, want only %s", got, wantID)
	}
	if got := pinnedGit(t, work, "reflog", "--format=%H", "refs/heads/main"); got != wantID {
		t.Errorf("reflog of main = %q, want only %s", got, wantID)
	}
	if packed := readFileT(t, bare, "packed-refs"); strings.Contains(packed, oldID) || !strings.Contains(packed, wantID+" refs/heads/main") {
		t.Errorf("packed-refs of the remote = %q, want main at %s and no %s", packed, wantID, oldID)
	}
	for _, repo := range []string{work, bare} {
		mustBeSound(t, repo)
		if objectExists(repo, oldID) {
			t.Errorf("%s still has the old commit %s", repo, oldID)
		}
	}
	if got, want := strings.Join(objectIDs(t, work), "\n"), strings.Join(objectIDs(t, fresh), "\n"); got != want {
		t.Errorf("objects of the work tree repository differ from a fresh one's:\n%s\nwant:\n%s", got, want)
	}
	if got, want := strings.Join(objectIDs(t, bare), "\n"), strings.Join(objectIDs(t, fresh), "\n"); got != want {
		t.Errorf("objects of the remote differ from a fresh repository's:\n%s\nwant:\n%s", got, want)
	}
	if status := pinnedGit(t, work, "status", "--porcelain", "--untracked-files=no"); status != "" {
		t.Errorf("git status = %q, want clean", status)
	}
	if staged := pinnedGit(t, work, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("the index differs from HEAD in %q", staged)
	}
	if a, b := pinnedGit(t, work, "ls-files", "-s"), pinnedGit(t, fresh, "ls-files", "-s"); a != b {
		t.Errorf("index entries = %q, want %q", a, b)
	}
	if tree := pinnedGit(t, work, "write-tree"); tree != pinnedGit(t, work, "rev-parse", "HEAD^{tree}") {
		t.Errorf("the index writes tree %s, not HEAD's", tree)
	}
	if got := readFileT(t, work, "untracked.txt"); got != "/old/root\n" {
		t.Errorf("an untracked file was touched: %q", got)
	}
}

// TestRewriteHeadCommitKeepsAnObjectTheNewCommitStillUses: a blob the old
// commit had twice, of which one path changes, stays; it is the other path's.
func TestRewriteHeadCommitKeepsAnObjectTheNewCommitStillUses(t *testing.T) {
	work := newRewriteRepo(t, map[string]string{"x.txt": "same\n", "y.txt": "same\n"}, "twin files")
	sameBlob := pinnedGit(t, work, "rev-parse", "HEAD:y.txt")
	writeFileT(t, work, "x.txt", "different\n")
	if err := RewriteHeadCommit(work, []string{"x.txt"}); err != nil {
		t.Fatal(err)
	}
	mustBeSound(t, work)
	if !objectExists(work, sameBlob) {
		t.Errorf("the blob y.txt still has, %s, was removed", sameBlob)
	}
	if got := pinnedGit(t, work, "show", "HEAD:x.txt"); got != "different" {
		t.Errorf("x.txt in HEAD = %q", got)
	}
}

// TestRewriteHeadCommitKeepsTheParents: only the commit HEAD names is
// rewritten; its parent stays as it was and is still its parent.
func TestRewriteHeadCommitKeepsTheParents(t *testing.T) {
	work := newRewriteRepo(t, map[string]string{"a.txt": "one\n", "dir/b.txt": "base\n"}, "first")
	parent := pinnedGit(t, work, "rev-parse", "HEAD")
	writeFileT(t, work, "dir/b.txt", "second /old\n")
	pinnedGit(t, work, "add", "-A")
	pinnedGit(t, work, "commit", "-q", "-m", "second")
	oldID := pinnedGit(t, work, "rev-parse", "HEAD")
	oldRest := withoutTree(t, work, "HEAD")
	writeFileT(t, work, "dir/b.txt", "second /new\n")

	if err := RewriteHeadCommit(work, []string{"dir/b.txt"}); err != nil {
		t.Fatal(err)
	}
	mustBeSound(t, work)
	if got := pinnedGit(t, work, "rev-parse", "HEAD^"); got != parent {
		t.Errorf("parent = %s, want %s", got, parent)
	}
	if got := pinnedGit(t, work, "rev-parse", "HEAD"); got == oldID {
		t.Errorf("HEAD is still %s", oldID)
	}
	if got := withoutTree(t, work, "HEAD"); got != oldRest {
		t.Errorf("commit apart from its tree = %q, want %q", got, oldRest)
	}
	if got := pinnedGit(t, work, "show", "HEAD:dir/b.txt"); got != "second /new" {
		t.Errorf("dir/b.txt = %q", got)
	}
	if got := pinnedGit(t, work, "show", "HEAD^:dir/b.txt"); got != "base" {
		t.Errorf("the parent's dir/b.txt = %q, want it unchanged", got)
	}
	if status := pinnedGit(t, work, "status", "--porcelain"); status != "" {
		t.Errorf("git status = %q, want clean", status)
	}
}

// TestRewriteHeadCommitWithNothingToChangeTouchesNothing: paths the commit
// does not track, tracked paths whose bytes are the commit's, and no paths at
// all leave every file of the repository as it was.
func TestRewriteHeadCommitWithNothingToChangeTouchesNothing(t *testing.T) {
	work := newRewriteRepo(t, map[string]string{"a.txt": "one\n"}, "only")
	bare := bareCloneOf(t, work)
	writeFileT(t, work, "untracked.txt", "x\n")
	snap := func() map[string]string {
		got := map[string]string{}
		for _, root := range []string{work, bare} {
			err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || !d.Type().IsRegular() {
					return err
				}
				b, err := os.ReadFile(p)
				got[p] = string(b)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	before := snap()
	for _, paths := range [][]string{nil, {"a.txt"}, {"untracked.txt", ".git/config", "nope/x.txt"}} {
		if err := RewriteHeadCommit(work, paths, bare); err != nil {
			t.Fatalf("paths %v: %v", paths, err)
		}
	}
	after := snap()
	for p, body := range before {
		if got, ok := after[p]; !ok || got != body {
			t.Errorf("%s changed", p)
		}
	}
	if len(after) != len(before) {
		t.Errorf("the rewrite added or removed files: %d now, %d before", len(after), len(before))
	}
}

// TestRewriteHeadCommitLeavesAHardLinkedTwinAlone: the loose objects of a
// copy are hard links to the original's, as the fixture's copies are, and
// rewriting the copy changes nothing the original reads: not its objects, not
// its refs, not its index.
func TestRewriteHeadCommitLeavesAHardLinkedTwinAlone(t *testing.T) {
	orig := newRewriteRepo(t, map[string]string{"a.txt": "at /orig\n", "d/b.txt": "at /orig too\n"}, "twin")
	origBare := bareCloneOf(t, orig)
	oldID := pinnedGit(t, orig, "rev-parse", "HEAD")

	twin := filepath.Join(t.TempDir(), "twin")
	twinBare := filepath.Join(t.TempDir(), "twin.git")
	linkTree(t, orig, twin)
	linkTree(t, origBare, twinBare)
	writeFileT(t, twin, "a.txt", "at /twin\n")
	writeFileT(t, twin, "d/b.txt", "at /twin too\n")
	if err := RewriteHeadCommit(twin, []string{"a.txt", "d/b.txt"}, twinBare); err != nil {
		t.Fatal(err)
	}

	for _, repo := range []string{twin, twinBare, orig, origBare} {
		mustBeSound(t, repo)
	}
	if got := pinnedGit(t, orig, "rev-parse", "HEAD"); got != oldID {
		t.Errorf("the original's HEAD moved to %s", got)
	}
	if got := pinnedGit(t, origBare, "rev-parse", "main"); got != oldID {
		t.Errorf("the original's remote moved to %s", got)
	}
	if !objectExists(orig, oldID) || !objectExists(origBare, oldID) {
		t.Errorf("the original lost its commit")
	}
	if got := pinnedGit(t, orig, "show", "HEAD:a.txt"); got != "at /orig" {
		t.Errorf("the original's a.txt = %q", got)
	}
	if status := pinnedGit(t, orig, "status", "--porcelain"); status != "" {
		t.Errorf("the original's git status = %q", status)
	}
	if got := pinnedGit(t, twin, "show", "HEAD:a.txt"); got != "at /twin" {
		t.Errorf("the twin's a.txt = %q", got)
	}
	if status := pinnedGit(t, twin, "status", "--porcelain"); status != "" {
		t.Errorf("the twin's git status = %q", status)
	}
}

// linkTree copies the directory src to dst, hard-linking the files under an
// objects directory and copying the rest, as a fixture's copy does.
func linkTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if strings.Contains(filepath.ToSlash(rel), "objects/") {
			if err := os.Link(p, target); err == nil {
				return nil
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRewriteHeadCommitRefusals: what a rewrite cannot do faithfully is an
// error, and says why.
func TestRewriteHeadCommitRefusals(t *testing.T) {
	t.Run("HEAD off a branch", func(t *testing.T) {
		work := newRewriteRepo(t, map[string]string{"a.txt": "one\n"}, "c")
		pinnedGit(t, work, "checkout", "-q", "--detach")
		writeFileT(t, work, "a.txt", "two\n")
		if err := RewriteHeadCommit(work, []string{"a.txt"}); err == nil || !strings.Contains(err.Error(), "not on a branch") {
			t.Errorf("err = %v, want a refusal naming the detached HEAD", err)
		}
	})
	t.Run("a mirror on another commit", func(t *testing.T) {
		work := newRewriteRepo(t, map[string]string{"a.txt": "one\n"}, "c")
		bare := bareCloneOf(t, work)
		writeFileT(t, work, "other.txt", "x\n")
		pinnedGit(t, work, "add", "-A")
		pinnedGit(t, work, "commit", "-q", "-m", "ahead")
		writeFileT(t, work, "a.txt", "two\n")
		if err := RewriteHeadCommit(work, []string{"a.txt"}, bare); err == nil || !strings.Contains(err.Error(), "not the rewritten commit") {
			t.Errorf("err = %v, want a refusal naming the mirror", err)
		}
	})
	t.Run("line endings converted", func(t *testing.T) {
		work := newRewriteRepo(t, map[string]string{"a.txt": "one\n"}, "c")
		pinnedGit(t, work, "config", "core.autocrlf", "true")
		writeFileT(t, work, "a.txt", "two\n")
		if err := RewriteHeadCommit(work, []string{"a.txt"}); err == nil || !strings.Contains(err.Error(), "autocrlf") {
			t.Errorf("err = %v, want a refusal naming core.autocrlf", err)
		}
	})
	t.Run("a directory", func(t *testing.T) {
		work := newRewriteRepo(t, map[string]string{"d/a.txt": "one\n"}, "c")
		if err := RewriteHeadCommit(work, []string{"d"}); err == nil || !strings.Contains(err.Error(), "not a file") {
			t.Errorf("err = %v, want a refusal naming the directory", err)
		}
	})
	t.Run("a tracked file gone from the work tree", func(t *testing.T) {
		work := newRewriteRepo(t, map[string]string{"a.txt": "one\n"}, "c")
		if err := os.Remove(filepath.Join(work, "a.txt")); err != nil {
			t.Fatal(err)
		}
		if err := RewriteHeadCommit(work, []string{"a.txt"}); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want the missing file", err)
		}
	})
	t.Run("a lock held", func(t *testing.T) {
		work := newRewriteRepo(t, map[string]string{"a.txt": "one\n"}, "c")
		writeFileT(t, work, ".git/refs/heads/main.lock", "")
		writeFileT(t, work, "a.txt", "two\n")
		if err := RewriteHeadCommit(work, []string{"a.txt"}); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Errorf("err = %v, want a refusal naming the lock", err)
		}
	})
}
