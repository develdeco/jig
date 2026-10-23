package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// The two files a store keeps its configuration in. A v2 store has
// project.yaml; a v3 store has store.yaml instead (the project's own settings
// moved to the jig.yaml marker in its repo). A store has exactly one.
const (
	StoreFile       = "store.yaml"
	LegacyStoreFile = "project.yaml"
)

// StoreConfigPath is the path of the config file the store at storeRoot has:
// store.yaml, else project.yaml. It refuses a store that has both, which no
// jig version writes (the v2 to v3 migration swaps one for the other in one
// commit), and one that has neither.
func StoreConfigPath(storeRoot string) (string, error) {
	v3 := filepath.Join(storeRoot, StoreFile)
	v2 := filepath.Join(storeRoot, LegacyStoreFile)
	has3, has2 := isFile(v3), isFile(v2)
	switch {
	case has3 && has2:
		return "", &axi.Error{
			Msg:  fmt.Sprintf("the store at %s has both %s and %s", storeRoot, StoreFile, LegacyStoreFile),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("A v3 store has %s only: remove %s if the store was migrated, or %s if it was not", StoreFile, LegacyStoreFile, StoreFile)},
		}
	case has3:
		return v3, nil
	case has2:
		return v2, nil
	}
	return "", &axi.Error{
		Msg:  fmt.Sprintf("the directory %s is not a jig store: it has neither %s nor %s", storeRoot, StoreFile, LegacyStoreFile),
		Code: "VALIDATION_ERROR",
	}
}

// IsStoreDir reports whether dir holds a store's config file, of either
// layout.
func IsStoreDir(dir string) bool {
	return dir != "" && (isFile(filepath.Join(dir, StoreFile)) || isFile(filepath.Join(dir, LegacyStoreFile)))
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// LoadStoreConfig reads the store's own configuration: store.yaml of a v3
// store, project.yaml of a v2 (or older) one. It is the one door to that
// file: nothing else names it. A v3 Config has no Repos, and a Name only when
// the store serves exactly one project; LoadConfig completes it from the
// project's marker.
func LoadStoreConfig(storeRoot string) (Config, error) {
	path, err := StoreConfigPath(storeRoot)
	if err != nil {
		return Config{}, err
	}
	return Load(path)
}

// LoadConfig is LoadStoreConfig completed with the project's own part, for a
// v3 store: the jig.yaml marker supplies the project's name and repos
// (WithMarkerRepos). The marker is looked for at each of roots in turn (the
// directories the caller knows the project is rooted at: Locate's root and
// repo directory), then at the root and clones the machine map under jigHome
// has for the project. A marker that says it is a member's is skipped: it
// points up at the project, it does not describe it. A v2 store carries its
// repos itself and comes back as LoadStoreConfig has it; a v3 store whose
// marker cannot be found has no Repos, and the verbs that need one say so.
// name is the project's name as the caller found it, used when neither the
// store nor a marker says.
func LoadConfig(jigHome, storeRoot, name string, roots ...string) (Config, error) {
	cfg, err := LoadStoreConfig(storeRoot)
	if err != nil || cfg.SchemaVersion < 3 {
		return cfg, err
	}
	if cfg.Name == "" {
		cfg.Name = name
	}
	try := func(dir string) (Config, bool) {
		if dir == "" || !isFile(filepath.Join(dir, MarkerFile)) {
			return cfg, false
		}
		m, err := LoadMarker(filepath.Join(dir, MarkerFile))
		if err != nil || m.Kind == KindMember {
			return cfg, false
		}
		if m.Project != "" && len(cfg.Projects) > 0 && !contains(cfg.Projects, m.Project) {
			// A marker of some other project that the store does not serve.
			return cfg, false
		}
		return WithMarkerRepos(cfg, dir, m), true
	}
	for _, dir := range roots {
		if out, ok := try(dir); ok {
			return out, nil
		}
	}
	if jigHome != "" && cfg.Name != "" {
		if machine, err := LoadMachine(jigHome); err == nil {
			mp := machine[cfg.Name]
			if out, ok := try(mp.Root); ok {
				return out, nil
			}
			for _, clone := range sortedKeys(mp.Clones) {
				if out, ok := try(mp.Clones[clone]); ok {
					return out, nil
				}
			}
		}
	}
	return cfg, nil
}

// WithMarkerRepos fills cfg's name and repos from the marker m found in the
// directory root. The project's name is the marker's. An orchestrator's repos
// are the marker's repos, each at its path under root; any other kind of
// project has the one repo at root itself, named by the marker's first repo
// when it lists one, else by the project. A repo's remote is, in turn: the
// marker's, the origin of the repo's own clone, the URL .gitmodules gives its
// path, else the repo's directory on this machine (pool clones work from a
// local path, and a project that is not shared has no other address).
func WithMarkerRepos(cfg Config, root string, m Marker) Config {
	if m.Project != "" {
		cfg.Name = m.Project
	}
	var repos []Repo
	if m.Kind == KindOrchestrator {
		for _, r := range m.Repos {
			dir := RepoDir(root, r)
			repos = append(repos, Repo{
				Label:  RepoName(r),
				Remote: repoRemote(root, r, dir),
				Target: r.Target,
			})
		}
	} else {
		r := MarkerRepo{Name: cfg.Name}
		if len(m.Repos) > 0 {
			first := m.Repos[0]
			if first.Name != "" {
				r.Name = first.Name
			}
			r.Remote, r.Target = first.Remote, first.Target
		}
		if r.Name == "" {
			r.Name = filepath.Base(root)
		}
		repos = []Repo{{Label: r.Name, Remote: repoRemote(root, r, root), Target: r.Target}}
	}
	cfg.Repos = repos
	if m.Branches != nil {
		cfg.BranchesFrom = m.Branches.From
	}
	return cfg
}

// RepoName is the name a marker repo goes by: its name, else the last element
// of its path.
func RepoName(r MarkerRepo) string {
	if r.Name != "" {
		return r.Name
	}
	return filepath.Base(filepath.FromSlash(r.Path))
}

// RepoDir is the directory of a marker repo on this machine: its path (or, with
// none, its name) under root.
func RepoDir(root string, r MarkerRepo) string {
	p := r.Path
	if p == "" {
		p = r.Name
	}
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

// repoRemote is a repo's remote, by the rule WithMarkerRepos states.
func repoRemote(root string, r MarkerRepo, dir string) string {
	if r.Remote != "" {
		return r.Remote
	}
	if isGitDir(dir) {
		if origin := OriginOf(dir); origin != "" {
			return origin
		}
	}
	if dir != root {
		if url := GitmodulesURL(root, relativeTo(root, dir)); url != "" {
			return url
		}
	}
	return dir
}

// OriginOf is the URL of dir's origin remote, "" when it has none (or dir is
// no repo).
func OriginOf(dir string) string {
	out, err := gitx.Run(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return out
}

// isGitDir reports whether dir is a repo's top level: it has a .git, a
// directory or (a linked worktree, a submodule) a file.
func isGitDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// relativeTo is dir's slash-separated path under root, or dir as it is when
// it is not under root.
func relativeTo(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(rel)
}

// Submodules lists the submodule paths root's .gitmodules declares, in file
// order, slash-separated. A root with no .gitmodules has none.
func Submodules(root string) []string {
	var paths []string
	for _, e := range submoduleEntries(root) {
		paths = append(paths, e.path)
	}
	return paths
}

// GitmodulesURL is the URL root's .gitmodules gives the submodule at path (a
// slash-separated path under root), "" when there is none.
func GitmodulesURL(root, path string) string {
	for _, e := range submoduleEntries(root) {
		if e.path != path {
			continue
		}
		out, err := gitx.Run(root, "config", "-f", ".gitmodules", "--get", "submodule."+e.name+".url")
		if err != nil {
			return ""
		}
		return out
	}
	return ""
}

// submoduleEntry is one submodule .gitmodules declares: its name and path.
type submoduleEntry struct{ name, path string }

// submoduleEntries reads root's .gitmodules through git config, so quoting and
// includes are git's own.
func submoduleEntries(root string) []submoduleEntry {
	if !isFile(filepath.Join(root, ".gitmodules")) {
		return nil
	}
	out, err := gitx.Run(root, "config", "-f", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return nil
	}
	var entries []submoduleEntry
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		entries = append(entries, submoduleEntry{name: name, path: strings.TrimSpace(value)})
	}
	return entries
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
