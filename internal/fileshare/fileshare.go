// Package fileshare opens files for reading so that another process can
// still rename over them or delete them while they are open. On Linux and
// macOS that is how every open file behaves. On Windows os.Open shares a file
// for reading and writing but not for deletion, so while a reader holds it a
// rename over it or a removal of it fails: git's rename of a lock file into
// place (HEAD, a ref, packed-refs, the index) and the store's atomic writes
// both do that. Every file jig reads beside a writer it does not control is
// opened here.
package fileshare

import "io"

// ReadFile reads path whole, opened with Open.
func ReadFile(path string) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
