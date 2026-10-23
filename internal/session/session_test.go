package session

import (
	"strings"
	"testing"
)

// TestNewFailsClosedWhenEnvIsSetOnABackendThatCannotApplyIt pins New's own
// guard: fake does not read Options.Env at all - a caller that filtered its
// own environment before handing it to session.New must find out at
// construction time that this backend would have ignored it, not have it
// silently dispatch under a different environment than the one it asked
// for. The error names both the option and the backend.
func TestNewFailsClosedWhenEnvIsSetOnABackendThatCannotApplyIt(t *testing.T) {
	env := []string{"FOO=bar"}
	for _, name := range []string{"fake"} {
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

// TestNewRefusesABackendItDoesNotKnow: herdr was a backend once, and a name
// that is no backend is refused, not guessed.
func TestNewRefusesABackendItDoesNotKnow(t *testing.T) {
	for _, name := range []string{"herdr", "nope", ""} {
		if _, err := New(name, Options{}); err == nil || !strings.Contains(err.Error(), "unknown backend") {
			t.Errorf("New(%q) = %v, want an unknown backend error", name, err)
		}
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
