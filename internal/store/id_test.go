package store

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestStoreIDIsSixteenHexDigits(t *testing.T) {
	t.Parallel()
	id, err := (&Store{Root: t.TempDir()}).ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(id) {
		t.Fatalf("ID = %q, want 16 lowercase hex digits", id)
	}
}

func TestStoreIDNamesTheClone(t *testing.T) {
	t.Parallel()
	a, err := (&Store{Root: t.TempDir()}).ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	b, err := (&Store{Root: t.TempDir()}).ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if a == b {
		t.Fatalf("two different clones share the id %q", a)
	}
}

// TestStoreIDIsStableAcrossSpellings checks that one clone reached by an
// unclean path, through a symlink, or (on Windows, where a path's case is
// not significant) in another case, names one id: the id is the resolved
// root's, not the spelling's.
func TestStoreIDIsStableAcrossSpellings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	want, err := (&Store{Root: root}).ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	sameID := func(t *testing.T, spelling string) {
		t.Helper()
		got, err := (&Store{Root: spelling}).ID()
		if err != nil {
			t.Fatalf("ID of %q: %v", spelling, err)
		}
		if got != want {
			t.Fatalf("ID of %q = %q, want %q", spelling, got, want)
		}
	}

	t.Run("unclean", func(t *testing.T) {
		sameID(t, root+string(os.PathSeparator)+"."+string(os.PathSeparator))
	})

	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(root, link); err != nil {
			t.Skipf("symlink: %v", err)
		}
		sameID(t, link)
	})

	t.Run("case", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("paths are case sensitive off Windows")
		}
		sameID(t, strings.ToUpper(root))
		sameID(t, strings.ToLower(root))
	})
}

func TestStoreIDRefusesAMissingRoot(t *testing.T) {
	t.Parallel()
	if id, err := (&Store{Root: filepath.Join(t.TempDir(), "gone")}).ID(); err == nil {
		t.Fatalf("ID of a missing root = %q, want an error", id)
	}
}
