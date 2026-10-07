package home

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRootHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JIG_HOME", dir)
	r, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if r != dir {
		t.Fatalf("Root() = %q, want %q", r, dir)
	}
}

// TestPathsDeriveFromTheRootGiven covers the home-anchored paths: they are
// derived from the root a caller passes, never from the environment.
func TestPathsDeriveFromTheRootGiven(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JIG_HOME", t.TempDir())
	if p := PoolDir(root); p != filepath.Join(root, "pool") {
		t.Fatalf("PoolDir(%q) = %q", root, p)
	}
	if m := MachinePath(root); m != filepath.Join(root, "projects.yaml") {
		t.Fatalf("MachinePath(%q) = %q", root, m)
	}
	if e := IntentExcerptDir(root); e != filepath.Join(root, "intent-excerpts") {
		t.Fatalf("IntentExcerptDir(%q) = %q", root, e)
	}
	if s := IntentScratchDir(root); s != filepath.Join(root, "intent-scratch") {
		t.Fatalf("IntentScratchDir(%q) = %q", root, s)
	}
}

func TestEvidenceDirIsUnderTheRootGiven(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JIG_HOME", t.TempDir())
	got, err := EvidenceDir(root, "0123456789abcdef", "T-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "evidence", "0123456789abcdef", "T-1", "abc123"); got != want {
		t.Fatalf("EvidenceDir = %q, want %q", got, want)
	}
}

// TestEvidenceDirRefusesAnythingButASingleDirectoryName checks each of the
// three caller-supplied parts: a spelling that climbs out of the evidence
// tree, or names two directories, or a directory Windows would fold onto
// another, is refused rather than joined.
func TestEvidenceDirRefusesAnythingButASingleDirectoryName(t *testing.T) {
	bad := []string{"", ".", "..", ".hidden", "a/b", `a\b`, "C:x", "x.", "x "}
	for _, name := range bad {
		for i, parts := range [][3]string{{name, "T-1", "abc"}, {"id", name, "abc"}, {"id", "T-1", name}} {
			if got, err := EvidenceDir(t.TempDir(), parts[0], parts[1], parts[2]); err == nil {
				t.Errorf("EvidenceDir with %q in part %d = %q, want a refusal", name, i, got)
			}
		}
	}
}

// userDirVar is the variable os.UserHomeDir reads on this OS.
func userDirVar() string {
	switch runtime.GOOS {
	case "windows":
		return "USERPROFILE"
	case "plan9":
		return "home"
	}
	return "HOME"
}

// envOf is a getenv that answers from vars alone, never from the process.
func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// TestRootFromHonorsTheEnvironmentGiven: JIG_HOME wins over the user home,
// and both come from the environment handed in, not the process's.
func TestRootFromHonorsTheEnvironmentGiven(t *testing.T) {
	t.Parallel()

	got, err := RootFrom(envOf(map[string]string{"JIG_HOME": "/elsewhere", userDirVar(): "/users/me"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != "/elsewhere" {
		t.Fatalf("RootFrom with JIG_HOME = %q, want /elsewhere", got)
	}
}

// TestRootFromFallsBackToTheUserHomesConfigDir: with no JIG_HOME, the root is
// .config/jig under the user home the environment names.
func TestRootFromFallsBackToTheUserHomesConfigDir(t *testing.T) {
	t.Parallel()

	userHome := t.TempDir()
	got, err := RootFrom(envOf(map[string]string{userDirVar(): userHome}))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(userHome, ".config", "jig"); got != want {
		t.Fatalf("RootFrom with only a user home = %q, want %q", got, want)
	}
}

// TestRootFromWithNoHomeAtAllFails: an environment naming neither JIG_HOME nor
// a user home gives no root, with the error os.UserHomeDir gives for it.
func TestRootFromWithNoHomeAtAllFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		t.Skipf("%s has a fixed home directory when the variable is unset", runtime.GOOS)
	}

	got, err := RootFrom(envOf(nil))
	if err == nil {
		t.Fatalf("RootFrom of an empty environment = %q, want an error", got)
	}
	if !strings.Contains(err.Error(), "is not defined") {
		t.Fatalf("RootFrom of an empty environment: error %q, want the variable-not-defined error os.UserHomeDir gives", err)
	}
}

// TestUserDirFromReadsTheVariableTheOSDoes: UserDirFrom answers from the
// environment given, by os.UserHomeDir's own rule.
func TestUserDirFromReadsTheVariableTheOSDoes(t *testing.T) {
	t.Parallel()

	got, err := UserDirFrom(envOf(map[string]string{userDirVar(): "/users/me"}))
	if err != nil || got != "/users/me" {
		t.Fatalf("UserDirFrom = %q, %v, want /users/me", got, err)
	}
	if got, err := UserDirFrom(envOf(map[string]string{"JIG_HOME": "/elsewhere"})); err == nil && runtime.GOOS != "android" && runtime.GOOS != "ios" {
		t.Fatalf("UserDirFrom with no user home variable = %q, want an error", got)
	}
}

// TestUserDirFromAgreesWithTheStdlibOnTheProcess pins that, over the
// process's own environment, UserDirFrom gives exactly what os.UserHomeDir
// gives: the same directory, or the same error. It only reads the process, so
// it is parallel.
func TestUserDirFromAgreesWithTheStdlibOnTheProcess(t *testing.T) {
	t.Parallel()

	got, gotErr := UserDirFrom(os.Getenv)
	want, wantErr := os.UserHomeDir()
	if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Errorf("UserDirFrom(os.Getenv) = %q, %v, want os.UserHomeDir's %q, %v", got, gotErr, want, wantErr)
	}
}
