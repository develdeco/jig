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

// defaultLiveModel is the model Gate itself picks when the builders used
// the cheapest rung, the common unattended case: the rung after the
// cheapest, not the cheapest itself. Gate's own model choice
// (internal/verifydeliver/gate.go) is staircase.Disjoint(d.Rungs,
// journal.BuilderModels(lines)); an unattended ticket's builders record the
// cheapest rung (staircase.Select opens on it), so Disjoint's next
// available rung up is what Gate would actually dispatch its reviewer
// with. Derived rather than hardcoded so this test never drifts from
// Gate's own choice.
func defaultLiveModel() string {
	cfg := staircase.Default()
	return staircase.Disjoint(cfg, []string{cfg.Rungs[0]})
}

// TestEvalLive runs the whole corpus through a real session backend,
// gated behind JIG_REVIEWEVAL_BACKEND so it never runs unattended in CI:
//
//	JIG_REVIEWEVAL_BACKEND=headless go test -count=1 -timeout 0 -v -run TestEvalLive ./internal/revieweval
//
// Report-only: a case's result is a measurement, not a Go test failure -
// review quality drifts over time and this run's job is to show that, not
// to gate the build on it. Only infrastructure failure (the backend is
// unavailable, a case cannot even be materialized, the corpus does not
// load, or a round comes back Failed - dispatch failure, judge failure, or
// the judge changing the case repo) fails the test; a refused round stays
// a measurement.
//
// Cases run one at a time through RunCase, not RunCorpus, and the text
// report and JSON are rewritten after every case, so a timeout (-timeout 0
// above disables Go's own default) or a panic partway through the corpus
// still leaves whatever finished on disk, rather than only a report for a
// run that never completed.
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
		model = defaultLiveModel()
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

	reportPath := os.Getenv("JIG_REVIEWEVAL_REPORT")
	workRoot := t.TempDir()
	scores := make([]CaseScore, 0, len(cases))
	for _, c := range cases {
		dir := filepath.Join(workRoot, c.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("revieweval: create work dir for %s: %v", c.Name, err)
		}
		sc, err := RunCase(dir, c, backend, judge, model)
		if err != nil {
			t.Fatalf("revieweval: run case %s: %v", c.Name, err)
		}
		scores = append(scores, sc)

		report := RenderReport(scores)
		t.Log(report)
		if reportPath != "" {
			if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
				t.Fatalf("revieweval: write report to %s: %v", reportPath, err)
			}
			jsonData, err := RenderJSON(scores)
			if err != nil {
				t.Fatalf("revieweval: render JSON report: %v", err)
			}
			if err := os.WriteFile(reportPath+".json", jsonData, 0o644); err != nil {
				t.Fatalf("revieweval: write JSON report to %s.json: %v", reportPath, err)
			}
		}
	}

	// A Failed round is infrastructure trouble (dispatch failure, judge
	// failure, the judge changing the case repo), not a review-quality
	// measurement - fail the test so it is never silently buried in a
	// report nobody reads. A refused round stays a measurement.
	for _, cs := range scores {
		for _, r := range cs.Rounds {
			if r.Failed {
				t.Errorf("revieweval: case %s round %d: Failed: %s", cs.Name, r.Round, r.Reason)
			}
		}
	}
}

// TestDefaultLiveModelSkipsTheCheapestRung runs unconditionally (no
// JIG_REVIEWEVAL_BACKEND needed), unlike TestEvalLive itself, so the
// default model choice stays covered whether or not a real backend is
// available.
func TestDefaultLiveModelSkipsTheCheapestRung(t *testing.T) {
	cfg := staircase.Default()
	got := defaultLiveModel()
	if got == cfg.Rungs[0] {
		t.Errorf("defaultLiveModel() = %q, want the rung after the cheapest: Gate's own pick once the builders used it", got)
	}
	if want := staircase.Disjoint(cfg, []string{cfg.Rungs[0]}); got != want {
		t.Errorf("defaultLiveModel() = %q, want %q", got, want)
	}
}
