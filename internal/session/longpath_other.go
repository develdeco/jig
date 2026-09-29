//go:build !windows

package session

// longPath returns p unchanged: only Windows has 8.3 short names.
func longPath(p string) string { return p }
