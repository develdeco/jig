package store

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// AtomicWrite writes data to path by writing a temp file in the same
// directory (so the final rename is atomic even across the same volume) and
// renaming it into place (replaceFile: on Windows with POSIX semantics, so
// the name moves in one step even while a reader holds the old file). A
// rename that still fails because another process has path open
// (replacing) is retried until replaceWindow has passed; readFile rides out
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

	deadline := time.Now().Add(replaceWindow)
	for {
		err := replaceFile(tmp, path)
		if err == nil {
			return nil
		}
		if !replacing(err) || time.Now().After(deadline) {
			_ = os.Remove(tmp)
			return err
		}
		time.Sleep(replaceBackoff)
	}
}

func tempName(name string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "." + name + "." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(b[:]) + ".tmp"
}
