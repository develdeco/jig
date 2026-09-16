package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const lockRetryInterval = 50 * time.Millisecond

// Lock acquires an advisory, exclusive lock on path's sidecar file
// ("<path>.lock", created if absent and never removed). Only writers lock;
// readers never do. It retries every 50ms until timeout elapses; on timeout
// it returns held=false so the caller can proceed anyway rather than block a
// finished write on a stale holder.
func Lock(path string, timeout time.Duration) (release func(), held bool, err error) {
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
			warnLockTimeout(path, timeout)
			return release, false, nil
		}
		time.Sleep(lockRetryInterval)
	}
}

// warnLockTimeout tells the operator, out loud, that a write is proceeding
// without the lock: every caller of Lock is a writer (readers never lock),
// so a timeout here means the write below runs unsynchronized. Centralizing
// this in Lock itself means every locked-write helper across the codebase
// gets the warning for free.
func warnLockTimeout(path string, timeout time.Duration) {
	fmt.Fprintf(os.Stderr, "jig: proceeding without lock on %s (timeout after %s)\n", path, timeout)
}
