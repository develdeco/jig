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
		t.Errorf("Asks[r1-f4] decided, want undecided (no workspace)")
	}
	if !strings.Contains(out.String(), "--yes") {
		t.Fatalf("stdout missing the --yes note line:\n%s", out.String())
	}
}

// TestTriageForNoteNamesWhyWhenNothingIsLeft pins the note line for a
// round with nothing left for a human: it still names why no prompt ran.
func TestTriageForNoteNamesWhyWhenNothingIsLeft(t *testing.T) {
	for _, tc := range []struct {
		yes  bool
		want string
	}{
		{true, "triage (--yes): kept every fix and buildable ask\n"},
		{false, "triage (stdin is not a terminal): kept every fix and buildable ask\n"},
	} {
		var out bytes.Buffer
		in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
		triageFor(tc.yes, strings.NewReader(""), &out)(in)
		if out.String() != tc.want {
			t.Errorf("yes=%v: note = %q, want %q", tc.yes, out.String(), tc.want)
		}
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

// TestTriageForYesNothingToTriagePrintsNoNoteLine pins the rule that a
// round that routed no fix, ask or note at all (e.g. a dispatched reviewer
// round with nothing new to report) must not print a triage note line -
// there was nothing to triage. On this path triageFor writes nothing at
// all, so the assertion is on stdout being empty, not on a substring: the
// note's own "triage (<why>):" prefix is never a bare "triage:", so a
// substring check for that text would pass here whether or not a note
// line was actually printed.
func TestTriageForYesNothingToTriagePrintsNoNoteLine(t *testing.T) {
	var out bytes.Buffer
	f := triageFor(true, strings.NewReader(""), &out)
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
	var out bytes.Buffer
	f := triageFor(true, strings.NewReader(""), &out)
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
	var out bytes.Buffer
	f := triageFor(true, strings.NewReader(""), &out)
	f(sampleTriageInput()) // r1-f4 has no workspace
	const want = "triage (--yes): kept every fix and buildable ask; 1 ask(s) left for a human\n"
	if out.String() != want {
		t.Fatalf("note = %q, want %q", out.String(), want)
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

// --- interactiveTriage: fix batch prompt ------------------------------------

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

// TestInteractiveTriageFixBatchDismissListSeparators pins that the batch
// dismiss list accepts a comma, a space, or a comma-and-space between ids
// alike (the prompt advertises "list ids", not one specific separator).
func TestInteractiveTriageFixBatchDismissListSeparators(t *testing.T) {
	for _, line := range []string{"r1-f1,r1-f2", "r1-f1 r1-f2", "r1-f1, r1-f2"} {
		t.Run(line, func(t *testing.T) {
			var out bytes.Buffer
			in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
			res := interactiveTriage(in, strings.NewReader(line+"\n"), &out)
			if !res.DismissedFixIDs["r1-f1"] || !res.DismissedFixIDs["r1-f2"] {
				t.Fatalf("input %q: DismissedFixIDs = %v, want both r1-f1 and r1-f2", line, res.DismissedFixIDs)
			}
		})
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
	// The whole note line, so what jig did with the remaining fixes is
	// pinned along with the reason, and no undecided-ask count follows it.
	const note = "triage (stdin closed): kept every remaining fix and buildable ask\n"
	if !strings.Contains(out.String(), note) {
		t.Fatalf("stdout missing the EOF note %q:\n%s", note, out.String())
	}
}

// TestInteractiveTriageFixBatchPromptExplainsConsequences pins the rule
// that the fix batch prompt says what each answer does, not only its
// syntax: Enter queues the fix slices, and listing ids dismisses those
// findings for good. Each fragment pairs an answer with its consequence, so
// a prompt that swapped the two would not match; the prompt also carries
// the fix count (DECISIONS.md frees the literal wording but not this answer
// syntax).
func TestInteractiveTriageFixBatchPromptExplainsConsequences(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
	interactiveTriage(in, strings.NewReader("\n"), &out)
	text := out.String()
	for _, want := range []string{"2 fix(es)", "Enter queues fix slice(s)", "list ids to dismiss (gone for good)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fix batch prompt missing %q:\n%s", want, text)
		}
	}
}

// TestInteractiveTriageEOFNoteStartsItsOwnLine pins that the stdin-closed
// note never runs on from the prompt whose read hit EOF: eofNote starts a
// new line before it prints. It looks only at the byte before the note, so
// it does not depend on how the prompt ends (that wording is a demo tape's
// Wait+Line anchor, which a test must not pin).
func TestInteractiveTriageEOFNoteStartsItsOwnLine(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Fixes: sampleTriageInput().Fixes}
	interactiveTriage(in, strings.NewReader(""), &out)
	text := out.String()
	i := strings.Index(text, "triage (stdin closed)")
	if i < 1 || text[i-1] != '\n' {
		t.Fatalf("EOF note does not start its own line:\n%q", text)
	}
}

// --- interactiveTriage: per-ask prompt --------------------------------------

func TestInteractiveTriageAskKeepWithDecision(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}, Manifest: oneOracleManifest()}
	res := interactiveTriage(in, strings.NewReader("k\ngo ahead\n"), &out)
	dec, ok := res.Asks["r1-f3"]
	if !ok || !dec.Keep || dec.Decision != "go ahead" || !dec.Human {
		t.Fatalf("Asks[r1-f3] = %+v, ok=%v, want kept with decision \"go ahead\", human", dec, ok)
	}
}

func TestInteractiveTriageAskKeepWithNoDecisionText(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}, Manifest: oneOracleManifest()}
	res := interactiveTriage(in, strings.NewReader("k\n"), &out)
	dec := res.Asks["r1-f3"]
	if !dec.Keep || dec.Decision != "" {
		t.Fatalf("Asks[r1-f3] = %+v, want kept with no decision text", dec)
	}
}

// The Human check matters as much as Keep here: if Enter stopped being an
// explicit keep, the prompt would reprompt on the same blank line forever
// and eventually hit EOF, which also keeps the ask (auto, not human) -
// checking Keep alone cannot tell that apart from a real, explicit answer.
func TestInteractiveTriageAskEnterAloneKeeps(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}, Manifest: oneOracleManifest()}
	res := interactiveTriage(in, strings.NewReader("\n\n"), &out)
	dec := res.Asks["r1-f3"]
	if !dec.Keep || !dec.Human {
		t.Fatalf("Asks[r1-f3] = %+v, want kept/human (bare Enter is an explicit keep)", dec)
	}
}

// TestInteractiveTriageAskShowsFileLineDetailAndRationale pins the rule
// that a finding is always shown with its rationale: the ask prompt must
// show enough to decide on, not the title alone.
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

// TestInteractiveTriageAskPromptExplainsConsequences pins the rule that the
// per-ask prompt says what each answer does, not only its syntax: keeping
// queues a fix slice and asks for a decision, dismissing means the finding
// is gone for good. Each consequence sits next to the answer that causes
// it, and the token list keeps the same keep-then-dismiss order, so a
// prompt that swapped the two would not match. It answers dismiss ("d") so
// no follow-up decision prompt ever runs: with a keep answer, that second
// prompt's own "decision for ... (optional, Enter to skip)" text would also
// satisfy a check for "decision", so a mutant that dropped the word from
// the ask prompt itself would go uncaught.
func TestInteractiveTriageAskPromptExplainsConsequences(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}, Manifest: oneOracleManifest()}
	interactiveTriage(in, strings.NewReader("d\n"), &out)
	text := out.String()
	for _, want := range []string{"keep (fix slice + decision)", "dismiss (gone for good)", "[k/keep/Enter, n/no/d/dismiss]"} {
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

// TestInteractiveTriageAskNoDismisses pins the rule that "n" and "no" must
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

// TestInteractiveTriageAskAllAdvertisedTokens pins every answer token the
// ask prompt advertises ("[k/keep/Enter, n/no/d/dismiss]"), case
// insensitively: each one decides the ask on the first read (no reprompt)
// and records Human true, keep or dismiss as the token says.
func TestInteractiveTriageAskAllAdvertisedTokens(t *testing.T) {
	for _, tok := range []string{"k", "keep", "K", "KEEP", ""} {
		name := tok
		if name == "" {
			name = "Enter"
		}
		t.Run("keep/"+name, func(t *testing.T) {
			var out bytes.Buffer
			in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}, Manifest: oneOracleManifest()}
			res := interactiveTriage(in, strings.NewReader(tok+"\n\n"), &out)
			dec, ok := res.Asks["r1-f3"]
			if !ok || !dec.Keep || !dec.Human {
				t.Fatalf("token %q: Asks[r1-f3] = %+v, ok=%v, want kept/human", tok, dec, ok)
			}
			if strings.Contains(out.String(), "please answer keep") {
				t.Fatalf("token %q: unexpected reprompt:\n%s", tok, out.String())
			}
		})
	}
	for _, tok := range []string{"n", "no", "d", "dismiss", "D", "DISMISS"} {
		t.Run("dismiss/"+tok, func(t *testing.T) {
			var out bytes.Buffer
			in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{{ID: "r1-f3", Workspace: "alpha", Title: "t"}}}
			res := interactiveTriage(in, strings.NewReader(tok+"\n"), &out)
			dec, ok := res.Asks["r1-f3"]
			if !ok || dec.Keep || !dec.Human {
				t.Fatalf("token %q: Asks[r1-f3] = %+v, ok=%v, want dismissed/human", tok, dec, ok)
			}
			if strings.Contains(out.String(), "please answer keep") {
				t.Fatalf("token %q: unexpected reprompt:\n%s", tok, out.String())
			}
		})
	}
}

// TestInteractiveTriageAskUnrecognizedAnswerReprompts pins the rule that
// free text that is not one of the keep/dismiss tokens is never read as an
// implicit keep-with-decision; it reprompts until a real answer arrives.
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

func TestInteractiveTriageAskWorkspacePromptForNoWorkspaceAsk(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks: []verifydeliver.Finding{{ID: "r1-f4", Title: "t"}}, // no Workspace
		Manifest: manifest.Manifest{
			Oracles:    map[string]string{"test": "true"},
			Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "beta"}},
		},
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
		Asks: []verifydeliver.Finding{{ID: "r1-f4", Title: "t"}},
		Manifest: manifest.Manifest{
			Oracles:    map[string]string{"test": "true"},
			Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}},
		},
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

// TestInteractiveTriageAskOraclePromptForStaleOracle covers the oracle half
// of the build-target rule at a terminal: a kept ask whose recorded oracle
// is no longer a manifest oracle (the manifest changed between rounds) is
// prompted for one from the manifest's current oracle names, the same way a
// no-workspace ask is prompted for a workspace, and the chosen oracle
// reaches the triage result.
func TestInteractiveTriageAskOraclePromptForStaleOracle(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks: []verifydeliver.Finding{{ID: "r1-f4", Workspace: "alpha", Oracle: "old", Title: "t"}},
		Manifest: manifest.Manifest{
			Oracles: map[string]string{"test": "true", "lint": "true"}, // "old" is neither
		},
	}
	res := interactiveTriage(in, strings.NewReader("k\nkeep it\nlint\n"), &out)
	dec, ok := res.Asks["r1-f4"]
	if !ok || !dec.Keep || dec.Oracle != "lint" || dec.Decision != "keep it" {
		t.Fatalf("Asks[r1-f4] = %+v, ok=%v, want kept with oracle lint and decision \"keep it\"", dec, ok)
	}
	if !strings.Contains(out.String(), "oracle is missing or no longer in the manifest") {
		t.Fatalf("stdout missing the oracle prompt:\n%s", out.String())
	}
}

func TestInteractiveTriageAskOraclePromptRejectsUnknownName(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks: []verifydeliver.Finding{{ID: "r1-f4", Workspace: "alpha", Title: "t"}},
		Manifest: manifest.Manifest{
			Oracles: map[string]string{"test": "true", "lint": "true"},
		},
	}
	res := interactiveTriage(in, strings.NewReader("k\n\nbogus\nlint\n"), &out)
	dec := res.Asks["r1-f4"]
	if !dec.Keep || dec.Oracle != "lint" {
		t.Fatalf("Asks[r1-f4] = %+v, want kept with oracle lint after the reprompt", dec)
	}
	if !strings.Contains(out.String(), `unknown oracle "bogus"`) {
		t.Fatalf("stdout missing the unknown-oracle message:\n%s", out.String())
	}
}

func TestInteractiveTriageAskEOFOnOraclePromptLeavesItUndecided(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks: []verifydeliver.Finding{{ID: "r1-f4", Workspace: "alpha", Title: "t"}},
		Manifest: manifest.Manifest{
			Oracles: map[string]string{"test": "true", "lint": "true"},
		},
	}
	res := interactiveTriage(in, strings.NewReader("k\n"), &out) // EOF right at the oracle prompt
	if _, ok := res.Asks["r1-f4"]; ok {
		t.Errorf("Asks[r1-f4] decided, want undecided (EOF cannot supply an oracle judgment)")
	}
}

// TestInteractiveTriageAskBothPromptsForAFindingMissingWorkspaceAndOracle
// covers an ask missing both parts of its build target at once: the human
// is prompted for the workspace first, then the oracle, and a kept answer
// to both reaches the triage result.
func TestInteractiveTriageAskBothPromptsForAFindingMissingWorkspaceAndOracle(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks: []verifydeliver.Finding{{ID: "r1-f4", Title: "t"}}, // no Workspace, no Oracle
		Manifest: manifest.Manifest{
			Oracles:    map[string]string{"test": "true", "lint": "true"},
			Workspaces: []manifest.Workspace{{ID: "alpha", Path: "alpha"}},
		},
	}
	res := interactiveTriage(in, strings.NewReader("k\n\nalpha\nlint\n"), &out)
	dec, ok := res.Asks["r1-f4"]
	if !ok || !dec.Keep || dec.Workspace != "alpha" || dec.Oracle != "lint" {
		t.Fatalf("Asks[r1-f4] = %+v, ok=%v, want kept in workspace alpha with oracle lint", dec, ok)
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
		t.Errorf("Asks[r1-f4] decided, want undecided (EOF cannot supply a workspace judgment)")
	}
}

func TestInteractiveTriageAskEOFKeepsRemainingWorkspaceAsksAsAuto(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{Asks: []verifydeliver.Finding{
		{ID: "r1-f3", Workspace: "alpha", Title: "t1"},
		{ID: "r1-f4", Workspace: "beta", Title: "t2"},
	}, Manifest: oneOracleManifest()}
	res := interactiveTriage(in, strings.NewReader(""), &out) // EOF immediately
	for _, id := range []string{"r1-f3", "r1-f4"} {
		dec, ok := res.Asks[id]
		if !ok || !dec.Keep || dec.Human {
			t.Errorf("Asks[%s] = %+v, ok=%v, want kept/auto", id, dec, ok)
		}
	}
}

// TestInteractiveTriageAskEOFDoesNotAutoKeepAStaleOracleAsk pins the other
// half of the same rule: stdin closing auto-keeps only an ask whose build
// target already resolves in full. A workspace alone is not enough once the
// oracle rule matters too - a stale oracle must leave the ask undecided at
// EOF the same way a missing workspace already does, not be silently
// dropped as though the human had agreed with a default nobody chose.
func TestInteractiveTriageAskEOFDoesNotAutoKeepAStaleOracleAsk(t *testing.T) {
	var out bytes.Buffer
	in := verifydeliver.TriageInput{
		Asks: []verifydeliver.Finding{{ID: "r1-f4", Workspace: "alpha", Oracle: "old", Title: "t"}},
		Manifest: manifest.Manifest{
			Oracles: map[string]string{"test": "true", "lint": "true"}, // "old" is neither
		},
	}
	res := interactiveTriage(in, strings.NewReader(""), &out) // EOF immediately
	if _, ok := res.Asks["r1-f4"]; ok {
		t.Error("Asks[r1-f4] decided, want undecided (a stale oracle needs a human's choice, EOF cannot supply it)")
	}
	// The whole note line: the reason, what jig did with what it could keep,
	// and the undecided count together.
	const note = "triage (stdin closed): kept every remaining fix and buildable ask; 1 ask(s) left for a human\n"
	if !strings.Contains(out.String(), note) {
		t.Fatalf("stdout missing the EOF note %q:\n%s", note, out.String())
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
