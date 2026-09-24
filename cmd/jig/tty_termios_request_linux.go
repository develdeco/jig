//go:build linux

package main

import "golang.org/x/sys/unix"

// termiosRequest is the ioctl request that reads terminal attributes on
// linux.
const termiosRequest = unix.TCGETS
