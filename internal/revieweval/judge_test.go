package revieweval

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// stubJudgeBackend writes body to the dispatch's ResultJSON (verdicts.json
// for a judge dispatch), or returns err when set, standing in for a real
// session backend in ModelJudge tests.
type stubJudgeBackend struct {
	body string
	err  error
}

func (b stubJudgeBackend) Run(d session.Dispatch) error {
	if b.err != nil {
		return b.err
	}
	return os.WriteFile(d.ResultJSON, []byte(b.body), 0o644)
}

// twoCandidateQuery builds a JudgeQuery with two candidates over two
// findings, enough for every readVerdicts test below.
func twoCandidateQuery(t *testing.T) JudgeQuery {
	t.Helper()
	return JudgeQuery{
		Case: "case1", Round: 1, RepoDir: t.TempDir(), WorkDir: filepath.Join(t.TempDir(), "work"),
		Findings: []verifydeliver.ResultFinding{
			{File: "a.go", Line: 10, Title: "t0"},
			{File: "a.go", Line: 20, Title: "t1"},
		},
		Candidates: []Candidate{
			{Point: Point{ID: "g1", File: "a.go", From: 10, To: 10}, Finding: 0},
			{Point: Point{ID: "g2", File: "a.go", From: 20, To: 20}, Finding: 1},
		},
	}
}

func TestModelJudgeConfirmParsesAValidVerdictsFile(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":0,"same":true},{"candidate":1,"same":false}]}`}, Model: "m"}
	verdicts, err := judge.Confirm(twoCandidateQuery(t))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(verdicts) != 2 || verdicts[0] != Same || verdicts[1] != Different {
		t.Errorf("verdicts = %v, want [same different]", verdicts)
	}
}

func TestModelJudgeConfirmRejectsAMissingCandidate(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":0,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, candidate 1 was never answered")
	}
}

func TestModelJudgeConfirmRejectsADuplicateCandidate(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":0,"same":true},{"candidate":0,"same":false},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, candidate 0 answered twice")
	}
}

func TestModelJudgeConfirmRejectsAnUnknownKey(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":0,"same":true},{"candidate":1,"same":true}],"confidence":"high"}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, \"confidence\" is not a recognized key")
	}
}

func TestModelJudgeConfirmReturnsADispatchError(t *testing.T) {
	wantErr := errors.New("boom")
	judge := &ModelJudge{Backend: stubJudgeBackend{err: wantErr}, Model: "m"}
	_, err := judge.Confirm(twoCandidateQuery(t))
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("Confirm error = %v, want it to wrap %v", err, wantErr)
	}
}
