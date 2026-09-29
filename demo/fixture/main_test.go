package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// TestMain gives this binary's tests the same hermetic git config every
// package whose tests run git uses (gittest.Run), since run below calls
// fixture.Build, which shells out to git.
func TestMain(m *testing.M) {
	os.Exit(gittest.Run(m))
}

// TestRunExplicitJIGHome builds into t.TempDir() through run, the same code
// path main uses, with JIG_HOME already set in the environment (the way a
// test or a tape that manages its own home would call this command): run
// must leave that value alone and export it back unchanged.
func TestRunExplicitJIGHome(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("JIG_HOME", home)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-out", dir, "-scenario", "reviewer"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0\nstderr:\n%s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "export JIG_HOME='"+home+"'\n") {
		t.Fatalf("run stdout does not export the caller's own JIG_HOME=%s:\n%s", home, out)
	}
	if !strings.Contains(out, "export JIG_TICKET='JIG-1'\n") {
		t.Fatalf("run stdout missing the ticket export:\n%s", out)
	}
}

// TestRunDefaultsJIGHome builds with JIG_HOME unset, so run must derive
// <out>/home itself and the fixture must actually land there (not the real
// user home) - the case a tape relies on when it never sets JIG_HOME before
// calling this command. HOME and USERPROFILE are also pointed at a
// t.TempDir(): if the <out>/home derivation ever regresses, the fallback
// inside home.Root resolves against the real user's home directory, and
// this test must never let that write land there even while it fails.
func TestRunDefaultsJIGHome(t *testing.T) {
	dir := t.TempDir()
	fallbackHome := t.TempDir()
	t.Setenv("JIG_HOME", "")
	t.Setenv("HOME", fallbackHome)
	t.Setenv("USERPROFILE", fallbackHome)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-out", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0\nstderr:\n%s", code, stderr.String())
	}

	wantHome := filepath.Join(dir, "home")
	out := stdout.String()
	for _, want := range []string{
		"export JIG_HOME='" + wantHome + "'\n",
		"export JIG_STORE_DIR='" + filepath.Join(dir, "store") + "'\n",
		"export JIG_SCENARIO_DIR='" + filepath.Join(dir, "scenario") + "'\n",
		"export JIG_REPO_DIR='" + filepath.Join(dir, "fixture-repo") + "'\n",
		"export JIG_TICKET='JIG-1'\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("run stdout missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(wantHome, "projects.yaml")); err != nil {
		t.Fatalf("default JIG_HOME %s was never actually used: %v", wantHome, err)
	}
}

// TestRunRequiresOut asserts the documented required flag is enforced with a
// clear message rather than a panic or a silent empty build directory.
func TestRunRequiresOut(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run with no -out exit = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "-out is required") {
		t.Fatalf("run stderr missing the -out requirement:\n%s", stderr.String())
	}
}

// TestRunUnknownScenario asserts an unknown -scenario value fails clearly
// (fixture.ErrUnknownScenarioBranch), rather than silently building the base
// scenario a typo'd name never meant to ask for.
func TestRunUnknownScenario(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"-out", dir, "-scenario", "no-such-scenario"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run with an unknown -scenario exit = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "no-such-scenario") {
		t.Fatalf("run stderr does not name the unknown scenario:\n%s", stderr.String())
	}
}

// TestRunQuotesExportsWithSpaces builds into a directory whose name has a
// space in it - the shell metacharacter most likely to turn up in a real
// -out path - and asserts every printed export line is single-quoted, so a
// tape's `eval "$(...)"` reads back the exact value instead of truncating
// at the first unquoted space. It checks the printed text itself, never by
// shelling out to eval it, so it needs no bash on the host running it.
func TestRunQuotesExportsWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "with space")
	fallbackHome := t.TempDir()
	t.Setenv("JIG_HOME", "")
	t.Setenv("HOME", fallbackHome)
	t.Setenv("USERPROFILE", fallbackHome)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-out", dir, "-scenario", "reviewer"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0\nstderr:\n%s", code, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"export JIG_HOME='" + filepath.Join(dir, "home") + "'\n",
		"export JIG_STORE_DIR='" + filepath.Join(dir, "store") + "'\n",
		"export JIG_SCENARIO_DIR='" + filepath.Join(dir, "scenario") + "'\n",
		"export JIG_REPO_DIR='" + filepath.Join(dir, "fixture-repo") + "'\n",
		"export JIG_TICKET='JIG-1'\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("run stdout missing shell-quoted %q:\n%s", want, out)
		}
	}
}

// TestShellQuoteEmbeddedSingleQuote asserts shellQuote's doc comment
// directly: a single quote inside s must close the current quote,
// contribute an escaped quote, and reopen a new one, so an eval reads back
// exactly the bytes of s. Without this test, a shellQuote that dropped the
// backslash escape and left the two quotes bare would still leave every
// other test in this file green, since none of their paths contain a
// quote character.
func TestShellQuoteEmbeddedSingleQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "a'b", want: `'a'\''b'`},
		{in: "'", want: `''\'''`},
		{in: "no-quote", want: `'no-quote'`},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Fatalf("shellQuote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
