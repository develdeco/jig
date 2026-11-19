package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// sha256HexFile returns the sha256 hex digest of path's exact on-disk
// bytes, computed independently of anything under test: a test pinning
// report.yaml's sha256 against this catches a hash that quietly drifts
// from the file a reviewer actually reads (for example hashing the wrong
// field, or a parsed/normalized stand-in for the file's own bytes).
func sha256HexFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// alwaysCleanSource is a GateSource stub that always reports a clean
// round, for tests that only care about the frontier/model/oracle path.
type alwaysCleanSource struct{}

func (alwaysCleanSource) Round(RoundInput) (Round, bool, error) { return Round{}, false, nil }

// TestGateCleanRound2 is the gate's one scripted chain, on a build with its
// real oracles: round 1 finds the scenario's fix slice, the fix is built, and
// round 2 is clean. Every rule of a scripted round that needs a whole ticket
// is a step of it, so the ticket is built once:
//
//   - round 1 writes its files and its receipt, appends the fix slice queued
//     with the round it came from, and journals the round (and names no branch,
//     since a ticket on its own default branch adopted nothing);
//   - the scripted source's fix slice counts toward the fix budget, since
//     report.yaml lists it as a reviewer round's own routing does;
//   - the brief is the intent, ahead of a stale intent.md: resolveIntent must
//     check brief.md first, never the other way around. writeExplicitIntent
//     itself refuses to create this state (INTENT_CONFLICT), so intent.md is
//     written directly through the store before the round, simulating a
//     brief.md added after an earlier explicit intent was recorded - which the
//     CLI hints (`jig ticket`) point a user toward doing, and nothing ever
//     removes the stale intent.md that leaves behind;
//   - round 2 is clean and leaves round 1's files as they were.
func TestGateCleanRound2(t *testing.T) {
	t.Parallel()

	fx := generateBuilt(t, "rung-a")

	d := newDeps(t, fx)
	src := NewFakeGateSource(fx.ScenarioDir)
	if err := d.Store.WriteIntent(fx.Ticket, store.Intent{Source: IntentSourceExplicit, Text: "stale explicit intent"}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}

	var report1 GateReport
	step(t, "round 1 finds the fix slice and records it", func(t *testing.T) {
		var err error
		report1, err = Gate(d, src, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Gate: %v", err)
		}
		if report1.Round != 1 {
			t.Fatalf("Round = %d, want 1", report1.Round)
		}
		if report1.Verdict != "fix-slices" {
			t.Fatalf("Verdict = %q, want fix-slices", report1.Verdict)
		}
		// A ticket on its own default branch adopted nothing, so its report names no
		// branch: only an adopted ticket's does.
		if report1.Branch != "" {
			t.Fatalf("Branch = %q, want none for a ticket that adopted no branch", report1.Branch)
		}

		roundDir := filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-1")
		for _, name := range []string{"findings.md", "report.yaml", "diff-changelog.md"} {
			if _, err := os.Stat(filepath.Join(roundDir, name)); err != nil {
				t.Fatalf("missing %s: %v", name, err)
			}
		}
		if _, err := os.Stat(filepath.Join(d.Store.TicketDir(fx.Ticket), "evidence", "round-1", "oracle-output.txt")); err != nil {
			t.Fatalf("missing receipt: %v", err)
		}

		slices, err := d.Store.ReadSlices(fx.Ticket)
		if err != nil {
			t.Fatalf("ReadSlices: %v", err)
		}
		var fix1 *store.Slice
		for i := range slices {
			if slices[i].ID == "fix-1" {
				fix1 = &slices[i]
			}
		}
		if fix1 == nil {
			t.Fatal("fix-1 was not appended to slices.yaml")
		}
		if fix1.FromGate != 1 {
			t.Fatalf("fix-1.FromGate = %d, want 1", fix1.FromGate)
		}
		st, err := d.Store.ReadSliceState(fx.Ticket, "fix-1")
		if err != nil {
			t.Fatalf("ReadSliceState fix-1: %v", err)
		}
		if st.State != "queued" {
			t.Fatalf("fix-1 state = %q, want queued", st.State)
		}

		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		want := map[string]bool{"gate-open": false, "gate-round": false, "fix-slice": false}
		for _, l := range lines {
			if _, ok := want[l.Event]; ok {
				want[l.Event] = true
			}
		}
		for event, seen := range want {
			if !seen {
				t.Fatalf("journal missing event %q", event)
			}
		}
		// The round is the slice's from_gate; the line carries no attempt, so
		// the fix slice's first verification is attempt 1.
		for _, l := range lines {
			if l.Event == journal.EventFixSlice && l.Attempt != 0 {
				t.Errorf("fix-slice line for %s carries attempt %d, want none", l.Slice, l.Attempt)
			}
		}
		if got := journal.NextAttempt(lines, "fix-1"); got != 1 {
			t.Errorf("NextAttempt(fix-1) = %d, want 1", got)
		}
	})

	// The scripted source never writes a round's own findings.yaml, so this
	// checks what Gate itself puts in report.yaml for a later round's
	// UsedFixBudget to read.
	step(t, "the scripted round's fix slices count toward the fix budget", func(t *testing.T) {
		if len(report1.FixSlices) == 0 {
			t.Fatalf("report.FixSlices = %v, want at least the scenario's own round-1 fix slice", report1.FixSlices)
		}
		used, unreadable := UsedFixBudget(d.Store, fx.Ticket, 2)
		if used != 1 || len(unreadable) != 0 {
			t.Fatalf("UsedFixBudget = %d (unreadable %v), want 1 (the scripted round counts toward the budget)", used, unreadable)
		}
	})

	// The top of the precedence order: a ticket with a brief.md (fixture.Generate
	// always writes one) resolves to source "brief", pointing at that file, with a
	// non-empty report.yaml sha256.
	step(t, "the brief is the intent, ahead of an intent.md", func(t *testing.T) {
		if report1.Intent.Source != IntentSourceBrief {
			t.Fatalf("Intent.Source = %q, want %q (brief.md must win even with intent.md present)", report1.Intent.Source, IntentSourceBrief)
		}
		wantPath, err := filepath.Abs(filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md"))
		if err != nil {
			t.Fatal(err)
		}
		if report1.Intent.Path != wantPath {
			t.Fatalf("Intent.Path = %q, want %q", report1.Intent.Path, wantPath)
		}
		wantSHA256 := sha256HexFile(t, wantPath)
		if report1.IntentSHA256 != wantSHA256 {
			t.Fatalf("IntentSHA256 = %q, want %q (sha256 of brief.md's own bytes)", report1.IntentSHA256, wantSHA256)
		}

		rep := reportYAMLAt(t, d, fx.Ticket, report1.Round)
		if rep.Intent.Source != IntentSourceBrief || rep.Intent.SHA256 != wantSHA256 {
			t.Fatalf("report.yaml intent = %+v, want source %q with sha256 %q", rep.Intent, IntentSourceBrief, wantSHA256)
		}
	})

	step(t, "round 2 is clean and leaves round 1 as it was", func(t *testing.T) {
		round1Dir := filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-1")
		before := readDirBytes(t, round1Dir)

		driveFix1(t, fx, "rung-a")

		report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Gate round 2: %v", err)
		}
		if report.Round != 2 {
			t.Fatalf("Round = %d, want 2", report.Round)
		}
		if report.Verdict != "clean" {
			t.Fatalf("Verdict = %q, want clean", report.Verdict)
		}

		after := readDirBytes(t, round1Dir)
		if len(before) != len(after) {
			t.Fatalf("round-1 file count changed: %d -> %d", len(before), len(after))
		}
		for name, data := range before {
			if string(after[name]) != string(data) {
				t.Fatalf("round-1/%s bytes changed after round 2", name)
			}
		}

		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		sawClean := false
		for _, l := range lines {
			if l.Event == "gate-clean" && l.Attempt == 2 {
				sawClean = true
			}
		}
		if !sawClean {
			t.Fatal("journal missing gate-clean for round 2")
		}
	})
}

// readDirBytes reads every regular file directly under dir into a map
// keyed by base name, for byte-level before/after comparison.
func readDirBytes(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = data
	}
	return out
}

// TestFrontierRefusalNamesThePromptItIsGiven: a refusal for a slice that is not
// green says what to ask when the caller has a prompt for the ticket, and
// nothing more when it has none.
func TestFrontierRefusalNamesThePromptItIsGiven(t *testing.T) {
	t.Parallel()
	d := Deps{Store: &store.Store{Root: t.TempDir()}}
	slices := []store.Slice{{ID: "a"}}
	help := func() []string {
		t.Helper()
		var ae *axi.Error
		if err := checkFrontier(d, "JIG-1", slices, false); !errors.As(err, &ae) || ae.Code != "GATE_NOT_GREEN" {
			t.Fatalf("checkFrontier = %v, want GATE_NOT_GREEN", err)
		}
		return ae.Help
	}

	if got := help(); len(got) != 1 {
		t.Errorf("with no prompt the help is %q, want the one line", got)
	}
	d.Prompt = func(string) string { return "" }
	if got := help(); len(got) != 1 {
		t.Errorf("with an empty prompt the help is %q, want the one line", got)
	}
	d.Prompt = func(ticket string) string { return "Build " + ticket }
	if got := help(); len(got) != 2 || got[1] != "ask: Build JIG-1" {
		t.Errorf("with a prompt the help is %q, want it to end with the ask", got)
	}
}

// duplicateFixSliceSource is a GateSource stub whose round-1 fix slice
// reuses an id that already exists in the ticket's slices.yaml.
type duplicateFixSliceSource struct{}

func (duplicateFixSliceSource) Round(in RoundInput) (Round, bool, error) {
	if in.Round == 1 {
		return Round{
			FindingsMD: "duplicate id",
			FixSlices:  []store.Slice{{ID: "a", Workspace: "root", Goal: "dup", Oracle: "test"}},
		}, true, nil
	}
	return Round{}, false, nil
}

// TestGateSurfacesDuplicateFixSliceID checks that Gate does not swallow
// store.AppendSlices's duplicate-id refusal when a round's fix slice reuses
// an id already present in slices.yaml.
func TestGateSurfacesDuplicateFixSliceID(t *testing.T) {
	t.Parallel()

	fx := generateBuilt(t, "rung-a")

	d := newDeps(t, fx)
	_, err := Gate(d, duplicateFixSliceSource{}, GateOpts{Ticket: fx.Ticket})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "SLICE_ID_DUPLICATE" {
		t.Fatalf("err = %v, want *axi.Error SLICE_ID_DUPLICATE", err)
	}
}

// removeBrief deletes fx's ticket's brief.md, simulating a brief-less
// ticket: fixture.Generate always writes one, and intent precedence only
// falls through to intent.md/none once it is gone. The ticket keeps its
// branch (fixture.RemoveBrief).
func removeBrief(t *testing.T, d Deps, ticket string) {
	t.Helper()
	if err := fixture.RemoveBrief(d.Store.Root, ticket); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}
}

// gateRefused runs Gate and returns its error, requiring the call to leave
// the journal exactly as it found it: a refusal that is documented as
// leaving no trace (a bad intent flag, a bad intent.md, an unmet
// precondition) must fail the round before its gate-open journal line is
// appended, not after, or a plain rerun is no longer the whole recovery.
// Journal lines are compared, not the store's HEAD: a round's own deferred
// push and Sync's own "record uncommitted store state" commit both move
// HEAD legitimately.
func gateRefused(t *testing.T, d Deps, src GateSource, o GateOpts) error {
	t.Helper()
	count := func() int {
		t.Helper()
		lines, err := journal.Read(d.Store, o.Ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		return len(lines)
	}
	before := count()
	_, err := Gate(d, src, o)
	if after := count(); after != before {
		t.Errorf("journal has %d lines after the refused Gate (err = %v), want the %d it had before: a refusal must leave no journal line", after, err, before)
	}
	return err
}

// requireReportIntentWireShape checks the intent binding's wire shape in
// the report.yaml bytes a round wrote, decoding them into raw keys rather
// than into reportYAML: every other reader in this package decodes into
// that struct, so a renamed yaml tag round-trips through it unnoticed
// while the file itself, store schema (ADR 0003), changes shape for
// everything else that reads it. The shape is one top-level "intent"
// mapping with exactly the keys "sha256" and "source".
func requireReportIntentWireShape(t *testing.T, reportData []byte) {
	t.Helper()
	var raw map[string]any
	if err := yaml.Unmarshal(reportData, &raw); err != nil {
		t.Fatalf("parse report.yaml as raw keys: %v", err)
	}
	rawIntent, ok := raw["intent"]
	if !ok {
		t.Fatalf("report.yaml has no \"intent\" key:\n%s", reportData)
	}
	fields, ok := rawIntent.(map[string]any)
	if !ok {
		t.Fatalf("report.yaml's \"intent\" is not a mapping: %T\n%s", rawIntent, reportData)
	}
	if got, want := slices.Sorted(maps.Keys(fields)), []string{"sha256", "source"}; !slices.Equal(got, want) {
		t.Errorf("report.yaml's intent keys = %v, want %v", got, want)
	}
}

// reportYAMLAt reads and parses one round's report.yaml, after pinning the
// intent block's raw key names (requireReportIntentWireShape), so every
// test that reads a report back also checks the file's shape.
func reportYAMLAt(t *testing.T, d Deps, ticket string, round int) reportYAML {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, ticket, round), "report.yaml"))
	if err != nil {
		t.Fatalf("read report.yaml: %v", err)
	}
	requireReportIntentWireShape(t, data)
	var rep reportYAML
	if err := yaml.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse report.yaml: %v", err)
	}
	return rep
}

// TestGateRefusalsLeaveNoTrace is the chain of the refusals that are documented
// as leaving no trace: no journal line (gateRefused), no recorded intent.md, no
// adopted branch and no start sha, so a plain rerun is the whole recovery. They
// are decided before the round writes anything, so one ticket and one pushed
// branch serve them all, each step standing on a ticket the ones before it left
// as it was. The steps run with the ticket's brief, then (as an intent flag
// needs no brief to conflict with) without it:
//
//   - a branch missing on origin fails loudly with BRANCH_NOT_FOUND, instead of
//     pool.Acquire's own fallback (origin/<target> as the start point for a
//     brand-new local branch) silently validating a branch that was never
//     pushed;
//   - --intent and --doc are refused with INTENT_CONFLICT on a ticket that
//     already has a brief.md;
//   - a --branch gate refused with BRANCH_NOT_FOUND leaves the same no trace:
//     --intent must not write intent.md when the branch check refuses the round
//     after the frontier precondition already passed;
//   - a --doc path that cannot be read is refused with INTENT_DOC_MISSING
//     rather than silently ignored;
//   - writeExplicitIntent refuses Intent and IntentDoc both being set, not only
//     the CLI (cmd/jig/gate.go's own "not both" check): GateOpts is a library
//     entry point, so a caller that reaches Gate directly, bypassing the CLI,
//     must be refused the same way rather than silently letting the doc win;
//   - a --doc file that reads but holds no text a reviewer could judge a fix
//     against - truly empty, or whitespace-only - is refused with INTENT_EMPTY
//     rather than recording a binding explicit intent with nothing in it;
//   - an adoption is recorded after --intent/--doc has been written and the
//     intent resolved, so a refusal by either leaves no adopted branch behind.
//     Recorded first, the refused round would leave the ticket on the branch, and
//     a later --branch naming another one would be refused over an adoption that
//     never happened: the last step is a later round that adopts another branch.
func TestGateRefusalsLeaveNoTrace(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()}) // brief.md present by default
	d := newDeps(t, fx)
	branch := pushedBranch(t, fx)
	wantRefused := func(t *testing.T, o GateOpts, code string) {
		t.Helper()
		o.Ticket, o.Early = fx.Ticket, true
		var ae *axi.Error
		if err := gateRefused(t, d, alwaysCleanSource{}, o); !errors.As(err, &ae) || ae.Code != code {
			t.Fatalf("err = %v, want *axi.Error %s", err, code)
		}
		if _, ok, rerr := d.Store.ReadIntent(fx.Ticket); rerr != nil || ok {
			t.Fatalf("ReadIntent after %s: ok=%v err=%v, want ok=false (refused before writing intent.md)", code, ok, rerr)
		}
		wantNoAdoption(t, d, fx.Ticket)
	}
	writeDoc := func(t *testing.T, body []byte) string {
		t.Helper()
		doc := filepath.Join(t.TempDir(), "doc.md")
		if err := os.WriteFile(doc, body, 0o644); err != nil {
			t.Fatalf("write doc: %v", err)
		}
		return doc
	}

	step(t, "a branch missing on origin is refused BRANCH_NOT_FOUND", func(t *testing.T) {
		wantRefused(t, GateOpts{Branch: "no-such-branch"}, "BRANCH_NOT_FOUND")
	})
	step(t, "--intent over a brief is refused INTENT_CONFLICT", func(t *testing.T) {
		wantRefused(t, GateOpts{Branch: branch, Intent: "explicit text"}, "INTENT_CONFLICT")
	})
	step(t, "--doc over a brief is refused INTENT_CONFLICT", func(t *testing.T) {
		wantRefused(t, GateOpts{Branch: branch, IntentDoc: writeDoc(t, []byte("text"))}, "INTENT_CONFLICT")
	})

	removeBrief(t, d, fx.Ticket)

	step(t, "a branch missing on origin is refused before --intent is recorded", func(t *testing.T) {
		wantRefused(t, GateOpts{Branch: "no-such-branch", Intent: "should not be recorded"}, "BRANCH_NOT_FOUND")
	})
	step(t, "a --doc path that cannot be read is refused INTENT_DOC_MISSING", func(t *testing.T) {
		wantRefused(t, GateOpts{Branch: branch, IntentDoc: filepath.Join(t.TempDir(), "missing.md")}, "INTENT_DOC_MISSING")
	})
	step(t, "--intent and --doc together are refused VALIDATION_ERROR", func(t *testing.T) {
		wantRefused(t, GateOpts{Branch: branch, Intent: "explicit text", IntentDoc: writeDoc(t, []byte("doc text"))}, "VALIDATION_ERROR")
	})
	step(t, "a --doc file with no text is refused INTENT_EMPTY", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			body []byte
		}{
			{"empty", nil},
			{"whitespace-only", []byte("\n")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				wantRefused(t, GateOpts{Branch: branch, IntentDoc: writeDoc(t, tc.body)}, "INTENT_EMPTY")
			})
		}
	})
	step(t, "a later round may adopt any other branch", func(t *testing.T) {
		other := authorBranch(t, fx, "add-retry")
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: "add-retry", Early: true}); err != nil {
			t.Fatalf("Gate --branch add-retry after the refusals (tip %s): %v", other, err)
		}
	})
}

// TestGateRefusesAnIntentMDItCannotBind checks that resolveIntent refuses
// a ticket's intent.md when it binds nothing jig could stand behind, each
// with its own code and before the round's gate-open journal line, and that
// every refusal names the way out, which works: rerunning with --intent
// replaces the file. Only a hand edit
// (or a stale file from a jig that recorded something else) reaches any of
// these, since `jig gate --intent`/`--doc` never writes one:
//
//   - a recorded source that is not one jig itself ever writes there
//     (today, only "explicit") must not silently impersonate a provenance
//     the human never actually gave, brief included;
//   - a body with no text in it (whitespace only) is refused on read the
//     same way writeExplicitIntent refuses to record it;
//   - a front matter that does not parse, however it fails to, is a coded
//     refusal with help, never a bare error.
func TestGateRefusesAnIntentMDItCannotBind(t *testing.T) {
	t.Parallel()

	// One build serves the three files, each tried in turn: the way out of each
	// refusal leaves a good intent.md, which the next bad file replaces.
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	removeBrief(t, d, fx.Ticket)

	for _, tc := range []struct {
		name string
		file string
		code string
	}{
		{"unsupported source", "---\nsource: brief\n---\nimpersonating brief provenance\n", "INTENT_INVALID_SOURCE"},
		{"whitespace-only body", "---\nsource: explicit\n---\n   \n\n", "INTENT_EMPTY"},
		{"no front matter", "just some words\n", "INTENT_INVALID"},
	} {
		step(t, tc.name, func(t *testing.T) {
			if err := os.WriteFile(d.Store.IntentPath(fx.Ticket), []byte(tc.file), 0o644); err != nil {
				t.Fatalf("write intent.md: %v", err)
			}

			err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
			var ae *axi.Error
			if !errors.As(err, &ae) || ae.Code != tc.code {
				t.Fatalf("err = %v, want *axi.Error %s", err, tc.code)
			}
			if len(ae.Help) != 1 || !strings.Contains(ae.Help[0], "jig gate "+fx.Ticket) || !strings.Contains(ae.Help[0], "--intent") {
				t.Fatalf("help = %q, want one line naming `jig gate %s` with --intent", ae.Help, fx.Ticket)
			}

			// The way out the help names: a rerun with --intent replaces
			// the bad file, and the round resolves to it.
			report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Intent: "the replacement text"})
			if err != nil {
				t.Fatalf("Gate --intent over the bad intent.md: %v", err)
			}
			if report.Intent.Source != IntentSourceExplicit {
				t.Fatalf("Intent.Source = %q, want %q", report.Intent.Source, IntentSourceExplicit)
			}
		})
	}
}

// TestGateIntentNotRecordedWhenNotGreen checks that a gate refused with
// GATE_NOT_GREEN leaves no trace: --intent must not write (or push)
// intent.md, nor append a journal line, before the frontier precondition
// is checked, so a plain rerun after finishing the frontier is the whole
// recovery. The frontier is the slices' states and nothing else
// (TestFrontierRefusalNamesThePromptItIsGiven reads the refusal's help), so
// the states that refuse a round are steps of one ticket whose slices are
// written directly, with no build:
//
//   - a slice left queued, with --intent;
//   - a slice left "stalled" (not merely "queued") also blocks gate without
//     --early: any non-green state is an incomplete delivery;
//   - a queued fix slice (FromGate != 0, appended by an earlier round) also
//     blocks a later gate round: it used to be exempted from the frontier
//     check entirely.
//
// Gate over slices that are all queued, with --early, is the round of
// TestGateRefusalsLeaveNoTrace's last step and of every adoption test, which is
// what --early is for.
func TestGateIntentNotRecordedWhenNotGreen(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	removeBrief(t, d, fx.Ticket)
	mark := func(t *testing.T, state string, ids ...string) {
		t.Helper()
		for _, id := range ids {
			st := store.SliceState{State: state, Attempts: 1}
			if state == "stalled" {
				st = store.SliceState{State: state, Reason: "stall"}
			}
			if err := d.Store.WriteSliceState(fx.Ticket, id, st); err != nil {
				t.Fatalf("write slice state %s: %v", id, err)
			}
		}
		if err := d.Store.Push(fx.Ticket + ": slices " + state); err != nil {
			t.Fatalf("push the slice states: %v", err)
		}
	}
	wantNotGreen := func(t *testing.T, err error) {
		t.Helper()
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "GATE_NOT_GREEN" {
			t.Fatalf("err = %v, want *axi.Error GATE_NOT_GREEN", err)
		}
	}

	step(t, "a queued slice refuses the round and --intent is not recorded", func(t *testing.T) {
		mark(t, "green", "a") // b, c, d are left queued: the frontier is not empty
		err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Intent: "should not be recorded"})
		wantNotGreen(t, err)
		if _, ok, rerr := d.Store.ReadIntent(fx.Ticket); rerr != nil || ok {
			t.Fatalf("ReadIntent after a refused gate: ok=%v err=%v, want ok=false (intent.md must not be written before preconditions pass)", ok, rerr)
		}
	})
	step(t, "a stalled slice refuses the round", func(t *testing.T) {
		mark(t, "green", "b", "c")
		mark(t, "stalled", "d")
		wantNotGreen(t, gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}))
	})
	step(t, "a queued fix slice refuses the round", func(t *testing.T) {
		mark(t, "green", "d")
		if err := d.Store.AppendSlices(fx.Ticket, []store.Slice{{ID: "fix-x", Workspace: "root", Goal: "fix", Oracle: "test", FromGate: 1}}); err != nil {
			t.Fatalf("AppendSlices: %v", err)
		}
		// fix-x defaults to the zero-value "queued" state (no state file
		// written yet), same as a fix slice fresh off a gate round.
		if err := d.Store.Push(fx.Ticket + ": append fix-x"); err != nil {
			t.Fatalf("push after appending fix-x: %v", err)
		}
		wantNotGreen(t, gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}))
	})
}

// TestGateUsesRecordedBranch runs a gate round on a ticket whose ticket.yaml
// records a branch, with the build lease on that branch instead of the
// default name. Gate resolves the branch once, through
// Store.TicketBranch, and its gate lease and its fetch from the build lease
// both name the result: the round succeeds only if the gate lease ends up on
// the recorded branch, and no branch under the default name is ever made
// there. Every other Gate test in this file records no branch, so a Gate that
// hardcoded the default's spelling would still pass them. A recorded branch is on
// origin by definition, so the test puts it there, at the target's tip, before
// the build.
func TestGateUsesRecordedBranch(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	recordBranch(t, d.Store, fx.Ticket, "feature/custom")
	run(t, fx.RepoRemote, "branch", "feature/custom", "main")
	driveBuild(t, fx, "rung-a")

	report, err := Gate(d, NewFakeGateSource(fx.ScenarioDir), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("Verdict = %q, want fix-slices", report.Verdict)
	}

	if want := run(t, fx.RepoDir, "rev-parse", "refs/heads/feature/custom"); report.Round != 1 || run(t, fx.RepoDir, "rev-parse", "HEAD") != want {
		t.Fatalf("the round reviewed another branch than the recorded one: the clone is at %s, feature/custom at %s", run(t, fx.RepoDir, "rev-parse", "HEAD"), want)
	}
	if _, err := gitx.Run(fx.RepoDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+ticketBranch(fx.Ticket)); err == nil {
		t.Fatalf("the clone has a %s branch, want only the recorded one", ticketBranch(fx.Ticket))
	}
}

// TestGateFailsWithTheRefusalOfTicketBranch is the other side of
// TestGateUsesRecordedBranch: whatever Store.TicketBranch refuses fails the
// gate with the refusal's code, and no gate lease is created for it. The two
// refusals a ticket.yaml can earn are a recorded branch equal to the target
// and a record jig cannot read (here one a newer jig wrote, which may name a
// branch this jig does not understand). A Gate that fell back to
// "jig/<ticket>" on either would review that branch instead, which is the
// silent fallback the refusals exist to prevent; a fallback that tells the two
// apart and keeps only one refusal is still one. The build runs before the
// record is written: the test's own build-lease helper resolves the branch
// too, and would refuse it. The refusals are steps of that one build, the
// record put back between them.
func TestGateFailsWithTheRefusalOfTicketBranch(t *testing.T) {
	t.Parallel()

	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)

	for _, tc := range []struct {
		name     string
		record   func(t *testing.T, st *store.Store, ticket string)
		wantCode string
	}{
		{
			name: "recorded branch equal to the target",
			record: func(t *testing.T, st *store.Store, ticket string) {
				recordBranch(t, st, ticket, "main")
			},
			wantCode: "TICKET_BRANCH_INVALID",
		},
		{
			// Written with os.WriteFile: WriteTicketBranch refuses a
			// record of a schema this jig cannot read.
			name: "record of a newer schema",
			record: func(t *testing.T, st *store.Store, ticket string) {
				if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "ticket.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
					t.Fatalf("write the newer-schema record: %v", err)
				}
				if err := st.Push(ticket + ": a newer jig's record"); err != nil {
					t.Fatalf("push the newer-schema record: %v", err)
				}
			},
			wantCode: "TICKET_SCHEMA_UNSUPPORTED",
		},
	} {
		step(t, tc.name, func(t *testing.T) {
			restore := saveTicketRecord(t, d, fx.Ticket)
			tc.record(t, d.Store, fx.Ticket)
			// Test setup: the record must earn this refusal from
			// TicketBranch itself, or the Gate below proves nothing.
			_, err := d.Store.TicketBranch(fx.Ticket, "main")
			wantAxiCode(t, err, tc.wantCode)

			_, err = Gate(d, NewFakeGateSource(fx.ScenarioDir), GateOpts{Ticket: fx.Ticket})
			wantAxiCode(t, err, tc.wantCode)

			if _, err := os.Stat(filepath.Join(fx.Home, "pool")); !os.IsNotExist(err) {
				t.Fatalf("the refused Gate left a pool directory (stat err %v), want none", err)
			}
			restore(t)
		})
	}
}

// --- the fix budget ----------------------------------------------------

// containsString reports whether s contains v.
func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// addSecondOracle adds a "vet" oracle to buildDir's checked-out manifest
// (a second command over the same go binary the fixture's own "test"
// oracle already resolved to), commits it on branch, and pushes it to
// origin, so a Gate round resolving the manifest from that branch sees two
// oracles instead of one.
func addSecondOracle(t *testing.T, buildDir, branch string) {
	t.Helper()
	yamlPath := filepath.Join(buildDir, ".claude", "jig.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read jig.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse jig.yaml: %v", err)
	}
	oracles, ok := doc["oracles"].(map[string]any)
	if !ok {
		t.Fatalf("jig.yaml oracles = %+v, want a map", doc["oracles"])
	}
	testCmd, ok := oracles["test"].(string)
	if !ok {
		t.Fatalf("jig.yaml oracles.test = %+v, want a string", oracles["test"])
	}
	oracles["vet"] = strings.Replace(testCmd, " test ", " vet ", 1)
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jig.yaml: %v", err)
	}
	if err := os.WriteFile(yamlPath, out, 0o644); err != nil {
		t.Fatalf("write jig.yaml: %v", err)
	}
	builder(t).Commit(buildDir, "add a second manifest oracle")
	builder(t).Push(buildDir, "origin", branch)
}

// renameOracle renames a manifest oracle (its declared name, not its
// command) on buildDir's checked-out branch and pushes it to origin,
// reproducing a manifest change between gate rounds: a finding whose
// recorded oracle was from before this becomes stale.
func renameOracle(t *testing.T, buildDir, branch, from, to string) {
	t.Helper()
	yamlPath := filepath.Join(buildDir, ".claude", "jig.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read jig.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse jig.yaml: %v", err)
	}
	oracles, ok := doc["oracles"].(map[string]any)
	if !ok {
		t.Fatalf("jig.yaml oracles = %+v, want a map", doc["oracles"])
	}
	cmd, ok := oracles[from].(string)
	if !ok {
		t.Fatalf("jig.yaml oracles.%s = %+v, want a string", from, oracles[from])
	}
	delete(oracles, from)
	oracles[to] = cmd
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jig.yaml: %v", err)
	}
	if err := os.WriteFile(yamlPath, out, 0o644); err != nil {
		t.Fatalf("write jig.yaml: %v", err)
	}
	builder(t).Commit(buildDir, "rename a manifest oracle")
	builder(t).Push(buildDir, "origin", branch)
}

// TestGateNoRoundKeepsAnOutstandingAskFromGoingClean pins the one path no
// test covered: a source with nothing to review for this round, while the
// cumulative fold still holds an undecided ask.
//
// The round used to be declared clean without consulting the fold at all.
// `jig publish` gates on that verdict, so the ticket squashed, pushed and
// opened its PR with the question never answered - while `jig status`,
// which reads the fold, listed it as outstanding the whole time. Whether
// a source happened to script a round says nothing about what earlier
// rounds left behind.
func TestGateNoRoundKeepsAnOutstandingAskFromGoingClean(t *testing.T) {
	t.Parallel()

	fx := generateBuilt(t, "rung-a")

	d := newDeps(t, fx)
	// Round 1 is recorded directly: an ask nobody decided, exactly the
	// state a real round 1 leaves when no human is at the terminal.
	roundDir := filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatalf("mkdir round 1: %v", err)
	}
	body, err := marshalFindingsYAML("full", []string{"alpha/alpha.go"}, []Finding{{
		ID: "r1-f1", File: "alpha/alpha.go", Line: 4, Title: "is this rename intended",
		Detail: "d", Action: ActionAsk, Risk: RiskHigh, RiskRationale: "callers depend on it",
		Status: StatusAsked,
	}}, nil, nil, "round 1", nil, nil)
	if err != nil {
		t.Fatalf("marshal findings.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roundDir, "findings.yaml"), body, 0o644); err != nil {
		t.Fatalf("write findings.yaml: %v", err)
	}

	// alwaysCleanSource stands in for any source with nothing to review
	// for round 2: the scripted source with no round-2 directory behaves
	// identically, which is the documented --scenario compatibility path.
	report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict == "clean" {
		t.Fatalf("Verdict = clean with r1-f1 still asked; publish gates on this verdict and would ship the ticket")
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("Verdict = %q, want fix-slices", report.Verdict)
	}
	if len(report.NeedsHuman) != 1 || report.NeedsHuman[0].ID != "r1-f1" {
		t.Fatalf("NeedsHuman = %+v, want the outstanding ask r1-f1 (the exit-2 signal)", report.NeedsHuman)
	}

	// The stored round must say the same thing the report does.
	data, err := os.ReadFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-2", "report.yaml"))
	if err != nil {
		t.Fatalf("read round 2 report.yaml: %v", err)
	}
	if strings.Contains(string(data), "verdict: clean") {
		t.Fatalf("round 2 report.yaml records a clean verdict:\n%s", data)
	}
}
