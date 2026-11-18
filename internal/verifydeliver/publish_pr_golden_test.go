package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
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

	wantBody := `## What

- JIG-1

## Verification

Oracles green:

- test

Gate rounds:

- Round 1: 1 fix slice asked
- Round 2: clean
`
	wantBody = withNoFinalRun(t, shortSHA(run(t, buildLeaseDir(t, fx), "rev-parse", "HEAD")), wantBody)
	if body != wantBody {
		t.Fatalf("pr/fixture-repo.md =\n%s\nwant\n%s", body, wantBody)
	}

	wantNotes := fmt.Sprintf(`# Review Notes

## Slices

- a (%[1]s): Fix Clamp so TestClamp passes.
- b (%[2]s): Give Clamp a percentage-range caller, once it works.
- c (%[3]s): Decide and implement the greeting's tone.
- d (%[4]s): Smoke-test the rig environment class.
- fix-1 (%[5]s): Add a doc comment to Add explaining its behavior.

## Coverage

Touched files:

- alpha/alpha.go
- alpha/percent.go
- alpha/percent_test.go
- beta/beta.go
- beta/version.go

`,
		greenShortSHA(t, d, fx.Ticket, "a"),
		greenShortSHA(t, d, fx.Ticket, "b"),
		greenShortSHA(t, d, fx.Ticket, "c"),
		greenShortSHA(t, d, fx.Ticket, "d"),
		greenShortSHA(t, d, fx.Ticket, "fix-1"),
	)
	if notes != wantNotes {
		t.Fatalf("pr/review-notes.md =\n%s\nwant\n%s", notes, wantNotes)
	}
}
