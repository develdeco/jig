package revieweval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"gopkg.in/yaml.v3"
)

// This file is the CI structural path: it runs the real corpus
// (testdata/revieweval, repo root) through RunCorpus/RunCase against
// scripted result.json fixtures under testdata/results/<set>/<case>/, never
// a real session backend, so it needs no claude CLI and never runs
// unattended dispatch. Each fixture set exercises one designed shape:
// perfect (every round passes), regressed (every case fails with exactly
// one designed failure), gamed (a structural match a judge must reject) and
// refused (a round that never covers must_review).

// resultsDir is one fixture set's root, e.g. testdata/results/perfect.
func resultsDir(set string) string {
	return filepath.Join("testdata", "results", set)
}

// evalCorpusRoot is the real ten-case corpus's root, at the repo's own
// testdata/revieweval (not under this package), the same root live_test.go
// uses.
func evalCorpusRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(fixture.RepoRoot(t), "testdata", "revieweval")
}

// loadEvalCase loads one named case directly from the real corpus, for a
// fixture test that only needs one case (gamed, refused) rather than the
// whole corpus.
func loadEvalCase(t *testing.T, name string) Case {
	t.Helper()
	c, err := LoadCase(filepath.Join(evalCorpusRoot(t), name))
	if err != nil {
		t.Fatalf("revieweval: LoadCase(%s): %v", name, err)
	}
	return c
}

// judgeFixtureEntry is one line of a round-<N>.judge.yaml: the candidate it
// answers (by finding index and point id, matching how MatchRound built
// that round's candidates) and the verdict to give it.
type judgeFixtureEntry struct {
	Finding int    `yaml:"finding"`
	Point   string `yaml:"point"`
	Verdict string `yaml:"verdict"`
}

// fixtureJudge answers MatchRound's candidates from a scripted
// round-<N>.judge.yaml beside the fixture set's round-<N>.json (dir/<case>/
// round-<N>.judge.yaml); a candidate the file does not name comes back
// Undecided, and a case/round with no judge.yaml at all answers everything
// Undecided, the same as a query with nothing scripted to confirm.
type fixtureJudge struct {
	dir string
}

func (j fixtureJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	path := filepath.Join(j.dir, q.Case, fmt.Sprintf("round-%d.judge.yaml", q.Round))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make([]Verdict, len(q.Candidates)), nil
		}
		return nil, fmt.Errorf("revieweval: fixtureJudge: read %s: %w", path, err)
	}
	var entries []judgeFixtureEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("revieweval: fixtureJudge: parse %s: %w", path, err)
	}

	out := make([]Verdict, len(q.Candidates))
	for i, c := range q.Candidates {
		for _, e := range entries {
			if e.Finding != c.Finding || e.Point != c.Point.ID {
				continue
			}
			switch e.Verdict {
			case "same":
				out[i] = Same
			case "different":
				out[i] = Different
			default:
				return nil, fmt.Errorf("revieweval: fixtureJudge: %s: verdict %q is neither \"same\" nor \"different\"", path, e.Verdict)
			}
			break
		}
	}
	return out, nil
}

// TestFixtureLoadCorpusLoadsAllTenCases pins the real corpus's shape: ten
// cases in name order, fourteen rounds total (five one-round cases, five
// two-round cases). Every other fixture test below depends on this corpus
// loading at all, so a broken corpus file would otherwise surface as a
// confusing failure somewhere else instead of here.
func TestFixtureLoadCorpusLoadsAllTenCases(t *testing.T) {
	cases, err := LoadCorpus(evalCorpusRoot(t))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	wantNames := []string{
		"clean", "forgotten-finding", "judgment-call", "loopvar-trap", "mechanical-batch",
		"nil-deref", "nit-disguise", "reworded-dismissed", "tenant-leak", "title-collision",
	}
	if len(cases) != len(wantNames) {
		var got []string
		for _, c := range cases {
			got = append(got, c.Name)
		}
		t.Fatalf("LoadCorpus loaded %v, want %v", got, wantNames)
	}
	totalRounds := 0
	for i, c := range cases {
		if c.Name != wantNames[i] {
			t.Errorf("cases[%d].Name = %q, want %q", i, c.Name, wantNames[i])
		}
		totalRounds += len(c.Rounds)
	}
	if totalRounds != 14 {
		t.Errorf("total rounds = %d, want 14 (5 one-round cases + 5 two-round cases)", totalRounds)
	}
}

// TestFixturePerfectPassesEveryCaseEveryRound drives the whole real corpus
// through RunCorpus against the "perfect" fixture set with no judge
// (structural matching alone, since every perfect finding sits on its
// gold's own line): every round of every case must pass, with nothing
// missed, lost, dropped, misattributed, a false alarm or re-litigated.
func TestFixturePerfectPassesEveryCaseEveryRound(t *testing.T) {
	cases, err := LoadCorpus(evalCorpusRoot(t))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	backend := scriptedReviewerBackend{dir: resultsDir("perfect")}
	scores, err := RunCorpus(t.TempDir(), cases, backend, nil, "fixture-model")
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	roundCount := 0
	for _, cs := range scores {
		if !cs.Passed {
			t.Errorf("case %s: Passed = false, want true", cs.Name)
		}
		for _, rs := range cs.Rounds {
			roundCount++
			if !rs.Passed {
				t.Errorf("case %s round %d: Passed = false (missed=%v lost=%v forgotten=%v dropped=%v misattributed=%v false-alarms=%v relitigated=%v reason=%q)",
					cs.Name, rs.Round, rs.Missed, rs.Lost, rs.Forgotten, rs.DroppedQuestions, rs.Misattributed, rs.FalseAlarms, rs.Relitigated, rs.Reason)
			}
		}
	}
	if roundCount != 14 {
		t.Errorf("scored %d rounds, want 14", roundCount)
	}
}

// regressedWant is one case round's exact expected failure lists in the
// "regressed" fixture set: every field not set here must come back empty,
// so a test failure here means either the designed failure did not fire or
// something else fired alongside it.
type regressedWant struct {
	Case             string
	Round            int
	Missed           []string
	Lost             []string
	Forgotten        []string
	DroppedQuestions []string
	Misattributed    []string
	FalseAlarms      []string
	Relitigated      []string
	WrongPriors      []string
}

// TestFixtureRegressedFailsExactlyAsDesigned drives the whole real corpus
// through RunCorpus against the "regressed" fixture set: every case fails
// with exactly the one designed failure named for it (and nothing else),
// and a multi-round case's untouched round 1 (a copy of "perfect") still
// passes, proving teacher-forcing keeps round 2's regression from bleeding
// into round 1's own score.
func TestFixtureRegressedFailsExactlyAsDesigned(t *testing.T) {
	cases, err := LoadCorpus(evalCorpusRoot(t))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	backend := scriptedReviewerBackend{dir: resultsDir("regressed")}
	scores, err := RunCorpus(t.TempDir(), cases, backend, nil, "fixture-model")
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	byCaseRound := map[string]map[int]RoundScore{}
	for _, cs := range scores {
		m := map[int]RoundScore{}
		for _, rs := range cs.Rounds {
			m[rs.Round] = rs
		}
		byCaseRound[cs.Name] = m
	}

	wants := []regressedWant{
		{Case: "nil-deref", Round: 1, Missed: []string{"nil-entry"}},
		{Case: "tenant-leak", Round: 1, FalseAlarms: []string{"Summarize leaks tenant data across the shared slice"}},
		{Case: "loopvar-trap", Round: 1, FalseAlarms: []string{"job is captured by reference across goroutines"}},
		{Case: "mechanical-batch", Round: 1, Missed: []string{"doc-typo", "missing-doc"}},
		{Case: "clean", Round: 1, FalseAlarms: []string{"Reverse allocates unnecessarily"}},
		{Case: "forgotten-finding", Round: 1}, // copies perfect: passes
		{Case: "forgotten-finding", Round: 2, Missed: []string{"report-ignored-error"}, Forgotten: []string{"report-ignored-error"}},
		{Case: "reworded-dismissed", Round: 1}, // copies perfect: passes
		{Case: "reworded-dismissed", Round: 2, Relitigated: []string{"Labels rebuilds its string inefficiently"}},
		{Case: "title-collision", Round: 1}, // copies perfect: passes
		{Case: "title-collision", Round: 2, Lost: []string{"writer-close-ignored"}, Misattributed: []string{"writer-close-ignored"}},
		{Case: "nit-disguise", Round: 1}, // copies perfect: passes
		{Case: "nit-disguise", Round: 2, Lost: []string{"upper-bound-exclusive"}},
		{Case: "judgment-call", Round: 1, DroppedQuestions: []string{"retry-non-idempotent"}},
	}
	if len(wants) != 14 {
		t.Fatalf("test bug: %d want entries, want 14 (one per corpus round)", len(wants))
	}

	for _, w := range wants {
		rs, ok := byCaseRound[w.Case][w.Round]
		if !ok {
			t.Errorf("case %s round %d: not scored", w.Case, w.Round)
			continue
		}
		wantPassed := len(w.Missed) == 0 && len(w.Lost) == 0 && len(w.Forgotten) == 0 &&
			len(w.DroppedQuestions) == 0 && len(w.Misattributed) == 0 &&
			len(w.FalseAlarms) == 0 && len(w.Relitigated) == 0 && len(w.WrongPriors) == 0
		if rs.Passed != wantPassed {
			t.Errorf("case %s round %d: Passed = %v, want %v", w.Case, w.Round, rs.Passed, wantPassed)
		}
		// Every regressed row's designed failure is an ordinary scoring
		// mismatch (a missed, lost, dropped, misattributed, false-alarm,
		// re-litigated or wrong-prior finding), never a round the reviewer's
		// own contract refused or that infrastructure failed - pinning that
		// here catches a regression that turned one of these into a refusal
		// or a failure instead of the designed scoring shape.
		if rs.Refused {
			t.Errorf("case %s round %d: Refused = true, want false", w.Case, w.Round)
		}
		if rs.Failed {
			t.Errorf("case %s round %d: Failed = true, want false", w.Case, w.Round)
		}
		checkStringList(t, w.Case, w.Round, "Missed", rs.Missed, w.Missed)
		checkStringList(t, w.Case, w.Round, "Lost", rs.Lost, w.Lost)
		checkStringList(t, w.Case, w.Round, "Forgotten", rs.Forgotten, w.Forgotten)
		checkStringList(t, w.Case, w.Round, "DroppedQuestions", rs.DroppedQuestions, w.DroppedQuestions)
		checkStringList(t, w.Case, w.Round, "Misattributed", rs.Misattributed, w.Misattributed)
		checkStringList(t, w.Case, w.Round, "FalseAlarms", rs.FalseAlarms, w.FalseAlarms)
		checkStringList(t, w.Case, w.Round, "Relitigated", rs.Relitigated, w.Relitigated)
		checkStringList(t, w.Case, w.Round, "WrongPriors", rs.WrongPriors, w.WrongPriors)
	}
}

// checkStringList compares got against want as sets (order does not carry
// meaning for these lists beyond what ScoreRound's own gold/finding
// iteration order already fixes, which the regressed table above matches),
// failing with the case and round so a mismatch is easy to place.
func checkStringList(t *testing.T, caseName string, round int, field string, got, want []string) {
	t.Helper()
	gotSorted := append([]string(nil), got...)
	wantSorted := append([]string(nil), want...)
	sort.Strings(gotSorted)
	sort.Strings(wantSorted)
	if len(gotSorted) != len(wantSorted) {
		t.Errorf("case %s round %d: %s = %v, want %v", caseName, round, field, got, want)
		return
	}
	for i := range gotSorted {
		if gotSorted[i] != wantSorted[i] {
			t.Errorf("case %s round %d: %s = %v, want %v", caseName, round, field, got, want)
			return
		}
	}
}

// TestFixtureGamedMissedWithJudgeUnconfirmedWithoutJudge is the gamed
// fixture's whole point: a finding at the gold's exact location that
// argues the wrong thing is indistinguishable from a real match by
// structure alone. A judge that has actually read the finding and says
// Different rejects it (Missed); with no judge at all (structure's own
// default), it matches, but only ever as Unconfirmed, never Found without
// qualification - proving the judge stage, not the structural window, is
// what rejects a gamed finding.
func TestFixtureGamedMissedWithJudgeUnconfirmedWithoutJudge(t *testing.T) {
	c := loadEvalCase(t, "nil-deref")
	backend := scriptedReviewerBackend{dir: resultsDir("gamed")}

	t.Run("scripted-judge-says-different-is-missed", func(t *testing.T) {
		judge := fixtureJudge{dir: resultsDir("gamed")}
		cs, err := RunCase(t.TempDir(), c, backend, judge, "fixture-model")
		if err != nil {
			t.Fatalf("RunCase: %v", err)
		}
		if len(cs.Rounds) != 1 {
			t.Fatalf("RunCase: %d rounds, want 1", len(cs.Rounds))
		}
		rs := cs.Rounds[0]
		if len(rs.Missed) != 1 || rs.Missed[0] != "nil-entry" {
			t.Errorf("Missed = %v, want [nil-entry]: the judge rejected the gamed finding", rs.Missed)
		}
		if len(rs.Found) != 0 {
			t.Errorf("Found = %v, want none", rs.Found)
		}
		if rs.Passed {
			t.Error("Passed = true, want false: a rejected gold finding must fail the round")
		}
	})

	t.Run("nil-judge-matches-but-only-unconfirmed", func(t *testing.T) {
		cs, err := RunCase(t.TempDir(), c, backend, nil, "fixture-model")
		if err != nil {
			t.Fatalf("RunCase: %v", err)
		}
		if len(cs.Rounds) != 1 {
			t.Fatalf("RunCase: %d rounds, want 1", len(cs.Rounds))
		}
		rs := cs.Rounds[0]
		if len(rs.Found) != 1 || rs.Found[0] != "nil-entry" {
			t.Errorf("Found = %v, want [nil-entry]: structure alone still matches the same line", rs.Found)
		}
		if len(rs.Unconfirmed) != 1 || rs.Unconfirmed[0] != "nil-entry" {
			t.Errorf("Unconfirmed = %v, want [nil-entry]: with no judge, structure cannot confirm it, only match it", rs.Unconfirmed)
		}
		if len(rs.Missed) != 0 {
			t.Errorf("Missed = %v, want none", rs.Missed)
		}
	})
}

// TestFixtureRefusedCountsGoldAsMissed is the refused fixture's whole
// point: a result whose reviewed_paths leaves out a must_review path is
// REVIEW_INVALID (verifydeliver's own read-only, coverage-checked
// contract), which RunCase turns into a refused round rather than an
// infrastructure error, and every one of that round's seeded gold findings
// counts as missed, never left out of the denominator the way a zero-over-
// zero round would.
func TestFixtureRefusedCountsGoldAsMissed(t *testing.T) {
	c := loadEvalCase(t, "nil-deref")
	backend := scriptedReviewerBackend{dir: resultsDir("refused")}

	cs, err := RunCase(t.TempDir(), c, backend, nil, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if len(cs.Rounds) != 1 {
		t.Fatalf("RunCase: %d rounds, want 1", len(cs.Rounds))
	}
	rs := cs.Rounds[0]
	if !rs.Refused {
		t.Error("Refused = false, want true: reviewed_paths left out the only must_review path")
	}
	if rs.Failed {
		t.Error("Failed = true, want false: a missing-coverage result is refused, not failed")
	}
	if len(rs.Missed) != 1 || rs.Missed[0] != "nil-entry" {
		t.Errorf("Missed = %v, want [nil-entry]: a refused round misses every seeded gold finding", rs.Missed)
	}
	if rs.Passed {
		t.Error("Passed = true, want false")
	}
	if rs.Reason == "" {
		t.Error("Reason is empty, want the refusal's own message")
	}
}

// TestFixtureReportRendersTotals runs the whole corpus through the
// "perfect" fixture set and renders it, pinning that RenderReport's totals
// line actually reflects a full-corpus run (every case and round passed,
// full recall) rather than only the synthetic single-round inputs
// score_test.go exercises RenderReport with directly.
func TestFixtureReportRendersTotals(t *testing.T) {
	cases, err := LoadCorpus(evalCorpusRoot(t))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	backend := scriptedReviewerBackend{dir: resultsDir("perfect")}
	scores, err := RunCorpus(t.TempDir(), cases, backend, nil, "fixture-model")
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	report := RenderReport(scores)
	if !strings.Contains(report, "cases passed 10/10, rounds passed 14/14, recall 1.00") {
		t.Errorf("report totals line missing or wrong; got:\n%s", report)
	}
	if !strings.Contains(report, "lost 0, forgotten 0, dropped questions 0, misattributed 0, false alarms 0, re-litigated 0, wrong priors 0, refused 0, failed 0") {
		t.Errorf("report zero-failure line missing or wrong; got:\n%s", report)
	}
}
