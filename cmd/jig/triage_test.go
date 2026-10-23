package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// oneOracleManifest is the manifest most interactiveTriage ask tests use
// unless they specifically exercise the oracle-prompt rule: a single oracle
// resolves automatically (there is no choice to make), so these tests can
// focus on the keep/dismiss/decision/workspace mechanics without also
// having to answer an oracle prompt.
func oneOracleManifest() manifest.Manifest {
	return manifest.Manifest{Oracles: map[string]string{"test": "true"}}
}

func sampleTriageInput() verifydeliver.TriageInput {
	return verifydeliver.TriageInput{
		Fixes: []verifydeliver.Finding{
			{ID: "r1-f1", Risk: "high", Title: "fix one", RiskRationale: "r"},
			{ID: "r1-f2", Risk: "low", Title: "fix two", RiskRationale: "r"},
		},
		Asks: []verifydeliver.Finding{
			{ID: "r1-f3", Risk: "high", Title: "ask with workspace", Workspace: "alpha"},
			{ID: "r1-f4", Risk: "medium", Title: "ask with no workspace"},
		},
		Notes: []verifydeliver.Finding{
			{ID: "r1-f5", Risk: "low", Title: "just fyi"},
		},
		Manifest: manifest.Manifest{
			Oracles:    map[string]string{"test": "true"},
			Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "beta"}},
		},
	}
}

// --- triageFor selection ----------------------------------------------------

func TestTriageForYesKeepsEverythingWithoutTouchingStdin(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	f := triageFor(processEnv(), true, strings.NewReader(""), &out)
	res := f(sampleTriageInput())
	if len(res.DismissedFixIDs) != 0 {
		t.Errorf("DismissedFixIDs = %v, want none", res.DismissedFixIDs)
	}
	if !res.Asks["r1-f3"].Keep {
		t.Errorf("Asks[r1-f3] = %+v, want kept (has a workspace)", res.Asks["r1-f3"])
	}
	if _, ok := res.Asks["r1-f4"]; ok {
		t.Errorf("Asks[r1-f4] decided, want undecided (no workspace)")
	}
	if !strings.Contains(out.String(), "--yes") {
		t.Fatalf("stdout missing the --yes note line:\n%s", out.String())
	}
}

// TestTriageForNoteNamesWhyWhenNothingIsLeft pins the note line for a
// round with nothing left for a human: it still names why no prompt ran.
func TestTriageForNoteNamesWhyWhenNothingIsLeft(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		yes  bool
		want string
	}{
		{true, "triage (--yes): kept every fix and buildable ask\n"},
		{false, "triage (jig does not prompt; decide asks with `jig triage`): kept every fix and buildable ask\n"},
	} {
		var out bytes.Buffer
		in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
		triageFor(processEnv(), tc.yes, strings.NewReader(""), &out)(in)
		if out.String() != tc.want {
			t.Errorf("yes=%v: note = %q, want %q", tc.yes, out.String(), tc.want)
		}
	}
}

// TestTriageForNeverPrompts: jig asks nobody anything while a round is
// recorded, at a terminal or not: the stdin it is given is never read, no fix
// is dismissed, and the note names `jig triage` as where an ask is decided.
func TestTriageForNeverPrompts(t *testing.T) {
	t.Parallel()
	for _, e := range []env{processEnv(), processEnv().atTerminal()} {
		var out bytes.Buffer
		stdin := strings.NewReader("n\nn\nn\n")
		f := triageFor(e, false, stdin, &out)
		res := f(sampleTriageInput())
		if len(res.DismissedFixIDs) != 0 {
			t.Errorf("DismissedFixIDs = %v, want none (jig never dismisses a fix on its own)", res.DismissedFixIDs)
		}
		if stdin.Len() != 6 {
			t.Errorf("stdin has %d byte(s) left of 6: triageFor read it", stdin.Len())
		}
		if !strings.Contains(out.String(), "jig does not prompt; decide asks with `jig triage`") {
			t.Fatalf("stdout missing the note line:\n%s", out.String())
		}
	}
}

// TestTriageForYesNothingToTriagePrintsNoNoteLine pins the rule that a
// round that routed no fix, ask or note at all (e.g. a dispatched reviewer
// round with nothing new to report) must not print a triage note line -
// there was nothing to triage. On this path triageFor writes nothing at
// all, so the assertion is on stdout being empty, not on a substring: the
// note's own "triage (<why>):" prefix is never a bare "triage:", so a
// substring check for that text would pass here whether or not a note
// line was actually printed.
func TestTriageForYesNothingToTriagePrintsNoNoteLine(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	f := triageFor(processEnv(), true, strings.NewReader(""), &out)
	f(verifydeliver.TriageInput{})
	if out.Len() != 0 {
		t.Fatalf("stdout has a triage note line for nothing to triage:\n%s", out.String())
	}
}

// TestTriageForYesNotesOnlyPrintsNoNoteLine pins the rule that a note is
// never triaged: a round that routed only notes, no fix or
// ask, must not print a triage note line either, even though its input is
// non-empty. As above, triageFor writes nothing at all on this path, so
// the assertion is on stdout being empty.
func TestTriageForYesNotesOnlyPrintsNoNoteLine(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	f := triageFor(processEnv(), true, strings.NewReader(""), &out)
	f(verifydeliver.TriageInput{Notes: []verifydeliver.Finding{{ID: "r1-f5", Risk: "low", Title: "just fyi"}}})
	if out.Len() != 0 {
		t.Fatalf("stdout has a triage note line for a notes-only round:\n%s", out.String())
	}
}

// TestTriageForYesUndecidedAskDoesNotClaimKept pins the rule that the note
// line must never say every ask was kept when a no-workspace ask was left
// undecided; it says how many are left for a human instead. The whole line
// is compared, so the reason, what jig did with the fixes and the buildable
// asks, and the undecided count are pinned together: a note that reversed
// what happened ("dismissed every fix ..."), dropped the reason, or fell
// back to the count-free shape would not match.
func TestTriageForYesUndecidedAskDoesNotClaimKept(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	f := triageFor(processEnv(), true, strings.NewReader(""), &out)
	f(sampleTriageInput()) // r1-f4 has no workspace
	const want = "triage (--yes): kept every fix and buildable ask; 1 ask(s) left for a human\n"
	if out.String() != want {
		t.Fatalf("note = %q, want %q", out.String(), want)
	}
}

// --- interactiveTriage: fix batch prompt ------------------------------------

// --- interactiveTriage: budget-parked asks ----------------------------------

// --- interactiveTriage: per-ask prompt --------------------------------------

// --- notes are only ever listed ---------------------------------------------

// --- stdinIsTerminal (generic, per-GOOS isTerminalFile) ---------------------

func TestStdinIsTerminalNonFile(t *testing.T) {
	t.Parallel()
	if stdinIsTerminal(strings.NewReader("")) {
		t.Fatal("stdinIsTerminal(strings.Reader) = true, want false")
	}
	if stdinIsTerminal(&bytes.Buffer{}) {
		t.Fatal("stdinIsTerminal(bytes.Buffer) = true, want false")
	}
}

func TestStdinIsTerminalRegularFile(t *testing.T) {
	t.Parallel()
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

func TestStdinIsTerminalDevNull(t *testing.T) {
	t.Parallel()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer f.Close()
	if stdinIsTerminal(f) {
		t.Fatalf("stdinIsTerminal(%s) = true, want false", os.DevNull)
	}
}

func TestStdinIsTerminalPipe(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if stdinIsTerminal(r) {
		t.Fatal("stdinIsTerminal(pipe) = true, want false")
	}
}
