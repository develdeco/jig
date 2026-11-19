package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestKeepsGateNotesAStoreReferenceWhenItOpensARound: a branch that adds a
// comment citing an id of the store gets the store_references table, with the
// file and line, when `jig gate` opens its round.
func TestKeepsGateNotesAStoreReferenceWhenItOpensARound(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	buildFixtureTicket(t, fx)
	if err := os.WriteFile(filepath.Join(fx.RepoDir, "notes.txt"), []byte("plain line\n// see "+fx.Ticket+" for why\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, fx.RepoDir, "add", "-A")
	gitIn(t, fx.RepoDir, "commit", "-m", "explain the total")

	out, code := runMain(t, cloneEnv(fx), "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("jig gate: exit %d\n%s", code, out)
	}
	for _, want := range []string{"store_references[1]", "notes.txt:2", fx.Ticket} {
		if !strings.Contains(out, want) {
			t.Errorf("jig gate lacks %q:\n%s", want, out)
		}
	}
}
