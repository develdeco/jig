package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/develdeco/jig/internal/gitx"
)

// checkpointLockTimeout is how long LockCheckpoint waits for another process
// to finish its checkpoint before it proceeds without the lock: two minutes
// covers a push over a slow network and still ends if the holder died with
// the lock's file handle somehow kept open.
const checkpointLockTimeout = 2 * time.Minute

// checkpointLockName is the file in the store's git common directory the
// checkpoint lock is held on (Lock adds its ".lock" sidecar suffix). It is in
// git's own directory so it is never part of the store's tree and is shared
// by every linked worktree of one clone.
const checkpointLockName = "jig-checkpoint"

// LockCheckpoint takes the store's checkpoint lock: the cross-process lock
// that keeps one process's git work on this store clone (a Sync, a Push, a
// Claim) from running into another's, and a journal append from landing in
// the middle of either. It matters now that a detached verify worker, a hook
// and a foreground command can all write to one clone at once.
//
// The lock is a file lock on <git common dir>/jig-checkpoint.lock, so every
// process that has the clone open, and every linked worktree of it, contends
// for the same one. It waits up to two minutes; on timeout it proceeds
// without the lock and says so through Warn (or, when none is set, on
// standard error), because a checkpoint must not hang on a holder that is
// stuck. A store that is not a git clone has no common directory and so no
// file to lock: its lock is the in-process one alone.
//
// The lock is re-entrant per Root within a process: a caller that already
// holds it (a Push that nests a Claim, a journal append inside a command that
// took the lock around a whole step) takes a further hold without waiting.
// Holds count, and the file lock is let go when the last one is released.
// Goroutines of one process share a hold in the same way, as they always
// shared the store; the lock orders processes, not goroutines.
//
// Push lets go of its hold before RunCheckpointHook, since the hook's mirror
// reaches the store through Claim, which takes the lock itself. The release
// func is idempotent, so a caller that releases early can still defer it.
func (s *Store) LockCheckpoint() (release func(), err error) {
	return s.lockCheckpoint(checkpointLockTimeout)
}

// lockCheckpoint is LockCheckpoint with the wait given, so a test can see
// the timeout without waiting two minutes.
func (s *Store) lockCheckpoint(timeout time.Duration) (func(), error) {
	key, lockPath := s.checkpointLockTarget()
	h := checkpointHoldFor(key)

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.depth == 0 {
		h.unlock = nil
		if lockPath != "" {
			unlock, held, err := TryLock(lockPath, timeout)
			if err != nil {
				return func() {}, fmt.Errorf("store: lock %s: %w", lockPath, err)
			}
			if held {
				h.unlock = unlock
			} else {
				s.warn("proceeding without the checkpoint lock on %s (timeout after %s)", lockPath+".lock", timeout)
			}
		}
	}
	h.depth++

	var once sync.Once
	return func() { once.Do(h.release) }, nil
}

// warn reports a problem that does not stop the command: through Warn when
// set, else as "warning: ..." on standard error.
func (s *Store) warn(format string, args ...any) {
	if s.Warn != nil {
		s.Warn(format, args...)
		return
	}
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

// checkpointHold is the process's hold on one clone's checkpoint lock: how
// many callers hold it, and what lets the file lock go when the last one
// does.
type checkpointHold struct {
	mu     sync.Mutex
	depth  int
	unlock func()
}

func (h *checkpointHold) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.depth == 0 {
		return
	}
	h.depth--
	if h.depth == 0 && h.unlock != nil {
		h.unlock()
		h.unlock = nil
	}
}

var (
	checkpointHoldsMu sync.Mutex
	checkpointHolds   = map[string]*checkpointHold{}
)

// checkpointHoldFor returns the process's one hold record for key.
func checkpointHoldFor(key string) *checkpointHold {
	checkpointHoldsMu.Lock()
	defer checkpointHoldsMu.Unlock()
	h := checkpointHolds[key]
	if h == nil {
		h = &checkpointHold{}
		checkpointHolds[key] = h
	}
	return h
}

// checkpointLockTarget names what this store's checkpoint lock is held on:
// key identifies the clone within the process (the lock file's path, so two
// spellings of one clone share a hold), and lockPath is the file to lock,
// "" for a store that is not a git clone.
func (s *Store) checkpointLockTarget() (key, lockPath string) {
	dir, ok := s.gitCommonDir()
	if !ok {
		abs, err := filepath.Abs(s.Root)
		if err != nil {
			abs = s.Root
		}
		return "root:" + abs, ""
	}
	path := filepath.Join(dir, checkpointLockName)
	return path, path
}

// commonDirCache remembers, per Root, the git common directory gitCommonDir
// had to ask git for (a linked worktree's, where .git is a file).
var commonDirCache sync.Map // Root -> common dir

// gitCommonDir returns the git common directory of the clone at s.Root. A
// store is its clone's top level, so <Root>/.git is the common directory when
// it is a directory, which costs a stat; when it is a file (a linked
// worktree or a submodule) git is asked, once per Root. ok is false when
// Root has no .git at all.
func (s *Store) gitCommonDir() (string, bool) {
	dotGit := filepath.Join(s.Root, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return canonicalDir(dotGit), true
	}
	if v, ok := commonDirCache.Load(s.Root); ok {
		if dir := v.(string); dirExists(dir) {
			return dir, true
		}
	}
	dir, err := gitx.CommonDir(s.Root)
	if err != nil {
		return "", false
	}
	commonDirCache.Store(s.Root, dir)
	return dir, true
}

// canonicalDir returns path absolute and with symlinks resolved, so two
// spellings of one directory name one lock; path itself when it cannot be
// resolved.
func canonicalDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
