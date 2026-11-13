package gitx

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// plus is files with one more (or changed) file.
func plus(files map[string]string, name, body string) map[string]string {
	out := map[string]string{name: body}
	for k, v := range files {
		if k != name {
			out[k] = v
		}
	}
	return out
}

// historyCommits is a history whose first commit holds the files that carry
// root: a second commit on main, a side branch from the first, a merge of the
// side, and a last commit.
func historyCommits(root string) []hcommit {
	first := map[string]string{"a.txt": "path: " + root + "\n", "dir/b.txt": "also " + root + "/b here\n", "dir/plain.txt": "no path\n"}
	second := plus(first, "second.txt", "second\n")
	side := plus(first, "side.txt", "side\n")
	merged := plus(second, "side.txt", "side\n")
	last := plus(merged, "last.txt", "last\n")
	return []hcommit{
		{"main", "first", nil, first},
		{"main", "second", []string{"first"}, second},
		{"side", "side", []string{"first"}, side},
		{"main", "merge side", []string{"second", "side"}, merged},
		{"main", "last", []string{"merge side"}, last},
	}
}

// historyWithRoot is the history of historyCommits as a work tree and a bare
// remote with origin's branches fetched. Two calls with the same root make the
// same commits.
func historyWithRoot(t *testing.T, root string, noIndex ...bool) (work, bare string, ids hist) {
	t.Helper()
	work = t.TempDir()
	ids = buildHist(t, work, "main", historyCommits(root), noIndex...)
	bare = bareOf(t, work)
	return work, bare, ids
}

// TestRewriteHistoryIsTheHistoryAFreshRepositoryMakes: files that carry a
// path sit in the first of several commits, with a merge and a side branch on
// top; once the work tree files hold the final path and the history is
// rewritten, it is the history built with the final path from the start: the
// same commit ids on every ref, the same objects, in the work tree repository
// and in its bare remote, with the reflogs, packed refs and the index agreeing.
func TestRewriteHistoryIsTheHistoryAFreshRepositoryMakes(t *testing.T) {
	const oldRoot, newRoot = "/old/root", "/new/place/longer"
	paths := []string{"a.txt", "dir/b.txt", "dir/plain.txt", "second.txt"}

	work, bare, old := historyWithRoot(t, oldRoot)
	for _, name := range []string{"a.txt", "dir/b.txt"} {
		writeFileT(t, work, name, strings.ReplaceAll(readFileT(t, work, name), oldRoot, newRoot))
	}
	fresh, freshBare, want := historyWithRoot(t, newRoot)
	if want["last"] == old["last"] {
		t.Fatalf("the files did not change the history")
	}

	if err := RewriteHistory(work, paths, bare); err != nil {
		t.Fatal(err)
	}

	for _, repo := range []string{work, bare} {
		fsck(t, repo)
		for _, id := range []string{old["first"], old["last"]} {
			if hasObject(t, repo, id) {
				t.Errorf("%s still has the commit %s of the old history", repo, id)
			}
		}
	}
	sameState(t, "work tree repository", work, fresh)
	sameState(t, "remote", bare, freshBare)
	if got := blobAt(t, work, want["first"], "a.txt"); got != "path: "+newRoot+"\n" {
		t.Errorf("a.txt in the first commit = %q", got)
	}
	if status := pinnedGit(t, work, "status", "--porcelain", "--untracked-files=no"); status != "" {
		t.Errorf("git status = %q, want clean", status)
	}
}

// TestRewriteHistoryKeepsACommitWhoseFilesDoNotChange: a side branch cut
// before the file existed holds none of the changed blobs, so its commit
// keeps its id.
func TestRewriteHistoryKeepsACommitWhoseFilesDoNotChange(t *testing.T) {
	base := map[string]string{"plain.txt": "no path\n"}
	work := t.TempDir()
	ids := buildHist(t, work, "main", []hcommit{
		{"main", "base", nil, base},
		{"side", "side", []string{"base"}, plus(base, "side.txt", "side\n")},
		{"main", "main adds the file", []string{"base"}, plus(base, "a.txt", "/old\n")},
	}, true)
	writeFileT(t, work, "a.txt", "/new\n")

	if err := RewriteHistory(work, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := RefTip(work, "refs/heads/side"); got != ids["side"] {
		t.Errorf("side = %s, want it kept at %s", got, ids["side"])
	}
	if got, _ := RevParse(work, "HEAD^"); got != ids["base"] {
		t.Errorf("parent of main = %s, want the base %s", got, ids["base"])
	}
	if got := blobAt(t, work, "HEAD", "a.txt"); got != "/new\n" {
		t.Errorf("a.txt in HEAD = %q", got)
	}
}

// TestRewriteHistoryOfOnlyTheTipIsRewriteHeadCommit: when only the tip holds
// the changed blob the two functions make the same repository.
func TestRewriteHistoryOfOnlyTheTipIsRewriteHeadCommit(t *testing.T) {
	build := func() string {
		work := t.TempDir()
		first := map[string]string{"x.txt": "one\n"}
		buildHist(t, work, "main", []hcommit{
			{"main", "first", nil, first},
			{"main", "second", []string{"first"}, plus(first, "p.txt", "path /old\n")},
		})
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
	sameState(t, "RewriteHistory against RewriteHeadCommit", b, a)
}

// TestRewriteHistoryWithNothingToChangeTouchesNothing: no path that differs
// from the tip's blob writes a byte of the work tree repository or its
// remote.
func TestRewriteHistoryWithNothingToChangeTouchesNothing(t *testing.T) {
	work, bare, _ := historyWithRoot(t, "/old/root", true)
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
		work, _, ids := historyWithRoot(t, "/old", true)
		repo, done, err := openStorage(filepath.Join(work, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		if _, err := repo.CreateTag("v1", plumbing.NewHash(ids["second"]), &git.CreateTagOptions{Message: "release", Tagger: &object.Signature{Name: "pinned", Email: "pinned@example.invalid", When: time.Unix(1767225600, 0)}}); err != nil {
			t.Fatal(err)
		}
		writeFileT(t, work, "a.txt", "path: /new\n")
		err = RewriteHistory(work, []string{"a.txt"})
		if err == nil || !strings.Contains(err.Error(), "annotated tag") {
			t.Errorf("err = %v, want an annotated tag refusal", err)
		}
	})
	t.Run("HEAD off a branch", func(t *testing.T) {
		work, _, ids := historyWithRoot(t, "/old", true)
		writeFileT(t, filepath.Join(work, ".git"), "HEAD", ids["last"]+"\n")
		writeFileT(t, work, "a.txt", "path: /new\n")
		err := RewriteHistory(work, []string{"a.txt"})
		if err == nil || !strings.Contains(err.Error(), "not on a branch") {
			t.Errorf("err = %v, want a refusal", err)
		}
	})
	t.Run("a mirror commit the work tree does not know", func(t *testing.T) {
		work, bare, ids := historyWithRoot(t, "/old", true)
		stream := "commit refs/heads/extra\nauthor " + histIdent + "\ncommitter " + histIdent + "\ndata 6\nextra\nfrom " + ids["first"] + "\ndeleteall\nM 100644 inline x.txt\ndata 2\nx\n\n"
		if _, err := RunInputEnv(bare, rewriteEnv, []byte(stream), "fast-import", "--quiet"); err != nil {
			t.Fatal(err)
		}
		writeFileT(t, work, "a.txt", "path: /new\n")
		err := RewriteHistory(work, []string{"a.txt"}, bare)
		if err == nil || !strings.Contains(err.Error(), "descends") {
			t.Errorf("err = %v, want a refusal for the half-rewritable mirror", err)
		}
	})
}

// TestRewriteHistoryGivesAMirrorOnlyWhatItsRefsReach: a mirror that holds just
// main, while the work tree has a branch it never pushed, ends with the
// objects a fresh remote of main has, and keeps its other ref where it was.
func TestRewriteHistoryGivesAMirrorOnlyWhatItsRefsReach(t *testing.T) {
	build := func(root string) (work, bare string) {
		first := map[string]string{"a.txt": "path: " + root + "\n"}
		work = t.TempDir()
		buildHist(t, work, "local", []hcommit{
			{"main", "first", nil, first},
			{"local", "local only", []string{"first"}, plus(first, "local.txt", "never pushed\n")},
		}, true)
		return work, bareOf(t, work)
	}
	work, bare := build("/old/root")
	writeFileT(t, work, "a.txt", "path: /new/place/longer\n")
	_, freshBare := build("/new/place/longer")

	if err := RewriteHistory(work, []string{"a.txt"}, bare); err != nil {
		t.Fatal(err)
	}
	g, w := stateOf(t, bare), stateOf(t, freshBare)
	if strings.Join(g.objects, "\n") != strings.Join(w.objects, "\n") {
		t.Errorf("objects of the remote:\n%v\nwant:\n%v", g.objects, w.objects)
	}
	if len(g.refs) != len(w.refs) {
		t.Errorf("refs of the remote: %v, want %v", g.refs, w.refs)
	}
}

// TestRewriteHistoryKeepsEachPathsOwnNewBytes: two paths that hold the same
// bytes at the tip, and have other bytes in the work tree, each get their own;
// and an older version of one that equals the other's tip blob is not replaced
// by it, since only the blob the tip tracks at the path is.
func TestRewriteHistoryKeepsEachPathsOwnNewBytes(t *testing.T) {
	work := t.TempDir()
	first := map[string]string{"a.txt": "path /old\n", "b.txt": "other\n"}
	buildHist(t, work, "main", []hcommit{
		{"main", "first", nil, first},
		{"main", "second", []string{"first"}, plus(first, "b.txt", "path /old\n")},
	})
	writeFileT(t, work, "a.txt", "path /new-a\n")
	writeFileT(t, work, "b.txt", "path /new-b\n")

	if err := RewriteHistory(work, []string{"a.txt", "b.txt"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ rev, path, want string }{
		{"HEAD", "a.txt", "path /new-a\n"}, {"HEAD", "b.txt", "path /new-b\n"},
		{"HEAD^", "a.txt", "path /new-a\n"}, {"HEAD^", "b.txt", "other\n"},
	} {
		if got := blobAt(t, work, c.rev, c.path); got != c.want {
			t.Errorf("%s:%s = %q, want %q", c.rev, c.path, got, c.want)
		}
	}
	idx, _, err := readIndex(filepath.Join(work, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx.Entries {
		if got := blobAt(t, work, "HEAD", e.Name); got != readFileT(t, work, e.Name) {
			t.Errorf("%s: the tip holds %q, the work tree %q", e.Name, got, readFileT(t, work, e.Name))
		}
	}
}

// TestRewriteHistoryMovesACommitWhoseOnlyChangeIsItsParent: a commit that
// deletes the path holds none of the changed blobs, and still takes the new id
// of its parent, so no commit of the old history stays reachable.
func TestRewriteHistoryMovesACommitWhoseOnlyChangeIsItsParent(t *testing.T) {
	build := func(root string) (string, hist) {
		work := t.TempDir()
		with := map[string]string{"a.txt": "path " + root + "\n"}
		ids := buildHist(t, work, "main", []hcommit{
			{"main", "first", nil, with},
			{"main", "delete it", []string{"first"}, map[string]string{"keep.txt": "k\n"}},
			{"main", "bring it back", []string{"delete it"}, plus(map[string]string{"keep.txt": "k\n"}, "a.txt", "path "+root+"\n")},
		})
		return work, ids
	}
	work, old := build("/old")
	fresh, want := build("/new")
	writeFileT(t, work, "a.txt", "path /new\n")

	if err := RewriteHistory(work, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	sameState(t, "the rewritten history", work, fresh)
	for _, msg := range []string{"first", "delete it", "bring it back"} {
		if hasObject(t, work, old[msg]) {
			t.Errorf("old commit %q (%s) is still in the repository", msg, old[msg])
		}
		if !hasObject(t, work, want[msg]) {
			t.Errorf("commit %q has not moved to %s", msg, want[msg])
		}
	}
}

// signedCommit is the tip of main in the repository at dir, replaced by the
// same commit with a signature, as git writes one.
func signedCommit(t *testing.T, dir string) {
	t.Helper()
	repo, done, err := openStorage(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	id, _ := refValue(repo, "refs/heads/main")
	c, err := repo.CommitObject(id)
	if err != nil {
		t.Fatal(err)
	}
	signed := *c
	signed.PGPSignature = "-----BEGIN PGP SIGNATURE-----\n\nabc\n-----END PGP SIGNATURE-----"
	obj, err := encodeCommit(repo, &signed)
	if err != nil {
		t.Fatal(err)
	}
	h, err := storeObject(repo, obj)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference("refs/heads/main", h)); err != nil {
		t.Fatal(err)
	}
}

// TestRewriteHistoryRefusesWhatItCannotRewriteFaithfully: a commit that would
// change and is signed, a repository that converts line endings, and one with
// a linked work tree (whose HEAD and index the rewrite would leave at the old
// commits) are errors, for the history rewriters and the head rewriter alike,
// and leave the repository as it was.
func TestRewriteHistoryRefusesWhatItCannotRewriteFaithfully(t *testing.T) {
	rewriters := map[string]func(dir string) error{
		"RewriteHistory": func(dir string) error { return RewriteHistory(dir, []string{"a.txt"}) },
		"RewriteHistoryFunc": func(dir string) error {
			_, err := RewriteHistoryFunc(dir, []string{"a.txt"}, func(_ string, d []byte) []byte { return []byte(strings.ReplaceAll(string(d), "/old", "/new")) })
			return err
		},
		"RewriteHeadCommit": func(dir string) error { return RewriteHeadCommit(dir, []string{"a.txt"}) },
	}
	cases := map[string]struct {
		setup func(t *testing.T, work string)
		want  string
	}{
		"autocrlf": {func(t *testing.T, work string) {
			writeFileT(t, filepath.Join(work, ".git"), "config", readFileT(t, filepath.Join(work, ".git"), "config")+"[core]\n\tautocrlf = true\n")
		}, "autocrlf"},
		"linked work tree": {func(t *testing.T, work string) {
			pinnedGit(t, work, "worktree", "add", "-q", "--detach", filepath.Join(t.TempDir(), "linked"))
		}, "linked work trees"},
		"signed": {func(t *testing.T, work string) { signedCommit(t, work) }, "signed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			buildHist(t, work, "main", []hcommit{{"main", "first", nil, map[string]string{"a.txt": "path /old\n"}}}, true)
			writeFileT(t, work, "a.txt", "path /new\n")
			c.setup(t, work)
			before, _ := RefTip(work, "refs/heads/main")
			for rname, rewrite := range rewriters {
				err := rewrite(work)
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("%s: err = %v, want a refusal naming %q", rname, err, c.want)
				}
				if got, _ := RefTip(work, "refs/heads/main"); got != before {
					t.Errorf("%s: main moved to %s after a refusal", rname, got)
				}
			}
		})
	}
}

// TestRewriteHistoryFuncPointsEveryVersionOfAFileAtTheNewPlace: a file that
// holds a path in each of its versions (an edit in the second commit and
// another in the third) is rewritten in all of them, by a rule, and the
// history equals the one built with the final path from the start, commit ids
// and the remote's objects included; the result names each commit that moved.
func TestRewriteHistoryFuncPointsEveryVersionOfAFileAtTheNewPlace(t *testing.T) {
	const oldRoot, newRoot = "/old/root", "/new/place/longer"
	build := func(root string) (work, bare string, ids hist) {
		v1 := map[string]string{"notes.txt": "root: " + root + "\nv1\n", "plain.txt": "x\n"}
		v2 := plus(v1, "notes.txt", "root: "+root+"\nv2\n")
		v3 := plus(v2, "notes.txt", "root: "+root+"\nv3\n")
		work = t.TempDir()
		ids = buildHist(t, work, "main", []hcommit{
			{"main", "first", nil, v1},
			{"main", "edit v2", []string{"first"}, v2},
			{"main", "edit again v3", []string{"edit v2"}, v3},
			{"main", "last", []string{"edit again v3"}, plus(v3, "last.txt", "last\n")},
		})
		return work, bareOf(t, work), ids
	}
	work, bare, old := build(oldRoot)
	fresh, freshBare, want := build(newRoot)
	writeFileT(t, work, "notes.txt", strings.ReplaceAll(readFileT(t, work, "notes.txt"), oldRoot, newRoot))

	moved, err := RewriteHistoryFunc(work, []string{"notes.txt", "plain.txt"}, func(_ string, data []byte) []byte {
		return []byte(strings.ReplaceAll(string(data), oldRoot, newRoot))
	}, bare)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != len(old) {
		t.Errorf("%d commits moved, want all %d: %v", len(moved), len(old), moved)
	}
	for msg, id := range old {
		if moved[id] != want[msg] {
			t.Errorf("commit %q moved to %q, want %s", msg, moved[id], want[msg])
		}
	}
	fsck(t, work)
	fsck(t, bare)
	sameState(t, "work tree repository", work, fresh)
	sameState(t, "remote", bare, freshBare)
	if again, err := RewriteHistoryFunc(work, []string{"notes.txt"}, func(_ string, data []byte) []byte { return data }); err != nil || len(again) != 0 {
		t.Errorf("a transform that changes nothing moved %v, %v", again, err)
	}
}

// TestRewritersRefuseAMirrorWithLinkedWorkTrees: a bare mirror that has a work
// tree of its own (git worktree add from the bare repository) is refused, as
// the repository itself is, by each rewriter, and nothing is changed.
func TestRewritersRefuseAMirrorWithLinkedWorkTrees(t *testing.T) {
	rewriters := map[string]func(dir, mirror string) error{
		"RewriteHistory": func(dir, mirror string) error { return RewriteHistory(dir, []string{"a.txt"}, mirror) },
		"RewriteHistoryFunc": func(dir, mirror string) error {
			_, err := RewriteHistoryFunc(dir, []string{"a.txt"}, func(_ string, d []byte) []byte { return []byte(strings.ReplaceAll(string(d), "/old", "/new")) }, mirror)
			return err
		},
		"RewriteHeadCommit": func(dir, mirror string) error { return RewriteHeadCommit(dir, []string{"a.txt"}, mirror) },
	}
	work := t.TempDir()
	buildHist(t, work, "main", []hcommit{{"main", "first", nil, map[string]string{"a.txt": "path /old\n"}}}, true)
	bare := bareOf(t, work)
	pinnedGit(t, bare, "worktree", "add", "-q", "--detach", filepath.Join(t.TempDir(), "linked"), "main")
	writeFileT(t, work, "a.txt", "path /new\n")
	before, _ := RefTip(work, "refs/heads/main")
	for name, rewrite := range rewriters {
		err := rewrite(work, bare)
		if err == nil || !strings.Contains(err.Error(), "linked work trees") {
			t.Fatalf("%s: err = %v, want a refusal naming the linked work trees", name, err)
		}
		if got, _ := RefTip(work, "refs/heads/main"); got != before {
			t.Errorf("%s: HEAD moved to %s after a refusal", name, got)
		}
		if got, _ := RefTip(bare, "refs/heads/main"); got != before {
			t.Errorf("%s: the mirror's main moved to %s after a refusal", name, got)
		}
	}
	fsck(t, bare)
}
