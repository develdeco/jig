package gitx

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// canonPath is p as git spells a path it makes absolute: cleaned, with the
// longest part of it that exists resolved of symbolic links (on Windows that
// also expands an 8.3 short name to its long one), and the rest kept as it
// is, since a path git names need not exist.
func canonPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// TestInProcessReadsMatchGit puts every in-process read next to the git
// program on the repositories of readFixture: for each question, the answer
// is the one git gives, or the read declines and the exported function asks
// git. Questions that must be answered in process are checked to be.
func TestInProcessReadsMatchGit(t *testing.T) {
	fx := buildReadFixture(t)
	t.Run("fixture", func(t *testing.T) { checkFixtureShape(t, fx) })
	t.Run("revisions", func(t *testing.T) { checkRevisions(t, fx) })
	t.Run("rev-parse of a missing object and a broken ref", func(t *testing.T) { checkRevParseMissing(t, fx) })
	t.Run("discovery", func(t *testing.T) { checkDiscovery(t, fx) })
	t.Run("head branch", func(t *testing.T) { checkHeadBranch(t, fx) })
	t.Run("ref tip", func(t *testing.T) { checkRefTip(t, fx) })
	t.Run("subject", func(t *testing.T) { checkSubject(t, fx) })
	t.Run("unmerged", func(t *testing.T) { checkUnmerged(t, fx) })
	t.Run("git paths", func(t *testing.T) { checkGitPaths(t, fx) })
	t.Run("work tree probe", func(t *testing.T) { checkProbe(t, fx) })
	t.Run("tree entries", func(t *testing.T) { checkTreeEntries(t, fx) })
	t.Run("paths in a commit", func(t *testing.T) { checkHasPathAt(t, fx) })
	t.Run("a replace ref", func(t *testing.T) { checkObjectsGitReadsDifferently(t, fx, fx.repl, "HEAD^") })
	t.Run("alternates", func(t *testing.T) { checkObjectsGitReadsDifferently(t, fx, fx.alt, "") })
}

// revCases are the revisions asked of every repository: the ones the
// in-process reads answer, then the ones they must leave to git (declined
// for every repository, mainOnly for the main one, whose refs they name).
func revCases(fx *readFixture) (answered, declined, mainOnly []string) {
	c1, c2, c3 := fx.shas["c1"], fx.shas["c2"], fx.shas["c3"]
	answered = []string{
		"HEAD", "HEAD^{commit}", "HEAD^{tree}", "HEAD^", "HEAD^1",
		"HEAD:a.txt", "HEAD:dir", "HEAD:dir/sub/c.txt", "HEAD:sp ace.txt", "HEAD:nope", "HEAD:dir/b.txt/x", "HEAD:dir/nope/x",
		"refs/heads/main", "refs/heads/main^{commit}", "refs/heads/main^{tree}", "refs/heads/feature/x", "refs/heads/both",
		"refs/heads/loose-only", "refs/heads/sym", "refs/heads/sym2", "refs/heads/d5", "refs/heads/dangling",
		"refs/heads/broken^{commit}", "refs/heads/nope", "refs/heads/nope^{commit}", "refs/heads/nope:a.txt",
		"refs/heads/wtb", "refs/heads/feature", "refs/heads/feature/x/y",
		"refs/tags/light", "refs/tags/light^{commit}", "refs/tags/ann", "refs/tags/ann^{commit}", "refs/tags/ann^{tree}",
		"refs/tags/ann^", "refs/tags/ann:a.txt", "refs/tags/ann2^{commit}", "refs/tags/ann2^{tree}", "refs/tags/treetag",
		"refs/tags/treetag^{tree}", "refs/tags/treetag^{commit}", "refs/tags/treetag:a.txt", "refs/tags/nope",
		"refs/remotes/origin/main", "refs/remotes/origin/HEAD", "refs/remotes/origin/HEAD^{commit}", "refs/remotes/origin/nope",
		"refs/bisect/bad", "refs/stash", "refs/notes/x",
		c1 + "^{commit}", c1 + "^{tree}", c1 + "^", c2 + "^", c3 + "^1", c1 + ":a.txt", c3 + ":dir/sub",
		strings.ToUpper(c1) + "^{commit}", fx.fakeSHA + "^{commit}", fx.fakeSHA + "^{tree}", fx.fakeSHA + ":a.txt", fx.fakeSHA + "^",
		fx.shas["ann"] + "^{commit}", fx.shas["blobA"] + "^{commit}", fx.shas["blobA"] + "^{tree}", fx.shas["tree3"] + "^{commit}",
		fx.shas["tree3"] + "^{tree}", fx.shas["tree3"] + ":a.txt",
		fx.shas["notree"] + "^{commit}", fx.shas["notree"] + "^{tree}", fx.shas["notree"] + ":a.txt", fx.shas["notree"] + "^",
	}
	declined = []string{
		"main", "origin/main", "feature/x", "HEAD~1", "HEAD^^", "HEAD~", "HEAD@{1}", "@", "@{u}", "HEAD^{}", "HEAD^0", "HEAD^2",
		"HEAD^{blob}", "HEAD^{tree}^", c1[:12], c1[:39], "-x", ":/c", ":0:a.txt", "HEAD:", "HEAD:./a.txt", "HEAD:../a.txt", "HEAD:dir/",
		"HEAD:/a.txt", "HEAD:dir//b.txt", `HEAD:a\b`, "refs/heads/x y", "refs/heads/../x", "refs/heads/x.lock",
		"main..HEAD", "HEAD...main", "refs/heads/*", "", "refs/tags/ann^{commit}^", "refs/",
	}
	// A ref whose file is not a ref, a chain of symbolic refs longer than git
	// follows, and a path that is a submodule's commit.
	mainOnly = []string{"refs/heads/garbage", "refs/heads/d6", "HEAD:subm"}
	return answered, declined, mainOnly
}

func checkRevisions(t *testing.T, fx *readFixture) {
	answered, declined, mainOnly := revCases(fx)
	for name, dir := range map[string]string{
		"main": fx.main, "subdirectory": fx.sub, "linked worktree": fx.wt, "detached linked worktree": fx.wtd,
		"separate git dir": fx.sep, "gitfile": fx.holder, "unborn": fx.unborn,
	} {
		t.Run(name, func(t *testing.T) {
			// One git process asks about every revision: cat-file prints
			// "<name> missing" for a name that resolves to no object and
			// "<oid> <type> <size>" for one that does, naming revisions
			// the way rev-parse does.
			var in []string
			var results []result
			var shas []string
			for _, rev := range answered {
				sha, res := answerRev(dir, rev, os.LookupEnv)
				if res == resDecline {
					t.Errorf("%q in %s: declined, want answered in process", rev, name)
				}
				in, results, shas = append(in, rev), append(results, res), append(shas, sha)
			}
			out, err := RunInput(dir, []byte(strings.Join(in, "\n")+"\n"), "cat-file", "--batch-check")
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
			if len(lines) != len(in) {
				t.Fatalf("git answered %d lines for %d revisions:\n%s", len(lines), len(in), out)
			}
			for i, rev := range in {
				missing := strings.HasSuffix(lines[i], " missing")
				switch results[i] {
				case resFound:
					// A name that resolves without its object being read
					// (a full object name, a ref to a missing object) is
					// "missing" to cat-file; the next test covers those.
					if !missing && !strings.HasPrefix(lines[i], shas[i]+" ") {
						t.Errorf("%q in %s: in process %s, git %q", rev, name, shas[i], lines[i])
					}
				case resNoRef, resNoEntry, resNoObject:
					if !missing {
						t.Errorf("%q in %s: in process no (%d), git %q", rev, name, results[i], lines[i])
					}
				}
			}
			left := declined
			if dir == fx.main {
				left = append(slices.Clone(declined), mainOnly...)
			}
			for _, rev := range left {
				if _, res := answerRev(dir, rev, os.LookupEnv); res != resDecline {
					t.Errorf("%q in %s: answered in process (%d), want left to git", rev, name, res)
				}
			}
		})
	}
	t.Run("per-worktree refs", func(t *testing.T) {
		for _, c := range []struct {
			dir, rev string
			want     result
		}{
			{fx.wt, "refs/bisect/bad", resFound},
			{fx.main, "refs/bisect/bad", resNoRef},
			{fx.wtd, "refs/bisect/bad", resNoRef},
			{fx.wt, "refs/heads/wtb", resFound},
			{fx.main, "refs/heads/wtb", resFound},
		} {
			_, res := answerRev(c.dir, c.rev, os.LookupEnv)
			_, ok, _ := resolve(c.dir, c.rev, false)
			if res != c.want || ok != (c.want == resFound) {
				t.Errorf("%q in %s: in process %d, exported found=%v, want %d", c.rev, c.dir, res, ok, c.want)
			}
		}
		if wt, _ := answerRev(fx.wt, "HEAD", os.LookupEnv); wt != fx.shas["wtb"] || wt == fx.shas["c4"] {
			t.Errorf("HEAD in the linked worktree = %q, want its own branch's %s", wt, fx.shas["wtb"])
		}
		if main, _ := answerRev(fx.main, "HEAD", os.LookupEnv); main != fx.shas["c4"] {
			t.Errorf("HEAD in the main worktree = %q, want %s", main, fx.shas["c4"])
		}
		if main, _ := answerRev(fx.main, "refs/heads/wtb", os.LookupEnv); main != fx.shas["wtb"] {
			t.Errorf("refs/heads/wtb from the main worktree = %q, want %s", main, fx.shas["wtb"])
		}
		if wtd, _ := answerRev(fx.wtd, "HEAD", os.LookupEnv); wtd != fx.shas["c1"] {
			t.Errorf("HEAD in the detached worktree = %q, want %s", wtd, fx.shas["c1"])
		}
	})
}

// checkRevParseMissing covers what cat-file cannot tell from a missing name:
// git's rev-parse prints a full object name, and a ref's object, without
// looking for the object.
func checkRevParseMissing(t *testing.T, fx *readFixture) {
	for _, rev := range []string{fx.fakeSHA, strings.ToUpper(fx.shas["c1"]), "refs/heads/broken", "refs/heads/sym", "HEAD", "refs/tags/ann", "refs/heads/both", "refs/heads/main"} {
		sha, res := answerRev(fx.main, rev, os.LookupEnv)
		if res != resFound {
			t.Errorf("%q: in process %d, want found", rev, res)
			continue
		}
		for _, args := range [][]string{{"rev-parse", "--verify", "--quiet", rev}, {"rev-parse", rev}} {
			if want := mustGit(t, fx.main, args...); want != sha {
				t.Errorf("git %v = %s, in process %s", args, want, sha)
			}
		}
		if got, err := RevParse(fx.main, rev); err != nil || got != sha {
			t.Errorf("RevParse(%q) = %q, %v; want %s", rev, got, err, sha)
		}
		if got, ok, err := Resolve(fx.main, rev); err != nil || !ok || got != sha {
			t.Errorf("Resolve(%q) = %q, %v, %v; want %s", rev, got, ok, err, sha)
		}
	}
	// The exported functions agree with git for the forms they leave to it.
	for _, rev := range []string{"main", "origin/main", "HEAD~1", "refs/heads/garbage", "feature/x", "nope", fx.shas["c1"][:10]} {
		want, werr := Run(fx.main, "rev-parse", "--verify", "--quiet", rev)
		got, ok, err := Resolve(fx.main, rev)
		if werr == nil && (err != nil || !ok || got != want) || werr != nil && ok {
			t.Errorf("Resolve(%q) = %q, %v, %v; git %q, %v", rev, got, ok, err, want, werr)
		}
		wantPlain, plainErr := Run(fx.main, "rev-parse", rev)
		if g, e := RevParse(fx.main, rev); (e == nil) != (plainErr == nil) || g != wantPlain {
			t.Errorf("RevParse(%q) = %q, %v; git %q, %v", rev, g, e, wantPlain, plainErr)
		}
	}
}

func checkDiscovery(t *testing.T, fx *readFixture) {
	for name, dir := range map[string]string{
		"top": fx.main, "subdirectory": fx.sub, "linked worktree": fx.wt, "detached linked worktree": fx.wtd,
		"separate git dir": fx.sep, "gitfile": fx.holder, "unborn": fx.unborn,
		"linked worktree subdirectory": filepath.Join(fx.wt, "dir", "sub"),
	} {
		t.Run(name, func(t *testing.T) {
			loc, ok := discover(dir, os.LookupEnv)
			if !ok {
				t.Fatalf("discover(%s) declined", dir)
			}
			out := mustGit(t, dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir", "--show-prefix")
			lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
			// --show-prefix prints an empty line at the top level, which Run trims.
			for len(lines) < 4 {
				lines = append(lines, "")
			}
			if got, want := canonPath(loc.workTree), canonPath(lines[0]); got != want {
				t.Errorf("work tree = %s, git %s", got, want)
			}
			if got, want := canonPath(loc.gitDir), canonPath(lines[1]); got != want {
				t.Errorf("git dir = %s, git %s", got, want)
			}
			if got, want := canonPath(loc.commonDir), canonPath(lines[2]); got != want {
				t.Errorf("common dir = %s, git %s", got, want)
			}
			if loc.prefix != lines[3] {
				t.Errorf("prefix = %q, git %q", loc.prefix, lines[3])
			}
			top, err := TopLevel(dir)
			if err != nil || canonPath(top) != canonPath(lines[0]) {
				t.Errorf("TopLevel = %q, %v; git %s", top, err, lines[0])
			}
			common, err := CommonDir(dir)
			if err != nil || canonPath(common) != canonPath(lines[2]) {
				t.Errorf("CommonDir = %q, %v; git %s", common, err, lines[2])
			}
		})
	}
	t.Run("a real submodule is left to git", func(t *testing.T) {
		// Its git directory sets core.worktree.
		if _, ok := discover(fx.subm, os.LookupEnv); ok {
			t.Errorf("discover(%s) answered; core.worktree is set", fx.subm)
		}
		want := mustGit(t, fx.subm, "rev-parse", "--show-toplevel")
		if top, err := TopLevel(fx.subm); err != nil || canonPath(top) != canonPath(want) {
			t.Errorf("TopLevel = %q, %v; git %s", top, err, want)
		}
	})
	t.Run("through a symbolic link", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(fx.sub, link); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
		loc, ok := discover(link, os.LookupEnv)
		if !ok {
			t.Fatalf("discover(%s) declined", link)
		}
		want := mustGit(t, link, "rev-parse", "--path-format=absolute", "--show-toplevel")
		if canonPath(loc.workTree) != canonPath(want) || loc.prefix != "dir/sub/" {
			t.Errorf("through a link: work tree %s prefix %q, git %s", loc.workTree, loc.prefix, want)
		}
	})
}

func checkHeadBranch(t *testing.T, fx *readFixture) {
	for _, c := range []struct {
		name, dir string
		inProcess bool
	}{
		{"main", fx.main, true},
		{"subdirectory", fx.sub, true},
		{"linked worktree", fx.wt, true},
		{"detached", fx.wtd, true},
		{"unborn", fx.unborn, true},
		{"separate git dir", fx.sep, true},
		{"a tag has the branch's name", fx.amb, false},
		{"not a repository", t.TempDir(), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			want, werr := Run(c.dir, "symbolic-ref", "--short", "-q", "HEAD")
			branch, has, ok := headBranchInProcess(c.dir, os.LookupEnv)
			if ok != c.inProcess {
				t.Errorf("answered in process = %v, want %v", ok, c.inProcess)
			}
			if ok && (has != (werr == nil) || branch != want) {
				t.Errorf("in process %q, %v; git %q, %v", branch, has, want, werr)
			}
			if got, has := HeadBranch(c.dir); has != (werr == nil) || got != want {
				t.Errorf("HeadBranch = %q, %v; git %q, %v", got, has, want, werr)
			}
		})
	}
}

func checkRefTip(t *testing.T, fx *readFixture) {
	listing := mustGit(t, fx.main, "for-each-ref", "--format=%(refname) %(objectname)")
	tips := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(listing, "\r\n", "\n"), "\n") {
		name, oid, _ := strings.Cut(line, " ")
		tips[name] = oid
	}
	if len(tips) < 15 || tips["refs/heads/broken"] != fx.fakeSHA {
		// git lists a ref whose object is missing; the reads do not look for it.
		t.Fatalf("only %d refs listed:\n%s", len(tips), listing)
	}
	for name, oid := range tips {
		got, ok := refTipInProcess(fx.main, name, os.LookupEnv)
		if !ok || got != oid {
			t.Errorf("%s: in process %q, %v; git %s", name, got, ok, oid)
		}
	}
	// No ref, a ref to a missing object or one that is not a ref (which
	// for-each-ref skips), and names with refs beneath them (which it lists
	// all): the answer is git's, from the reads when they answer.
	for _, name := range []string{
		"refs/heads/nope", "refs/remotes/origin/nope", "refs/tags/nope", "refs/heads/dangling", "refs/heads/garbage",
		"refs/heads/feature", "refs/heads", "refs/remotes/origin", "refs/heads/main/x",
	} {
		want := mustGit(t, fx.main, "for-each-ref", "--format=%(objectname)", name)
		if got, ok := refTipInProcess(fx.main, name, os.LookupEnv); ok && got != want {
			t.Errorf("%s: in process %q, git %q", name, got, want)
		}
		if got, err := RefTip(fx.main, name); err != nil || got != want {
			t.Errorf("RefTip(%s) = %q, %v; git %q", name, got, err, want)
		}
	}
	for _, name := range []string{"refs/heads/nope", "refs/remotes/origin/nope", "refs/tags/nope", "refs/heads/dangling"} {
		if got, ok := refTipInProcess(fx.main, name, os.LookupEnv); !ok || got != "" {
			t.Errorf("%s: in process %q, %v; want no ref, answered", name, got, ok)
		}
	}
	for _, name := range []string{"refs/heads/garbage", "refs/heads/feature", "refs/heads"} {
		if _, ok := refTipInProcess(fx.main, name, os.LookupEnv); ok {
			t.Errorf("%s: answered in process", name)
		}
	}
	for _, name := range []string{"refs/heads/main", "refs/heads/nope"} {
		want := mustGit(t, fx.wt, "for-each-ref", "--format=%(objectname)", name)
		if got, err := RefTip(fx.wt, name); err != nil || got != want {
			t.Errorf("RefTip(%s) in the worktree = %q, %v; git %q", name, got, err, want)
		}
	}
}

func checkSubject(t *testing.T, fx *readFixture) {
	for _, rev := range []string{fx.shas["c1"], fx.shas["c2"], fx.shas["c3"], fx.shas["c4"], "HEAD", "refs/heads/feature/x", "refs/heads/main", "refs/tags/light"} {
		want := mustGit(t, fx.main, "log", "-1", "--format=%s", rev)
		got, ok := subjectInProcess(fx.main, rev, os.LookupEnv)
		if !ok {
			t.Errorf("Subject(%s) not answered in process", rev)
		} else if got != want {
			t.Errorf("Subject(%s) = %q in process, git %q", rev, got, want)
		}
		if got, err := Subject(fx.main, rev); err != nil || got != want {
			t.Errorf("Subject(%s) = %q, %v; git %q", rev, got, err, want)
		}
	}
	// A tag is a commit's name, not a commit; a form the reads do not take
	// goes to git.
	for _, rev := range []string{"refs/tags/ann", "HEAD~1", "main", fx.shas["c2"][:10]} {
		want := mustGit(t, fx.main, "log", "-1", "--format=%s", rev)
		if _, ok := subjectInProcess(fx.main, rev, os.LookupEnv); ok {
			t.Errorf("Subject(%s) answered in process", rev)
		}
		if got, err := Subject(fx.main, rev); err != nil || got != want {
			t.Errorf("Subject(%s) = %q, %v; git %q", rev, got, err, want)
		}
	}
	if _, err := Subject(fx.main, fx.fakeSHA); err == nil {
		t.Errorf("Subject of a missing commit succeeded")
	}
}

// TestSubjectMatchesGitForMessages puts the subject of unusual messages next
// to git's.
func TestSubjectMatchesGitForMessages(t *testing.T) {
	dir := initFixtureRepo(t, t.TempDir(), "msgs")
	mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "seed")
	tree := mustGit(t, dir, "rev-parse", "HEAD^{tree}")
	parent := mustGit(t, dir, "rev-parse", "HEAD")
	for name, c := range map[string]struct {
		message string
		ascii   bool
	}{
		"one line":                {"just one\n", true},
		"no newline":              {"bare", true},
		"two lines":               {"first\nsecond\n\nbody\n", true},
		"blank lines first":       {"\n\n  \nfirst\nsecond\n", true},
		"trailing white space":    {"first \t\nsecond  \r\n\nbody", true},
		"leading white space":     {"  indented\n  second\n", true},
		"empty":                   {"", true},
		"only blank lines":        {"\n \n", true},
		"white space between":     {"a\n \t \nb\n", true},
		"not ascii":               {"café au lait\n", false},
		"form feed and tab":       {"a\fb\tc\nd\n", true},
		"long line":               {strings.Repeat("x", 5000) + "\nsecond\n", true},
		"carriage return only":    {"one\rtwo\n", true},
		"numbers and punctuation": {"fix(x): handle 100% of #12 (again)\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			body := "tree " + tree + "\nparent " + parent + "\nauthor " + fiIdent + "\ncommitter " + fiIdent + "\n\n" + c.message
			sha, err := RunInput(dir, []byte(body), "hash-object", "-t", "commit", "-w", "--stdin")
			if err != nil {
				t.Fatal(err)
			}
			want := mustGit(t, dir, "log", "-1", "--format=%s", sha)
			got, ok := subjectInProcess(dir, sha, os.LookupEnv)
			if ok != c.ascii {
				t.Errorf("answered in process = %v, want %v", ok, c.ascii)
			}
			if ok && got != want {
				t.Errorf("subject = %q in process, git %q", got, want)
			}
			if got, err := Subject(dir, sha); err != nil || got != want {
				t.Errorf("Subject = %q, %v; git %q", got, err, want)
			}
		})
	}
	t.Run("a named encoding", func(t *testing.T) {
		body := "tree " + tree + "\nparent " + parent + "\nauthor " + fiIdent + "\ncommitter " + fiIdent + "\nencoding ISO-8859-1\n\ncaf\xe9\n"
		sha, err := RunInput(dir, []byte(body), "hash-object", "-t", "commit", "-w", "--stdin")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := subjectInProcess(dir, sha, os.LookupEnv); ok {
			t.Errorf("answered in process for a commit that names an encoding")
		}
		want := mustGit(t, dir, "log", "-1", "--format=%s", sha)
		if got, err := Subject(dir, sha); err != nil || got != want {
			t.Errorf("Subject = %q, %v; git %q", got, err, want)
		}
	})
}

func checkUnmerged(t *testing.T, fx *readFixture) {
	for _, c := range []struct {
		name, dir string
		want      bool
	}{
		{"conflicted index", fx.conf, true},
		{"clean index", fx.main, false},
		{"linked worktree", fx.wt, false},
		{"no index yet", fx.unborn, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			listing := mustGit(t, c.dir, "ls-files", "--unmerged")
			if (listing != "") != c.want {
				t.Fatalf("git lists %q, want unmerged = %v", listing, c.want)
			}
			got, ok := hasUnmergedInProcess(c.dir, os.LookupEnv)
			if !ok || got != c.want {
				t.Errorf("in process %v, %v; want %v", got, ok, c.want)
			}
			if got, err := HasUnmerged(c.dir); err != nil || got != c.want {
				t.Errorf("HasUnmerged = %v, %v; want %v", got, err, c.want)
			}
		})
	}
	// ls-files lists only what is below the directory it runs in.
	if _, ok := hasUnmergedInProcess(fx.sub, os.LookupEnv); ok {
		t.Errorf("answered in process from a subdirectory")
	}
	if got, err := HasUnmerged(fx.sub); err != nil || got {
		t.Errorf("HasUnmerged(subdirectory) = %v, %v", got, err)
	}
}

func checkGitPaths(t *testing.T, fx *readFixture) {
	names := []string{"rebase-merge", "rebase-apply/applying", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "sequencer", "BISECT_LOG", "info/exclude", "HEAD", "index", "config", "hooks/pre-commit"}
	for name, dir := range map[string]string{"main": fx.main, "subdirectory": fx.sub, "linked worktree": fx.wt, "separate git dir": fx.sep, "gitfile": fx.holder} {
		t.Run(name, func(t *testing.T) {
			args := []string{"rev-parse", "--path-format=absolute"}
			for _, n := range names {
				args = append(args, "--git-path", n)
			}
			want := strings.Split(strings.ReplaceAll(mustGit(t, dir, args...), "\r\n", "\n"), "\n")
			got, ok := gitPathsInProcess(dir, names, os.LookupEnv)
			if !ok {
				t.Fatalf("not answered in process")
			}
			for i, n := range names {
				if canonPath(got[i]) != canonPath(want[i]) {
					t.Errorf("%s: in process %s, git %s", n, got[i], want[i])
				}
			}
			viaExported, err := GitPaths(dir, names...)
			if err != nil || len(viaExported) != len(names) {
				t.Fatalf("GitPaths = %v, %v", viaExported, err)
			}
		})
	}
	// Names the reads do not place go to git, whose paths are made absolute.
	odd := []string{"objects/info/alternates", "logs/HEAD", "info/sparse-checkout", "refs/heads/main"}
	if _, ok := gitPathsInProcess(fx.main, odd[:1], os.LookupEnv); ok {
		t.Errorf("a name outside the table was answered in process")
	}
	want := strings.Split(strings.ReplaceAll(mustGit(t, fx.sub, "rev-parse", "--path-format=absolute", "--git-path", odd[1], "--git-path", odd[2]), "\r\n", "\n"), "\n")
	got, err := GitPaths(fx.sub, odd[1], odd[2])
	if err != nil || len(got) != 2 || canonPath(got[0]) != canonPath(want[0]) || canonPath(got[1]) != canonPath(want[1]) {
		t.Errorf("GitPaths(odd names) = %v, %v; git %v", got, err, want)
	}
}

func checkProbe(t *testing.T, fx *readFixture) {
	for name, dir := range map[string]string{"top": fx.main, "subdirectory": fx.sub, "linked worktree": fx.wt, "gitfile": fx.holder, "separate git dir": fx.sep} {
		t.Run(name, func(t *testing.T) {
			want := mustGit(t, dir, "-c", "safe.directory=*", "rev-parse", "--is-inside-work-tree", "--show-prefix")
			got, ok := workTreeProbeInProcess(dir, os.LookupEnv)
			if !ok || got != want {
				t.Errorf("in process %q, %v; git %q", got, ok, want)
			}
			if got, err := WorkTreeProbe(dir); err != nil || got != want {
				t.Errorf("WorkTreeProbe = %q, %v; git %q", got, err, want)
			}
		})
	}
	// Inside a git directory and in no repository, git answers.
	gitDir := filepath.Join(fx.main, ".git")
	want := mustGit(t, gitDir, "-c", "safe.directory=*", "rev-parse", "--is-inside-work-tree", "--show-prefix")
	if got, err := WorkTreeProbe(gitDir); err != nil || got != want {
		t.Errorf("WorkTreeProbe(.git) = %q, %v; git %q", got, err, want)
	}
	if _, ok := workTreeProbeInProcess(gitDir, os.LookupEnv); ok {
		t.Errorf("answered in process inside a git directory")
	}
	if _, err := WorkTreeProbe(t.TempDir()); err == nil {
		t.Errorf("WorkTreeProbe outside a repository succeeded")
	}
}

func checkTreeEntries(t *testing.T, fx *readFixture) {
	// listing is git's own answer for every path of a commit's tree: the type
	// ls-tree gives each (trees included), by path.
	listing := func(commit string) map[string]string {
		out := mustGit(t, fx.main, "ls-tree", "-r", "-t", "--full-tree", commit)
		types := map[string]string{}
		for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
			meta, path, _ := strings.Cut(line, "\t")
			types[path] = strings.Fields(meta)[1]
		}
		return types
	}
	c1, c2, c3 := listing(fx.shas["c1"]), listing(fx.shas["c2"]), listing(fx.shas["c3"])
	if c3["dir/sub/c.txt"] != "blob" || c3["dir"] != "tree" || c3["subm"] != "commit" || c2["subm"] != "" {
		t.Fatalf("unexpected listings:\n%v\n%v", c2, c3)
	}
	paths := []string{"a.txt", "dir", "dir/b.txt", "dir/sub", "dir/sub/c.txt", "sp ace.txt", "subm", "nope", "dir/nope", "a.txt/x", "dir/b.txt/x", "Dir", "dir/sub/c.txt/y"}
	for rev, types := range map[string]map[string]string{
		fx.shas["c3"]: c3, "HEAD": c3, "refs/heads/main": c3, "refs/heads/feature/x": c2, "refs/tags/ann": c2, "refs/tags/ann2": c2,
		"refs/tags/light": c1, fx.shas["c1"]: c1,
	} {
		for _, path := range paths {
			wantType, wantFound := types[path]
			et, found, ok := treeEntryInProcess(fx.main, rev, path, os.LookupEnv)
			if !ok {
				t.Errorf("%s:%s not answered in process", rev, path)
				continue
			}
			if found != wantFound || (found && et != wantType) {
				t.Errorf("%s:%s = %q, %v in process; git %q, %v", rev, path, et, found, wantType, wantFound)
			}
			gotBlob, err := FileExistsAtRev(fx.main, rev, path)
			if err != nil || gotBlob != (wantFound && wantType == "blob") {
				t.Errorf("FileExistsAtRev(%s, %s) = %v, %v; git says %q, %v", rev, path, gotBlob, err, wantType, wantFound)
			}
			gotAny, err := PathExistsAtRev(fx.main, rev, path)
			if err != nil || gotAny != wantFound {
				t.Errorf("PathExistsAtRev(%s, %s) = %v, %v; git says %v", rev, path, gotAny, err, wantFound)
			}
		}
	}
	// A rev that is no commit, and paths of forms the lookup does not take,
	// go to git, which gives the answers (or errors) it always did.
	for _, rev := range []string{fx.fakeSHA, "refs/heads/nope", "refs/tags/treetag"} {
		if _, _, ok := treeEntryInProcess(fx.main, rev, "a.txt", os.LookupEnv); ok {
			t.Errorf("%s: answered in process for a rev that is no commit", rev)
		}
		if _, err := FileExistsAtRev(fx.main, rev, "a.txt"); err == nil {
			t.Errorf("FileExistsAtRev(%s) succeeded", rev)
		}
	}
	for _, path := range []string{"", "dir/", "./a.txt", "../a.txt", "dir//b.txt", "/a.txt"} {
		if _, _, ok := treeEntryInProcess(fx.main, "HEAD", path, os.LookupEnv); ok {
			t.Errorf("path %q answered in process", path)
		}
	}
	if got, err := FileExistsAtRev(fx.main, "main", "a.txt"); err != nil || !got {
		t.Errorf("FileExistsAtRev with a short branch name = %v, %v", got, err)
	}
}

func checkHasPathAt(t *testing.T, fx *readFixture) {
	c3 := fx.shas["c3"]
	var names []string
	cases := []struct{ rev, path string }{
		{c3, "a.txt"}, {c3, "dir"}, {c3, "dir/sub/c.txt"}, {c3, "sp ace.txt"}, {c3, "nope"}, {c3, "dir/nope"},
		{"HEAD", "a.txt"}, {"refs/heads/main", "dir/b.txt"}, {fx.shas["c1"], "a.txt"}, {fx.fakeSHA, "a.txt"},
	}
	for _, c := range cases {
		names = append(names, c.rev+":"+c.path)
	}
	out, err := RunInput(fx.main, []byte(strings.Join(names, "\n")+"\n"), "cat-file", "--batch-check")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for i, c := range cases {
		want := !strings.HasSuffix(lines[i], " missing")
		if got := HasPathAt(fx.main, c.rev, c.path); got != want {
			t.Errorf("HasPathAt(%s, %s) = %v; git %q", c.rev, c.path, got, lines[i])
		}
		if hasPathAt(fx.main, c.rev, c.path, os.LookupEnv) && !want {
			t.Errorf("hasPathAt(%s, %s) = yes in process; git %q", c.rev, c.path, lines[i])
		}
	}
	// A submodule's commit is not in this repository: cat-file -e says no,
	// where cat-file --batch-check says "submodule".
	_, gitErr := Run(fx.main, "cat-file", "-e", c3+":subm")
	if got := HasPathAt(fx.main, c3, "subm"); got != (gitErr == nil) || got {
		t.Errorf("HasPathAt(%s, subm) = %v; git cat-file -e: %v", c3, got, gitErr)
	}
	if !hasPathAt(fx.main, c3, "dir/sub/c.txt", os.LookupEnv) {
		t.Errorf("a path in a commit was not found in process")
	}
	if HasCommit(fx.main, fx.fakeSHA) || !HasCommit(fx.main, c3) || !HasCommit(fx.main, "refs/tags/ann") || HasCommit(fx.main, "refs/tags/treetag") {
		t.Errorf("HasCommit disagrees with git")
	}
}

// checkFixtureShape keeps the fixture honest: its refs are the kinds the
// tests above claim they are.
func checkFixtureShape(t *testing.T, fx *readFixture) {
	packed, err := os.ReadFile(filepath.Join(fx.main, ".git", "packed-refs"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, line := range strings.Split(string(packed), "\n") {
		if _, name, ok := strings.Cut(line, " "); ok && strings.HasPrefix(name, "refs/") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, want := range []string{"refs/heads/both", "refs/heads/feature/x", "refs/heads/main", "refs/tags/ann", "refs/tags/light", "refs/tags/treetag"} {
		if i := sort.SearchStrings(names, want); i == len(names) || names[i] != want {
			t.Errorf("packed-refs lacks %s: %v", want, names)
		}
	}
	for _, loose := range []string{"refs/heads/main", "refs/heads/both", "refs/heads/loose-only"} {
		if _, err := os.Stat(filepath.Join(fx.main, ".git", filepath.FromSlash(loose))); err != nil {
			t.Errorf("%s is not loose: %v", loose, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.alt, ".git", "objects", "info", "alternates")); err != nil {
		t.Errorf("alt has no alternates: %v", err)
	}
	if got := mustGit(t, fx.main, "rev-parse", "HEAD"); got != fx.shas["c4"] {
		t.Errorf("HEAD = %s, want c4 %s", got, fx.shas["c4"])
	}
}

// checkObjectsGitReadsDifferently holds a repository whose object store git
// reads differently from go-git (a replace ref swaps one object for another,
// alternates lend another repository's objects) to git's answers: the object
// reads decline for it, its refs are still read in process, and every
// exported question answers what git does. parent is the revision of HEAD's
// first parent, or "" when HEAD has none.
func checkObjectsGitReadsDifferently(t *testing.T, fx *readFixture, dir, parent string) {
	t.Helper()
	r := newReader(dir, os.LookupEnv)
	if r == nil {
		t.Fatalf("%s: the repository itself is declined", dir)
	}
	defer r.close()
	if _, ok := r.objects(); ok {
		t.Errorf("objects are read in process")
	}
	if sha, res := answerRev(dir, "HEAD", os.LookupEnv); res != resFound || sha != mustGit(t, dir, "rev-parse", "HEAD") {
		t.Errorf("HEAD = %q, %d in process; a ref is read whatever the objects are", sha, res)
	}
	for _, rev := range []string{"HEAD^{commit}", "HEAD^{tree}", "HEAD^", "HEAD:nope"} {
		if _, res := answerRev(dir, rev, os.LookupEnv); res != resDecline {
			t.Errorf("%s = %d in process, want left to git", rev, res)
		}
	}
	revs := []string{"HEAD", "HEAD^{commit}", "HEAD^{tree}", "HEAD^", "HEAD^1", "HEAD:nope", fx.fakeSHA + "^{commit}"}
	if parent != "" {
		pid := mustGit(t, dir, "rev-parse", parent)
		revs = append(revs, pid+"^{commit}", pid+"^{tree}", pid+"^")
	}
	for _, rev := range revs {
		want, werr := Run(dir, "rev-parse", "--verify", "--quiet", rev)
		got, ok, err := Resolve(dir, rev)
		if werr == nil && (err != nil || !ok || got != want) || werr != nil && (err != nil || ok) {
			t.Errorf("Resolve(%q) = %q, %v, %v; git %q, %v", rev, got, ok, err, want, werr)
		}
	}
	commitRevs := []string{"HEAD", fx.fakeSHA}
	if parent != "" {
		commitRevs = append(commitRevs, mustGit(t, dir, "rev-parse", parent))
	}
	for _, rev := range commitRevs {
		_, catErr := Run(dir, "cat-file", "-e", rev+"^{commit}")
		if got := HasCommit(dir, rev); got != (catErr == nil) {
			t.Errorf("HasCommit(%q) = %v; git cat-file -e: %v", rev, got, catErr)
		}
		want, werr := Run(dir, "log", "-1", "--format=%s", rev)
		got, err := Subject(dir, rev)
		if (err == nil) != (werr == nil) || got != want {
			t.Errorf("Subject(%q) = %q, %v; git %q, %v", rev, got, err, want, werr)
		}
	}
	for _, path := range []string{"nope", ".gitkeep"} {
		_, catErr := Run(dir, "cat-file", "-e", "HEAD:"+path)
		if got := HasPathAt(dir, "HEAD", path); got != (catErr == nil) {
			t.Errorf("HasPathAt(HEAD, %s) = %v; git cat-file -e: %v", path, got, catErr)
		}
	}
}
