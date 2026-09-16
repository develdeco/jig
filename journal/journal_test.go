package journal

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/develdeco/jig/store"
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

func TestBuilderModels(t *testing.T) {
	lines := []Line{
		{Event: "dispatch", Model: "claude-sonnet-5"},
		{Event: "result", Outcome: "green"},
		{Event: "dispatch", Model: "claude-haiku-4-5"},
		{Event: "dispatch", Model: "claude-sonnet-5"},
		{Event: "dispatch", Model: ""},
	}
	got := BuilderModels(lines)
	want := []string{"claude-sonnet-5", "claude-haiku-4-5"}
	if len(got) != len(want) {
		t.Fatalf("BuilderModels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BuilderModels = %v, want %v", got, want)
		}
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
