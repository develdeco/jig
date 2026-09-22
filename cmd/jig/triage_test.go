package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/verifydeliver"
)

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
		Manifest: manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "beta"}}},
	}
}

// --- triageFor selection ----------------------------------------------------

func TestTriageForYesKeepsEverythingWithoutTouchingStdin(t *testing.T) {
	var out bytes.Buffer
	f := triageFor(true, strings.NewReader(""), &out)
	res := f(sampleTriageInput())
	if len(res.DismissedFixIDs) != 0 {
		t.Errorf("DismissedFixIDs = %v, want none", res.DismissedFixIDs)
	}
	if !res.Asks["r1-f3"].Keep {
		t.Errorf("Asks[r1-f3] = %+v, want kept (has a workspace)", res.Asks["r1-f3"])
	}
	if _, ok := res.Asks["r1-f4"]; ok {
		t.Errorf("Asks[r1-f4] decided, want undecided (Q1: no workspace)")
	}
	if !strings.Contains(out.String(), "--yes") {
		t.Fatalf("stdout missing the --yes note line:\n%s", out.String())
	}
}

func TestTriageForNonTerminalNeverPrompts(t *testing.T) {
	var out bytes.Buffer
	f := triageFor(false, strings.NewReader("n\nn\nn\n"), &out)
	res := f(sampleTriageInput())
	if len(res.DismissedFixIDs) != 0 {
		t.Errorf("DismissedFixIDs = %v, want none (non-terminal never dismisses)", res.DismissedFixIDs)
	}
	if !strings.Contains(out.String(), "not a terminal") {
		t.Fatalf("stdout missing the non-terminal note line:\n%s", out.String())
	}
}

// TestTriageForYesNothingToTriagePrintsNoNoteLine pins F16/N-6: a round
// that routed no fix, ask or note at all (e.g. a dispatched reviewer round
// with nothing new to report) must not print a triage note line - there
// was nothing to triage.
func TestTriageForYesNothingToTriagePrintsNoNoteLine(t *testing.T) {
	var out bytes.Buffer
	f := triageFor(true, strings.NewReader(""), &out)
	f(verifydeliver.TriageInput{})
	if strings.Contains(out.String(), "triage:") {
		t.Fatalf("stdout has a triage note line for nothing to triage:\n%s", out.String())
	}
}

// TestTriageForYesUndecidedAskDoesNotClaimKept pins F16/N-6: the note line
// must never say every ask was kept when a no-workspace ask (Q1) was left
// undecided; it says how many are left for a human instead.
func TestTriageForYesUndecidedAskDoesNotClaimKept(t *testing.T) {
	var out bytes.Buffer
	f := triageFor(true, strings.NewReader(""), &out)
	f(sampleTriageInput()) // r1-f4 has no workspace
	line := out.String()
	if strings.Contains(line, "kept every fix and workspace ask (--yes)") {
		t.Fatalf("stdout falsely claims every ask was kept:\n%s", line)
	}
	if !strings.Contains(line, "1 ask(s) with no workspace left for a human") {
		t.Fatalf("stdout missing the undecided-ask count:\n%s", line)
	}
}

func TestTriageForTerminalScriptedReachesInteractivePath(t *testing.T) {
	prev := stdinIsTerminal
	stdinIsTerminal = func(r io.Reader) bool { return true }
	defer func() { stdinIsTerminal = prev }()

	var out bytes.Buffer
	f := triageFor(false, strings.NewReader("\nk\nd\nk\n"), &out)
	f(sampleTriageInput())
	if strings.Contains(out.String(), "not a terminal") || strings.Contains(out.String(), "--yes") {
		t.Fatalf("expected the interactive path, got a non-interactive note line:\n%s", out.String())
	}
}

// --- interactiveTriage: fix batch prompt (D-2) ------------------------------

func TestInteractiveTriageFixBatchEnterAcceptsAll(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
	res := interactiveTriage(in, strings.NewReader("\n"), &out)
	if len(res.DismissedFixIDs) != 0 {
		t.Errorf("DismissedFixIDs = %v, want none", res.DismissedFixIDs)
	}
	if !res.FixHuman {
		t.Error("FixHuman = false, want true (a person was prompted and answered)")
	}
}

func TestInteractiveTriageFixBatchDismissByID(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
	res := interactiveTriage(in, strings.NewReader("r1-f2\n"), &out)
	if !res.DismissedFixIDs["r1-f2"] || res.DismissedFixIDs["r1-f1"] {
		t.Errorf("DismissedFixIDs = %v, want only r1-f2", res.DismissedFixIDs)
	}
}

func TestInteractiveTriageFixBatchUnknownIDReprompts(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
	res := interactiveTriage(in, strings.NewReader("bogus-id\nr1-f1\n"), &out)
	if !res.DismissedFixIDs["r1-f1"] {
		t.Errorf("DismissedFixIDs = %v, want r1-f1 after the reprompt", res.DismissedFixIDs)
	}
	if !strings.Contains(out.String(), `unknown fix id "bogus-id"`) {
		t.Fatalf("stdout missing the unknown-id message:\n%s", out.String())
	}
}

func TestInteractiveTriageFixBatchEOFKeepsAllAsAuto(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
	res := interactiveTriage(in, strings.NewReader(""), &out)
	if len(res.DismissedFixIDs) != 0 {
		t.Errorf("DismissedFixIDs = %v, want none", res.DismissedFixIDs)
	}
	if res.FixHuman {
		t.Error("FixHuman = true, want false (stdin closed before anyone answered)")
	}
	if !strings.Contains(out.String(), "stdin closed") {
		t.Fatalf("stdout missing the EOF note:\n%s", out.String())
	}
}

// --- interactiveTriage: per-ask prompt --------------------------------------

func TestInteractiveTriageAskKeepWithDecision(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
	res := interactiveTriage(in, strings.NewReader("k\ngo ahead\n"), &out)
	dec, ok := res.Asks["r1-f3"]
	if !ok || !dec.Keep || dec.Decision != "go ahead" || !dec.Human {
		t.Fatalf("Asks[r1-f3] = %+v, ok=%v, want kept with decision \"go ahead\", human", dec, ok)
	}
}

func TestInteractiveTriageAskKeepWithNoDecisionText(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
	res := interactiveTriage(in, strings.NewReader("k\n"), &out)
	dec := res.Asks["r1-f3"]
	if !dec.Keep || dec.Decision != "" {
		t.Fatalf("Asks[r1-f3] = %+v, want kept with no decision text", dec)
	}
}

func TestInteractiveTriageAskEnterAloneKeeps(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
	res := interactiveTriage(in, strings.NewReader("\n\n"), &out)
	dec := res.Asks["r1-f3"]
	if !dec.Keep {
		t.Fatalf("Asks[r1-f3] = %+v, want kept (bare Enter is an explicit keep)", dec)
	}
}

// TestInteractiveTriageAskShowsFileLineDetailAndRationale pins F10b (design
// 6.4: "each with its rationale"): the ask prompt must show enough to
// decide on, not the title alone.
func TestInteractiveTriageAskShowsFileLineDetailAndRationale(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{
		ID: "r1-f3", Workspace: "alpha", File: "beta/beta.go", Line: 4,
		Title: "ASK-TITLE", Detail: "ASK-DETAIL", Risk: "high", RiskRationale: "ASK-RATIONALE",
	}}}
	interactiveTriage(in, strings.NewReader("d\n"), &out)
	text := out.String()
	for _, want := range []string{"beta/beta.go:4", "ASK-DETAIL", "ASK-RATIONALE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ask prompt missing %q:\n%s", want, text)
		}
	}
}

func TestInteractiveTriageAskDismiss(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
	res := interactiveTriage(in, strings.NewReader("d\n"), &out)
	dec, ok := res.Asks["r1-f3"]
	if !ok || dec.Keep || !dec.Human {
		t.Fatalf("Asks[r1-f3] = %+v, ok=%v, want dismissed/human", dec, ok)
	}
}

// TestInteractiveTriageAskNoDismisses pins F17/N-10: "n" and "no" must
// dismiss, not silently keep with "n"/"no" as the decision text.
func TestInteractiveTriageAskNoDismisses(t *testing.T) {
	for _, word := range []string{"n", "no", "No", "N"} {
		t.Run(word, func(t *testing.T) {
			var out bytes.Buffer
			in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
			res := interactiveTriage(in, strings.NewReader(word+"\n"), &out)
			dec, ok := res.Asks["r1-f3"]
			if !ok || dec.Keep {
				t.Fatalf("Asks[r1-f3] answered %q = %+v, ok=%v, want dismissed", word, dec, ok)
			}
		})
	}
}

// TestInteractiveTriageAskUnrecognizedAnswerReprompts pins F17: free text
// that is not one of the keep/dismiss tokens is never read as an implicit
// keep-with-decision; it reprompts until a real answer arrives.
func TestInteractiveTriageAskUnrecognizedAnswerReprompts(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
	res := interactiveTriage(in, strings.NewReader("go ahead\nd\n"), &out)
	dec, ok := res.Asks["r1-f3"]
	if !ok || dec.Keep {
		t.Fatalf("Asks[r1-f3] = %+v, ok=%v, want dismissed after the reprompt", dec, ok)
	}
	if !strings.Contains(out.String(), "please answer keep") {
		t.Fatalf("stdout missing the reprompt message:\n%s", out.String())
	}
}

func TestInteractiveTriageAskWorkspacePromptForQ1(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks:     []verifydeliver.Finding{{ID: "r1-f4", Title: "t"}}, // no Workspace
		Manifest: manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "beta"}}},
	}
	res := interactiveTriage(in, strings.NewReader("k\nkeep it\nbeta\n"), &out)
	dec, ok := res.Asks["r1-f4"]
	if !ok || !dec.Keep || dec.Workspace != "beta" || dec.Decision != "keep it" {
		t.Fatalf("Asks[r1-f4] = %+v, ok=%v, want kept in workspace beta with decision \"keep it\"", dec, ok)
	}
	if !strings.Contains(out.String(), "no declared workspace") {
		t.Fatalf("stdout missing the workspace prompt:\n%s", out.String())
	}
}

func TestInteractiveTriageAskWorkspacePromptRejectsUnknownID(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks:     []verifydeliver.Finding{{ID: "r1-f4", Title: "t"}},
		Manifest: manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}}},
	}
	res := interactiveTriage(in, strings.NewReader("k\n\nbogus\nalpha\n"), &out)
	dec := res.Asks["r1-f4"]
	if !dec.Keep || dec.Workspace != "alpha" {
		t.Fatalf("Asks[r1-f4] = %+v, want kept in workspace alpha after the reprompt", dec)
	}
	if !strings.Contains(out.String(), `unknown workspace "bogus"`) {
		t.Fatalf("stdout missing the unknown-workspace message:\n%s", out.String())
	}
}

func TestInteractiveTriageAskEOFOnWorkspacePromptLeavesItUndecided(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks:     []verifydeliver.Finding{{ID: "r1-f4", Title: "t"}},
		Manifest: manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}}},
	}
	res := interactiveTriage(in, strings.NewReader("k\n"), &out) // EOF right at the workspace prompt
	if _, ok := res.Asks["r1-f4"]; ok {
		t.Errorf("Asks[r1-f4] decided, want undecided (Q1: EOF cannot supply a workspace judgment)")
	}
}

func TestInteractiveTriageAskEOFKeepsRemainingWorkspaceAsksAsAuto(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{
		{ID: "r1-f3", Workspace: "alpha", Title: "t1"},
		{ID: "r1-f4", Workspace: "beta", Title: "t2"},
	}}
	res := interactiveTriage(in, strings.NewReader(""), &out) // EOF immediately
	for _, id := range []string{"r1-f3", "r1-f4"} {
		dec, ok := res.Asks[id]
		if !ok || !dec.Keep || dec.Human {
			t.Errorf("Asks[%s] = %+v, ok=%v, want kept/auto", id, dec, ok)
		}
	}
}

// --- notes are only ever listed ---------------------------------------------

func TestInteractiveTriageListsNotes(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Notes: []verifydeliver.Finding{{ID: "r1-f5", File: "alpha/percent.go", Line: 3, Risk: "low", Title: "just fyi", RiskRationale: "NOTE-RATIONALE"}}}
	interactiveTriage(in, strings.NewReader(""), &out)
	text := out.String()
	for _, want := range []string{"just fyi", "alpha/percent.go:3", "NOTE-RATIONALE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("notes table missing %q:\n%s", want, text)
		}
	}
}

// --- stdinIsTerminal (generic, per-GOOS isTerminalFile) ---------------------

func TestStdinIsTerminalNonFile(t *testing.T) {
	if stdinIsTerminal(strings.NewReader("")) {
		t.Fatal("stdinIsTerminal(strings.Reader) = true, want false")
	}
	if stdinIsTerminal(&bytes.Buffer{}) {
		t.Fatal("stdinIsTerminal(bytes.Buffer) = true, want false")
	}
}

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

func TestStdinIsTerminalDevNull(t *testing.T) {
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
