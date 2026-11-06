//go:build !windows

package fileshare

import "os"

// Replacing reports whether err is a rename over a file, or an open of one,
// failing because another process is replacing it. A rename is atomic for
// readers here, so no error is.
func Replacing(error) bool { return false }

// Rename renames tmp over path, atomically for readers.
func Rename(tmp, path string) error { return os.Rename(tmp, path) }
