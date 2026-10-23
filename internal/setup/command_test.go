package setup

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOwnedCommand(t *testing.T) {
	t.Parallel()
	owned := []string{
		"jig observe --harness claude",
		"jig observe",
		"jig githook post-commit",
		"/usr/local/bin/jig observe --harness claude",
		`"/opt/my tools/jig" observe --harness claude`,
		`"C:/Program Files/jig/jig.exe" observe --harness claude`,
		`C:\tools\jig.exe observe --harness claude`,
		"./jig observe",
		`"jig" githook post-commit`,
	}
	for _, c := range owned {
		if !OwnedCommand(c) {
			t.Errorf("OwnedCommand(%q) = false, want true", c)
		}
	}
	notOwned := []string{
		"",
		"echo hi",
		"jig",
		"jig status",
		"jig observer",
		"jig-dev observe",
		"my_jig githook",
		"echo not jig, only talks of jig observe",
		"npx tool --jig observe",
		"/usr/bin/notjig observe",
		`"/usr/bin/xjig.exe" observe`,
		"cd /src/jig && make observe",
		" jig observe",
	}
	for _, c := range notOwned {
		if OwnedCommand(c) {
			t.Errorf("OwnedCommand(%q) = true, want false", c)
		}
	}
}

func TestJigCommand(t *testing.T) {
	t.Parallel()
	abs := filepath.Join(t.TempDir(), "bin dir", "jig")
	if runtime.GOOS == "windows" {
		abs += ".exe"
	}

	if got, err := JigCommand(abs, true); err != nil || got != "jig" {
		t.Errorf("on PATH: %q, %v, want the bare word", got, err)
	}
	got, err := JigCommand(abs, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"` + filepath.ToSlash(abs) + `"`; got != want {
		t.Errorf("off PATH: %q, want %q", got, want)
	}
	if !OwnedCommand(ClaudeCommand(got)) || !OwnedCommand(got+" githook post-commit") {
		t.Errorf("the commands made of %q are not recognized as jig's", got)
	}

	for name, exe := range map[string]string{
		"a binary not named jig": filepath.Join(filepath.Dir(abs), "jig-dev"),
		"a relative path":        "jig.exe",
		"a dollar":               filepath.Join(filepath.Dir(abs), "a$b", "jig"),
		"a quote":                filepath.Join(filepath.Dir(abs), `a"b`, "jig"),
	} {
		if _, err := JigCommand(exe, false); err == nil {
			t.Errorf("%s: JigCommand accepted %q", name, exe)
		}
	}
	// A binary not named jig is refused even when it would be found on PATH.
	if _, err := JigCommand(filepath.Join(filepath.Dir(abs), "jig-dev"), true); err == nil || !strings.Contains(err.Error(), "jig-dev") {
		t.Errorf("a binary not named jig on PATH: %v, want a refusal naming it", err)
	}
}
