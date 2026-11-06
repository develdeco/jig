package fileshare

import "time"

// ReplaceWindow is how long a replacement of a file (Replace), and a read of
// one a writer replaces, ride out another process holding it. On Windows a
// replacement is not atomic for a process opening the file: while the rename
// lands, the open fails with a sharing violation, access denied (the old file
// is pending delete) or not found, and the rename itself fails while a reader
// that does not share deletion holds the file. A busy machine, or a virus
// scanner opening each new file as it lands, stretches that moment well past
// a few tries.
const ReplaceWindow = 2 * time.Second

// ReplaceBackoff is the pause between tries within ReplaceWindow.
const ReplaceBackoff = 20 * time.Millisecond

// Replace renames tmp over path (Rename), and tries again while the rename
// fails because another process has path open (Replacing) until ReplaceWindow
// has passed. It returns the last error then; the caller removes tmp. This is
// the one rule for a file jig replaces by rename: store.AtomicWrite and the
// git ref and index writes both use it.
func Replace(tmp, path string) error {
	deadline := time.Now().Add(ReplaceWindow)
	for {
		err := Rename(tmp, path)
		if err == nil || !Replacing(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(ReplaceBackoff)
	}
}
