//go:build !windows

package verifydeliver

// shortenQuotedPath is a no-op off Windows: sh -c has no equivalent
// quoting bug with a leading quoted, space-containing path.
func shortenQuotedPath(cmd string) string { return cmd }
