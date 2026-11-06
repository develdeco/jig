package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/fileshare"
)

// replaceWindow and replaceBackoff are how long a read, and AtomicWrite's
// rename, ride out another process replacing the same file, and the pause
// between tries (fileshare.Replace has the reasons).
const (
	replaceWindow  = fileshare.ReplaceWindow
	replaceBackoff = fileshare.ReplaceBackoff
)

// readFile is os.ReadFile that rides out the moment AtomicWrite replaces the
// file. On Windows it opens the file with delete sharing and AtomicWrite
// renames with POSIX semantics (fileshare.Rename), which together make the
// replacement atomic for readers; a failure that can still be that moment
// (transientRead: a file system without POSIX renames, another program
// holding the file) is retried until replaceWindow has passed. Elsewhere the
// rename is atomic for readers, and only a read during a file's first write
// can wait.
func readFile(path string) ([]byte, error) {
	deadline := time.Now().Add(replaceWindow)
	for {
		data, err := fileshare.ReadFile(path)
		if err == nil || !transientRead(path, err) || time.Now().After(deadline) {
			return data, err
		}
		time.Sleep(replaceBackoff)
	}
}

// transientRead reports whether err, from reading path, can be AtomicWrite
// replacing it: a failure replacing names, or path not there while an
// AtomicWrite of it is under way, or there again already (the write landed
// between the read and the look for its temp file). A file that is simply
// missing fails at once.
func transientRead(path string, err error) bool {
	if fileshare.Replacing(err) {
		return true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if replacementPending(path) {
		return true
	}
	_, statErr := os.Stat(path)
	return statErr == nil
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
