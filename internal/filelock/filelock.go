// Package filelock is the cross-process lock jig's packages share: an
// advisory, exclusive lock on a sidecar file, which the operating system lets
// go of when its holder exits, however it exits. The store's locks are built
// on it, and so is gitx's lock on a repository's worktrees, which the store
// cannot provide because the store imports gitx.
package filelock

import (
	"os"
	"path/filepath"
	"time"
)

const retryInterval = 50 * time.Millisecond

// TryLock acquires an advisory, exclusive lock on path's sidecar file
// ("<path>.lock", created if absent and never removed). It retries every 50ms
// until timeout elapses; on timeout it returns held=false and a nil error,
// and the caller decides what that means: whether to proceed unlocked, and in
// what words to say so.
func TryLock(path string, timeout time.Duration) (release func(), held bool, err error) {
	release = func() {}
	lockPath := path + ".lock"
	if dir := filepath.Dir(lockPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return release, false, err
		}
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return release, false, err
	}

	deadline := time.Now().Add(timeout)
	for {
		ok, lerr := tryLockFile(f)
		if lerr != nil {
			f.Close()
			return release, false, lerr
		}
		if ok {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, true, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return release, false, nil
		}
		time.Sleep(retryInterval)
	}
}
