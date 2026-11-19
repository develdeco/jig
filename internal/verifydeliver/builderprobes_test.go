package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// ledgerTicket names the ticket of the journals these tests write.
const ledgerTicket = "ticket-one"

// ledgerRepo is a one-file git repo and the blob of that file at HEAD.
func ledgerRepo(t *testing.T) (roundFiles, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "f"}, {"config", "user.email", "f@example.invalid"}} {
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "c1"}} {
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	blob, err := gitx.RevParse(dir, "HEAD:a.go")
	if err != nil {
		t.Fatal(err)
	}
	return roundFiles{repos: []roundRepo{{Dir: dir, Head: "HEAD"}}}, blob
}

// TestLedgerBuilderProbeIsStaleAfterAnEditAndCurrentWithout: a builder's probe
// is current while its file's blob at the head is the one it recorded, and
// stale once the file differs; only a current, caught one covers its term, and
// a current survivor is probed again.
func TestLedgerBuilderProbeIsStaleAfterAnEditAndCurrentWithout(t *testing.T) {
	t.Parallel()
	rf, blob := ledgerRepo(t)
	probed, current, _, err := currentLedger(rf, []ProbeRecord{
		{Slice: "fix-1", File: "a.go", Line: 3, Outcome: ProbeCaught, Blob: blob},
		{Slice: "fix-1", File: "a.go", Line: 5, Outcome: ProbeCaught, Blob: "an older blob"},
		{Slice: "fix-1", File: "a.go", Line: 7, Outcome: ProbeSurvived, Blob: blob},
	}, nil)
	if err != nil {
		t.Fatalf("currentLedger: %v", err)
	}
	var states []string
	for _, p := range probed {
		states = append(states, p.State+"/"+p.Slice)
	}
	if got, want := strings.Join(states, ","), "current/fix-1,stale/fix-1,current/fix-1"; got != want {
		t.Errorf("states = %s, want %s", got, want)
	}
	if len(current) != 1 || current[0].Line != 3 {
		t.Errorf("covering probes = %+v, want only the current caught one at line 3", current)
	}
}

// TestLedgerRanksProbesByWhenTheyWereMade: the latest probe of a line, across
// builders and rounds, says what it is now. A builder's probe made after round
// 1 closed supersedes round 1's and is superseded by round 2's; one made
// before round 1 closed is earlier than round 1's; of two builder probes of a
// line the later wins.
func TestLedgerRanksProbesByWhenTheyWereMade(t *testing.T) {
	t.Parallel()
	rf, blob := ledgerRepo(t)
	probed, _, _, err := currentLedger(rf, []ProbeRecord{
		// line 1: a reviewer's survivor, then the builder's re-probe after it.
		{Round: 1, File: "a.go", Line: 1, Outcome: ProbeSurvived, Blob: blob},
		{Slice: "s", After: 1, At: 10, File: "a.go", Line: 1, Outcome: ProbeCaught, Blob: blob},
		// line 2: the builder's survivor, then a reviewer's probe of the fix.
		{Slice: "s", After: 1, At: 10, File: "a.go", Line: 2, Outcome: ProbeSurvived, Blob: blob},
		{Round: 2, File: "a.go", Line: 2, Outcome: ProbeCaught, Blob: blob},
		// line 3: a builder probe made before round 1 closed is earlier.
		{Slice: "s", After: 0, At: 5, File: "a.go", Line: 3, Outcome: ProbeCaught, Blob: blob},
		{Round: 1, File: "a.go", Line: 3, Outcome: ProbeSurvived, Blob: blob},
		// line 4: two builder probes, the later wins.
		{Slice: "s", After: 0, At: 5, File: "a.go", Line: 4, Outcome: ProbeSurvived, Blob: blob},
		{Slice: "s", After: 0, At: 6, File: "a.go", Line: 4, Outcome: ProbeCaught, Blob: blob},
	}, nil)
	if err != nil {
		t.Fatalf("currentLedger: %v", err)
	}
	var got []string
	for _, p := range probed {
		got = append(got, p.State)
	}
	want := []string{"stale", "current", "stale", "current", "stale", "current", "stale", "current"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("states = %v, want %v", got, want)
	}
}

// TestLedgerReadsBuilderProbesFromTheJournal: the ledger before a round holds
// the probes and skips builders journaled, with their slice, and places each
// by the rounds that had closed when it was made.
func TestLedgerReadsBuilderProbesFromTheJournal(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	at := func(sec int) string {
		return time.Date(2026, 1, 1, 0, 0, sec, 0, time.UTC).Format(time.RFC3339Nano)
	}
	probe := &journal.ProbeNote{Seam: "s", File: "a.go", Line: 5, Mutation: "m", Command: "go test", Outcome: ProbeCaught, Blob: "b1"}
	for _, l := range []journal.Line{
		{TS: at(1), Event: journal.EventBuilderProbe, Slice: "first", Probe: probe},
		{TS: at(2), Event: journal.EventGateRound, Attempt: 1},
		{TS: at(3), Event: journal.EventBuilderProbe, Slice: "fix-1", Probe: probe},
		{TS: at(4), Event: journal.EventBuilderProbe, Slice: "fix-1", Skip: &journal.SkipNote{File: "a_test.go", Why: "tests", Blob: "b2"}},
		{TS: at(5), Event: journal.EventOracle, Slice: "fix-1"},
	} {
		if err := journal.Append(st, ledgerTicket, l); err != nil {
			t.Fatal(err)
		}
	}
	probes, skips, err := ledgerBefore(st, ledgerTicket, 2)
	if err != nil {
		t.Fatalf("ledgerBefore: %v", err)
	}
	if len(probes) != 2 || probes[0].Slice != "first" || probes[0].After != 0 || probes[1].Slice != "fix-1" || probes[1].After != 1 || probes[1].Blob != "b1" {
		t.Errorf("probes = %+v, want the two builder probes, the second after round 1", probes)
	}
	if len(skips) != 1 || skips[0].Slice != "fix-1" || skips[0].File != "a_test.go" || skips[0].Blob != "b2" {
		t.Errorf("skips = %+v, want the builder's skip of a_test.go", skips)
	}
}

// TestPublishCoverageNamesTheSliceOfABuilderProbe: the review notes' ledger
// names the slice whose probe a builder-made entry is, and the round a
// reviewer's is from.
func TestPublishCoverageNamesTheSliceOfABuilderProbe(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	cov := &behaviorCoverage{Probes: []ProbedEntry{
		{Round: 1, ResultProbe: ResultProbe{Seam: "the clamp", File: "a.go", Line: 3, Mutation: "raise it", Outcome: ProbeCaught}, State: ProbeCurrent},
		{Slice: "fix-1", ResultProbe: ResultProbe{Seam: "the clamp", File: "a.go", Line: 9, Mutation: "negate it", Outcome: ProbeCaught}, State: ProbeCurrent},
	}}
	if err := writeReviewNotes(st, ledgerTicket, "review-notes.md", "", 1, nil, "", nil, nil, nil, nil, cov, "", nil); err != nil {
		t.Fatal(err)
	}
	notes := readNotes(t, st, ledgerTicket, "review-notes.md")
	for _, want := range []string{"(the clamp; round 1)", "(the clamp; builder, slice fix-1)"} {
		if !strings.Contains(notes, want) {
			t.Errorf("review notes lack %q:\n%s", want, notes)
		}
	}
}

// TestLedgerCheckBuilderDraftHoldsTheRoundsShapeRules: a builder's probe or
// skip is held to the rules a round's draft is, and takes no finding.
func TestLedgerCheckBuilderDraftHoldsTheRoundsShapeRules(t *testing.T) {
	t.Parallel()
	ok := ResultProbe{Seam: "s", File: "./a.go", Line: 1, Mutation: "m", Command: "c", Outcome: ProbeCaught}
	got, err := CheckBuilderDraft(Draft{Probe: &ok})
	if err != nil || got.Probe.File != "a.go" {
		t.Errorf("a well-formed probe: %+v, %v; want it with its file normalized", got.Probe, err)
	}
	bad := ok
	bad.Outcome = "killed"
	for name, dr := range map[string]Draft{
		"an unknown outcome": {Probe: &bad},
		"a skip with no why": {Skip: &ResultSkip{File: "a.go"}},
		"a finding":          {Finding: &ResultFinding{File: "a.go"}},
		"nothing":            {},
	} {
		if _, err := CheckBuilderDraft(dr); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}
