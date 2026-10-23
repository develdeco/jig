package slicectx

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestBuildCarriesTheSliceAndItsBriefSections: the slice, the sections its
// from_brief names with their text, the Seams section the slice did not list,
// the count of names the brief no longer holds, and the paths of the brief.
func TestBuildCarriesTheSliceAndItsBriefSections(t *testing.T) {
	w := newWorld(t)
	c, err := Build(w.input("b"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Slice.ID != "b" || c.Slice.Goal != "Add a caller." || c.Slice.State != "queued" || c.Slice.Env != "rig" {
		t.Errorf("slice = %+v", c.Slice)
	}
	if got := c.Slice.BlockedBy; len(got) != 1 || got[0] != "a" {
		t.Errorf("blocked_by = %v, want [a]", got)
	}
	if len(c.BriefSections) != 1 || c.BriefSections[0].Heading != "Slice B - callers" ||
		c.BriefSections[0].Text != "\nAdd a caller.\n\nWith detail." {
		t.Errorf("brief sections = %+v, want only Slice B with its text", c.BriefSections)
	}
	if c.StaleBriefSections != 1 {
		t.Errorf("stale = %d, want 1 (the unknown hash)", c.StaleBriefSections)
	}
	if c.Seams == nil || c.Seams.Heading != "Seams" || !strings.Contains(c.Seams.Text, "the verb") {
		t.Errorf("seams = %+v, want the Seams section", c.Seams)
	}
	if !strings.HasSuffix(c.Brief, "brief.md") || c.Intent != "" {
		t.Errorf("brief = %q, intent = %q, want the brief path and no intent", c.Brief, c.Intent)
	}
}

// TestSeamsListedBySliceAreNotRepeated: a slice that lists Seams in its
// from_brief has it among its sections and no separate seams part.
func TestSeamsListedBySliceAreNotRepeated(t *testing.T) {
	w := newWorld(t)
	slices, err := w.st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range store.BriefSections([]byte(testBrief)) {
		if sec.Heading == "Seams" {
			slices[0].FromBrief = append(slices[0].FromBrief, sec.Hash)
		}
	}
	if err := removeSlices(w); err != nil {
		t.Fatal(err)
	}
	if err := w.st.AppendSlices("JIG-1", slices); err != nil {
		t.Fatal(err)
	}
	c, err := Build(w.input("a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.BriefSections) != 2 || c.Seams != nil {
		t.Errorf("sections %d, seams %+v, want 2 sections and no separate seams", len(c.BriefSections), c.Seams)
	}
}

// TestBuildResolvesTheOracleAndItsLastTime: the oracle of the slice is
// resolved through the manifest of the clone, the last run of that exact
// command with that env gives the seconds, and the attempt is one past the
// highest the oracle lines record.
func TestBuildResolvesTheOracleAndItsLastTime(t *testing.T) {
	w := newWorld(t)
	cmd := "go test ./alpha/..."
	w.line(journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "pass", Command: cmd, Env: "rig", Seconds: 41, Attempt: 1})
	w.line(journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "pass", Command: cmd, Env: "", Seconds: 7, Attempt: 2})
	w.line(journal.Line{Slice: "b", Event: journal.EventOracle, Outcome: "fail", Command: cmd, Env: "rig", Seconds: 19, Attempt: 1, Summary: "alpha_test.go:12: want 3, got 2", Commit: "0123456789abcdef0123"})

	c, err := Build(w.input("b"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Slice.OracleCommand != cmd {
		t.Errorf("oracle command = %q, want %q", c.Slice.OracleCommand, cmd)
	}
	if c.OracleSeconds != 19 {
		t.Errorf("oracle seconds = %d, want 19 (the latest run with env rig)", c.OracleSeconds)
	}
	if c.Slice.Attempt != 2 {
		t.Errorf("attempt = %d, want 2 (one past the failed attempt 1 of b)", c.Slice.Attempt)
	}
	if len(c.FailedAttempts) != 1 || c.FailedAttempts[0].Summary != "alpha_test.go:12: want 3, got 2" || c.FailedAttempts[0].Attempt != 1 {
		t.Errorf("failed attempts = %+v, want the one with its summary", c.FailedAttempts)
	}

	// No clone: the command cannot be resolved, so nothing is claimed of it.
	in := w.input("b")
	in.RepoDir = ""
	c, err = Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if c.Slice.OracleCommand != "" || c.OracleSeconds != 0 {
		t.Errorf("without a clone: command %q, seconds %d, want neither", c.Slice.OracleCommand, c.OracleSeconds)
	}
}
