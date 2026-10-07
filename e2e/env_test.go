package e2e

import (
	"runtime"
	"slices"
	"testing"
)

// TestJigEnvReplacesWhatTheProcessHas pins what runJig relies on to give
// each test its own environment: a jigEnv's variables replace the process's
// entries of the same name (once each, never beside them), and leave every
// other entry, including Windows' "=C:" per-drive ones, alone.
func TestJigEnvReplacesWhatTheProcessHas(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/bin", "JIG_HOME=/old", "HOME=/real", "=C:=C:\\work", "USERPROFILE=/real"}
	got := replaceEnv(base, jigEnv{home: "/h", userHome: "/u"}.overrides())
	want := []string{"PATH=/bin", "=C:=C:\\work", "JIG_HOME=/h", "HOME=/u", "USERPROFILE=/u"}
	if !slices.Equal(got, want) {
		t.Fatalf("replaceEnv = %q, want %q", got, want)
	}

	// A jigEnv with no user home leaves the process's alone.
	got = replaceEnv(base, jigEnv{home: "/h"}.overrides())
	want = []string{"PATH=/bin", "HOME=/real", "=C:=C:\\work", "USERPROFILE=/real", "JIG_HOME=/h"}
	if !slices.Equal(got, want) {
		t.Fatalf("replaceEnv without a user home = %q, want %q", got, want)
	}

	// Names differ by case on Unix and are one name on Windows.
	got = replaceEnv([]string{"Jig_Home=/old"}, jigEnv{home: "/h"}.overrides())
	want = []string{"Jig_Home=/old", "JIG_HOME=/h"}
	if runtime.GOOS == "windows" {
		want = []string{"JIG_HOME=/h"}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("replaceEnv on %s with a differently cased name = %q, want %q", runtime.GOOS, got, want)
	}
}
