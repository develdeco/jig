package revieweval

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
)

// buildJigBinary compiles cmd/jig into a temp dir and returns its path, so
// TestEvalLive can pass it as session.Options{ScreenBinary: ...} - every
// reviewer and judge dispatch is screened, and a screened headless session
// needs a real jig binary for its PreToolUse hook to re-exec
// (internal/session/headless_live_test.go builds the same way).
func buildJigBinary(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "jig-revieweval-bin")
	if err != nil {
		t.Fatalf("revieweval: create build dir: %v", err)
	}
	gittest.AtExit(func() { os.RemoveAll(dir) })

	name := "jig"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, filepath.Join(fixture.RepoRoot(t), "cmd", "jig"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("revieweval: build jig: %v\n%s", err, output)
	}
	return out
}

// TestEvalLive runs the whole corpus through a real session backend,
// gated behind JIG_REVIEWEVAL_BACKEND so it never runs unattended in CI:
//
//	JIG_REVIEWEVAL_BACKEND=headless go test ./internal/revieweval -run Live
//
// Report-only: a case's result is a measurement, not a Go test failure -
// review quality drifts over time and this run's job is to show that, not
// to gate the build on it. Only infrastructure failure (the backend is
// unavailable, a case cannot even be materialized, the corpus does not
// load) fails the test.
func TestEvalLive(t *testing.T) {
	backendName := os.Getenv("JIG_REVIEWEVAL_BACKEND")
	if backendName == "" {
		t.Skip("revieweval: JIG_REVIEWEVAL_BACKEND not set")
	}
	if err := session.Available(backendName); err != nil {
		t.Fatalf("revieweval: backend %s not available: %v", backendName, err)
	}

	jigBin := buildJigBinary(t)
	backend, err := session.New(backendName, session.Options{ScreenBinary: jigBin})
	if err != nil {
		t.Fatalf("revieweval: construct backend %s: %v", backendName, err)
	}

	model := os.Getenv("JIG_REVIEWEVAL_MODEL")
	if model == "" {
		// The model Gate picks when no builder model is recorded yet
		// (staircase.Disjoint's cheapest-rung default), derived rather
		// than hardcoded so this test never drifts from Gate's own
		// choice.
		model = staircase.Disjoint(staircase.Default(), nil)
	}
	judgeModel := os.Getenv("JIG_REVIEWEVAL_JUDGE_MODEL")
	if judgeModel == "" {
		judgeModel = model
	}
	judge := &ModelJudge{Backend: backend, Model: judgeModel}

	corpusRoot := os.Getenv("JIG_REVIEWEVAL_CORPUS")
	if corpusRoot == "" {
		corpusRoot = filepath.Join(fixture.RepoRoot(t), "testdata", "revieweval")
	}
	cases, err := LoadCorpus(corpusRoot)
	if err != nil {
		t.Fatalf("revieweval: load corpus: %v", err)
	}

	scores, err := RunCorpus(t.TempDir(), cases, backend, judge, model)
	if err != nil {
		t.Fatalf("revieweval: run corpus: %v", err)
	}

	report := RenderReport(scores)
	t.Log(report)
	if path := os.Getenv("JIG_REVIEWEVAL_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			t.Fatalf("revieweval: write report to %s: %v", path, err)
		}
		jsonData, err := RenderJSON(scores)
		if err != nil {
			t.Fatalf("revieweval: render JSON report: %v", err)
		}
		if err := os.WriteFile(path+".json", jsonData, 0o644); err != nil {
			t.Fatalf("revieweval: write JSON report to %s.json: %v", path, err)
		}
	}
}
