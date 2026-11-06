package store

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"

	"github.com/develdeco/jig/internal/fileshare"
)

// AtomicWrite writes data to path by writing a temp file in the same
// directory (so the final rename is atomic even across the same volume) and
// renaming it into place (fileshare.Replace: on Windows with POSIX semantics, so
// the name moves in one step even while a reader holds the old file). A
// rename that still fails because another process has path open
// is retried until fileshare.ReplaceWindow has passed; readFile rides out
// the same moment from the reading side.
func AtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := filepath.Join(dir, tempName(filepath.Base(path)))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}

	if err := fileshare.Replace(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func tempName(name string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "." + name + "." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(b[:]) + ".tmp"
}
