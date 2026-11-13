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

	"github.com/develdeco/jig/internal/gitx"
)

// commitIn makes a commit of everything in dir with the fixture's pinned
// identity and dates.
func commitIn(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, append(append([]string{}, hermeticGitEnv...), identityEnv...), "commit", "-q", "-m", msg); err != nil {
		t.Fatalf("commit %q in %s: %v", msg, dir, err)
	}
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
	for _, d := range []string{work, other, aaa, zzz} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		pin(t, d, "init", "-q", "-b", "main")
		pin(t, d, "config", "core.autocrlf", "false")
	}
	writeIn(t, work, "manifest.yaml", "work: "+dirs[0]+"\nremote: "+filepath.ToSlash(remote)+"\n")
	writeIn(t, work, "plain.txt", "names no directory\n")
	commitIn(t, work, "first")
	pin(t, work, "clone", "-q", "--bare", ".", remote)
	pin(t, work, "remote", "add", "origin", remote)
	writeIn(t, work, "second.txt", "second\n")
	commitIn(t, work, "second")
	pin(t, work, "checkout", "-q", "-b", "side", "HEAD^")
	writeIn(t, work, "side.txt", "side\n")
	commitIn(t, work, "side")
	pin(t, work, "checkout", "-q", "main")
	if _, err := gitx.RunEnv(work, append(append([]string{}, hermeticGitEnv...), identityEnv...), "merge", "-q", "--no-ff", "-m", "merge side", "side"); err != nil {
		t.Fatal(err)
	}
	pin(t, work, "fetch", "-q", "origin")
	writeIn(t, other, "other.yaml", "dir: "+dirs[1]+"\n")
	commitIn(t, other, "other first")
	writeIn(t, other, "later.txt", "later\n")
	commitIn(t, other, "other later")
	// Files that name a commit of another repository, which moves when its
	// files are pointed at the copy: one tracked, in two versions, and one not.
	first := git(t, work, "rev-list", "--max-parents=0", "main")
	writeIn(t, other, "ids.txt", "seen "+first+"\n")
	commitIn(t, other, "other sees a commit")
	writeIn(t, other, "ids.txt", "seen "+first+"\nand again\n")
	commitIn(t, other, "other sees it again")
	writeIn(t, dirs[1], "state.txt", "first commit "+first+"\n")

	// An id that moves twice: other (rewritten before zzz in a pass) names a
	// commit of zzz, so other's commits move again once zzz's are known. A file
	// of aaa (rewritten before both, holding no directory, only the id) and one
	// no repository tracks name other's commit.
	writeIn(t, zzz, "zzz.yaml", "dir: "+dirs[1]+"\n")
	commitIn(t, zzz, "zzz first")
	writeIn(t, other, "zzz-seen.txt", "zzz "+git(t, zzz, "rev-parse", "HEAD")+"\n")
	commitIn(t, other, "other names zzz")
	writeIn(t, aaa, "seen.txt", "other "+git(t, other, "rev-parse", "HEAD")+"\n")
	commitIn(t, aaa, "aaa names other")
	writeIn(t, dirs[1], "twice.txt", "other "+git(t, other, "rev-parse", "HEAD")+"\n")
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
			if got := git(t, work, "show", git(t, work, "rev-list", "--max-parents=0", "HEAD")+":manifest.yaml"); !strings.Contains(got, dirs[0]) || !strings.Contains(got, filepath.ToSlash(filepath.Join(dirs[1], "remote.git"))) {
				t.Errorf("the first commit's manifest = %q, want it to name %s and the remote", got, dirs[0])
			}
			other := filepath.Join(dirs[1], "other")
			if got := git(t, other, "show", "HEAD~3:other.yaml"); !strings.Contains(got, dirs[1]) {
				t.Errorf("the other repository's first commit = %q, want it to name %s", got, dirs[1])
			}
			first := git(t, work, "rev-list", "--max-parents=0", "main")
			for _, rev := range []string{"HEAD", "HEAD^"} {
				if got := git(t, other, "show", rev+":ids.txt"); !strings.Contains(got, first) {
					t.Errorf("ids.txt at %s = %q, want the first commit's new id %s", rev, got, first)
				}
			}
			// The ids that moved twice name commits that exist, in a file of a
			// repository that holds no directory and in one no repository tracks.
			for _, f := range []string{filepath.Join(dirs[0], "aaa", "seen.txt"), filepath.Join(dirs[1], "twice.txt")} {
				id := strings.TrimSpace(strings.TrimPrefix(readFileString(t, f), "other "))
				if got := git(t, other, "log", "-1", "--format=%s", id); got != "other names zzz" {
					t.Errorf("%s names %s, whose subject is %q, want the commit that names zzz", f, id, got)
				}
			}
			zid := strings.TrimSpace(strings.TrimPrefix(readFileString(t, filepath.Join(other, "zzz-seen.txt")), "zzz "))
			if got := git(t, filepath.Join(dirs[1], "zzz"), "log", "-1", "--format=%s", zid); got != "zzz first" {
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
			if got := git(t, fx.RepoDir, "show", "HEAD^:.claude/jig.yaml"); !strings.Contains(got, filepath.ToSlash(dirs[0])) && !strings.Contains(got, dirs[0]) {
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
			for _, r := range append(append([]string{}, works...), bares...) {
				git(t, r, "fsck", "--strict", "--no-dangling")
			}
			for _, w := range works {
				if status := git(t, w, "status", "--porcelain"); status != "" {
					t.Errorf("%s: git status = %q, want clean", w, status)
				}
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
	build := func(dirs []string) (*Fixture, error) {
		built.Add(1)
		buildSmallHistory(t, dirs)
		return nil, nil
	}
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
	heads := map[string]bool{}
	for _, dirs := range copies {
		heads[git(t, filepath.Join(dirs[0], "work"), "rev-parse", "main")] = false
	}
	// Each copy is at its own place, so their commits differ.
	if len(heads) != callers {
		t.Errorf("%d different histories from %d copies; each is in its own directory", len(heads), callers)
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
	headBefore := git(t, workB, "rev-parse", "HEAD")
	workA := filepath.Join(a[0], "work")
	writeIn(t, workA, "new.txt", "new\n")
	commitIn(t, workA, "more")
	for _, repo := range []string{workA, filepath.Join(a[1], "remote.git")} {
		git(t, repo, "repack", "-a", "-d", "-q")
		git(t, repo, "prune", "--expire=now")
	}
	for _, repo := range []string{workB, filepath.Join(b[1], "remote.git"), filepath.Join(b[1], "other")} {
		git(t, repo, "fsck", "--strict", "--no-dangling")
	}
	if got := git(t, workB, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("another copy's HEAD moved to %s", got)
	}
	if status := git(t, workB, "status", "--porcelain"); status != "" {
		t.Errorf("another copy's git status = %q", status)
	}
}

// TestAScenarioIsBuiltDirectlyInADirTheCopyWouldNotServe: a directory that
// quotes differently from the build's gets the build function run in it, as a
// shape's template is not used for one.
func TestAScenarioIsBuiltDirectlyInADirTheCopyWouldNotServe(t *testing.T) {
	var built atomic.Int32
	build := func(dirs []string) (*Fixture, error) {
		built.Add(1)
		buildSmallHistory(t, dirs)
		return nil, nil
	}
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
	if got := git(t, filepath.Join(odd[0], "work"), "log", "-1", "--format=%s"); got != "merge side" {
		t.Errorf("the direct build's last commit = %q", got)
	}
	git(t, filepath.Join(odd[0], "work"), "fsck", "--strict", "--no-dangling")
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
