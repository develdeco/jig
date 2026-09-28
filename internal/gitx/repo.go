package gitx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrUseCLI is what a Repo method returns when it cannot do its job the way
// the git program would - the repository declares something go-git does not
// honor, a remote is not a bare repository at a local path, a history has a
// merge, a git process holds a lock - so the caller runs its git program
// path instead.
var ErrUseCLI = errors.New("gitx: in-process git does not cover this; use the git program")

// ErrPushRejected is Repo.Push's error when the remote branch moved on and
// the push is not a fast-forward.
var ErrPushRejected = errors.New("gitx: push rejected: not a fast-forward")

// Repo is a repository gitx works on in this process, through go-git,
// rather than by starting the git program for every step. It serves jig's
// own store: a small repository jig alone writes, where a status, an add,
// a commit and a push to a local remote cost a few milliseconds in process
// and tens (a hundred and more on Windows) as git processes. Users'
// repositories stay on the git program (Run), whose hooks, filters and
// credential helpers they rely on. Every method opens the repository
// afresh, so a git program run in between (a pack it wrote, a ref it
// packed) is never missed.
type Repo struct {
	dir string
}

// OpenRepo opens the repository whose top-level directory is dir, or
// returns ErrUseCLI when the repository declares something go-git would not
// honor (see needsGitProgram and convertsNothing) or go-git cannot open it,
// leaving the git program to handle it or report what is wrong.
func OpenRepo(dir string) (*Repo, error) {
	r := &Repo{dir: dir}
	gitDir := r.gitDir()
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return nil, ErrUseCLI // a linked worktree or submodule (.git is a file), or no repository
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, ErrUseCLI
	}
	cfg, err := repo.Config()
	if err != nil {
		return nil, ErrUseCLI
	}
	if needsGitProgram(gitDir, cfg) || !convertsNothing(dir, gitDir, cfg) {
		return nil, ErrUseCLI
	}
	return r, nil
}

// open opens the repository for one method call.
func (r *Repo) open() (*git.Repository, error) {
	repo, err := git.PlainOpen(r.dir)
	if err != nil {
		return nil, fmt.Errorf("gitx: open %s: %w", r.dir, err)
	}
	return repo, nil
}

func (r *Repo) gitDir() string { return filepath.Join(r.dir, ".git") }

// StoreAttributes is the .gitattributes jig writes at a store's root: no
// line-ending conversion for any file, so the git program and go-git,
// which converts nothing, store and check out the same bytes.
const StoreAttributes = "* -text\n"

// convertsNothing reports whether the repository itself rules out
// line-ending conversion, whatever the machine's own config says (Git for
// Windows installs with core.autocrlf=true, so a CRLF working file would
// otherwise be committed as is here and normalized by the git program):
// its root .gitattributes is exactly StoreAttributes, or it has no
// attributes file at all and its own config sets core.autocrlf=false. A
// repository that says nothing, or declares any other attribute (a text
// rule, a filter such as Git LFS), stays with the git program.
func convertsNothing(dir, gitDir string, cfg *config.Config) bool {
	if exists(filepath.Join(gitDir, "info", "attributes")) {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	if err == nil {
		return strings.ReplaceAll(string(data), "\r\n", "\n") == StoreAttributes
	}
	if !os.IsNotExist(err) {
		return false
	}
	return cfg.Raw.Section("core").Option("autocrlf") == "false"
}

// needsGitProgram reports whether the repository at gitDir declares, in its
// own files and config, something go-git does not do the way the git
// program would: hooks of its own (a hooks directory with anything but
// git's samples, or core.hooksPath), a shallow or grafted history,
// alternates, a repository format extension (SHA-256 objects, reftable
// refs), or group-shared permissions. The machine's own git config is never
// read, so a repository is handled the same way on every host.
func needsGitProgram(gitDir string, cfg *config.Config) bool {
	for _, name := range []string{"shallow", "info/grafts", "objects/info/alternates"} {
		if exists(filepath.Join(gitDir, filepath.FromSlash(name))) {
			return true
		}
	}
	core := cfg.Raw.Section("core")
	if v := core.Option("repositoryformatversion"); v != "" && v != "0" && v != "1" {
		return true
	}
	if len(cfg.Raw.Section("extensions").Options) > 0 {
		return true
	}
	if core.Option("hooksPath") != "" {
		return true
	}
	switch strings.ToLower(core.Option("sharedRepository")) {
	case "", "false", "umask", "0":
	default:
		return true
	}
	hooks, err := os.ReadDir(filepath.Join(gitDir, "hooks"))
	if err != nil && !os.IsNotExist(err) {
		return true
	}
	for _, h := range hooks {
		if !h.IsDir() && !strings.HasSuffix(h.Name(), ".sample") {
			return true
		}
	}
	return false
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// GitPath returns the absolute path of name inside the repository's .git
// directory, as `git rev-parse --git-path` would for a repository that is
// not a linked worktree (OpenRepo refuses those).
func (r *Repo) GitPath(name string) string {
	return filepath.Join(r.gitDir(), filepath.FromSlash(name))
}

// RepoState is what one look at a repository reports before anything is
// staged.
type RepoState struct {
	Branch   string // the checked-out branch; "" when HEAD is detached
	Unmerged bool   // the index has conflicted (unmerged) entries
	Dirty    bool   // anything tracked, staged or untracked differs from HEAD
}

// State reads the branch, unmerged entries and changes, without writing
// anything (a git status refreshes and rewrites the index unless told not
// to; go-git does not).
func (r *Repo) State() (RepoState, error) {
	var st RepoState
	repo, err := r.open()
	if err != nil {
		return st, err
	}
	head, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return st, fmt.Errorf("gitx: read HEAD in %s: %w", r.dir, err)
	}
	if head.Type() == plumbing.SymbolicReference && head.Target().IsBranch() {
		st.Branch = head.Target().Short()
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return st, fmt.Errorf("gitx: read index in %s: %w", r.dir, err)
	}
	for _, e := range idx.Entries {
		// Stage 0 is a merged entry; 1-3 are a conflict's base, ours and
		// theirs. (go-git's index.Merged constant is 1, the same as
		// AncestorMode, so it is not the value to compare with.)
		if e.Stage != 0 {
			st.Unmerged = true
			break
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return st, err
	}
	status, err := wt.Status()
	if err != nil {
		return st, fmt.Errorf("gitx: status in %s: %w", r.dir, err)
	}
	st.Dirty = st.Unmerged || !status.IsClean()
	return st, nil
}

// HasRemote reports whether the repository has a remote named name.
func (r *Repo) HasRemote(name string) bool {
	repo, err := r.open()
	if err != nil {
		return false
	}
	_, err = repo.Remote(name)
	return err == nil
}

// CommitAll stages every change (`git add -A`) and commits it with msg to
// the checked-out branch, reporting whether there was anything to commit.
// The author and committer are name and email, unless GIT_AUTHOR_* or
// GIT_COMMITTER_* set them, as they do for the git program. It holds the
// index's and the branch's lock files, as the git program does, so a git
// process cannot write either meanwhile. It returns ErrUseCLI, before
// anything is staged, when a git process holds one of those locks, when
// HEAD is not on a branch, when a pinned date does not parse, or when the
// index tracks anything but regular files (an executable, a symbolic link
// or a submodule, which the git program's core.fileMode and core.symlinks
// decide how to stage).
func (r *Repo) CommitAll(msg, name, email string) (bool, error) {
	author, err := signature("AUTHOR", name, email)
	if err != nil {
		return false, err
	}
	committer, err := signature("COMMITTER", name, email)
	if err != nil {
		return false, err
	}
	repo, err := r.open()
	if err != nil {
		return false, err
	}
	head, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return false, fmt.Errorf("gitx: read HEAD in %s: %w", r.dir, err)
	}
	if head.Type() != plumbing.SymbolicReference || !head.Target().IsBranch() {
		return false, ErrUseCLI
	}
	unlock, err := lockFiles(filepath.Join(r.gitDir(), "index"), refPath(r.gitDir(), head.Target()))
	if err != nil {
		return false, err
	}
	defer unlock()
	idx, err := repo.Storer.Index()
	if err != nil {
		return false, fmt.Errorf("gitx: read index in %s: %w", r.dir, err)
	}
	for _, e := range idx.Entries {
		if e.Mode != filemode.Regular {
			return false, ErrUseCLI
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return false, err
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return false, fmt.Errorf("gitx: add -A in %s: %w", r.dir, err)
	}
	_, err = wt.Commit(strings.TrimRight(msg, "\n")+"\n", &git.CommitOptions{Author: author, Committer: committer})
	if errors.Is(err, git.ErrEmptyCommit) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("gitx: commit in %s: %w", r.dir, err)
	}
	return true, nil
}

// signature is the git program's identity for role ("AUTHOR" or
// "COMMITTER"): GIT_<role>_NAME, _EMAIL and _DATE when set, else name,
// email and now.
func signature(role, name, email string) (*object.Signature, error) {
	if v := os.Getenv("GIT_" + role + "_NAME"); v != "" {
		name = v
	}
	if v := os.Getenv("GIT_" + role + "_EMAIL"); v != "" {
		email = v
	}
	when := time.Now()
	if v := os.Getenv("GIT_" + role + "_DATE"); v != "" {
		t, ok := parseGitDate(v)
		if !ok {
			return nil, ErrUseCLI
		}
		when = t
	}
	return &object.Signature{Name: name, Email: email, When: when}, nil
}

// parseGitDate parses the date forms jig and its tests pin: RFC 3339, and
// git's internal "<unix seconds> <+hhmm>" (with or without a leading @).
func parseGitDate(v string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	secs, zone, ok := strings.Cut(strings.TrimPrefix(v, "@"), " ")
	if !ok || len(zone) != 5 || (zone[0] != '+' && zone[0] != '-') {
		return time.Time{}, false
	}
	s, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	hh, err1 := strconv.Atoi(zone[1:3])
	mm, err2 := strconv.Atoi(zone[3:5])
	if err1 != nil || err2 != nil {
		return time.Time{}, false
	}
	off := (hh*60 + mm) * 60
	if zone[0] == '-' {
		off = -off
	}
	return time.Unix(s, 0).In(time.FixedZone("", off)), true
}

// lockFiles takes the git program's lock file (<path>.lock, created only if
// absent) for each path, in order, and returns the function that releases
// them. A lock another process holds returns ErrUseCLI, having taken none,
// so the git program runs that step and reports the lock as it always does.
func lockFiles(paths ...string) (func(), error) {
	var held []string
	release := func() {
		for _, p := range held {
			os.Remove(p)
		}
	}
	for _, p := range paths {
		lock := p + ".lock"
		if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
			release()
			return nil, err
		}
		f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err != nil {
			release()
			if errors.Is(err, fs.ErrExist) {
				return nil, ErrUseCLI
			}
			return nil, err
		}
		f.Close()
		held = append(held, lock)
	}
	return release, nil
}

// refPath is the loose-ref file for name in gitDir.
func refPath(gitDir string, name plumbing.ReferenceName) string {
	return filepath.Join(gitDir, filepath.FromSlash(name.String()))
}

// errRefMoved is writeRef's error when the ref no longer holds the value
// the caller read.
var errRefMoved = errors.New("gitx: ref moved")

// writeRef sets ref name in the repository at gitDir to to, the way the git
// program updates a ref: it takes the ref's lock file, which a git process
// updating the same ref also takes, checks the ref still holds from (when
// from is not nil; ZeroHash for a ref that must not exist yet), writes the
// new value into the lock file and renames it over the ref. It returns
// ErrUseCLI when another process holds the lock, and errRefMoved when the
// ref no longer holds from.
func writeRef(repo *git.Repository, gitDir string, name plumbing.ReferenceName, to plumbing.Hash, from *plumbing.Hash) error {
	path := refPath(gitDir, name)
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if errors.Is(err, fs.ErrExist) {
		return ErrUseCLI
	}
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			f.Close()
			os.Remove(lock)
		}
	}()
	if from != nil {
		cur := plumbing.ZeroHash
		ref, err := repo.Storer.Reference(name)
		switch {
		case err == nil && ref.Type() == plumbing.HashReference:
			cur = ref.Hash()
		case err == nil:
			return ErrUseCLI // a symbolic ref: the git program's to update
		case !errors.Is(err, plumbing.ErrReferenceNotFound):
			return err
		}
		if cur != *from {
			return errRefMoved
		}
	}
	if _, err := f.WriteString(to.String() + "\n"); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := renameOver(lock, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

// renameOver renames src over dst. On Windows a rename fails while another
// process has dst open without sharing deletion (a reader of the ref, a
// scanner), so it is retried for a moment, as Git for Windows does.
func renameOver(src, dst string) error {
	err := os.Rename(src, dst)
	for i := 1; err != nil && runtime.GOOS == "windows" && i <= 10; i++ {
		time.Sleep(time.Duration(i) * 5 * time.Millisecond)
		err = os.Rename(src, dst)
	}
	return err
}

// localRemote opens the remote named name as a bare repository at a local
// path, with its directory, or returns ErrUseCLI: a network remote keeps
// the git program for the user's credential helpers and SSH configuration,
// a non-bare one because only the git program's receive-pack knows to
// protect its checked-out branch, and so does any remote whose config
// (a push URL, a custom receive-pack, URL rewriting, a fetch refspec other
// than the default) or whose own repository (see needsGitProgram) asks for
// what go-git does not do.
func localRemote(repo *git.Repository, dir, name string) (*git.Repository, string, error) {
	cfg, err := repo.Config()
	if err != nil {
		return nil, "", fmt.Errorf("gitx: read config in %s: %w", dir, err)
	}
	rc, ok := cfg.Remotes[name]
	if !ok {
		return nil, "", fmt.Errorf("gitx: no remote %s in %s", name, dir)
	}
	raw := cfg.Raw.Section("remote").Subsection(name)
	for _, key := range []string{"pushurl", "receivepack", "uploadpack", "mirror", "vcs"} {
		if raw.Option(key) != "" {
			return nil, "", ErrUseCLI
		}
	}
	if len(cfg.Raw.Section("url").Subsections) > 0 {
		return nil, "", ErrUseCLI
	}
	defaultFetch := config.RefSpec("+refs/heads/*:refs/remotes/" + name + "/*")
	if len(rc.Fetch) != 1 || rc.Fetch[0] != defaultFetch {
		return nil, "", ErrUseCLI
	}
	if len(rc.URLs) != 1 || strings.Contains(rc.URLs[0], "://") {
		return nil, "", ErrUseCLI
	}
	path := rc.URLs[0]
	if !isAbsPath(path) {
		path = filepath.Join(dir, path)
	}
	if !isBareRepo(path) {
		return nil, "", ErrUseCLI
	}
	rem, err := git.PlainOpen(path)
	if err != nil {
		return nil, "", ErrUseCLI
	}
	remCfg, err := rem.Config()
	if err != nil || needsGitProgram(path, remCfg) || checksTransfers(cfg) || checksTransfers(remCfg) {
		return nil, "", ErrUseCLI
	}
	return rem, path, nil
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

// isTrue reports whether key is set in s and not to one of git's false
// values; a key with no value at all is true, as it is for git.
func isTrue(s *format.Section, key string) bool {
	return s.HasOption(key) && !isFalse(s.Option(key))
}

// isFalse reports whether v is one of git's false values.
func isFalse(v string) bool {
	switch strings.ToLower(v) {
	case "false", "no", "off", "0":
		return true
	}
	return false
}

// isBareRepo reports whether dir holds a bare repository: git's own files
// (config, HEAD, objects) at its top, not under a .git directory.
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
		tip  plumbing.Hash
		at   plumbing.Hash // ZeroHash once the walk passed a root commit
		path []plumbing.Hash
		seen map[plumbing.Hash]bool
	}
	wa := &walk{repo: ra, tip: a, at: a, path: []plumbing.Hash{a}, seen: map[plumbing.Hash]bool{a: true}}
	wb := &walk{repo: rb, tip: b, at: b, path: []plumbing.Hash{b}, seen: map[plumbing.Hash]bool{b: true}}
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
func (r *Repo) Push(remote, branch string) error {
	repo, err := r.open()
	if err != nil {
		return err
	}
	rem, remDir, err := localRemote(repo, r.dir, remote)
	if err != nil {
		return err
	}
	name := plumbing.NewBranchReferenceName(branch)
	ours, err := repo.Reference(name, true)
	if err != nil {
		return fmt.Errorf("gitx: push %s from %s: %w", branch, r.dir, err)
	}
	theirs, err := rem.Reference(name, true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return ErrUseCLI
	}
	if err != nil {
		return fmt.Errorf("gitx: read %s's %s: %w", remote, branch, err)
	}
	rel, commits, err := relate(repo, ours.Hash(), rem, theirs.Hash())
	if err != nil {
		return err
	}
	switch rel {
	case Behind, Diverged:
		return ErrPushRejected
	case Ahead:
		if err := copyCommits(repo, rem, commits); err != nil {
			return fmt.Errorf("gitx: push %s %s from %s: %w", remote, branch, r.dir, err)
		}
		from := theirs.Hash()
		err := writeRef(rem, remDir, name, ours.Hash(), &from)
		if errors.Is(err, errRefMoved) {
			return ErrPushRejected
		}
		if err != nil {
			return err
		}
		if autoGCOnReceive(rem, remDir) {
			// Best effort, as the store's own maintenance: a failure here
			// must not fail a push that already landed.
			_ = MaintenanceAuto(remDir)
		}
	}
	return writeRef(repo, r.gitDir(), plumbing.NewRemoteReferenceName(remote, branch), ours.Hash(), nil)
}

// Fetch brings branch from remote, a bare repository at a local path
// (otherwise ErrUseCLI), into its remote-tracking branch, and reports how
// the local branch stands against it. It changes neither the local branch
// nor the working tree: bringing a Behind branch forward is the git
// program's checkout (`git merge --ff-only`), which keeps untracked and
// ignored files and refuses to overwrite them. A Diverged branch is left to
// the git program's rebase, which fetches for itself, so nothing is copied
// and the remote-tracking branch does not move.
func (r *Repo) Fetch(remote, branch string) (Relation, error) {
	repo, err := r.open()
	if err != nil {
		return 0, err
	}
	rem, _, err := localRemote(repo, r.dir, remote)
	if err != nil {
		return 0, err
	}
	name := plumbing.NewBranchReferenceName(branch)
	theirs, err := rem.Reference(name, true)
	if err != nil {
		return 0, ErrUseCLI // no such branch there: the git program reports it
	}
	ours, err := repo.Reference(name, true)
	if err != nil {
		return 0, ErrUseCLI // an unborn branch: the git program sets it up
	}
	rel, commits, err := relate(repo, ours.Hash(), rem, theirs.Hash())
	if err != nil {
		return 0, err
	}
	switch rel {
	case Diverged:
		return Diverged, nil
	case Behind:
		if err := copyCommits(rem, repo, commits); err != nil {
			return 0, fmt.Errorf("gitx: fetch %s %s into %s: %w", remote, branch, r.dir, err)
		}
	}
	if err := writeRef(repo, r.gitDir(), plumbing.NewRemoteReferenceName(remote, branch), theirs.Hash(), nil); err != nil {
		return 0, err
	}
	return rel, nil
}

// LooseObjectsPastAutoGC reports whether the repository's loose objects may
// have passed git's gc.auto limit (see loosePastAutoGC). A commit or fetch
// through Repo only adds loose objects, so below it `git maintenance run
// --auto` would start only to find nothing to do.
func (r *Repo) LooseObjectsPastAutoGC() bool {
	repo, err := r.open()
	if err != nil {
		return true // let git decide
	}
	return loosePastAutoGC(repo, r.gitDir())
}

// autoGCOnReceive reports whether the git program's receive-pack would have
// started automatic maintenance in the bare repository at gitDir after this
// push: receive.autoGC is not turned off, and its loose objects may have
// passed gc.auto.
func autoGCOnReceive(repo *git.Repository, gitDir string) bool {
	if cfg, err := repo.Config(); err == nil {
		if s := cfg.Raw.Section("receive"); s.HasOption("autoGC") && isFalse(s.Option("autoGC")) {
			return false
		}
	}
	return loosePastAutoGC(repo, gitDir)
}

// loosePastAutoGC reports whether the loose objects of the repository at
// gitDir may have passed git's gc.auto limit (6700, unless the repository's
// own config sets another; 0 turns automatic gc off), estimated the way git
// estimates it: the loose objects in objects/17, one of the 256 object
// directories, above the limit divided by 256.
func loosePastAutoGC(repo *git.Repository, gitDir string) bool {
	limit := 6700
	if cfg, err := repo.Config(); err == nil {
		if v := cfg.Raw.Section("gc").Option("auto"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return true // a value go-git cannot read: let git decide
			}
			limit = n
		}
	}
	if limit <= 0 {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(gitDir, "objects", "17"))
	if err != nil {
		return false
	}
	loose := 0
	for _, e := range entries {
		if len(e.Name()) == 38 && isHex(e.Name()) {
			loose++
		}
	}
	return loose > (limit+255)/256
}

// isHex reports whether s is lowercase hexadecimal.
func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
