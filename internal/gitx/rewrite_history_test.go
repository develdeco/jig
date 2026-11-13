package gitx

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// historyWithRoot builds, with the git program, a history whose first commit
// holds the files that carry root: a second commit on main, a side branch
// from the first, a no-ff merge of the side, and a last commit, all with the
// pinned identity. It returns the work tree and a bare remote cloned from it,
// with origin's branches fetched. Two calls with the same root make the same
// commits.
func historyWithRoot(t *testing.T, root string) (work, bare string) {
	t.Helper()
	work = newRewriteRepo(t, map[string]string{
		"a.txt":         "path: " + root + "\n",
		"dir/b.txt":     "also " + root + "/b here\n",
		"dir/plain.txt": "no path\n",
	}, "first")
	writeFileT(t, work, "second.txt", "second\n")
	pinnedGit(t, work, "add", "-A")
	pinnedGit(t, work, "commit", "-q", "-m", "second")
	pinnedGit(t, work, "checkout", "-q", "-b", "side", "HEAD^")
	writeFileT(t, work, "side.txt", "side\n")
	pinnedGit(t, work, "add", "-A")
	pinnedGit(t, work, "commit", "-q", "-m", "side")
	pinnedGit(t, work, "checkout", "-q", "main")
	pinnedGit(t, work, "merge", "-q", "--no-ff", "-m", "merge side", "side")
	writeFileT(t, work, "last.txt", "last\n")
	pinnedGit(t, work, "add", "-A")
	pinnedGit(t, work, "commit", "-q", "-m", "last")
	bare = bareCloneOf(t, work)
	pinnedGit(t, work, "fetch", "-q", "origin")
	return work, bare
}

// refList is the refs of the repository at dir and what each names.
func refList(t *testing.T, dir string) string {
	t.Helper()
	return pinnedGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)")
}

// TestRewriteHistoryIsTheHistoryAFreshRepositoryMakes: files that carry a
// path sit in the first of several commits, with a merge and a side branch on
// top; once the work tree files hold the final path and the history is
// rewritten, it is the history built with the final path from the start: the
// same commit ids on every ref, the same objects, in the work tree repository
// and in its bare remote, with the reflogs and the index agreeing.
func TestRewriteHistoryIsTheHistoryAFreshRepositoryMakes(t *testing.T) {
	const oldRoot, newRoot = "/old/root", "/new/place/longer"
	paths := []string{"a.txt", "dir/b.txt", "dir/plain.txt", "second.txt"}

	work, bare := historyWithRoot(t, oldRoot)
	oldTip := pinnedGit(t, work, "rev-parse", "HEAD")
	oldFirst := pinnedGit(t, work, "rev-list", "--max-parents=0", "HEAD")
	for _, name := range []string{"a.txt", "dir/b.txt"} {
		writeFileT(t, work, name, strings.ReplaceAll(readFileT(t, work, name), oldRoot, newRoot))
	}
	fresh, freshBare := historyWithRoot(t, newRoot)
	wantTip := pinnedGit(t, fresh, "rev-parse", "HEAD")
	if wantTip == oldTip {
		t.Fatalf("the files did not change the history")
	}

	if err := RewriteHistory(work, paths, bare); err != nil {
		t.Fatal(err)
	}

	for _, repo := range []string{work, bare} {
		mustBeSound(t, repo)
		if objectExists(repo, oldFirst) || objectExists(repo, oldTip) {
			t.Errorf("%s still has a commit of the old history", repo)
		}
	}
	if got, want := refList(t, work), refList(t, fresh); got != want {
		t.Errorf("refs of the work tree repository:\n%s\nwant:\n%s", got, want)
	}
	if got, want := refList(t, bare), refList(t, freshBare); got != want {
		t.Errorf("refs of the remote:\n%s\nwant:\n%s", got, want)
	}
	if got, want := strings.Join(objectIDs(t, work), "\n"), strings.Join(objectIDs(t, fresh), "\n"); got != want {
		t.Errorf("objects of the work tree repository:\n%s\nwant:\n%s", got, want)
	}
	if got, want := strings.Join(objectIDs(t, bare), "\n"), strings.Join(objectIDs(t, freshBare), "\n"); got != want {
		t.Errorf("objects of the remote:\n%s\nwant:\n%s", got, want)
	}
	if got, want := pinnedGit(t, work, "rev-list", "--parents", "--all"), pinnedGit(t, fresh, "rev-list", "--parents", "--all"); got != want {
		t.Errorf("commit graph:\n%s\nwant:\n%s", got, want)
	}
	for _, rev := range []string{"HEAD", "side", "origin/main"} {
		if got, want := withoutTree(t, work, rev), withoutTree(t, fresh, rev); got != want {
			t.Errorf("%s apart from its tree = %q, want %q", rev, got, want)
		}
	}
	for _, ref := range []string{"HEAD", "refs/heads/main", "refs/heads/side"} {
		if got, want := pinnedGit(t, work, "reflog", "--format=%H", ref), pinnedGit(t, fresh, "reflog", "--format=%H", ref); got != want {
			t.Errorf("reflog of %s = %q, want %q", ref, got, want)
		}
	}
	if packed := readFileT(t, bare, "packed-refs"); strings.Contains(packed, oldTip) || !strings.Contains(packed, wantTip+" refs/heads/main") {
		t.Errorf("packed-refs of the remote = %q, want main at %s and no %s", packed, wantTip, oldTip)
	}
	if status := pinnedGit(t, work, "status", "--porcelain", "--untracked-files=no"); status != "" {
		t.Errorf("git status = %q, want clean", status)
	}
	if a, b := pinnedGit(t, work, "ls-files", "-s"), pinnedGit(t, fresh, "ls-files", "-s"); a != b {
		t.Errorf("index entries = %q, want %q", a, b)
	}
	if got := pinnedGit(t, work, "show", rootCommit(t, work)+":a.txt"); got != "path: "+newRoot {
		t.Errorf("a.txt in the first commit = %q", got)
	}
}

// rootCommit is the root commit of the history at dir.
func rootCommit(t *testing.T, dir string) string {
	t.Helper()
	return pinnedGit(t, dir, "rev-list", "--max-parents=0", "HEAD")
}

// TestRewriteHistoryKeepsACommitWhoseFilesDoNotChange: a side branch cut
// before the file existed holds none of the changed blobs, so its commit
// keeps its id.
func TestRewriteHistoryKeepsACommitWhoseFilesDoNotChange(t *testing.T) {
	work := newRewriteRepo(t, map[string]string{"plain.txt": "no path\n"}, "base")
	base := pinnedGit(t, work, "rev-parse", "HEAD")
	pinnedGit(t, work, "checkout", "-q", "-b", "side")
	writeFileT(t, work, "side.txt", "side\n")
	pinnedGit(t, work, "add", "-A")
	pinnedGit(t, work, "commit", "-q", "-m", "side")
	side := pinnedGit(t, work, "rev-parse", "HEAD")
	pinnedGit(t, work, "checkout", "-q", "main")
	writeFileT(t, work, "a.txt", "/old\n")
	pinnedGit(t, work, "add", "-A")
	pinnedGit(t, work, "commit", "-q", "-m", "main adds the file")
	writeFileT(t, work, "a.txt", "/new\n")

	if err := RewriteHistory(work, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	mustBeSound(t, work)
	if got := pinnedGit(t, work, "rev-parse", "side"); got != side {
		t.Errorf("side = %s, want it kept at %s", got, side)
	}
	if got := pinnedGit(t, work, "rev-parse", "HEAD^"); got != base {
		t.Errorf("parent of main = %s, want the base %s", got, base)
	}
	if got := pinnedGit(t, work, "show", "HEAD:a.txt"); got != "/new" {
		t.Errorf("a.txt in HEAD = %q", got)
	}
}

// TestRewriteHistoryOfOnlyTheTipIsRewriteHeadCommit: when only the tip holds
// the changed blob the two functions make the same repository.
func TestRewriteHistoryOfOnlyTheTipIsRewriteHeadCommit(t *testing.T) {
	build := func() string {
		work := newRewriteRepo(t, map[string]string{"x.txt": "one\n"}, "first")
		writeFileT(t, work, "p.txt", "path /old\n")
		pinnedGit(t, work, "add", "-A")
		pinnedGit(t, work, "commit", "-q", "-m", "second")
		writeFileT(t, work, "p.txt", "path /new\n")
		return work
	}
	a, b := build(), build()
	if err := RewriteHeadCommit(a, []string{"p.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := RewriteHistory(b, []string{"p.txt"}); err != nil {
		t.Fatal(err)
	}
	if x, y := refList(t, a), refList(t, b); x != y {
		t.Errorf("refs: %q vs %q", x, y)
	}
	if x, y := strings.Join(objectIDs(t, a), "\n"), strings.Join(objectIDs(t, b), "\n"); x != y {
		t.Errorf("objects differ:\n%s\nvs\n%s", x, y)
	}
	if x, y := pinnedGit(t, a, "ls-files", "-s"), pinnedGit(t, b, "ls-files", "-s"); x != y {
		t.Errorf("index: %q vs %q", x, y)
	}
}

// TestRewriteHistoryWithNothingToChangeTouchesNothing: no path that differs
// from the tip's blob writes a byte of the work tree repository or its
// remote.
func TestRewriteHistoryWithNothingToChangeTouchesNothing(t *testing.T) {
	work, bare := historyWithRoot(t, "/old/root")
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
	writeFileT(t, work, "untracked.txt", "x\n")
	for _, paths := range [][]string{nil, {"a.txt", "dir/b.txt"}, {"untracked.txt", ".git/config", "nope/x.txt"}} {
		if err := RewriteHistory(work, paths, bare); err != nil {
			t.Fatalf("paths %v: %v", paths, err)
		}
	}
	after := snap()
	for p, body := range before {
		if got, ok := after[p]; !ok || got != body {
			t.Errorf("%s changed", p)
		}
	}
	if len(after) != len(before)+1 {
		t.Errorf("the rewrite added or removed files: %d now, %d before and one untracked", len(after), len(before))
	}
}

// TestRewriteHistoryRefusals: what a history rewrite cannot do faithfully is
// an error, and says why.
func TestRewriteHistoryRefusals(t *testing.T) {
	t.Run("an annotated tag", func(t *testing.T) {
		work, _ := historyWithRoot(t, "/old")
		pinnedGit(t, work, "tag", "-a", "-m", "release", "v1", "HEAD^")
		writeFileT(t, work, "a.txt", "path: /new\n")
		err := RewriteHistory(work, []string{"a.txt"})
		if err == nil || !strings.Contains(err.Error(), "annotated tag") {
			t.Errorf("err = %v, want an annotated tag refusal", err)
		}
	})
	t.Run("HEAD off a branch", func(t *testing.T) {
		work, _ := historyWithRoot(t, "/old")
		pinnedGit(t, work, "checkout", "-q", "--detach")
		writeFileT(t, work, "a.txt", "path: /new\n")
		err := RewriteHistory(work, []string{"a.txt"})
		if err == nil || !strings.Contains(err.Error(), "not on a branch") {
			t.Errorf("err = %v, want a refusal", err)
		}
	})
	t.Run("a mirror commit the work tree does not know", func(t *testing.T) {
		work, bare := historyWithRoot(t, "/old")
		other := t.TempDir()
		pinnedGit(t, other, "clone", "-q", bare, ".")
		pinnedGit(t, other, "checkout", "-q", "-b", "extra", rootCommit(t, other))
		writeFileT(t, other, "extra.txt", "x\n")
		pinnedGit(t, other, "add", "-A")
		pinnedGit(t, other, "commit", "-q", "-m", "extra")
		pinnedGit(t, other, "push", "-q", "origin", "extra")
		writeFileT(t, work, "a.txt", "path: /new\n")
		err := RewriteHistory(work, []string{"a.txt"}, bare)
		if err == nil || !strings.Contains(err.Error(), "descends") {
			t.Errorf("err = %v, want a refusal for the half-rewritable mirror", err)
		}
	})
}

// TestRewriteHistoryGivesAMirrorOnlyWhatItsRefsReach: a mirror that holds just
// main, while the work tree has a side branch and a merge it never pushed,
// ends with the objects a fresh remote of main has, and keeps its other ref
// where it was.
func TestRewriteHistoryGivesAMirrorOnlyWhatItsRefsReach(t *testing.T) {
	const oldRoot, newRoot = "/old/root", "/new/place/longer"
	build := func(root string) (work, bare string) {
		work = newRewriteRepo(t, map[string]string{"a.txt": "path: " + root + "\n"}, "first")
		bare = bareCloneOf(t, work)
		pinnedGit(t, work, "checkout", "-q", "-b", "local")
		writeFileT(t, work, "local.txt", "never pushed\n")
		pinnedGit(t, work, "add", "-A")
		pinnedGit(t, work, "commit", "-q", "-m", "local only")
		return work, bare
	}
	work, bare := build(oldRoot)
	writeFileT(t, work, "a.txt", "path: "+newRoot+"\n")
	_, freshBare := build(newRoot)

	if err := RewriteHistory(work, []string{"a.txt"}, bare); err != nil {
		t.Fatal(err)
	}
	mustBeSound(t, bare)
	if got, want := strings.Join(objectIDs(t, bare), "\n"), strings.Join(objectIDs(t, freshBare), "\n"); got != want {
		t.Errorf("objects of the remote:\n%s\nwant:\n%s", got, want)
	}
	if got, want := refList(t, bare), refList(t, freshBare); got != want {
		t.Errorf("refs of the remote:\n%s\nwant:\n%s", got, want)
	}
}
