package locate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
)

// resolve runs the resolution order (see Locate) and fills l.w with what
// the first matching step found.
func (l *locator) resolve() error {
	if l.o.StoreFlag != "" {
		return l.byFlag()
	}
	ignored, err := project.LoadIgnored(l.jigHome)
	if err != nil {
		return err
	}
	if len(ignored) > 0 {
		if dir, ok := l.ignoredBy(ignored); ok {
			l.explain("ignored: %s is under %s, which `jig ignore` told jig not to track", l.cwd, dir)
			l.explain("none: %s is not tracked", l.cwd)
			return nil
		}
	}

	dir, m, ok, err := project.FindMarker(l.cwd)
	if err != nil {
		return err
	}
	switch {
	case !ok:
		l.explain("marker: no %s from %s up", project.MarkerFile, l.cwd)
	case !isMarker(m):
		l.explain("marker: %s names no project, store or repos; not read as a jig marker", filepath.Join(dir, project.MarkerFile))
	default:
		return l.byMarker(dir, m)
	}

	if l.byMapPath() || l.byMapRemote() || l.byStoreDir() {
		return nil
	}
	l.explain("none: %s is not in a jig project", l.cwd)
	return nil
}

// isMarker reports whether m says anything a marker says. A jig.yaml that
// decodes to nothing jig knows is some other tool's file.
func isMarker(m project.Marker) bool {
	return m.Jig != "" || m.Project != "" || m.Store != "" || m.Kind != "" || len(m.Repos) > 0
}

// byFlag takes the store --store names.
func (l *locator) byFlag() error {
	storePath := l.o.StoreFlag
	if !filepath.IsAbs(storePath) {
		storePath = filepath.Join(l.cwd, storePath)
	}
	cfg, err := project.Load(filepath.Join(storePath, "project.yaml"))
	if err != nil {
		return err
	}
	w := &l.w
	w.How, w.StorePath, w.Project, w.Kind = HowFlag, storePath, cfg.Name, project.KindProject
	l.explain("flag: --store names %s (project %s)", storePath, cfg.Name)
	mp := l.machine[cfg.Name]
	if name, dir, ok := l.cloneContaining(mp.Clones); ok {
		w.Repo, w.RepoDir = name, dir
		l.explain("repo: %s is inside clone %s of %s", l.cwd, dir, name)
	}
	return nil
}

// byMarker takes the project a jig.yaml marker names, and finds its store.
func (l *locator) byMarker(dir string, m project.Marker) error {
	file := filepath.Join(dir, project.MarkerFile)
	kind := m.Kind
	if kind == "" {
		kind = project.KindProject
	}
	l.explain("marker: %s names project %q (kind %s)", file, m.Project, kind)

	name := m.Project
	if name == "" {
		name = l.projectRootedAt(dir)
	}
	w := &l.w
	w.How, w.Kind, w.Root, w.StoreRemote = HowMarker, kind, dir, m.Store

	storePath := l.markerStore(dir, name, m)
	if storePath != "" {
		w.StorePath = storePath
		if n := storeName(storePath); n != "" && n != name {
			l.explain("project: the store at %s is named %q, not %q", storePath, n, name)
			name = n
		}
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	w.Project = name
	mp := l.machine[name]

	var cfgRepos []project.Repo
	if storePath != "" {
		if cfg, err := project.Load(filepath.Join(storePath, "project.yaml")); err == nil {
			cfgRepos = cfg.Repos
		}
	}

	clones := map[string]string{}
	if kind == project.KindOrchestrator {
		best := ""
		for _, r := range m.Repos {
			rd := r.Path
			if rd == "" {
				rd = r.Name
			}
			if !filepath.IsAbs(rd) {
				rd = filepath.Join(dir, rd)
			}
			if fi, err := os.Stat(rd); err != nil || !fi.IsDir() {
				continue
			}
			rname := r.Name
			if rname == "" {
				rname = filepath.Base(rd)
			}
			w.Nested = append(w.Nested, rd)
			clones[rname] = rd
			if inside(rd, l.cwd) && len(rd) > len(best) {
				best, w.Repo, w.RepoDir = rd, rname, rd
			}
		}
		if w.Repo == "" {
			l.explain("repo: %s is in the orchestrator itself, in none of its %d repos", l.cwd, len(w.Nested))
		} else {
			l.explain("repo: %s is inside %s, repo %s of the orchestrator", l.cwd, w.RepoDir, w.Repo)
		}
	} else {
		repoDir := cloneRootOf(dir)
		w.Repo, w.RepoDir = l.repoNameAt(repoDir, cfgRepos, mp, m), repoDir
		clones[w.Repo] = repoDir
		l.explain("repo: %s at %s", w.Repo, repoDir)
	}

	if storePath != "" {
		e := &mapEntry{project: name, store: storePath, clones: clones}
		if kind != project.KindMember {
			e.root = dir
		}
		l.entry = e
	}
	return nil
}

// projectRootedAt names the mapped project whose marker directory is dir, ""
// when none.
func (l *locator) projectRootedAt(dir string) string {
	for _, name := range sortedKeys(l.machine) {
		if root := l.machine[name].Root; root != "" && samePath(root, dir) {
			return name
		}
	}
	return ""
}

// markerStore finds the store of the project a marker names: the machine
// map's entry, a store directory the marker itself names, or the sibling
// <root>/../<project>-tickets. It returns "" when the store is not on this
// machine, and says why.
func (l *locator) markerStore(dir, name string, m project.Marker) string {
	if name != "" {
		if mp, ok := l.machine[name]; ok && mp.Store != "" {
			if isStoreDir(mp.Store) {
				l.explain("store: %s (machine map entry for %s)", mp.Store, name)
				return mp.Store
			}
			l.explain("store: the machine map puts %s at %s, but no store is there", name, mp.Store)
		}
	}
	if m.Store != "" && !isRemoteURL(m.Store) {
		p := m.Store
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if isStoreDir(p) {
			l.explain("store: %s (the marker's store, a path)", p)
			return p
		}
	}
	sibling := ""
	if name != "" {
		sibling = filepath.Join(filepath.Dir(dir), name+"-tickets")
		if isStoreDir(sibling) {
			l.explain("store: %s (sibling of %s)", sibling, dir)
			return sibling
		}
	}
	switch {
	case m.Store != "" && sibling != "":
		l.explain("store: not cloned here; the marker's remote is %s, expected at %s", m.Store, sibling)
	case m.Store != "":
		l.explain("store: not cloned here; the marker's remote is %s", m.Store)
	default:
		l.explain("store: the marker names none and no sibling store is next to %s", dir)
	}
	return ""
}

// repoNameAt names the repo of the project that sits at dir: the clone the
// machine map already has there, the project's only repo, the repo whose
// remote dir's origin is, the marker's only repo, else the directory's name.
func (l *locator) repoNameAt(dir string, cfgRepos []project.Repo, mp project.MachineProject, m project.Marker) string {
	for _, name := range sortedKeys(mp.Clones) {
		if samePath(mp.Clones[name], dir) {
			return name
		}
	}
	if len(cfgRepos) == 1 {
		return cfgRepos[0].Name()
	}
	if len(cfgRepos) > 1 {
		if origin := originOf(dir); origin != "" {
			norm := NormalizeRemote(origin)
			for _, r := range cfgRepos {
				if NormalizeRemote(r.Remote) == norm {
					return r.Name()
				}
			}
		}
	}
	if len(m.Repos) == 1 && m.Repos[0].Name != "" {
		return m.Repos[0].Name
	}
	return filepath.Base(dir)
}

// byMapPath finds the mapped clone that holds cwd: the longest clone path
// containing it, else the clone it is a linked worktree of.
func (l *locator) byMapPath() bool {
	type candidate struct {
		project, repo, dir string
		isRoot             bool
	}
	var cands []candidate
	for _, p := range sortedKeys(l.machine) {
		mp := l.machine[p]
		for _, repo := range sortedKeys(mp.Clones) {
			if mp.Clones[repo] != "" {
				cands = append(cands, candidate{p, repo, mp.Clones[repo], false})
			}
		}
		if mp.Root != "" {
			cands = append(cands, candidate{p, "", mp.Root, true})
		}
	}
	best := -1
	for i, c := range cands {
		if inside(c.dir, l.cwd) && (best < 0 || len(filepath.Clean(c.dir)) > len(filepath.Clean(cands[best].dir))) {
			best = i
		}
	}
	relation := "is inside"
	if best < 0 && !isStoreDir(l.cwd) {
		// (A store is no clone of a repo, so a directory holding one costs
		// no git call to say so.)
		if root := l.cloneRoot(); root != "" {
			for i, c := range cands {
				if !c.isRoot && gitx.SameDir(c.dir, root) {
					best, relation = i, "is a linked worktree of"
					break
				}
			}
		}
	}
	if best < 0 {
		l.explain("map-path: no mapped clone holds %s", l.cwd)
		return false
	}
	c := cands[best]
	mp := l.machine[c.project]
	w := &l.w
	w.How, w.Project, w.Kind = HowMapPath, c.project, project.KindProject
	w.StorePath, w.Repo = mp.Store, c.repo
	if !c.isRoot {
		w.RepoDir = c.dir
	}
	w.Root = mp.Root
	if w.Root == "" {
		w.Root = c.dir
	} else {
		l.shapeFromMarker(w.Root)
	}
	l.explain("map-path: %s %s %s, repo %q of project %s", l.cwd, relation, c.dir, c.repo, c.project)
	return true
}

// shapeFromMarker reads the marker at root, if there is one, for the kind of
// directory the project's root is and the repos nested under it.
func (l *locator) shapeFromMarker(root string) {
	m, err := project.LoadMarker(filepath.Join(root, project.MarkerFile))
	if err != nil {
		return
	}
	if m.Kind != "" {
		l.w.Kind = m.Kind
	}
	if l.w.Kind != project.KindOrchestrator {
		return
	}
	for _, r := range m.Repos {
		rd := r.Path
		if rd == "" {
			rd = r.Name
		}
		if !filepath.IsAbs(rd) {
			rd = filepath.Join(root, rd)
		}
		if fi, err := os.Stat(rd); err == nil && fi.IsDir() {
			l.w.Nested = append(l.w.Nested, rd)
		}
	}
}

// byMapRemote finds the mapped project one of whose repos has the remote cwd's
// repository has as origin, and maps this clone for it.
func (l *locator) byMapRemote() bool {
	if isStoreDir(l.cwd) {
		return false
	}
	top := l.toplevel()
	if top == "" {
		l.explain("map-remote: %s is not in a git work tree", l.cwd)
		return false
	}
	origin := originOf(top)
	if origin == "" {
		l.explain("map-remote: %s has no origin remote", top)
		return false
	}
	norm := NormalizeRemote(origin)
	for _, p := range sortedKeys(l.machine) {
		mp := l.machine[p]
		if mp.Store == "" {
			continue
		}
		cfg, err := project.Load(filepath.Join(mp.Store, "project.yaml"))
		if err != nil {
			continue
		}
		for _, r := range cfg.Repos {
			if NormalizeRemote(r.Remote) != norm {
				continue
			}
			clone := cloneRootOf(top)
			w := &l.w
			w.How, w.Project, w.Kind = HowMapRemote, p, project.KindProject
			w.StorePath, w.Repo, w.RepoDir, w.Root = mp.Store, r.Name(), clone, clone
			l.explain("map-remote: origin %s is repo %s of project %s", origin, r.Name(), p)
			l.entry = &mapEntry{project: p, store: mp.Store, clones: map[string]string{r.Name(): clone}}
			return true
		}
	}
	l.explain("map-remote: origin %s is no repo of a mapped project", origin)
	return false
}

// byStoreDir takes a store cwd is in, or the standalone store next to the repo
// cwd is in.
func (l *locator) byStoreDir() bool {
	w := &l.w
	if isStoreDir(l.cwd) {
		l.setStore(l.cwd)
		l.explain("store dir: %s holds a store", l.cwd)
		return true
	}
	top := l.toplevel()
	if top != "" && looksLikeStore(top) {
		l.setStore(top)
		l.explain("store dir: %s is inside the store at %s", l.cwd, top)
		return true
	}
	for _, d := range []string{l.cwd, top} {
		if d == "" {
			continue
		}
		sibling, err := project.StandaloneStoreDir(d)
		if err != nil || !isStoreDir(sibling) {
			continue
		}
		l.setStore(sibling)
		w.RepoDir = d
		if cfg, err := project.Load(filepath.Join(sibling, "project.yaml")); err == nil && len(cfg.Repos) > 0 {
			w.Repo = cfg.Repos[0].Name()
		}
		w.Root = d
		w.Kind = project.KindProject
		l.explain("store dir: %s is the standalone store next to %s", sibling, d)
		return true
	}
	l.explain("store dir: %s is not in a store", l.cwd)
	return false
}

func (l *locator) setStore(path string) {
	l.w.How, l.w.StorePath, l.w.Project = HowStore, path, storeName(path)
}

// looksLikeStore is isStoreDir for a directory found by climbing out of a
// subdirectory: a project.yaml alone could be any tool's file, so it also
// needs something only a jig store has.
func looksLikeStore(dir string) bool {
	if !isStoreDir(dir) {
		return false
	}
	for _, name := range []string{"ledger.md", "tickets", "platform"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// ignoredBy reports the ignored directory that holds cwd, or the clone cwd is
// a linked worktree of.
func (l *locator) ignoredBy(ignored []string) (string, bool) {
	if dir, ok := project.IsIgnored(ignored, l.cwd); ok {
		return dir, true
	}
	if root := l.cloneRoot(); root != "" {
		if dir, ok := project.IsIgnored(ignored, root); ok {
			return dir, true
		}
	}
	return "", false
}

// cloneContaining picks, of clones (repo name to directory), the one that
// holds cwd by path, the longest first, else the one cwd is a linked worktree
// of.
func (l *locator) cloneContaining(clones map[string]string) (name, dir string, ok bool) {
	for _, n := range sortedKeys(clones) {
		d := clones[n]
		if d != "" && inside(d, l.cwd) && (!ok || len(filepath.Clean(d)) > len(filepath.Clean(dir))) {
			name, dir, ok = n, d, true
		}
	}
	if ok {
		return name, dir, true
	}
	if root := l.cloneRoot(); root != "" {
		for _, n := range sortedKeys(clones) {
			if gitx.SameDir(clones[n], root) {
				return n, clones[n], true
			}
		}
	}
	return "", "", false
}

// cwdInRepo reports whether cwd is a work tree of the repo Where names: inside
// its directory, or a linked worktree of it.
func (l *locator) cwdInRepo() bool {
	if l.toplevel() == "" {
		return false
	}
	if inside(l.w.RepoDir, l.cwd) {
		return true
	}
	root := l.cloneRoot()
	return root != "" && gitx.SameDir(root, l.w.RepoDir)
}

// toplevel is the top of the git work tree cwd is in, "" when it is in none.
// It asks git once.
func (l *locator) toplevel() string {
	if !l.topDone {
		l.topDone = true
		if top, err := gitx.TopLevel(l.cwd); err == nil {
			l.top = top
		}
	}
	return l.top
}

// cloneRoot is the clone the work tree cwd is in belongs to: its own top
// level, or for a linked worktree the clone it was added from.
func (l *locator) cloneRoot() string {
	if !l.rootDone {
		l.rootDone = true
		if top := l.toplevel(); top != "" {
			l.root = cloneRootOf(top)
		}
	}
	return l.root
}

// cloneRootOf is the clone dir belongs to: dir itself when it is a clone's
// top level (or not a repo at all), and for a linked worktree, whose .git is
// a file, the directory holding the common .git.
func cloneRootOf(dir string) string {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil || fi.IsDir() {
		return dir
	}
	common, err := gitx.CommonDir(dir)
	if err != nil || filepath.Base(common) != ".git" {
		return dir
	}
	return filepath.Dir(common)
}

// originOf is the URL of dir's origin remote, "" when it has none.
func originOf(dir string) string {
	out, err := gitx.Run(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return out
}

// mapEntry is what a lookup by marker or remote adds to the machine map: the
// project's store, the marker's directory, and the clones it found.
type mapEntry struct {
	project string
	store   string
	root    string
	clones  map[string]string
}

// apply adds what mp lacks: the store when it has none (or the one it names
// is gone), the root when it has none, and each clone it has none for or
// whose directory is gone. A clone that is there is never replaced. It
// returns the entry and whether it changed.
func (e *mapEntry) apply(mp project.MachineProject, had bool) (project.MachineProject, bool) {
	changed := !had
	if mp.Store != e.store && (mp.Store == "" || !isStoreDir(mp.Store)) {
		mp.Store, changed = e.store, true
	}
	if e.root != "" && (mp.Root == "" || !isDir(mp.Root)) && mp.Root != e.root {
		mp.Root, changed = e.root, true
	}
	clones := make(map[string]string, len(mp.Clones)+len(e.clones))
	for k, v := range mp.Clones {
		clones[k] = v
	}
	for name, dir := range e.clones {
		if cur := clones[name]; cur == "" || (!isDir(cur) && cur != dir) {
			clones[name], changed = dir, true
		}
	}
	mp.Clones = clones
	return mp, changed
}

var errNothingToMap = fmt.Errorf("locate: nothing to map")

// mapOnTheSpot records what the lookup found in the machine map, unless
// Opts.NoMap. A failure to write it is told in Explain and is not an error:
// the lookup itself succeeded.
func (l *locator) mapOnTheSpot() {
	e := l.entry
	if e == nil || l.o.NoMap {
		return
	}
	mp, had := l.machine[e.project]
	if _, changed := e.apply(mp, had); !changed {
		return
	}
	err := project.UpdateMachine(l.jigHome, func(m map[string]project.MachineProject) error {
		cur, had := m[e.project]
		next, changed := e.apply(cur, had)
		if !changed {
			return errNothingToMap
		}
		m[e.project] = next
		return nil
	})
	switch err {
	case nil:
		l.w.Mapped = true
		l.explain("map: recorded project %s in the machine map (store %s, %d clone(s))", e.project, e.store, len(e.clones))
	case errNothingToMap:
	default:
		l.explain("map: could not record project %s in the machine map: %v", e.project, err)
	}
}

// inside reports whether target is base or somewhere below it, by path, then
// by the paths with their symlinks resolved.
func inside(base, target string) bool {
	if base == "" || target == "" {
		return false
	}
	if pathWithin(base, target) {
		return true
	}
	rb, errB := filepath.EvalSymlinks(base)
	rt, errT := filepath.EvalSymlinks(target)
	return errB == nil && errT == nil && (rb != base || rt != target) && pathWithin(rb, rt)
}

// pathWithin reports whether target is base itself or a descendant of it.
func pathWithin(base, target string) bool {
	b, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	t, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(b, t)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !startsWithDotDot(rel) && !filepath.IsAbs(rel))
}

func startsWithDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && os.IsPathSeparator(rel[2])
}

// samePath reports whether a and b name one directory, by path or on disk.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return (pathWithin(a, b) && pathWithin(b, a)) || gitx.SameDir(a, b)
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
