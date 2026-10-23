package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// markerVersion is the version of the marker format `jig init` writes.
const markerVersion = "1"

// Inference is what InferKind found in a folder.
type Inference struct {
	// Kind is KindProject or KindOrchestrator.
	Kind string
	// Nested are the repos nested directly in the folder.
	Nested []MarkerRepo
	// Reason says why, in a few words: it is printed before init writes.
	Reason string
}

// InferKind reads the case off the folder: one holding repos nested directly in
// it (a child with a .git, or .gitmodules entries) is an orchestrator, one
// holding none is a single-repo project. A single repo that vendors a
// submodule reads wrong: that is what init's flags are for.
func InferKind(dir string) Inference {
	nested := NestedRepos(dir)
	if len(nested) == 0 {
		return Inference{Kind: KindProject, Reason: "it holds no nested repos"}
	}
	names := make([]string, len(nested))
	for i, n := range nested {
		names[i] = n.Path
	}
	return Inference{Kind: KindOrchestrator, Nested: nested, Reason: fmt.Sprintf("it holds %d nested repo(s): %s", len(nested), strings.Join(names, ", "))}
}

// IsRepo reports whether dir is a git repository's top level.
func IsRepo(dir string) bool { return isGitDir(dir) }

// CheckInitDir refuses a folder `jig init` may not run in: init defines a
// project, and runs only at the root of one.
//
//   - A jig.yaml in the folder means it is one already.
//   - A jig.yaml found walking up means the folder is a nested repo of a
//     project; its repos are kept by `jig repos`.
//   - A folder inside a git repository, below its top, is not a root.
//   - A store is not a project.
func CheckInitDir(dir string) error {
	if IsStoreDir(dir) {
		return &axi.Error{
			Msg:  fmt.Sprintf("%s is a jig store: init defines a project, in the repo or the orchestrator the store serves", dir),
			Code: "VALIDATION_ERROR",
		}
	}
	if mdir, _, ok, err := FindMarker(dir); err != nil {
		return err
	} else if ok {
		if isSameDir(mdir, dir) {
			return &axi.Error{
				Msg:  fmt.Sprintf("%s is already initialized: it has a %s", dir, MarkerFile),
				Code: "VALIDATION_ERROR",
				Help: []string{"Run `jig here` to see it, or `jig repos` to see its repos"},
			}
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("%s is inside the jig project at %s: init runs at a project's root, not in a repo nested in one", dir, mdir),
			Code: "VALIDATION_ERROR",
			Help: []string{
				"Add this repo to the project with `jig repos add` from " + mdir,
				"Or place jig in it as a member with `jig attach --tracked --members` from there",
			},
		}
	}
	if top, err := gitx.TopLevel(dir); err == nil && !isSameDir(top, dir) {
		return &axi.Error{
			Msg:  fmt.Sprintf("%s is inside the repository at %s, below its top: init runs at a project's root", dir, top),
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `jig init` in " + top},
		}
	}
	return nil
}

// isSameDir reports whether a and b name one directory, by path or on disk.
func isSameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	return gitx.SameDir(a, b)
}

// InitOpts are what Init makes a project from.
type InitOpts struct {
	// Dir is the project's root, absolute: the single repo, or the
	// orchestrator.
	Dir string
	// JigHome is the jig home root the machine map is under.
	JigHome string
	// Kind is KindProject or KindOrchestrator.
	Kind string
	// Key is the area key the store declares; "" derives it
	// from the project's name.
	Key string
	// Repos are an orchestrator's repos as the person named them: paths under
	// Dir, or URLs to fetch. With none, every repo nested in Dir is taken.
	Repos []string
	// Clone fetches a URL as a gitignored clone instead of a submodule.
	Clone bool
	// MakeRepo allows git init in an orchestrator folder that is not a
	// repository yet.
	MakeRepo bool
}

// InitResult is what Init made.
type InitResult struct {
	Name    string
	Kind    string
	Dir     string
	Key     string
	Store   string
	Marker  string
	Repos   []RepoStatus
	Fetched []string // repos fetched from a URL
	MadeGit bool     // Dir was made a git repository
}

// Init defines the project at o.Dir. It creates the project's store, a git
// repository on main next to the folder (<name>-tickets) with a v3
// store.yaml, commits its scaffold as the person, writes the jig.yaml marker in
// the folder, and records the project in the machine map. Nothing is
// published: the store has no remote and the marker no store: key until
// `jig share`. And nothing is committed in the folder: jig.yaml (and, for an
// orchestrator, .gitmodules) is the person's to commit.
//
// A machine with no git identity is refused before anything is written, the
// way every store commit is. A failure after the store is made takes it away
// again.
func Init(o InitOpts) (InitResult, error) {
	if o.Kind != KindProject && o.Kind != KindOrchestrator {
		return InitResult{}, fmt.Errorf("project: init: unknown kind %q", o.Kind)
	}
	if o.JigHome == "" {
		return InitResult{}, fmt.Errorf("project: no jig home given")
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return InitResult{}, fmt.Errorf("project: resolve %s: %w", o.Dir, err)
	}
	if err := CheckInitDir(dir); err != nil {
		return InitResult{}, err
	}
	name := filepath.Base(dir)
	key, err := initKey(name, o.Key)
	if err != nil {
		return InitResult{}, err
	}
	storeDir := siblingStoreDir(dir)

	machine, err := LoadMachine(o.JigHome)
	if err != nil {
		return InitResult{}, err
	}
	if mp, ok := machine[name]; ok && mp.Store != "" && !isSameDir(mp.Store, storeDir) && IsStoreDir(mp.Store) {
		return InitResult{}, &axi.Error{
			Msg:  fmt.Sprintf("a project named %q is already mapped on this machine, with its store at %s", name, mp.Store),
			Code: "VALIDATION_ERROR",
			Help: []string{"Rename this folder, or remove the other project from the machine map (projects.yaml under the jig home)"},
		}
	}
	if err := checkStoreDirFree(storeDir); err != nil {
		return InitResult{}, err
	}

	var args []RepoArg
	if o.Kind == KindOrchestrator {
		if !isGitDir(dir) && !o.MakeRepo {
			return InitResult{}, &axi.Error{
				Msg:  fmt.Sprintf("%s is not a git repository: an orchestrator is one", dir),
				Code: "VALIDATION_ERROR",
				Help: []string{"Run `jig init --orchestrator --yes` to make it one"},
			}
		}
		if len(o.Repos) == 0 {
			for _, n := range NestedRepos(dir) {
				args = append(args, RepoArg{Repo: n})
			}
		} else if args, err = ResolveRepoArgs(dir, o.Repos, true); err != nil {
			return InitResult{}, err
		}
	} else if len(o.Repos) > 0 {
		return InitResult{}, &axi.Error{
			Msg:  "a single-repo project takes no repos to list",
			Code: "VALIDATION_ERROR",
			Help: []string{"Use --orchestrator to define a project over the repos nested in this folder"},
		}
	}

	res := InitResult{Name: name, Kind: o.Kind, Dir: dir, Key: key, Store: storeDir, Marker: filepath.Join(dir, MarkerFile)}
	madeStore, err := scaffoldStore(storeDir, name, key)
	if err != nil {
		return InitResult{}, err
	}
	undo := func() { clearStore(storeDir, madeStore) }

	marker := Marker{Jig: markerVersion, Project: name, Kind: o.Kind}
	if o.Kind == KindOrchestrator {
		if !isGitDir(dir) {
			if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
				undo()
				return InitResult{}, fmt.Errorf("project: git init %s: %w", dir, err)
			}
			res.MadeGit = true
		}
		for _, a := range args {
			entry := a.Repo
			if a.URL != "" {
				if entry, err = FetchRepo(dir, a, o.Clone); err != nil {
					undo()
					return InitResult{}, err
				}
				res.Fetched = append(res.Fetched, entry.Name)
			}
			marker.Repos = append(marker.Repos, entry)
		}
		res.Repos = Statuses(dir, marker.Repos)
	}
	if err := WriteMarker(dir, marker); err != nil {
		undo()
		return InitResult{}, err
	}
	err = UpdateMachine(o.JigHome, func(m map[string]MachineProject) error {
		clones := map[string]string{}
		if o.Kind == KindProject {
			clones[name] = dir
		}
		for _, r := range res.Repos {
			if r.State == RepoCheckedOut {
				clones[RepoName(r.Repo)] = r.Dir
			}
		}
		m[name] = MachineProject{Store: storeDir, Clones: clones, Root: dir}
		return nil
	})
	if err != nil {
		_ = os.Remove(res.Marker)
		undo()
		return InitResult{}, err
	}
	return res, nil
}

// initKey is the area key the new store declares: key when given (checked), else
// the one derived from the project's name.
func initKey(name, key string) (string, error) {
	if key == "" {
		return deriveProjectKey(name)
	}
	if !keyRE.MatchString(key) {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("--key %q is not 2 to 10 uppercase letters or digits starting with a letter", key),
			Code: "VALIDATION_ERROR",
		}
	}
	return key, nil
}

// checkStoreDirFree refuses a place for the store that already holds something:
// a store of an earlier init, or any other folder.
func checkStoreDirFree(storeDir string) error {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("project: read %s: %w", storeDir, err)
	}
	if IsStoreDir(storeDir) {
		return &axi.Error{
			Msg:  fmt.Sprintf("store already initialized at %s", storeDir),
			Code: "VALIDATION_ERROR",
			Help: []string{"Commit its project's jig.yaml, or move the store away; `jig ticket new --title \"...\"` uses an existing store"},
		}
	}
	if len(entries) > 0 {
		return &axi.Error{
			Msg:  fmt.Sprintf("%s exists and is not empty: the project's store goes there", storeDir),
			Code: "VALIDATION_ERROR",
			Help: []string{"Move it away, or rename this folder"},
		}
	}
	return nil
}

// clearStore takes back a store scaffoldStore made: the directory, when it made
// it, else (it was there and empty, checkStoreDirFree) what it put in it.
func clearStore(storeDir string, madeDir bool) {
	if madeDir {
		_ = os.RemoveAll(storeDir)
		return
	}
	if entries, err := os.ReadDir(storeDir); err == nil {
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(storeDir, e.Name()))
		}
	}
}

// scaffoldStore creates the store at storeDir: a fresh git repository on main
// holding a v3 store.yaml, an empty platform/ and ledger.md, and the ignore
// and attribute files every store has, committed as the person. The person's
// identity is resolved in the new store before anything is written, so a
// machine with none is refused with nothing left behind: the directory this
// call made is taken away again, never one that existed. It reports whether it
// made the directory.
func scaffoldStore(storeDir, name, key string) (madeDir bool, err error) {
	_, statErr := os.Stat(storeDir)
	madeDir = os.IsNotExist(statErr)
	fail := func(err error) (bool, error) {
		clearStore(storeDir, madeDir)
		return false, err
	}
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return false, fmt.Errorf("project: create store dir: %w", err)
	}
	if _, err := gitx.Run(storeDir, "init", "-b", "main"); err != nil {
		return fail(fmt.Errorf("project: git init: %w", err))
	}
	id, err := gitx.ResolveIdentity(storeDir)
	if err != nil {
		return fail(err)
	}
	storeYAML, err := NewStoreYAML(name, key)
	if err != nil {
		return fail(err)
	}
	files := []struct {
		name string
		data []byte
	}{
		{StoreFile, storeYAML},
		{"ledger.md", nil},
		// store.Lock's sidecar "*.lock" files (never removed) and
		// store.AtomicWrite's ".*.tmp" scratch files are noise the truth repo
		// must never track: ignore them at the store root so locked writes
		// never dirty git status or collide with another clone's lock files.
		{".gitignore", []byte("*.lock\n.*.tmp\n")},
		// No conversion of any file in the store: its files are jig's data,
		// and it lets gitx work on the store in process (gitx.Repo).
		{".gitattributes", []byte(gitx.StoreAttributes)},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(storeDir, f.name), f.data, 0o644); err != nil {
			return fail(fmt.Errorf("project: write %s: %w", f.name, err))
		}
	}
	if err := os.MkdirAll(filepath.Join(storeDir, "platform"), 0o755); err != nil {
		return fail(fmt.Errorf("project: create platform dir: %w", err))
	}
	// The scaffold is committed by path, as every later claim is, rather than
	// by a sweeping `add -A`.
	if _, err := gitx.Run(storeDir, "add", "-A", "--", StoreFile, "platform", "ledger.md", ".gitignore", ".gitattributes"); err != nil {
		return fail(fmt.Errorf("project: stage store scaffold: %w", err))
	}
	if staged, err := gitx.Run(storeDir, "diff", "--cached", "--name-only"); err != nil {
		return fail(fmt.Errorf("project: check staged store scaffold: %w", err))
	} else if staged != "" {
		if _, err := gitx.Run(storeDir, "-c", "user.name="+id.Name, "-c", "user.email="+id.Email, "commit", "-m", "jig: init store"); err != nil {
			return fail(fmt.Errorf("project: commit store scaffold: %w", err))
		}
	}
	return madeDir, nil
}
