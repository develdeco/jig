package revieweval

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/session"
)

// TestEvalLive runs the full corpus through a real session backend,
// gated behind JIG_REVIEWEVAL_BACKEND so it never runs unattended in CI:
//
//	JIG_REVIEWEVAL_BACKEND=headless go test ./internal/revieweval -run Eval
//
// Case failures are measurements (the report), not Go test failures:
// review quality drifts over time and a live run's job is to show that,
// not to gate the build on it. Only infrastructure errors (the backend is
// unavailable, or a case cannot even be materialized) fail the test.
func TestEvalLive(t *testing.T) {
	backendName := os.Getenv("JIG_REVIEWEVAL_BACKEND")
	if backendName == "" {
		t.Skip("revieweval: JIG_REVIEWEVAL_BACKEND not set")
	}
	if err := session.Available(backendName); err != nil {
		t.Fatalf("revieweval: backend %s not available: %v", backendName, err)
	}
	backend, err := session.New(backendName, session.Options{})
	if err != nil {
		t.Fatalf("revieweval: construct backend %s: %v", backendName, err)
	}

	model := os.Getenv("JIG_REVIEWEVAL_MODEL")
	if model == "" {
		// The rung gate picks this model when builders used the cheapest
		// rung (staircase.Disjoint's default gate model).
		model = "claude-sonnet-5"
	}

	cases := loadCorpus(t)
	scores, err := RunCorpus(t.TempDir(), cases, backend, model)
	if err != nil {
		t.Fatalf("revieweval: run corpus: %v", err)
	}

	report := RenderReport(scores)
	t.Log(report)
	if path := os.Getenv("JIG_REVIEWEVAL_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			t.Fatalf("revieweval: write report to %s: %v", path, err)
		}
	}
}
