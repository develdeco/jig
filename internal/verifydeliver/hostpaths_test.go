package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/session"
)

// TestLeaveOutHostPathsNamesDirectoriesByRole: a real operating system error
// for a path under each directory prints, after leaveOutHostPaths, the role of
// the directory and the rest of the path, and nothing of the host.
func TestLeaveOutHostPathsNamesDirectoriesByRole(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jigHome := filepath.Join(root, "home")
	picks := filepath.Join(jigHome, "evidence", "id", "JIG-1", "picks", "abc123")
	storeRoot := filepath.Join(root, "store")
	dirs := []hostDir{{storeRoot, "<store>"}, {jigHome, "<jig home>"}, {picks, "<picks dir>"}}

	errFor := func(path string) string {
		_, err := os.Lstat(path)
		if err == nil {
			t.Fatalf("%s exists", path)
		}
		return err.Error()
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"a path in the picks directory", errFor(filepath.Join(picks, "a.png")), "<picks dir>" + string(filepath.Separator) + "a.png"},
		{"a path in the jig home, outside the picks directory", errFor(filepath.Join(jigHome, "evidence", "id", "JIG-1", "other")), "<jig home>" + string(filepath.Separator) + "evidence"},
		{"a path in the store", errFor(filepath.Join(storeRoot, "JIG-1", "work")), "<store>" + string(filepath.Separator) + "JIG-1"},
		{"the forward-slash spelling", "mkdir " + filepath.ToSlash(filepath.Join(jigHome, "evidence")) + ": no", "mkdir <jig home>/evidence: no"},
		{"the picks directory itself", "cannot read " + picks + ".", "cannot read <picks dir>."},
		{"no path", "the pick session failed: boom", "the pick session failed: boom"},
	}
	for _, c := range cases {
		got := leaveOutHostPaths(c.text, dirs...)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want it to contain %q", c.name, got, c.want)
		}
		for _, host := range []string{root, jigHome, storeRoot} {
			if strings.Contains(got, host) || strings.Contains(got, filepath.ToSlash(host)) {
				t.Errorf("%s: %q still names %s", c.name, got, host)
			}
		}
	}
}

// TestLeaveOutHostPathsNamesARelativeDirectoryByItsAbsoluteSpelling: a
// relative directory (a store reached by a relative --store) is named where
// text spells it absolutely, and its relative spelling, which names nothing of
// the machine, stays.
func TestLeaveOutHostPathsNamesARelativeDirectoryByItsAbsoluteSpelling(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	text := "open " + filepath.Join(cwd, "rel-store", "x") + " (see ." + sep + "rel-store)"
	want := "open <store>" + sep + "x (see ." + sep + "rel-store)"
	if got := leaveOutHostPaths(text, hostDir{"rel-store", "<store>"}); got != want {
		t.Errorf("leaveOutHostPaths(%q) = %q, want %q", text, got, want)
	}
}

// TestLeaveOutHostPathsNeverRewritesWhatIsNotAHostDirectory: an empty
// directory (which would resolve to the working directory), a bare relative
// spelling and a filesystem root each name nothing of the machine (or, for a
// root, a prefix of every path), so text holding them is left as it is.
func TestLeaveOutHostPathsNeverRewritesWhatIsNotAHostDirectory(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fsRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	text := "clear the picks directory: remove store: the file is in use. See ./store, " + cwd + " and " + filepath.Join(fsRoot, "tmp")
	got := leaveOutHostPaths(text, hostDir{"", "<empty>"}, hostDir{"store", "<store>"}, hostDir{fsRoot, "<root>"})
	if got != text {
		t.Errorf("leaveOutHostPaths rewrote %q to %q", text, got)
	}
}

// TestLeaveOutHostPathsNamesTheGoQuotedSpelling: a message that quotes one of
// jig's own paths with %q spells it Go-quoted, where a backslash doubles (every
// separator of a Windows path) and a quote character is escaped, and that is
// the same path, so it is named too. The directory has a quote in its name so
// the quoted spelling differs from the plain one on every platform.
func TestLeaveOutHostPathsNamesTheGoQuotedSpelling(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jigHome := filepath.Join(root, `we"ird`, "home")
	picks := filepath.Join(jigHome, "evidence", "id", "JIG-1", "picks", "abc123")
	quoted := func(p string) string { return strings.Trim(strconv.Quote(p), `"`) }
	if quoted(jigHome) == jigHome {
		t.Fatalf("the quoted spelling of %q is the plain one, so this test proves nothing", jigHome)
	}

	got := leaveOutHostPaths(fmt.Sprintf("open %q: no such file; staged %q", filepath.Join(jigHome, "evidence"), filepath.Join(picks, "a.png")),
		hostDir{jigHome, "<jig home>"}, hostDir{picks, "<picks dir>"})
	sep := string(filepath.Separator)
	if want := fmt.Sprintf(`open "<jig home>%sevidence": no such file; staged "<picks dir>%sa.png"`, quoted(sep), quoted(sep)); got != want {
		t.Errorf("leaveOutHostPaths = %q, want %q", got, want)
	}
	for _, host := range []string{root, jigHome} {
		if strings.Contains(got, quoted(host)) {
			t.Errorf("%q still names %s", got, quoted(host))
		}
	}
}

// TestContainsHostPathMatchesEveryHandedSpelling: containsHostPath is the
// render-time check behind the owner's decision on r1-f13 (DECISIONS.md),
// and so - unlike leaveOutHostPaths, which composes jig's own reasons - it
// must also catch a directory's WSL mount spelling, the one herdr hands a
// session on Windows: that is a spelling jig handed out, not jig's own
// text, so a session's caption or summary can repeat it back. A path that
// is not one of the known directories, in any spelling, is not matched:
// comparison is exact strings only, never a pattern.
func TestContainsHostPathMatchesEveryHandedSpelling(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "home")
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	dirs := []hostDir{{home, "<jig home>"}}
	wsl := session.WSLPath(home)

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"the raw spelling", "under " + home + " somewhere", true},
		{"the forward-slash spelling", "under " + filepath.ToSlash(home) + " somewhere", true},
		{"the WSL mount spelling", "ran from " + wsl + " and it worked", true},
		{"an unrelated path", "under " + elsewhere + " it worked", false},
		{"no path at all", "it just worked", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containsHostPath(c.text, dirs...); got != c.want {
				t.Errorf("containsHostPath(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}
