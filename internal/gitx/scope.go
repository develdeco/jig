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
	// facts is where the facts about commits are kept, nil for none.
	facts *Kept

	once sync.Once
	// tips maps a full ref name under refs/heads and refs/remotes to the commit
	// it names; ok says the listing was made. A ref that names no commit is
	// left out of tips and listed in notCommit, so it is asked of git.
	tips      map[string]string
	notCommit map[string]bool
	ok        bool
}

// BeginScope opens a read scope for each of dirs, keeping facts about commits in
// the file at keptPath ("" keeps none). The returned function ends it, and
// writes the answers learned to the file. Scopes nest: a directory is open
// until every scope that opened it has ended.
func BeginScope(keptPath string, dirs ...string) (end func()) {
	var kept *Kept
	if keptPath != "" {
		kept = keptFileAt(keptPath)
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
		if s.facts == nil {
			s.facts = kept
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

// kept is where the scope keeps facts about commits, nil for none.
func (s *scopedRepo) kept() *Kept {
	scopes.Lock()
	defer scopes.Unlock()
	return s.facts
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
// about (whether one commit is in another's history, which files a branch
// changes past a base, whether two branches merge). A commit id names its whole
// history, so what is asked of ids never changes: the file is only ever added
// to, a line that is not whole is skipped, and a fact is asked of git once, by
// this process or any other.
type Kept struct {
	path string

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

// maxKeptFacts is how many facts the file holds before it is cut back to its
// newest half, so a long-lived machine's file does not grow without bound.
const maxKeptFacts = 60000

var keptFiles = struct {
	sync.Mutex
	by map[string]*Kept
}{by: map[string]*Kept{}}

// keptFileAt is the process's one handle on the file at path.
func keptFileAt(path string) *Kept {
	keptFiles.Lock()
	defer keptFiles.Unlock()
	if k := keptFiles.by[path]; k != nil {
		return k
	}
	k := &Kept{path: path}
	keptFiles.by[path] = k
	return k
}

// KeptFor is the facts kept for dir by the scope open for it, nil when none is.
func KeptFor(dir string) *Kept {
	if s := scopeOf(dir); s != nil {
		return s.kept()
	}
	return nil
}

// CommitOf is the commit the full branch name ref (refs/heads/x or
// refs/remotes/o/x) names in dir, from the scope's listing; false when no scope
// is open for dir or it cannot say.
func CommitOf(dir, ref string) (string, bool) {
	sha, found, answered := listedCommit(dir, ref)
	return sha, answered && found && isCommitID(sha)
}

// loadLocked reads the file once; the caller holds k.mu.
func (k *Kept) loadLocked() {
	if k.loaded {
		return
	}
	k.loaded = true
	k.known = map[string]string{}
	f, err := os.Open(k.path)
	if err != nil {
		return
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	// Closed before the file is replaced: Windows does not rename over one
	// that is open.
	_ = f.Close()
	for _, l := range lines {
		key, answer, ok := strings.Cut(l, "\t")
		if !ok || key == "" {
			continue
		}
		k.known[key] = answer
	}
	if len(lines) > maxKeptFacts {
		keep := lines[len(lines)-maxKeptFacts/2:]
		if tmp, err := os.CreateTemp(filepath.Dir(k.path), "kept-*"); err == nil {
			_, werr := tmp.WriteString(strings.Join(keep, "\n") + "\n")
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
	if c.err == nil {
		k.known[key] = c.out
		k.pending = append(k.pending, key+"\t"+c.out)
	}
	k.mu.Unlock()
	close(c.done)
	return c.out, c.err
}

// flush appends the facts learned since the last flush. A file that cannot be
// written loses them: they are asked again.
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
