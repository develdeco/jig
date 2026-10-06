package journal

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAppendReadRoundTrip(t *testing.T) {
	st := newTestStore(t)

	lines := []Line{
		{Ticket: "JIG-1", Slice: "a", Event: "dispatch", Model: "claude-sonnet-5", Attempt: 1},
		{Ticket: "JIG-1", Slice: "a", Event: "result", Outcome: "green", Commit: "abc1234def"},
	}
	for _, l := range lines {
		if err := Append(st, "JIG-1", l); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := Read(st, "JIG-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(lines) {
		t.Fatalf("Read returned %d lines, want %d", len(got), len(lines))
	}
	for i, l := range got {
		if l.TS == "" {
			t.Fatalf("line %d: TS not filled", i)
		}
		if l.Ticket != "JIG-1" || l.Slice != lines[i].Slice || l.Event != lines[i].Event {
			t.Fatalf("line %d = %+v, want ticket/slice/event to match %+v", i, l, lines[i])
		}
	}
}

func TestReadAbsentJournal(t *testing.T) {
	st := newTestStore(t)
	got, err := Read(st, "JIG-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != nil {
		t.Fatalf("Read on absent journal = %v, want nil", got)
	}
}

func TestAppendConcurrentNoLostLines(t *testing.T) {
	st := newTestStore(t)

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Append(st, "JIG-1", Line{Slice: "a", Event: "dispatch", Attempt: i}); err != nil {
				t.Errorf("Append: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got, err := Read(st, "JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("Read returned %d lines, want %d (concurrent appends lost lines)", len(got), n)
	}
}

func TestFailedAttempts(t *testing.T) {
	lines := []Line{
		{Slice: "a", Event: "result", Outcome: "code-bug", Attempt: 1},
		{Slice: "a", Event: "result", Outcome: "green", Attempt: 2}, // claimed green, never verified
		{Slice: "a", Event: "result", Outcome: "needs-input", Attempt: 3},
		{Slice: "a", Event: "result", Outcome: "flawed-brief", Attempt: 4},
		{Slice: "a", Event: "result", Outcome: "blocked-by-env", Attempt: 5},
		{Slice: "a", Event: "result", Outcome: "green", Attempt: 6},
		{Slice: "a", Event: "verified", Attempt: 6},
		{Slice: "b", Event: "result", Outcome: "failed", Attempt: 1},
	}
	if got := FailedAttempts(lines, "a"); got != 2 {
		t.Fatalf("FailedAttempts(a) = %d, want 2 (the code-bug and the green that did not verify)", got)
	}
	if got := FailedAttempts(lines, "c"); got != 0 {
		t.Fatalf("FailedAttempts(c) = %d, want 0 for a slice with no lines", got)
	}
}

// TestRenderChangelogListsOnlyVerifiedCommits: a builder may claim green
// more than once in an attempt (a red oracle run goes back to its session,
// ADR 0020), so a journal with verified lines lists each slice by the commit
// that verified, once, in every changelog.
func TestRenderChangelogListsOnlyVerifiedCommits(t *testing.T) {
	lines := []Line{
		{Ticket: "JIG-1", Slice: "a", Event: "result", Outcome: "green", Commit: "1111111aaa", Attempt: 1},
		{Ticket: "JIG-1", Slice: "a", Event: "oracle", Outcome: "fail", Attempt: 1},
		{Ticket: "JIG-1", Slice: "a", Event: "result", Outcome: "green", Commit: "2222222bbb", Attempt: 1},
		{Ticket: "JIG-1", Slice: "a", Event: "oracle", Outcome: "pass", Attempt: 1},
		{Ticket: "JIG-1", Slice: "a", Event: "verified", Commit: "2222222bbb", Attempt: 1},
	}
	sliceWS := map[string]string{"a": "root"}
	if got, want := RenderChangelog(lines, "root", sliceWS), "# Changelog - root\n- a: 2222222\n"; got != want {
		t.Errorf("RenderChangelog =\n%q\nwant\n%q", got, want)
	}
	if got := RenderConsolidated(lines); !strings.Contains(got, "## Slices\n- a: 2222222\n\n") {
		t.Errorf("RenderConsolidated =\n%q\nwant slice a listed once, by its verified commit", got)
	}
	if got, want := RenderDiffChangelog(lines, 1), "# Diff changelog - round 1\n- a: 2222222\n"; got != want {
		t.Errorf("RenderDiffChangelog =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderChangelogGolden(t *testing.T) {
	lines := []Line{
		{Slice: "a", Event: "dispatch", Model: "claude-sonnet-5"},
		{Slice: "a", Event: "result", Outcome: "green", Commit: "abcdef1234567890"},
		{Slice: "b", Event: "result", Outcome: "code-bug", Commit: ""},
		{Slice: "b", Event: "result", Outcome: "green", Commit: "1112223"},
		{Slice: "c", Event: "result", Outcome: "green", Commit: "beadbeef"}, // different workspace
	}
	sliceWS := map[string]string{"a": "root", "b": "root", "c": "other"}

	got := RenderChangelog(lines, "root", sliceWS)
	want := "# Changelog - root\n" +
		"- a: abcdef1\n" +
		"- b: 1112223\n"
	if got != want {
		t.Fatalf("RenderChangelog =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderChangelogNoGreens(t *testing.T) {
	got := RenderChangelog(nil, "root", nil)
	want := "# Changelog - root\n"
	if got != want {
		t.Fatalf("RenderChangelog(nil) = %q, want %q", got, want)
	}
}

func TestRenderConsolidatedGolden(t *testing.T) {
	lines := []Line{
		{Ticket: "JIG-1", Slice: "a", Event: "result", Outcome: "green", Commit: "abcdef1234"},
		{Ticket: "JIG-1", Slice: "b", Event: "result", Outcome: "green", Commit: "1112223"},
		{Ticket: "JIG-1", Event: "gate-round", Attempt: 1, Outcome: "fix-slices"},
		{Ticket: "JIG-1", Slice: "fix-1", Event: "fix-slice", Attempt: 1},
		{Ticket: "JIG-1", Slice: "fix-1", Event: "result", Outcome: "green", Commit: "cafebabe"},
	}

	got := RenderConsolidated(lines)
	want := "# JIG-1 - consolidated changelog\n" +
		"\n## Slices\n" +
		"- a: abcdef1\n" +
		"- b: 1112223\n" +
		"- fix-1: cafebab\n" +
		"\n## Fix rounds\n" +
		"- round 1: fix-slice fix-1\n"
	if got != want {
		t.Fatalf("RenderConsolidated =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderConsolidatedEmpty(t *testing.T) {
	got := RenderConsolidated(nil)
	want := "#  - consolidated changelog\n" +
		"\n## Slices\n- none\n" +
		"\n## Fix rounds\n- none\n"
	if got != want {
		t.Fatalf("RenderConsolidated(nil) =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderDiffChangelogGolden(t *testing.T) {
	lines := []Line{
		{Slice: "a", Event: "result", Outcome: "green", Commit: "1111111"}, // before round 1
		{Event: "gate-round", Attempt: 1},
		{Slice: "fix-1", Event: "fix-slice", Attempt: 1},
		{Slice: "fix-1", Event: "result", Outcome: "code-bug"},
		{Slice: "fix-1", Event: "result", Outcome: "green", Commit: "2222222"}, // between round 1 and 2
		{Event: "gate-round", Attempt: 2},
		{Slice: "fix-2", Event: "result", Outcome: "green", Commit: "3333333"}, // after round 2, excluded
	}

	got := RenderDiffChangelog(lines, 2)
	want := "# Diff changelog - round 2\n" +
		"- fix-1: 2222222\n"
	if got != want {
		t.Fatalf("RenderDiffChangelog(round 2) =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderDiffChangelogRoundOneFromStart(t *testing.T) {
	lines := []Line{
		{Slice: "a", Event: "result", Outcome: "green", Commit: "1111111"},
		{Slice: "b", Event: "result", Outcome: "green", Commit: "2222222"},
		{Event: "gate-round", Attempt: 1},
	}
	got := RenderDiffChangelog(lines, 1)
	want := "# Diff changelog - round 1\n" +
		"- a: 1111111\n" +
		"- b: 2222222\n"
	if got != want {
		t.Fatalf("RenderDiffChangelog(round 1) =\n%q\nwant\n%q", got, want)
	}
}

// TestBuiltCommits: the commits jig's builders reported that verified are the
// commit of every verified line, in order and without repeats. A result line
// is only what a builder claimed - it names a commit whether or not the
// commit verified - and lines of any other event are not builds.
func TestBuiltCommits(t *testing.T) {
	lines := []Line{
		{Event: "dispatch", Slice: "a", Commit: "not-a-verification"},
		{Event: "result", Slice: "a", Outcome: "green", Commit: "c1"},
		{Event: "verified", Slice: "a", Commit: "c1"},
		{Event: "result", Slice: "b", Outcome: "green", Commit: "claimed-only"},
		{Event: "result", Slice: "b", Outcome: "code-bug"},
		{Event: "result", Slice: "b", Outcome: "green", Commit: "c2"},
		{Event: "verified", Slice: "b", Commit: "c2"},
		{Event: "verified", Slice: "c", Commit: "c1"}, // reported again after a requeue
		{Event: "verified", Slice: "c"},
		{Event: "squash", Commit: "not-a-build"},
	}
	got := BuiltCommits(lines)
	if len(got) != 2 || got[0] != "c1" || got[1] != "c2" {
		t.Fatalf("BuiltCommits = %v, want [c1 c2]", got)
	}
	if got := BuiltCommits(nil); len(got) != 0 {
		t.Fatalf("BuiltCommits(nil) = %v, want none", got)
	}
}

// TestGreenClaims: a green result line names the commit a builder claimed,
// verified or not, in order and without repeats. A result of any other outcome
// or without a commit, a verified line and any other event are not claims.
func TestGreenClaims(t *testing.T) {
	lines := []Line{
		{Event: "dispatch", Slice: "a", Commit: "not-a-claim"},
		{Event: "result", Slice: "a", Outcome: "green", Commit: "c1"},
		{Event: "verified", Slice: "a", Commit: "c1"},
		{Event: "result", Slice: "b", Outcome: "green", Commit: "claimed-only"},
		{Event: "result", Slice: "b", Outcome: "code-bug", Commit: "not-green"},
		{Event: "result", Slice: "b", Outcome: "green"},
		{Event: "result", Slice: "c", Outcome: "green", Commit: "c1"}, // claimed again after a requeue
		{Event: "verified", Slice: "d", Commit: "only-verified"},
		{Event: "squash", Commit: "not-a-claim"},
	}
	got := GreenClaims(lines)
	if len(got) != 2 || got[0] != "c1" || got[1] != "claimed-only" {
		t.Fatalf("GreenClaims = %v, want [c1 claimed-only]", got)
	}
	if got := GreenClaims(nil); len(got) != 0 {
		t.Fatalf("GreenClaims(nil) = %v, want none", got)
	}
}

// TestLastOracleSeconds: the latest oracle line of the exact command and env
// class that recorded a wall time wins; other commands, other env classes,
// other events and lines without one are skipped, and no such line is 0.
func TestLastOracleSeconds(t *testing.T) {
	lines := []Line{
		{Slice: "a", Event: "oracle", Command: "go test ./...", Seconds: 600},
		{Slice: "b", Event: "oracle", Command: "go test ./alpha/...", Seconds: 30},
		{Slice: "b", Event: "oracle", Command: "go test ./...", Seconds: 640},
		{Slice: "c", Event: "oracle", Command: "go test ./..."},
		{Slice: "c", Event: "result", Command: "go test ./...", Seconds: 1},
		{Slice: "d", Event: "oracle", Command: "go test ./...", Env: "rig", Seconds: 900},
	}
	if got := LastOracleSeconds(lines, "go test ./...", ""); got != 640 {
		t.Errorf("LastOracleSeconds(go test ./...) = %d, want 640", got)
	}
	if got := LastOracleSeconds(lines, "go test ./...", "rig"); got != 900 {
		t.Errorf("LastOracleSeconds(go test ./..., rig) = %d, want 900", got)
	}
	if got := LastOracleSeconds(lines, "go vet ./...", ""); got != 0 {
		t.Errorf("LastOracleSeconds(never run) = %d, want 0", got)
	}
}
