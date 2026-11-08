package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// gitIn runs git in dir and fails the test on an error, returning its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// landOnTarget lands a commit of a file on the target (the remote's main) and
// returns to the ticket's branch in the operator's clone.
func landOnTarget(t *testing.T, fx *fixture.Fixture, name string) {
	t.Helper()
	gitIn(t, fx.RepoDir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(fx.RepoDir, name), []byte("from the target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, fx.RepoDir, "add", "-A")
	gitIn(t, fx.RepoDir, "commit", "-m", "the target moves: "+name)
	gitIn(t, fx.RepoDir, "push", "origin", "main")
	gitIn(t, fx.RepoDir, "checkout", fx.Branch)
}

// TestGateCarriesACleanMergeOfTheTargetAndPublishTakesTheHead is the one chain
// (GATE-16): a reviewed branch merges a moved main cleanly, `jig gate` records
// a carried-over round with no reviewer and no round left open, `jig status`
// says so, and `jig publish` takes the new head. A second case, a merge with a
// hand-resolved hunk, opens a delta round over that hunk alone.
func TestGateCarriesACleanMergeOfTheTargetAndPublishTakesTheHead(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)
	reviewed := gitIn(t, fx.RepoDir, "rev-parse", "HEAD")

	landOnTarget(t, fx, "moved.txt")
	gitIn(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "main")
	merged := gitIn(t, fx.RepoDir, "rev-parse", "HEAD")

	out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("gate after a clean merge: exit = %d, want 0 and a clean verdict\n%s", code, out)
	}
	if strings.Contains(out, "jig review done") {
		t.Errorf("gate asked for a reviewer for a clean merge of the target:\n%s", out)
	}
	if out, code = runMain(t, e, "", "review", "done", fx.Ticket, "--store", fx.StoreDir); code == 0 {
		t.Errorf("review done found a round open after a carried one:\n%s", out)
	}
	status, code := runMain(t, e, "", "status", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(status, "clean (carried over from round 1)") {
		t.Fatalf("status exit = %d, want round 2 clean (carried over from round 1)\n%s", code, status)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	carried := 0
	for _, l := range lines {
		if l.Event == journal.EventGateClean && l.Carried == 1 && l.Attempt == 2 {
			carried++
		}
	}
	if carried != 1 {
		t.Errorf("%d carried gate-clean line(s) for round 2, want 1", carried)
	}

	out, code = runMain(t, e, "", "publish", fx.Ticket, "--yes", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("publish of a carried round: exit = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "PUBLISH_UNREVIEWED_HEAD") {
		t.Errorf("publish refused the merged head %s (reviewed %s):\n%s", merged, reviewed, out)
	}
	if got := gitIn(t, fx.RepoRemote, "rev-parse", "refs/heads/"+fx.Branch); got != merged {
		t.Errorf("origin's %s = %s, want the merged head %s", fx.Branch, got, merged)
	}
}

// TestGateOpensADeltaRoundForAHandResolvedMerge: the merge with a hand edit is
// not carried over; the round opens and a reviewer is asked to read the hand
// edit alone, none of what the target brought in.
func TestGateOpensADeltaRoundForAHandResolvedMerge(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)

	landOnTarget(t, fx, "moved.txt")
	gitIn(t, fx.RepoDir, "merge", "--no-ff", "--no-commit", "main")
	if err := os.WriteFile(filepath.Join(fx.RepoDir, "hand.txt"), []byte("a hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, fx.RepoDir, "add", "-A")
	gitIn(t, fx.RepoDir, "commit", "-m", "merge main, by hand")

	out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || strings.Contains(out, "verdict: clean") {
		t.Fatalf("gate after a hand-resolved merge: exit = %d, want 0 and an open round\n%s", code, out)
	}
	ctx, code := runMain(t, e, "", "review", "context", fx.Ticket, "--json", "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(ctx, "hand.txt") || strings.Contains(ctx, "moved.txt\"") {
		t.Errorf("review context: exit = %d, want a request for hand.txt alone\n%s", code, ctx)
	}
}
