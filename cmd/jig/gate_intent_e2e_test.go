package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// buildFixtureTicket drives fx's ticket through `jig run` to completion via
// Main, exactly as TestGateIntentNoneToExplicitThroughMain's own setup
// does: run 1 pauses at q-001, then --answer finishes it. Without this, the
// ticket never has a build lease (no branch for gate to fetch from), so any
// gate call past the CLI's own flag checks fails on that unrelated,
// unbuilt-ticket error rather than reaching the rule a test means to
// exercise.
func buildFixtureTicket(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	runArgs := []string{"run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code := runMain(t, "", runArgs...)
	if code != 2 {
		t.Fatalf("run 1: exit = %d, want 2 (paused at q-001)\n%s", code, out)
	}
	out, code = runMain(t, "", append(append([]string{}, runArgs...), "--answer", "q-001", "Casual.")...)
	if code != 0 {
		t.Fatalf("run (answer): exit = %d, want 0\n%s", code, out)
	}
}

// TestGateIntentFlagsRefusedAtTheCLI checks the refusals `jig gate` makes
// on its own flags, before anything reaches verifydeliver.Gate. Every case
// runs against one real, built fixture ticket (brief.md still present),
// built once: a refusal writes nothing, so the cases do not disturb each
// other, and each still asserts that it left no intent.md behind. The
// ticket is built, and keeps its brief.md, so that if a rule were ever
// removed the case would fail on a real outcome instead of coincidentally
// failing on the unrelated unbuilt-ticket fetch error an unbuilt fixture
// ticket would produce either way (buildFixtureTicket's own doc comment):
//
//   - both flags: removing the rule lets both reach Gate, which fails on
//     its own INTENT_CONFLICT (the ticket already has a brief.md);
//   - an empty --intent or --doc (the flag explicitly given an empty
//     value): removing the rule leaves the empty value indistinguishable
//     from the flag never having been passed, so a full round would really
//     run to completion against the ticket's own brief.md (exit 0).
func TestGateIntentFlagsRefusedAtTheCLI(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	buildFixtureTicket(t, fx)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	for _, tc := range []struct {
		name string
		args []string
		code string
	}{
		{"intent and doc together", []string{"--intent", "a", "--doc", "b"}, "VALIDATION_ERROR"},
		{"empty intent", []string{"--intent", ""}, "INTENT_EMPTY"},
		{"empty doc", []string{"--doc", ""}, "INTENT_EMPTY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"gate", fx.Ticket}, tc.args...)
			args = append(args, "--early", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
			out, code := runMain(t, "", args...)
			if code == 0 || !strings.Contains(out, "code: "+tc.code) {
				t.Fatalf("gate %q: exit = %d, want a non-zero exit with code: %s:\n%s", tc.args, code, tc.code, out)
			}
			if _, ok, rerr := st.ReadIntent(fx.Ticket); rerr != nil || ok {
				t.Fatalf("ReadIntent after gate %q: ok=%v err=%v, want ok=false (refused before writing intent.md)", tc.args, ok, rerr)
			}
		})
	}
}

// TestGateDocFlagWritesIntentThroughMain pins the CLI's own --doc wiring
// (cmd/jig/gate.go's IntentDoc: *doc): nothing else in this file drives
// --doc through Main, so a wiring bug there - --doc silently never reaching
// GateOpts.IntentDoc - would pass every other test in this package and in
// e2e, cmd/jig/gate_test.go included.
func TestGateDocFlagWritesIntentThroughMain(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// A brief-less ticket, the same as TestGateIntentNoneToExplicitThroughMain:
	// --doc is refused outright on a ticket with a brief.md, so proving the
	// flag's own wiring needs one without.
	if err := os.Remove(filepath.Join(st.TicketDir(fx.Ticket), "brief.md")); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}
	buildFixtureTicket(t, fx)

	docPath := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(docPath, []byte("what the ticket is for"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	out, code := runMain(t, "", "gate", fx.Ticket, "--doc", docPath,
		"--early", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("gate --doc: exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "intent: explicit") {
		t.Fatalf("report missing intent: explicit:\n%s", out)
	}
	in, ok, err := st.ReadIntent(fx.Ticket)
	if err != nil || !ok || in.Text != "what the ticket is for" {
		t.Fatalf("intent.md = %+v ok=%v err=%v, want the doc file's own text", in, ok, err)
	}
}

// TestGateIntentNoneToExplicitThroughMain drives a brief-less ticket
// through `jig gate` twice via Main, exactly as a real terminal session
// would: round 1 with no brief and no --intent prints "intent: none";
// a `jig run` in between works round 1's own fix-1 (the base scenario's
// scripted round) back to green, so round 2 - with --intent, recording
// intent.md and printing "intent: explicit" - needs no --early either: the
// frontier is fully green by the time each gate call runs, matching
// demo/gate-intent.tape's own flow keystroke for keystroke (its `cat` of
// intent.md is the raw file read below). Neither round needs --early for
// that reason; skipping the `jig run` in between would leave fix-1 queued
// through round 2, which still prints "verdict: clean" but then points the
// next-step hint at `jig run` instead of `jig publish` - a mismatched
// frame this test's own final assertion catches.
func TestGateIntentNoneToExplicitThroughMain(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// fixture.Generate always writes a brief.md; remove it so this ticket
	// starts brief-less, the scenario this test demonstrates.
	if err := os.Remove(filepath.Join(st.TicketDir(fx.Ticket), "brief.md")); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}

	runArgs := []string{"run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code := runMain(t, "", runArgs...)
	if code != 2 {
		t.Fatalf("run 1: exit = %d, want 2 (paused at q-001)\n%s", code, out)
	}
	out, code = runMain(t, "", append(append([]string{}, runArgs...), "--answer", "q-001", "Casual.")...)
	if code != 0 {
		t.Fatalf("run (answer): exit = %d, want 0\n%s", code, out)
	}

	gateArgs := func(extra ...string) []string {
		base := []string{"gate", fx.Ticket, "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
		return append(base, extra...)
	}

	out, code = runMain(t, "", gateArgs()...)
	if code != 0 {
		t.Fatalf("gate round 1: exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "intent: none") {
		t.Fatalf("round 1 report missing intent: none:\n%s", out)
	}

	// Round 1's scripted round raised a fix, queuing fix-1: work it back to
	// green before round 2, the same as a real terminal session would (and
	// demo/gate-intent.tape does) rather than gating again over unfinished
	// work.
	out, code = runMain(t, "", runArgs...)
	if code != 0 {
		t.Fatalf("run (fix-1): exit = %d, want 0\n%s", code, out)
	}

	out, code = runMain(t, "", gateArgs("--intent", "make the greeting warmer")...)
	if code != 0 {
		t.Fatalf("gate round 2 (--intent): exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "intent: explicit") {
		t.Fatalf("round 2 report missing intent: explicit:\n%s", out)
	}

	in, ok, err := st.ReadIntent(fx.Ticket)
	if err != nil || !ok || in.Text != "make the greeting warmer" {
		t.Fatalf("intent.md = %+v ok=%v err=%v, want the recorded explicit text", in, ok, err)
	}
	// The file's own bytes, the frame demo/gate-intent.tape's `cat` shows on
	// camera: a front matter naming the provenance the report just printed,
	// then the flag's own text.
	raw, err := os.ReadFile(st.IntentPath(fx.Ticket))
	if err != nil {
		t.Fatalf("read intent.md: %v", err)
	}
	if want := "---\nsource: explicit\n---\nmake the greeting warmer\n"; string(raw) != want {
		t.Fatalf("intent.md = %q, want %q", raw, want)
	}

	// The frontier is fully green and round 2 was clean, so the next-step
	// hint must point at publish, not run - the exact frame
	// demo/gate-intent.tape's own final command shows on camera.
	out, code = runMain(t, "", "status", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("status: exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "Run `jig publish "+fx.Ticket+"` to open or update the PR") {
		t.Fatalf("status hint after a clean round 2 does not point at publish:\n%s", out)
	}
}
