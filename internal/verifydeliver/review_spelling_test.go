package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRelativizeReviewedPathAcrossSpellings pins that a reviewed_paths entry
// naming a file inside the lease counts toward coverage whichever spelling
// of the lease the reviewer reports it through: jig's own, or the same
// directory reached through a symlink resolved.
func TestRelativizeReviewedPathAcrossSpellings(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "lease")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a directory symlink here: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(real, "a.go"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, lease, reported string }{
		{"lease through a link, file resolved", link, resolved},
		{"lease resolved, file through the link", real, filepath.Join(link, "a.go")},
	} {
		if got, ok := relativizeReviewedPath(c.lease, c.reported); !ok || got != "a.go" {
			t.Errorf("%s: relativizeReviewedPath(%s, %s) = %q, %v, want a.go", c.name, c.lease, c.reported, got, ok)
		}
	}

	outside := filepath.Join(t.TempDir(), "b.go")
	if err := os.WriteFile(outside, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := relativizeReviewedPath(link, outside); ok {
		t.Errorf("relativizeReviewedPath(%s, %s) = %q, want a path outside the lease left out", link, outside, got)
	}
}
