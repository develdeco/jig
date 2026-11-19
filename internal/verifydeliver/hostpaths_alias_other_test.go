//go:build !windows

package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"
)

// otherSpelling is a symlink to dir; a test skips where the filesystem makes
// none.
func otherSpelling(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("no symlink to %s: %v", dir, err)
	}
	return link
}
