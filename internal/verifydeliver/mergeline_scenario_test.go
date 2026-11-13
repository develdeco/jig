package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// The merge-line history is the same fifteen cases over one repository
// whichever test reads them, and the tests only read it, so it is built once
// for the binary, as a scenario, and each test gets a copy of its own: file
// operations, no git process. Built with git, one command at a time, it cost
// about a thousand processes for the four tests that use it.
//
// The scenario writes the history as one git fast-import stream. Every commit
// names its files in full (a stream that leaves out the unchanged ones would
// need git's own merge to say what a merge holds), and the model below
// computes each merge's files as git's merge of these commits makes them: the
// first parent's, plus the files only another parent has (the cases never
// change one file on two sides but by the hand edit that a case spells out).
// The commits git itself would make are not the stream's byte for byte (a
// merge's message names the commit it merges where the cases merge a commit),
// but their trees, parents and subjects are, which the equivalence test
// (TestLineHistoryCopyIsTheGitBuiltHistory) proves against the history git
// builds, for every case.

// lineIdent is the author and committer of every commit, the date reviewGitEnv
// pins.
const lineIdent = "jig-fixture <fixture@example.invalid> 1767225600 +0000"

// lineNode is a commit of the model: its mark and its files in full.
type lineNode struct {
	mark  int
	files map[string]string
}

// modelCase is a lineCase named by marks.
type modelCase struct {
	name           string
	reviewed, head *lineNode
	kind           lineKind
	ownTreeOf      *lineNode
}

// lineModel writes the fast-import stream of the line history as it plays the
// steps of lineCasesByGit.
type lineModel struct {
	out      strings.Builder
	marks    int
	blobs    map[string]int // content to its blob's mark
	cur      string
	heads    map[string]*lineNode
	origin   *lineNode // refs/remotes/origin/main
	cases    []modelCase
	seedNode *lineNode
}

func newLineModel() *lineModel {
	m := &lineModel{blobs: map[string]int{}, heads: map[string]*lineNode{}}
	seed := m.commitOn("main", "seed", nil, map[string]string{"seed.txt": "seed\n", "shared.txt": "one\ntwo\nthree\n"})
	m.heads["main"], m.origin, m.seedNode = seed, seed, seed
	m.heads["work"], m.cur = seed, "work"
	return m
}

func (m *lineModel) mark() int { m.marks++; return m.marks }

// data is a fast-import data command.
func data(s string) string { return fmt.Sprintf("data %d\n%s\n", len(s), s) }

// blob is the mark of the blob of body, written the first time it is needed.
func (m *lineModel) blob(body string) int {
	if n, ok := m.blobs[body]; ok {
		return n
	}
	n := m.mark()
	fmt.Fprintf(&m.out, "blob\nmark :%d\n%s", n, data(body))
	m.blobs[body] = n
	return n
}

// commitOn writes a commit on branch with the given parents (the first is the
// branch's own line) and files in full.
func (m *lineModel) commitOn(branch, msg string, parents []*lineNode, files map[string]string) *lineNode {
	marks := map[string]int{}
	names := make([]string, 0, len(files))
	for name, body := range files {
		marks[name] = m.blob(body)
		names = append(names, name)
	}
	sort.Strings(names)
	n := &lineNode{mark: m.mark(), files: files}
	fmt.Fprintf(&m.out, "commit refs/heads/%s\nmark :%d\nauthor %s\ncommitter %s\n%s", branch, n.mark, lineIdent, lineIdent, data(msg+"\n"))
	for i, p := range parents {
		if i == 0 {
			fmt.Fprintf(&m.out, "from :%d\n", p.mark)
		} else {
			fmt.Fprintf(&m.out, "merge :%d\n", p.mark)
		}
	}
	m.out.WriteString("deleteall\n")
	for _, name := range names {
		fmt.Fprintf(&m.out, "M 100644 :%d %s\n", marks[name], name)
	}
	m.out.WriteString("\n")
	return n
}

// with is files plus one file.
func with(files map[string]string, name, body string) map[string]string {
	out := make(map[string]string, len(files)+1)
	for k, v := range files {
		out[k] = v
	}
	out[name] = body
	return out
}

// union is the files of the first with the ones only the others have: what
// git's merge of commits that add different files makes.
func union(first map[string]string, others ...map[string]string) map[string]string {
	out := make(map[string]string, len(first))
	for k, v := range first {
		out[k] = v
	}
	for _, o := range others {
		for k, v := range o {
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
	}
	return out
}

func (m *lineModel) tip() *lineNode { return m.heads[m.cur] }

// commit is lineRepo.commit: write name on the current branch and commit it.
func (m *lineModel) commit(name, body string) *lineNode {
	n := m.commitOn(m.cur, "write "+name, []*lineNode{m.tip()}, with(m.tip().files, name, body))
	m.heads[m.cur] = n
	return n
}

// onMain is lineRepo.onMain: a commit on main, which origin/main follows.
func (m *lineModel) onMain(name, body string) *lineNode {
	was := m.cur
	m.cur = "main"
	n := m.commit(name, body)
	m.origin = n
	m.cur = was
	return n
}

// merge is a merge commit on the current branch of the others into it.
func (m *lineModel) mergeInto(msg string, edit func(map[string]string), others ...*lineNode) *lineNode {
	parents := []*lineNode{m.tip()}
	var files []map[string]string
	for _, o := range others {
		parents = append(parents, o)
		files = append(files, o.files)
	}
	merged := union(m.tip().files, files...)
	if edit != nil {
		edit(merged)
	}
	n := m.commitOn(m.cur, msg, parents, merged)
	m.heads[m.cur] = n
	return n
}

// branch is `git switch -c name from`.
func (m *lineModel) branch(name string, from *lineNode) {
	m.heads[name], m.cur = from, name
}

func (m *lineModel) add(name string, reviewed, head *lineNode, kind lineKind, ownTreeOf *lineNode) {
	m.cases = append(m.cases, modelCase{name: name, reviewed: reviewed, head: head, kind: kind, ownTreeOf: ownTreeOf})
}

// play is lineCasesByGit over the model.
func (m *lineModel) play() {
	reviewed := m.commit("work.txt", "the work\n")
	m.add("the same head", reviewed, reviewed, lineSame, nil)

	ordinary := m.commit("more.txt", "an ordinary commit\n")
	m.add("an ordinary commit", reviewed, ordinary, lineOther, nil)
	m.heads["work"] = reviewed

	m.onMain("main1.txt", "from main\n")
	m1 := m.mergeInto("Merge branch 'main' into work", nil, m.heads["main"])
	m.add("one clean merge", reviewed, m1, lineClean, nil)
	m.onMain("main2.txt", "from main again\n")
	m2 := m.mergeInto("Merge branch 'main' into work", nil, m.heads["main"])
	m.add("two clean merges", reviewed, m2, lineClean, nil)
	m.add("a clean merge after a reviewed clean merge", m1, m2, lineClean, nil)

	onTop := m.commit("top.txt", "after the merge\n")
	m.add("a merge and an ordinary commit", reviewed, onTop, lineMergedThenOwn, m2)
	reviewed2 := onTop

	m.onMain("main3.txt", "from main, third time\n")
	resolved := m.mergeInto("merge main, by hand", func(f map[string]string) {
		f["shared.txt"] = "one\ntwo\nthree\nand a hand edit\n"
	}, m.heads["main"])
	m.add("a hand-resolved tip", reviewed2, resolved, lineResolved, resolved)
	handThenOwn := m.commit("after-hand.txt", "after the hand-resolved merge\n")
	m.add("a hand-resolved merge and an ordinary commit", reviewed2, handThenOwn, lineMergedThenOwn, resolved)
	m.heads["work"] = resolved

	m.onMain("main4.txt", "from main, fourth time\n")
	after := m.mergeInto("Merge branch 'main' into work", nil, m.heads["main"])
	m.add("a hand-resolved merge that is not the tip", reviewed2, after, lineOther, nil)
	m.add("a clean merge past a reviewed hand-resolved one", resolved, after, lineClean, nil)

	mainTip := m.heads["main"]
	m.add("a reviewed head off the first-parent line", mainTip, after, lineOther, nil)
	m.add("a reviewed head that is not behind the head", after, resolved, lineOther, nil)

	seed := m.seedNode
	m.branch("mainline", seed)
	for _, name := range []string{"first", "second"} {
		m.branch(name, seed)
		m.commit(name+".txt", "a feature merged into the target\n")
		feature := m.heads[name]
		m.cur = "mainline"
		m.mergeInto("Merge branch '"+name+"' into mainline", nil, feature)
	}
	m.mergeInto("Merge commit '"+shaToken+"' into mainline", nil, reviewed)
	mainline := m.heads["mainline"]
	m.cur = "work"
	m.add("clean merges that do not start at the reviewed head", reviewed, mainline, lineOther, nil)

	m.branch("side", m.heads["main"])
	side := m.commit("side.txt", "a side branch\n")
	m.cur = "work"
	m.onMain("main5.txt", "from main, fifth time\n")
	octopus := m.mergeInto("Merge branch 'main'; commit '"+shaToken+"' into work", nil, m.heads["main"], side)
	m.add("an octopus", after, octopus, lineOther, nil)

	m.branch("unrelated", seed)
	m.commit("unrelated.txt", "work that is not on the target\n")
	unrelatedTip := m.heads["unrelated"]
	m.cur = "work"
	unrelated := m.mergeInto("Merge branch 'unrelated' into work", nil, unrelatedTip)
	m.add("a clean merge of an unrelated branch", octopus, unrelated, lineOther, nil)
	m.heads["work"] = octopus
	unrelatedResolved := m.mergeInto("merge unrelated, by hand", func(f map[string]string) {
		f["shared.txt"] = "one\ntwo\nthree\nan unrelated hand edit\n"
	}, unrelatedTip)
	m.add("a hand-resolved merge of an unrelated branch", octopus, unrelatedResolved, lineOther, nil)

	m.heads["work"] = reviewed
	m.commit("before.txt", "an ordinary commit first\n")
	m.onMain("main6.txt", "from main, sixth time\n")
	ordinaryThenMerge := m.mergeInto("Merge branch 'main' into work", nil, m.heads["main"])
	m.add("a merge after an ordinary commit", reviewed, ordinaryThenMerge, lineOther, nil)

	m.heads["work"] = reviewed
	m.onMain("main7.txt", "from main, seventh time\n")
	m.mergeInto("Merge branch 'main' into work", nil, m.heads["main"])
	m.commit("own1.txt", "the branch's own commit\n")
	m.onMain("main8.txt", "from main, eighth time\n")
	m.mergeInto("Merge branch 'main' into work", nil, m.heads["main"])
	mergeCommitMerge := m.commit("own2.txt", "another of its own\n")
	m.add("a clean merge, a commit, a merge and a commit", reviewed, mergeCommitMerge, lineOther, nil)
	m.heads["work"] = reviewed
	m.onMain("main9.txt", "from main, ninth time\n")
	m.mergeInto("Merge branch 'main' into work", nil, m.heads["main"])
	m.commit("own3.txt", "the branch's own commit\n")
	m.onMain("main10.txt", "from main, tenth time\n")
	m.mergeInto("Merge branch 'main'; commit '"+shaToken+"' into work", nil, m.heads["main"], side)
	octopusAfter := m.commit("own4.txt", "after the octopus\n")
	m.add("a clean merge, a commit, an octopus and a commit", reviewed, octopusAfter, lineOther, nil)
}

// shaToken stands for the id of a commit a merge's message names, which the
// stream cannot know. The equivalence test reads such a subject as it reads
// git's.
const shaToken = "<sha>"

// finish ends the stream: every branch where the cases leave it.
func (m *lineModel) finish() string {
	var names []string
	for name := range m.heads {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&m.out, "reset refs/heads/%s\nfrom :%d\n\n", name, m.heads[name].mark)
	}
	fmt.Fprintf(&m.out, "reset refs/remotes/origin/main\nfrom :%d\n\n", m.origin.mark)
	m.out.WriteString("done\n")
	return m.out.String()
}

var (
	lineHistoryMu    sync.Mutex
	lineHistoryCases []lineCase
)

// lineHistoryConfig is the repository's config: what git init writes, less
// the settings of the platform, and the identity the hand-made commits used.
const lineHistoryConfig = "[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tautocrlf = false\n" +
	"[user]\n\tname = jig-fixture\n\temail = fixture@example.invalid\n"

// buildLineHistory is the scenario's build: the repository in dirs[0], made by
// one git fast-import, and the cases as the shas it gave them.
func buildLineHistory(dirs []string) (*fixture.Fixture, error) {
	m := newLineModel()
	m.play()
	stream := m.finish()

	gitDir := filepath.Join(dirs[0], ".git")
	for _, d := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(gitDir, d), 0o755); err != nil {
			return nil, err
		}
	}
	for name, body := range map[string]string{"HEAD": "ref: refs/heads/work\n", "config": lineHistoryConfig} {
		if err := os.WriteFile(filepath.Join(gitDir, name), []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	scratch, err := os.MkdirTemp("", "jig-line-history")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	marksFile := filepath.Join(scratch, "marks")
	if _, err := gitx.RunInputEnv(dirs[0], reviewGitEnv, []byte(stream), "fast-import", "--quiet", "--export-marks="+marksFile); err != nil {
		return nil, fmt.Errorf("fast-import the line history: %w", err)
	}
	raw, err := os.ReadFile(marksFile)
	if err != nil {
		return nil, err
	}
	shas := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		markText, sha, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("marks line %q", line)
		}
		n, err := strconv.Atoi(strings.TrimPrefix(markText, ":"))
		if err != nil {
			return nil, err
		}
		shas[n] = sha
	}
	sha := func(n *lineNode) string {
		if n == nil {
			return ""
		}
		return shas[n.mark]
	}
	var cases []lineCase
	for _, c := range m.cases {
		cases = append(cases, lineCase{name: c.name, reviewed: sha(c.reviewed), head: sha(c.head), kind: c.kind, ownTreeOf: sha(c.ownTreeOf)})
	}
	lineHistoryMu.Lock()
	lineHistoryCases = cases
	lineHistoryMu.Unlock()
	return nil, nil
}

// lineCases is the table of cases over a repository of its own: a copy of the
// line history, which every caller only reads.
func lineCases(t *testing.T) (dir string, cases []lineCase) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "line-history")
	if _, err := fixture.Scenario("merge-line history", fixture.Opts{}, []string{dir}, buildLineHistory); err != nil {
		t.Fatal(err)
	}
	lineHistoryMu.Lock()
	defer lineHistoryMu.Unlock()
	return dir, append([]lineCase(nil), lineHistoryCases...)
}

var shaInSubject = regexp.MustCompile(`'[0-9a-f]{40}'`)

// canonCommit is a commit as a shape that does not depend on any commit id:
// its tree, its subject (with the id a merge message names read as a token)
// and its parents' shapes, in order.
func canonCommit(t *testing.T, dir, sha string, memo map[string]string) string {
	t.Helper()
	if c, ok := memo[sha]; ok {
		return c
	}
	out, err := gitx.Run(dir, "show", "-s", "--format=%T%n%s%n%P", sha)
	if err != nil {
		t.Fatalf("show %s in %s: %v", sha, dir, err)
	}
	lines := strings.Split(out, "\n")
	for len(lines) < 3 {
		lines = append(lines, "")
	}
	var parents []string
	for _, p := range strings.Fields(lines[2]) {
		parents = append(parents, canonCommit(t, dir, p, memo))
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s %q [%s]", lines[0], shaInSubject.ReplaceAllString(lines[1], "'"+shaToken+"'"), strings.Join(parents, " "))))
	c := hex.EncodeToString(sum[:8])
	memo[sha] = c
	return c
}

// refShapes is each ref of the repository at dir, with the shape of the commit
// it names.
func refShapes(t *testing.T, dir string, memo map[string]string) map[string]string {
	t.Helper()
	out, err := gitx.Run(dir, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, sha, ok := strings.Cut(line, " ")
		if ok {
			got[name] = canonCommit(t, dir, sha, memo)
		}
	}
	return got
}

// TestLineHistoryCopyIsTheGitBuiltHistory: the history the scenario copies is
// the one git builds a command at a time (the reference, lineCasesByGit), as
// far as the tests can tell: for each of the 15 cases, the reviewed head and
// the head have the same tree, subject and parents (and theirs, to the root);
// every ref names such a commit in both, and the same refs exist; and
// classifyLine and headReviewed answer alike for every case, the base a
// carried merge names included. The ids of the commits themselves are not
// compared: a merge's message names a commit by id, which the stream cannot.
// And a copy starts no git process: with git off the PATH, one is still made.
func TestLineHistoryCopyIsTheGitBuiltHistory(t *testing.T) {
	copied, got := lineCases(t)
	byGit, want := lineCasesByGit(t)
	if len(got) != len(want) {
		t.Fatalf("%d cases, git built %d", len(got), len(want))
	}
	gotMemo, wantMemo := map[string]string{}, map[string]string{}
	for i := range want {
		g, w := got[i], want[i]
		if g.name != w.name || g.kind != w.kind {
			t.Errorf("case %d is %q (kind %d), git built %q (kind %d)", i, g.name, g.kind, w.name, w.kind)
			continue
		}
		for what, pair := range map[string][2]string{"reviewed": {g.reviewed, w.reviewed}, "head": {g.head, w.head}, "own tree of": {g.ownTreeOf, w.ownTreeOf}} {
			if (pair[0] == "") != (pair[1] == "") {
				t.Errorf("%s: %s is %q, git built %q", g.name, what, pair[0], pair[1])
				continue
			}
			if pair[0] == "" {
				continue
			}
			if a, b := canonCommit(t, copied, pair[0], gotMemo), canonCommit(t, byGit, pair[1], wantMemo); a != b {
				t.Errorf("%s: %s is\n%s\ngit built\n%s", g.name, what, a, b)
			}
		}
		gotKind, gotOwn, gotErr := classifyLine(copied, g.reviewed, g.head, "main")
		wantKind, wantOwn, wantErr := classifyLine(byGit, w.reviewed, w.head, "main")
		if gotErr != nil || wantErr != nil || gotKind != wantKind {
			t.Errorf("%s: classifyLine = %d, %v; git built's = %d, %v", g.name, gotKind, gotErr, wantKind, wantErr)
		}
		// The base is a tree, or a commit when it is a carried merge.
		if (gotOwn == "") != (wantOwn == "") {
			t.Errorf("%s: classifyLine's base is %q, git built's %q", g.name, gotOwn, wantOwn)
		} else if gotOwn != "" {
			if gotType, wantType := objectType(t, copied, gotOwn), objectType(t, byGit, wantOwn); gotType != wantType {
				t.Errorf("%s: the base is a %s, git built's a %s", g.name, gotType, wantType)
			} else if gotType == "tree" && gotOwn != wantOwn {
				t.Errorf("%s: the base tree is %s, git built's %s", g.name, gotOwn, wantOwn)
			} else if gotType == "commit" && canonCommit(t, copied, gotOwn, gotMemo) != canonCommit(t, byGit, wantOwn, wantMemo) {
				t.Errorf("%s: the base commit differs from git built's", g.name)
			}
		}
		gotOK, gotErr := headReviewed(copied, g.reviewed, g.head, "main")
		wantOK, wantErr := headReviewed(byGit, w.reviewed, w.head, "main")
		if gotErr != nil || wantErr != nil || gotOK != wantOK {
			t.Errorf("%s: headReviewed = %v, %v; git built's = %v, %v", g.name, gotOK, gotErr, wantOK, wantErr)
		}
	}
	gotRefs, wantRefs := refShapes(t, copied, gotMemo), refShapes(t, byGit, wantMemo)
	if len(gotRefs) != len(wantRefs) {
		t.Errorf("refs %v, git built %v", gotRefs, wantRefs)
	}
	for name, shape := range wantRefs {
		if gotRefs[name] != shape {
			t.Errorf("%s names\n%s\ngit built\n%s", name, gotRefs[name], shape)
		}
	}
	if head, err := os.ReadFile(filepath.Join(copied, ".git", "HEAD")); err != nil || !strings.Contains(string(head), "refs/heads/work") {
		t.Errorf("HEAD = %q, %v, want the branch work", head, err)
	}

	t.Setenv("PATH", t.TempDir())
	if _, err := gitx.Run(copied, "rev-parse", "HEAD"); err == nil {
		t.Fatalf("git still runs with an empty PATH, so a copy that starts it would pass")
	}
	if _, cases := lineCases(t); len(cases) != len(want) {
		t.Errorf("a copy without git made %d cases", len(cases))
	}
}

// objectType is the type of the object sha in the repository at dir.
func objectType(t *testing.T, dir, sha string) string {
	t.Helper()
	out, err := gitx.Run(dir, "cat-file", "-t", sha)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
