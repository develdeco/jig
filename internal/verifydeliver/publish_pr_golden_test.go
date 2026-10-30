package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// readPRFiles reads ticket's published pr/<repo>.md and pr/review-notes.md
// out of d's store, for a golden-render comparison against the two files
// the brief's "Tests" section names: "Golden renders of both files for
// four tickets: one with a brief; one with an explicit intent; one with an
// inferred intent (no Intent section); an adopted ticket."
func readPRFiles(t *testing.T, d Deps, ticket, repo string) (body, notes string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d.Store.TicketDir(ticket), "pr", repo+".md"))
	if err != nil {
		t.Fatalf("read pr/%s.md: %v", repo, err)
	}
	n, err := os.ReadFile(filepath.Join(d.Store.TicketDir(ticket), "pr", "review-notes.md"))
	if err != nil {
		t.Fatalf("read pr/review-notes.md: %v", err)
	}
	return string(b), string(n)
}

// greenShortSHA returns the short sha of slice's last green journaled
// commit - the same lookup render.go's own writePRBody uses
// (lastGreenCommits) - so a golden render's expected text can name the
// exact sha the real render will, without hardcoding a commit sha that a
// fixture rebuilt elsewhere (a different temp dir embedded in
// .claude/jig.yaml, say) would not reproduce.
func greenShortSHA(t *testing.T, d Deps, ticket, sliceID string) string {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	sha, ok := lastGreenCommits(lines)[sliceID]
	if !ok {
		t.Fatalf("no green commit journaled for slice %s", sliceID)
	}
	return shortSHA(sha)
}

// driveFixSlice plays scenario slice's attempt 1 against the fixture's
// build lease and journals it green, the same shape driveFix1 drives for
// the one-fix-slice base scenario, parameterized for a scenario (like
// "reviewer") whose round-by-round fix slices have their own ids.
func driveFixSlice(t *testing.T, fx *fixture.Fixture, model, slice string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	driveAttempt(t, st, fx, buildLeaseDir(t, fx), model, slice, 1)
}

// gateThroughReviewerScenario drives the fixture's "reviewer" scenario
// branch through all three of its scripted gate rounds, with the triage
// cmd/jig/gate_reviewer_e2e_test.go/TestGateReviewerRoundsThroughMain pins
// end to end through cmd/jig's own CLI: a fix kept (r1-f1, high), a second
// fix dismissed at the batch prompt (r1-f2, low), an ask kept with a
// decision (r1-f3, medium), a note (r1-f4, low); a delta round where the
// kept fix recurs and the dismissed one reaches no one; a clean third
// round. It is the one scenario in this file whose gate rounds write
// findings.yaml at all (the reviewer source, not the scripted one), so it is
// the only one exercising the findings-outcome rules a golden render needs
// to pin: fixed-by-slice, dismissed-by-a-human, noted, and the counts and
// risk ordering built from them.
func gateThroughReviewerScenario(t *testing.T, fx *fixture.Fixture, d Deps) GateReport {
	t.Helper()
	driveBuild(t, fx, "rung-a")

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	src := newReviewerGateSource(backend)

	// The scenario's own low-risk fix (whitespace trimming) is below the
	// default floor and routes straight to a note, never reaching this
	// batch; nothing is left to dismiss here.
	round1Triage := func(in TriageInput) TriageResult {
		asks := map[string]AskOutcome{}
		for _, f := range in.Asks {
			asks[f.ID] = AskOutcome{Keep: true, Decision: "Use a warm, casual tone; no exclamation marks.", Human: true}
		}
		return TriageResult{FixHuman: true, Asks: asks}
	}
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Triage: round1Triage})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("round 1 verdict = %q, want fix-slices", report.Verdict)
	}

	driveFixSlice(t, fx, "rung-a", "fix-1-alpha-test")
	driveFixSlice(t, fx, "rung-a", "fix-1-r1-f3")

	round2Triage := func(TriageInput) TriageResult {
		return TriageResult{FixHuman: true, Asks: map[string]AskOutcome{}}
	}
	report, err = Gate(d, src, GateOpts{Ticket: fx.Ticket, Triage: round2Triage})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("round 2 verdict = %q, want fix-slices", report.Verdict)
	}

	driveFixSlice(t, fx, "rung-a", "fix-2-alpha-test")

	report, err = Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if report.Verdict != "clean" {
		t.Fatalf("round 3 verdict = %q, want clean", report.Verdict)
	}
	return report
}

// TestGoldenRenderBriefSourcedTicket pins both files for a ticket whose
// intent comes from its brief.md, gated through three real reviewer rounds
// (the "reviewer" scenario) so the findings-outcome rules - fixed by slice
// X and cleared at round N, noted (a genuine note, and the scenario's own
// low-risk fix once the risk floor routes it there instead of into the
// triage batch), the risk-then-id sort, the findings count, the oracle
// names, and Coverage's touched-vs-reviewed split - are all exercised, not
// merely the Intent/What-changed shape the other three golden tickets below
// cover. It is also the one golden ticket
// with real fix slices, so it pins what their bullets are: one line each,
// the first line of the builder prompt buildFixSlices wrote, with the short
// sha beside it and none of the gate's own finding text, which belongs to
// the review-notes comment this body only points at. Its build recorded
// nothing, so the body has no ## Demo section.
func TestGoldenRenderBriefSourcedTicket(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "reviewer", Home: t.TempDir()})
	d := newDeps(t, fx)
	gateThroughReviewerScenario(t, fx, d)

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	body, notes := readPRFiles(t, d, fx.Ticket, "fixture-repo")

	wantBody := fmt.Sprintf(`## Intent

Give the fixture repo two small, real fixes so the frontier loop has
something to chew on: a broken function to repair, a caller to add once it is
fixed, a wording decision to make, and an environment class to smoke-test.

## What changed

- Fix Clamp so TestClamp passes. (%[1]s)
- Give Clamp a percentage-range caller, once it works. (%[2]s)
- Decide and implement the greeting's tone. (%[3]s)
- Smoke-test the rig environment class. (%[4]s)

Fixes from review:

- Fix these gate findings (%[5]s)
- Gate finding r1-f3, kept by the human (%[6]s)
- Fix these gate findings (%[7]s)

## Verification

Oracles:

- test

Reviewed head:

- fixture-repo: %[7]s

Revalidation tier: none

Findings: 2 fixed, 0 dismissed, 2 noted, 0 asked. See the pull request's first comment for detail.
`,
		greenShortSHA(t, d, fx.Ticket, "a"),
		greenShortSHA(t, d, fx.Ticket, "b"),
		greenShortSHA(t, d, fx.Ticket, "c"),
		greenShortSHA(t, d, fx.Ticket, "d"),
		greenShortSHA(t, d, fx.Ticket, "fix-1-alpha-test"),
		greenShortSHA(t, d, fx.Ticket, "fix-1-r1-f3"),
		greenShortSHA(t, d, fx.Ticket, "fix-2-alpha-test"),
	)
	if body != wantBody {
		t.Fatalf("pr/fixture-repo.md =\n%s\nwant\n%s", body, wantBody)
	}

	wantNotes := `# Review Notes

## Round 3

round 3: nothing new; the shared-helper note from round 1 remains on record

## Findings

- **Add's doc comment still omits overflow behavior** (high): silent overflow could misfeed a downstream money calculation
  - Status: fixed by slice fix-2-alpha-test and cleared at round 3
  - Oracle: test

- **Greeting formality beyond casual is not decided** (medium): the wrong formality reaches every caller of Greet
  - Status: fixed by slice fix-1-r1-f3 and cleared at round 2
  - Oracle: test

- **Greet does not trim surrounding whitespace from name** (low): cosmetic only, no functional or safety impact
  - Status: noted
  - Oracle: test

- **ClampPercent and Clamp could share a bounds-check helper later** (low): no functional risk, purely a maintainability idea
  - Status: noted
  - Oracle: test

## Coverage

Touched files:

- alpha/alpha.go
- alpha/percent.go
- alpha/percent_test.go
- beta/beta.go
- beta/version.go

Reviewed paths:

- .claude/jig.yaml
- alpha/alpha.go
- alpha/alpha_test.go
- alpha/percent.go
- alpha/percent_test.go
- beta/beta.go
- beta/beta_test.go
- beta/version.go
- go.mod

Probes current at the reviewed head:

- alpha/percent.go:5 caught: upper bound 100 to 101 (ClampPercent bounds a percentage; round 3)

Set aside as on no critical path:

- alpha/percent_test.go: tests

Hunks no probe or skip covers:

- alpha/alpha.go:5-7
- alpha/alpha.go:17-19
- beta/beta.go:4-6
- beta/version.go:1-5

`
	if notes != wantNotes {
		t.Fatalf("pr/review-notes.md =\n%s\nwant\n%s", notes, wantNotes)
	}
}

// TestGoldenRenderInferredIntentTicket pins both files for a ticket whose
// last clean round's intent is inferred: this is the test the security
// promise behind renderIntentSection rests on (.github/SECURITY.md,
// docs/adr/0012) - an inferred intent, here deliberately given marker text
// that would be unmistakable if it leaked, produces no ## Intent section at
// all, not an empty one.
func TestGoldenRenderInferredIntentTicket(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	removeBrief(t, d, fx.Ticket)
	const marker = "TRANSCRIPT-ONLY-MARKER: a private summary of the operator's own Claude Code session"
	if err := d.Store.WriteIntent(fx.Ticket, store.Intent{Source: IntentSourceInferred, Text: marker}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}
	driveBuild(t, fx, "rung-a")
	src := NewFakeGateSource(fx.ScenarioDir)
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("round 1 verdict = %q, want fix-slices", report.Verdict)
	}
	if report.Intent.Source != IntentSourceInferred {
		t.Fatalf("report.Intent.Source = %q, want %q", report.Intent.Source, IntentSourceInferred)
	}
	driveFix1(t, fx, "rung-a")
	report, err = Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report.Verdict != "clean" {
		t.Fatalf("round 2 verdict = %q, want clean", report.Verdict)
	}

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	body, notes := readPRFiles(t, d, fx.Ticket, "fixture-repo")

	if strings.Contains(body, marker) || strings.Contains(notes, marker) {
		t.Fatalf("the inferred intent's own private text leaked into a published file:\nbody:\n%s\nnotes:\n%s", body, notes)
	}
	if strings.Contains(body, "## Intent") {
		t.Fatalf("pr/fixture-repo.md has an ## Intent section for an inferred intent, want none at all:\n%s", body)
	}

	wantBody := fmt.Sprintf(`## What changed

- Fix Clamp so TestClamp passes. (%s)
- Give Clamp a percentage-range caller, once it works. (%s)
- Decide and implement the greeting's tone. (%s)
- Smoke-test the rig environment class. (%s)

Fixes from review:

- Add a doc comment to Add explaining its behavior. (%s)

## Verification

Oracles:

- test

Revalidation tier: none

Findings: 0 fixed, 0 dismissed, 0 noted, 0 asked. See the pull request's first comment for detail.
`,
		greenShortSHA(t, d, fx.Ticket, "a"),
		greenShortSHA(t, d, fx.Ticket, "b"),
		greenShortSHA(t, d, fx.Ticket, "c"),
		greenShortSHA(t, d, fx.Ticket, "d"),
		greenShortSHA(t, d, fx.Ticket, "fix-1"),
	)
	if body != wantBody {
		t.Fatalf("pr/fixture-repo.md =\n%s\nwant\n%s", body, wantBody)
	}

	wantNotes := `# Review Notes

## Coverage

Touched files:

- alpha/alpha.go
- alpha/percent.go
- alpha/percent_test.go
- beta/beta.go
- beta/version.go

`
	if notes != wantNotes {
		t.Fatalf("pr/review-notes.md =\n%s\nwant\n%s", notes, wantNotes)
	}
}
