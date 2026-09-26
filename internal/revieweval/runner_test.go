package revieweval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/session"
)

// --- a case's own verdict is its worst round's, never its last's -----------

// TestAccumulateCaseVerdictTakesTheWorstRoundNotTheLast pins
// accumulateCaseVerdict's own contract directly: folding a FAIL (or
// PROVISIONAL) round followed by a PASS round must leave the case's own
// Verdict at the worse of the two, in either order - "last round wins"
// would leave PASS after this exact sequence.
func TestAccumulateCaseVerdictTakesTheWorstRoundNotTheLast(t *testing.T) {
	// passedFor mirrors verdictFor's own contract (score.go): a round's
	// Passed bit is false exactly when its Verdict is FAIL, true for PASS
	// and PROVISIONAL alike - so each synthetic RoundScore below is an
	// internally consistent round, the same shape runRound ever actually
	// produces.
	passedFor := func(v RoundVerdict) bool { return v != VerdictFail }

	for _, tc := range []struct {
		name           string
		first, second  RoundVerdict
		wantVerdict    RoundVerdict
		wantCasePassed bool
	}{
		{"fail-then-pass-stays-fail", VerdictFail, VerdictPass, VerdictFail, false},
		{"provisional-then-pass-stays-provisional", VerdictProvisional, VerdictPass, VerdictProvisional, true},
		{"pass-then-fail-becomes-fail", VerdictPass, VerdictFail, VerdictFail, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := CaseScore{Verdict: VerdictPass, Passed: true}
			accumulateCaseVerdict(&cs, RoundScore{Round: 1, Verdict: tc.first, Passed: passedFor(tc.first)})
			accumulateCaseVerdict(&cs, RoundScore{Round: 2, Verdict: tc.second, Passed: passedFor(tc.second)})
			if cs.Verdict != tc.wantVerdict {
				t.Errorf("Verdict = %q, want %q", cs.Verdict, tc.wantVerdict)
			}
			if cs.Passed != tc.wantCasePassed {
				t.Errorf("Passed = %v, want %v", cs.Passed, tc.wantCasePassed)
			}
		})
	}
}

// --- verdictRank orders worst-first: FAIL > PROVISIONAL > PASS -------------

// TestVerdictRankOrdersWorstFirst pins the numeric order
// accumulateCaseVerdict and RenderReport's totals both depend on: swapping
// FAIL's and PROVISIONAL's own ranks would flip which one a mixed case or
// report line keeps.
func TestVerdictRankOrdersWorstFirst(t *testing.T) {
	if verdictRank(VerdictFail) <= verdictRank(VerdictProvisional) {
		t.Errorf("verdictRank(FAIL) = %d, want it to outrank verdictRank(PROVISIONAL) = %d", verdictRank(VerdictFail), verdictRank(VerdictProvisional))
	}
	if verdictRank(VerdictProvisional) <= verdictRank(VerdictPass) {
		t.Errorf("verdictRank(PROVISIONAL) = %d, want it to outrank verdictRank(PASS) = %d", verdictRank(VerdictProvisional), verdictRank(VerdictPass))
	}
}

// --- a failed or refused round is scored FAIL, never PASS ------------------

// TestMissedRoundScoreVerdictIsFail pins missedRoundScore's own Verdict:
// every path that never reached a scoreable result (a dispatch error, a
// judge failure, the judge changing the repo) builds its RoundScore through
// this helper, and none of them may read as a pass.
func TestMissedRoundScoreVerdictIsFail(t *testing.T) {
	rs := missedRoundScore(1, Gold{Findings: []GoldFinding{{ID: "g1"}}}, nil, "some reason")
	if rs.Verdict != VerdictFail {
		t.Errorf("Verdict = %q, want %q", rs.Verdict, VerdictFail)
	}
	if rs.Passed {
		t.Errorf("Passed = true, want false: RoundScore.Passed is never set true by missedRoundScore")
	}
}

// TestReviewerRoundFailureRefusedAndFailedBothScoreFail: a REVIEW_INVALID
// result (Refused) and a REVIEW_FAILED one (Failed) are two different
// reasons a round never produced a scoreable review, but both must land on
// the same FAIL verdict - the round line's Refused/Failed flags are for a
// person to tell them apart, not the verdict a program reads.
func TestReviewerRoundFailureRefusedAndFailedBothScoreFail(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		code                    string
		wantRefused, wantFailed bool
	}{
		{"review-invalid-is-refused", "REVIEW_INVALID", true, false},
		{"review-failed-is-failed", "REVIEW_FAILED", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := &axi.Error{Code: tc.code, Msg: "boom"}
			rs, handled := reviewerRoundFailure(1, Gold{}, nil, err)
			if !handled {
				t.Fatal("reviewerRoundFailure: want handled=true for a review-outcome error")
			}
			if rs.Verdict != VerdictFail {
				t.Errorf("Verdict = %q, want %q", rs.Verdict, VerdictFail)
			}
			if rs.Refused != tc.wantRefused || rs.Failed != tc.wantFailed {
				t.Errorf("Refused=%v Failed=%v, want Refused=%v Failed=%v", rs.Refused, rs.Failed, tc.wantRefused, tc.wantFailed)
			}
		})
	}
}

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
	// A Failed round still holds the reviewer's own findings, status
	// set, no gold match and no fate - matching never ran far enough to
	// say either.
	if len(rs.Findings) != 1 {
		t.Fatalf("Findings = %+v, want the round's one reported finding", rs.Findings)
	}
	if rs.Findings[0].Status == "" {
		t.Error("Findings[0].Status is empty, want jig's own assigned status")
	}
	if rs.Findings[0].Gold != "" || rs.Findings[0].Fate != "" {
		t.Errorf("Findings[0] = %+v, want no Gold and no Fate: matching never ran far enough to say either", rs.Findings[0])
	}
}

// --- a git error inside the read-only check is infrastructure ---------

// TestCheckJudgeReadOnlyGitErrorIsReturnedNotViolated pins
// checkJudgeReadOnly's own contract directly, isolated from
// restoreCaseRepo's own separate error handling in runRound: a directory
// that is not a git repo at all makes both of its own git commands fail,
// which must come back as a plain error, never violated=true.
func TestCheckJudgeReadOnlyGitErrorIsReturnedNotViolated(t *testing.T) {
	dir := t.TempDir() // deliberately not a git repo
	violated, err := checkJudgeReadOnly(dir, "0000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("checkJudgeReadOnly: want an error, dir is not a git repo")
	}
	if violated {
		t.Error("violated = true, want false: a git command failing is infrastructure, not a violation")
	}
}

// TestRunCaseJudgeErrorFailsTheRoundAndTheCaseContinues: a judge whose Confirm itself returns an error - not one that mutates the
// repo - fails the round with that error as Reason, and the case
// still continues into round 2 rather than stopping the whole run, the
// same as any other Failed round.
func TestRunCaseJudgeErrorFailsTheRoundAndTheCaseContinues(t *testing.T) {
	c := loadEvalCase(t, "forgotten-finding")
	backend := scriptedReviewerBackend{dir: resultsDir("perfect")}
	judge := erroringJudge{round: 1, err: fmt.Errorf("judge blew up"), fallback: fixtureJudge{dir: resultsDir("perfect")}}

	cs, err := RunCase(t.TempDir(), c, backend, judge, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if len(cs.Rounds) != 2 {
		t.Fatalf("RunCase: %d rounds, want 2", len(cs.Rounds))
	}
	r1 := cs.Rounds[0]
	if !r1.Failed {
		t.Error("round 1 Failed = false, want true: the judge itself errored")
	}
	if r1.Reason == "" || !strings.Contains(r1.Reason, "judge blew up") {
		t.Errorf("round 1 Reason = %q, want it to contain the judge's own error", r1.Reason)
	}
	if len(r1.Findings) == 0 {
		t.Error("round 1 Findings is empty, want the reviewer's own findings preserved")
	}
	// The case continues: round 2 is teacher-forced from forgotten-finding's
	// own recorded round-1 history, never from round 1's own live (failed)
	// result, so it is unaffected and scores normally.
	if cs.Rounds[1].Refused || cs.Rounds[1].Failed {
		t.Errorf("round 2 Refused=%v Failed=%v, want both false: teacher-forcing means round 1's failure does not propagate", cs.Rounds[1].Refused, cs.Rounds[1].Failed)
	}
}

// erroringJudge fails Confirm, without touching the repo at all, only on
// its own round (a judge error distinct from a repo mutation) and defers
// to fallback on every other round, so
// TestRunCaseJudgeErrorFailsTheRoundAndTheCaseContinues can tell "this
// round's own judge error" apart from "every round's judge errored".
type erroringJudge struct {
	round    int
	err      error
	fallback Judge
}

func (j erroringJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	if q.Round == j.round {
		return nil, j.err
	}
	return j.fallback.Confirm(q)
}

// TestRunCaseGitErrorInReadOnlyCheckIsInfrastructureNotAJudgeViolation is
// the negative case: a judge that deletes the case repo's own .git
// directory makes checkJudgeReadOnly's git commands themselves fail,
// which is infrastructure trouble, not "the judge changed the case repo" -
// RunCase must return a real error, never a Failed CaseScore with that
// reason.
func TestRunCaseGitErrorInReadOnlyCheckIsInfrastructureNotAJudgeViolation(t *testing.T) {
	c := loadEvalCase(t, "nil-deref")
	backend := scriptedReviewerBackend{dir: resultsDir("perfect")}
	judge := repoMutatingJudge{mutate: func(repoDir string) error {
		return os.RemoveAll(filepath.Join(repoDir, ".git"))
	}}

	_, err := RunCase(t.TempDir(), c, backend, judge, "fixture-model")
	if err == nil {
		t.Fatal("RunCase: want an error, the case repo's .git is gone: a git command itself must fail")
	}
	if strings.Contains(err.Error(), "the judge changed the case repo") {
		t.Errorf("RunCase error = %q, want infrastructure trouble, not the judge-violation message", err)
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
	caseName, err := caseNameForRunID(b.dir, d.Ticket)
	if err != nil {
		return err
	}
	path := filepath.Join(b.dir, caseName, fmt.Sprintf("round-%d.json", d.Attempt))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("revieweval: workDirLeakBackend: no scripted result at %s: %w", path, err)
	}
	return os.WriteFile(d.ResultJSON, data, 0o644)
}

// recordingJudge delegates every Confirm to inner but first records
// q.WorkDir: the judge's own scratch dir is otherwise unobservable from
// outside MatchRound/runRound, since RunCase never returns it.
type recordingJudge struct {
	inner    Judge
	workDirs *[]string
}

func (j recordingJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	*j.workDirs = append(*j.workDirs, q.WorkDir)
	return j.inner.Confirm(q)
}

// TestRunCaseDeletesWorkAndJudgeDirsAfterScoring: once a round is
// scored, its store-side work dir is gone entirely - not moved to an
// archive elsewhere under the work root, since no later session poking
// around under workDir should find a live result anywhere - and the
// judge's own scratch dir, which must sit entirely outside workDir in the
// first place (nothing named "judge" ever appears there, beside the
// reviewer's own worktree), is gone too once the case ends.
func TestRunCaseDeletesWorkAndJudgeDirsAfterScoring(t *testing.T) {
	c := loadEvalCase(t, "nil-deref")
	backend := scriptedReviewerBackend{dir: resultsDir("perfect")}
	workDir := t.TempDir()

	var judgeWorkDirs []string
	judge := recordingJudge{inner: fixtureJudge{dir: resultsDir("perfect")}, workDirs: &judgeWorkDirs}

	cs, err := RunCase(workDir, c, backend, judge, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if len(cs.Rounds) != 1 || !cs.Rounds[0].Passed {
		t.Fatalf("RunCase: want one passed round, got %+v", cs.Rounds)
	}
	if len(judgeWorkDirs) != 1 || judgeWorkDirs[0] == "" {
		t.Fatalf("the judge was not dispatched with its own work dir: %v", judgeWorkDirs)
	}
	judgeDir := judgeWorkDirs[0]

	if rel, rerr := filepath.Rel(workDir, judgeDir); rerr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Errorf("judge scratch dir %s sits under the case work root %s (rel=%s), want it entirely outside", judgeDir, workDir, rel)
	}

	workGone := filepath.Join(workDir, "store", runID(c.Name), "work")
	if _, err := os.Stat(workGone); !os.IsNotExist(err) {
		t.Errorf("store work dir %s still exists (err=%v), want it deleted after scoring", workGone, err)
	}
	if _, err := os.Stat(judgeDir); !os.IsNotExist(err) {
		t.Errorf("judge scratch dir %s still exists (err=%v), want it deleted once the case ends", judgeDir, err)
	}
	// judgeDir is judgeRoot/round-1, which retireRoundWork already deletes
	// per round; the root itself (RunCase's own os.MkdirTemp) is a
	// separate removal (its own defer), so it must be checked on its own -
	// an empty judgeRoot left behind is not caught by judgeDir's own check
	// above, or by the "exactly [repo store]" listing below, since
	// judgeRoot never sits under workDir at all.
	judgeRoot := filepath.Dir(judgeDir)
	if _, err := os.Stat(judgeRoot); !os.IsNotExist(err) {
		t.Errorf("judge scratch root %s still exists (err=%v), want it deleted after RunCase returns", judgeRoot, err)
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("read work dir: %v", err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	if len(got) != 2 || !got["repo"] || !got["store"] {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("work dir %s holds %v, want exactly [repo store]: nothing named \"judge\" beside the reviewer's worktree", workDir, names)
	}
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
