//go:build windows

package verifydeliver

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestRelativizeReviewedPathShortLease pins the spelling the headless
// backend creates: jig reached the lease through a Windows 8.3 short name,
// the reviewer session was handed its long spelling, and it reports the
// files it read spelled long.
func TestRelativizeReviewedPathShortLease(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(base, "a lease with a long name")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(long, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := syscall.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, syscall.MAX_PATH)
	n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		t.Fatalf("GetShortPathName(%s): %d, %v", long, n, err)
	}
	short := syscall.UTF16ToString(buf[:n])
	if short == long {
		t.Skipf("the volume holding %s makes no 8.3 short names", long)
	}

	if got, ok := relativizeReviewedPath(short, filepath.Join(long, "a.go")); !ok || got != "a.go" {
		t.Errorf("relativizeReviewedPath(%s, long a.go) = %q, %v, want a.go", short, got, ok)
	}
}
