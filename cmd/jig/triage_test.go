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
	res := interactiveTriage(in, strings.NewReader("go ahead\n"), &out)
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

func TestInteractiveTriageAskDismiss(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
	res := interactiveTriage(in, strings.NewReader("d\n"), &out)
	dec, ok := res.Asks["r1-f3"]
	if !ok || dec.Keep || !dec.Human {
		t.Fatalf("Asks[r1-f3] = %+v, ok=%v, want dismissed/human", dec, ok)
	}
}

func TestInteractiveTriageAskWorkspacePromptForQ1(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks:     []verifydeliver.Finding{{ID: "r1-f4", Title: "t"}}, // no Workspace
		Manifest: manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "beta"}}},
	}
	res := interactiveTriage(in, strings.NewReader("keep it\nbeta\n"), &out)
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
	res := interactiveTriage(in, strings.NewReader("k\nbogus\nalpha\n"), &out)
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
	in := verifydeliver.TriageInput{Notes: []verifydeliver.Finding{{ID: "r1-f5", Risk: "low", Title: "just fyi"}}}
	interactiveTriage(in, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "just fyi") {
		t.Fatalf("stdout missing the note:\n%s", out.String())
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
