package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/ready"
)

// TestNextNotesAnAmbiguousWorkspace: jig next tells a slice whose workspace
// two repos declare to name its repo, groups it under ?, and shares out only
// the repos that own their slices. The ambiguity itself is decided by
// internal/ready (TestPlace); this is the wording a session reads.
func TestNextNotesAnAmbiguousWorkspace(t *testing.T) {
	t.Parallel()
	n := nextTicket{
		Ticket: "JIG-1", State: "building", Branch: "jig/JIG-1",
		Ready: []nextBatch{
			{Repo: "api", Slices: []string{"server"}},
			{Repo: "web", Slices: []string{"ui"}},
			{Repo: ready.Unplaced, Slices: []string{"helper"}},
		},
		Ambiguous: []string{"lib"},
	}
	out := strings.Join(n.blocks(false), "\n")
	if want := "note: more than one repo declares workspace(s) lib: name the repo in the slice, as <repo>/<workspace>; their slices are grouped under ?"; !strings.Contains(out, want) {
		t.Errorf("jig next lacks %q:\n%s", want, out)
	}
	advice := strings.Join(distributionAdvice(n.Ready), "\n")
	if want := "2 repos have ready slices (api, web)"; !strings.Contains(advice, want) {
		t.Errorf("jig next's advice lacks %q:\n%s", want, advice)
	}
}
