package gitx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Reading a whole store of tickets asks git the same two questions about every
// branch: where does this ref point, and is this commit in that one's history.
// Asked one at a time they cost a process each, so the cost of a listing grew
// with its tickets and branches. A read scope (BeginScope) answers both from
// what is read once for each repo:
//
//   - a branch's tip is a lookup in one listing of the repo's branches, made by
//     one for-each-ref the first time the scope is asked about the repo;
//   - whether one commit is an ancestor of another is a fact for ever, since a
//     commit id names its whole history, so an answer is kept in a file the
//     caller names and asked of git once, by this process or any other.
//
// A scope is for a command that only reads: the listing stands for as long as
// the scope does, whatever the repo does meanwhile, which is the command's own
// run. It is keyed by the directory asked about, so a repo outside the scope,
// and a test running in another directory, is answered as it always was.

// scopes are the directories read scopes are open for, keyed as memoKey.
var scopes = struct {
	sync.Mutex
	by map[string]*scopedRepo
}{by: map[string]*scopedRepo{}}

// scopedRepo is one directory's open scope.
type scopedRepo struct {
	open int
	// ancestry and overlaps are where facts about commits are kept, nil for
	// none: whether one commit is in another's history, and the files a branch
	// changes and whether two branches merge. They are files of their own, so
	// a read that asks only the first reads none of the second.
	ancestry, overlaps *Kept

	once sync.Once
	// tips maps a full ref name under refs/heads and refs/remotes to the commit
	// it names; ok says the listing was made. A ref that names no commit is
	// left out of tips and listed in notCommit, so it is asked of git.
	tips      map[string]string
	notCommit map[string]bool
	ok        bool
}

// BeginScope opens a read scope for each of dirs, keeping facts about commits in
// the files at ancestryPath and overlapsPath ("" keeps none of that kind). The
// returned function ends it, and writes the facts learned to the files. Scopes
// nest: a directory is open until every scope that opened it has ended.
func BeginScope(ancestryPath, overlapsPath string, dirs ...string) (end func()) {
	var ancestry, overlaps *Kept
	if ancestryPath != "" {
		ancestry = keptFileAt(ancestryPath, validAncestry, maxAncestryBytes)
	}
	if overlapsPath != "" {
		overlaps = keptFileAt(overlapsPath, validOverlap, maxOverlapBytes)
	}
	keys := make([]string, 0, len(dirs))
	scopes.Lock()
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		key := memoKey(dir)
		s := scopes.by[key]
		if s == nil {
			s = &scopedRepo{}
			scopes.by[key] = s
		}
		s.open++
		if s.ancestry == nil {
			s.ancestry = ancestry
		}
		if s.overlaps == nil {
			s.overlaps = overlaps
		}
		keys = append(keys, key)
	}
	scopes.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			scopes.Lock()
			for _, key := range keys {
				if s := scopes.by[key]; s != nil {
					s.open--
					if s.open <= 0 {
						delete(scopes.by, key)
					}
				}
			}
			scopes.Unlock()
			if ancestry != nil {
				ancestry.flush()
			}
			if overlaps != nil {
				overlaps.flush()
			}
		})
	}
}

// scopeOf is dir's open scope, nil for none.
func scopeOf(dir string) *scopedRepo {
	scopes.Lock()
	defer scopes.Unlock()
	return scopes.by[memoKey(dir)]
}

// keptAncestry and keptOverlaps are where the scope keeps those facts, nil for
// none.
func (s *scopedRepo) keptAncestry() *Kept {
	scopes.Lock()
	defer scopes.Unlock()
	return s.ancestry
}

func (s *scopedRepo) keptOverlaps() *Kept {
	scopes.Lock()
	defer scopes.Unlock()
	return s.overlaps
}

// load lists the repo's branches once.
func (s *scopedRepo) load(dir string) {
	s.once.Do(func() {
		out, err := Run(dir, "for-each-ref", "--format=%(objectname) %(objecttype) %(refname)", "refs/heads", "refs/remotes")
		if err != nil {
			return
		}
		tips := map[string]string{}
		notCommit := map[string]bool{}
		sc := bufio.NewScanner(strings.NewReader(out))
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			oid, rest, ok := strings.Cut(sc.Text(), " ")
			kind, name, ok2 := strings.Cut(rest, " ")
			if !ok || !ok2 || name == "" {
				continue
			}
			if kind == "commit" {
				tips[name] = oid
			} else {
				notCommit[name] = true
			}
		}
		s.tips, s.notCommit, s.ok = tips, notCommit, true
	})
}

// listedCommit answers rev from dir's listing when a scope is open for it and
// rev is a full branch name (refs/heads/x or refs/remotes/o/x), with or
// without ^{commit}: the commit and true, "" and false for a branch that is not
// there. answered is false when the scope cannot say, and the caller asks git.
func listedCommit(dir, rev string) (sha string, found, answered bool) {
	s := scopeOf(dir)
	if s == nil {
		return "", false, false
	}
	name := strings.TrimSuffix(rev, "^{commit}")
	if !strings.HasPrefix(name, "refs/heads/") && !strings.HasPrefix(name, "refs/remotes/") {
		return "", false, false
	}
	if strings.ContainsAny(name, "^~:@{}\\ \t*?[") || strings.Contains(name, "..") {
		return "", false, false
	}
	s.load(dir)
	if !s.ok || s.notCommit[name] {
		return "", false, false
	}
	oid, ok := s.tips[name]
	return oid, ok, true
}

// Kept is a file of facts about commit ids kept on this machine: a line for
// each, "<key> TAB <answer>", where the key names the question and the ids it is
// about. A commit id names its whole history, so what is asked of ids never
// changes: the file is only ever added to, a fact is asked of git once, by this
// process or any other, and a line that does not parse whole (a write cut short,
// or joined with another's append) is no fact. The file is bounded by bytes and
// cut back to its newest facts, so reading it costs what a roster asks, never
// how long the machine has been used.
type Kept struct {
	path     string
	valid    func(key, answer string) bool
	maxBytes int

	mu       sync.Mutex
	loaded   bool
	known    map[string]string
	pending  []string
	inflight map[string]*askedFact
}

// askedFact is a question being put to the program, so that the same one asked
// at the same time by two readers is put once.
type askedFact struct {
	done chan struct{}
	out  string
	err  error
}

// The bounds, in bytes, of the two files: an ancestry fact is about 90 bytes, an
// overlap fact a few kilobytes.
const (
	maxAncestryBytes = 1 << 20
	maxOverlapBytes  = 4 << 20
)

var keptFiles = struct {
	sync.Mutex
	by map[string]*Kept
}{by: map[string]*Kept{}}

// keptFileAt is the process's one handle on the file at path.
func keptFileAt(path string, valid func(key, answer string) bool, maxBytes int) *Kept {
	keptFiles.Lock()
	defer keptFiles.Unlock()
	if k := keptFiles.by[path]; k != nil {
		return k
	}
	k := &Kept{path: path, valid: valid, maxBytes: maxBytes}
	keptFiles.by[path] = k
	return k
}

// KeptOverlaps is the overlap facts kept for dir by the scope open for it, nil
// when none is.
func KeptOverlaps(dir string) *Kept {
	if s := scopeOf(dir); s != nil {
		return s.keptOverlaps()
	}
	return nil
}

// validAncestry says a line is whole: "ancestor <id> <id>" and 0 or 1.
func validAncestry(key, answer string) bool {
	f := strings.Split(key, " ")
	return len(f) == 3 && f[0] == "ancestor" && isCommitID(f[1]) && isCommitID(f[2]) && (answer == "0" || answer == "1")
}

// validOverlap says a line is whole: "diff <id> <id> <filter>" or "merge <id>
// <id>", and a JSON answer.
func validOverlap(key, answer string) bool {
	f := strings.Split(key, " ")
	switch {
	case len(f) == 4 && f[0] == "diff" && isCommitID(f[1]) && isCommitID(f[2]) && f[3] != "":
	case len(f) == 3 && f[0] == "merge" && isCommitID(f[1]) && isCommitID(f[2]):
	default:
		return false
	}
	return json.Valid([]byte(answer))
}

// loadLocked reads the file once; the caller holds k.mu.
func (k *Kept) loadLocked() {
	if k.loaded {
		return
	}
	k.loaded = true
	k.known = map[string]string{}
	data, err := os.ReadFile(k.path)
	if err != nil {
		return
	}
	cut := false
	if len(data) > k.maxBytes {
		// Only the newest facts are kept: from a line start past the half.
		data = data[len(data)-k.maxBytes/2:]
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
		cut = true
	}
	var kept []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		key, answer, ok := strings.Cut(line, "\t")
		if !ok || !k.valid(key, answer) {
			continue
		}
		k.known[key] = answer
		kept = append(kept, line)
	}
	if cut {
		if tmp, err := os.CreateTemp(filepath.Dir(k.path), "kept-*"); err == nil {
			_, werr := tmp.WriteString(strings.Join(kept, "\n") + "\n")
			cerr := tmp.Close()
			if werr == nil && cerr == nil && os.Rename(tmp.Name(), k.path) == nil {
				return
			}
			_ = os.Remove(tmp.Name())
		}
	}
}

// Answer is the kept answer to the question key, or the one a reader asking the
// same just now is waiting for, else ask's, which is kept when it is an answer
// and not an error. key and the answer hold no tab or line break.
func (k *Kept) Answer(key string, ask func() (string, error)) (string, error) {
	k.mu.Lock()
	k.loadLocked()
	if out, ok := k.known[key]; ok {
		k.mu.Unlock()
		return out, nil
	}
	if c, ok := k.inflight[key]; ok {
		k.mu.Unlock()
		<-c.done
		return c.out, c.err
	}
	c := &askedFact{done: make(chan struct{})}
	if k.inflight == nil {
		k.inflight = map[string]*askedFact{}
	}
	k.inflight[key] = c
	k.mu.Unlock()

	c.out, c.err = ask()

	k.mu.Lock()
	delete(k.inflight, key)
	if c.err == nil && k.valid(key, c.out) {
		k.known[key] = c.out
		k.pending = append(k.pending, key+"\t"+c.out)
	}
	k.mu.Unlock()
	close(c.done)
	return c.out, c.err
}

// flush appends the facts learned since the last flush, each line whole and
// ended, in one write. A file that cannot be written loses them: they are asked
// again.
func (k *Kept) flush() {
	k.mu.Lock()
	pending := k.pending
	k.pending = nil
	k.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(k.path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(k.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(strings.Join(pending, "\n") + "\n")
}

// isCommitID reports whether s is a full object name, SHA-1 or SHA-256.
func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// commitIDs are the commit ids the refs name: an id as it is, a full branch
// name as the scope's listing has it; ok is false when any is neither.
func commitIDs(dir string, refs ...string) (ids []string, ok bool) {
	ids = make([]string, len(refs))
	for i, ref := range refs {
		if isCommitID(ref) {
			ids[i] = ref
			continue
		}
		sha, found, answered := listedCommit(dir, ref)
		if !answered || !found || !isCommitID(sha) {
			return nil, false
		}
		ids[i] = sha
	}
	return ids, true
}
