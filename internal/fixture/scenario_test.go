package fixture

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/develdeco/jig/internal/gitbuild"
	"github.com/develdeco/jig/internal/gitx"
)

// commitIn makes a commit of everything in dir with the fixture's pinned
// identity and dates.
func commitIn(t *testing.T, dir, msg string) {
	t.Helper()
	gitbuild.New(t, append(append([]string{}, hermeticGitEnv...), identityEnv...)).Commit(dir, msg)
}

// writeIn writes a file under dir.
func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scratchDirs is n new directories for a scenario to fill.
func scratchDirs(t *testing.T, n int) []string {
	t.Helper()
	base := t.TempDir()
	var dirs []string
	for i := 0; i < n; i++ {
		dirs = append(dirs, filepath.Join(base, fmt.Sprintf("dir-%d", i)))
	}
	return dirs
}

// buildSmallHistory builds, with git, a history that spans two directories: a work
// tree in the first whose first commit holds files naming both directories (in
// the two spellings a root has in a file), a bare remote in the second, a side
// branch, a merge, and commits the remote never had; and a second repository in
// the second directory that names its own directory in an old commit.
func buildSmallHistory(t *testing.T, dirs []string) {
	t.Helper()
	work, remote, other := filepath.Join(dirs[0], "work"), filepath.Join(dirs[1], "remote.git"), filepath.Join(dirs[1], "other")
	aaa, zzz := filepath.Join(dirs[0], "aaa"), filepath.Join(dirs[1], "zzz")
	plus := func(files map[string]string, name, body string) map[string]string {
		out := map[string]string{name: body}
		for k, v := range files {
			if k != name {
				out[k] = v
			}
		}
		return out
	}
	gitDirOf := func(dir string) string { return filepath.Join(dir, ".git") }

	first := map[string]string{"manifest.yaml": "work: " + dirs[0] + "\nremote: " + filepath.ToSlash(remote) + "\n", "plain.txt": "names no directory\n"}
	second := plus(first, "second.txt", "second\n")
	side := plus(first, "side.txt", "side\n")
	merged := plus(second, "side.txt", "side\n")
	workCommits := []fcommit{
		{"main", "first", nil, first},
		{"main", "second", []string{"first"}, second},
		{"side", "side", []string{"first"}, side},
		{"main", "merge side", []string{"second", "side"}, merged},
	}
	ids := importRepo(t, gitDirOf(work), "main", false, workCommits)
	for name, body := range merged {
		writeIn(t, work, name, body)
	}
	pin(t, work, "reset", "-q")
	// The remote has the first commit only: the others were never pushed.
	importRepo(t, remote, "main", true, workCommits[:1])
	writeIn(t, gitDirOf(work), "config", readFileString(t, filepath.Join(gitDirOf(work), "config"))+"[remote \"origin\"]\n\turl = "+filepath.ToSlash(remote)+"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n")
	writeIn(t, gitDirOf(work), "refs/remotes/origin/main", ids["first"]+"\n")

	// Files that name a commit of another repository, which moves when its
	// files are pointed at the copy: one tracked, in two versions, and one not.
	// An id that moves twice: other (rewritten before zzz in a pass) names a
	// commit of zzz, so other's commits move again once zzz's are known. A file
	// of aaa (rewritten before both, holding no directory, only the id) and one
	// no repository tracks name other's commit.
	zzzFiles := map[string]string{"zzz.yaml": "dir: " + dirs[1] + "\n"}
	zzzIDs := importRepo(t, gitDirOf(zzz), "main", false, []fcommit{{"main", "zzz first", nil, zzzFiles}})
	checkOut(t, zzz, zzzFiles)
	o1 := map[string]string{"other.yaml": "dir: " + dirs[1] + "\n"}
	o2 := plus(o1, "later.txt", "later\n")
	o3 := plus(o2, "ids.txt", "seen "+ids["first"]+"\n")
	o4 := plus(o3, "ids.txt", "seen "+ids["first"]+"\nand again\n")
	o5 := plus(o4, "zzz-seen.txt", "zzz "+zzzIDs["zzz first"]+"\n")
	otherIDs := importRepo(t, gitDirOf(other), "main", false, []fcommit{
		{"main", "other first", nil, o1},
		{"main", "other later", []string{"other first"}, o2},
		{"main", "other sees a commit", []string{"other later"}, o3},
		{"main", "other sees it again", []string{"other sees a commit"}, o4},
		{"main", "other names zzz", []string{"other sees it again"}, o5},
	})
	checkOut(t, other, o5)
	aaaFiles := map[string]string{"seen.txt": "other " + otherIDs["other names zzz"] + "\n"}
	importRepo(t, gitDirOf(aaa), "main", false, []fcommit{{"main", "aaa names other", nil, aaaFiles}})
	checkOut(t, aaa, aaaFiles)
	writeIn(t, dirs[1], "state.txt", "first commit "+ids["first"]+"\n")
	writeIn(t, dirs[1], "twice.txt", "other "+otherIDs["other names zzz"]+"\n")
}

// sameTrees fails t unless the copy read as got is the direct build read as
// want, file for file: directories, files, refs and reflogs (which name
// commits by id, so the commits), object files and indexes.
func sameTrees(t *testing.T, want, got *tree) {
	t.Helper()
	sameNames(t, "directories", want.dirs, got.dirs)
	sameNames(t, "files", want.files, got.files)
	sameNames(t, "object files and directories", want.objects, got.objects)
	sameNames(t, "indexes", want.indexes, got.indexes)
	sameNames(t, "refs and reflogs", want.refs, got.refs)
	for _, name := range sortedKeys(want.refs) {
		if g, ok := got.refs[name]; ok && !bytes.Equal(want.refs[name], g) {
			t.Errorf("%s differs:\ndirect build:\n%s\ncopy:\n%s", name, want.refs[name], g)
		}
	}
	for _, name := range sortedKeys(want.files) {
		if g, ok := got.files[name]; ok && !bytes.Equal(want.files[name], g) {
			t.Errorf("%s differs:\ndirect build:\n%s\ncopy:\n%s", name, show(want.files[name]), show(g))
		}
	}
}

// gitRepos lists the work trees and bare repositories under dir.
func gitRepos(t *testing.T, dir string) (works, bares []string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			works = append(works, filepath.Dir(p))
			return filepath.SkipDir
		}
		if d.IsDir() && strings.HasSuffix(d.Name(), ".git") {
			bares = append(bares, p)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return works, bares
}

// scenarioBuilds are the scenarios the equivalence tests copy: what each
// builds, and a check of what is only true of it.
var scenarioBuilds = []struct {
	name  string
	build func(t *testing.T, dirs []string) (*Fixture, error)
	check func(t *testing.T, dirs []string, fx *Fixture)
}{
	{
		name: "a history with a root in an old commit",
		build: func(t *testing.T, dirs []string) (*Fixture, error) {
			buildSmallHistory(t, dirs)
			return nil, nil
		},
		check: func(t *testing.T, dirs []string, fx *Fixture) {
			work := filepath.Join(dirs[0], "work")
			if got := blobIn(t, work, "main~2", "manifest.yaml"); !strings.Contains(got, dirs[0]) || !strings.Contains(got, filepath.ToSlash(filepath.Join(dirs[1], "remote.git"))) {
				t.Errorf("the first commit's manifest = %q, want it to name %s and the remote", got, dirs[0])
			}
			other := filepath.Join(dirs[1], "other")
			if got := blobIn(t, other, "HEAD~4", "other.yaml"); !strings.Contains(got, dirs[1]) {
				t.Errorf("the other repository's first commit = %q, want it to name %s", got, dirs[1])
			}
			first := revParse(t, work, "main~2")
			for _, rev := range []string{"HEAD", "HEAD^"} {
				if got := blobIn(t, other, rev, "ids.txt"); !strings.Contains(got, first) {
					t.Errorf("ids.txt at %s = %q, want the first commit's new id %s", rev, got, first)
				}
			}
			// The ids that moved twice name commits that exist, in a file of a
			// repository that holds no directory and in one no repository tracks.
			for _, f := range []string{filepath.Join(dirs[0], "aaa", "seen.txt"), filepath.Join(dirs[1], "twice.txt")} {
				id := strings.TrimSpace(strings.TrimPrefix(readFileString(t, f), "other "))
				if got := subjectIn(t, other, id); got != "other names zzz" {
					t.Errorf("%s names %s, whose subject is %q, want the commit that names zzz", f, id, got)
				}
			}
			zid := strings.TrimSpace(strings.TrimPrefix(readFileString(t, filepath.Join(other, "zzz-seen.txt")), "zzz "))
			if got := subjectIn(t, filepath.Join(dirs[1], "zzz"), zid); got != "zzz first" {
				t.Errorf("zzz-seen.txt names %s, whose subject is %q", zid, got)
			}
			if got := readFileString(t, filepath.Join(dirs[1], "state.txt")); !strings.Contains(got, first) {
				t.Errorf("state.txt = %q, want the first commit's new id %s", got, first)
			}
		},
	},
	{
		name: "a fixture with a commit on top of its first",
		build: func(t *testing.T, dirs []string) (*Fixture, error) {
			fx, err := Build(dirs[0], Opts{SecondRepo: true, Home: dirs[1]})
			if err != nil {
				return nil, err
			}
			writeIn(t, fx.RepoDir, "built.txt", "built on top\n")
			commitIn(t, fx.RepoDir, "built on top")
			return fx, nil
		},
		check: func(t *testing.T, dirs []string, fx *Fixture) {
			if fx == nil || fx.Dir != dirs[0] || fx.Home != dirs[1] || !strings.HasPrefix(fx.RepoDir, dirs[0]) {
				t.Fatalf("Fixture = %+v, want it in %v", fx, dirs)
			}
			if got := blobIn(t, fx.RepoDir, "HEAD^", ".claude/jig.yaml"); !strings.Contains(got, filepath.ToSlash(dirs[0])) && !strings.Contains(got, dirs[0]) {
				t.Errorf("the manifest in the base commit = %q, want it to name %s", got, dirs[0])
			}
		},
	},
}

// TestAScenarioCopyIsTheDirectBuild: a scenario copied into directories and
// the build function run directly in the same directories are the same state,
// file for file: the same files, refs, reflogs and objects (so the same commit
// ids, whose files hold the directories), indexes and history; git finds each
// repository sound and clean; the Fixture is the direct build's; and no file
// names a path of the template.
func TestAScenarioCopyIsTheDirectBuild(t *testing.T) {
	for _, sc := range scenarioBuilds {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			dirs := scratchDirs(t, 2)
			opts := Opts{}
			var built atomic.Int32
			build := func(dirs []string) (*Fixture, error) {
				built.Add(1)
				return sc.build(t, dirs)
			}

			copied, err := Scenario(sc.name, opts, dirs, build)
			if err != nil {
				t.Fatal(err)
			}
			if n := built.Load(); n > 1 {
				t.Errorf("the build ran %d times for one copy", n)
			}
			var got []*tree
			for _, d := range dirs {
				got = append(got, readTree(t, d))
			}
			sc.check(t, dirs, copied)
			works, bares := gitRepos(t, filepath.Dir(dirs[0]))
			// Git itself finds the repositories that have an index sound and clean,
			// and one remote sound; the others are compared with the direct build below.
			fscked := false
			for _, w := range works {
				if _, err := os.Stat(filepath.Join(w, ".git", "index")); err != nil {
					continue
				}
				if !fscked {
					git(t, w, "fsck", "--strict", "--no-dangling")
					fscked = true
				}
				if status := git(t, w, "status", "--porcelain"); status != "" {
					t.Errorf("%s: git status = %q, want clean", w, status)
				}
			}
			if len(bares) > 0 {
				git(t, bares[0], "fsck", "--strict", "--no-dangling")
			}

			tpl := scenarioTemplate(t, sc.name, opts, 2)
			olds, _ := rootSpellings(tpl.root, "")
			for i, tr := range got {
				for _, m := range []map[string][]byte{tr.files, tr.refs} {
					for name, data := range m {
						for _, o := range olds {
							if bytes.Contains(bytes.ToLower(data), []byte(strings.ToLower(o))) {
								t.Errorf("dir %d: %s still names the template's directory %s", i, name, o)
							}
						}
					}
				}
			}

			// The direct build, in the same directories.
			for _, d := range dirs {
				if err := os.RemoveAll(d); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			direct, err := sc.build(t, dirs)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(copied, direct) {
				t.Errorf("Fixture = %+v, direct build made %+v", copied, direct)
			}
			for i, d := range dirs {
				sameTrees(t, readTree(t, d), got[i])
			}
		})
	}
}

// scenarioTemplate is the template of the scenario, which a test has called.
func scenarioTemplate(t *testing.T, name string, opts Opts, dirs int) *template {
	t.Helper()
	opts.Home = ""
	scenariosMu.Lock()
	defer scenariosMu.Unlock()
	tpl := scenarios[scenarioKey{name, opts, dirs}]
	if tpl == nil {
		t.Fatalf("no scenario %q", name)
	}
	return tpl
}

// TestAScenarioIsBuiltOncePerBinary: callers racing for one scenario build it
// once and each get a copy; a scenario of another name, or of the same name
// with other options or another number of directories, is built again.
func TestAScenarioIsBuiltOncePerBinary(t *testing.T) {
	var built atomic.Int32
	build := whereBuild(&built)
	const callers = 6
	var wg sync.WaitGroup
	copies := make([][]string, callers)
	for i := range copies {
		copies[i] = scratchDirs(t, 2)
	}
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Scenario("once", Opts{}, copies[i], build); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := built.Load(); n != 1 {
		t.Errorf("the build ran %d times for %d callers, want once", n, callers)
	}
	// Each copy names its own directories.
	for _, dirs := range copies {
		for _, d := range dirs {
			if got := readFileString(t, filepath.Join(d, "where.txt")); got != "built in "+d+whereNL {
				t.Errorf("%s holds %q, want it to name itself", d, got)
			}
		}
	}

	for what, call := range map[string]func() error{
		"another name":    func() error { _, err := Scenario("once-too", Opts{}, scratchDirs(t, 2), build); return err },
		"other options":   func() error { _, err := Scenario("once", Opts{Standalone: true}, scratchDirs(t, 2), build); return err },
		"another count":   func() error { _, err := Scenario("once", Opts{}, scratchDirs(t, 3), threeDirs(build)); return err },
		"the same, again": func() error { _, err := Scenario("once", Opts{}, scratchDirs(t, 2), build); return err },
	} {
		before := built.Load()
		if err := call(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		wantBuilt := int32(1)
		if what == "the same, again" {
			wantBuilt = 0
		}
		if got := built.Load() - before; got != wantBuilt {
			t.Errorf("%s: %d builds, want %d", what, got, wantBuilt)
		}
	}
}

// threeDirs is build for a scenario of three directories, of which it fills
// the first two.
func threeDirs(build func([]string) (*Fixture, error)) func([]string) (*Fixture, error) {
	return func(dirs []string) (*Fixture, error) { return build(dirs[:2]) }
}

// TestAScenarioWhoseBuildFailsFailsEveryCaller: a build that returns an error,
// or panics, fails each caller with it, and runs once.
func TestAScenarioWhoseBuildFailsFailsEveryCaller(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		var built atomic.Int32
		build := func(dirs []string) (*Fixture, error) {
			built.Add(1)
			return nil, errors.New("the build broke")
		}
		for i := 0; i < 3; i++ {
			_, err := Scenario("fails", Opts{}, scratchDirs(t, 1), build)
			if err == nil || !strings.Contains(err.Error(), "the build broke") {
				t.Errorf("call %d: err = %v, want the build's", i, err)
			}
		}
		if n := built.Load(); n != 1 {
			t.Errorf("the failed build ran %d times, want once", n)
		}
	})
	t.Run("panic", func(t *testing.T) {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("the panic was swallowed")
				}
			}()
			Scenario("panics", Opts{}, scratchDirs(t, 1), func([]string) (*Fixture, error) { panic("boom") })
		}()
		_, err := Scenario("panics", Opts{}, scratchDirs(t, 1), func([]string) (*Fixture, error) { return nil, nil })
		if err == nil || !strings.Contains(err.Error(), "did not finish") {
			t.Errorf("a caller after the panic got %v, want a failure", err)
		}
	})
	t.Run("a directory that is not empty", func(t *testing.T) {
		dirs := scratchDirs(t, 1)
		writeIn(t, dirs[0], "x", "x")
		if _, err := Scenario("busy", Opts{}, dirs, func([]string) (*Fixture, error) { return nil, nil }); err == nil {
			t.Errorf("a directory with a file in it was filled")
		}
	})
}

// TestAScenarioCopyCanBeChangedWithoutChangingTheOthers: a commit made in
// one copy changes no other copy, which shares its loose objects.
func TestAScenarioCopyCanBeChangedWithoutChangingTheOthers(t *testing.T) {
	build := func(dirs []string) (*Fixture, error) {
		buildSmallHistory(t, dirs)
		return nil, nil
	}
	a, b := scratchDirs(t, 2), scratchDirs(t, 2)
	for _, dirs := range [][]string{a, b} {
		if _, err := Scenario("shared", Opts{}, dirs, build); err != nil {
			t.Fatal(err)
		}
	}
	workB := filepath.Join(b[0], "work")
	headBefore := revParse(t, workB, "HEAD")
	workA := filepath.Join(a[0], "work")
	writeIn(t, workA, "new.txt", "new\n")
	commitIn(t, workA, "more")
	// Copies share their loose objects as links: removing every one of a copy's
	// own (what a repack and prune leave) must leave the other whole.
	for _, gitDir := range []string{filepath.Join(workA, ".git"), filepath.Join(a[1], "remote.git")} {
		removeLooseObjects(t, gitDir)
	}
	git(t, workB, "fsck", "--strict", "--no-dangling")
	git(t, filepath.Join(b[1], "remote.git"), "fsck", "--strict", "--no-dangling")
	if got := revParse(t, workB, "HEAD"); got != headBefore {
		t.Errorf("another copy's HEAD moved to %s", got)
	}
}

// TestAScenarioIsBuiltDirectlyInADirTheCopyWouldNotServe: a directory that
// quotes differently from the build's gets the build function run in it, as a
// shape's template is not used for one.
func TestAScenarioIsBuiltDirectlyInADirTheCopyWouldNotServe(t *testing.T) {
	var built atomic.Int32
	build := whereBuild(&built)
	if _, err := Scenario("unserved", Opts{}, scratchDirs(t, 2), build); err != nil {
		t.Fatal(err)
	}
	odd := []string{filepath.Join(t.TempDir(), "it's here"), filepath.Join(t.TempDir(), "two")}
	if _, err := Scenario("unserved", Opts{}, odd, build); err != nil {
		t.Fatal(err)
	}
	if n := built.Load(); n != 2 {
		t.Errorf("%d builds, want the template's and the one in the odd directory", n)
	}
	if got := readFileString(t, filepath.Join(odd[0], "where.txt")); got != "built in "+odd[0]+whereNL {
		t.Errorf("the direct build holds %q", got)
	}
}

// pin runs git in dir under the hermetic environment and the fixture's pinned
// identity and dates, so that even a reflog line of a checkout or a fetch is
// the same in every build.
func pin(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitx.RunEnv(dir, append(append([]string{}, hermeticGitEnv...), identityEnv...), args...); err != nil {
		t.Fatalf("git %s (in %s): %v", strings.Join(args, " "), dir, err)
	}
}

// readFileString is the content of the file at p.
func readFileString(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// removeLooseObjects removes every loose object of the git directory gitDir,
// as a repack and a prune of its objects leave it.
func removeLooseObjects(t *testing.T, gitDir string) {
	t.Helper()
	objects := filepath.Join(gitDir, "objects")
	entries, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == 2 {
			if err := os.RemoveAll(filepath.Join(objects, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// checkOut writes the files of a commit into the work tree dir, as a checkout
// of it would.
func checkOut(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		writeIn(t, dir, name, body)
	}
}

// whereBuild is a scenario that holds one file naming each directory it was
// built in (no repository: the tests of when and where a scenario is built
// need none, and a repository costs git processes).
func whereBuild(count *atomic.Int32) func(dirs []string) (*Fixture, error) {
	return func(dirs []string) (*Fixture, error) {
		count.Add(1)
		for _, d := range dirs {
			if err := os.WriteFile(filepath.Join(d, "where.txt"), []byte("built in "+d+"\n"), 0o644); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
}

const whereNL = "\n"
