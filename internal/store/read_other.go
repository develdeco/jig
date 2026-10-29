//go:build !windows

package store

import "os"

// replacing reports whether err is a failed open of a file being renamed into
// place. A rename is atomic for readers here, so no error is.
func replacing(error) bool { return false }

// readFileOnce reads path whole.
func readFileOnce(path string) ([]byte, error) { return os.ReadFile(path) }

// replaceFile renames tmp over path, atomically for readers.
func replaceFile(tmp, path string) error { return os.Rename(tmp, path) }
