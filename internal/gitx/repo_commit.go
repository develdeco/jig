package gitx

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// CommitAll stages every change and commits it to the checked-out branch,
// as `git add -A && git commit` would, and reports whether there was
// anything to commit. The author and committer are name and email, unless
// GIT_AUTHOR_* or GIT_COMMITTER_* set them, as they do for the git program.
//
// It works from the index, as the git program does: a tracked file whose
// size and modification time still match its index entry is not read, only
// changed and new files are hashed, and the trees are built from the index
// entries, so the cost follows the store's files and what changed, never
// its history. It holds the index's and the branch's lock files throughout
// and writes both the git program's way, into the lock file and renamed
// over the original, so a git process cannot write either meanwhile and a
// reader never sees half of one.
//
// It returns ErrUseCLI, having changed nothing, when a git process holds
// either lock, HEAD is not on a branch, a pinned date does not parse, the
// index cannot be read or tracks anything but regular files, or the work
// tree has what the git program would stage differently: a symbolic link,
// an executable file where the repository's core.fileMode counts it, an
// embedded repository, a .gitattributes below the root, or a name that
// core.ignoreCase or core.precomposeUnicode would match differently.
func (r *Repo) CommitAll(msg, name, email string) (bool, error) {
	author, err := signature("AUTHOR", name, email)
	if err != nil {
		return false, err
	}
	committer, err := signature("COMMITTER", name, email)
	if err != nil {
		return false, err
	}
	repo, done, err := r.open()
	if err != nil {
		return false, err
	}
	defer done()
	cfg, err := repo.Config()
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
	branch := head.Target()
	gitDir := r.gitDir()
	indexLock, err := lock(filepath.Join(gitDir, "index"))
	if err != nil {
		return false, err
	}
	defer indexLock.release()
	branchLock, err := lock(refPath(gitDir, branch))
	if err != nil {
		return false, err
	}
	defer branchLock.release()

	parent, err := refValue(repo, branch)
	if err != nil {
		return false, err
	}
	var parentTree plumbing.Hash
	if !parent.IsZero() {
		c, err := repo.CommitObject(parent)
		if err != nil {
			return false, err
		}
		parentTree = c.TreeHash
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return false, ErrUseCLI
	}
	s, err := newScan(repo, r.dir, gitDir, cfg, idx)
	if err != nil {
		return false, err
	}
	if err := s.run(); err != nil {
		return false, err
	}
	var tree plumbing.Hash
	if len(idx.Entries) > 0 {
		sort.Slice(idx.Entries, func(i, j int) bool { return idx.Entries[i].Name < idx.Entries[j].Name })
		root, err := buildTree(repo, idx.Entries, "")
		if err != nil {
			return false, err
		}
		if err := persistTree(repo, root, parentTree); err != nil {
			return false, err
		}
		tree = root.hash
	}
	if (parent.IsZero() && len(idx.Entries) == 0) || (!parent.IsZero() && tree == parentTree) {
		if s.indexChanged {
			return false, writeIndex(indexLock, idx)
		}
		return false, nil
	}
	c := &object.Commit{
		Author:    *author,
		Committer: *committer,
		Message:   strings.TrimRight(msg, "\n") + "\n",
		TreeHash:  tree,
	}
	if !parent.IsZero() {
		c.ParentHashes = []plumbing.Hash{parent}
	}
	obj := repo.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return false, err
	}
	commit, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return false, fmt.Errorf("gitx: write commit in %s: %w", r.dir, err)
	}
	// The git program's order: the branch moves, then the index.
	if err := branchLock.commit([]byte(commit.String() + "\n")); err != nil {
		return false, fmt.Errorf("gitx: update %s in %s: %w", branch, r.dir, err)
	}
	if err := writeIndex(indexLock, idx); err != nil {
		return true, fmt.Errorf("gitx: write index in %s: %w", r.dir, err)
	}
	return true, nil
}

// writeIndex writes idx into its held lock and renames it over the index.
// go-git's encoder writes no extensions, so no cached tree survives to
// disagree with the entries.
func writeIndex(l *lockFile, idx *index.Index) error {
	sort.Slice(idx.Entries, func(i, j int) bool { return idx.Entries[i].Name < idx.Entries[j].Name })
	idx.Cache, idx.ResolveUndo, idx.EndOfIndexEntry = nil, nil, nil
	var buf bytes.Buffer
	if err := index.NewEncoder(&buf).Encode(idx); err != nil {
		return err
	}
	return l.commit(buf.Bytes())
}

// scan brings one index up to date with the work tree, as `git add -A`
// does, writing the blobs of what changed.
type scan struct {
	repo       *git.Repository
	dir        string
	idx        *index.Index
	indexTime  time.Time // the index file's modification time; zero when there is none
	fileMode   bool      // core.fileMode: the executable bit counts
	ignoreCase bool      // core.ignoreCase
	precompose bool      // core.precomposeUnicode
	tracked    map[string]*index.Entry
	names      []string // tracked paths, sorted
	folded     map[string]bool
	seen       map[string]bool

	indexChanged bool
}

// newScan prepares a scan of idx against the work tree at dir. An index
// entry that is not a plain tracked file (a conflict stage, an executable,
// a symbolic link, a submodule, a sparse or intent-to-add entry) returns
// ErrUseCLI.
func newScan(repo *git.Repository, dir, gitDir string, cfg *config.Config, idx *index.Index) (*scan, error) {
	core := cfg.Raw.Section("core")
	s := &scan{
		repo:       repo,
		dir:        dir,
		idx:        idx,
		fileMode:   !core.HasOption("fileMode") || isTrue(core, "fileMode"),
		ignoreCase: isTrue(core, "ignoreCase"),
		precompose: isTrue(core, "precomposeUnicode"),
		tracked:    make(map[string]*index.Entry, len(idx.Entries)),
		folded:     map[string]bool{},
		seen:       make(map[string]bool, len(idx.Entries)),
	}
	if fi, err := os.Stat(filepath.Join(gitDir, "index")); err == nil {
		s.indexTime = fi.ModTime()
	}
	for _, e := range idx.Entries {
		if e.Stage != 0 || e.Mode != filemode.Regular || e.SkipWorktree || e.IntentToAdd {
			return nil, ErrUseCLI
		}
		s.tracked[e.Name] = e
		s.names = append(s.names, e.Name)
		if s.ignoreCase {
			s.folded[strings.ToLower(e.Name)] = true
		}
	}
	sort.Strings(s.names)
	return s, nil
}

// run walks the work tree once. A tracked file is compared with its entry
// by size and modification time from the directory listing (on Windows the
// listing carries them, so no file is opened or stat'ed on its own), and
// hashed only when they differ or its time is not safely before the
// index's; an untracked file the repository's ignore rules do not exclude
// is added. Tracked files the walk did not find are removed.
func (s *scan) run() error {
	var ps []gitignore.Pattern
	exclude, err := readPatterns(filepath.Join(s.dir, ".git", "info", "exclude"), nil)
	if err != nil {
		return err
	}
	ps = append(ps, exclude...)
	if err := s.walk(nil, s.dir, ps, false); err != nil {
		return err
	}
	kept := s.idx.Entries[:0]
	for _, e := range s.idx.Entries {
		if s.seen[e.Name] {
			kept = append(kept, e)
		} else {
			s.indexChanged = true
		}
	}
	s.idx.Entries = kept
	return nil
}

// walk scans the directory abs, at parts under the top level, with the
// ignore patterns in force above it. In an ignored directory (walked only
// because it holds tracked files) untracked files stay out.
func (s *scan) walk(parts []string, abs string, ps []gitignore.Pattern, ignored bool) error {
	ents, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Name() == ".gitignore" && e.Type().IsRegular() {
			own, err := readPatterns(filepath.Join(abs, ".gitignore"), parts)
			if err != nil {
				return err
			}
			ps = append(ps[:len(ps):len(ps)], own...)
		}
	}
	m := gitignore.NewMatcher(ps)
	for _, e := range ents {
		name := e.Name()
		if name == ".git" {
			if len(parts) > 0 && !ignored {
				return ErrUseCLI // an embedded repository
			}
			continue
		}
		if s.precompose && !isASCII(name) {
			return ErrUseCLI // the git program would compare its precomposed form
		}
		if name == ".gitattributes" && len(parts) > 0 {
			return ErrUseCLI // it can turn conversion back on below the root
		}
		p := append(parts[:len(parts):len(parts)], name)
		rel := strings.Join(p, "/")
		if e.IsDir() {
			sub := ignored || m.Match(p, true)
			if sub && !s.tracksUnder(rel+"/") {
				continue
			}
			if err := s.walk(p, filepath.Join(abs, name), ps, sub); err != nil {
				return err
			}
			continue
		}
		entry, tracked := s.tracked[rel]
		if !tracked && (ignored || m.Match(p, false)) {
			continue
		}
		if !tracked && s.ignoreCase && s.folded[strings.ToLower(rel)] {
			return ErrUseCLI // the git program would match it to the tracked name
		}
		info, err := e.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue // removed while the walk ran
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || (s.fileMode && info.Mode()&0o111 != 0) {
			return ErrUseCLI
		}
		if tracked {
			s.seen[rel] = true
			if entry.Size == uint32(info.Size()) && sameTime(entry.ModifiedAt, info.ModTime()) &&
				(s.indexTime.IsZero() || info.ModTime().Before(s.indexTime)) {
				continue
			}
		} else {
			entry = &index.Entry{Name: rel, Mode: filemode.Regular}
			s.idx.Entries = append(s.idx.Entries, entry)
			s.seen[rel] = true
		}
		h, err := s.writeBlob(filepath.Join(abs, name))
		if err != nil {
			return err
		}
		entry.Hash, entry.ModifiedAt, entry.Size = h, info.ModTime(), uint32(info.Size())
		s.indexChanged = true
	}
	return nil
}

// sameTime reports whether an index entry's modification time matches the
// file's. A git built without nanosecond timestamps records whole seconds,
// and then only the seconds are compared, as that git does; a file changed
// within that second after the index was written is still caught, since
// its time is not before the index's.
func sameTime(entry, file time.Time) bool {
	if entry.Nanosecond() == 0 {
		return entry.Unix() == file.Unix()
	}
	return entry.Equal(file)
}

// tracksUnder reports whether any tracked path starts with prefix.
func (s *scan) tracksUnder(prefix string) bool {
	i := sort.SearchStrings(s.names, prefix)
	return i < len(s.names) && strings.HasPrefix(s.names[i], prefix)
}

// writeBlob stores the file at path as a blob, as is, and returns its hash.
func (s *scan) writeBlob(path string) (plumbing.Hash, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	obj := s.repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(data)))
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(data); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return storeObject(s.repo, obj)
}

// storeObject writes obj unless the repository has it, and returns its hash.
func storeObject(repo *git.Repository, obj plumbing.EncodedObject) (plumbing.Hash, error) {
	h := obj.Hash()
	if repo.Storer.HasEncodedObject(h) == nil {
		return h, nil
	}
	return repo.Storer.SetEncodedObject(obj)
}

// readPatterns reads a gitignore-format file for the directory at domain,
// as go-git's own status does; a missing file has none.
func readPatterns(path string, domain []string) ([]gitignore.Pattern, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ps []gitignore.Pattern
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			ps = append(ps, gitignore.ParsePattern(line, domain))
		}
	}
	return ps, sc.Err()
}

// isASCII reports whether s is plain ASCII.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// treeNode is a tree built from index entries, in memory until
// persistTree writes it.
type treeNode struct {
	obj  plumbing.EncodedObject
	hash plumbing.Hash
	subs map[string]*treeNode
}

// buildTree builds the tree for entries (sorted by path, all under prefix)
// and the trees below it. Sorted full paths put each directory's entries
// together and in git's tree order, where a directory sorts as its name
// followed by "/".
func buildTree(repo *git.Repository, entries []*index.Entry, prefix string) (*treeNode, error) {
	node := &treeNode{subs: map[string]*treeNode{}}
	var t object.Tree
	for i := 0; i < len(entries); {
		rest := entries[i].Name[len(prefix):]
		dir, _, isDir := strings.Cut(rest, "/")
		if !isDir {
			t.Entries = append(t.Entries, object.TreeEntry{Name: rest, Mode: entries[i].Mode, Hash: entries[i].Hash})
			i++
			continue
		}
		sub := prefix + dir + "/"
		j := i
		for j < len(entries) && strings.HasPrefix(entries[j].Name, sub) {
			j++
		}
		child, err := buildTree(repo, entries[i:j], sub)
		if err != nil {
			return nil, err
		}
		node.subs[dir] = child
		t.Entries = append(t.Entries, object.TreeEntry{Name: dir, Mode: filemode.Dir, Hash: child.hash})
		i = j
	}
	node.obj = repo.Storer.NewEncodedObject()
	if err := t.Encode(node.obj); err != nil {
		return nil, err
	}
	node.hash = node.obj.Hash()
	return node, nil
}

// persistTree writes node and every tree below it that differs from known,
// the tree at the same path in the parent commit (ZeroHash for none), which
// the repository already has with everything in it: only the trees along
// changed paths are read and written, however many directories the store
// holds.
func persistTree(repo *git.Repository, node *treeNode, known plumbing.Hash) error {
	if node.hash == known {
		return nil
	}
	old := map[string]plumbing.Hash{}
	if !known.IsZero() {
		kt, err := repo.TreeObject(known)
		if err != nil {
			return err
		}
		for _, e := range kt.Entries {
			if e.Mode == filemode.Dir {
				old[e.Name] = e.Hash
			}
		}
	}
	for name, sub := range node.subs {
		if err := persistTree(repo, sub, old[name]); err != nil {
			return err
		}
	}
	_, err := storeObject(repo, node.obj)
	return err
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
	n, err := strconv.ParseInt(secs, 10, 64)
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
	return time.Unix(n, 0).In(time.FixedZone("", off)), true
}
