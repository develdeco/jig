package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTestBrief writes ticket T-1's brief.md and commits it, so the store's
// history holds this revision.
func writeTestBrief(t *testing.T, st *Store, work, body string) {
	t.Helper()
	dir := st.TicketDir("T-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "brief")
}

// TestRestampSlices: one locked write replaces from_brief of the slices named
// and nothing else; a slice that is not there refuses the whole call and
// leaves the file byte for byte.
func TestRestampSlices(t *testing.T) {
	st, _ := newTestStandaloneStore(t)
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []Slice{
		{ID: "a", Workspace: "root", Goal: "g", Oracle: "o", Env: "db", BlockedBy: []string{}, FromBrief: []string{"h1"}},
		{ID: "b", Workspace: "root", Goal: "g2", Oracle: "o2", BlockedBy: []string{"a"}, FromBrief: []string{"h2"}, FromGate: 1, Findings: []string{"f1"}},
	}
	if err := st.AppendSlices("T-1", rows); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(st.slicesFile("T-1"))
	if err != nil {
		t.Fatal(err)
	}

	err = st.RestampSlices("T-1", map[string][]string{"a": {"n1"}, "missing": {"x"}})
	if err == nil {
		t.Fatal("RestampSlices with an unknown slice succeeded")
	}
	after, _ := os.ReadFile(st.slicesFile("T-1"))
	if string(after) != string(before) {
		t.Fatalf("a refused restamp changed slices.yaml:\n%s", after)
	}

	if err := st.RestampSlices("T-1", map[string][]string{"a": {"n1", "n2"}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ReadSlices("T-1")
	if err != nil {
		t.Fatal(err)
	}
	want := append([]Slice(nil), rows...)
	want[0].FromBrief = []string{"n1", "n2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slices = %+v, want %+v", got, want)
	}
}

// TestBriefHeadings: a hash is resolved to the heading it was the hash of in
// the newest committed revision that holds it, through a rename and a removed
// section; a hash no revision holds is absent.
func TestBriefHeadings(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	hash := func(brief, heading string) string { return BriefSectionHashes([]byte(brief))[heading] }

	v1 := "# t\n\n## Problem\n\nold problem\n\n## Seams\n\nseams\n"
	v2 := "# t\n\n## Context\n\nold problem\n\n## Seams\n\nseams edited\n"
	writeTestBrief(t, st, work, v1)
	writeTestBrief(t, st, work, v2)

	uncommitted := "# t\n\n## Seams\n\nnever committed\n"
	got := st.BriefHeadings("T-1", []string{
		hash(v1, "Problem"), hash(v1, "Seams"), hash(v2, "Seams"), hash(uncommitted, "Seams"),
	})
	want := map[string]string{
		hash(v1, "Problem"): "Problem",
		hash(v1, "Seams"):   "Seams",
		hash(v2, "Seams"):   "Seams",
	}
	// The renamed section's body is the same text under a new heading: the
	// newest revision holding it names it.
	want[hash(v1, "Problem")] = "Context"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BriefHeadings = %v, want %v", got, want)
	}
}

// TestBriefHeadingsNoHistory: a store with no committed brief resolves nothing.
func TestBriefHeadingsNoHistory(t *testing.T) {
	st, _ := newTestStandaloneStore(t)
	if got := st.BriefHeadings("T-1", []string{strings.Repeat("a", 64)}); len(got) != 0 {
		t.Fatalf("BriefHeadings = %v, want none", got)
	}
}
