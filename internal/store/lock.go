package store

import (
	"fmt"
	"os"
	"time"

	"github.com/develdeco/jig/internal/filelock"
)

// Lock acquires an advisory, exclusive lock on path's sidecar file
// ("<path>.lock", created if absent and never removed). Only writers lock;
// readers never do. It retries every 50ms until timeout elapses; on timeout
// it returns held=false so the caller can proceed anyway rather than block a
// finished write on a stale holder, after telling the operator on stderr
// that it is.
func Lock(path string, timeout time.Duration) (release func(), held bool, err error) {
	release, held, err = TryLock(path, timeout)
	if err == nil && !held {
		warnLockTimeout(path, timeout)
	}
	return release, held, err
}

// TryLock is Lock without the warning: on timeout it returns held=false and
// says nothing, for a caller that does not proceed unlocked and so has its
// own word for it (the machine ledger refuses, the checkpoint lock warns in
// terms of what it guards). It is filelock.TryLock, which gitx shares.
func TryLock(path string, timeout time.Duration) (release func(), held bool, err error) {
	return filelock.TryLock(path, timeout)
}

// warnLockTimeout tells the operator, out loud, that a write is proceeding
// without the lock: every caller of Lock is a writer (readers never lock),
// so a timeout here means the write below runs unsynchronized. Centralizing
// this in Lock itself means every locked-write helper across the codebase
// gets the warning for free.
func warnLockTimeout(path string, timeout time.Duration) {
	fmt.Fprintf(os.Stderr, "jig: proceeding without lock on %s (timeout after %s)\n", path, timeout)
}
