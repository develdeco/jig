package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

func findings3() []verifydeliver.Finding {
	return []verifydeliver.Finding{
		{ID: "r1-f1", Class: "mechanical", Title: "typo in doc comment", Workspace: "alpha"},
		{ID: "r1-f2", Class: "intent", Title: "nil deref on empty input", Workspace: "alpha"},
		{ID: "r1-f3", Class: "intent", Title: "missing tenant filter", Workspace: "beta"},
	}
}

// TestTriageForYes checks that --yes keeps every finding and prints exactly
// one note line, without ever consulting stdin.
func TestTriageForYes(t *testing.T) {
	var out bytes.Buffer
	f := triageFor(true, strings.NewReader(""), &out)
	kept := f(findings3())
	if len(kept) != 3 {
		t.Fatalf("kept = %d findings, want 3", len(kept))
	}
	if !strings.Contains(out.String(), "triage: kept all 3 findings (--yes)") {
		t.Fatalf("stdout missing --yes note line:\n%s", out.String())
	}
}

// TestTriageForNonTerminal checks that a non-terminal stdin (the default
// for stdinIsTerminal against a strings.Reader) keeps every finding without
// prompting, printing the non-terminal note line.
func TestTriageForNonTerminal(t *testing.T) {
	var out bytes.Buffer
	f := triageFor(false, strings.NewReader("n\nn\nn\n"), &out)
	kept := f(findings3())
	if len(kept) != 3 {
		t.Fatalf("kept = %d findings, want 3 (non-terminal stdin must never prompt)", len(kept))
	}
	if !strings.Contains(out.String(), "triage: kept all 3 findings (stdin is not a terminal)") {
		t.Fatalf("stdout missing non-terminal note line:\n%s", out.String())
	}
}

// TestTriageForTerminalScripted overrides stdinIsTerminal to force the
// interactive path over a scripted piped reader, proving triageFor's
// selection logic reaches interactiveTriage when stdin reports as a
// terminal.
func TestTriageForTerminalScripted(t *testing.T) {
	prev := stdinIsTerminal
	stdinIsTerminal = func(r io.Reader) bool { return true }
	defer func() { stdinIsTerminal = prev }()

	var out bytes.Buffer
	f := triageFor(false, strings.NewReader("y\nn\ny\n"), &out)
	kept := f(findings3())
	if len(kept) != 2 {
		t.Fatalf("kept = %d findings, want 2 (y, n, y)", len(kept))
	}
	if strings.Contains(out.String(), "not a terminal") || strings.Contains(out.String(), "--yes") {
		t.Fatalf("expected the interactive path, got a non-interactive note line:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "triage: kept 2 of 3 findings") {
		t.Fatalf("stdout missing interactive summary line:\n%s", out.String())
	}
}

// TestInteractiveTriageYNMix drives a plain y/n/y sequence and checks the
// exact kept ids and the summary line.
func TestInteractiveTriageYNMix(t *testing.T) {
	var out bytes.Buffer
	kept := interactiveTriage(findings3(), strings.NewReader("y\nn\nyes\n"), &out)
	wantIDs := []string{"r1-f1", "r1-f3"}
	assertKeptIDs(t, kept, wantIDs)
	if !strings.Contains(out.String(), "triage: kept 2 of 3 findings") {
		t.Fatalf("stdout missing summary line:\n%s", out.String())
	}
}

// TestInteractiveTriageKeepRest checks that "A" keeps the current finding
// and every remaining one without further prompts.
func TestInteractiveTriageKeepRest(t *testing.T) {
	var out bytes.Buffer
	kept := interactiveTriage(findings3(), strings.NewReader("n\nA\n"), &out)
	assertKeptIDs(t, kept, []string{"r1-f2", "r1-f3"})
}

// TestInteractiveTriageDismissRest checks that "N" (case-sensitive) dismisses
// the current finding and every remaining one without further prompts.
func TestInteractiveTriageDismissRest(t *testing.T) {
	var out bytes.Buffer
	kept := interactiveTriage(findings3(), strings.NewReader("y\nN\n"), &out)
	assertKeptIDs(t, kept, []string{"r1-f1"})
}

// TestInteractiveTriageGarbageThenY checks that an unrecognized answer
// reprompts the same finding (rather than silently keeping or dismissing
// it) until a valid one arrives.
func TestInteractiveTriageGarbageThenY(t *testing.T) {
	var out bytes.Buffer
	kept := interactiveTriage(findings3()[:1], strings.NewReader("garbage\ny\n"), &out)
	assertKeptIDs(t, kept, []string{"r1-f1"})
	if strings.Count(out.String(), "keep? [y]es") != 2 {
		t.Fatalf("expected 2 prompts (initial + reprompt after garbage), got:\n%s", out.String())
	}
}

// TestInteractiveTriageEmptyLine checks that a bare empty line (just Enter)
// keeps the finding, same as "y".
func TestInteractiveTriageEmptyLine(t *testing.T) {
	var out bytes.Buffer
	kept := interactiveTriage(findings3()[:1], strings.NewReader("\n"), &out)
	assertKeptIDs(t, kept, []string{"r1-f1"})
}

// TestInteractiveTriageEOFMidway checks that stdin closing mid-prompt keeps
// the current and every remaining finding, with a note line, rather than
// looping forever or dismissing by default.
func TestInteractiveTriageEOFMidway(t *testing.T) {
	var out bytes.Buffer
	kept := interactiveTriage(findings3(), strings.NewReader("n\n"), &out)
	assertKeptIDs(t, kept, []string{"r1-f2", "r1-f3"})
	if !strings.Contains(out.String(), "stdin closed") {
		t.Fatalf("stdout missing an EOF note line:\n%s", out.String())
	}
}

// TestStdinIsTerminalNonFile checks that anything other than an *os.File
// (a bytes.Buffer, a strings.Reader) is never treated as a terminal.
func TestStdinIsTerminalNonFile(t *testing.T) {
	if stdinIsTerminal(strings.NewReader("")) {
		t.Fatalf("stdinIsTerminal(strings.Reader) = true, want false")
	}
	if stdinIsTerminal(&bytes.Buffer{}) {
		t.Fatalf("stdinIsTerminal(bytes.Buffer) = true, want false")
	}
}

// TestStdinIsTerminalRegularFile checks that an *os.File backed by a
// regular file (not a character device) is not treated as a terminal.
func TestStdinIsTerminalRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-tty.txt")
	if err := os.WriteFile(path, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if stdinIsTerminal(f) {
		t.Fatalf("stdinIsTerminal(%s) = true, want false", path)
	}
}

func assertKeptIDs(t *testing.T, kept []verifydeliver.Finding, want []string) {
	t.Helper()
	if len(kept) != len(want) {
		t.Fatalf("kept ids = %v, want %v", idsOf(kept), want)
	}
	for i, w := range want {
		if kept[i].ID != w {
			t.Fatalf("kept ids = %v, want %v", idsOf(kept), want)
		}
	}
}

func idsOf(fs []verifydeliver.Finding) []string {
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.ID
	}
	return ids
}
