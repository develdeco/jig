//go:build windows

package verifydeliver

import (
	"syscall"
	"testing"
)

// otherSpelling is dir's 8.3 short name; a test skips where the volume makes
// none.
func otherSpelling(t *testing.T, dir string) string {
	t.Helper()
	from, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n, err := syscall.GetShortPathName(from, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		t.Skipf("no short name for %s: %v", dir, err)
	}
	short := syscall.UTF16ToString(buf[:n])
	if short == dir {
		t.Skipf("the volume of %s makes no short names", dir)
	}
	return short
}
