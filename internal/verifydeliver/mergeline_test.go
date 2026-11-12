package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
)

// lineRepo is a repository for the head-reviewed-through-merges tests: "main"
// with a seed and a shared.txt, and a branch "work" cut from it. Methods commit
// on the branch they name, and leave the work tree on "work".
type lineRepo struct {
	t   *testing.T
	dir string
}

func newLineRepo(t *testing.T) *lineRepo {
	t.Helper()
	r := &lineRepo{t: t, dir: t.TempDir()}
	r.git("init", "-b", "main")
	r.git("config", "user.name", "jig-fixture")
	r.git("config", "user.email", "fixture@example.invalid")
	r.git("config", "core.autocrlf", "false")
	r.write("seed.txt", "seed\n")
	r.write("shared.txt", "one\ntwo\nthree\n")
	r.git("add", "-A")
	r.git("commit", "-m", "seed")
	r.git("update-ref", "refs/remotes/origin/main", "HEAD")
	r.git("switch", "-c", "work")
	return r
}

func (r *lineRepo) git(args ...string) string {
	r.t.Helper()
	out, err := gitx.RunEnv(r.dir, reviewGitEnv, args...)
	if err != nil {
		r.t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func (r *lineRepo) write(name, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit writes name on the current branch and commits it, returning the head.
func (r *lineRepo) commit(name, body string) string {
	r.t.Helper()
	r.write(name, body)
	r.git("add", "-A")
	r.git("commit", "-m", "write "+name)
	return r.git("rev-parse", "HEAD")
}

// onMain commits name on main and comes back to work.
func (r *lineRepo) onMain(name, body string) string {
	r.t.Helper()
	r.git("switch", "main")
	sha := r.commit(name, body)
	r.git("update-ref", "refs/remotes/origin/main", sha)
	r.git("switch", "work")
	return sha
}

// merge merges main into work as git would (a merge commit, however it ends up).
func (r *lineRepo) merge() string {
	r.t.Helper()
	r.git("merge", "--no-ff", "--no-edit", "main")
	return r.git("rev-parse", "HEAD")
}

// handMerge merges main into work without committing, then lets edit change the
// work tree before the commit: a merge resolved by hand.
func (r *lineRepo) handMerge(edit func()) string {
	r.t.Helper()
	r.git("merge", "--no-ff", "--no-commit", "main")
	edit()
	r.git("add", "-A")
	r.git("commit", "-m", "merge main, by hand")
	return r.git("rev-parse", "HEAD")
}

// lineCase is a head and the reviewed head before it, with the answer.
type lineCase struct {
	name           string
	reviewed, head string
	kind           lineKind
	// ownTreeOf is the merge commit whose parents' own merge classifyLine names
	// as the tree, for a resolved tip.
	ownTreeOf string
}

// lineCases builds the cases of the table over one repository.
func lineCases(t *testing.T) (dir string, cases []lineCase) {
	t.Helper()
	r := newLineRepo(t)
	reviewed := r.commit("work.txt", "the work\n")
	cases = append(cases, lineCase{name: "the same head", reviewed: reviewed, head: reviewed, kind: lineSame})

	// An ordinary commit after the reviewed head.
	ordinary := r.commit("more.txt", "an ordinary commit\n")
	cases = append(cases, lineCase{name: "an ordinary commit", reviewed: reviewed, head: ordinary, kind: lineOther})
	r.git("reset", "--hard", reviewed)

	// One clean merge, then a second (the target moved twice).
	r.onMain("main1.txt", "from main\n")
	m1 := r.merge()
	cases = append(cases, lineCase{name: "one clean merge", reviewed: reviewed, head: m1, kind: lineClean})
	r.onMain("main2.txt", "from main again\n")
	m2 := r.merge()
	cases = append(cases, lineCase{name: "two clean merges", reviewed: reviewed, head: m2, kind: lineClean})
	cases = append(cases, lineCase{name: "a clean merge after a reviewed clean merge", reviewed: m1, head: m2, kind: lineClean})

	// A merge, then an ordinary commit on top of it.
	onTop := r.commit("top.txt", "after the merge\n")
	cases = append(cases, lineCase{name: "a merge and an ordinary commit", reviewed: reviewed, head: onTop, kind: lineMergedThenOwn, ownTreeOf: m2})
	reviewed2 := onTop

	// A hand-resolved tip: the merge also edits shared.txt.
	r.onMain("main3.txt", "from main, third time\n")
	resolved := r.handMerge(func() { r.write("shared.txt", "one\ntwo\nthree\nand a hand edit\n") })
	cases = append(cases, lineCase{name: "a hand-resolved tip", reviewed: reviewed2, head: resolved, kind: lineResolved, ownTreeOf: resolved})
	// A hand-resolved merge, then an ordinary commit: the hand edits and the
	// commit are the round's, the target's files are not.
	handThenOwn := r.commit("after-hand.txt", "after the hand-resolved merge\n")
	cases = append(cases, lineCase{name: "a hand-resolved merge and an ordinary commit", reviewed: reviewed2, head: handThenOwn, kind: lineMergedThenOwn, ownTreeOf: resolved})
	r.git("reset", "--hard", resolved)

	// A hand-resolved merge that is not the tip: a clean merge follows it.
	r.onMain("main4.txt", "from main, fourth time\n")
	after := r.merge()
	cases = append(cases, lineCase{name: "a hand-resolved merge that is not the tip", reviewed: reviewed2, head: after, kind: lineOther})
	// But from the hand-resolved merge as the reviewed head, the clean one is clean.
	cases = append(cases, lineCase{name: "a clean merge past a reviewed hand-resolved one", reviewed: resolved, head: after, kind: lineClean})

	// A reviewed head off the first-parent line: the target's own commit.
	mainTip := r.git("rev-parse", "main")
	cases = append(cases, lineCase{name: "a reviewed head off the first-parent line", reviewed: mainTip, head: after, kind: lineOther})
	// A reviewed head that is not behind the head at all.
	cases = append(cases, lineCase{name: "a reviewed head that is not behind the head", reviewed: after, head: resolved, kind: lineOther})

	// A line of the target's own clean merges that does not start at the reviewed
	// head: the reviewed head came in through the last merge's second parent, and
	// every commit of the line is a two-parent merge git itself would have made.
	// Only the line's start tells it from a line grown from the reviewed head.
	seed := r.git("rev-list", "--max-parents=0", "main")
	r.git("switch", "-c", "mainline", seed)
	for _, name := range []string{"first", "second"} {
		r.git("switch", "-c", name, seed)
		r.commit(name+".txt", "a feature merged into the target\n")
		r.git("switch", "mainline")
		r.git("merge", "--no-ff", "--no-edit", name)
	}
	r.git("merge", "--no-ff", "--no-edit", reviewed)
	mainline := r.git("rev-parse", "HEAD")
	r.git("switch", "work")
	cases = append(cases, lineCase{name: "clean merges that do not start at the reviewed head", reviewed: reviewed, head: mainline, kind: lineOther})

	// An octopus merge is any other commit.
	r.git("switch", "-c", "side", "main")
	side := r.commit("side.txt", "a side branch\n")
	r.git("switch", "work")
	r.onMain("main5.txt", "from main, fifth time\n")
	r.git("merge", "--no-ff", "--no-edit", "main", side)
	octopus := r.git("rev-parse", "HEAD")
	cases = append(cases, lineCase{name: "an octopus", reviewed: after, head: octopus, kind: lineOther})

	// A merge that does not bring in the target: a clean merge of an
	// unrelated branch, then a hand-resolved one, are the reviewer's to read.
	r.git("switch", "-c", "unrelated", seed)
	r.commit("unrelated.txt", "work that is not on the target\n")
	r.git("switch", "work")
	r.git("merge", "--no-ff", "--no-edit", "unrelated")
	unrelated := r.git("rev-parse", "HEAD")
	cases = append(cases, lineCase{name: "a clean merge of an unrelated branch", reviewed: octopus, head: unrelated, kind: lineOther})
	r.git("reset", "--hard", octopus)
	r.git("merge", "--no-ff", "--no-commit", "unrelated")
	r.write("shared.txt", "one\ntwo\nthree\nan unrelated hand edit\n")
	r.git("add", "-A")
	r.git("commit", "-m", "merge unrelated, by hand")
	unrelatedResolved := r.git("rev-parse", "HEAD")
	cases = append(cases, lineCase{name: "a hand-resolved merge of an unrelated branch", reviewed: octopus, head: unrelatedResolved, kind: lineOther})

	// A merge after an ordinary commit is the reviewer's to read in full.
	r.git("reset", "--hard", reviewed)
	r.commit("before.txt", "an ordinary commit first\n")
	r.onMain("main6.txt", "from main, sixth time\n")
	ordinaryThenMerge := r.merge()
	cases = append(cases, lineCase{name: "a merge after an ordinary commit", reviewed: reviewed, head: ordinaryThenMerge, kind: lineOther})
	return r.dir, cases
}

// TestClassifyLineNeedsTheTarget: a clean merge of the target is clean only
// against a ref that holds the merged commit; with no ref, or one that does not,
// it is lineOther.
func TestClassifyLineNeedsTheTarget(t *testing.T) {
	t.Parallel()
	r := newLineRepo(t)
	reviewed := r.commit("work.txt", "the work\n")
	r.onMain("main1.txt", "from main\n")
	merged := r.merge()
	r.git("branch", "parent", reviewed)
	for ref, want := range map[string]lineKind{"main": lineClean, "": lineOther, "parent": lineOther, "missing": lineOther} {
		kind, _, err := classifyLine(r.dir, reviewed, merged, ref)
		if err != nil || kind != want {
			t.Errorf("target %q: classifyLine = %d, %v, want %d", ref, kind, err, want)
		}
	}
}

// TestClassifyLine: the table over a real repository - the same head, merges
// git itself would have made, a tip resolved by hand, and everything else.
func TestClassifyLine(t *testing.T) {
	t.Parallel()
	dir, cases := lineCases(t)
	for _, c := range cases {
		kind, own, err := classifyLine(dir, c.reviewed, c.head, "main")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if kind != c.kind {
			t.Errorf("%s: kind = %d, want %d", c.name, kind, c.kind)
		}
		if c.kind == lineMergedThenOwn {
			// The base: the carried merge itself when it is clean, git's own merge
			// of its parents (a tree) when it was resolved by hand.
			tree, _, merr := gitx.MergeTree(dir, c.ownTreeOf+"^1", c.ownTreeOf+"^2")
			mergeTree, _ := gitx.RevParse(dir, c.ownTreeOf+"^{tree}")
			want := c.ownTreeOf
			if merr != nil || tree != mergeTree {
				want = tree
			}
			if own != want {
				t.Errorf("%s: base = %q, want %q (%v)", c.name, own, want, merr)
			}
			continue
		}
		if c.kind != lineResolved {
			if own != "" {
				t.Errorf("%s: ownTree = %q, want none", c.name, own)
			}
			continue
		}
		tree, _, merr := gitx.MergeTree(dir, c.ownTreeOf+"^1", c.ownTreeOf+"^2")
		if merr != nil || own != tree {
			t.Errorf("%s: ownTree = %q, want git's own merge of the tip's parents, %q (%v)", c.name, own, tree, merr)
		}
		if tipTree, _ := gitx.RevParse(dir, c.head+"^{tree}"); own == tipTree {
			t.Errorf("%s: ownTree is the tip's own tree, so the diff to it would be empty", c.name)
		}
	}
}

// TestClassifyLineGivesTheRefusalItHadWhenGitCannotTell: a directory that is not
// a repository, an object the directory lacks and a head with nothing reviewed
// are lineOther with no error, so the refusal stays what it was; the same head
// is the same whatever the directory.
func TestClassifyLineGivesTheRefusalItHadWhenGitCannotTell(t *testing.T) {
	t.Parallel()
	dir, cases := lineCases(t)
	reviewed, head := cases[0].reviewed, cases[3].head
	const stray = "0123456789abcdef0123456789abcdef01234567"
	for name, c := range map[string]struct {
		dir, reviewed, head string
		kind                lineKind
	}{
		"no directory":                  {"", reviewed, head, lineOther},
		"no directory, the same head":   {"", reviewed, reviewed, lineSame},
		"not a repository":              {t.TempDir(), reviewed, head, lineOther},
		"an object the directory lacks": {dir, stray, head, lineOther},
		"a head the directory lacks":    {dir, reviewed, stray, lineOther},
		"nothing reviewed":              {dir, "", head, lineOther},
	} {
		kind, own, err := classifyLine(c.dir, c.reviewed, c.head, "main")
		if err != nil || kind != c.kind || own != "" {
			t.Errorf("%s: classifyLine = %d, %q, %v, want %d with no error", name, kind, own, err, c.kind)
		}
	}
}

// gitBefore238 writes a git that answers `merge-tree` as a git before 2.38 does
// (a usage error, exit 129) and runs every other command through the real git,
// and returns the directory it is in, to put first on a PATH.
func gitBefore238(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	name := "git"
	body := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"\tif [ \"$a\" = merge-tree ]; then echo 'usage: git merge-tree' >&2; exit 129; fi\n" +
		"done\n" +
		"exec '" + real + "' \"$@\"\n"
	if runtime.GOOS == "windows" {
		name = "git.bat"
		body = "@echo off\r\n" +
			"for %%a in (%*) do if \"%%a\"==\"merge-tree\" (\r\n" +
			"\techo usage: git merge-tree 1>&2\r\n" +
			"\texit /b 129\r\n" +
			")\r\n" +
			"\"" + real + "\" %*\r\n"
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// oldGitHelperEnv marks a run of this test binary as the child that
// TestClassifyLineAsksAnOldGitOnlyForALineOfMerges starts with a git before 2.38
// first on its PATH. Its value is the repository, the reviewed head and the
// heads to classify, separated by "|".
const oldGitHelperEnv = "JIG_VERIFYDELIVER_TEST_OLD_GIT"

// TestClassifyLineOldGitHelper runs as that child: it logs, for each head, what
// classifyLine says of it, and whether git can make a merge at all. Outside that
// it does nothing. The PATH is the child's, so the test process's own is left
// as it is.
func TestClassifyLineOldGitHelper(t *testing.T) {
	arg := os.Getenv(oldGitHelperEnv)
	if arg == "" {
		t.Skip("only TestClassifyLineAsksAnOldGitOnlyForALineOfMerges runs this")
	}
	fields := strings.Split(arg, "|")
	dir, reviewed, heads := fields[0], fields[1], fields[2:]
	code := func(err error) string {
		var ae *axi.Error
		switch {
		case err == nil:
			return "none"
		case errors.As(err, &ae):
			return ae.Code
		}
		return "error: " + err.Error()
	}
	_, _, err := gitx.MergeTree(dir, reviewed, reviewed)
	t.Logf("merge-tree: %s", code(err))
	for i, head := range heads {
		kind, own, err := classifyLine(dir, reviewed, head, "main")
		t.Logf("head[%d]: kind=%d own=%q err=%s", i, kind, own, code(err))
	}
}

// TestClassifyLineAsksAnOldGitOnlyForALineOfMerges: with a git before 2.38 a line
// of merges only is refused with GIT_TOO_OLD, but a line with an ordinary commit
// in it, whether it comes before or after the merges, is lineOther with no error:
// it was before, on any git. The git is the child's: the
// package's tests do not edit the environment they share.
func TestClassifyLineAsksAnOldGitOnlyForALineOfMerges(t *testing.T) {
	t.Parallel()
	r := newLineRepo(t)
	reviewed := r.commit("work.txt", "the work\n")
	r.onMain("main1.txt", "from main\n")
	merged := r.merge()
	mergeThenOrdinary := r.commit("top.txt", "after the merge\n")
	r.git("reset", "--hard", reviewed)
	ordinary := r.commit("more.txt", "an ordinary commit\n")
	r.onMain("main2.txt", "from main again\n")
	ordinaryThenMerge := r.merge()

	heads := []string{ordinary, mergeThenOrdinary, ordinaryThenMerge, merged}
	cmd := exec.Command(os.Args[0], "-test.run=^TestClassifyLineOldGitHelper$", "-test.v")
	cmd.Env = append(os.Environ(),
		oldGitHelperEnv+"="+strings.Join(append([]string{r.dir, reviewed}, heads...), "|"),
		"PATH="+gitBefore238(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"merge-tree: " + gitx.CodeGitTooOld,
		fmt.Sprintf("head[0]: kind=%d own=\"\" err=none", lineOther),
		fmt.Sprintf("head[1]: kind=%d own=\"\" err=none", lineOther),
		fmt.Sprintf("head[2]: kind=%d own=\"\" err=none", lineOther),
		fmt.Sprintf("head[3]: kind=%d own=\"\" err=%s", lineOther, gitx.CodeGitTooOld),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("helper output lacks %q:\n%s", want, got)
		}
	}
}

// TestHeadReviewedThroughMerges: only the same head and clean merges are reviewed.
func TestHeadReviewedThroughMerges(t *testing.T) {
	t.Parallel()
	dir, cases := lineCases(t)
	for _, c := range cases {
		got, err := headReviewed(dir, c.reviewed, c.head, "main")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if want := c.kind == lineSame || c.kind == lineClean; got != want {
			t.Errorf("%s: headReviewed = %v, want %v", c.name, got, want)
		}
	}
	if ok, err := headReviewed("", cases[0].reviewed, cases[3].head, "main"); ok || err != nil {
		t.Errorf("with no directory to ask: %v, %v, want a plain no", ok, err)
	}
}

// TestCheckBodyHeadThroughMerges: a body is rendered for a branch whose tip is
// the reviewed head or only clean merges past it, read in the clone that holds
// the commits between them; a tip with a hand-resolved merge, or an ordinary
// commit, past the reviewed head is refused as it was, with the same code.
func TestCheckBodyHeadThroughMerges(t *testing.T) {
	t.Parallel()
	r := newLineRepo(t)
	reviewed := r.commit("work.txt", "the work\n")
	d := Deps{Machine: project.MachineProject{Clones: map[string]string{"api": r.dir}}, Home: t.TempDir()}
	target := func() *publishTarget {
		return &publishTarget{ticket: "T-1", repoName: "api", target: "main", branch: "work", gateRep: reportYAML{ReviewedSHA: map[string]string{"api": reviewed}}}
	}

	if err := checkBodyHead(d, target(), ""); err != nil {
		t.Errorf("the branch at the reviewed head: %v", err)
	}
	r.onMain("main1.txt", "from main\n")
	r.merge()
	if err := checkBodyHead(d, target(), ""); err != nil {
		t.Errorf("the branch a clean merge past the reviewed head: %v", err)
	}
	r.onMain("main2.txt", "from main again\n")
	r.handMerge(func() { r.write("shared.txt", "one\ntwo\nthree\nthe hand edit\n") })
	wantAxiCode(t, checkBodyHead(d, target(), ""), "PUBLISH_UNREVIEWED_HEAD")
	r.git("reset", "--hard", "HEAD~1")
	r.commit("more.txt", "an ordinary commit\n")
	wantAxiCode(t, checkBodyHead(d, target(), ""), "PUBLISH_UNREVIEWED_HEAD")

	// A clean merge of a branch that is not the target is refused.
	r.git("reset", "--hard", reviewed)
	seed := r.git("rev-list", "--max-parents=0", "main")
	r.git("switch", "-c", "unrelated", seed)
	r.commit("unrelated.txt", "work that is not on the target\n")
	r.git("switch", "work")
	r.git("merge", "--no-ff", "--no-edit", "unrelated")
	wantAxiCode(t, checkBodyHead(d, target(), ""), "PUBLISH_UNREVIEWED_HEAD")

	// A tip is read in the repository it was found in: the reader's copy of
	// origin's branch (dir) holds the merge and its parents, so it covers it
	// though the operator maps no clone at all.
	r.git("reset", "--hard", reviewed)
	r.onMain("main3.txt", "from main, third time\n")
	r.merge()
	r.git("update-ref", "refs/remotes/origin/work", "HEAD")
	only := Deps{Home: t.TempDir()}
	if err := checkBodyHead(only, target(), r.dir); err != nil {
		t.Errorf("a tip read in the reader's repository: %v", err)
	}
}
