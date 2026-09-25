package revieweval

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/session"
)

// repoMutatingJudge confirms every candidate Undecided (survives any
// non-line0 structural edge, which is all the tests below need) but first
// calls mutate against the round's own case repo, if set - a stand-in for
// a judge whose dispatch left something behind there.
type repoMutatingJudge struct {
	mutate func(repoDir string) error
}

func (j repoMutatingJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	if j.mutate != nil {
		if err := j.mutate(q.RepoDir); err != nil {
			return nil, err
		}
	}
	return make([]Verdict, len(q.Candidates)), nil
}

// --- the judge must not change the case repo --------------------------------

func TestRunCaseFailsRoundWhenJudgeEditsATrackedFile(t *testing.T) {
	c := loadEvalCase(t, "nil-deref")
	backend := scriptedReviewerBackend{dir: resultsDir("perfect")}
	judge := repoMutatingJudge{mutate: func(repoDir string) error {
		return os.WriteFile(filepath.Join(repoDir, ".claude", "jig.yaml"), []byte("mutated\n"), 0o644)
	}}

	cs, err := RunCase(t.TempDir(), c, backend, judge, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if len(cs.Rounds) != 1 {
		t.Fatalf("RunCase: %d rounds, want 1", len(cs.Rounds))
	}
	rs := cs.Rounds[0]
	if !rs.Failed {
		t.Error("Failed = false, want true: the judge changed a tracked file")
	}
	if rs.Reason != "the judge changed the case repo" {
		t.Errorf("Reason = %q, want \"the judge changed the case repo\"", rs.Reason)
	}
	if rs.Passed {
		t.Error("Passed = true, want false")
	}
}

// TestRunCaseJudgeUntrackedFileDoesNotFailTheRoundOrBreakTheNext covers both
// halves of the judge read-only requirement in one case: an untracked file
// the judge leaves behind is not a repo change (round 1 must not fail over
// it), and it must not still be sitting in the repo by round 2's own patch
// apply - which a left-behind file could break (round 2 would commit it
// too, widening the diff scope beyond what round 2's scripted
// reviewed_paths covers, refusing the round).
func TestRunCaseJudgeUntrackedFileDoesNotFailTheRoundOrBreakTheNext(t *testing.T) {
	c := loadEvalCase(t, "forgotten-finding")
	backend := scriptedReviewerBackend{dir: resultsDir("perfect")}
	judge := repoMutatingJudge{mutate: func(repoDir string) error {
		return os.WriteFile(filepath.Join(repoDir, "judge-scratch.txt"), []byte("leftover\n"), 0o644)
	}}

	cs, err := RunCase(t.TempDir(), c, backend, judge, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if len(cs.Rounds) != 2 {
		t.Fatalf("RunCase: %d rounds, want 2", len(cs.Rounds))
	}
	if cs.Rounds[0].Failed {
		t.Error("round 1 Failed = true, want false: an untracked file is not a repo change")
	}
	if cs.Rounds[1].Refused || cs.Rounds[1].Failed {
		t.Errorf("round 2 Refused=%v Failed=%v, want both false: the judge's untracked leftover must have been cleaned before round 2's own patch applied", cs.Rounds[1].Refused, cs.Rounds[1].Failed)
	}
}

// --- a round's live output leaves the store after it is scored -------------

// workDirLeakBackend copies the fixture result the same way
// scriptedReviewerBackend does, but first checks whether any earlier
// round's result file is still readable in the work dir the store hands
// this dispatch (d.ResultJSON's own directory): if RunCase retires a
// round's work dir before the next round's dispatch, as it must, that file
// is already gone by then.
type workDirLeakBackend struct {
	dir    string
	leaked *bool
}

func (b workDirLeakBackend) Run(d session.Dispatch) error {
	workDir := filepath.Dir(d.ResultJSON)
	for k := 1; k < d.Attempt; k++ {
		earlier := filepath.Join(workDir, fmt.Sprintf("gate.round-%d.result.json", k))
		if _, err := os.Stat(earlier); err == nil {
			*b.leaked = true
		}
	}
	path := filepath.Join(b.dir, d.Ticket, fmt.Sprintf("round-%d.json", d.Attempt))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("revieweval: workDirLeakBackend: no scripted result at %s: %w", path, err)
	}
	return os.WriteFile(d.ResultJSON, data, 0o644)
}

func TestRunCaseRetiresRoundWorkBeforeTheNextRoundsDispatch(t *testing.T) {
	c := loadEvalCase(t, "forgotten-finding")
	var leaked bool
	backend := workDirLeakBackend{dir: resultsDir("perfect"), leaked: &leaked}

	cs, err := RunCase(t.TempDir(), c, backend, nil, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if len(cs.Rounds) != 2 {
		t.Fatalf("RunCase: %d rounds, want 2", len(cs.Rounds))
	}
	if leaked {
		t.Error("an earlier round's result file was still readable under the ticket dir at a later round's dispatch time")
	}
}
