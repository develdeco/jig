package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// A hook must not fail the harness whatever it is handed: it exits 0, prints
// nothing a harness could take for context, and leaves the reason in the log.
func TestObserveNeverFailsTheHarness(t *testing.T) {
	t.Parallel()
	fx, e, rec := observeRig(t)
	claude := []string{"--harness", "claude"}
	cases := []struct {
		name   string
		stdin  string
		args   []string
		logged bool // a failure, which the log must say; the rest are not
	}{
		{"garbage", "this is not json", claude, true},
		{"empty stdin", "", claude, true},
		{"binary", "\x00\x01\xff\xfe", claude, true},
		{"an array", "[1,2,3]", claude, true},
		{"a truncated object", `{"hook_event_name":"Stop","session_id":`, claude, true},
		{"an unknown harness", `{}`, []string{"--harness", "emacs"}, true},
		{"no harness and no event", `{}`, nil, true},
		{"an unknown flag", `{}`, append([]string{"--no-such-flag"}, claude...), true},
		{"a stray argument", `{}`, append([]string{"extra"}, claude...), true},
		{"an event that is none", "", []string{"--event", "nonsense"}, true},
		{"an exit that is no number", "", []string{"--event", "tool", "--exit", "x"}, true},
		{"an event of another harness shape", `{"hook_event_name":"sessionStart"}`, []string{"--harness", "gemini"}, false},
		{"a directory that is nowhere", `{"hook_event_name":"Stop"}`, []string{"--harness", "claude", "--cwd", filepath.Join(fx.Dir, "does", "not", "exist")}, false},
	}
	failures := 0
	for _, c := range cases {
		out, code := runMain(t, e, c.stdin, append([]string{"observe"}, c.args...)...)
		if code != 0 || out != "" {
			t.Errorf("%s: exit %d, output %q, want a silent exit 0", c.name, code, out)
		}
		if c.logged {
			failures++
		}
	}
	data, err := os.ReadFile(filepath.Join(home.LogsDir(fx.Home), "observe.log"))
	if err != nil {
		t.Fatalf("the log of failed hooks: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != failures {
		t.Errorf("observe.log has %d lines for %d failures:\n%s", got, failures, data)
	}
	if len(rec.Specs()) != 0 {
		t.Errorf("a malformed call started %v", rec.Specs())
	}
	if lines := ledgerEvents(t, fx.Home, journal.EventSessionStart); len(lines) != 0 {
		t.Errorf("a malformed call wrote %+v", lines)
	}
}

func TestObserveIgnoresAnEventOfTheHarnessItDoesNotActOn(t *testing.T) {
	t.Parallel()
	fx, e, _ := observeRig(t)
	out, code := runMain(t, e, hookPayload(t, fx, "Notification", nil), "observe", "--harness", "claude")
	if out != "" || code != 0 {
		t.Errorf("exit %d, output %q, want silence", code, out)
	}
	if _, err := os.Stat(filepath.Join(home.LogsDir(fx.Home), "observe.log")); !os.IsNotExist(err) {
		t.Errorf("an event jig does not act on is no failure, yet the log exists (stat: %v)", err)
	}
}

// A person who runs it by hand is at a terminal, where the reason belongs.
func TestObserveTellsAPersonAtATerminalWhatIsWrong(t *testing.T) {
	t.Parallel()
	_, e, _ := observeRig(t)
	e.stdinIsTerminal = func(io.Reader) bool { return true }
	for name, args := range map[string][]string{
		"no harness":        {"observe"},
		"unknown harness":   {"observe", "--harness", "emacs"},
		"waits on terminal": {"observe", "--harness", "claude"},
	} {
		out, code := runMain(t, e, "", args...)
		if code == 0 || !strings.Contains(out, "observe:") {
			t.Errorf("%s: exit %d, output %q, want an error a person can read", name, code, out)
		}
	}
}
