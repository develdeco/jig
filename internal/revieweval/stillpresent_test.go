package revieweval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestStillPresentScoresLikeAFullReport runs one corpus case whose second
// round confirms an unchanged earlier finding in still_present instead of
// writing it in full (perfect/forgotten-finding, rewritten here): every
// round must score exactly as the full re-report does, a clean pass.
func TestStillPresentScoresLikeAFullReport(t *testing.T) {
	t.Parallel()

	const name = "forgotten-finding"
	src := filepath.Join(resultsDir("perfect"), name)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"round-1.json", "round-2.json"} {
		data, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if f == "round-2.json" {
			res, err := verifydeliver.ParseReviewResult(data)
			if err != nil {
				t.Fatalf("parse %s: %v", f, err)
			}
			var kept []verifydeliver.ResultFinding
			for _, rf := range res.Findings {
				if rf.Prior == "" {
					kept = append(kept, rf)
					continue
				}
				res.StillPresent = append(res.StillPresent, verifydeliver.StillPresentEntry{Prior: rf.Prior, Line: rf.Line})
			}
			if len(res.StillPresent) == 0 {
				t.Fatalf("%s round 2 has no finding with a prior to confirm", name)
			}
			if kept == nil {
				kept = []verifydeliver.ResultFinding{}
			}
			res.Findings = kept
			if data, err = json.Marshal(res); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	scores, err := RunCorpus(t.TempDir(), []Case{loadEvalCase(t, name)}, scriptedReviewerBackend{dir: dir}, nil, "fixture-model")
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if len(scores) != 1 || len(scores[0].Rounds) != 2 {
		t.Fatalf("scores = %+v, want one case with two rounds", scores)
	}
	for _, rs := range scores[0].Rounds {
		if !rs.Passed || rs.Verdict != VerdictPass || len(rs.Pending) != 0 {
			t.Errorf("round %d: Passed=%v Verdict=%q (missed=%v lost=%v forgotten=%v wrong-priors=%v reason=%q), want a clean pass",
				rs.Round, rs.Passed, rs.Verdict, rs.Missed, rs.Lost, rs.Forgotten, rs.WrongPriors, rs.Reason)
		}
	}
}
