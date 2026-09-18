package revieweval

import (
	"path/filepath"
	"regexp"
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
	names := []string{"nil-deref", "loopvar-trap", "mechanical-batch", "tenant-leak"}
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

	// tenant-leak: the real ForTenant finding is found, but a second
	// finding flags Summarize (a correct, tenant-filtered helper) as
	// leaking too, tripping the trap.
	if s := byName["tenant-leak"]; s.Passed || len(s.Found) != 1 || len(s.Missed) != 0 || len(s.FalsePositives) != 1 {
		t.Errorf("tenant-leak: want FAIL found=1 missed=0 fp=1, got passed=%v found=%v missed=%v fp=%v", s.Passed, s.Found, s.Missed, s.FalsePositives)
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

// TestScorerRejectsGamedReviews proves the tightened gold patterns and the
// one-to-one matching cannot be gamed: a finding that only names the
// vocabulary near a bug (without describing the defect), or one lumped
// finding claiming every mechanical issue at once, must still FAIL.
func TestScorerRejectsGamedReviews(t *testing.T) {
	cases := loadCorpus(t)
	names := []string{"tenant-leak", "nil-deref", "mechanical-batch"}
	var selected []Case
	for _, name := range names {
		selected = append(selected, caseByName(t, cases, name))
	}

	backend := scriptedBackend{dir: filepath.Join("testdata", "results", "gamed")}
	scores, err := RunCorpus(t.TempDir(), selected, backend, "test-model")
	if err != nil {
		t.Fatalf("revieweval: run corpus: %v", err)
	}

	byName := map[string]CaseScore{}
	for _, s := range scores {
		byName[s.Name] = s
	}

	// tenant-leak: the finding only names ForTenant, never the leak, so
	// the tightened pattern must reject it.
	if s := byName["tenant-leak"]; s.Passed || len(s.Missed) != 1 {
		t.Errorf("tenant-leak: want FAIL missed=1 (gamed review rejected), got passed=%v missed=%v found=%v", s.Passed, s.Missed, s.Found)
	}

	// nil-deref: the finding says values are "never nil", the opposite of
	// the defect, so the tightened pattern must reject it.
	if s := byName["nil-deref"]; s.Passed || len(s.Missed) != 1 {
		t.Errorf("nil-deref: want FAIL missed=1 (gamed review rejected), got passed=%v missed=%v found=%v", s.Passed, s.Missed, s.Found)
	}

	// mechanical-batch: one finding naming all three issues can satisfy at
	// most one gold entry under one-to-one matching, so two stay missed.
	if s := byName["mechanical-batch"]; s.Passed || len(s.Found) != 1 || len(s.Missed) != 2 {
		t.Errorf("mechanical-batch: want FAIL found=1 missed=2 (one-to-one), got passed=%v found=%v missed=%v", s.Passed, s.Found, s.Missed)
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

// syntheticCase builds a Case directly (no corpus directory) with its gold
// regexps pre-compiled, so scoreCase can be unit tested against hand-picked
// gold/trap combinations without touching the real corpus.
func syntheticCase(t *testing.T, gold Gold) Case {
	t.Helper()
	c := Case{Name: "synthetic", Gold: gold}
	for _, g := range gold.Findings {
		c.goldFindingRe = append(c.goldFindingRe, regexp.MustCompile(g.TitlePattern))
	}
	for _, tr := range gold.Traps {
		c.goldTrapRe = append(c.goldTrapRe, regexp.MustCompile(tr.TitlePattern))
	}
	return c
}

// TestScoreCaseTrapWithGoldFindingsPresent exercises the trap check in a
// case that also has gold findings (M9/M1's gap: the corpus's only trap
// lived in a zero-gold case, so the trap branch never decided any
// outcome). A finding matching the trap must count as a false positive and
// fail the case even though the real gold finding was also found.
func TestScoreCaseTrapWithGoldFindingsPresent(t *testing.T) {
	c := syntheticCase(t, Gold{
		Findings: []GoldFinding{{ID: "g1", Class: "intent", TitlePattern: "(?i)real bug", File: "a.go"}},
		Traps:    []GoldTrap{{ID: "t1", TitlePattern: "(?i)trap pattern", File: "b.go"}},
	})

	result := parsedResult(t, `{"verdict":"findings","findings":[
		{"id":"f1","class":"intent","title":"real bug found","detail":"a.go: yes, a real problem here.","workspace":"root","oracle":"test"},
		{"id":"f2","class":"intent","title":"trap pattern triggered","detail":"b.go: looks wrong but is not.","workspace":"root","oracle":"test"}
	],"closures":[],"summary":"two findings"}`)

	sc := scoreCase(c, result)
	if sc.Passed || len(sc.Found) != 1 || len(sc.Missed) != 0 || len(sc.FalsePositives) != 1 {
		t.Errorf("trap in a gold-bearing case: want FAIL found=1 missed=0 fp=1, got passed=%v found=%v missed=%v fp=%v", sc.Passed, sc.Found, sc.Missed, sc.FalsePositives)
	}
}

// TestScoreCaseTrapWrongFileIsNotFlagged proves the trap's own file check
// is exercised: a finding matching the trap's title pattern but naming a
// different file must not be counted a false positive by the trap path (it
// still fails to match gold, so it is reported unmatched, not failing).
func TestScoreCaseTrapWrongFileIsNotFlagged(t *testing.T) {
	c := syntheticCase(t, Gold{
		Findings: []GoldFinding{{ID: "g1", Class: "intent", TitlePattern: "(?i)real bug", File: "a.go"}},
		Traps:    []GoldTrap{{ID: "t1", TitlePattern: "(?i)trap pattern", File: "b.go"}},
	})

	result := parsedResult(t, `{"verdict":"findings","findings":[
		{"id":"f1","class":"intent","title":"real bug found","detail":"a.go: yes, a real problem here.","workspace":"root","oracle":"test"},
		{"id":"f2","class":"intent","title":"trap pattern triggered","detail":"c.go: mentions the trap wording but not the trap's file.","workspace":"root","oracle":"test"}
	],"closures":[],"summary":"two findings"}`)

	sc := scoreCase(c, result)
	if !sc.Passed || len(sc.Found) != 1 || len(sc.FalsePositives) != 0 || len(sc.Unmatched) != 1 {
		t.Errorf("trap pattern on the wrong file: want passed found=1 fp=0 unmatched=1, got passed=%v found=%v fp=%v unmatched=%v", sc.Passed, sc.Found, sc.FalsePositives, sc.Unmatched)
	}
}

// TestScoreCaseWrongFileCountsAsMissed proves the file check is exercised:
// a finding with the right class and title pattern but the wrong file must
// not satisfy the gold entry.
func TestScoreCaseWrongFileCountsAsMissed(t *testing.T) {
	c := syntheticCase(t, Gold{
		Findings: []GoldFinding{{ID: "g1", Class: "intent", TitlePattern: "(?i)bug", File: "right.go"}},
	})

	result := parsedResult(t, `{"verdict":"findings","findings":[{"id":"f1","class":"intent","title":"bug found","detail":"wrong.go: description of the bug.","workspace":"root","oracle":"test"}],"closures":[],"summary":"s"}`)

	sc := scoreCase(c, result)
	if sc.Passed || len(sc.Found) != 0 || len(sc.Missed) != 1 || len(sc.FalsePositives) != 0 || len(sc.Unmatched) != 1 {
		t.Errorf("wrong file: want FAIL missed=1 unmatched=1, got passed=%v found=%v missed=%v fp=%v unmatched=%v", sc.Passed, sc.Found, sc.Missed, sc.FalsePositives, sc.Unmatched)
	}
}

// TestScoreCaseOneToOneMatching proves one result finding cannot satisfy
// two gold entries: two gold findings share a pattern and file, one result
// finding matches both, and only one may be counted found.
func TestScoreCaseOneToOneMatching(t *testing.T) {
	c := syntheticCase(t, Gold{
		Findings: []GoldFinding{
			{ID: "g1", Class: "intent", TitlePattern: "(?i)alpha", File: "a.go"},
			{ID: "g2", Class: "intent", TitlePattern: "(?i)alpha", File: "a.go"},
		},
	})

	result := parsedResult(t, `{"verdict":"findings","findings":[{"id":"f1","class":"intent","title":"alpha issue","detail":"a.go: alpha problem here.","workspace":"root","oracle":"test"}],"closures":[],"summary":"one finding"}`)

	sc := scoreCase(c, result)
	if len(sc.Found) != 1 || len(sc.Missed) != 1 {
		t.Errorf("one finding matching two gold entries: want found=1 missed=1 (one-to-one), got found=%v missed=%v", sc.Found, sc.Missed)
	}
}

// TestRunCaseFailsWithReasonOnDispatchError proves a failed dispatch fails
// the case via Reason rather than as a Go error, so a corpus run keeps
// scoring the rest of the cases, and that it counts the case's gold
// findings as missed rather than dropping them from recall's denominator.
func TestRunCaseFailsWithReasonOnDispatchError(t *testing.T) {
	cases := loadCorpus(t)
	c := caseByName(t, cases, "nil-deref")

	backend := scriptedBackend{dir: t.TempDir()} // empty dir: no scripted result for any ticket
	score, err := RunCase(t.TempDir(), c, backend, "test-model")
	if err != nil {
		t.Fatalf("revieweval: RunCase returned an error instead of a Reason: %v", err)
	}
	if score.Passed || score.Reason == "" {
		t.Errorf("want a failed case with a Reason, got passed=%v reason=%q", score.Passed, score.Reason)
	}
	if len(score.Missed) != len(c.Gold.Findings) {
		t.Errorf("want every gold finding counted as missed on dispatch failure, got missed=%v (gold has %d)", score.Missed, len(c.Gold.Findings))
	}
	if len(score.Found) != 0 {
		t.Errorf("want no found findings on dispatch failure, got found=%v", score.Found)
	}
}

// TestRunCaseFailsWithReasonOnInvalidResult proves a strictly-invalid
// result.json (ParseReviewResult's job) fails the case via Reason too, and
// also counts its gold findings as missed.
func TestRunCaseFailsWithReasonOnInvalidResult(t *testing.T) {
	cases := loadCorpus(t)
	c := caseByName(t, cases, "nil-deref")

	backend := invalidResultBackend{}
	score, err := RunCase(t.TempDir(), c, backend, "test-model")
	if err != nil {
		t.Fatalf("revieweval: RunCase returned an error instead of a Reason: %v", err)
	}
	if score.Passed || score.Reason == "" {
		t.Errorf("want a failed case with a Reason, got passed=%v reason=%q", score.Passed, score.Reason)
	}
	if len(score.Missed) != len(c.Gold.Findings) {
		t.Errorf("want every gold finding counted as missed on an invalid result, got missed=%v (gold has %d)", score.Missed, len(c.Gold.Findings))
	}
}
