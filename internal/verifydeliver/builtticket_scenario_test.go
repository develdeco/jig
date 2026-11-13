package verifydeliver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// A built ticket (a fixture driven through its scenario by driveBuild: six
// attempts, a question and its answer, the journal, the slice states and the
// pushes) is the same in every test that starts from one, and costs, built with
// git, thousands of git processes a package (a lease's checkout, a commit and a
// push for each attempt). It is built once per test binary, as a fixture
// scenario, and each test gets a copy of its own: the fixture's Dir and the jig
// home, with every path in them (a file's, a commit's, a ref's) pointing at the
// copy. driveBuild stays the build function.

// generateBuilt returns a fixture of the default shape that driveBuild has
// played with model, in directories of the test's own: what fixture.Generate
// followed by driveBuild(t, fx, model) returns, for the cost of a copy. A test
// that changes the state afterwards changes only its copy.
func generateBuilt(t *testing.T, model string) *fixture.Fixture {
	t.Helper()
	return generateBuiltOpts(t, fixture.Opts{}, model)
}

// generateBuiltOpts is generateBuilt for a fixture of the shape opts names
// (opts.Home is not used: the jig home is the copy's own directory).
func generateBuiltOpts(t *testing.T, opts fixture.Opts, model string) *fixture.Fixture {
	t.Helper()
	return builtInto(t, opts, model, []string{filepath.Join(t.TempDir(), "fixture"), t.TempDir()})
}

// builtInto is generateBuiltOpts in the directories dirs: the fixture's and the
// jig home's.
func builtInto(t *testing.T, opts fixture.Opts, model string, dirs []string) *fixture.Fixture {
	t.Helper()
	opts.Home = ""
	fx, err := fixture.Scenario("built ticket: "+model, opts, dirs, func(dirs []string) (*fixture.Fixture, error) {
		built := opts
		built.Home = dirs[1]
		fx, err := fixture.Build(dirs[0], built)
		if err != nil {
			return nil, err
		}
		driveBuild(t, fx, model)
		return fx, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fx == nil {
		t.Fatal("the built-ticket scenario returned no fixture")
	}
	return fx
}

// rootForms is dir in the spellings a file can hold it in: as it is, with
// forward slashes, and with each backslash doubled.
func rootForms(dir string) []string {
	forms := []string{dir, filepath.ToSlash(dir), strings.ReplaceAll(dir, `\`, `\\`)}
	var out []string
	seen := map[string]bool{}
	for _, f := range forms {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// asRoots replaces each of the directories from, in every spelling, by the
// directory at the same place in to.
func asRoots(from, to []string) *strings.Replacer {
	var pairs []string
	for i := range from {
		olds, news := rootForms(from[i]), rootForms(to[i])
		for j := range olds {
			pairs = append(pairs, olds[j], news[j])
		}
	}
	return strings.NewReplacer(pairs...)
}

// objectID matches an object id, which names the directory a commit was made in
// when the commit holds a path.
var objectID = regexp.MustCompile(`[0-9a-f]{40}`)

// stampLike matches a time written into a file: an RFC 3339 time, or the
// seconds and zone git writes into a reflog line.
var stampLike = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)|\b1[6-9]\d{8} [+-]\d{4}\b`)

// inStoreRepo reports whether the slash path is in the store's repository or
// its remote. Its commits carry the time they were made, as its files do, so a
// build made twice in one place makes other commits: the ids in its refs are
// never compared.
func inStoreRepo(slash string) bool {
	return strings.HasPrefix(slash, "store/.git/") || strings.HasPrefix(slash, "store.git/")
}

// builtFiles reads the files under dir that are not objects or indexes (which
// differ with the directories), by slash path, with the paths of the roots
// replaced by the ones of as and the times blanked.
func builtFiles(t *testing.T, dir string, replace *strings.Replacer, blankIDs bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		if !d.Type().IsRegular() || strings.Contains(slash, ".git/objects/") || strings.HasSuffix(slash, ".git/index") || strings.HasPrefix(slash, "objects/") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		text := stampLike.ReplaceAllString(replace.Replace(string(data)), "<time>")
		if blankIDs || inStoreRepo(slash) {
			text = objectID.ReplaceAllString(text, "<id>")
		}
		out[slash] = text
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// gitDirsUnder lists the work trees and the bare repositories under dir.
func gitDirsUnder(t *testing.T, dir string) []string {
	t.Helper()
	var repos []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			repos = append(repos, filepath.Dir(p))
			return filepath.SkipDir
		}
		if d.IsDir() && strings.HasSuffix(d.Name(), ".git") {
			repos = append(repos, p)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(repos)
	return repos
}

// shapeOf is a commit as the subjects and parents of its history, with no id
// in it: a digest of its subject and its parents' digests.
func shapeOf(t *testing.T, dir, sha string, memo map[string]string) string {
	t.Helper()
	if s, ok := memo[sha]; ok {
		return s
	}
	out, err := gitx.Run(dir, "show", "-s", "--format=%s%n%P", sha)
	if err != nil {
		t.Fatalf("show %s in %s: %v", sha, dir, err)
	}
	subject, parents, _ := strings.Cut(out, "\n")
	text := subject
	for _, p := range strings.Fields(parents) {
		text += "\x00" + shapeOf(t, dir, p, memo)
	}
	sum := sha256.Sum256([]byte(text))
	memo[sha] = hex.EncodeToString(sum[:6])
	return memo[sha]
}

// repoFacts is what git says of the repository at dir that does not hold a
// path: its refs with the shape of the commit each names, the files each ref's
// tree holds (mode and path, and the blob when `blobs` says its id is
// comparable), and how many objects it has.
type repoFacts struct {
	refs    map[string]string
	trees   map[string][]string // by ref: "mode path blob", sorted
	objects int
}

func readRepo(t *testing.T, dir string) repoFacts {
	t.Helper()
	facts := repoFacts{refs: map[string]string{}, trees: map[string][]string{}}
	memo := map[string]string{}
	out, err := gitx.Run(dir, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		name, sha, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		facts.refs[name] = shapeOf(t, dir, sha, memo)
		tree, err := gitx.Run(dir, "ls-tree", "-r", sha)
		if err != nil {
			t.Fatal(err)
		}
		var files []string
		for _, l := range strings.Split(tree, "\n") {
			meta, path, _ := strings.Cut(l, "\t")
			f := strings.Fields(meta)
			if len(f) == 3 {
				files = append(files, f[0]+" "+path+" "+f[2])
			}
		}
		sort.Strings(files)
		facts.trees[name] = files
	}
	all, err := gitx.Run(dir, "cat-file", "--batch-all-objects", "--batch-check")
	if err != nil {
		t.Fatal(err)
	}
	facts.objects = len(strings.Split(all, "\n"))
	return facts
}

// TestBuiltTicketCopyIsTheDirectBuild: a copy of the built ticket and a
// fixture built directly (fixture.Generate, then driveBuild) are the same
// state in different directories: the same files in the fixture's directory
// and in the jig home (the store, the remotes, the build lease's clone and its
// config, the machine mapping, the journal), once the directories are read as
// each other's and the times blanked; the same repositories with the same refs,
// the same shape of history (subjects and parents) under each, the same files in
// each ref's tree, the same number of objects; and every file of the copy that
// holds a path holds its own, in every commit of every repository as well as
// on disk.
func TestBuiltTicketCopyIsTheDirectBuild(t *testing.T) {
	t.Parallel()
	t.Run("elsewhere", func(t *testing.T) {
		copied := generateBuilt(t, "rung-a")
		direct := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, direct, "rung-a")
		compareBuilt(t, copied, direct, false)
	})
	t.Run("in the same place", func(t *testing.T) {
		dirs := []string{filepath.Join(t.TempDir(), "fixture"), t.TempDir()}
		copied := builtInto(t, fixture.Opts{}, "rung-a", dirs)
		var held []map[string]string
		for _, d := range dirs {
			held = append(held, builtFiles(t, d, strings.NewReplacer(), false))
		}
		for _, d := range dirs {
			if err := os.RemoveAll(d); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		direct, err := fixture.Build(dirs[0], fixture.Opts{Home: dirs[1]})
		if err != nil {
			t.Fatal(err)
		}
		driveBuild(t, direct, "rung-a")
		for i, d := range dirs {
			want := builtFiles(t, d, strings.NewReplacer(), false)
			for name, body := range want {
				if g, ok := held[i][name]; !ok || g != body {
					t.Errorf("root %d: %s differs in place:\ndirect build:\n%s\ncopy:\n%s", i, name, body, g)
				}
			}
			if len(want) != len(held[i]) {
				t.Errorf("root %d: %d files, direct build has %d", i, len(held[i]), len(want))
			}
		}
		if !reflect.DeepEqual(copied, direct) {
			t.Errorf("Fixture = %+v, direct build made %+v", copied, direct)
		}
	})
}

// compareBuilt is the comparison of a copy of the built ticket with a direct
// build of it in other directories, where the ids of commits are not compared:
// a commit that holds a path has the id of the directory it was made in.
func compareBuilt(t *testing.T, copied, direct *fixture.Fixture, sameIDs bool) {
	t.Helper()
	from, to := []string{copied.Dir, copied.Home}, []string{direct.Dir, direct.Home}
	asDirect := asRoots(from, to)
	for i := range from {
		got, want := builtFiles(t, from[i], asDirect, !sameIDs), builtFiles(t, to[i], strings.NewReplacer(), !sameIDs)
		for name := range want {
			if _, ok := got[name]; !ok {
				t.Errorf("root %d: %s is in the direct build only", i, name)
			}
		}
		for name, body := range got {
			w, ok := want[name]
			if !ok {
				t.Errorf("root %d: %s is in the copy only", i, name)
				continue
			}
			if body != w {
				t.Errorf("root %d: %s differs:\ndirect build:\n%s\ncopy:\n%s", i, name, w, body)
			}
		}

		gotRepos, wantRepos := gitDirsUnder(t, from[i]), gitDirsUnder(t, to[i])
		if len(gotRepos) != len(wantRepos) || len(wantRepos) == 0 {
			t.Fatalf("root %d: %d repositories, direct build has %d", i, len(gotRepos), len(wantRepos))
		}
		for j := range wantRepos {
			gr, wr := readRepo(t, gotRepos[j]), readRepo(t, wantRepos[j])
			name, _ := filepath.Rel(to[i], wantRepos[j])
			if rel, _ := filepath.Rel(from[i], gotRepos[j]); rel != name {
				t.Errorf("repository %d of root %d is %s, direct build has %s", j, i, rel, name)
			}
			for ref, shape := range wr.refs {
				if gr.refs[ref] != shape {
					t.Errorf("%s: %s names a history of shape %q, direct build %q", name, ref, gr.refs[ref], shape)
				}
			}
			if len(gr.refs) != len(wr.refs) {
				t.Errorf("%s: %d refs, direct build has %d", name, len(gr.refs), len(wr.refs))
			}
			for ref, files := range wr.trees {
				if !sameTreeFiles(files, gr.trees[ref]) {
					t.Errorf("%s: %s holds\n%v\ndirect build\n%v", name, ref, gr.trees[ref], files)
				}
			}
			if gr.objects != wr.objects {
				t.Errorf("%s: %d objects, direct build has %d", name, gr.objects, wr.objects)
			}
			// A path of the template in any commit of any ref.
			if hit := templatePathIn(t, gotRepos[j]); hit != "" {
				t.Errorf("%s: a commit still names the template's directory: %s", name, hit)
			}
		}
	}
	for _, p := range []string{copied.RepoDir, copied.StoreDir, copied.Home} {
		if !strings.HasPrefix(p, copied.Dir) && !strings.HasPrefix(p, copied.Home) {
			t.Errorf("%s is outside the copy", p)
		}
	}
}

// sameTreeFiles reports whether two listings of "mode path blob" are the same
// files: a file that holds a directory has another blob in another directory,
// so only its mode and path are compared, and the other files' blobs too.
func sameTreeFiles(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		w, g := strings.Fields(want[i]), strings.Fields(got[i])
		if w[0] != g[0] || w[1] != g[1] {
			return false
		}
	}
	return true
}

// templatePathIn names a commit of the repository at dir that holds the
// directory a scenario is built in, or "".
func templatePathIn(t *testing.T, dir string) string {
	t.Helper()
	revs, err := gitx.Run(dir, "rev-list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"grep", "-I", "-l", "-i", "jig-fixture-scenario"}, strings.Fields(revs)...)
	out, err := gitx.Run(dir, args...)
	if err != nil && !bytes.Contains([]byte(err.Error()), []byte("exit status 1")) {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}
