package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// The tests that go through a gate or a publish need a ticket that was built.
// jig builds nothing any more: an agent commits in the operator's clone and
// jig verifies each commit. These helpers do what such an agent does with the
// fixture's scenario patches - commit them on the ticket's branch in the clone,
// each with the slice's trailer, and have `jig verify --wait` run the oracle
// and mark the slice green - so a test starts from the state a built ticket
// is in, with the work in the operator's clone and not pushed anywhere.

// ticketBranchOf is the branch a fixture ticket (which has no title, so its
// brief's heading gives the slug) is built on.
func ticketBranchOf(fx *fixture.Fixture) string { return fx.Branch }

// cloneEnv is the env of a command run from the operator's clone.
func cloneEnv(fx *fixture.Fixture) env { return testEnv(fx.Home).inDir(fx.RepoDir) }

// commitScenarioSlice commits the patch of slice's attempt in the fixture's
// scenario on the clone's current branch, with the slice's trailer, and
// returns the commit. The slice may be a gate fix slice the scenario builds.
func commitScenarioSlice(t *testing.T, fx *fixture.Fixture, slice string, attempt int) string {
	t.Helper()
	patch := filepath.Join(fx.ScenarioDir, "slices", slice, "attempt-"+strconv.Itoa(attempt), "patch.diff")
	gitMust(t, fx.RepoDir, "apply", patch)
	gitMust(t, fx.RepoDir, "add", "-A")
	gitMust(t, fx.RepoDir, "commit", "-m", fx.Ticket+" "+slice+": scenario patch", "-m", "Jig-Slice: "+slice)
	return gitMust(t, fx.RepoDir, "rev-parse", "HEAD")
}

// buildScenarioSlice commits slice's patch (commitScenarioSlice) and verifies
// the commit in this process, so the slice is green and the gate can reuse the
// pass.
func buildScenarioSlice(t *testing.T, fx *fixture.Fixture, slice string, attempt int) string {
	t.Helper()
	sha := commitScenarioSlice(t, fx, slice, attempt)
	out, code := runMain(t, cloneEnv(fx), "", "verify", fx.Ticket, sha, "--slice", slice, "--wait", "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "state: pass") {
		t.Fatalf("jig verify %s %s: exit = %d, want 0 and a pass\n%s", slice, sha, code, out)
	}
	return sha
}

// buildFixtureTicket builds the fixture ticket the way an agent does: on the
// ticket's branch in the operator's clone it commits slice a, slice b (whose
// first attempt fails its test and is not verified, the second is the
// slice's), slice c and slice d, each verified. Without a built ticket a gate
// call past the CLI's own flag checks fails on the missing branch, rather than
// reaching the rule a test means to exercise. It returns the last commit.
func buildFixtureTicket(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	gitMust(t, fx.RepoDir, "checkout", "-b", ticketBranchOf(fx))
	buildScenarioSlice(t, fx, "a", 1)
	commitScenarioSlice(t, fx, "b", 1)
	buildScenarioSlice(t, fx, "b", 2)
	buildScenarioSlice(t, fx, "c", 2)
	return buildScenarioSlice(t, fx, "d", 1)
}
