//go:build !windows

package store

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryLockFile attempts a non-blocking exclusive flock on f.
func tryLockFile(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		if err == unix.EWOULDBLOCK {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
