package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// This file covers Dispatch.ExtraReadFile on the backend that spells a
// dispatch's paths for its session: headless spells it long (and grants
// nothing for it).

// TestSessionViewSpellsTheExtraReadFileLikeTheOtherPaths: the file the prompt
// names for the session to read goes through the same long-spelling rewrite as
// the worktree and the result path, and grants nothing: the settings are the
// same with and without it. (longPath is the identity off Windows, so this
// pins the wiring; longpath_windows_test.go pins the spelling.)
func TestSessionViewSpellsTheExtraReadFileLikeTheOtherPaths(t *testing.T) {
	d := missingDispatch(t, true)
	d.ExtraReadFile = filepath.Join(t.TempDir(), "reviews", "round-1", "recordings.json")
	d.Prompt = "recordings.json at " + d.ExtraReadFile
	got := sessionView(d)
	if want := longPath(d.ExtraReadFile); got.ExtraReadFile != want || got.Prompt != "recordings.json at "+want {
		t.Errorf("sessionView left ExtraReadFile %q and prompt %q, want the long spelling %q", got.ExtraReadFile, got.Prompt, want)
	}

	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}
	with, err := b.settings(d)
	if err != nil {
		t.Fatal(err)
	}
	d.ExtraReadFile = ""
	without, err := b.settings(d)
	if err != nil {
		t.Fatal(err)
	}
	if with != without {
		t.Errorf("an ExtraReadFile changed the settings:\n%s\nwant\n%s", with, without)
	}
}

// TestRespellMentionsReplacesTheLongestPathFirst: a path that contains
// another one is replaced whole, not by its shorter prefix, and an empty path
// or one the spelling leaves alone is skipped.
func TestRespellMentionsReplacesTheLongestPathFirst(t *testing.T) {
	t.Parallel()
	mount := func(p string) string { return strings.NewReplacer(`C:\`, "/mnt/c/", `\`, "/").Replace(p) }
	got := respellMentions(`run C:\a\b\c.json in C:\a\b, then C:\a`, []string{`C:\a`, "", `C:\a\b\c.json`, `C:\a\b`, "/mnt/c/z"}, mount)
	if want := `run /mnt/c/a/b/c.json in /mnt/c/a/b, then /mnt/c/a`; got != want {
		t.Errorf("respellMentions = %q, want %q", got, want)
	}
}
