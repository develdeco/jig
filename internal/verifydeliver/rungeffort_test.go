package verifydeliver

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// TestGateDispatchesTheReviewerOnTheDearestRungWithEffortByScope drives
// three gate rounds over a ticket whose builders all ran on the dearest rung:
// the reviewer runs on that rung, at the full effort on round 1 and the delta
// effort on round 2, each round's own journal line records the effort, and
// round 3, which dispatches no reviewer, records none (ADR 0023).
func TestGateDispatchesTheReviewerOnTheDearestRungWithEffortByScope(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-b")
	d := newDeps(t, fx)
	d.Rungs = staircase.Config{Rungs: []string{"rung-a", "rung-b"}}

	type seen struct{ model, effort, scope string }
	var got []seen
	backend := stubBackend{run: func(sd session.Dispatch) error {
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}
		got = append(got, seen{sd.Model, sd.Effort, req.Scope})
		result := ReviewResult{ReviewedPaths: req.MustReview}
		if len(got) == 1 {
			result.Findings = []ResultFinding{{
				File: "alpha/alpha.go", Line: 1, Title: "needs a fix", Detail: "d",
				Action: ActionFix, Risk: RiskMedium, RiskRationale: "r", Oracle: "test",
			}}
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := NewReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want one", report1.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report1.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark the fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report1.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push the fix slice state: %v", err)
	}
	if _, err := Gate(d, src, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	// Round 3 has nothing outstanding and nothing new to review, so it
	// dispatches no reviewer and journals no effort.
	if _, err := Gate(d, src, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}

	want := []seen{{"rung-b", "high", "full"}, {"rung-b", "medium", "delta"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("reviewer dispatches = %+v, want %+v", got, want)
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	efforts := map[int]string{}
	for _, l := range lines {
		if l.Event == "gate-round" || l.Event == "gate-clean" {
			efforts[l.Attempt] = l.Effort
		}
	}
	if len(efforts) != 3 || efforts[1] != "high" || efforts[2] != "medium" || efforts[3] != "" {
		t.Errorf("round lines' efforts by round = %v, want 1: high, 2: medium, 3: none (no reviewer); journal: %+v", efforts, lines)
	}
}
