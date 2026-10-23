package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsIgnoredThroughASymlink: a directory ignored by one spelling is ignored
// when reached by another that resolves to it, as on macOS, where the temp
// directory under /var is /private/var to git and to os.Getwd.
func TestIsIgnoredThroughASymlink(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a directory symlink here: %v", err)
	}
	if dir, ok := IsIgnored([]string{link}, filepath.Join(real, "sub")); !ok || dir != link {
		t.Errorf("ignored by the link, the real path: IsIgnored = %q, %v; want %q, true", dir, ok, link)
	}
	if dir, ok := IsIgnored([]string{real}, filepath.Join(link, "sub")); !ok || dir != real {
		t.Errorf("ignored by the real path, the link: IsIgnored = %q, %v; want %q, true", dir, ok, real)
	}
	if _, ok := IsIgnored([]string{filepath.Join(base, "other")}, filepath.Join(link, "sub")); ok {
		t.Error("a directory outside every ignored one is reported ignored")
	}
}
