package fixture

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
)

// A fixture is built with git once for each shape in a process and copied for
// every Build after that.
//
// Building one directly costs about eighteen git processes (init, three
// config calls, add and commit for each repo, a bare clone and a remote add
// for each remote), and the suite builds thousands of fixtures, each of them
// identical to the last but for the directory it is in. That directory is
// written into three kinds of file: the manifest of each fixture repo
// (jig.yaml: the envtool binary and the state file), the store's project.yaml
// (the repos' remotes), and the remote URLs in the .git/config of every
// repository. The first two are tracked, so a plain copy would leave each
// repo's one commit saying the template's paths.
//
// A template is the fixture of one shape (an Opts less its Home), built by
// buildTree into a directory of its own under the temp directory. Build then:
//
//  1. copies the template into dir: directories as they are, the loose
//     objects of every git directory hard-linked (they are written once and
//     never changed; a copy is made where the file system will not link), and
//     every other file copied;
//  2. replaces the template's root with dir in the files that held it, in each
//     spelling they hold it in (native separators, forward slashes, and the
//     doubled backslashes of a git config value), which the template's scan
//     found once, so a Build reads only the files it has to change;
//  3. makes the commits agree with the files: for each repository whose
//     tracked files changed, gitx.RewriteHeadCommit writes the commit a
//     repository built from the final files would have made, in the work tree
//     and in its bare remote, in process.
//
// None of the steps starts a git process, and the copy is, to git and to jig,
// the fixture buildTree makes in dir, down to the commit ids, which the
// equivalence test (template_test.go) checks for each shape.
//
// The copy is not served when it would not be that fixture: a dir that
// quotes differently from the template's root in the commands and YAML the
// files hold (see servable). Build makes those directly.

// planKind says what the copy does with one entry of the template.
type planKind uint8

const (
	planDir   planKind = iota // make the directory
	planCopy                  // copy the file as it is
	planPatch                 // copy the file with the template's root replaced by dir
	planLink                  // hard-link the file (a loose object or pack), or copy it where that fails
)

// planEntry is one file or directory of a template.
type planEntry struct {
	rel  string // slash-separated, relative to the template's root
	kind planKind
	perm fs.FileMode
}

// tplRepo is a repository of a template whose tracked files the copy
// changes, and what its rewrite needs.
type tplRepo struct {
	dir    string   // the work tree, relative to the template's root
	remote string   // its bare remote, relative to the root, "" for none
	paths  []string // the files of the work tree the copy changes, relative to dir
}

// template is the fixture of one shape, built once.
type template struct {
	once sync.Once
	err  error

	root  string   // where it was built; deleted when the test binary exits
	fx    *Fixture // as buildTree returned it, rooted at root
	plan  []planEntry
	repos []tplRepo
}

var (
	templatesMu sync.Mutex
	templates   = map[Opts]*template{}
)

// templateFor returns the template of opts' shape, building it on the first
// call. Concurrent callers of one shape wait for that build; a build that
// failed fails every caller with its error.
func templateFor(opts Opts, testdataDir string) (*template, error) {
	opts.Home = ""
	templatesMu.Lock()
	t := templates[opts]
	if t == nil {
		t = &template{}
		templates[opts] = t
	}
	templatesMu.Unlock()
	t.once.Do(func() {
		// A build that panics must not leave a template that reads as built.
		t.err = errors.New("fixture: the template's build did not finish")
		t.err = t.build(opts, testdataDir)
	})
	return t, t.err
}

// build builds the template in a new temp directory and plans its copies.
// Like the envtool's cache (buildEnvtool), the directory is removed through
// gittest.AtExit when a test binary finishes; outside one it is a leak of one
// directory per shape for the life of the process.
func (t *template) build(opts Opts, testdataDir string) error {
	root, err := os.MkdirTemp("", "jig-fixture-template")
	if err != nil {
		return fmt.Errorf("fixture: create template dir: %w", err)
	}
	gittest.AtExit(func() { os.RemoveAll(root) })
	fx, err := buildTree(root, opts, testdataDir)
	if err != nil {
		os.RemoveAll(root)
		return fmt.Errorf("fixture: build the template: %w", err)
	}
	t.root, t.fx = root, fx
	if err := t.scan(); err != nil {
		os.RemoveAll(root)
		return fmt.Errorf("fixture: scan the template: %w", err)
	}
	return nil
}

// rootSpellings are the strings a file can hold root in, each paired with the
// same spelling of other: its native form, with forward slashes, and with
// every backslash doubled, which is how git writes a path into a config value
// and how a quoted YAML or JSON scalar holds one. On a file system whose
// separator is a slash all three are one.
func rootSpellings(root, other string) (olds, news []string) {
	forms := []func(string) string{
		func(s string) string { return s },
		filepath.ToSlash,
		func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) },
	}
	seen := map[string]bool{}
	for _, f := range forms {
		o := f(root)
		if seen[o] {
			continue
		}
		seen[o] = true
		olds, news = append(olds, o), append(news, f(other))
	}
	return olds, news
}

// scan walks the finished template once and plans a copy of it: what each
// file is to the copy, and which repositories' tracked files change.
func (t *template) scan() error {
	olds, _ := rootSpellings(t.root, "")
	lowers := make([][]byte, len(olds))
	for i, o := range olds {
		lowers[i] = []byte(strings.ToLower(o))
	}
	// patched is the set of files that hold the root, by slash path.
	patched := map[string]bool{}
	// objectsDirs are the object databases found so far, as slash paths with
	// a trailing slash. A directory is visited before what is in it.
	var objectsDirs []string
	err := filepath.WalkDir(t.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(t.root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slash := filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			t.plan = append(t.plan, planEntry{rel: slash, kind: planDir})
			if d.Name() == "objects" {
				// The object database of a git directory: the directory has a
				// HEAD beside it.
				if _, err := os.Stat(filepath.Join(filepath.Dir(p), "HEAD")); err == nil {
					objectsDirs = append(objectsDirs, slash+"/")
				}
			}
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is neither a file nor a directory", slash)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		perm := info.Mode().Perm()
		if under(slash, objectsDirs) {
			if runtime.GOOS == "windows" && perm&0o200 == 0 {
				// Git writes its objects read-only, and a read-only attribute
				// belongs to the file, not to a name of it: every copy's link
				// shares it. Go removes a read-only file by clearing the
				// attribute and deleting, and when two copies are removed
				// at once the second finds it already cleared, does not retry
				// its failed delete and reports "Access is denied" (a test's
				// temp directory cleanup fails with it). A writable object
				// has no attribute to race over.
				if err := os.Chmod(p, 0o666); err != nil {
					return err
				}
				perm = 0o666
			}
			t.plan = append(t.plan, planEntry{rel: slash, kind: planLink, perm: perm})
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		holds := false
		for _, o := range olds {
			if bytes.Contains(data, []byte(o)) {
				holds = true
				break
			}
		}
		if !holds {
			// The root in a spelling the copy does not replace would be a
			// path into the template, kept by every copy.
			lower := bytes.ToLower(data)
			for i, l := range lowers {
				if bytes.Contains(lower, l) {
					return fmt.Errorf("%s holds the template's root as %q in another case than the spellings %q that a copy replaces", slash, olds[i], olds)
				}
			}
			t.plan = append(t.plan, planEntry{rel: slash, kind: planCopy, perm: perm})
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return fmt.Errorf("%s is a binary file that holds the template's root, which a replacement would corrupt", slash)
		}
		patched[slash] = true
		t.plan = append(t.plan, planEntry{rel: slash, kind: planPatch, perm: perm})
		return nil
	})
	if err != nil {
		return err
	}

	for _, r := range t.repositories() {
		if r.dir == "" {
			continue
		}
		prefix := r.dir + "/"
		for file := range patched {
			rel, ok := strings.CutPrefix(file, prefix)
			if !ok || rel == ".git" || strings.HasPrefix(rel, ".git/") {
				continue
			}
			r.paths = append(r.paths, rel)
		}
		if len(r.paths) > 0 {
			t.repos = append(t.repos, r)
		}
	}
	return nil
}

// under reports whether the slash path rel is inside one of dirs, which end
// in a slash.
func under(rel string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(rel, d) {
			return true
		}
	}
	return false
}

// repositories lists the template's work trees and their bare remotes, as
// paths relative to its root, with the remote beside each. (Its callers
// fill in the paths.)
func (t *template) repositories() []tplRepo {
	rel := func(p string) string {
		if p == "" {
			return ""
		}
		r, err := filepath.Rel(t.root, p)
		if err != nil {
			return ""
		}
		return filepath.ToSlash(r)
	}
	fx := t.fx
	repos := []tplRepo{
		{dir: rel(fx.RepoDir), remote: rel(fx.RepoRemote)},
		{dir: rel(fx.SecondRepoDir), remote: rel(fx.SecondRepoRemote)},
		{dir: rel(fx.StoreDir), remote: rel(fx.StoreRemote)},
	}
	for name, dir := range fx.NestedDirs {
		repos = append(repos, tplRepo{dir: rel(dir), remote: rel(fx.NestedRemotes[name])})
	}
	return repos
}

// servable reports whether the template can stand in for a direct build into
// dir. The files that hold the root quote it by what is in it: the envtool's
// command in jig.yaml is wrapped in double quotes when the path has a space
// (quoteIfSpaced), and YAML quotes a scalar when characters in it mean
// something else. A copy only replaces text, so it is the same file only
// when dir needs the quoting the root did, and none the root did not. Dir is
// served when it has a space exactly when the root has one and every other
// character in it is one that needs no quoting in a plain YAML scalar in the
// middle of a line (the manifest's commands and the store's remotes are
// block-style scalars that start with the path's first character, which a dir
// shares with the root), in a git config value (which quotes one holding a
// "#" or ";", and writes a backslash doubled, which the copy does) or in a
// command: a letter, a digit, a separator, a drive's colon, and the
// characters temp directory names are made of when a test's name has them
// ("~" is also the tail of a Windows short name, RUNNER~1). Any other dir is
// built directly, which is correct and as slow as a Build always was.
func (t *template) servable(dir string) bool {
	if strings.Contains(t.root, " ") != strings.Contains(dir, " ") {
		return false
	}
	for _, r := range dir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-~/\\: ,+=@()!$%&^{}", r):
		default:
			return false
		}
	}
	return true
}

// buildFromTemplate is the fixture of opts in dir, made by copying the
// template of its shape, or by buildTree where the template cannot be served.
func buildFromTemplate(dir string, opts Opts, testdataDir string) (*Fixture, error) {
	t, err := templateFor(opts, testdataDir)
	if err != nil {
		return nil, err
	}
	if !t.servable(dir) {
		return buildTree(dir, opts, testdataDir)
	}
	return t.instantiate(dir)
}

// instantiate copies the template into the empty directory dir and points the
// copy at dir.
func (t *template) instantiate(dir string) (*Fixture, error) {
	olds, news := rootSpellings(t.root, dir)
	pairs := make([]string, 0, 2*len(olds))
	for i := range olds {
		pairs = append(pairs, olds[i], news[i])
	}
	replace := strings.NewReplacer(pairs...)

	for _, e := range t.plan {
		src := filepath.Join(t.root, filepath.FromSlash(e.rel))
		dst := filepath.Join(dir, filepath.FromSlash(e.rel))
		var err error
		switch e.kind {
		case planDir:
			err = os.Mkdir(dst, 0o755)
		case planLink:
			err = linkOrCopy(os.Link, src, dst, e.perm)
		case planCopy:
			err = copyData(src, dst, e.perm, nil)
		case planPatch:
			err = copyData(src, dst, e.perm, replace)
		}
		if err != nil {
			return nil, fmt.Errorf("fixture: copy the template to %s: %w", dir, err)
		}
	}

	at := func(p string) string {
		if p == "" {
			return ""
		}
		rel, err := filepath.Rel(t.root, p)
		if err != nil {
			return ""
		}
		return filepath.Join(dir, rel)
	}
	fx := *t.fx
	fx.Dir = dir
	fx.RepoDir, fx.RepoRemote = at(fx.RepoDir), at(fx.RepoRemote)
	fx.StoreDir, fx.StoreRemote = at(fx.StoreDir), at(fx.StoreRemote)
	fx.ScenarioDir = at(fx.ScenarioDir)
	fx.SecondRepoDir, fx.SecondRepoRemote = at(fx.SecondRepoDir), at(fx.SecondRepoRemote)
	fx.OrchestratorDir = at(fx.OrchestratorDir)
	if fx.NestedDirs != nil {
		fx.NestedDirs, fx.NestedRemotes = map[string]string{}, map[string]string{}
		for name, p := range t.fx.NestedDirs {
			fx.NestedDirs[name] = at(p)
			fx.NestedRemotes[name] = at(t.fx.NestedRemotes[name])
		}
	}

	for _, r := range t.repos {
		var mirrors []string
		if r.remote != "" {
			mirrors = append(mirrors, filepath.Join(dir, filepath.FromSlash(r.remote)))
		}
		work := filepath.Join(dir, filepath.FromSlash(r.dir))
		if err := gitx.RewriteHeadCommit(work, r.paths, mirrors...); err != nil {
			return nil, fmt.Errorf("fixture: point %s at %s: %w", path.Base(r.dir), dir, err)
		}
	}
	return &fx, nil
}

// linkOrCopy makes dst the same file as src through link, and a copy of it
// when link fails: a file system that does not link files, a volume dst is not
// on, or a file already linked as often as the file system allows (NTFS
// stops at 1023). Objects are written once and never changed, so a link and
// a copy read alike.
func linkOrCopy(link func(oldname, newname string) error, src, dst string, perm fs.FileMode) error {
	if link(src, dst) == nil {
		return nil
	}
	return copyData(src, dst, perm, nil)
}

// copyData copies the file src to a new file dst of mode perm, with the
// template's root replaced through replace when it is not nil.
func copyData(src, dst string, perm fs.FileMode, replace *strings.Replacer) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if replace != nil {
		data = []byte(replace.Replace(string(data)))
	}
	return os.WriteFile(dst, data, perm)
}
