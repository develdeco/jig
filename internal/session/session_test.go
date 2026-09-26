package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestAvailable checks the backend preflight against an empty PATH and a PATH
// holding stand-in programs.
func TestAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, c := range []struct{ goos, name string }{
		{"linux", "headless"}, {"linux", "herdr"}, {"windows", "herdr"},
	} {
		var ae *axi.Error
		if err := available(c.goos, c.name); !errors.As(err, &ae) || ae.Code != "BACKEND_UNAVAILABLE" {
			t.Errorf("available(%s, %s) with an empty PATH = %v, want BACKEND_UNAVAILABLE", c.goos, c.name, err)
		}
	}
	if err := available(runtime.GOOS, "fake"); err != nil {
		t.Errorf("fake backend: %v, want nil", err)
	}

	bin := t.TempDir()
	for _, prog := range []string{"claude", "herdr", "wsl"} {
		name := prog
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte{}, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for _, name := range []string{"headless", "herdr"} {
		if err := available(runtime.GOOS, name); err != nil {
			t.Errorf("available(%s) with the program on PATH: %v", name, err)
		}
	}
}

// TestNewFailsClosedWhenEnvIsSetOnABackendThatCannotApplyIt pins New's own
// guard: herdr never reads Options.Env at all (newHerdrBackend takes opts
// but has no env field), and fake does not either - a caller that filtered
// its own environment before handing it to session.New must find out at
// construction time that this backend would have ignored it, not have it
// silently dispatch under a different environment than the one it asked
// for. The error names both the option and the backend.
func TestNewFailsClosedWhenEnvIsSetOnABackendThatCannotApplyIt(t *testing.T) {
	env := []string{"FOO=bar"}
	for _, name := range []string{"herdr", "fake"} {
		t.Run(name, func(t *testing.T) {
			_, err := New(name, Options{Env: env})
			if err == nil {
				t.Fatalf("New(%s, Options{Env: ...}): want an error, %s cannot apply Env", name, name)
			}
			if !strings.Contains(err.Error(), "Env") || !strings.Contains(err.Error(), name) {
				t.Errorf("New(%s, Options{Env: ...}) error = %q, want it to name both Env and %q", name, err, name)
			}
		})
	}
}

// TestNewAppliesEnvOnHeadlessWithNoError is the positive half of the guard
// above: headless is the one backend that does apply Options.Env, so a
// non-nil Env there must not be refused.
func TestNewAppliesEnvOnHeadlessWithNoError(t *testing.T) {
	if _, err := New("headless", Options{Env: []string{"FOO=bar"}}); err != nil {
		t.Errorf("New(headless, Options{Env: ...}): %v, want no error - headless applies Env", err)
	}
}
