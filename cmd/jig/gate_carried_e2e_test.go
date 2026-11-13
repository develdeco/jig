package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// gitIn runs git in dir and fails the test on an error, returning its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.RunEnv(dir, []string{
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
	}, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
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
// of the carried round: a reviewed branch merges a moved main cleanly, `jig gate` records
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
	if !strings.Contains(out, "verdict: clean (carried over from round 1)") || strings.Contains(out, "probes:") || !strings.Contains(out, "none ran") {
		t.Errorf("gate does not print the round as carried over, with no reviewer and no probe counts:\n%s", out)
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
// not carried over. A publish refuses the merged head as unreviewed and pushes
// nothing; the gate opens a delta round at the merge whose base is a tree (git's
// own merge of the merge's parents) and whose reviewer is asked to read the
// hand-edited file alone, none of what the target brought in; once that round
// is clean the publish pushes the merge commit.
func TestGateOpensADeltaRoundForAHandResolvedMerge(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)
	flags := []string{"--store", fx.StoreDir}

	landOnTarget(t, fx, "moved.txt")
	landOnTarget(t, fx, "brought.txt")
	gitIn(t, fx.RepoDir, "merge", "--no-ff", "--no-commit", "main")
	if err := os.WriteFile(filepath.Join(fx.RepoDir, "moved.txt"), []byte("from the target\nand a hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, fx.RepoDir, "add", "-A")
	gitIn(t, fx.RepoDir, "commit", "-m", "merge main, by hand")
	merge := gitIn(t, fx.RepoDir, "rev-parse", "HEAD")

	originBranch := func() string {
		out, err := gitx.Run(fx.RepoRemote, "rev-parse", "--verify", "-q", "refs/heads/"+fx.Branch)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	before := originBranch()
	out, code := runMain(t, e, "", append([]string{"publish", fx.Ticket, "--yes"}, flags...)...)
	if code == 0 || !strings.Contains(out, "PUBLISH_UNREVIEWED_HEAD") {
		t.Fatalf("publish of a hand-resolved merge: exit = %d, want PUBLISH_UNREVIEWED_HEAD\n%s", code, out)
	}
	if got := originBranch(); got != before {
		t.Fatalf("the refused publish moved origin's %s from %q to %q", fx.Branch, before, got)
	}

	out, code = runMain(t, e, "", append([]string{"gate", fx.Ticket}, flags...)...)
	if code != 0 || strings.Contains(out, "verdict: clean") {
		t.Fatalf("gate after a hand-resolved merge: exit = %d, want 0 and an open round\n%s", code, out)
	}
	ctx, code := runMain(t, e, "", append([]string{"review", "context", fx.Ticket, "--json"}, flags...)...)
	if code != 0 {
		t.Fatalf("review context: exit = %d\n%s", code, ctx)
	}
	var req struct {
		Scope      string   `json:"scope"`
		HeadSHA    string   `json:"head_sha"`
		BaseSHA    string   `json:"base_sha"`
		MustReview []string `json:"must_review"`
	}
	if err := json.Unmarshal([]byte(ctx), &req); err != nil {
		t.Fatalf("review context --json is not the request: %v\n%s", err, ctx)
	}
	if req.Scope != "delta" || req.HeadSHA != merge {
		t.Errorf("round = scope %q head %s, want a delta at the merge %s", req.Scope, req.HeadSHA, merge)
	}
	if kind := gitIn(t, fx.RepoDir, "cat-file", "-t", req.BaseSHA); kind != "tree" {
		t.Errorf("base_sha %s is a %s, want a tree: git's own merge of the merge's parents", req.BaseSHA, kind)
	}
	if want := []string{"moved.txt"}; !reflect.DeepEqual(req.MustReview, want) {
		t.Errorf("must_review = %v, want the hand-edited file alone, %v", req.MustReview, want)
	}

	result, err := json.Marshal(map[string]any{
		"findings": []any{}, "still_present": []any{}, "reviewed_paths": req.MustReview, "summary": "nothing found",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clean.json")
	if err := os.WriteFile(path, result, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code = runMain(t, e, "", append([]string{"review", "done", fx.Ticket, "--result", path}, flags...)...); code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("review done: exit = %d, want a clean verdict\n%s", code, out)
	}
	if out, code = runMain(t, e, "", append([]string{"publish", fx.Ticket, "--yes"}, flags...)...); code != 0 {
		t.Fatalf("publish after the clean round: exit = %d\n%s", code, out)
	}
	if got := originBranch(); got != merge {
		t.Errorf("origin's %s = %s, want the merge commit %s", fx.Branch, got, merge)
	}
}
