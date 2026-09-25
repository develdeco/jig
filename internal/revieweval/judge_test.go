package revieweval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// --- strict verdicts ------------------------------------------------------

func TestModelJudgeConfirmRejectsAnEntryMissingTheCandidateKey(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"same":true},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, one entry has no \"candidate\" key at all")
	}
}

func TestModelJudgeConfirmRejectsANullCandidate(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":null,"same":true},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, \"candidate\" is null")
	}
}

func TestModelJudgeConfirmRejectsAnEntryMissingTheSameKey(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":0},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, one entry has no \"same\" key at all")
	}
}

func TestModelJudgeConfirmRejectsANullSame(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":0,"same":null},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, \"same\" is null")
	}
}

func TestModelJudgeConfirmRejectsACaseVariantKey(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"Candidate":0,"same":true},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, \"Candidate\" is a case variant of \"candidate\", not a match")
	}
}

func TestModelJudgeConfirmRejectsATopLevelCaseVariantKey(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"Verdicts":[{"candidate":0,"same":true},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, \"Verdicts\" is a case variant of \"verdicts\", not a match")
	}
}

func TestModelJudgeConfirmRejectsAKeyRepeatedInOneObject(t *testing.T) {
	judge := &ModelJudge{Backend: stubJudgeBackend{body: `{"verdicts":[{"candidate":0,"same":true,"same":false},{"candidate":1,"same":true}]}`}, Model: "m"}
	if _, err := judge.Confirm(twoCandidateQuery(t)); err == nil {
		t.Fatal("Confirm: want an error, \"same\" is repeated within one verdict entry")
	}
}

// --- one question for the judge, whatever the point is ---------------------

// TestJudgePromptNeverNamesAPointKind pins that the judge prompt states the
// job and the output contract only: it must never name or hint at which of
// gold/trap/decision/dismissed a candidate's point is, since the judge is
// asked the same one question for every candidate.
func TestJudgePromptNeverNamesAPointKind(t *testing.T) {
	prompt := fmt.Sprintf(judgePromptTemplate, 1, "judge.json", "verdicts.json")
	for _, word := range []string{"seeded", "trap", "decision", "dismissed", "gold"} {
		if strings.Contains(strings.ToLower(prompt), word) {
			t.Errorf("prompt contains %q: the judge must not be told which kind of point a candidate is", word)
		}
	}
}

// TestJudgePromptTemplateHasNoRoomForACaseName pins the prompt template
// itself: judgePromptTemplate takes exactly round, the
// judge.json path and the verdicts.json path - three verbs, no fourth slot
// a case name could ever be threaded through, however the caller changed.
func TestJudgePromptTemplateHasNoRoomForACaseName(t *testing.T) {
	if n := strings.Count(judgePromptTemplate, "%"); n != 3 {
		t.Fatalf("judgePromptTemplate has %d format verbs, want exactly 3 (round, judge.json path, verdicts.json path)", n)
	}
}
