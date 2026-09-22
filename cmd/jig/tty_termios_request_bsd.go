//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "golang.org/x/sys/unix"

// termiosRequest is the ioctl request that reads terminal attributes on
// darwin and the BSD family.
const termiosRequest = unix.TIOCGETA
