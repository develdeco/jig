//go:build !windows

package store

// replacing reports whether err is a failed open of a file being renamed into
// place. A rename is atomic for readers here, so no error is.
func replacing(error) bool { return false }
