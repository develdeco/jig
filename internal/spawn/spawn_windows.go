//go:build windows

package spawn

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// detachAttr detaches the child from the caller's console, gives it a hidden
// console of its own (CREATE_NO_WINDOW), puts it in a process group of its
// own so a Ctrl-C aimed at the caller's does not reach it, and hides any
// window. The hidden console is what the child's own children (a worker runs
// git and the oracles) inherit: a child with no console at all
// (DETACHED_PROCESS) makes Windows allocate a console window for every
// console program it starts, which flashes on the screen and does not return
// at all while the session is locked. Attempt 0 also asks to break away from
// the caller's job object (CREATE_BREAKAWAY_FROM_JOB), so a terminal or
// harness that kills its job on close does not take the child with it;
// attempt 1 leaves that flag out, for a job that does not allow breakaway.
func detachAttr(attempt int) *syscall.SysProcAttr {
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	if attempt == 0 {
		flags |= windows.CREATE_BREAKAWAY_FROM_JOB
	}
	return &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
}

// retryDetach reports whether a failed start is worth one more attempt
// without breakaway: a job that does not allow breakaway refuses a create
// that asks for it with ERROR_ACCESS_DENIED. The child then stays in the
// job, and is killed with it if the job's owner kills the job; that is the
// limit of what a process can do about a job that will not let it go.
func retryDetach(err error, attempt int) bool {
	return attempt == 1 && errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
