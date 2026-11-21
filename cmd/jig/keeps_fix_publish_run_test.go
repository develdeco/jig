package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestKeepsPublishPrintsTheFinalRunOfTheReviewedHead: `jig publish` of a
// reviewed ticket whose final run was not made at the reviewed head says so in
// its final_run table, with the line to run, and still pushes.
func TestKeepsPublishPrintsTheFinalRunOfTheReviewedHead(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)
	head := gitIn(t, fx.RepoDir, "rev-parse", "HEAD")

	out, code := runMain(t, e, "", "publish", fx.Ticket, "--yes", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("jig publish: exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{
		"final_run[1]{repo,outcome,seconds,command,run}:",
		"none at the reviewed head " + head[:12] + ",,,jig verify " + fx.Ticket + " --final",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("jig publish lacks %q:\n%s", want, out)
		}
	}
}
