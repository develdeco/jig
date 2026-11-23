package gitx

import (
	"bufio"
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
	// ancestry is where the answers about ancestry are kept, nil for none.
	ancestry *ancestryKept

	once sync.Once
	// tips maps a full ref name under refs/heads and refs/remotes to the commit
	// it names; ok says the listing was made. A ref that names no commit is
	// left out of tips and listed in notCommit, so it is asked of git.
	tips      map[string]string
	notCommit map[string]bool
	ok        bool
}

// BeginScope opens a read scope for each of dirs, keeping ancestry answers in
// the file at ancestryPath ("" keeps none). The returned function ends it, and
// writes the answers learned to the file. Scopes nest: a directory is open
// until every scope that opened it has ended.
func BeginScope(ancestryPath string, dirs ...string) (end func()) {
	var kept *ancestryKept
	if ancestryPath != "" {
		kept = ancestryFileAt(ancestryPath)
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
			s.ancestry = kept
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
			if kept != nil {
				kept.flush()
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

// kept is where the scope keeps ancestry answers, nil for none.
func (s *scopedRepo) kept() *ancestryKept {
	scopes.Lock()
	defer scopes.Unlock()
	return s.ancestry
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

// ancestryKept is a file of answers about ancestry: one line for each pair of
// commit ids, "<ancestor> <descendant> <1 or 0>". An answer never changes, so
// the file is only ever appended to; a line that is not whole is skipped.
type ancestryKept struct {
	path string

	mu       sync.Mutex
	loaded   bool
	known    map[string]bool
	pending  []string
	inflight map[string]*askedAncestry
}

// askedAncestry is a question being put to the program, so that the same pair
// asked at the same time by two readers is put once.
type askedAncestry struct {
	done chan struct{}
	is   bool
	err  error
}

// maxKeptAncestry is how many answers the file holds before it is cut back to
// its newest half, so a long-lived machine's file does not grow without bound.
const maxKeptAncestry = 60000

var ancestryFiles = struct {
	sync.Mutex
	by map[string]*ancestryKept
}{by: map[string]*ancestryKept{}}

// ancestryFileAt is the process's one handle on the file at path.
func ancestryFileAt(path string) *ancestryKept {
	ancestryFiles.Lock()
	defer ancestryFiles.Unlock()
	if k := ancestryFiles.by[path]; k != nil {
		return k
	}
	k := &ancestryKept{path: path}
	ancestryFiles.by[path] = k
	return k
}

func ancestryKey(a, d string) string { return a + " " + d }

// loadLocked reads the file once; the caller holds k.mu.
func (k *ancestryKept) loadLocked() {
	if k.loaded {
		return
	}
	k.loaded = true
	k.known = map[string]bool{}
	f, err := os.Open(k.path)
	if err != nil {
		return
	}
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	// Closed before the file is replaced: Windows does not rename over one
	// that is open.
	_ = f.Close()
	for _, l := range lines {
		a, rest, ok := strings.Cut(l, " ")
		d, v, ok2 := strings.Cut(rest, " ")
		if !ok || !ok2 || !isCommitID(a) || !isCommitID(d) || (v != "1" && v != "0") {
			continue
		}
		k.known[ancestryKey(a, d)] = v == "1"
	}
	if len(lines) > maxKeptAncestry {
		keep := lines[len(lines)-maxKeptAncestry/2:]
		if tmp, err := os.CreateTemp(filepath.Dir(k.path), "ancestry-*"); err == nil {
			_, werr := tmp.WriteString(strings.Join(keep, "\n") + "\n")
			cerr := tmp.Close()
			if werr == nil && cerr == nil && os.Rename(tmp.Name(), k.path) == nil {
				return
			}
			_ = os.Remove(tmp.Name())
		}
	}
}

// answer is whether a is an ancestor of d: the kept answer, or the one a
// reader asking the same pair just now is waiting for, else ask's, which is
// kept when it is an answer and not an error.
func (k *ancestryKept) answer(a, d string, ask func() (bool, error)) (bool, error) {
	key := ancestryKey(a, d)
	k.mu.Lock()
	k.loadLocked()
	if is, ok := k.known[key]; ok {
		k.mu.Unlock()
		return is, nil
	}
	if c, ok := k.inflight[key]; ok {
		k.mu.Unlock()
		<-c.done
		return c.is, c.err
	}
	c := &askedAncestry{done: make(chan struct{})}
	if k.inflight == nil {
		k.inflight = map[string]*askedAncestry{}
	}
	k.inflight[key] = c
	k.mu.Unlock()

	c.is, c.err = ask()

	k.mu.Lock()
	delete(k.inflight, key)
	if c.err == nil {
		k.known[key] = c.is
		v := "0"
		if c.is {
			v = "1"
		}
		k.pending = append(k.pending, key+" "+v)
	}
	k.mu.Unlock()
	close(c.done)
	return c.is, c.err
}

// flush appends the answers learned since the last flush. A file that cannot
// be written loses them: they are asked again.
func (k *ancestryKept) flush() {
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

// ancestryIDs are the two commit ids a question names, a branch taken from the
// scope's listing, and ok false when either is still not an id.
func ancestryIDs(dir, a, d string) (ia, id string, ok bool) {
	ia, id = a, d
	if !isCommitID(ia) {
		sha, found, answered := listedCommit(dir, ia)
		if !answered || !found {
			return "", "", false
		}
		ia = sha
	}
	if !isCommitID(id) {
		sha, found, answered := listedCommit(dir, id)
		if !answered || !found {
			return "", "", false
		}
		id = sha
	}
	return ia, id, isCommitID(ia) && isCommitID(id)
}
