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
	"github.com/go-git/go-git/v5/plumbing/cache"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

// ErrUseCLI is what a Repo method returns when it cannot do its job the way
// the git program would - the repository declares something gitx does not
// honor in process, a remote is not a bare repository at a local path, a
// history has a merge, a git process holds a lock - so the caller runs its
// git program path instead.
var ErrUseCLI = errors.New("gitx: in-process git does not cover this; use the git program")

// ErrPushRejected is Repo.Push's error when the remote branch moved on and
// the push is not a fast-forward.
var ErrPushRejected = errors.New("gitx: push rejected: not a fast-forward")

// Repo is a repository gitx works on in this process rather than by
// starting the git program for every step. It serves jig's own store: a
// small repository jig alone writes, where a status, a commit and a push to
// a local remote cost a few milliseconds in process and tens (a hundred and
// more on Windows) as git processes. Users' repositories stay on the git
// program (Run), whose hooks, filters and credential helpers they rely on.
// Objects and refs are read and written through go-git's storage; staging,
// commits and ref updates are gitx's own, done the way the git program does
// them. Every method opens the repository afresh and closes it before it
// returns, so a git program run in between (a pack it wrote, a ref it
// packed) is never missed and no file stays open for it to trip on.
type Repo struct {
	dir string
}

// OpenRepo opens the repository whose top-level directory is dir, or
// returns ErrUseCLI when the repository declares something gitx would not
// honor in process (see needsGitProgram and convertsNothing), or go-git
// cannot open it, leaving the git program to handle it or report what is
// wrong.
func OpenRepo(dir string) (*Repo, error) {
	r := &Repo{dir: dir}
	gitDir := r.gitDir()
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return nil, ErrUseCLI // a linked worktree or submodule (.git is a file), or no repository
	}
	repo, done, err := openStorage(gitDir)
	if err != nil {
		return nil, ErrUseCLI
	}
	defer done()
	cfg, err := repo.Config()
	if err != nil {
		return nil, ErrUseCLI
	}
	if needsGitProgram(gitDir, cfg) || !convertsNothing(dir, gitDir) {
		return nil, ErrUseCLI
	}
	return r, nil
}

// openStorage opens the repository whose git directory is gitDir, for
// objects, refs, config and index only, and returns what closes it. Pack
// files stay open until then rather than being reopened for every object.
func openStorage(gitDir string) (*git.Repository, func(), error) {
	abs, err := filepath.Abs(gitDir)
	if err != nil {
		return nil, nil, err
	}
	st := filesystem.NewStorageWithOptions(plainFS{root: abs}, cache.NewObjectLRUDefault(), filesystem.Options{KeepDescriptors: true})
	repo, err := git.Open(st, nil)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return repo, func() { st.Close() }, nil
}

// open opens the repository for one method call.
func (r *Repo) open() (*git.Repository, func(), error) {
	repo, done, err := openStorage(r.gitDir())
	if err != nil {
		return nil, nil, fmt.Errorf("gitx: open %s: %w", r.dir, err)
	}
	return repo, done, nil
}

func (r *Repo) gitDir() string { return filepath.Join(r.dir, ".git") }

// StoreAttributes is the .gitattributes jig writes at a store's root. It
// unsets, for every file, each attribute that changes the bytes stored or
// checked out: line-ending conversion, clean and smudge filters (Git LFS),
// $Id$ expansion and re-encoding. A repository's own .gitattributes wins
// over the machine's attributes file, so with it the git program and gitx,
// which converts nothing, store the same bytes on every host.
const StoreAttributes = "* -text -filter -ident -working-tree-encoding\n"

// convertsNothing reports whether the repository itself rules out every
// conversion of file contents: its root .gitattributes is exactly
// StoreAttributes and it has no info/attributes. (A .gitattributes further
// down is found while staging; see scan.)
func convertsNothing(dir, gitDir string) bool {
	if exists(filepath.Join(gitDir, "info", "attributes")) {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	return err == nil && strings.ReplaceAll(string(data), "\r\n", "\n") == StoreAttributes
}

// needsGitProgram reports whether the repository at gitDir declares, in its
// own files and config, something gitx does not do the way the git program
// would: hooks of its own (a hooks directory with anything but git's
// samples, or core.hooksPath), a shallow or grafted history, alternates, a
// repository format extension (SHA-256 objects, reftable refs), a split or
// sparse index, a separate work tree, its own excludes or attributes file,
// group-shared permissions, or config included from another file, which
// go-git does not read. The machine's own git config is never read, so a
// repository is handled the same way on every host.
func needsGitProgram(gitDir string, cfg *config.Config) bool {
	for _, name := range []string{"shallow", "info/grafts", "objects/info/alternates"} {
		if exists(filepath.Join(gitDir, filepath.FromSlash(name))) {
			return true
		}
	}
	for _, s := range cfg.Raw.Sections {
		if s.IsName("include") || s.IsName("includeIf") || (s.IsName("extensions") && len(s.Options) > 0) {
			return true
		}
	}
	core := cfg.Raw.Section("core")
	if v := core.Option("repositoryFormatVersion"); v != "" && v != "0" && v != "1" {
		return true
	}
	for _, key := range []string{"hooksPath", "worktree", "excludesFile", "attributesFile"} {
		if core.HasOption(key) {
			return true
		}
	}
	if isTrue(core, "splitIndex") || isTrue(core, "sparseCheckout") || isTrue(cfg.Raw.Section("index"), "sparse") {
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

// RepoState is what State reads before anything is staged.
type RepoState struct {
	Branch   string // the checked-out branch; "" when HEAD is detached
	Unmerged bool   // the index has conflicted (unmerged) entries
}

// State reads the checked-out branch and whether the index has unmerged
// entries, writing nothing. An index go-git cannot read (the git program
// writes one under index.skipHash or core.splitIndex, which a machine's
// config can set) returns ErrUseCLI.
func (r *Repo) State() (RepoState, error) {
	var st RepoState
	repo, done, err := r.open()
	if err != nil {
		return st, err
	}
	defer done()
	head, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return st, fmt.Errorf("gitx: read HEAD in %s: %w", r.dir, err)
	}
	if head.Type() == plumbing.SymbolicReference && head.Target().IsBranch() {
		st.Branch = head.Target().Short()
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return st, ErrUseCLI
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
	return st, nil
}

// HasRemote reports whether the repository has a remote named name.
func (r *Repo) HasRemote(name string) bool {
	repo, done, err := r.open()
	if err != nil {
		return false
	}
	defer done()
	_, err = repo.Remote(name)
	return err == nil
}

// lockFile is a file held under the git program's lock (<path>.lock,
// created only if absent). commit writes the new contents into the lock
// file and renames it over the file; release gives the lock up otherwise.
type lockFile struct {
	path string
	f    *os.File
}

// lock takes path's lock file. A lock another process holds returns
// ErrUseCLI, so the git program runs that step and reports the lock as it
// always does.
func lock(path string) (*lockFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if errors.Is(err, fs.ErrExist) {
		return nil, ErrUseCLI
	}
	if err != nil {
		return nil, err
	}
	return &lockFile{path: path, f: f}, nil
}

// commit replaces the locked file with data and gives up the lock.
func (l *lockFile) commit(data []byte) error {
	if _, err := l.f.Write(data); err != nil {
		return err
	}
	if err := l.f.Close(); err != nil {
		return err
	}
	l.f = nil
	return renameOver(l.path+".lock", l.path)
}

// release gives up the lock without changing the file, unless commit did.
func (l *lockFile) release() {
	if l.f != nil {
		l.f.Close()
		l.f = nil
		os.Remove(l.path + ".lock")
	}
}

// renameOver renames src over dst. On Windows a rename fails while another
// process has dst open without sharing deletion (a reader of the file, a
// scanner), so it is retried for a moment, as Git for Windows does.
func renameOver(src, dst string) error {
	err := os.Rename(src, dst)
	for i := 1; err != nil && runtime.GOOS == "windows" && i <= 10; i++ {
		time.Sleep(time.Duration(i) * 5 * time.Millisecond)
		err = os.Rename(src, dst)
	}
	if err != nil {
		os.Remove(src)
	}
	return err
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
	l, err := lock(refPath(gitDir, name))
	if err != nil {
		return err
	}
	defer l.release()
	if from != nil {
		cur, err := refValue(repo, name)
		if err != nil {
			return err
		}
		if cur != *from {
			return errRefMoved
		}
	}
	return l.commit([]byte(to.String() + "\n"))
}

// refValue is the commit ref name holds, ZeroHash when it does not exist;
// a symbolic ref returns ErrUseCLI.
func refValue(repo *git.Repository, name plumbing.ReferenceName) (plumbing.Hash, error) {
	ref, err := repo.Storer.Reference(name)
	switch {
	case errors.Is(err, plumbing.ErrReferenceNotFound):
		return plumbing.ZeroHash, nil
	case err != nil:
		return plumbing.ZeroHash, err
	case ref.Type() != plumbing.HashReference:
		return plumbing.ZeroHash, ErrUseCLI
	}
	return ref.Hash(), nil
}

// LooseObjectsPastAutoGC reports whether `git maintenance run --auto` may
// find work in the repository (see pastAutoGC). Staging and commits through
// Repo only add loose objects, so below that the git program would start
// only to find nothing to do.
func (r *Repo) LooseObjectsPastAutoGC() bool {
	repo, done, err := r.open()
	if err != nil {
		return true // let git decide
	}
	defer done()
	return pastAutoGC(repo, r.gitDir())
}

// pastAutoGC reports whether the repository at gitDir may have passed
// either limit git's automatic gc starts at, estimated the way git
// estimates them: more loose objects in objects/17, one of the 256 object
// directories, than gc.auto (6700) divided by 256, or more packs without a
// .keep file than gc.autoPackLimit (50). Only the repository's own config
// can change either limit (0 turns it off).
func pastAutoGC(repo *git.Repository, gitDir string) bool {
	cfg, err := repo.Config()
	if err != nil {
		return true // let git decide
	}
	gc := cfg.Raw.Section("gc")
	limit := func(key string, def int) (int, bool) {
		v := gc.Option(key)
		if v == "" {
			return def, true
		}
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	loose, ok1 := limit("auto", 6700)
	packs, ok2 := limit("autoPackLimit", 50)
	if !ok1 || !ok2 {
		return true // a value gitx cannot read: let git decide
	}
	if loose <= 0 {
		return false // gc.auto=0 turns automatic gc off altogether
	}
	if entries, err := os.ReadDir(filepath.Join(gitDir, "objects", "17")); err == nil {
		n := 0
		for _, e := range entries {
			if len(e.Name()) == 38 && isHex(e.Name()) {
				n++
			}
		}
		if n > (loose+255)/256 {
			return true
		}
	}
	if packs <= 0 {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(gitDir, "objects", "pack"))
	if err != nil {
		return false
	}
	n := 0
	for _, e := range entries {
		if base, ok := strings.CutSuffix(e.Name(), ".pack"); ok && !exists(filepath.Join(gitDir, "objects", "pack", base+".keep")) {
			n++
		}
	}
	return n > packs
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
