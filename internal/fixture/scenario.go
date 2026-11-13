package fixture

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
)

// A scenario is a state a test package builds once for its binary and every
// test that wants it copies: the state of a built ticket (a fixture driven
// through a ticket's slices), or the history of one repository, which a fixture
// shape cannot name because only the test package that owns the build step can
// run it (the fixture cannot import store, journal or pool; their tests import
// it).
//
// A scenario is built as a template is, and copied by the same plan (hard-linked
// loose objects, files that hold a root patched with the caller's directory),
// with two differences. A scenario spans several directories (a fixture's Dir
// and the jig home, say), each a root of its own that is replaced by the
// directory the caller gives for it; and the files that carry a root may sit in
// an old commit, not only in the tip (a commit made on top of the fixture's
// leaves the fixture's manifest behind), so the copy rewrites each
// repository's whole history (gitx.RewriteHistory), in process, and the
// repositories it finds (a directory with a .git, and the bare repositories its
// remotes name) are not listed by anyone. A copy starts no git process.
//
// A work tree of a scenario must be clean where its tracked files carry a root:
// the rewrite stores the work tree's bytes for them.

type scenarioKey struct {
	name string
	opts Opts // less Home
	dirs int
}

var (
	scenariosMu sync.Mutex
	scenarios   = map[scenarioKey]*template{}
)

// Scenario fills the empty directories dirs with the scenario name built with
// opts (what the build function was given, so that options that change what is
// built name another scenario) and returns the fixture the build returned, with
// its paths in dirs, or nil if it returned none.
//
// The first call in a test binary for a name, opts and number of directories
// calls build with directories of its own, in a temp directory removed when
// the binary exits, and every call, that first one included, copies what it
// built into dirs. Concurrent callers wait for that build; a build that failed
// or panicked fails every caller with its error. The directories dirs are the
// roots: a path of one in a file of the build is a path of the same directory
// in a copy. A dir that would not be served by a copy (see servable) has build
// run in it directly.
func Scenario(name string, opts Opts, dirs []string, build func(dirs []string) (*Fixture, error)) (*Fixture, error) {
	if len(dirs) == 0 {
		return nil, errors.New("fixture: a scenario spans at least one directory")
	}
	targets := make([]string, len(dirs))
	for i, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("fixture: resolve %s: %w", d, err)
		}
		empty, _, err := dirEmpty(abs)
		if err != nil {
			return nil, err
		}
		if !empty {
			return nil, fmt.Errorf("fixture: %s is not empty", abs)
		}
		targets[i] = abs
	}
	opts.Home = ""
	key := scenarioKey{name, opts, len(dirs)}
	scenariosMu.Lock()
	t := scenarios[key]
	if t == nil {
		t = &template{history: true}
		scenarios[key] = t
	}
	scenariosMu.Unlock()
	t.once.Do(func() {
		// A build that panics must not leave a scenario that reads as built.
		t.err = fmt.Errorf("fixture: the build of scenario %q did not finish", name)
		t.err = t.buildScenario(len(dirs), build)
	})
	if t.err != nil {
		return nil, fmt.Errorf("fixture: scenario %q: %w", name, t.err)
	}
	for i, dir := range targets {
		if !servableTo(t.roots[i], dir) {
			return build(targets)
		}
	}
	fx, err := t.copyTo(targets)
	if err != nil {
		return nil, fmt.Errorf("fixture: scenario %q: %w", name, err)
	}
	return fx, nil
}

// buildScenario runs build in new directories under a temp directory of its
// own and plans copies of what it made.
func (t *template) buildScenario(n int, build func(dirs []string) (*Fixture, error)) error {
	root, err := os.MkdirTemp("", "jig-fixture-scenario")
	if err != nil {
		return fmt.Errorf("create the build dir: %w", err)
	}
	gittest.AtExit(func() { os.RemoveAll(root) })
	t.root = root
	for i := 0; i < n; i++ {
		sub := fmt.Sprintf("d%d", i)
		dir := filepath.Join(root, sub)
		if err := os.Mkdir(dir, 0o755); err != nil {
			os.RemoveAll(root)
			return err
		}
		t.subs, t.roots = append(t.subs, sub), append(t.roots, dir)
	}
	fx, err := build(append([]string(nil), t.roots...))
	if err != nil {
		os.RemoveAll(root)
		return fmt.Errorf("build: %w", err)
	}
	t.fx = fx
	if err := t.scan(); err != nil {
		os.RemoveAll(root)
		return fmt.Errorf("scan the build: %w", err)
	}
	return nil
}

// discovered lists the scenario's repositories that a copy rewrites: each work
// tree (a slash path under the template's root with a .git directory), with
// the bare repositories (bares) its config names as remotes.
func (t *template) discovered(workTrees []string, bares map[string]bool) []tplRepo {
	sort.Strings(workTrees)
	var repos []tplRepo
	for _, wt := range workTrees {
		r := tplRepo{dir: wt}
		cfg, err := os.ReadFile(filepath.Join(t.root, filepath.FromSlash(wt), ".git", "config"))
		if err == nil {
			for _, line := range strings.Split(string(cfg), "\n") {
				key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
				if !ok || strings.TrimSpace(key) != "url" {
					continue
				}
				// Git writes a backslash in a config value doubled.
				url := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(strings.Trim(strings.TrimSpace(value), `"`), `\\`, `\`)))
				rel, err := filepath.Rel(t.root, url)
				if err != nil {
					continue
				}
				if slash := filepath.ToSlash(rel); bares[slash] {
					r.remotes = append(r.remotes, slash)
				}
			}
		}
		repos = append(repos, r)
	}
	return repos
}

// maxHistoryPasses bounds the passes rewriteHistories makes: a repository's
// files can hold the ids of another's commits, which move when that other is
// rewritten, so a pass over every repository can change what the next one
// reads. The chain a fixture makes is two or three long.
const maxHistoryPasses = 8

// rewriteHistories makes the commits of every repository of a scenario's copy
// say what a build in the copy's directories would have made. The files of the
// copy already hold the new directories (copyData replaced them); a commit that
// held a root in a file now has other content, so its id changes, and so do
// the ids of the commits on top of it, and a file that names such a commit (a
// journal that records the commits it saw, the start of a ticket) must name the
// new one. So each repository is rewritten (gitx.RewriteHistoryFunc: every
// version of each file that holds a root or an old id, in every commit) with
// the ids moved so far, and the passes repeat until none moves a commit.
func (t *template) rewriteHistories(targets []string, roots *strings.Replacer) error {
	moved := map[string]string{} // an id before the copy, and what it is now
	for pass := 0; pass < maxHistoryPasses; pass++ {
		changed := false
		for _, r := range t.repos {
			work := t.abs(r.dir, targets)
			var mirrors []string
			for _, m := range r.remotes {
				mirrors = append(mirrors, t.abs(m, targets))
			}
			if r.remote != "" {
				mirrors = append(mirrors, t.abs(r.remote, targets))
			}
			ids := idReplacer(moved)
			paths, err := pointFilesAtIDs(work, r.paths, moved, ids)
			if err != nil {
				return fmt.Errorf("%s: %w", path.Base(r.dir), err)
			}
			transform := func(_ string, data []byte) []byte {
				return []byte(ids.Replace(roots.Replace(string(data))))
			}
			got, err := gitx.RewriteHistoryFunc(work, paths, transform, mirrors...)
			if err != nil {
				return fmt.Errorf("%s: %w", path.Base(r.dir), err)
			}
			if len(got) == 0 {
				continue
			}
			changed = true
			// Compose: an id that moved before and moves again is the last.
			for from, to := range moved {
				if again, ok := got[to]; ok {
					moved[from] = again
				}
			}
			for from, to := range got {
				moved[from] = to
			}
		}
		if !changed {
			return t.pointUntrackedAtIDs(targets, idReplacer(moved), moved)
		}
	}
	return fmt.Errorf("the commits were still moving after %d passes", maxHistoryPasses)
}

// idReplacer replaces each old id of moved by its new one.
func idReplacer(moved map[string]string) *strings.Replacer {
	pairs := make([]string, 0, 2*len(moved))
	for from, to := range moved {
		pairs = append(pairs, from, to)
	}
	return strings.NewReplacer(pairs...)
}

// pointFilesAtIDs writes, into each file of the work tree at dir that holds an
// id of moved, the new one, and returns paths and those files (slash paths
// relative to dir).
func pointFilesAtIDs(dir string, paths []string, moved map[string]string, ids *strings.Replacer) ([]string, error) {
	out := append([]string(nil), paths...)
	if len(moved) == 0 {
		return out, nil
	}
	seen := map[string]bool{}
	for _, p := range paths {
		seen[p] = true
	}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		changed, err := replaceInFile(p, ids, moved)
		if err != nil || !changed {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// replaceInFile applies ids to the file at p if it holds an id of moved, and
// reports whether it did.
func replaceInFile(p string, ids *strings.Replacer, moved map[string]string) (bool, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return false, err
	}
	hit := false
	for from := range moved {
		if strings.Contains(string(data), from) {
			hit = true
			break
		}
	}
	if !hit {
		return false, nil
	}
	info, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(p, []byte(ids.Replace(string(data))), info.Mode().Perm())
}

// pointUntrackedAtIDs points the files of the copy that no repository tracks
// (state a build wrote beside its repositories) at the new ids. A file in a
// repository's git directory or in a bare repository is left to the rewrite.
func (t *template) pointUntrackedAtIDs(targets []string, ids *strings.Replacer, moved map[string]string) error {
	if len(moved) == 0 {
		return nil
	}
	bare := map[string]bool{}
	for _, b := range t.bareDirs {
		bare[filepath.Clean(t.abs(b, targets))] = true
	}
	for _, target := range targets {
		err := filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == ".git" || bare[filepath.Clean(p)]) {
				return filepath.SkipDir
			}
			if !d.Type().IsRegular() {
				return nil
			}
			_, err = replaceInFile(p, ids, moved)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
