package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestAnUncoveredFailureIsNoEnforcedNote: a failed run that covers no commit (a
// dirty tree, another command) is no verification of the commit it names, so
// it is no "verification FAILED" note and holds no unattended Stop.
func TestAnUncoveredFailureIsNoEnforcedNote(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("b")
	r.recordStart()
	sha := r.commit("b", "b.txt")
	r.add(journal.Line{Event: journal.EventOracle, Slice: "b", Commit: sha, Attempt: 1, Outcome: "fail", Summary: "FAIL beta TestX", Uncovered: journal.UncoveredDirtyBefore})

	res := r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "ls" }))
	if strings.Contains(res.Context, "FAILED") {
		t.Errorf("tool context = %q, want no failure told for a run that covers nothing", res.Context)
	}
	stop := r.handle(r.ev(KindStop))
	if strings.Contains(stop.Block, "FAILED") || strings.Contains(stop.Message, "failed") {
		t.Errorf("Stop = %+v, want no failure named for a run that covers nothing", stop)
	}
}

// TestACommitWithATrailerAndNoRecordedResultHoldsAnUnattendedStop: work begun
// by hand has no claim and so no recorded start sha; its commits are those
// past origin's target, and one that carries a slice trailer and has no
// recorded result still holds an unattended session at Stop.
func TestACommitWithATrailerAndNoRecordedResultHoldsAnUnattendedStop(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	sha := r.commit("a", "a.txt") // no claim, no start sha recorded
	marker := "jig: \"1\"\nproject: " + r.projectName(t) + "\nunattended: true\n"
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "jig.yaml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	res := r.handle(r.ev(KindStop))
	if !strings.Contains(res.Block, "commit "+sha[:12]+" for slice a is not verified") {
		t.Errorf("Block = %q, want the commit with no recorded result held", res.Block)
	}
}

// TestStopHoldsOnlyOnASlicesLatestCommit: a slice committed twice before its
// run (a test commit, then the code) has one commit that can be cleared by
// `jig verify`, which runs at HEAD. Only that commit is held to a result; the
// older one is history.
func TestStopHoldsOnlyOnASlicesLatestCommit(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()
	first := r.commit("a", "a1.txt")
	second := r.commit("a", "a2.txt")

	res := r.handle(r.ev(KindStop))
	if !strings.Contains(res.Block, "commit "+second[:12]+" for slice a is not verified") {
		t.Fatalf("Block = %q, want the latest commit held to a result", res.Block)
	}
	if strings.Contains(res.Block, first[:12]) {
		t.Errorf("Block = %q, want the older commit of the slice left out", res.Block)
	}
}

// TestStopHoldsOnlyOnASlicesLatestCommitUnlessVerifiedAtADescendant: slice a
// committed, then slice b, then both verified at HEAD (the plain order of a
// multi-slice ticket): slice a's commit is history, for `jig verify` at HEAD
// records a verification of HEAD and could never clear it. Only a `verified`
// line of that same slice, at a descendant, makes a commit history.
func TestStopHoldsOnlyOnASlicesLatestCommitUnlessVerifiedAtADescendant(t *testing.T) {
	t.Parallel()
	pass := func(r *rig, slice, commit string) {
		r.add(journal.Line{Event: journal.EventOracle, Slice: slice, Commit: commit, Attempt: 1, Outcome: "pass"})
		r.add(journal.Line{Event: journal.EventVerified, Slice: slice, Commit: commit, Attempt: 1})
	}
	for _, c := range []struct {
		name string
		// build makes the commits and lines, and returns the commit of slice a
		// whose hold is in question.
		build    func(r *rig) string
		wantHeld bool
	}{
		{"both slices verified at HEAD hold nothing for slice a", func(r *rig) string {
			a1 := r.commit("a", "a1.txt")
			b1 := r.commit("b", "b1.txt")
			pass(r, "a", b1)
			pass(r, "b", b1)
			return a1
		}, false},
		{"only slice b verified at HEAD: slice a's commit is still held", func(r *rig) string {
			a1 := r.commit("a", "a1.txt")
			b1 := r.commit("b", "b1.txt")
			pass(r, "b", b1)
			return a1
		}, true},
		{"only an uncovered pass of slice a at HEAD: still held", func(r *rig) string {
			a1 := r.commit("a", "a1.txt")
			b1 := r.commit("b", "b1.txt")
			pass(r, "b", b1)
			r.add(journal.Line{Event: journal.EventOracle, Slice: "a", Commit: b1, Attempt: 1, Outcome: "pass", Uncovered: journal.UncoveredDirtyBefore})
			return a1
		}, true},
		{"slice a verified at a1, then a2 committed: a2 is held", func(r *rig) string {
			a1 := r.commit("a", "a1.txt")
			pass(r, "a", a1)
			return r.commit("a", "a2.txt")
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.onTicketBranch()
			r.handle(r.ev(KindSessionStart))
			r.unattendedClaim("a")
			r.recordStart()
			commit := c.build(r)

			res := r.handle(r.ev(KindStop))
			held := strings.Contains(res.Block, "commit "+commit[:12]+" for slice a is not verified")
			if held != c.wantHeld {
				t.Errorf("Stop = %+v, commit %s held = %v, want %v", res, commit[:12], held, c.wantHeld)
			}
		})
	}
}
