package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestReviewContextShowsTheSlicesPasses: the round's view lists each slice's
// witnessed pass with the workspace, the command, the commit it was witnessed
// at, whether that commit has the head's tree, and its seconds, and nothing
// of the gate's own runs or of a reused pass.
func TestReviewContextShowsTheSlicesPasses(t *testing.T) {
	t.Parallel()
	p := verifydeliver.Prepared{
		Ticket: "T-1", Round: 2, Branch: "T-1-b", Copy: "the clone's", Head: "bbbb", Base: "aaaa", Scope: "full",
		Request: verifydeliver.ReviewRequest{
			MustReview: []string{"a/a.go"},
			OraclesPassed: []verifydeliver.SlicePass{
				{Slice: "s1", Workspace: "a", Command: "go test ./a/...", Commit: "1111111111111111111111111111111111111111", AtHead: "no", Seconds: 4},
				{Slice: "s2", Workspace: "b", Command: "go test ./b/...", Commit: "2222222222222222222222222222222222222222", AtHead: "yes", Seconds: 9},
			},
		},
	}
	// Only the full view lists the passes: a delta round's lists them again at
	// its new head, and the short view of a round lists none.
	for _, full := range []bool{true} {
		out := strings.Join(roundBlocks(p, nil, full, nil), "\n")
		want := "oracles_passed[2]{slice,workspace,command,commit,at_head,seconds}:\n" +
			"  s1,a,go test ./a/...,111111111111,no,4\n" +
			"  s2,b,go test ./b/...,222222222222,yes,9"
		if !strings.Contains(out, want) {
			t.Errorf("the round's view (full %v) lacks %q\n%s", full, want, out)
		}
		for _, gone := range []string{"reused_from", "ran}", "{oracle,"} {
			if strings.Contains(out, gone) {
				t.Errorf("the round's view (full %v) still holds %q\n%s", full, gone, out)
			}
		}
	}
}

// TestGateHelpSaysNothingRuns: `jig gate -h` says a round runs no command and
// no longer warns of oracles the gate runs that end with it.
func TestGateHelpSaysNothingRuns(t *testing.T) {
	t.Parallel()
	out, code := runMain(t, testEnv(t.TempDir()), "", "gate", "-h")
	if code != 0 {
		t.Fatalf("jig gate -h: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "runs no command") {
		t.Errorf("jig gate -h does not say a round runs no command:\n%s", out)
	}
	for _, gone := range []string{"SIGKILL", "oracles a gate runs", "re-verify"} {
		if strings.Contains(out, gone) {
			t.Errorf("jig gate -h still holds %q:\n%s", gone, out)
		}
	}
}
