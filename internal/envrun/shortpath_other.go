//go:build !windows

package envrun

// ShortenQuotedPath is a no-op off Windows: sh -c has no equivalent
// quoting bug with a leading quoted, space-containing path.
func ShortenQuotedPath(cmd string) string { return cmd }
