package gitx

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Memo is one command's memory of git's answers. A command that asks the same
// repository the same question again (its remote URL, its top level, its
// common directory, its author identity) gets the first answer back instead
// of starting git again. Each answer is git's own: a miss runs the call gitx
// runs without a memo, and a "no such remote" error is an answer too.
//
// A Memo belongs to one command and is never process-wide, so it holds
// nothing across commands. A nil *Memo is valid and asks git every time, so a
// caller with no command to speak of loses nothing.
//
// An entry goes when its answer may have changed: Forget(dir) drops a
// repository's entries (a command that adds, changes or removes a remote
// calls it), and a remote or identity entry also remembers the stat of the
// repository's config file, so a config another process rewrote is a miss.
// The top level and the common directory cannot change under a running
// command and keep no stat.
type Memo struct {
	mu    sync.Mutex
	repos map[string]*memoEntries
	asked atomic.Int64
}

// NewMemo returns an empty memo for one command.
func NewMemo() *Memo { return &Memo{repos: map[string]*memoEntries{}} }

type memoEntries struct {
	cfgPath  string
	cfgKnown bool
	remotes  map[string]*memoAnswer[string]
	top      *memoAnswer[string]
	common   *memoAnswer[string]
	ident    *memoAnswer[Identity]
}

// memoAnswer is one kept answer and the config stat it was read under.
type memoAnswer[T any] struct {
	val  T
	err  error
	stat cfgStat
}

// cfgStat is the size and modification time of a config file; a file that is
// not there is the zero value with exists false.
type cfgStat struct {
	exists bool
	size   int64
	mtime  time.Time
}

func statConfig(path string) cfgStat {
	info, err := os.Stat(path)
	if err != nil {
		return cfgStat{}
	}
	return cfgStat{exists: true, size: info.Size(), mtime: info.ModTime()}
}

func memoKey(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

// repo returns dir's entry set; the caller holds m.mu.
func (m *Memo) repo(dir string) *memoEntries {
	key := memoKey(dir)
	r := m.repos[key]
	if r == nil {
		r = &memoEntries{remotes: map[string]*memoAnswer[string]{}}
		m.repos[key] = r
	}
	return r
}

// config is the stat of dir's repository config file, found once per entry
// set; the caller holds m.mu. A repository whose common directory cannot be
// found has no file to watch and keeps its answers until Forget.
func (m *Memo) config(dir string, r *memoEntries) cfgStat {
	if !r.cfgKnown {
		r.cfgKnown = true
		if loc, ok := discover(dir, os.LookupEnv); ok {
			r.cfgPath = filepath.Join(loc.commonDir, "config")
		} else if common, err := CommonDir(dir); err == nil {
			r.cfgPath = filepath.Join(common, "config")
		}
	}
	if r.cfgPath == "" {
		return cfgStat{}
	}
	return statConfig(r.cfgPath)
}

// OriginURL is "git remote get-url remote" in dir.
func (m *Memo) OriginURL(dir, remote string) (string, error) {
	if m == nil {
		return Run(dir, "remote", "get-url", remote)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.repo(dir)
	now := m.config(dir, r)
	if a := r.remotes[remote]; a != nil && a.stat == now {
		return a.val, a.err
	}
	m.asked.Add(1)
	val, err := Run(dir, "remote", "get-url", remote)
	r.remotes[remote] = &memoAnswer[string]{val: val, err: err, stat: now}
	return val, err
}

// TopLevel is gitx.TopLevel(dir).
func (m *Memo) TopLevel(dir string) (string, error) {
	if m == nil {
		return TopLevel(dir)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.repo(dir)
	if r.top == nil {
		m.asked.Add(1)
		val, err := TopLevel(dir)
		r.top = &memoAnswer[string]{val: val, err: err}
	}
	return r.top.val, r.top.err
}

// CommonDir is gitx.CommonDir(dir).
func (m *Memo) CommonDir(dir string) (string, error) {
	if m == nil {
		return CommonDir(dir)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.repo(dir)
	if r.common == nil {
		m.asked.Add(1)
		val, err := CommonDir(dir)
		r.common = &memoAnswer[string]{val: val, err: err}
	}
	return r.common.val, r.common.err
}

// Identity is gitx.ResolveIdentity(dir): the name and email only, never the
// date "git var" also reports.
func (m *Memo) Identity(dir string) (Identity, error) {
	if m == nil {
		return ResolveIdentity(dir)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.repo(dir)
	now := m.config(dir, r)
	if a := r.ident; a != nil && a.stat == now {
		return a.val, a.err
	}
	m.asked.Add(1)
	val, err := ResolveIdentity(dir)
	if err != nil {
		// A failed lookup is not kept: the person may fix it and ask again.
		r.ident = nil
		return val, err
	}
	r.ident = &memoAnswer[Identity]{val: val, stat: now}
	return val, err
}

// Forget drops every entry the memo holds for dir.
func (m *Memo) Forget(dir string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.repos, memoKey(dir))
}

// Asked is how many questions the memo sent to git (a miss, whether git or a
// plain repository's files answered it). A nil memo asked none.
func (m *Memo) Asked() int {
	if m == nil {
		return 0
	}
	return int(m.asked.Load())
}
