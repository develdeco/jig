package revieweval

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// requestCapturingBackend plays a scripted result set and keeps the
// review.json each round's reviewer was handed, by round.
type requestCapturingBackend struct {
	inner scriptedReviewerBackend
	reqs  map[int]verifydeliver.ReviewRequest
}

func (b requestCapturingBackend) Run(d session.Dispatch) error {
	data, err := os.ReadFile(d.SliceJSON)
	if err != nil {
		return err
	}
	var req verifydeliver.ReviewRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return err
	}
	b.reqs[d.Attempt] = req
	return b.inner.Run(d)
}

// TestRecordedProbesReachTheNextRound (ADR 0036): a teacher-forced case's
// recorded round 1 carries a probe, and its runner hands round 2 that probe
// as the gate would, current while the file has the blob it recorded, in the
// hunk it falls inside.
func TestRecordedProbesReachTheNextRound(t *testing.T) {
	t.Parallel()

	backend := requestCapturingBackend{inner: scriptedReviewerBackend{dir: resultsDir("perfect")}, reqs: map[int]verifydeliver.ReviewRequest{}}
	if _, err := RunCorpus(t.TempDir(), []Case{loadEvalCase(t, "unprobed-term")}, backend, nil, "fixture-model"); err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if got := backend.reqs[1].Probed; len(got) != 0 {
		t.Errorf("round 1 probed = %+v, want none", got)
	}
	req := backend.reqs[2]
	if len(req.Probed) != 1 || req.Probed[0].File != "cart/cart.go" || req.Probed[0].Line != 26 || req.Probed[0].State != verifydeliver.ProbeCurrent {
		t.Fatalf("round 2 probed = %+v, want the recorded probe of cart/cart.go:26, current", req.Probed)
	}
	var covering *verifydeliver.HunkEntry
	for i, h := range req.Hunks {
		if h.File == "cart/cart.go" && h.From <= 26 && 26 <= h.To {
			covering = &req.Hunks[i]
		}
	}
	if covering == nil || covering.Probes != 1 {
		t.Errorf("round 2 hunks = %+v, want the one holding cart/cart.go:26 to count the probe", req.Hunks)
	}
}
