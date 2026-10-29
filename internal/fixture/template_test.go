package fixture

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// shapes are the fixtures a copy of a template is checked against a direct
// build for: the default, and one for each option that changes what is built.
var shapes = []struct {
	name string
	opts Opts
}{
	{"default", Opts{}},
	{"second repo", Opts{SecondRepo: true}},
	{"standalone", Opts{Standalone: true}},
	{"scenario branch", Opts{ScenarioBranch: "reviewer"}},
	{"env fail", Opts{EnvFail: true}},
	{"orchestrator", Opts{Orchestrator: true}},
}

// repoPair is a repository of a fixture that holds a commit, and its bare
// remote.
type repoPair struct {
	name         string
	work, remote string // remote is "" for a repository with none
}

// repoPairs lists the repositories of fx that hold a commit.
func repoPairs(fx *Fixture) []repoPair {
	var out []repoPair
	add := func(name, work, remote string) {
		if work != "" {
			out = append(out, repoPair{name, work, remote})
		}
	}
	add("fixture-repo", fx.RepoDir, fx.RepoRemote)
	add("second-repo", fx.SecondRepoDir, fx.SecondRepoRemote)
	add("store", fx.StoreDir, fx.StoreRemote)
	for _, name := range sortedKeys(fx.NestedDirs) {
		add(name, fx.NestedDirs[name], fx.NestedRemotes[name])
	}
	return out
}

// tree is a fixture directory read into memory.
type tree struct {
	dirs    map[string]bool
	files   map[string][]byte      // every file but the ones below
	perms   map[string]fs.FileMode // of those files
	objects map[string]bool        // the files and directories of an object database, by path; true for a directory
	indexes map[string]bool        // the .git/index files, by path
	refs    map[string][]byte      // refs, reflogs and packed-refs, which name commits by id
}

// isRefFile reports whether the slash path rel is a ref, a reflog or a
// packed-refs file of a git directory.
func isRefFile(rel string) bool {
	return strings.Contains(rel, "/refs/") || strings.Contains(rel, "/logs/") || path.Base(rel) == "packed-refs"
}

// readTree reads the directory dir.
func readTree(t *testing.T, dir string) *tree {
	t.Helper()
	tr := &tree{
		dirs: map[string]bool{}, files: map[string][]byte{}, perms: map[string]fs.FileMode{},
		objects: map[string]bool{}, indexes: map[string]bool{}, refs: map[string][]byte{},
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch {
		case strings.Contains(rel, ".git/objects/"):
			// The directories of an object database too: a rewrite must not
			// leave behind the empty ones of the objects it replaced.
			tr.objects[rel] = d.IsDir()
		case d.IsDir():
			tr.dirs[rel] = true
		case path.Base(rel) == "index" && path.Base(path.Dir(rel)) == ".git":
			tr.indexes[rel] = true
		case isRefFile(rel):
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			tr.refs[rel] = data
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			tr.files[rel], tr.perms[rel] = data, info.Mode().Perm()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// git runs the git program in dir under Build's hermetic environment.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.RunEnv(dir, hermeticGitEnv, args...)
	if err != nil {
		t.Fatalf("git %s (in %s): %v", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(out)
}

// sortedKeys is the keys of m, sorted.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sameNames fails t unless a and b hold the same names, and lists the ones
// that are in only one.
func sameNames[A, B any](t *testing.T, what string, a map[string]A, b map[string]B) {
	t.Helper()
	var onlyA, onlyB []string
	for k := range a {
		if _, ok := b[k]; !ok {
			onlyA = append(onlyA, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			onlyB = append(onlyB, k)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	if len(onlyA) > 0 || len(onlyB) > 0 {
		t.Errorf("%s differ: only in the direct build %v, only in the copy %v", what, onlyA, onlyB)
	}
}

// show is a file's content for a failure message: text as it is, anything
// else as its size and hash.
func show(data []byte) string {
	if len(data) > 4096 || bytes.IndexByte(data, 0) >= 0 {
		return fmt.Sprintf("%d bytes, sha256 %x", len(data), sha256.Sum256(data))
	}
	return string(data)
}

// facts are what git says of the repositories of a fixture, by repository:
// the index entries (mode, blob and path of each) and the history as one
// record per commit, with its author, committer, dates, parents, subject and
// the files it changes with their modes and blobs.
type facts map[string]struct{ index, history string }

// logFormat starts a record per commit.
const logFormat = "--format=COMMIT%n%an%n%ae%n%ad%n%cn%n%ce%n%cd%n%P%n%s"

// readFacts asks git for the facts of every repository of fx.
func readFacts(t *testing.T, fx *Fixture) facts {
	t.Helper()
	out := facts{}
	for _, r := range repoPairs(fx) {
		out[r.name] = struct{ index, history string }{
			index:   git(t, r.work, "ls-files", "-s"),
			history: git(t, r.work, "log", "--root", "--raw", "-r", "--no-abbrev", "--date=raw", logFormat),
		}
	}
	return out
}

// withoutBlobs is a history without the blob ids of its files, which hold
// the directory a fixture is in, and so differ from one directory to the
// next: each file keeps its mode and path.
func withoutBlobs(history string) string {
	var out []string
	for _, line := range strings.Split(history, "\n") {
		if strings.HasPrefix(line, ":") {
			meta, name, _ := strings.Cut(line, "\t")
			fields := strings.Fields(meta)
			line = fields[1] + " " + fields[4] + "\t" + name
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// mustBeSound fails t unless git finds the repository sound and clean: its
// objects are intact, its work tree matches its index and its one commit, the
// bare remote has the same branch at the same commit, and the work tree's
// origin is that remote.
func mustBeSound(t *testing.T, r repoPair, history string) {
	t.Helper()
	if status := git(t, r.work, "status", "--porcelain"); status != "" {
		t.Errorf("%s: git status --porcelain = %q, want nothing", r.name, status)
	}
	git(t, r.work, "fsck", "--strict", "--no-dangling")
	if n := strings.Count(history, "COMMIT\n"); n != 1 {
		t.Errorf("%s: %d commits, want one", r.name, n)
	}
	if r.remote == "" {
		return
	}
	git(t, r.remote, "fsck", "--strict", "--no-dangling")
	if got, want := git(t, r.remote, "rev-parse", "refs/heads/main"), git(t, r.work, "rev-parse", "HEAD"); got != want {
		t.Errorf("%s: the remote's main = %s, the work tree's HEAD %s", r.name, got, want)
	}
	if url := git(t, r.work, "remote", "get-url", "origin"); filepath.Clean(url) != filepath.Clean(r.remote) {
		t.Errorf("%s: origin = %s, want %s", r.name, url, r.remote)
	}
}

// TestACopyIsTheFixtureABuildMakes: for each shape, a fixture built by the
// git program and a copy of the shape's template are the same fixture, in
// the two ways one directory can differ from another.
//
// In its place: the direct build is read, removed, and the copy made in the
// same directory. They differ in no path, so they are the same file for file:
// the same Fixture, directories, files, refs and reflogs, object files under
// the same ids (so the same commits and trees), index entries and history,
// and the same permissions where the platform has them.
//
// Elsewhere: the copy is made in another directory, of another name length.
// Every file but the objects, indexes and refs is the direct build's once
// the copy's directory is read as the direct build's, in each spelling; git
// finds every repository sound and clean, each with one commit of the direct
// build's message, author, committer, date, parents and file modes, a bare
// remote on the work tree's branch, and remote URLs inside the copy.
func TestACopyIsTheFixtureABuildMakes(t *testing.T) {
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			opts := shape.opts
			opts.Home = t.TempDir()
			base := t.TempDir()
			place := filepath.Join(base, "place")
			elsewhere := filepath.Join(base, "a-copy-with-a-longer-name")

			direct, err := buildDirect(place, opts)
			if err != nil {
				t.Fatalf("direct build: %v", err)
			}
			want, wantFacts := readTree(t, place), readFacts(t, direct)
			if err := os.RemoveAll(place); err != nil {
				t.Fatal(err)
			}

			// In its place.
			copied, err := Build(place, opts)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			got := readTree(t, place)
			if !reflect.DeepEqual(direct, copied) {
				t.Errorf("Fixture = %+v, direct build made %+v", copied, direct)
			}
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
				g, ok := got.files[name]
				if !ok {
					continue
				}
				if !bytes.Equal(want.files[name], g) {
					t.Errorf("%s differs:\ndirect build:\n%s\ncopy:\n%s", name, show(want.files[name]), show(g))
				}
				if runtime.GOOS != "windows" && want.perms[name] != got.perms[name] {
					t.Errorf("%s has mode %v, direct build %v", name, got.perms[name], want.perms[name])
				}
			}
			if gotFacts := readFacts(t, copied); !reflect.DeepEqual(gotFacts, wantFacts) {
				t.Errorf("index entries and history = %v, direct build %v", gotFacts, wantFacts)
			}
			if err := os.RemoveAll(place); err != nil {
				t.Fatal(err)
			}

			// Elsewhere.
			other, err := Build(elsewhere, opts)
			if err != nil {
				t.Fatalf("Build elsewhere: %v", err)
			}
			far := readTree(t, elsewhere)
			olds, news := rootSpellings(elsewhere, place)
			var pairs []string
			for i := range olds {
				pairs = append(pairs, olds[i], news[i])
			}
			asDirect := strings.NewReplacer(pairs...)
			normalized := map[string][]byte{}
			for name, data := range far.files {
				normalized[name] = []byte(asDirect.Replace(string(data)))
			}
			sameNames(t, "directories", want.dirs, far.dirs)
			sameNames(t, "files", want.files, normalized)
			sameNames(t, "indexes", want.indexes, far.indexes)
			sameNames(t, "refs and reflogs", want.refs, far.refs)
			for _, name := range sortedKeys(want.files) {
				if g, ok := normalized[name]; ok && !bytes.Equal(want.files[name], g) {
					t.Errorf("%s differs:\ndirect build:\n%s\ncopy:\n%s", name, show(want.files[name]), show(g))
				}
			}
			// The ids of the objects differ with the directory, so the names
			// of their files do, and the directories they are in can; the
			// number of files does not.
			if g, w := countFiles(far.objects), countFiles(want.objects); g != w {
				t.Errorf("%d object files, direct build has %d", g, w)
			}
			farFacts := readFacts(t, other)
			wantRepos := repoPairs(direct)
			for i, r := range repoPairs(other) {
				name := r.name
				if wantRepos[i].name != name {
					t.Fatalf("repository %d is %s, direct build has %s", i, name, wantRepos[i].name)
				}
				if g, w := withoutBlobs(farFacts[name].history), withoutBlobs(wantFacts[name].history); g != w {
					t.Errorf("%s: history = %q, direct build %q", name, g, w)
				}
				mustBeSound(t, r, farFacts[name].history)
				if r.remote != "" {
					url := git(t, r.remote, "config", "remote.origin.url")
					if !strings.HasPrefix(filepath.ToSlash(url), filepath.ToSlash(elsewhere)+"/") {
						t.Errorf("%s: the remote's origin %s is not inside %s", name, url, elsewhere)
					}
				}
			}
			if len(wantRepos) != len(repoPairs(other)) {
				t.Errorf("%d repositories, direct build has %d", len(repoPairs(other)), len(wantRepos))
			}
		})
	}
}

// countFiles is how many of the entries of an object database are files.
func countFiles(objects map[string]bool) int {
	n := 0
	for _, isDir := range objects {
		if !isDir {
			n++
		}
	}
	return n
}

// templateOf is the template of opts' shape.
func templateOf(t *testing.T, opts Opts) *template {
	t.Helper()
	testdataDir, err := testdataFixtureDir()
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := templateFor(opts, testdataDir)
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

// TestACopyHoldsNoPathOfItsTemplate: nothing in a copy outside its object
// databases names the directory its template was built in, in any case and
// spelling.
func TestACopyHoldsNoPathOfItsTemplate(t *testing.T) {
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			opts := shape.opts
			opts.Home = t.TempDir()
			dir := filepath.Join(t.TempDir(), "copy")
			if _, err := Build(dir, opts); err != nil {
				t.Fatal(err)
			}
			olds, _ := rootSpellings(templateOf(t, opts).root, "")
			tr := readTree(t, dir)
			all := map[string][]byte{}
			for _, m := range []map[string][]byte{tr.files, tr.refs} {
				for name, data := range m {
					all[name] = data
				}
			}
			for name, data := range all {
				lower := bytes.ToLower(data)
				for _, o := range olds {
					if bytes.Contains(lower, []byte(strings.ToLower(o))) {
						t.Errorf("%s still names the template's directory %s", name, o)
					}
				}
			}
			if len(tr.files) == 0 {
				t.Fatal("no files read")
			}
		})
	}
}

// TestTemplatesAreBuiltOncePerShape: Builds of one shape share a template
// whatever their Home, and different shapes have their own.
func TestTemplatesAreBuiltOncePerShape(t *testing.T) {
	a := templateOf(t, Opts{Home: "one"})
	b := templateOf(t, Opts{Home: "two"})
	if a != b || a.root != b.root {
		t.Errorf("Opts that differ only in Home have two templates, %s and %s", a.root, b.root)
	}
	if c := templateOf(t, Opts{SecondRepo: true}); c == a || c.root == a.root {
		t.Errorf("a second-repo fixture shares the default template")
	}
}

// TestLooseObjectsOfACopyAreLinksToTheTemplates: where the file system links
// files, a copy's loose objects are the template's own files, not more
// bytes; where it does not, they are copies and the test says nothing.
func TestLooseObjectsOfACopyAreLinksToTheTemplates(t *testing.T) {
	opts := Opts{Home: t.TempDir()}
	dir := filepath.Join(t.TempDir(), "copy")
	fx, err := Build(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	tpl := templateOf(t, opts)
	probe := filepath.Join(tpl.root, "probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(probe, probe+".link"); err != nil {
		t.Skipf("the file system does not link files: %v", err)
	}
	os.Remove(probe)
	os.Remove(probe + ".link")

	// The objects the template has are linked; the ones a rewrite wrote for
	// the copy (the commit and trees and blobs of the files that name the
	// directory) are the copy's own.
	linked, own := 0, 0
	for _, remote := range []string{fx.RepoRemote, fx.StoreRemote} {
		err = filepath.WalkDir(remote, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.Contains(filepath.ToSlash(p), "/objects/") {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			a, err := os.Stat(p)
			if err != nil {
				return err
			}
			b, err := os.Stat(filepath.Join(tpl.root, rel))
			switch {
			case os.IsNotExist(err):
				own++
			case err != nil:
				return err
			case os.SameFile(a, b):
				linked++
			default:
				t.Errorf("%s is a copy of the template's file, not a link", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if linked == 0 {
		t.Errorf("none of the %d loose objects of the bare remotes is the template's file", own)
	}
	if own == 0 {
		t.Errorf("no object is the copy's own, but its commits were rewritten")
	}
}

// TestAnObjectIsCopiedWhereTheFileSystemWillNotLink: when linking fails (no
// support, another volume, a file at its limit of links) the object is
// copied, as the same bytes in a file of its own.
func TestAnObjectIsCopiedWhereTheFileSystemWillNotLink(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("object bytes"), 0o444); err != nil {
		t.Fatal(err)
	}
	refuse := func(string, string) error { return errors.New("too many links") }
	if err := linkOrCopy(refuse, src, dst, 0o444); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "object bytes" {
		t.Fatalf("copy = %q, %v; want the object's bytes", got, err)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if os.SameFile(a, b) {
		t.Errorf("the copy is the object's own file")
	}
}

// TestACopyCanBeChangedWithoutChangingTheOthers: git changing a copy's
// objects (a commit, a repack that removes the loose objects it packs, a
// prune) reaches neither the template nor another copy, though their loose
// objects are one file.
func TestACopyCanBeChangedWithoutChangingTheOthers(t *testing.T) {
	opts := Opts{SecondRepo: true, Home: t.TempDir()}
	a, err := Build(filepath.Join(t.TempDir(), "a"), opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(filepath.Join(t.TempDir(), "b"), opts)
	if err != nil {
		t.Fatal(err)
	}
	headBefore := git(t, b.RepoDir, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(a.RepoDir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, a.RepoDir, "add", "-A")
	if _, err := gitx.RunEnv(a.RepoDir, append(append([]string{}, hermeticGitEnv...), identityEnv...), "commit", "-q", "-m", "more"); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{a.RepoDir, a.RepoRemote} {
		git(t, repo, "repack", "-a", "-d", "-q")
		git(t, repo, "prune", "--expire=now")
	}

	tpl := templateOf(t, opts)
	for _, repo := range []string{b.RepoDir, b.RepoRemote, tpl.fx.RepoDir, tpl.fx.RepoRemote} {
		git(t, repo, "fsck", "--strict", "--no-dangling")
	}
	if got := git(t, b.RepoDir, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("another copy's HEAD moved to %s", got)
	}
	if status := git(t, b.RepoDir, "status", "--porcelain"); status != "" {
		t.Errorf("another copy's git status = %q", status)
	}
}

// TestBuildsInParallelMakeSeparateFixtures: Builds of one shape racing to be
// the first, and every one after, each make a fixture of their own.
func TestBuildsInParallelMakeSeparateFixtures(t *testing.T) {
	opts := Opts{EnvFail: true, SecondRepo: true, Standalone: true, Home: t.TempDir()}
	const n = 6
	fixtures := make([]*Fixture, n)
	errs := make([]error, n)
	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = filepath.Join(t.TempDir(), "fx")
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fixtures[i], errs[i] = Build(dirs[i], opts)
		}()
	}
	wg.Wait()
	for i, fx := range fixtures {
		if errs[i] != nil {
			t.Fatalf("build %d: %v", i, errs[i])
		}
		if fx.Dir != dirs[i] || !strings.HasPrefix(fx.RepoDir, dirs[i]) || !strings.HasPrefix(fx.SecondRepoRemote, dirs[i]) {
			t.Errorf("build %d is in %s, not %s", i, fx.RepoDir, dirs[i])
		}
		if status := git(t, fx.RepoDir, "status", "--porcelain"); status != "" {
			t.Errorf("build %d: git status = %q", i, status)
		}
	}
}

// TestADirTheTemplateCannotServeIsBuiltDirectly: the files of a fixture quote
// the directory they are in by what it holds, so a directory that needs other
// quoting than the template's root is built by the git program, and the
// fixture is whole all the same.
func TestADirTheTemplateCannotServeIsBuiltDirectly(t *testing.T) {
	tpl := templateOf(t, Opts{})
	spaced := strings.Contains(tpl.root, " ")
	base := os.TempDir()
	for _, c := range []struct {
		dir  string
		want bool
	}{
		{filepath.Join(base, "plain-dir_1.x"), !spaced},
		{filepath.Join(base, "RUNNER~1", "Temp"), !spaced},
		{filepath.Join(base, "plain dir"), spaced},
		{filepath.Join(base, "odd#dir"), false},
		{filepath.Join(base, "odd'dir"), false},
		{filepath.Join(base, `odd"dir`), false},
		{filepath.Join(base, "odd,dir"), false},
	} {
		if got := tpl.servable(c.dir); got != c.want {
			t.Errorf("servable(%q) = %v, want %v", c.dir, got, c.want)
		}
	}

	dir := filepath.Join(t.TempDir(), "odd#dir")
	fx, err := Build(dir, Opts{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if status := git(t, fx.RepoDir, "status", "--porcelain"); status != "" {
		t.Errorf("git status = %q", status)
	}
	data, err := os.ReadFile(filepath.Join(fx.RepoDir, ".claude", "jig.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.ToSlash(dir)) || strings.Contains(string(data), filepath.ToSlash(tpl.root)) {
		t.Errorf("jig.yaml names %s or the template:\n%s", dir, data)
	}
}
