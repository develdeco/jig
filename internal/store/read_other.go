//go:build !windows

package store

import "os"

// replacing reports whether err is a failed open of a file being renamed into
// place. A rename is atomic for readers here, so no error is.
func replacing(error) bool { return false }

// replaceFile renames tmp over path, atomically for readers.
func replaceFile(tmp, path string) error { return os.Rename(tmp, path) }
