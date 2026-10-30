package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/session"
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

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	d.Oracle = nil // the real oracle run is what this test is about
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

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")

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

// TestGateIntentFlagReplacesExistingIntentMD is the one chain of a brief-less
// ticket's intent, on a ticket built once with its brief removed
// (fixture.Generate always writes one, and intent precedence only falls through
// to intent.md and then none once it is gone). The steps run in order, each on
// what the one before wrote:
//
//   - the reviewer is handed source "none" and no path, and the round resolves to
//     none with no path and no sha256, in the report and in report.yaml: the
//     bottom of the precedence order, a ticket with neither a brief.md nor an
//     intent.md (this runs before any intent.md is written);
//   - --intent works in normal (non-branch) mode, not only --branch: it writes
//     intent.md and the round resolves to source "explicit". The text is
//     recorded byte for byte (surrounding spaces, CRLF and an inner blank line
//     kept, one newline appended to end the file), which ReadIntent's own
//     normalization cannot show, so the file is compared directly;
//   - a second --intent replaces intent.md's text rather than appending to it;
//   - --doc: its file's content, not its path, becomes intent.md's text, byte
//     for byte. The doc is shaped so that trimming it, normalizing its CRLF line
//     endings, or collapsing its trailing blank lines (CRLF ones, then bare LF
//     ones, so a trim of LF alone and a trim of CR and LF each leave a different
//     file) each change the file: ReadIntent and ParseIntent normalize CRLF and
//     trim newlines themselves, so only the file's own bytes can tell a recorded
//     text from a rewritten one;
//   - the intent Gate resolves and hands RoundInput for a binding explicit
//     intent is the same one a real reviewer source writes into review.json.
//
// alwaysCleanSource never appends a fix slice, so the frontier stays green
// across every round; none needs --early.
func TestGateIntentFlagReplacesExistingIntentMD(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)
	removeBrief(t, d, fx.Ticket)
	wantExplicit := func(t *testing.T, report GateReport) string {
		t.Helper()
		if report.Intent.Source != IntentSourceExplicit {
			t.Fatalf("Intent.Source = %q, want %q", report.Intent.Source, IntentSourceExplicit)
		}
		wantSHA256 := sha256HexFile(t, d.Store.IntentPath(fx.Ticket))
		rep := reportYAMLAt(t, d, fx.Ticket, report.Round)
		if rep.Intent.Source != IntentSourceExplicit || rep.Intent.SHA256 != wantSHA256 {
			t.Fatalf("report.yaml intent = %+v, want source %q with sha256 %q (intent.md's own bytes)", rep.Intent, IntentSourceExplicit, wantSHA256)
		}
		raw, err := os.ReadFile(d.Store.IntentPath(fx.Ticket))
		if err != nil {
			t.Fatalf("read intent.md: %v", err)
		}
		return string(raw)
	}

	step(t, "the reviewer is handed source none and no path", func(t *testing.T) {
		got := reviewJSONIntent(t, d, fx.Ticket, GateOpts{})
		if got.Source != IntentSourceNone || got.Path != "" {
			t.Fatalf("review.json intent = %+v, want {%q \"\"}", got, IntentSourceNone)
		}
	})

	step(t, "no brief and no intent.md resolve to none", func(t *testing.T) {
		report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Gate: %v", err)
		}
		if report.Intent.Source != IntentSourceNone {
			t.Fatalf("Intent.Source = %q, want %q", report.Intent.Source, IntentSourceNone)
		}
		if report.Intent.Path != "" {
			t.Fatalf("Intent.Path = %q, want empty", report.Intent.Path)
		}
		if report.IntentSHA256 != "" {
			t.Fatalf("IntentSHA256 = %q, want empty", report.IntentSHA256)
		}

		rep := reportYAMLAt(t, d, fx.Ticket, report.Round)
		if rep.Intent.Source != IntentSourceNone || rep.Intent.SHA256 != "" {
			t.Fatalf("report.yaml intent = %+v, want source %q with an empty sha256", rep.Intent, IntentSourceNone)
		}
	})

	step(t, "--intent outside a branch records its text byte for byte", func(t *testing.T) {
		const intentText = "  fix the thing that broke,\r\n\r\nnot the one next to it  "
		report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Intent: intentText})
		if err != nil {
			t.Fatalf("Gate --intent: %v", err)
		}
		in, ok, err := d.Store.ReadIntent(fx.Ticket)
		if err != nil || !ok {
			t.Fatalf("ReadIntent: ok=%v err=%v", ok, err)
		}
		if in.Source != IntentSourceExplicit {
			t.Fatalf("intent.md source = %q, want %q", in.Source, IntentSourceExplicit)
		}
		if raw, want := wantExplicit(t, report), "---\nsource: explicit\n---\n"+intentText+"\n"; raw != want {
			t.Fatalf("intent.md = %q, want the given text's own bytes after the front matter: %q", raw, want)
		}
	})

	step(t, "a second --intent replaces intent.md's text", func(t *testing.T) {
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Intent: "second take"}); err != nil {
			t.Fatalf("Gate --intent (2nd): %v", err)
		}
		in, ok, err := d.Store.ReadIntent(fx.Ticket)
		if err != nil || !ok {
			t.Fatalf("ReadIntent: ok=%v err=%v", ok, err)
		}
		if in.Text != "second take" {
			t.Fatalf("intent.md text = %q, want it replaced with %q, not appended", in.Text, "second take")
		}
	})

	step(t, "--doc records its file's content, not its path, byte for byte", func(t *testing.T) {
		const docText = "\r\n  line one\r\n\r\nline two  \r\n\r\n\n\n"
		doc := filepath.Join(t.TempDir(), "doc.md")
		if err := os.WriteFile(doc, []byte(docText), 0o644); err != nil {
			t.Fatalf("write doc: %v", err)
		}

		report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, IntentDoc: doc})
		if err != nil {
			t.Fatalf("Gate --doc: %v", err)
		}
		if raw, want := wantExplicit(t, report), "---\nsource: explicit\n---\n"+docText; raw != want {
			t.Fatalf("intent.md = %q, want the doc's own bytes after the front matter: %q", raw, want)
		}
	})

	step(t, "the reviewer is handed the explicit intent and the path of its intent.md", func(t *testing.T) {
		// The reviewer is dispatched only when the round changes something to
		// review.
		commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "a change for the reviewer\n"})
		got := reviewJSONIntent(t, d, fx.Ticket, GateOpts{Intent: "make the greeting warmer"})
		wantPath, err := filepath.Abs(d.Store.IntentPath(fx.Ticket))
		if err != nil {
			t.Fatal(err)
		}
		if got.Source != IntentSourceExplicit || got.Path != wantPath {
			t.Fatalf("review.json intent = %+v, want {%q %q}", got, IntentSourceExplicit, wantPath)
		}
	})
}

// gateReviewRequest dispatches a minimal clean round through a real
// reviewerGateSource and returns the review.json that round's reviewer was
// actually handed, rather than anything Gate reports, which cannot catch
// RoundInput losing a field on the way to the reviewer (for example a
// RoundInput literal built with a zero Intent). It also pins the intent
// field's raw key names (requireIntentWireShape), which decoding into
// ReviewRequest cannot see.
func gateReviewRequest(t *testing.T, d Deps, ticket string, opts GateOpts) ReviewRequest {
	t.Helper()
	var got ReviewRequest
	backend := stubBackend{run: func(sd session.Dispatch) error {
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		requireIntentWireShape(t, reviewData)
		if err := json.Unmarshal(reviewData, &got); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}
		result := ReviewResult{ReviewedPaths: got.MustReview, Summary: "clean"}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	opts.Ticket = ticket
	if _, err := Gate(d, newReviewerGateSource(backend), opts); err != nil {
		t.Fatalf("Gate: %v", err)
	}
	return got
}

// reviewJSONIntent is the "intent" field of gateReviewRequest's review.json:
// what the reviewer itself was pointed at.
func reviewJSONIntent(t *testing.T, d Deps, ticket string, opts GateOpts) Intent {
	t.Helper()
	return gateReviewRequest(t, d, ticket, opts).Intent
}

// TestGateHandsTheReviewerTheOraclesItRan: review.json's oracles_passed
// lists every oracle run the gate made before dispatching the reviewer -
// one per oracle per workspace, workspaces in manifest order and oracles by
// name - so the reviewer has the oracle's result and reads instead of
// retesting (ADR 0017). Two oracles make the order observable: map order
// would put vet before test about half the time.
func TestGateHandsTheReviewerTheOraclesItRan(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	addSecondOracle(t, buildLeaseDir(t, fx), ticketBranch(fx.Ticket))
	d := newDeps(t, fx)
	rec := &oracleRecorder{}
	d.Oracle = rec.run

	got := gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed
	// The fixture repo's jig.yaml declares test as "@GO test ./{path}/..."
	// over workspaces alpha and beta; addSecondOracle adds vet beside it.
	want := []struct{ oracle, workspace, suffix string }{
		{"test", "alpha", " test ./alpha/..."},
		{"vet", "alpha", " vet ./alpha/..."},
		{"test", "beta", " test ./beta/..."},
		{"vet", "beta", " vet ./beta/..."},
	}
	if len(got) != len(want) {
		t.Fatalf("review.json oracles_passed = %+v, want %d runs", got, len(want))
	}
	for i, w := range want {
		if got[i].Oracle != w.oracle || got[i].Workspace != w.workspace || !strings.HasSuffix(got[i].Command, w.suffix) {
			t.Errorf("oracles_passed[%d] = %+v, want oracle %s, workspace %s, a command ending %q", i, got[i], w.oracle, w.workspace, w.suffix)
		}
	}
	// Every entry is a run the gate made, in the order it made them.
	ran := rec.ran()
	if len(ran) != len(got) {
		t.Fatalf("the gate ran %q, want the %d runs oracles_passed lists", ran, len(got))
	}
	for i := range got {
		if ran[i] != got[i].Command {
			t.Errorf("run %d = %q, want %q, oracles_passed's entry %d", i, ran[i], got[i].Command, i)
		}
	}
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
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
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

// TestGateRecoversLeftoverTrackedDirtOnceBranchAdvances is the chain of what a
// killed reviewer or oracle leaves in the gate lease, on one build whose gate
// rounds run the real oracles: what the second round must not trip over.
//
// NM2 scenario A: a reviewer that outlived a killed jig (no signal handler
// reaches a future reviewer source's own defer restore) can leave an untracked
// failing test file and a tracked edit sitting in the gate lease. The next `jig
// gate` must not have its oracles fail or get skewed by that leftover dirt -
// Gate itself restores the lease to a pristine head before any oracle runs. The
// untracked file here is a failing test of alpha, which the fixture's "test"
// oracle (`go test ./alpha/...`) runs for real: it is only absent from the
// oracle's run if the restore came first.
//
// NM5, a residual of NM2: leftover tracked dirt (a reviewer that outlived a
// killed jig, or an oracle rewrite from an earlier attempt) does not get in the
// way of an oracle run by itself, but once the ticket branch later advances past
// the same file, the checkout that runs before Gate's post-acquire restore
// (fetchTicketBranchFromBuildLease's own final `git checkout <branch>`) refuses
// with "local changes ... would be overwritten", and Gate would return before
// that restore ever runs - leaving the lease detached and still dirty, so
// pool.Acquire's own checkout would fail the exact same way on every later
// attempt. Gate restores an existing lease pristine before Acquire ever touches
// it, so the very next gate succeeds (the second round here, whose lease holds
// the dirt of both scenarios and whose branch advanced), and a lease already
// left wedged by an earlier failed attempt (detached, still dirty, its local
// branch ref already fast-forwarded past the dirty file) recovers too (the
// third).
func TestGateRecoversLeftoverTrackedDirtOnceBranchAdvances(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	d.Oracle = nil // the real oracle run is what this test is about
	var gateDir string
	trackedPath := func() string { return filepath.Join(gateDir, "alpha", "alpha.go") }
	var orig []byte
	dirty := func(t *testing.T, note string) {
		t.Helper()
		edited := append(append([]byte{}, orig...), []byte("\n// "+note+"\n")...)
		if err := os.WriteFile(trackedPath(), edited, 0o644); err != nil {
			t.Fatalf("dirty tracked file: %v", err)
		}
	}
	advance := func(t *testing.T, note string) {
		t.Helper()
		buildDir := buildLeaseDir(t, fx)
		bp := filepath.Join(buildDir, "alpha", "alpha.go")
		bdata, err := os.ReadFile(bp)
		if err != nil {
			t.Fatalf("read build alpha.go: %v", err)
		}
		if err := os.WriteFile(bp, append(bdata, []byte("\n// "+note+"\n")...), 0o644); err != nil {
			t.Fatalf("write build alpha.go: %v", err)
		}
		if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
			t.Fatalf("git add: %v", err)
		}
		if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", note); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	wantPristine := func(t *testing.T, what string) {
		t.Helper()
		status, err := gitx.Run(gateDir, "status", "--porcelain")
		if err != nil {
			t.Fatalf("git status: %v", err)
		}
		if status != "" {
			t.Fatalf("gate lease left dirty after %s: %q", what, status)
		}
	}

	step(t, "round 1 leaves the gate lease its own", func(t *testing.T) {
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Gate round 1: %v", err)
		}
		gateLease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket, pool.Gate)
		if err != nil {
			t.Fatalf("reacquire gate lease: %v", err)
		}
		gateDir = gateLease.Dir
		if orig, err = os.ReadFile(trackedPath()); err != nil {
			t.Fatalf("read tracked file: %v", err)
		}
	})

	step(t, "round 2 wipes a failing repro and a tracked edit before its oracles, though the branch advanced", func(t *testing.T) {
		// An untracked failing test file, as a reviewer's own repro would leave
		// behind: the fixture's "test" oracle runs `go test ./alpha/...`.
		reproPath := filepath.Join(gateDir, "alpha", "zz_repro_test.go")
		repro := "package alpha\n\nimport \"testing\"\n\nfunc TestZZRepro(t *testing.T) { t.Fatal(\"boom\") }\n"
		if err := os.WriteFile(reproPath, []byte(repro), 0o644); err != nil {
			t.Fatalf("write leftover repro test: %v", err)
		}
		// A tracked edit left uncommitted, as an oracle rewrite or a killed
		// reviewer's own edit would leave behind.
		dirty(t, "leftover reviewer edit")
		// Advance the ticket's branch in the build lease with a commit touching the
		// same file, as a later slice build (e.g. after an --early gate) would.
		advance(t, "next slice")

		report2, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Gate round 2 should succeed on the first attempt despite the leftover dirt and the advanced branch: %v", err)
		}
		if report2.Round != 2 || report2.Verdict != "clean" {
			t.Fatalf("report2 = %+v, want round 2 clean", report2)
		}
		if _, err := os.Stat(reproPath); !os.IsNotExist(err) {
			t.Fatalf("leftover untracked repro file still present after Gate: err = %v", err)
		}
		wantPristine(t, "round 2")
	})

	step(t, "round 3 recovers a lease an earlier failed attempt left detached and dirty", func(t *testing.T) {
		// Forge exactly the state a pre-fix failed attempt would have left
		// behind: advance the branch again, dirty the lease again, then call
		// the real fetchTicketBranchFromBuildLease directly - its fetch force-
		// updates the local branch ref to the new tip and succeeds, but its own
		// final checkout back onto that branch fails on the dirty file, so it
		// returns with the lease detached at the old commit and still dirty.
		advance(t, "third slice")
		dirty(t, "leftover from a killed attempt")
		if err := fetchTicketBranchFromBuildLease(fx.Home, gateDir, "fixture-repo", fx.Ticket, ticketBranch(fx.Ticket)); err == nil {
			t.Fatal("test setup: fetchTicketBranchFromBuildLease should still fail on the dirty file before the pre-Acquire restore runs")
		}

		report3, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Gate round 3 should recover a lease left detached and dirty by an earlier failed attempt: %v", err)
		}
		if report3.Round != 3 || report3.Verdict != "clean" {
			t.Fatalf("report3 = %+v, want round 3 clean", report3)
		}
		head, err := gitx.RevParse(gateDir, "HEAD")
		if err != nil {
			t.Fatalf("resolve HEAD: %v", err)
		}
		branchHead, err := gitx.RevParse(gateDir, ticketBranch(fx.Ticket))
		if err != nil {
			t.Fatalf("resolve branch HEAD: %v", err)
		}
		if head != branchHead {
			t.Fatalf("gate lease HEAD %s is not the ticket branch's HEAD %s: still detached", head, branchHead)
		}
		wantPristine(t, "round 3")
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

	gateLease, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("resolve gate lease: %v", err)
	}
	if head := run(t, gateLease, "symbolic-ref", "--short", "HEAD"); head != "feature/custom" {
		t.Fatalf("gate lease HEAD branch = %q, want the recorded %q", head, "feature/custom")
	}
	if _, err := gitx.Run(gateLease, "rev-parse", "--verify", "--quiet", "refs/heads/"+ticketBranch(fx.Ticket)); err == nil {
		t.Fatalf("the gate lease has a %s branch, want only the recorded one", ticketBranch(fx.Ticket))
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

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	driveBuild(t, fx, "rung-a")

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

			gateLease, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Gate)
			if err != nil {
				t.Fatalf("resolve gate lease: %v", err)
			}
			if _, err := os.Stat(gateLease); !os.IsNotExist(err) {
				t.Fatalf("the refused Gate left a gate lease at %s (stat err %v), want none", gateLease, err)
			}
			restore(t)
		})
	}
}

// TestGateReviewerFindingsBookkeepingAcrossRounds is findings bookkeeping's
// own Gate() integration test: it drives two real reviewer rounds through
// Gate itself (not reviewerGateSource.Round directly) and checks that the
// wiring findings.go adds - review.json's open list built from the fold,
// the verdict derived from applying the round, findings.yaml/md written to
// disk, and reviewed_sha recorded - all agree with each other.
//
// Round 2 is answered the way a headless backend's file-read tool does, with
// every reviewed path in its lease-absolute form: the finding must still clear
// once its file is covered, and findings.yaml must record the coverage as
// repo-relative paths only, never a host path. (A reviewer that echoes the
// relative must_review list clears it the same way, in every other reviewer
// round of this package.)
func TestGateReviewerFindingsBookkeepingAcrossRounds(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			if len(req.Open) != 0 {
				t.Fatalf("round 1 review.json Open = %+v, want none", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "leaks a tenant id", Action: ActionFix,
					Risk: RiskHigh, RiskRationale: "customer data", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1 summary",
			}
		case 2:
			if len(req.Open) != 1 || req.Open[0].Title != "needs a fix" || req.Open[0].File != "alpha/alpha.go" {
				t.Fatalf("round 2 review.json Open = %+v, want the round 1 finding fed back", req.Open)
			}
			// Nothing new reported: the round 1 finding clears (rule 3), its
			// file covered by every entry in the lease-absolute form.
			abs := make([]string, len(req.MustReview))
			for i, p := range req.MustReview {
				abs[i] = filepath.Join(sd.Worktree, filepath.FromSlash(p))
			}
			result = ReviewResult{ReviewedPaths: abs, Summary: "round 2 summary"}
		default:
			t.Fatalf("unexpected round %d", round)
		}

		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}

	src := newReviewerGateSource(backend)
	var f1 Finding

	step(t, "round 1 records the finding, routes its fix and the head it reviewed", func(t *testing.T) {
		report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Gate round 1: %v", err)
		}
		if report1.Round != 1 {
			t.Fatalf("round = %d, want 1", report1.Round)
		}
		if report1.Verdict != "fix-slices" {
			t.Fatalf("round 1 Verdict = %q, want fix-slices", report1.Verdict)
		}
		if report1.ReviewedSHA == nil || report1.ReviewedSHA["fixture-repo"] == "" {
			t.Fatalf("round 1 ReviewedSHA = %+v, want a non-empty fixture-repo entry", report1.ReviewedSHA)
		}

		ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
		if err != nil {
			t.Fatalf("read round 1 findings.yaml: %v", err)
		}
		if !ok {
			t.Fatal("round 1 findings.yaml missing")
		}
		if len(ff1.Findings) != 1 {
			t.Fatalf("round 1 findings = %+v, want 1", ff1.Findings)
		}
		f1 = ff1.Findings[0]
		if f1.Status != StatusOpen {
			t.Errorf("round 1 finding status = %q, want open", f1.Status)
		}
		if f1.Workspace != "alpha" {
			t.Errorf("round 1 finding workspace = %q, want alpha", f1.Workspace)
		}
		if f1.ID == "" {
			t.Error("round 1 finding has no id")
		}
		if len(report1.FixSlices) != 1 {
			t.Fatalf("round 1 FixSlices = %v, want 1 (routing kept the fix)", report1.FixSlices)
		}
		sliceID := report1.FixSlices[0]
		fixSlices, err := d.Store.ReadSlices(fx.Ticket)
		if err != nil {
			t.Fatalf("read slices: %v", err)
		}
		var fs *store.Slice
		for i := range fixSlices {
			if fixSlices[i].ID == sliceID {
				fs = &fixSlices[i]
			}
		}
		if fs == nil {
			t.Fatalf("appended fix slice %q not found in slices.yaml", sliceID)
		}
		if !equalStrings(fs.Findings, []string{f1.ID}) {
			t.Errorf("fix slice %s Findings = %v, want [%s]", sliceID, fs.Findings, f1.ID)
		}
		if fs.Workspace != "alpha" || fs.Oracle != "test" {
			t.Errorf("fix slice %s = %+v, want workspace alpha, oracle test", sliceID, fs)
		}

		mdPath := filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "findings.md")
		if md, err := os.ReadFile(mdPath); err != nil || len(md) == 0 {
			t.Fatalf("round 1 findings.md: data=%q err=%v", md, err)
		}
	})

	step(t, "round 2 clears the finding from lease-absolute reviewed paths", func(t *testing.T) {
		// Round 2 runs --early: the fix slice routing just appended is still
		// queued, and driving it green belongs to frontier, not this test
		// (which only exercises Gate's own findings bookkeeping/routing).
		report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
		if err != nil {
			t.Fatalf("Gate round 2: %v", err)
		}
		if report2.Verdict != "clean" {
			t.Fatalf("round 2 Verdict = %q, want clean (the absolute-path reviewed_paths should clear %s)", report2.Verdict, f1.ID)
		}
		if report2.ReviewedSHA == nil || report2.ReviewedSHA["fixture-repo"] == "" {
			t.Fatalf("round 2 ReviewedSHA = %+v, want a non-empty fixture-repo entry", report2.ReviewedSHA)
		}

		ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
		if err != nil {
			t.Fatalf("read round 2 findings.yaml: %v", err)
		}
		if !ok {
			t.Fatal("round 2 findings.yaml missing")
		}
		if !equalStrings(ff2.Cleared, []string{f1.ID}) {
			t.Fatalf("round 2 cleared = %v, want [%s]", ff2.Cleared, f1.ID)
		}
		for _, p := range ff2.ReviewedPaths {
			if filepath.IsAbs(p) || strings.Contains(p, "\\") {
				t.Errorf("round 2 findings.yaml reviewed_paths entry %q is not repo-relative", p)
			}
		}

		cum, err := cumulativeFindings(d.Store, fx.Ticket, 3)
		if err != nil {
			t.Fatalf("cumulativeFindings: %v", err)
		}
		if !isClean(cum) {
			t.Fatalf("cumulative state after round 2 = %+v, want clean", cum)
		}
	})
}

// --- the fix budget ----------------------------------------------------

// TestGateFixBudgetParksAfterRoundsUsed is the budget's core integration
// seam: round 1, with a fix_rounds:1 budget and nothing used yet, queues
// its fix slice normally; round 2's own would-be open fix, with the
// budget now reached, parks instead - unattended (nil Triage, i.e.
// DefaultTriage), the way --yes and `jig solve` would leave it too.
func TestGateFixBudgetParksAfterRoundsUsed(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)
	fr := 1
	d.Cfg.Gate = &project.GateConfig{FixRounds: &fr}

	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		req := readReviewRequest(t, sd.SliceJSON)
		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "round 1 finding",
					Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1",
			}
		case 2:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "round 2 finding",
					Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 2",
			}
		default:
			t.Fatalf("unexpected round %d", round)
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Verdict != "fix-slices" || len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 = %+v, want fix-slices with one fix slice (the budget is not used yet)", report1)
	}
	if report1.BudgetParked != 0 {
		t.Fatalf("round 1 BudgetParked = %d, want 0", report1.BudgetParked)
	}

	// Round 2 runs --early: round 1's own fix slice is still queued, and
	// driving it green belongs to frontier, not this test.
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(report2.FixSlices) != 0 {
		t.Fatalf("round 2 FixSlices = %v, want none (the budget is reached)", report2.FixSlices)
	}
	if report2.BudgetParked != 1 || report2.BudgetUsed != 1 || report2.BudgetLimit != 1 {
		t.Fatalf("round 2 budget = used %d limit %d parked %d, want 1/1/1", report2.BudgetUsed, report2.BudgetLimit, report2.BudgetParked)
	}
	if len(report2.NeedsHuman) != 1 {
		t.Fatalf("round 2 NeedsHuman = %+v, want the parked finding", report2.NeedsHuman)
	}
	parked := report2.NeedsHuman[0]
	if parked.Status != StatusAsked || parked.RoutedAs != ActionAsk || parked.RoutedWhy != RoutedWhyBudget {
		t.Fatalf("parked finding = %+v, want asked/routed_as ask/routed_why budget", parked)
	}

	used, unreadable := UsedFixBudget(d.Store, fx.Ticket, 3)
	if used != 1 || len(unreadable) != 0 {
		t.Fatalf("UsedFixBudget before round 3 = %d (unreadable %v), want 1 and none unreadable", used, unreadable)
	}
}

// TestGateFixBudgetTerminalKeepStillQueuesAFixAndBudgetStaysReached pins
// the two halves of "who can keep a parked finding": a human at a
// terminal keeping one queues its fix slice anyway, and the budget stays
// reached afterward, so the next round parks again. The budget here is
// fix_rounds: 0 - no round ever queues a fix slice unattended, starting with
// the very first one, whose finding is parked (routed_why budget) before the
// person keeps it. The second round, unattended (nil Triage, i.e.
// DefaultTriage, the way --yes and `jig solve` would leave it too), is that
// rule: no fix slice, and the budget-parked finding in needs-a-human.
func TestGateFixBudgetTerminalKeepStillQueuesAFixAndBudgetStaysReached(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)
	fr := 0
	d.Cfg.Gate = &project.GateConfig{FixRounds: &fr}

	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		req := readReviewRequest(t, sd.SliceJSON)
		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{Findings: []ResultFinding{{
				File: "alpha/alpha.go", Line: 1, Title: "round 1 finding", Detail: "d",
				Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test",
			}}, ReviewedPaths: req.MustReview, Summary: "round 1"}
		case 2:
			result = ReviewResult{Findings: []ResultFinding{{
				File: "alpha/alpha.go", Line: 1, Title: "round 2 finding", Detail: "d",
				Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test",
			}}, ReviewedPaths: req.MustReview, Summary: "round 2"}
		default:
			t.Fatalf("unexpected round %d", round)
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	// A human at a terminal keeps every parked finding this round.
	keepTriage := func(in TriageInput) TriageResult {
		asks := make(map[string]AskOutcome, len(in.Asks))
		for _, f := range in.Asks {
			asks[f.ID] = AskOutcome{Keep: true, Human: true, Decision: "ship it"}
		}
		return TriageResult{Asks: asks}
	}
	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Triage: keepTriage})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want one (a human kept the parked finding)", report1.FixSlices)
	}
	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok {
		t.Fatalf("read round 1 findings.yaml: ok=%v err=%v", ok, err)
	}
	if ff1.Findings[0].Status != StatusOpen || ff1.Findings[0].RoutedWhy != RoutedWhyBudget {
		t.Fatalf("round 1 finding = %+v, want open with routed_why budget still recorded (provenance)", ff1.Findings[0])
	}

	// Round 2: the budget stays reached after a person keeps one, so the
	// next round parks again - unattended this time.
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(report2.FixSlices) != 0 {
		t.Fatalf("round 2 FixSlices = %v, want none (the budget stays reached)", report2.FixSlices)
	}
	if len(report2.NeedsHuman) != 1 || report2.NeedsHuman[0].RoutedWhy != RoutedWhyBudget {
		t.Fatalf("round 2 NeedsHuman = %+v, want one budget-parked finding", report2.NeedsHuman)
	}
}

// TestGateFixSliceSizeCapSplitsAnOversizedGroupAndNumbersItsSlices pins
// "Fix slices sized to one session" through the full Gate round:
// gate.fix_slice_findings threads from project.yaml down to the appended
// slices, a (workspace, oracle) group over the bound splits into several
// fix slices, each carrying only its own subset, and the split slices are
// numbered "-1", "-2" from 1. A group that fits in one slice keeps today's
// unsuffixed id, already pinned by TestGateCleanRound2; the packing
// algorithm itself (file order, crowding, an oversized file getting its
// own slice) is pinned at the lower seam by
// TestRouteRoundSplitsAFixGroupPackingFilesGreedilyByPathOrder.
func TestGateFixSliceSizeCapSplitsAnOversizedGroupAndNumbersItsSlices(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)
	fsf := 2
	d.Cfg.Gate = &project.GateConfig{FixSliceFindings: &fsf}

	backend := stubBackend{run: func(sd session.Dispatch) error {
		req := readReviewRequest(t, sd.SliceJSON)
		result := ReviewResult{
			Findings: []ResultFinding{
				{File: "alpha/alpha.go", Line: 1, Title: "a1", Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test"},
				{File: "alpha/alpha.go", Line: 2, Title: "a2", Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test"},
				{File: "alpha/alpha_test.go", Line: 1, Title: "b1", Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: "test"},
			},
			ReviewedPaths: req.MustReview,
			Summary:       "round 1",
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	wantIDs := []string{"fix-1-alpha-test-1", "fix-1-alpha-test-2"}
	if !equalStrings(report.FixSlices, wantIDs) {
		t.Fatalf("FixSlices = %v, want %v", report.FixSlices, wantIDs)
	}

	allSlices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	byID := map[string]store.Slice{}
	for _, s := range allSlices {
		byID[s.ID] = s
	}
	if got := byID["fix-1-alpha-test-1"].Findings; !equalStrings(got, []string{"r1-f1", "r1-f2"}) {
		t.Errorf("slice 1 (alpha/alpha.go, fits the bound) Findings = %v, want [r1-f1 r1-f2]", got)
	}
	if got := byID["fix-1-alpha-test-2"].Findings; !equalStrings(got, []string{"r1-f3"}) {
		t.Errorf("slice 2 (alpha/alpha_test.go, crowded out of slice 1) Findings = %v, want [r1-f3]", got)
	}
}

// TestGateFailedRoundCommitsAndPushesStore is the chain of a gate round that
// fails, on one ticket whose build and reviewer source are shared by its steps.
//
// A round failing after its gate-open journal line (REVIEW_INVALID here; a
// REVIEW_FAILED or a routing error take the exact same path) left the journal
// line jig itself just appended uncommitted: the round returns before Gate's
// own end-of-round Store.Push, so that tracked change sat uncommitted and
// unpushed until whatever later command's own Store.Sync happened to sweep it
// into an anonymous commit, on this ticket or any other, rather than
// attributed to this round's own failure.
//
// A failure between the gate-open journal line and the old roundNum assignment
// (manifest.Resolve, runGateOracles, or existingGateRounds itself) committed a
// permanent, pushed store subject reading "gate round 0" - roundNum's zero
// value, never actually resolved - and interpolated the raw error with %v,
// which for a manifest parse failure includes the gate lease's absolute host
// path. The third step forces the failure to land on round 2 (not round 1) so a
// wrong zero cannot be mistaken for a coincidentally-correct round number, and
// breaks manifest.Resolve specifically so the raw error would carry an absolute
// path if it leaked.
func TestGateFailedRoundCommitsAndPushesStore(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	attempt := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		attempt++
		result := ReviewResult{Summary: "round 1"}
		if attempt == 1 {
			// Missing coverage: REVIEW_INVALID, after the gate-open
			// journal line has already been appended.
			result.ReviewedPaths = nil
		} else {
			reviewData, err := os.ReadFile(sd.SliceJSON)
			if err != nil {
				t.Fatalf("read review.json: %v", err)
			}
			var req ReviewRequest
			if err := json.Unmarshal(reviewData, &req); err != nil {
				t.Fatalf("parse review.json: %v", err)
			}
			result.ReviewedPaths = req.MustReview
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	step(t, "a round that fails after its gate-open line commits and pushes the store", func(t *testing.T) {
		_, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
		if err == nil {
			t.Fatal("Gate: want an error (missing must_review coverage)")
		}
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID", err)
		}

		// The store's working copy must already be clean and pushed, under a
		// commit naming this round's own failure - no leftover tracked change
		// from the failed round's own gate-open journal line, review.json or
		// result.json, and no dependence on a later command's Sync to sweep
		// and push it anonymously.
		wantFailureCommit(t, d, "main", fx.Ticket+": gate round 1 failed: REVIEW_INVALID")
	})

	step(t, "a plain rerun works with no manual cleanup", func(t *testing.T) {
		// Sync sweeps any leftover uncommitted state on its own before it pulls,
		// push or no push: the corrected round succeeds as round 1 (the failed
		// attempt above wrote no gate/round-1/ directory).
		report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Gate rerun: %v", err)
		}
		if report.Round != 1 {
			t.Fatalf("rerun Round = %d, want 1", report.Round)
		}
	})

	step(t, "a failure before the round is known names the real round and no raw error", func(t *testing.T) {
		// Push an unparsable .claude/jig.yaml onto the ticket branch: round 2's
		// manifest.Resolve(lease.Dir) then fails after the gate-open journal
		// line, naming the gate lease's own absolute path in its error text.
		buildDir := buildLeaseDir(t, fx)
		claudeDir := filepath.Join(buildDir, ".claude")
		if err := os.MkdirAll(claudeDir, 0o755); err != nil {
			t.Fatalf("mkdir .claude: %v", err)
		}
		if err := os.WriteFile(filepath.Join(claudeDir, "jig.yaml"), []byte("workspaces: [this is not valid yaml"), 0o644); err != nil {
			t.Fatalf("write broken jig.yaml: %v", err)
		}
		if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
			t.Fatalf("git add jig.yaml: %v", err)
		}
		if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "break manifest"); err != nil {
			t.Fatalf("commit jig.yaml: %v", err)
		}
		branch := ticketBranch(fx.Ticket)
		if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
			t.Fatalf("push branch: %v", err)
		}

		gateLease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Gate)
		if err != nil {
			t.Fatalf("reacquire gate lease: %v", err)
		}

		_, err = Gate(d, src, GateOpts{Ticket: fx.Ticket})
		if err == nil {
			t.Fatal("Gate round 2: want an error (broken manifest)")
		}

		subject, gerr := gitx.Run(d.Store.Root, "log", "-1", "--format=%s")
		if gerr != nil {
			t.Fatalf("read store log: %v", gerr)
		}
		if strings.Contains(subject, "round 0") {
			t.Fatalf("push subject = %q, round was never actually resolved before journaling", subject)
		}
		if !strings.Contains(subject, "round 2") {
			t.Fatalf("push subject = %q, want it to name the real round 2", subject)
		}
		if strings.Contains(subject, gateLease.Dir) {
			t.Fatalf("push subject = %q, leaked the gate lease's absolute host path", subject)
		}
		if strings.Contains(subject, "yaml") {
			t.Fatalf("push subject = %q, leaked the raw manifest parse error instead of a code", subject)
		}
	})
}

// TestGateRecurrenceBoundSurvivesANoteInBetween reproduces the review
// finding that labeling a recurrence "note" reset its identity: a noted
// finding left review.json's open list entirely, so no later round could
// ever cite it as `prior` again, and the same underlying problem reported
// afterward became a brand new id at recurrences 0 - the recurrence bound
// (two) was evadable just by alternating fix and note.
//
// Round 1 reports a fix (r1-f1, recurrences 0). Its fix slice is marked
// green directly (frontier's own job, not this test's). Round 2 cites
// prior: r1-f1 as a note: recurrences 1, status noted. Round 3 must still
// be able to cite prior: r1-f1 (proving it stayed a citable id although
// noted) and report it as a fix again: recurrences 2 forces it to asked
// (routed_as: ask) whatever this round's own label, exactly like an
// uninterrupted fix/fix recurrence would - visible here in its own
// per-finding fix slice, since the finding's build target already
// resolves in full and DefaultTriage auto-keeps it.
func TestGateRecurrenceBoundSurvivesANoteInBetween(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1",
			}
		case 2:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 0 {
				t.Fatalf("round 2 review.json Open = %+v, want r1-f1 at 0 recurrences", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "still there, harmless now",
					Detail: "d", Action: ActionNote, Risk: RiskLow, RiskRationale: "r",
					Oracle: "test", Prior: "r1-f1",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 2",
			}
		case 3:
			// The finding jig's own round-2 bookkeeping recorded as noted
			// must still be offered as a citable id: the fix.
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 1 || req.Open[0].Action != ActionNote {
				t.Fatalf("round 3 review.json Open = %+v, want r1-f1 (noted, 1 recurrence) fed back", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "back again",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "test", Prior: "r1-f1",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 3",
			}
		default:
			t.Fatalf("unexpected round %d", round)
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want exactly one", report1.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report1.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 1 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report1.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 1 fix slice state: %v", err)
	}

	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(report2.FixSlices) != 0 {
		t.Fatalf("round 2 FixSlices = %v, want none (a note is never routed)", report2.FixSlices)
	}
	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("read round 2 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff2.Findings) != 1 || ff2.Findings[0].Status != StatusNoted || ff2.Findings[0].Recurrences != 1 {
		t.Fatalf("round 2 findings = %+v, want [r1-f1 noted, 1 recurrence]", ff2.Findings)
	}

	// A noted finding is not outstanding work, so it no longer forces a
	// round to dispatch on its own (Q8's clean-without-dispatch shortcut):
	// a trivial, unrelated commit gives round 3 a reason to dispatch,
	// exactly like a real ticket branch that keeps advancing.
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	if err := os.WriteFile(filepath.Join(buildDir, "beta", "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatalf("write unrelated.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add unrelated.txt: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "add unrelated.txt"); err != nil {
		t.Fatalf("commit unrelated.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	// r1-f1's file (alpha/alpha.go, oracle test) already has a full build
	// target, so DefaultTriage - unattended, no terminal - auto-keeps the
	// forced ask rather than leaving it for a human: that is what proves
	// the bound actually fired here, distinctly from an ordinary fix. A
	// fix routes into the round's grouped fix-<n>-<workspace>-<oracle>
	// slice; only a (kept) ask gets its own per-finding
	// fix-<n>-<finding-id> slice (route.go's Q9 id scheme), and
	// routed_as/recurrences on the persisted finding confirm it directly.
	if len(report3.Findings) != 1 || report3.Findings[0].ID != "r1-f1" {
		t.Fatalf("round 3 Findings = %+v, want exactly [r1-f1]", report3.Findings)
	}
	f := report3.Findings[0]
	if f.RoutedAs != ActionAsk || f.Recurrences != 2 {
		t.Fatalf("round 3 finding = %+v, want routed_as ask, 2 recurrences (the bound reached)", f)
	}
	if len(report3.FixSlices) != 1 || report3.FixSlices[0] != "fix-3-r1-f1" {
		t.Fatalf("round 3 FixSlices = %v, want exactly [fix-3-r1-f1] (the ask's own per-finding slice)", report3.FixSlices)
	}
}

// TestGateReviewerRoundsProceedWhenAnOpenFindingsFileBecomesIgnoredAndGenerated
// reproduces the wedge a text match on git's cat-file message used to fall
// into: an open finding's file is later untracked and gitignored on the
// ticket branch, but an oracle regenerates it on disk in the gate lease
// before the next round's review. FileExistsAtRev must report it absent at
// head from the tree alone, never from what happens to sit in the working
// tree, or every later round fails the same way and no dismissal can get
// past it.
func TestGateReviewerRoundsProceedWhenAnOpenFindingsFileBecomesIgnoredAndGenerated(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)

	// Round 1 needs a tracked file for its open finding to point at.
	if err := os.WriteFile(filepath.Join(buildDir, "alpha", "gen.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("write gen.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add gen.txt: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "add tracked gen.txt"); err != nil {
		t.Fatalf("commit gen.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	d := newDeps(t, fx)
	d.Oracle = nil // the real oracle run is what this test is about
	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/gen.txt", Line: 1, Title: "a generated file was committed",
					Detail: "gen.txt should not be tracked", Action: ActionFix,
					// High risk: this test is about gen.txt's own
					// ignored/generated handling across rounds, not the
					// risk floor, so the risk stays well clear of it.
					Risk: RiskHigh, RiskRationale: "build artifact churn", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1 summary",
			}
		case 2:
			// gen.txt is untracked and gitignored as of this round's head, so
			// it no longer belongs in must_review; nothing to report on it.
			if containsString(req.MustReview, "alpha/gen.txt") {
				t.Fatalf("round 2 must_review = %v, want it to exclude alpha/gen.txt (absent at head)", req.MustReview)
			}
			result = ReviewResult{ReviewedPaths: req.MustReview, Summary: "round 2 summary"}
		default:
			t.Fatalf("unexpected round %d", round)
		}

		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Verdict != "fix-slices" {
		t.Fatalf("round 1 Verdict = %q, want fix-slices", report1.Verdict)
	}
	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok {
		t.Fatalf("read round 1 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff1.Findings) != 1 || ff1.Findings[0].Status != StatusOpen {
		t.Fatalf("round 1 findings = %+v, want one open finding", ff1.Findings)
	}
	findingID := ff1.Findings[0].ID

	// Advance the ticket branch: untrack gen.txt, ignore it, and make the
	// alpha oracle regenerate it as a build artifact, exactly as a real
	// generated-artifact test would leave it sitting in the lease.
	if _, err := gitx.Run(buildDir, "rm", "--cached", "alpha/gen.txt"); err != nil {
		t.Fatalf("git rm --cached gen.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, ".gitignore"), []byte("alpha/gen.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	alphaTest, err := os.ReadFile(filepath.Join(buildDir, "alpha", "alpha_test.go"))
	if err != nil {
		t.Fatalf("read alpha_test.go: %v", err)
	}
	regen := strings.Replace(string(alphaTest), "import \"testing\"",
		"import (\n\t\"os\"\n\t\"testing\"\n)", 1)
	regen += "\nfunc TestZZRegenGen(t *testing.T) {\n" +
		"\tif err := os.WriteFile(\"gen.txt\", []byte(\"regenerated\\n\"), 0o644); err != nil {\n" +
		"\t\tt.Fatalf(\"write gen.txt: %v\", err)\n" +
		"\t}\n}\n"
	if err := os.WriteFile(filepath.Join(buildDir, "alpha", "alpha_test.go"), []byte(regen), 0o644); err != nil {
		t.Fatalf("write regenerating alpha_test.go: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "untrack and regenerate gen.txt"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	// The fix slice routed from round 1 is still queued; this round only
	// exercises the reviewer path, not frontier's own build loop.
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "clean" {
		t.Fatalf("round 2 Verdict = %q, want clean (the ignored, regenerated file must clear, not wedge the round)", report2.Verdict)
	}
	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("read round 2 findings.yaml: ok=%v err=%v", ok, err)
	}
	if !equalStrings(ff2.Cleared, []string{findingID}) {
		t.Fatalf("round 2 cleared = %v, want [%s]", ff2.Cleared, findingID)
	}

	// A third round must proceed too: the round after the wedge is not a
	// one-time reprieve, the file stays absent at head for good.
	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if report3.Verdict != "clean" {
		t.Fatalf("round 3 Verdict = %q, want clean", report3.Verdict)
	}
}

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
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add jig.yaml: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "add a second manifest oracle"); err != nil {
		t.Fatalf("commit jig.yaml: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}
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
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add jig.yaml: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "rename a manifest oracle"); err != nil {
		t.Fatalf("commit jig.yaml: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}
}

// setupStaleOracleThroughRoundTwo drives a fixture through two green gate
// rounds recording the same open finding on a two-oracle manifest with an
// explicit oracle ("vet"), then renames that oracle to "lint" on the ticket
// branch: a fix slice recorded a valid oracle, went green twice, and the
// manifest changed between rounds so that recorded oracle no longer
// resolves. round3 supplies round 3's own review
// result (a recurrence of the same finding via Prior, reported as a note
// with its oracle omitted so validation's "must name one of the manifest's
// oracles" rule does not apply - the same way the real scenario reaches jig
// with a stale, carried-forward oracle instead of a freshly named one) and
// gets req (round 3's review.json request) to build it from. It returns the
// deps, fixture and reviewer source with rounds 1 and 2 already applied,
// ready for the caller to run round 3 through Gate.
func setupStaleOracleThroughRoundTwo(t *testing.T, round3 func(req ReviewRequest) ReviewResult) (Deps, *fixture.Fixture, GateSource) {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	addSecondOracle(t, buildDir, branch)

	d := newDeps(t, fx)
	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "vet",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1",
			}
		case 2:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" {
				t.Fatalf("round 2 review.json Open = %+v, want r1-f1 fed back", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "vet", Prior: "r1-f1",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 2",
			}
		case 3:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 1 {
				t.Fatalf("round 3 review.json Open = %+v, want r1-f1 at 1 recurrence", req.Open)
			}
			result = round3(req)
		default:
			t.Fatalf("unexpected round %d", round)
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want exactly one", report1.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report1.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 1 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report1.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 1 fix slice state: %v", err)
	}

	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(report2.FixSlices) != 1 {
		t.Fatalf("round 2 FixSlices = %v, want exactly one", report2.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report2.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 2 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report2.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 2 fix slice state: %v", err)
	}

	// Advance the branch: the oracle both green fix slices recorded no
	// longer exists in the manifest by round 3.
	renameOracle(t, buildDir, branch, "vet", "lint")

	return d, fx, src
}

// TestGateStaleOracleRecurrenceTerminalKeepBuildsSliceOnTheChosenOracle
// proves the terminal half of the same rule: a human keeping the same
// finding supplies the missing oracle (exactly what cmd/jig's
// interactiveTriage prompt gathers once BuildTargetGaps reports it
// missing), and routing builds the fix slice on that chosen oracle, not the
// stale one the finding carried forward.
func TestGateStaleOracleRecurrenceTerminalKeepBuildsSliceOnTheChosenOracle(t *testing.T) {
	t.Parallel()

	d, fx, src := setupStaleOracleThroughRoundTwo(t, func(req ReviewRequest) ReviewResult {
		return ReviewResult{
			Findings: []ResultFinding{{
				File: "alpha/alpha.go", Line: 1, Title: "still there", Detail: "d",
				Action: ActionNote, Risk: RiskLow, RiskRationale: "r", Prior: "r1-f1",
			}},
			ReviewedPaths: req.MustReview,
			Summary:       "round 3",
		}
	})

	// The triage hook stands in for cmd/jig's interactiveTriage: a human at
	// a terminal keeping this finding and being prompted for an oracle
	// (its own, "vet", no longer resolves) answers "lint".
	triage := func(in TriageInput) TriageResult {
		if len(in.Asks) != 1 || in.Asks[0].ID != "r1-f1" {
			t.Fatalf("TriageInput.Asks = %+v, want exactly [r1-f1]", in.Asks)
		}
		return TriageResult{Asks: map[string]AskOutcome{
			"r1-f1": {Keep: true, Decision: "use lint here", Oracle: "lint", Human: true},
		}}
	}

	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Triage: triage})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if len(report3.NeedsHuman) != 0 {
		t.Fatalf("NeedsHuman = %+v, want none (the human decided)", report3.NeedsHuman)
	}
	if len(report3.FixSlices) != 1 {
		t.Fatalf("FixSlices = %v, want exactly one", report3.FixSlices)
	}
	slices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	var fs *store.Slice
	for i := range slices {
		if slices[i].ID == report3.FixSlices[0] {
			fs = &slices[i]
		}
	}
	if fs == nil {
		t.Fatalf("appended fix slice %q not found in slices.yaml", report3.FixSlices[0])
	}
	if fs.Oracle != "lint" {
		t.Fatalf("fix slice oracle = %q, want lint (the human's chosen oracle, not the stale one)", fs.Oracle)
	}
	if !strings.Contains(fs.Goal, "use lint here") {
		t.Fatalf("fix slice goal missing the human's decision text:\n%s", fs.Goal)
	}

	ff, ok, err := readFindingsYAML(d.Store, fx.Ticket, 3)
	if err != nil || !ok {
		t.Fatalf("read round 3 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff.Findings) != 1 || ff.Findings[0].Status != StatusOpen || ff.Findings[0].Oracle != "lint" || ff.Findings[0].Triage != TriageHuman {
		t.Fatalf("round 3 finding = %+v, want open, oracle lint, triage human", ff.Findings[0])
	}
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

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")

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
	}}, nil, "round 1", nil, nil)
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
