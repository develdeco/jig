package gitx

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

// maxPackIndexBytes is the total size of a repository's pack index files
// above which the in-process object reads leave it to the git program:
// go-git loads a pack index whole, and a big repository must not pay that
// for every question.
const maxPackIndexBytes = 16 << 20

// maxIndexBytes is the size of an index above which the in-process reads
// leave the unmerged-entries question to the git program: go-git decodes the
// whole file, which for a big work tree costs more than git reading it.
const maxIndexBytes = 2 << 20

// maxObjectBytes is the size above which the in-process reads do not load a
// commit, tag or tree.
const maxObjectBytes = 8 << 20

// maxPeel bounds how many tags the in-process reads follow.
const maxPeel = 32

// objState is how an object lookup ended.
type objState int

const (
	// objFound: the answer is there, and final.
	objFound objState = iota
	// objAbsent: the object is not there, or is not what was asked for. The
	// caller confirms this with the git program: a repack running at the
	// time can hide an object from a read git itself would retry.
	objAbsent
	// objNoEntry: every tree on the way was read, and none has the path
	// asked for. Final, as a positive answer is.
	objNoEntry
	// objUnsure: the in-process reads cannot say.
	objUnsure
)

// objectStore is the part of go-git's object storage the reads use; a test
// wraps it to hide an object, as a repack running at the time can.
type objectStore interface {
	HasEncodedObject(plumbing.Hash) error
	EncodedObject(plumbing.ObjectType, plumbing.Hash) (plumbing.EncodedObject, error)
	Close() error
}

// objectDB is the object store of one repository, opened for a few reads.
type objectDB struct {
	st objectStore
}

// openObjects opens the repository's object store for in-process reads, or
// reports false when the repository has something that changes what git
// reads from it: alternates, grafts, a shallow boundary, replace refs, pack
// indexes of more than maxPackIndexBytes, or an object directory that cannot
// be listed.
func (l *repoLoc) openObjects() (*objectDB, bool) {
	for _, name := range []string{"objects/info/alternates", "info/grafts", "shallow"} {
		if _, err := os.Lstat(filepath.Join(l.commonDir, filepath.FromSlash(name))); err == nil || !isAbsent(err) {
			return nil, false
		}
	}
	if present, known := l.replaceRefs(); present || !known {
		return nil, false
	}
	ents, err := os.ReadDir(filepath.Join(l.commonDir, "objects", "pack"))
	if err != nil && !isAbsent(err) {
		return nil, false
	}
	var idxBytes int64
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".idx") {
			info, err := e.Info()
			if err != nil {
				return nil, false
			}
			idxBytes += info.Size()
		}
	}
	if idxBytes > maxPackIndexBytes {
		return nil, false
	}
	st := filesystem.NewStorageWithOptions(plainFS{root: l.commonDir}, cache.NewObjectLRU(4*cache.MiByte),
		filesystem.Options{LargeObjectThreshold: maxObjectBytes})
	return &objectDB{st: st}, true
}

func (db *objectDB) close() {
	if db != nil && db.st != nil {
		db.st.Close()
	}
}

// has reports whether the object is there.
func (db *objectDB) has(hex string) (bool, objState) {
	err := db.st.HasEncodedObject(plumbing.NewHash(hex))
	switch {
	case err == nil:
		return true, objFound
	case errors.Is(err, plumbing.ErrObjectNotFound):
		return false, objAbsent
	}
	return false, objUnsure
}

// storedObject is a commit, tag or tree as stored.
type storedObject struct {
	kind plumbing.ObjectType
	body []byte
}

// load reads the object. Blobs and objects over maxObjectBytes are not
// loaded; their kind is returned with no body.
func (db *objectDB) load(hex string) (storedObject, objState) {
	obj, err := db.st.EncodedObject(plumbing.AnyObject, plumbing.NewHash(hex))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return storedObject{}, objAbsent
		}
		return storedObject{}, objUnsure
	}
	switch obj.Type() {
	case plumbing.CommitObject, plumbing.TagObject, plumbing.TreeObject:
	case plumbing.BlobObject:
		return storedObject{kind: plumbing.BlobObject}, objFound
	default:
		return storedObject{}, objUnsure
	}
	if obj.Size() > maxObjectBytes {
		return storedObject{}, objUnsure
	}
	r, err := obj.Reader()
	if err != nil {
		return storedObject{}, objUnsure
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil || int64(len(body)) != obj.Size() {
		return storedObject{}, objUnsure
	}
	return storedObject{kind: obj.Type(), body: body}, objFound
}

// headerValue returns the value of the first header line "<key> <value>" of
// a commit or tag body.
func headerValue(body []byte, key string) (string, bool) {
	prefix := []byte(key + " ")
	for len(body) > 0 {
		line, rest, _ := bytes.Cut(body, []byte("\n"))
		if len(line) == 0 {
			return "", false
		}
		if bytes.HasPrefix(line, prefix) {
			return string(line[len(prefix):]), true
		}
		body = rest
	}
	return "", false
}

// commitParts are what the in-process reads need of a commit.
type commitParts struct {
	tree      string
	parent    string // the first parent, or ""
	encoding  bool   // the commit names a message encoding
	message   []byte
	malformed bool
}

func parseCommit(body []byte) commitParts {
	var c commitParts
	head, msg, found := bytes.Cut(body, []byte("\n\n"))
	if !found {
		head, msg = body, nil
	}
	c.message = msg
	sawTree := false
	for _, line := range bytes.Split(head, []byte("\n")) {
		switch {
		case bytes.HasPrefix(line, []byte("tree ")):
			if !sawTree {
				c.tree = string(line[5:])
				sawTree = true
			}
		case bytes.HasPrefix(line, []byte("parent ")):
			if c.parent == "" {
				c.parent = string(line[7:])
			}
		case bytes.HasPrefix(line, []byte("encoding ")):
			c.encoding = true
		}
	}
	if !isHex40(c.tree) || (c.parent != "" && !isHex40(c.parent)) {
		c.malformed = true
	}
	return c
}

// peelTo follows tags from the object hex until it is a commit (want
// "commit") or a tree (want "tree", where a commit gives its tree), and
// returns that object's name. An object of another kind is objAbsent: it is
// not what was asked for.
func (db *objectDB) peelTo(hex, want string) (string, objState) {
	for i := 0; i < maxPeel; i++ {
		o, st := db.load(hex)
		if st != objFound {
			return "", st
		}
		switch o.kind {
		case plumbing.TagObject:
			next, ok := headerValue(o.body, "object")
			if !ok || !isHex40(next) {
				return "", objUnsure
			}
			hex = strings.ToLower(next)
		case plumbing.CommitObject:
			if want == "commit" {
				return hex, objFound
			}
			c := parseCommit(o.body)
			if c.malformed {
				return "", objUnsure
			}
			// git parses the tree it peels to, so a commit whose tree is
			// missing has no ^{tree}.
			tree := strings.ToLower(c.tree)
			if has, st := db.has(tree); !has {
				return "", st
			}
			return tree, objFound
		case plumbing.TreeObject:
			if want == "tree" {
				return hex, objFound
			}
			return "", objAbsent
		default:
			return "", objAbsent
		}
	}
	return "", objUnsure
}

// parentOf returns the first parent of the commit hex names (tags peeled).
func (db *objectDB) parentOf(hex string) (string, objState) {
	commit, st := db.peelTo(hex, "commit")
	if st != objFound {
		return "", st
	}
	o, st := db.load(commit)
	if st != objFound || o.kind != plumbing.CommitObject {
		return "", objUnsure
	}
	c := parseCommit(o.body)
	switch {
	case c.malformed:
		return "", objUnsure
	case c.parent == "":
		return "", objAbsent
	}
	return strings.ToLower(c.parent), objFound
}

// treeEntry is one entry of a tree.
type treeEntry struct {
	mode uint32
	oid  string
}

// entryType is the name ls-tree gives an entry's object type.
func (e treeEntry) entryType() string {
	switch {
	case e.mode == 0o040000:
		return "tree"
	case e.mode == 0o160000:
		return "commit"
	}
	return "blob"
}

// lookupEntry finds the entry called name in the tree body: the entry, and
// whether it is there; the last result is false for a body that is not a
// tree.
func lookupEntry(body []byte, name string) (treeEntry, bool, bool) {
	for len(body) > 0 {
		sp := bytes.IndexByte(body, ' ')
		if sp < 0 {
			return treeEntry{}, false, false
		}
		mode, err := strconv.ParseUint(string(body[:sp]), 8, 32)
		if err != nil {
			return treeEntry{}, false, false
		}
		rest := body[sp+1:]
		nul := bytes.IndexByte(rest, 0)
		if nul < 0 || len(rest) < nul+1+20 {
			return treeEntry{}, false, false
		}
		entryName := rest[:nul]
		raw := rest[nul+1 : nul+1+20]
		body = rest[nul+1+20:]
		if string(entryName) == name {
			var h plumbing.Hash
			copy(h[:], raw)
			return treeEntry{mode: uint32(mode), oid: h.String()}, true, true
		}
	}
	return treeEntry{}, false, true
}

// entryAt finds the entry at path (slash separated, no empty, "." or ".."
// part) below the tree hex names. A path with no such entry is objAbsent.
func (db *objectDB) entryAt(treeHex, path string) (treeEntry, objState) {
	parts := strings.Split(path, "/")
	cur := treeHex
	for i, part := range parts {
		o, st := db.load(cur)
		if st != objFound {
			return treeEntry{}, st
		}
		if o.kind != plumbing.TreeObject {
			return treeEntry{}, objAbsent
		}
		e, found, ok := lookupEntry(o.body, part)
		if !ok {
			return treeEntry{}, objUnsure
		}
		if !found {
			return treeEntry{}, objNoEntry
		}
		if i == len(parts)-1 {
			return e, objFound
		}
		if e.mode != 0o040000 {
			return treeEntry{}, objNoEntry
		}
		cur = e.oid
	}
	return treeEntry{}, objUnsure
}

// cleanTreePath reports whether path is a plain repository-relative path:
// parts separated by single slashes, none empty, "." or "..", and none
// holding a backslash or a NUL.
func cleanTreePath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// subjectOf is what `git log -1 --format=%s` prints for a commit body: the
// first paragraph of the message with its lines trimmed on the right and
// joined by one space, found after the blank lines that may open the
// message. A commit that names an encoding, whose message git would convert,
// and a subject that is not ASCII, which git outputs in the encoding the
// machine's config names, are objUnsure.
func subjectOf(body []byte) (string, objState) {
	c := parseCommit(body)
	if c.malformed || c.encoding || bytes.IndexByte(c.message, 0) >= 0 {
		return "", objUnsure
	}
	lines := strings.Split(string(c.message), "\n")
	i := 0
	for i < len(lines) && isBlankLine(lines[i]) {
		i++
	}
	var parts []string
	for ; i < len(lines); i++ {
		if isBlankLine(lines[i]) {
			break
		}
		parts = append(parts, strings.TrimRight(lines[i], gitSpace))
	}
	subject := strings.Join(parts, " ")
	for i := 0; i < len(subject); i++ {
		if subject[i] >= 0x80 {
			return "", objUnsure
		}
	}
	return subject, objFound
}

// gitSpace is what git's own isspace counts as white space: not the vertical
// tab and form feed C's does.
const gitSpace = " \t\n\r"

func isBlankLine(s string) bool { return strings.Trim(s, gitSpace) == "" }

// hasUnmerged reports whether the worktree's index has an unmerged entry
// (stage 1 to 3), as `git ls-files --unmerged` would list one. An index the
// decoder cannot read (an extension that must be understood, such as a split
// index, or a bad checksum) is not answered.
func (l *repoLoc) hasUnmerged() (unmerged, ok bool) {
	if matches, _ := filepath.Glob(filepath.Join(l.gitDir, "sharedindex.*")); len(matches) > 0 {
		return false, false
	}
	f, err := os.Open(filepath.Join(l.gitDir, "index"))
	if err != nil {
		return false, isAbsent(err)
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() > maxIndexBytes {
		return false, false
	}
	idx := &index.Index{}
	if err := index.NewDecoder(f).Decode(idx); err != nil {
		return false, false
	}
	for _, e := range idx.Entries {
		if e.Stage != 0 {
			return true, true
		}
	}
	return false, true
}
