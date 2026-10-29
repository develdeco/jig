package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// replaceWindow is how long a read, and AtomicWrite's rename, ride out
// another process replacing the same file. On Windows a replacement is not
// atomic for a process opening the file: while the rename lands, the open
// fails with a sharing violation, access denied (the old file is pending
// delete) or not found, and the rename itself fails while a reader holds
// the file. A busy machine, or a virus scanner opening each new file as it
// lands, stretches that moment well past a few tries.
const replaceWindow = 2 * time.Second

// replaceBackoff is the pause between tries within replaceWindow.
const replaceBackoff = 20 * time.Millisecond

// readFile is os.ReadFile that rides out the moment AtomicWrite replaces the
// file. On Windows it opens the file with delete sharing and AtomicWrite
// renames with POSIX semantics (replaceFile), which together make the
// replacement atomic for readers; a failure that can still be that moment
// (transientRead: a file system without POSIX renames, another program
// holding the file) is retried until replaceWindow has passed. Elsewhere the
// rename is atomic for readers, and only a read during a file's first write
// can wait.
func readFile(path string) ([]byte, error) {
	deadline := time.Now().Add(replaceWindow)
	for {
		data, err := readFileOnce(path)
		if err == nil || !transientRead(path, err) || time.Now().After(deadline) {
			return data, err
		}
		time.Sleep(replaceBackoff)
	}
}

// transientRead reports whether err, from reading path, can be AtomicWrite
// replacing it: a failure replacing names, or path not there while an
// AtomicWrite of it is under way. A file that is simply missing fails at once.
func transientRead(path string, err error) bool {
	return replacing(err) || (errors.Is(err, fs.ErrNotExist) && replacementPending(path))
}

// replacementPending reports whether an AtomicWrite of path is under way:
// its temp file (tempName) sits in path's directory, written within the last
// replaceWindow. An older one is a writer's leftover, not a write.
func replacementPending(path string) bool {
	dir, base := filepath.Split(path)
	entries, err := os.ReadDir(filepath.Clean(dir))
	if err != nil {
		return false
	}
	prefix := "." + base + "."
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) < replaceWindow {
			return true
		}
	}
	return false
}
