package revieweval

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestScorerPassesEveryCaseOnAPerfectResult is the always-on, network-free
// proof the scorer works: a stub backend plays back
// testdata/results/perfect/<case>.json (exactly the gold findings,
// correctly classified) for every corpus case, and every case must PASS.
func TestScorerPassesEveryCaseOnAPerfectResult(t *testing.T) {
	cases := loadCorpus(t)
	backend := scriptedBackend{dir: filepath.Join("testdata", "results", "perfect")}

	scores, err := RunCorpus(t.TempDir(), cases, backend, "test-model")
	if err != nil {
		t.Fatalf("revieweval: run corpus: %v", err)
	}
	for _, s := range scores {
		if !s.Passed {
			t.Errorf("case %s: want PASS, got FAIL (missed=%v fp=%v unmatched=%v reason=%q)", s.Name, s.Missed, s.FalsePositives, s.Unmatched, s.Reason)
		}
	}

	report := RenderReport(scores)
	if !strings.Contains(report, "PASS") {
		t.Errorf("report has no PASS line:\n%s", report)
	}
	if strings.Contains(report, "FAIL") {
		t.Errorf("report unexpectedly has a FAIL line:\n%s", report)
	}
}

// TestScorerFailsRegressedCasesWithExpectedCounts is the proof the scorer
// can fail: testdata/results/regressed/<case>.json scripts a missed
// finding, a flagged trap, and a misclassified finding, and each must FAIL
// with the counts the regression implies.
func TestScorerFailsRegressedCasesWithExpectedCounts(t *testing.T) {
	cases := loadCorpus(t)
	names := []string{"nil-deref", "loopvar-trap", "mechanical-batch"}
	var selected []Case
	for _, name := range names {
		selected = append(selected, caseByName(t, cases, name))
	}

	backend := scriptedBackend{dir: filepath.Join("testdata", "results", "regressed")}
	scores, err := RunCorpus(t.TempDir(), selected, backend, "test-model")
	if err != nil {
		t.Fatalf("revieweval: run corpus: %v", err)
	}

	byName := map[string]CaseScore{}
	for _, s := range scores {
		byName[s.Name] = s
	}

	// nil-deref: the reviewer missed the finding entirely.
	if s := byName["nil-deref"]; s.Passed || len(s.Missed) != 1 || len(s.FalsePositives) != 0 {
		t.Errorf("nil-deref: want FAIL missed=1 fp=0, got passed=%v missed=%v fp=%v", s.Passed, s.Missed, s.FalsePositives)
	}

	// loopvar-trap: the reviewer flagged the trap as a false positive.
	if s := byName["loopvar-trap"]; s.Passed || len(s.FalsePositives) != 1 || len(s.Missed) != 0 {
		t.Errorf("loopvar-trap: want FAIL fp=1 missed=0, got passed=%v fp=%v missed=%v", s.Passed, s.FalsePositives, s.Missed)
	}

	// mechanical-batch: one finding has the wrong class, so its gold entry
	// stays missed and the misclassified finding itself is unmatched (not
	// a false positive: this case has other gold findings, so an
	// unrecognized finding is reported, not failing on its own).
	if s := byName["mechanical-batch"]; s.Passed || len(s.Missed) != 1 || len(s.Unmatched) != 1 || len(s.FalsePositives) != 0 {
		t.Errorf("mechanical-batch: want FAIL missed=1 unmatched=1 fp=0, got passed=%v missed=%v unmatched=%v fp=%v", s.Passed, s.Missed, s.Unmatched, s.FalsePositives)
	}

	report := RenderReport(scores)
	if !strings.Contains(report, "FAIL") {
		t.Errorf("report has no FAIL line:\n%s", report)
	}
	for _, name := range names {
		if !strings.Contains(report, name) {
			t.Errorf("report missing case %s:\n%s", name, report)
		}
	}
}

// TestScoreCaseFalsePositiveWithNoGoldFindings covers the clean-case rule
// directly: with zero gold findings, every raised finding is a false
// positive, whether or not it happens to match a trap.
func TestScoreCaseFalsePositiveWithNoGoldFindings(t *testing.T) {
	cases := loadCorpus(t)
	clean := caseByName(t, cases, "clean")

	result := parsedResult(t, `{"verdict":"findings","findings":[{"id":"f1","class":"mechanical","title":"Spurious finding","detail":"stringutil/stringutil.go: nothing is actually wrong here.","workspace":"root","oracle":"test"}],"closures":[],"summary":"spurious"}`)

	sc := scoreCase(clean, result)
	if sc.Passed || len(sc.FalsePositives) != 1 || len(sc.Missed) != 0 || len(sc.Unmatched) != 0 {
		t.Errorf("clean case with a spurious finding: want FAIL fp=1, got passed=%v fp=%v missed=%v unmatched=%v", sc.Passed, sc.FalsePositives, sc.Missed, sc.Unmatched)
	}
}

// TestRunCaseFailsWithReasonOnDispatchError proves a failed dispatch fails
// the case via Reason rather than as a Go error, so a corpus run keeps
// scoring the rest of the cases.
func TestRunCaseFailsWithReasonOnDispatchError(t *testing.T) {
	cases := loadCorpus(t)
	c := caseByName(t, cases, "clean")

	backend := scriptedBackend{dir: filepath.Join("testdata", "results", "regressed")} // no clean.json there
	score, err := RunCase(t.TempDir(), c, backend, "test-model")
	if err != nil {
		t.Fatalf("revieweval: RunCase returned an error instead of a Reason: %v", err)
	}
	if score.Passed || score.Reason == "" {
		t.Errorf("want a failed case with a Reason, got passed=%v reason=%q", score.Passed, score.Reason)
	}
}

// TestRunCaseFailsWithReasonOnInvalidResult proves a strictly-invalid
// result.json (ParseReviewResult's job) fails the case via Reason too.
func TestRunCaseFailsWithReasonOnInvalidResult(t *testing.T) {
	cases := loadCorpus(t)
	c := caseByName(t, cases, "clean")

	backend := invalidResultBackend{}
	score, err := RunCase(t.TempDir(), c, backend, "test-model")
	if err != nil {
		t.Fatalf("revieweval: RunCase returned an error instead of a Reason: %v", err)
	}
	if score.Passed || score.Reason == "" {
		t.Errorf("want a failed case with a Reason, got passed=%v reason=%q", score.Passed, score.Reason)
	}
}
