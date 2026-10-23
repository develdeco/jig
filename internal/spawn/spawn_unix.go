//go:build !windows

package spawn

import "syscall"

// detachAttr puts the child in a session of its own (setsid), so it has no
// controlling terminal and a hangup on the caller's does not reach it. There
// is only the one way to detach here: attempt is always 0.
func detachAttr(attempt int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// retryDetach reports whether a failed start is worth another attempt with a
// weaker detach. On Unix there is none.
func retryDetach(err error, attempt int) bool {
	return false
}
