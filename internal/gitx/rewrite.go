package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// RewriteHeadCommit rewrites the commit the checked-out branch of the
// repository at dir points at, so that each of paths holds, in that commit,
// the bytes the work tree holds for it now. It is for one narrow job and is
// not a way to edit history: a repository that jig's test fixture has just
// created with the git program, whose one commit was made before the files
// that carry an absolute path were pointed at their final directory (a copy
// of the fixture, moved to a new place), so that the commit, the refs and the
// index of the copy say what a fixture built there from scratch would say.
//
// Paths are slash-separated and relative to dir. A path the commit does not
// track (an untracked file, one a .gitignore keeps out, anything under
// .git) is left alone, and a tracked path whose bytes already are the
// commit's changes nothing; with nothing to change the repository is not
// touched at all. The file is stored as it is, so a repository that converts
// line endings (core.autocrlf true or input) is refused.
//
// What changes, for the commit and nothing else about it:
//
//   - New blobs for the paths, new trees along them up to the root, and a new
//     commit with those trees and the old commit's parents, author, committer,
//     dates and message, written as loose objects into the repository and into
//     each of mirrors, bare repositories (git directories) that hold the
//     same branch at the same commit, such as the remote the fixture cloned
//     from it.
//   - Every ref and reflog line of the repository and of each mirror that
//     named the old commit (the branch, packed-refs, logs/HEAD and
//     logs/refs/...) names the new one.
//   - The repository's index: the entries of the rewritten paths take the new
//     blobs and the work tree files' stat data, and the index is written
//     without its extensions (git rebuilds the cached trees).
//   - The loose objects the rewrite replaced (the old commit, its changed
//     trees and the old blobs) are removed from the repository and each
//     mirror unless the new commit still uses them, so the object database
//     holds what a commit made from the final files would have written. A
//     replaced object that is packed stays where it is.
//
// Only files outside objects/ are written in place (through the lock file and
// a rename, which also gives a file that was a hard link a new inode), and
// objects are only added or unlinked, never written to, so a repository
// whose loose objects are hard links to another repository's stays correct
// for both.
//
// A signed commit, a HEAD that is not on a branch, a mirror that does not
// hold the branch at the old commit, an index go-git cannot read and a lock
// file another process holds are errors. An error part way leaves the
// repository as far as it got; the caller discards it.
func RewriteHeadCommit(dir string, paths []string, mirrors ...string) error {
	gitDir := filepath.Join(dir, ".git")
	repo, done, err := openStorage(gitDir)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	defer done()
	cfg, err := repo.Config()
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: read config: %w", dir, err)
	}
	switch strings.ToLower(cfg.Raw.Section("core").Option("autocrlf")) {
	case "true", "input":
		return fmt.Errorf("gitx: rewrite the commit in %s: core.autocrlf converts line endings, and the rewrite stores files as they are", dir)
	}
	head, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: read HEAD: %w", dir, err)
	}
	if head.Type() != plumbing.SymbolicReference || !head.Target().IsBranch() {
		return fmt.Errorf("gitx: rewrite the commit in %s: HEAD is not on a branch", dir)
	}
	branch := head.Target()
	oldID, err := refValue(repo, branch)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: read %s: %w", dir, branch, err)
	}
	if oldID.IsZero() {
		return fmt.Errorf("gitx: rewrite the commit in %s: %s has no commit", dir, branch)
	}
	commit, err := repo.CommitObject(oldID)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: read %s: %w", dir, oldID, err)
	}
	if commit.PGPSignature != "" || commit.MergeTag != "" {
		return fmt.Errorf("gitx: rewrite the commit in %s: %s is signed or carries a tag, which a rewrite would break", dir, oldID)
	}
	// A commit that go-git does not write back as it read it (a header or a
	// time zone it normalizes) would change in more than its tree.
	if again, err := encodeCommit(repo, commit); err != nil || again.Hash() != oldID {
		return fmt.Errorf("gitx: rewrite the commit in %s: %s does not encode back to itself", dir, oldID)
	}
	root, err := repo.TreeObject(commit.TreeHash)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: read its tree: %w", dir, err)
	}

	changes, added, stale, err := changedBlobs(repo, root, dir, paths)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	newRoot, trees, staleTrees, err := rewriteTree(repo, commit.TreeHash, changes)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	next := *commit
	next.TreeHash = newRoot
	newCommit, err := encodeCommit(repo, &next)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	newID := newCommit.Hash()
	objs := append(append(added, trees...), newCommit)
	stale = append(append(stale, staleTrees...), oldID)

	// Objects first, everywhere, then the refs that make them reachable.
	if err := storeAll(repo, objs); err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	keep, err := reachable(repo, newRoot)
	if err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	keep[newID] = true
	for _, m := range mirrors {
		if err := rewriteMirror(m, branch, oldID, newID, objs, stale, keep); err != nil {
			return fmt.Errorf("gitx: rewrite the commit in %s: mirror %s: %w", dir, m, err)
		}
	}
	if err := repointRefs(gitDir, oldID, newID); err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	if got, err := refValue(repo, branch); err != nil || got != newID {
		return fmt.Errorf("gitx: rewrite the commit in %s: %s is %s after the rewrite, want %s", dir, branch, got, newID)
	}
	if err := repointIndex(gitDir, dir, changes); err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	if err := pruneLoose(gitDir, stale, keep); err != nil {
		return fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
	}
	return nil
}

// changedBlobs reads each of paths that root tracks from the work tree at
// dir and returns, by path, the blob it now has when that is not the
// tracked one, with the new blobs as objects to store and the hashes of the
// blobs they replace.
func changedBlobs(repo *git.Repository, root *object.Tree, dir string, paths []string) (map[string]plumbing.Hash, []plumbing.EncodedObject, []plumbing.Hash, error) {
	changes := map[string]plumbing.Hash{}
	var objs []plumbing.EncodedObject
	var stale []plumbing.Hash
	for _, p := range paths {
		p = filepath.ToSlash(p)
		if p == "" || strings.HasPrefix(p, "/") || strings.EqualFold(strings.SplitN(p, "/", 2)[0], ".git") {
			continue
		}
		entry, err := root.FindEntry(p)
		if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("gitx: rewrite the commit in %s: find %s: %w", dir, p, err)
		}
		if entry.Mode != filemode.Regular && entry.Mode != filemode.Executable {
			return nil, nil, nil, fmt.Errorf("gitx: rewrite the commit in %s: %s is not a file the commit tracks as one", dir, p)
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("gitx: rewrite the commit in %s: %w", dir, err)
		}
		blob := repo.Storer.NewEncodedObject()
		blob.SetType(plumbing.BlobObject)
		blob.SetSize(int64(len(data)))
		w, err := blob.Writer()
		if err != nil {
			return nil, nil, nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, nil, nil, err
		}
		if err := w.Close(); err != nil {
			return nil, nil, nil, err
		}
		if blob.Hash() == entry.Hash {
			continue
		}
		changes[p] = blob.Hash()
		objs = append(objs, blob)
		stale = append(stale, entry.Hash)
	}
	return changes, objs, stale, nil
}

// rewriteTree returns the hash of the tree treeHash would be with each of
// changes (path under it to blob) applied, the new trees it took (as objects
// to store) and the hashes of the trees they replace. Entries keep their
// order, since only hashes change.
func rewriteTree(repo *git.Repository, treeHash plumbing.Hash, changes map[string]plumbing.Hash) (plumbing.Hash, []plumbing.EncodedObject, []plumbing.Hash, error) {
	tree, err := repo.TreeObject(treeHash)
	if err != nil {
		return plumbing.ZeroHash, nil, nil, err
	}
	here := map[string]plumbing.Hash{}
	below := map[string]map[string]plumbing.Hash{}
	for p, h := range changes {
		first, rest, nested := strings.Cut(p, "/")
		if !nested {
			here[first] = h
			continue
		}
		if below[first] == nil {
			below[first] = map[string]plumbing.Hash{}
		}
		below[first][rest] = h
	}
	var objs []plumbing.EncodedObject
	stale := []plumbing.Hash{treeHash}
	entries := make([]object.TreeEntry, len(tree.Entries))
	copy(entries, tree.Entries)
	for i, e := range entries {
		if h, ok := here[e.Name]; ok {
			entries[i].Hash = h
			continue
		}
		sub, ok := below[e.Name]
		if !ok {
			continue
		}
		h, subObjs, subStale, err := rewriteTree(repo, e.Hash, sub)
		if err != nil {
			return plumbing.ZeroHash, nil, nil, err
		}
		entries[i].Hash = h
		objs = append(objs, subObjs...)
		stale = append(stale, subStale...)
	}
	next := object.Tree{Entries: entries}
	obj := repo.Storer.NewEncodedObject()
	if err := next.Encode(obj); err != nil {
		return plumbing.ZeroHash, nil, nil, err
	}
	return obj.Hash(), append(objs, obj), stale, nil
}

// encodeCommit encodes c as the object go-git would write for it.
func encodeCommit(repo *git.Repository, c *object.Commit) (plumbing.EncodedObject, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// storeAll writes each of objs that repo does not have yet.
func storeAll(repo *git.Repository, objs []plumbing.EncodedObject) error {
	for _, o := range objs {
		if _, err := storeObject(repo, o); err != nil {
			return err
		}
	}
	return nil
}

// reachable is every tree and blob under the tree root, root included.
func reachable(repo *git.Repository, root plumbing.Hash) (map[plumbing.Hash]bool, error) {
	seen := map[plumbing.Hash]bool{root: true}
	var walk func(plumbing.Hash) error
	walk = func(h plumbing.Hash) error {
		t, err := repo.TreeObject(h)
		if err != nil {
			return err
		}
		for _, e := range t.Entries {
			seen[e.Hash] = true
			if e.Mode == filemode.Dir {
				if err := walk(e.Hash); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return seen, walk(root)
}

// rewriteMirror gives the bare repository mirror the new objects and moves
// its branch from oldID to newID, which it must hold at oldID.
func rewriteMirror(mirror string, branch plumbing.ReferenceName, oldID, newID plumbing.Hash, objs []plumbing.EncodedObject, stale []plumbing.Hash, keep map[plumbing.Hash]bool) error {
	repo, done, err := openStorage(mirror)
	if err != nil {
		return err
	}
	defer done()
	if got, err := refValue(repo, branch); err != nil || got != oldID {
		return fmt.Errorf("%s is %s, not the rewritten commit %s", branch, got, oldID)
	}
	if err := storeAll(repo, objs); err != nil {
		return err
	}
	if err := repointRefs(mirror, oldID, newID); err != nil {
		return err
	}
	if got, err := refValue(repo, branch); err != nil || got != newID {
		return fmt.Errorf("%s is %s after the rewrite, want %s", branch, got, newID)
	}
	return pruneLoose(mirror, stale, keep)
}

// repointRefs makes every ref and reflog line in the git directory gitDir
// that names the commit from name to instead: HEAD, ORIG_HEAD, FETCH_HEAD,
// packed-refs and every file under refs/ and logs/. Each is replaced
// through its lock file, so a reader sees the old file or the new one.
func repointRefs(gitDir string, from, to plumbing.Hash) error {
	old, now := []byte(from.String()), []byte(to.String())
	files := []string{
		filepath.Join(gitDir, "HEAD"),
		filepath.Join(gitDir, "ORIG_HEAD"),
		filepath.Join(gitDir, "FETCH_HEAD"),
		filepath.Join(gitDir, "packed-refs"),
	}
	for _, sub := range []string{"refs", "logs"} {
		err := filepath.WalkDir(filepath.Join(gitDir, sub), func(p string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.Type().IsRegular() {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !bytes.Contains(data, old) {
			continue
		}
		if err := replaceFile(p, bytes.ReplaceAll(data, old, now)); err != nil {
			return err
		}
	}
	return nil
}

// repointIndex gives the entries of changes (path to new blob) in the index
// of the repository at gitDir, whose work tree is dir, the new blob and the
// work tree file's stat data, and writes the index without extensions. A
// repository with no index has nothing to change.
func repointIndex(gitDir, dir string, changes map[string]plumbing.Hash) error {
	if !exists(filepath.Join(gitDir, "index")) {
		return nil
	}
	idx, _, err := readIndex(gitDir)
	if err != nil {
		return fmt.Errorf("read the index: %w", err)
	}
	for _, e := range idx.Entries {
		h, ok := changes[e.Name]
		if !ok || e.Stage != 0 {
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(e.Name)))
		if err != nil {
			return err
		}
		e.Hash, e.Size, e.ModifiedAt = h, uint32(info.Size()), info.ModTime()
		fillStat(e, info)
	}
	data, err := encodeIndex(idx)
	if err != nil {
		return fmt.Errorf("write the index: %w", err)
	}
	return replaceFile(filepath.Join(gitDir, "index"), data)
}

// pruneLoose removes the loose objects among stale that keep does not name
// from the git directory gitDir, and the object directories they leave
// empty, as git's own prune does.
func pruneLoose(gitDir string, stale []plumbing.Hash, keep map[plumbing.Hash]bool) error {
	for _, h := range stale {
		if keep[h] {
			continue
		}
		hex := h.String()
		dir := filepath.Join(gitDir, "objects", hex[:2])
		if err := os.Remove(filepath.Join(dir, hex[2:])); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // packed, or already gone
			}
			return err
		}
		os.Remove(dir) // fails while other objects are in it, which is fine
	}
	return nil
}

// replaceFile replaces the file at path with data through its lock file.
func replaceFile(path string, data []byte) error {
	l, err := lock(path)
	if errors.Is(err, ErrUseCLI) {
		return fmt.Errorf("%s is locked by another process", path)
	}
	if err != nil {
		return err
	}
	defer l.release()
	return l.commit(data)
}
