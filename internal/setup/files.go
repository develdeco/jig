package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// readIfExists reads path; a file that is not there is (nil, false, nil).
func readIfExists(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// writeFile replaces path with data, atomically (a temp file in the same
// directory, renamed over it), creating the directory. With keepMode a file
// already there keeps its permission bits, so a settings file its owner made
// private stays private and a hook script someone else wrote stays as
// executable as it was; otherwise, and for a new file, the file gets perm.
// A path that is a symbolic link (a dotfiles manager keeps settings that way)
// is written through: the file it points to is replaced, not the link.
func writeFile(path string, data []byte, perm fs.FileMode, keepMode bool) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if info, err := os.Stat(path); keepMode && err == nil {
		perm = info.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		cleanup()
		return err
	}
	// On Windows a rename onto a file another process has open fails with a
	// sharing violation; a short retry rides out a scanner or an editor.
	for attempt := 0; ; attempt++ {
		err = os.Rename(tmpPath, path)
		if err == nil {
			return nil
		}
		if attempt == 9 {
			cleanup()
			return fmt.Errorf("replace %s: %w", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
