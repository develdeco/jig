package gitx

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// remote is a bare repository at a local path that Push and Fetch work on
// in process; done closes it, and may be called more than once.
type remote struct {
	repo *git.Repository
	dir  string
	done func()
}

// localRemote opens the remote named name as a bare repository at a local
// path, or returns ErrUseCLI: a network remote keeps the git program for
// the user's credential helpers and SSH configuration, a non-bare one
// because only the git program's receive-pack knows to protect its
// checked-out branch, and so does any remote whose config (a push URL, a
// custom receive-pack, URL rewriting, a fetch refspec other than the
// default, objects checked on transfer) or whose own repository (see
// needsGitProgram) asks for what gitx does not do in process.
func localRemote(repo *git.Repository, dir, name string) (*remote, error) {
	cfg, err := repo.Config()
	if err != nil {
		return nil, fmt.Errorf("gitx: read config in %s: %w", dir, err)
	}
	rc, ok := cfg.Remotes[name]
	if !ok {
		return nil, fmt.Errorf("gitx: no remote %s in %s", name, dir)
	}
	raw := cfg.Raw.Section("remote").Subsection(name)
	for _, key := range []string{"pushurl", "receivepack", "uploadpack", "mirror", "vcs"} {
		if raw.Option(key) != "" {
			return nil, ErrUseCLI
		}
	}
	if len(cfg.Raw.Section("url").Subsections) > 0 || checksTransfers(cfg) {
		return nil, ErrUseCLI
	}
	defaultFetch := config.RefSpec("+refs/heads/*:refs/remotes/" + name + "/*")
	if len(rc.Fetch) != 1 || rc.Fetch[0] != defaultFetch {
		return nil, ErrUseCLI
	}
	if len(rc.URLs) != 1 || strings.Contains(rc.URLs[0], "://") {
		return nil, ErrUseCLI
	}
	path := rc.URLs[0]
	if !isAbsPath(path) {
		path = filepath.Join(dir, path)
	}
	if !isBareRepo(path) {
		return nil, ErrUseCLI
	}
	rem, done, err := openStorage(path)
	if err != nil {
		return nil, ErrUseCLI
	}
	remCfg, err := rem.Config()
	if err != nil || !isTrue(remCfg.Raw.Section("core"), "bare") || needsGitProgram(path, remCfg) || checksTransfers(remCfg) {
		done()
		return nil, ErrUseCLI
	}
	var once sync.Once
	return &remote{repo: rem, dir: path, done: func() { once.Do(done) }}, nil
}

// checksTransfers reports whether cfg asks the git program to check the
// objects a fetch or push moves (transfer, fetch or receive .fsckObjects),
// which an in-process copy does not do.
func checksTransfers(cfg *config.Config) bool {
	for _, section := range []string{"transfer", "fetch", "receive"} {
		if isTrue(cfg.Raw.Section(section), "fsckObjects") {
			return true
		}
	}
	return false
}

// isBareRepo reports whether dir holds a repository's own files (config,
// HEAD, objects) at its top, as a bare repository and a work tree's .git
// directory do; localRemote then requires core.bare.
func isBareRepo(dir string) bool {
	for _, name := range []string{"config", "HEAD", "objects"} {
		if !exists(filepath.Join(dir, name)) {
			return false
		}
	}
	return true
}

// Relation is how a local branch stands against the same branch on a
// remote.
type Relation int

const (
	Same     Relation = iota // both at the same commit
	Ahead                    // the remote's commit is in the local branch's history
	Behind                   // the local commit is in the remote branch's history
	Diverged                 // neither is in the other's history
)

// relate reports how commit a, in repository ra, stands against commit b,
// in rb, and the commits the one ahead has that the other lacks, newest
// first. It walks both histories back one commit at a time, in turn, until
// one walk reaches the other's tip (Ahead or Behind) or a commit the other
// walk already passed (Diverged), so its cost follows the commits since the
// two last met, not the length of the history. A merge commit or a missing
// commit on either walk, or two histories that never meet, return
// ErrUseCLI: jig's store history is linear, since it only fast-forwards and
// rebases, and the git program handles the rest.
func relate(ra *git.Repository, a plumbing.Hash, rb *git.Repository, b plumbing.Hash) (Relation, []plumbing.Hash, error) {
	if a == b {
		return Same, nil, nil
	}
	type walk struct {
		repo *git.Repository
		at   plumbing.Hash // ZeroHash once the walk passed a root commit
		path []plumbing.Hash
		seen map[plumbing.Hash]bool
	}
	wa := &walk{repo: ra, at: a, path: []plumbing.Hash{a}, seen: map[plumbing.Hash]bool{a: true}}
	wb := &walk{repo: rb, at: b, path: []plumbing.Hash{b}, seen: map[plumbing.Hash]bool{b: true}}
	// step moves w one commit back and reports whether it met other.
	step := func(w, other *walk) (bool, error) {
		if w.at.IsZero() {
			return false, nil
		}
		c, err := w.repo.CommitObject(w.at)
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return false, ErrUseCLI
		}
		if err != nil {
			return false, err
		}
		switch len(c.ParentHashes) {
		case 0:
			w.at = plumbing.ZeroHash
			return false, nil
		case 1:
			w.at = c.ParentHashes[0]
		default:
			return false, ErrUseCLI
		}
		if other.seen[w.at] {
			return true, nil
		}
		w.seen[w.at] = true
		w.path = append(w.path, w.at)
		return false, nil
	}
	for !wa.at.IsZero() || !wb.at.IsZero() {
		met, err := step(wa, wb)
		if err != nil {
			return 0, nil, err
		}
		if met {
			if wa.at == b {
				return Ahead, wa.path, nil
			}
			return Diverged, nil, nil
		}
		if met, err = step(wb, wa); err != nil {
			return 0, nil, err
		}
		if met {
			if wb.at == a {
				return Behind, wb.path, nil
			}
			return Diverged, nil, nil
		}
	}
	return 0, nil, ErrUseCLI
}

// copyCommits copies commits from src into dst: commits is a linear run,
// newest first, each commit's only parent the next one, and the last one's
// parent a commit dst already has with everything it references (a branch
// tip). Each commit brings the objects its tree has that its parent's tree
// does not. Objects are written oldest commit first, and every object after
// the objects it references, so a copy cut short never leaves dst an object
// whose references are missing.
func copyCommits(src, dst *git.Repository, commits []plumbing.Hash) error {
	for i := len(commits) - 1; i >= 0; i-- {
		c, err := src.CommitObject(commits[i])
		if err != nil {
			return err
		}
		parentTree := plumbing.ZeroHash
		if len(c.ParentHashes) == 1 {
			p, err := src.CommitObject(c.ParentHashes[0])
			if err != nil {
				return err
			}
			parentTree = p.TreeHash
		}
		if err := copyTree(src, dst, c.TreeHash, parentTree); err != nil {
			return err
		}
		if err := copyObject(src, dst, c.Hash); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies tree h from src into dst, with every entry that differs
// from known, the tree at the same path in the parent commit (ZeroHash for
// none), which dst already has with everything in it.
func copyTree(src, dst *git.Repository, h, known plumbing.Hash) error {
	if h == known {
		return nil
	}
	t, err := src.TreeObject(h)
	if err != nil {
		return err
	}
	old := map[string]object.TreeEntry{}
	if !known.IsZero() {
		kt, err := src.TreeObject(known)
		if err != nil {
			return err
		}
		for _, e := range kt.Entries {
			old[e.Name] = e
		}
	}
	for _, e := range t.Entries {
		k, had := old[e.Name]
		if had && k.Hash == e.Hash {
			continue
		}
		switch e.Mode {
		case filemode.Dir:
			kh := plumbing.ZeroHash
			if had && k.Mode == filemode.Dir {
				kh = k.Hash
			}
			if err := copyTree(src, dst, e.Hash, kh); err != nil {
				return err
			}
		case filemode.Submodule:
			// A commit in another repository: nothing here to copy.
		default:
			if err := copyObject(src, dst, e.Hash); err != nil {
				return err
			}
		}
	}
	return copyObject(src, dst, h)
}

// copyObject copies object h from src into dst unless dst has it.
func copyObject(src, dst *git.Repository, h plumbing.Hash) error {
	if dst.Storer.HasEncodedObject(h) == nil {
		return nil
	}
	obj, err := src.Storer.EncodedObject(plumbing.AnyObject, h)
	if err != nil {
		return err
	}
	_, err = dst.Storer.SetEncodedObject(obj)
	return err
}

// Push pushes branch to the same branch on remote, a bare repository at a
// local path (otherwise ErrUseCLI), as `git push remote branch` would when
// it fast-forwards: the commits the remote lacks are copied into it, its
// branch moves under the git program's own ref lock and only from the
// commit this push started from, the remote gets the automatic maintenance
// its receive-pack would start, and the local remote-tracking branch
// follows. When the remote's branch is not in this branch's history, or
// moved while the push ran, it returns ErrPushRejected and the remote's
// branch does not change. A branch the remote lacks, a merge in either
// history or a lock another process holds return ErrUseCLI.
func (r *Repo) Push(remoteName, branch string) error {
	repo, done, err := r.open()
	if err != nil {
		return err
	}
	defer done()
	rem, err := localRemote(repo, r.dir, remoteName)
	if err != nil {
		return err
	}
	defer rem.done()
	name := plumbing.NewBranchReferenceName(branch)
	ours, err := repo.Reference(name, true)
	if err != nil {
		return fmt.Errorf("gitx: push %s from %s: %w", branch, r.dir, err)
	}
	theirs, err := rem.repo.Reference(name, true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return ErrUseCLI
	}
	if err != nil {
		return fmt.Errorf("gitx: read %s's %s: %w", remoteName, branch, err)
	}
	rel, commits, err := relate(repo, ours.Hash(), rem.repo, theirs.Hash())
	if err != nil {
		return err
	}
	switch rel {
	case Behind, Diverged:
		return ErrPushRejected
	case Ahead:
		if err := copyCommits(repo, rem.repo, commits); err != nil {
			return fmt.Errorf("gitx: push %s %s from %s: %w", remoteName, branch, r.dir, err)
		}
		from := theirs.Hash()
		err := writeRef(rem.repo, rem.dir, name, ours.Hash(), &from)
		if errors.Is(err, errRefMoved) {
			return ErrPushRejected
		}
		if err != nil {
			return err
		}
		if autoGCOnReceive(rem.repo, rem.dir) {
			// Closed first, so no pack this process holds open keeps the
			// repack from removing it. Best effort, as the store's own
			// maintenance: a failure here must not fail a push that landed.
			rem.done()
			_ = MaintenanceAuto(rem.dir)
		}
	}
	return writeRef(repo, r.gitDir(), plumbing.NewRemoteReferenceName(remoteName, branch), ours.Hash(), nil)
}

// Fetch brings branch from remote, a bare repository at a local path
// (otherwise ErrUseCLI), into its remote-tracking branch, and reports how
// the local branch stands against it. It changes neither the local branch
// nor the working tree: bringing a Behind branch forward is the git
// program's checkout (`git merge --ff-only`), which keeps untracked and
// ignored files and refuses to overwrite them. A Diverged branch is left to
// the git program's rebase, which fetches for itself, so nothing is copied
// and the remote-tracking branch does not move.
func (r *Repo) Fetch(remoteName, branch string) (Relation, error) {
	repo, done, err := r.open()
	if err != nil {
		return 0, err
	}
	defer done()
	rem, err := localRemote(repo, r.dir, remoteName)
	if err != nil {
		return 0, err
	}
	defer rem.done()
	name := plumbing.NewBranchReferenceName(branch)
	theirs, err := rem.repo.Reference(name, true)
	if err != nil {
		return 0, ErrUseCLI // no such branch there: the git program reports it
	}
	ours, err := repo.Reference(name, true)
	if err != nil {
		return 0, ErrUseCLI // an unborn branch: the git program sets it up
	}
	rel, commits, err := relate(repo, ours.Hash(), rem.repo, theirs.Hash())
	if err != nil {
		return 0, err
	}
	switch rel {
	case Diverged:
		return Diverged, nil
	case Behind:
		if err := copyCommits(rem.repo, repo, commits); err != nil {
			return 0, fmt.Errorf("gitx: fetch %s %s into %s: %w", remoteName, branch, r.dir, err)
		}
	}
	if err := writeRef(repo, r.gitDir(), plumbing.NewRemoteReferenceName(remoteName, branch), theirs.Hash(), nil); err != nil {
		return 0, err
	}
	return rel, nil
}

// autoGCOnReceive reports whether the git program's receive-pack would have
// started automatic maintenance in the bare repository at gitDir after this
// push: its receive.autoGC is not turned off, and it may have passed a gc
// limit (see pastAutoGC).
func autoGCOnReceive(repo *git.Repository, gitDir string) bool {
	if cfg, err := repo.Config(); err == nil {
		if s := cfg.Raw.Section("receive"); s.HasOption("autoGC") && isFalse(s.Option("autoGC")) {
			return false
		}
	}
	return pastAutoGC(repo, gitDir)
}
