package store

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A mirror sync reads the remote for as long as the network takes and writes
// records as it goes, and it must not hold the checkpoint lock meanwhile: every
// claim would wait on a round trip. But records written straight into the
// working tree sit there as unstaged changes until the landing commits them,
// and a claim that has to rebase refuses a tree with changes in it. So while a
// sync runs (HoldTrackerWrites) the tracker tree's writers do not touch the
// working tree: they change a pending tree kept in memory, which their own
// reads see through, and the landing applies it to the working tree and
// commits it inside one hold of the lock (landTrackerWrites).

// pendKind is what a pending entry says its path is now.
type pendKind int

const (
	// pendFile is a regular file holding data.
	pendFile pendKind = iota + 1
	// pendLink is a symlink to target.
	pendLink
	// pendDir is a folder. fresh is set when nothing of the working tree
	// shows through it: the path held something else, or nothing, before.
	pendDir
	// pendGone is a path that no longer exists.
	pendGone
)

type pendEntry struct {
	kind   pendKind
	data   []byte
	target string
	fresh  bool
}

// pendingTree is the sync's changes to the working tree, by store-relative
// slash path, one entry per path: its state once the sync is done.
type pendingTree struct {
	root    string
	refused func(rel string, err error)
	mu      sync.Mutex
	entries map[string]pendEntry
	// directs counts the holds of direct: above 0, writes go to the working tree.
	directs int
}

// HoldTrackerWrites makes the tracker tree's writers keep their changes
// pending until the landing (LandTrackerWrites, or the end of
// RunCheckpointHook), instead of changing the working tree as they go. A
// caller that runs a mirror sync outside the checkpoint hook calls it first.
func (s *Store) HoldTrackerWrites() {
	s.tracker.pmu.Lock()
	defer s.tracker.pmu.Unlock()
	if s.tracker.pend == nil {
		s.tracker.pend = &pendingTree{root: s.Root, refused: s.linkRefused, entries: map[string]pendEntry{}}
	}
}

// takePending returns the pending tree and stops holding writes.
func (s *Store) takePending() *pendingTree {
	s.tracker.pmu.Lock()
	defer s.tracker.pmu.Unlock()
	p := s.tracker.pend
	s.tracker.pend = nil
	return p
}

func (s *Store) pending() *pendingTree {
	s.tracker.pmu.Lock()
	defer s.tracker.pmu.Unlock()
	return s.tracker.pend
}

func (p *pendingTree) empty() bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries) == 0
}

// apply changes the working tree to what the pending tree says: folders and
// files first, parents before children, then removals, children before
// parents. A link the file system refuses is reported to refused, and left
// out.
func (p *pendingTree) apply() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rels := make([]string, 0, len(p.entries))
	for rel := range p.entries {
		rels = append(rels, rel)
	}
	sort.Slice(rels, func(i, j int) bool {
		di, dj := strings.Count(rels[i], "/"), strings.Count(rels[j], "/")
		if di != dj {
			return di < dj
		}
		return rels[i] < rels[j]
	})
	for _, rel := range rels {
		if p.entries[rel].kind == pendGone {
			continue
		}
		if err := p.applyOne(rel); err != nil {
			return err
		}
	}
	for i := len(rels) - 1; i >= 0; i-- {
		if p.entries[rels[i]].kind == pendGone {
			_ = p.applyOne(rels[i])
		}
	}
	return nil
}

// applyOne makes the working tree hold rel's pending entry and drops the
// entry. A removal of a folder that still holds something stays undone.
func (p *pendingTree) applyOne(rel string) error {
	e, ok := p.entries[rel]
	if !ok {
		return nil
	}
	delete(p.entries, rel)
	a := filepath.Join(p.root, filepath.FromSlash(rel))
	fi, err := os.Lstat(a)
	switch e.kind {
	case pendGone:
		if err := os.Remove(a); err != nil && !os.IsNotExist(err) {
			return nil
		}
	case pendDir:
		if err == nil && !fi.IsDir() {
			if err := os.Remove(a); err != nil {
				return err
			}
		}
		return os.MkdirAll(a, 0o755)
	case pendFile:
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			// A write would otherwise follow the link.
			if err := os.Remove(a); err != nil {
				return err
			}
		}
		return AtomicWrite(a, e.data)
	case pendLink:
		if err == nil {
			if err := os.Remove(a); err != nil {
				return err
			}
		}
		if err := os.Symlink(e.target, a); err != nil && p.refused != nil {
			p.refused(rel, err)
		}
	}
	return nil
}

// settle makes the working tree hold the pending entries at rel and above it,
// for a write that goes straight to the working tree (see Direct): the write
// must find the path as the pending tree has it, and leave none of its own
// entries to be applied over it later.
func (p *pendingTree) settle(rel string) error {
	var chain []string
	for a := rel; a != "." && a != ""; a = path.Dir(a) {
		chain = append(chain, a)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if err := p.applyOne(chain[i]); err != nil {
			return err
		}
	}
	return nil
}

// direct makes the tree's writes go to the working tree, until the returned
// function is called: a Claim's write callback commits what it wrote itself,
// under the checkpoint lock, so its records must be on disk, not pending.
func (p *pendingTree) direct() (done func()) {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	p.directs++
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		p.directs--
		p.mu.Unlock()
	}
}

// DirectTrackerWrites is what Claim runs around its write callback; see
// pendingTree.direct.
func (s *Store) DirectTrackerWrites() (done func()) {
	return s.pending().direct()
}

// relOf is abs as a store-relative slash path, when it is inside the store.
func (p *pendingTree) relOf(abs string) (string, bool) {
	r, err := filepath.Rel(p.root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

type pendInfo struct {
	name string
	e    pendEntry
}

func (i pendInfo) Name() string { return i.name }
func (i pendInfo) Size() int64  { return int64(len(i.e.data)) }
func (i pendInfo) Mode() fs.FileMode {
	switch i.e.kind {
	case pendDir:
		return fs.ModeDir | 0o755
	case pendLink:
		return fs.ModeSymlink | 0o777
	}
	return 0o644
}
func (i pendInfo) ModTime() time.Time { return time.Time{} }
func (i pendInfo) IsDir() bool        { return i.e.kind == pendDir }
func (i pendInfo) Sys() any           { return nil }

func notExist(op, abs string) error {
	return &fs.PathError{Op: op, Path: abs, Err: fs.ErrNotExist}
}

// lookup is what abs is now: the pending entry for it when there is one (and
// ent says so), otherwise the working tree's, unless a pending entry above it
// hides the working tree there.
func (p *pendingTree) lookup(abs string) (fs.FileInfo, *pendEntry, error) {
	rel, ok := p.relOf(abs)
	if !ok || rel == "." {
		fi, err := os.Lstat(abs)
		return fi, nil, err
	}
	hidden := false
	for a := path.Dir(rel); a != "."; a = path.Dir(a) {
		e, ok := p.entries[a]
		if !ok {
			continue
		}
		switch {
		case e.kind != pendDir:
			return nil, nil, notExist("lstat", abs)
		case e.fresh:
			hidden = true
		}
	}
	if e, ok := p.entries[rel]; ok {
		if e.kind == pendGone {
			return nil, nil, notExist("lstat", abs)
		}
		return pendInfo{name: path.Base(rel), e: e}, &e, nil
	}
	if hidden {
		return nil, nil, notExist("lstat", abs)
	}
	fi, err := os.Lstat(abs)
	return fi, nil, err
}

func (p *pendingTree) lstat(abs string) (fs.FileInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fi, _, err := p.lookup(abs)
	return fi, err
}

func (p *pendingTree) stat(abs string) (fs.FileInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for range 8 {
		fi, ent, err := p.lookup(abs)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			return fi, err
		}
		var target string
		if ent != nil {
			target = ent.target
		} else if target, err = os.Readlink(abs); err != nil {
			return nil, err
		}
		target = filepath.FromSlash(target)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(abs), target)
		}
		abs = target
	}
	return nil, &fs.PathError{Op: "stat", Path: abs, Err: errors.New("too many levels of symbolic links")}
}

func (p *pendingTree) readlink(abs string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fi, ent, err := p.lookup(abs)
	if err != nil {
		return "", err
	}
	if ent != nil && ent.kind == pendLink {
		return ent.target, nil
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return "", &fs.PathError{Op: "readlink", Path: abs, Err: errors.New("not a symbolic link")}
	}
	return os.Readlink(abs)
}

func (p *pendingTree) readFile(abs string) ([]byte, error) {
	p.mu.Lock()
	_, ent, err := p.lookup(abs)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if ent != nil {
		if ent.kind != pendFile {
			return nil, &fs.PathError{Op: "read", Path: abs, Err: errors.New("not a regular file")}
		}
		return append([]byte(nil), ent.data...), nil
	}
	return ReadFile(abs)
}

func (p *pendingTree) readDir(abs string) ([]fs.DirEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readDirLocked(abs)
}

func (p *pendingTree) readDirLocked(abs string) ([]fs.DirEntry, error) {
	fi, ent, err := p.lookup(abs)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: abs, Err: errors.New("not a directory")}
	}
	byName := map[string]fs.DirEntry{}
	if ent == nil || !ent.fresh {
		base, err := os.ReadDir(abs)
		if err != nil && ent == nil {
			return nil, err
		}
		for _, d := range base {
			byName[d.Name()] = d
		}
	}
	if rel, ok := p.relOf(abs); ok {
		for k, e := range p.entries {
			if path.Dir(k) != rel {
				continue
			}
			name := path.Base(k)
			if e.kind == pendGone {
				delete(byName, name)
				continue
			}
			byName[name] = fs.FileInfoToDirEntry(pendInfo{name: name, e: e})
		}
	}
	out := make([]fs.DirEntry, 0, len(byName))
	for _, d := range byName {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (p *pendingTree) mkdirAll(abs string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mkdirAllLocked(abs)
}

func (p *pendingTree) mkdirAllLocked(abs string) error {
	if direct, err := p.goDirect(abs); direct {
		if err != nil {
			return err
		}
		return os.MkdirAll(abs, 0o755)
	}
	rel, ok := p.relOf(abs)
	if !ok {
		return os.MkdirAll(abs, 0o755)
	}
	if rel == "." {
		return nil
	}
	cur := p.root
	for _, seg := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, seg)
		fi, _, err := p.lookup(cur)
		switch {
		case err == nil && fi.IsDir():
			continue
		case err == nil:
			return &fs.PathError{Op: "mkdir", Path: cur, Err: errors.New("not a directory")}
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		r, _ := p.relOf(cur)
		p.entries[r] = pendEntry{kind: pendDir, fresh: true}
	}
	return nil
}

func (p *pendingTree) writeFile(abs string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeFileLocked(abs, data)
}

func (p *pendingTree) writeFileLocked(abs string, data []byte) error {
	if direct, err := p.goDirect(abs); direct {
		if err != nil {
			return err
		}
		return AtomicWrite(abs, data)
	}
	rel, ok := p.relOf(abs)
	if !ok || rel == "." {
		return AtomicWrite(abs, data)
	}
	if err := p.mkdirAllLocked(filepath.Dir(abs)); err != nil {
		return err
	}
	if fi, _, err := p.lookup(abs); err == nil && fi.IsDir() {
		return &fs.PathError{Op: "write", Path: abs, Err: errors.New("is a directory")}
	}
	p.entries[rel] = pendEntry{kind: pendFile, data: append([]byte(nil), data...)}
	return nil
}

func (p *pendingTree) remove(abs string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.removeLocked(abs)
}

func (p *pendingTree) removeLocked(abs string) error {
	if direct, err := p.goDirect(abs); direct {
		if err != nil {
			return err
		}
		return os.Remove(abs)
	}
	rel, ok := p.relOf(abs)
	if !ok || rel == "." {
		return os.Remove(abs)
	}
	fi, ent, err := p.lookup(abs)
	if err != nil {
		return &fs.PathError{Op: "remove", Path: abs, Err: fs.ErrNotExist}
	}
	if fi.IsDir() {
		kids, err := p.readDirLocked(abs)
		if err != nil {
			return err
		}
		if len(kids) > 0 {
			return &fs.PathError{Op: "remove", Path: abs, Err: errors.New("directory not empty")}
		}
	}
	if ent != nil {
		// Made by the sync: gone from the pending tree when the working tree
		// never had it, and otherwise a removal of what the working tree has.
		if _, derr := os.Lstat(abs); errors.Is(derr, fs.ErrNotExist) {
			delete(p.entries, rel)
			return nil
		}
	}
	p.entries[rel] = pendEntry{kind: pendGone}
	return nil
}

func (p *pendingTree) rename(from, to string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if direct, err := p.goDirect(from, to); direct {
		if err != nil {
			return err
		}
		return os.Rename(from, to)
	}
	fi, ent, err := p.lookup(from)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrNotExist}
	}
	if fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("only a file is renamed")}
	}
	var data []byte
	if ent != nil {
		data = ent.data
	} else if data, err = ReadFile(from); err != nil {
		return err
	}
	if err := p.writeFileLocked(to, data); err != nil {
		return err
	}
	return p.removeLocked(from)
}

func (p *pendingTree) symlink(target, abs string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if direct, err := p.goDirect(abs); direct {
		if err != nil {
			return err
		}
		return os.Symlink(target, abs)
	}
	rel, ok := p.relOf(abs)
	if !ok || rel == "." {
		return os.Symlink(target, abs)
	}
	if _, _, err := p.lookup(abs); err == nil {
		return &fs.PathError{Op: "symlink", Path: abs, Err: fs.ErrExist}
	}
	if fi, _, err := p.lookup(filepath.Dir(abs)); err != nil || !fi.IsDir() {
		return &fs.PathError{Op: "symlink", Path: abs, Err: fs.ErrNotExist}
	}
	p.entries[rel] = pendEntry{kind: pendLink, target: target}
	return nil
}

// The t-prefixed methods are what the tracker tree's writers and readers use
// for the working tree: the operating system's when no sync is holding its
// writes, the pending tree's when one is.

func (s *Store) tLstat(abs string) (fs.FileInfo, error) {
	if p := s.pending(); p != nil {
		return p.lstat(abs)
	}
	return os.Lstat(abs)
}

func (s *Store) tStat(abs string) (fs.FileInfo, error) {
	if p := s.pending(); p != nil {
		return p.stat(abs)
	}
	return os.Stat(abs)
}

func (s *Store) tReadlink(abs string) (string, error) {
	if p := s.pending(); p != nil {
		return p.readlink(abs)
	}
	return os.Readlink(abs)
}

func (s *Store) tReadFile(abs string) ([]byte, error) {
	if p := s.pending(); p != nil {
		return p.readFile(abs)
	}
	return ReadFile(abs)
}

func (s *Store) tReadDir(abs string) ([]fs.DirEntry, error) {
	if p := s.pending(); p != nil {
		return p.readDir(abs)
	}
	return os.ReadDir(abs)
}

func (s *Store) tMkdirAll(abs string, perm fs.FileMode) error {
	if p := s.pending(); p != nil {
		return p.mkdirAll(abs)
	}
	return os.MkdirAll(abs, perm)
}

func (s *Store) tWriteFile(abs string, data []byte) error {
	if p := s.pending(); p != nil {
		return p.writeFile(abs, data)
	}
	return AtomicWrite(abs, data)
}

func (s *Store) tRemove(abs string) error {
	if p := s.pending(); p != nil {
		return p.remove(abs)
	}
	return os.Remove(abs)
}

func (s *Store) tRename(from, to string) error {
	if p := s.pending(); p != nil {
		return p.rename(from, to)
	}
	return os.Rename(from, to)
}

func (s *Store) tSymlink(target, abs string) error {
	if p := s.pending(); p != nil {
		return p.symlink(target, abs)
	}
	return os.Symlink(target, abs)
}

// goDirect reports whether writes go straight to the working tree, and when
// they do, first settles the pending entries at and above each of abss.
func (p *pendingTree) goDirect(abss ...string) (bool, error) {
	if p.directs == 0 {
		return false, nil
	}
	for _, abs := range abss {
		if rel, ok := p.relOf(abs); ok {
			if err := p.settle(rel); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}
