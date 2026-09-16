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
// renaming it into place. On Windows, a sharing violation from the rename
// (another process has path open) is retried 10 times with a 50ms backoff.
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

	var err error
	for attempt := 0; attempt < 10; attempt++ {
		err = os.Rename(tmp, path)
		if err == nil {
			return nil
		}
		if attempt == 9 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = os.Remove(tmp)
	return err
}

func tempName(name string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "." + name + "." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(b[:]) + ".tmp"
}
