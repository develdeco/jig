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

// TestJigEnvDropsTheRecordDir: the jig under test never gets JIG_RECORD_DIR,
// whether or not the suite that runs it was given one, and only that name goes.
func TestJigEnvDropsTheRecordDir(t *testing.T) {
	t.Parallel()
	got := withoutEnv([]string{"PATH=/bin", "JIG_RECORD_DIR=/rec", "JIG_RECORD_DIR_X=/keep", "=C:=C:\\work", "HOME=/h"}, recordDirEnv)
	want := []string{"PATH=/bin", "JIG_RECORD_DIR_X=/keep", "=C:=C:\\work", "HOME=/h"}
	if !slices.Equal(got, want) {
		t.Fatalf("withoutEnv = %q, want %q", got, want)
	}
	if runtime.GOOS == "windows" {
		if got := withoutEnv([]string{"Jig_Record_Dir=/rec", "PATH=/bin"}, recordDirEnv); !slices.Equal(got, []string{"PATH=/bin"}) {
			t.Errorf("withoutEnv on Windows with a differently cased name = %q, want it dropped", got)
		}
	}

	// Whatever the process has, the environment runJig hands over has none.
	for _, kv := range (jigEnv{home: t.TempDir()}).environ() {
		if sameEnvName(envName(kv), recordDirEnv) {
			t.Errorf("environ() holds %q, want no %s", kv, recordDirEnv)
		}
	}
}
