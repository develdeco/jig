//go:build !windows

package fileshare

import "os"

// Open opens path for reading. A rename over it or a removal of it goes
// ahead while it is open, as for any open file here.
func Open(path string) (*os.File, error) { return os.Open(path) }
