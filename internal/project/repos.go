package project

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// Repo states, as `jig repos` reports them.
const (
	// RepoCheckedOut: the repo's directory is a clone here.
	RepoCheckedOut = "checked out"
	// RepoNotHere: a submodule the checkout has not fetched; it is part of the
	// project and simply "not checked out here".
	RepoNotHere = "not checked out here"
	// RepoMissing: the repo is listed, its directory is not there, and
	// nothing says where to get it from.
	RepoMissing = "missing"
)

// RepoStatus is one listed repo of an orchestrator and what is on this
// machine for it.
type RepoStatus struct {
	Repo  MarkerRepo
	Dir   string
	State string
	// Submodule reports that .gitmodules declares the repo's path.
	Submodule bool
}

// urlArg matches what `jig init` and `jig repos add` take as a git remote
// URL: anything with a scheme, or scp-like user@host:path. A bare path is
// never one, even when it names a bare repository on this machine: an
// orchestrator references the repos nested in it, so a path has to be one of
// those.
var urlArg = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]*://|[^@/\\\s:]+@[^/\\\s:]+:)`)

// IsRepoURL reports whether arg is a git remote URL rather than a path.
func IsRepoURL(arg string) bool { return urlArg.MatchString(arg) }

// URLRepoName is the folder name git clone would give a repository at url:
// the last element of its path, without ".git".
func URLRepoName(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), "/\\")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, "/\\:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// NestedRepos lists the repos nested directly in root: each direct child
// directory that has a .git (a directory, or the file a submodule and a
// linked worktree have), and each direct submodule path .gitmodules declares
// even when the checkout has not fetched it. Hidden directories are not
// repos. The result is sorted by path.
func NestedRepos(root string) []MarkerRepo {
	seen := map[string]bool{}
	var out []MarkerRepo
	add := func(name string) {
		if name == "" || seen[name] || strings.HasPrefix(name, ".") {
			return
		}
		seen[name] = true
		out = append(out, MarkerRepo{Name: name, Path: name})
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.IsDir() && isGitDir(filepath.Join(root, e.Name())) {
				add(e.Name())
			}
		}
	}
	for _, p := range Submodules(root) {
		if !strings.Contains(p, "/") {
			add(p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Statuses reports each of an orchestrator's listed repos: its directory and
// whether it is checked out here.
func Statuses(root string, repos []MarkerRepo) []RepoStatus {
	subs := map[string]bool{}
	for _, p := range Submodules(root) {
		subs[p] = true
	}
	out := make([]RepoStatus, len(repos))
	for i, r := range repos {
		dir := RepoDir(root, r)
		st := RepoStatus{Repo: r, Dir: dir, Submodule: subs[relativeTo(root, dir)]}
		switch {
		case isGitDir(dir):
			st.State = RepoCheckedOut
		case st.Submodule:
			st.State = RepoNotHere
		case r.Remote != "":
			st.State = RepoNotHere
		default:
			st.State = RepoMissing
		}
		out[i] = st
	}
	return out
}

// RepoArg is one repo `jig init` or `jig repos add` was told about, resolved:
// the entry for the marker, and the URL to fetch it from when it is not on
// this machine yet.
type RepoArg struct {
	Repo MarkerRepo
	URL  string
}

// ResolveRepoArgs resolves what the person named, against the orchestrator at
// root. A URL becomes a repo named as git clone would name it, nested directly
// in root; it must not exist yet. Anything else is a path under root to a
// directory that is a repo. direct limits paths to root's direct children (what
// `jig init` takes; `jig repos add` takes a path of any depth). Each repo is
// named once.
func ResolveRepoArgs(root string, args []string, direct bool) ([]RepoArg, error) {
	var out []RepoArg
	names := map[string]string{}
	for _, arg := range args {
		var ra RepoArg
		if IsRepoURL(arg) {
			name := URLRepoName(arg)
			if name == "" || strings.HasPrefix(name, ".") {
				return nil, &axi.Error{Msg: fmt.Sprintf("cannot name a repository folder after the URL %q", arg), Code: "VALIDATION_ERROR"}
			}
			if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("%s already exists: %q would be fetched into it", filepath.Join(root, name), arg),
					Code: "VALIDATION_ERROR",
					Help: []string{fmt.Sprintf("Name the folder it is: `jig repos add %s`, or move it away and run this again", name)},
				}
			}
			ra = RepoArg{Repo: MarkerRepo{Name: name, Path: name}, URL: arg}
		} else {
			rel, err := underRoot(root, arg)
			if err != nil {
				return nil, err
			}
			if direct && strings.Contains(rel, "/") {
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("%q is not a direct child of %s: init takes the repos nested directly in the orchestrator", arg, root),
					Code: "VALIDATION_ERROR",
					Help: []string{"List a deeper repo afterwards with `jig repos add " + rel + "`"},
				}
			}
			dir := filepath.Join(root, filepath.FromSlash(rel))
			if !isGitDir(dir) {
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("%s is not a repository (it has no .git)", dir),
					Code: "VALIDATION_ERROR",
					Help: []string{"Pass the git URL to fetch it into the orchestrator, or `git init` / `git clone` it there first"},
				}
			}
			ra = RepoArg{Repo: MarkerRepo{Name: filepath.Base(filepath.FromSlash(rel)), Path: rel}}
		}
		if prev, dup := names[ra.Repo.Name]; dup {
			return nil, &axi.Error{
				Msg:  fmt.Sprintf("two repos would be named %q: %s and %s", ra.Repo.Name, prev, arg),
				Code: "VALIDATION_ERROR",
			}
		}
		names[ra.Repo.Name] = arg
		out = append(out, ra)
	}
	return out, nil
}

// underRoot returns arg as a slash-separated path under root, refusing a path
// that is root itself or outside it: an orchestrator can only reference repos
// nested within it.
func underRoot(root, arg string) (string, error) {
	p := filepath.FromSlash(arg)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%q is not inside the orchestrator at %s", arg, root),
			Code: "VALIDATION_ERROR",
			Help: []string{"An orchestrator only references repos nested within it: move or clone the repo under it, or pass its git URL"},
		}
	}
	return filepath.ToSlash(rel), nil
}

// FetchRepo fetches the repo of ra into the orchestrator at root: as a
// submodule written with ignore = all, so its commits never dirty the
// orchestrator, or, with clone, as a clone git ignores (the hidden structure:
// nothing about the repo is committed, so its remote goes in jig.yaml). It
// returns the entry to list. Nothing is committed in the orchestrator; the
// person commits .gitmodules and jig.yaml.
func FetchRepo(root string, ra RepoArg, clone bool) (MarkerRepo, error) {
	r := ra.Repo
	if clone {
		if _, err := gitx.Run(root, "clone", ra.URL, r.Path); err != nil {
			return MarkerRepo{}, fmt.Errorf("project: clone %s: %w", ra.URL, err)
		}
		if err := ignoreInGit(root, r.Path); err != nil {
			return MarkerRepo{}, err
		}
		r.Remote = ra.URL
		return r, nil
	}
	args := []string{}
	if gitx.IsLocalRemote(ra.URL) {
		// A URL naming a place on this machine is the person's own word;
		// git's default refuses the file protocol for submodules.
		args = append(args, "-c", "protocol.file.allow=always")
	}
	args = append(args, "submodule", "add", ra.URL, r.Path)
	if _, err := gitx.Run(root, args...); err != nil {
		return MarkerRepo{}, fmt.Errorf("project: add submodule %s: %w", ra.URL, err)
	}
	name := submoduleNameFor(root, r.Path)
	if name == "" {
		name = r.Path
	}
	if _, err := gitx.Run(root, "config", "-f", ".gitmodules", "submodule."+name+".ignore", "all"); err != nil {
		return MarkerRepo{}, fmt.Errorf("project: write .gitmodules: %w", err)
	}
	if _, err := gitx.Run(root, "add", ".gitmodules"); err != nil {
		return MarkerRepo{}, fmt.Errorf("project: stage .gitmodules: %w", err)
	}
	return r, nil
}

// submoduleNameFor is the name .gitmodules gives the submodule at path.
func submoduleNameFor(root, path string) string {
	for _, e := range submoduleEntries(root) {
		if e.path == path {
			return e.name
		}
	}
	return ""
}

// ignoreInGit adds /<path>/ to root's .gitignore unless a line already has it.
func ignoreInGit(root, path string) error {
	file := filepath.Join(root, ".gitignore")
	line := "/" + strings.Trim(path, "/") + "/"
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("project: read .gitignore: %w", err)
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += line + "\n"
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		return fmt.Errorf("project: write .gitignore: %w", err)
	}
	return nil
}

// SyncReport is what Sync changed in a repo list.
type SyncReport struct {
	// Added are the nested repos the list lacked.
	Added []MarkerRepo
	// Removed are the listed repos whose folder is gone.
	Removed []MarkerRepo
	// NotHere are the listed submodules the checkout has not fetched; they
	// stay listed unless the person said to drop them.
	NotHere []MarkerRepo
}

// Sync reconciles an orchestrator's listed repos with its folder: it adds the
// nested repos the list lacks, and removes listed ones whose folder is gone. A
// listed submodule whose folder is there but unfetched (or empty) is merely not
// checked out here: it stays, unless drop says to remove it (drop is where the
// person is asked; nil keeps them all). A listed repo that carries its own
// remote and has no folder stays too: another checkout can clone it. It
// returns the new list, in the order of the old one with additions sorted at
// the end.
func Sync(root string, listed []MarkerRepo, drop func(MarkerRepo) bool) ([]MarkerRepo, SyncReport) {
	var rep SyncReport
	subs := map[string]bool{}
	for _, p := range Submodules(root) {
		subs[p] = true
	}
	have := map[string]bool{}
	var out []MarkerRepo
	for _, r := range listed {
		dir := RepoDir(root, r)
		rel := relativeTo(root, dir)
		have[rel] = true
		switch {
		case isGitDir(dir):
			out = append(out, r)
		case subs[rel]:
			if drop != nil && drop(r) {
				rep.Removed = append(rep.Removed, r)
				continue
			}
			rep.NotHere = append(rep.NotHere, r)
			out = append(out, r)
		case r.Remote != "":
			rep.NotHere = append(rep.NotHere, r)
			out = append(out, r)
		default:
			rep.Removed = append(rep.Removed, r)
		}
	}
	for _, n := range NestedRepos(root) {
		if have[n.Path] {
			continue
		}
		rep.Added = append(rep.Added, n)
		out = append(out, n)
	}
	return out, rep
}

// RemoveRepo takes the repo called name out of list, returning the rest and
// the one removed. The folder is not touched.
func RemoveRepo(list []MarkerRepo, name string) ([]MarkerRepo, MarkerRepo, error) {
	var out []MarkerRepo
	var gone MarkerRepo
	found := false
	for _, r := range list {
		if RepoName(r) == name && !found {
			gone, found = r, true
			continue
		}
		out = append(out, r)
	}
	if !found {
		var names []string
		for _, r := range list {
			names = append(names, RepoName(r))
		}
		return nil, MarkerRepo{}, &axi.Error{
			Msg:  fmt.Sprintf("the project lists no repo %q (it lists: %s)", name, strings.Join(names, ", ")),
			Code: "VALIDATION_ERROR",
		}
	}
	return out, gone, nil
}

// MergeRepos adds the entries of add to list, in order, refusing a name or
// path the list already has.
func MergeRepos(list, add []MarkerRepo) ([]MarkerRepo, error) {
	out := append([]MarkerRepo(nil), list...)
	for _, a := range add {
		for _, r := range out {
			if RepoName(r) == RepoName(a) || (r.Path != "" && r.Path == a.Path) {
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("the project already lists a repo %q", RepoName(a)),
					Code: "VALIDATION_ERROR",
				}
			}
		}
		out = append(out, a)
	}
	return out, nil
}

// MapRepos records the repos of the project name in the machine map under
// jigHome: each repo's directory as its clone, and the store and root when the
// entry has none. A clone already there is replaced by the one given: the
// person just said where the repo is.
func MapRepos(jigHome, name, storeDir, root string, repos []RepoStatus) error {
	return UpdateMachine(jigHome, func(m map[string]MachineProject) error {
		mp := m[name]
		if mp.Store == "" {
			mp.Store = storeDir
		}
		if mp.Root == "" {
			mp.Root = root
		}
		clones := make(map[string]string, len(mp.Clones)+len(repos))
		for k, v := range mp.Clones {
			clones[k] = v
		}
		for _, r := range repos {
			if r.State == RepoCheckedOut {
				clones[RepoName(r.Repo)] = r.Dir
			}
		}
		mp.Clones = clones
		m[name] = mp
		return nil
	})
}

// UnmapRepos takes the named repos' clones out of the project's entry in the
// machine map.
func UnmapRepos(jigHome, name string, repos []string) error {
	return UpdateMachine(jigHome, func(m map[string]MachineProject) error {
		mp, ok := m[name]
		if !ok {
			return nil
		}
		clones := make(map[string]string, len(mp.Clones))
		for k, v := range mp.Clones {
			clones[k] = v
		}
		for _, r := range repos {
			delete(clones, r)
		}
		mp.Clones = clones
		m[name] = mp
		return nil
	})
}
