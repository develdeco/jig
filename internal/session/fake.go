package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/outcome"
)

// fakeBackend is the CI path: a deterministic, zero-API scenario player. It
// reads scripted attempts from a scenario directory and applies them to the
// dispatch's worktree.
type fakeBackend struct {
	scenarioDir string
}

func newFakeBackend(opts Options) Backend {
	return &fakeBackend{scenarioDir: opts.ScenarioDir}
}

// fakeGitEnv pins the author/committer identity and dates for fake-backend
// commits so tests can compare shas and messages deterministically.
var fakeGitEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// Run plays back the scripted attempt for d.Slice/d.Attempt: missing
// scenario coverage becomes a failed result, a present patch.diff is
// applied and committed with a pinned identity, and the scenario's
// result.json is copied to d.ResultJSON (substituting the real HEAD sha for
// a green result whose commit field is "@HEAD" or absent). A gate-review
// dispatch (d.Slice == "gate") is played back separately by runGate.
func (b *fakeBackend) Run(d Dispatch) error {
	if d.Slice == "gate" {
		return b.runGate(d)
	}

	attemptDir := filepath.Join(b.scenarioDir, "slices", d.Slice, fmt.Sprintf("attempt-%d", d.Attempt))
	if fi, err := os.Stat(attemptDir); err != nil || !fi.IsDir() {
		return writeJSONResult(d.ResultJSON, map[string]any{
			"outcome": outcome.Failed,
			"summary": fmt.Sprintf("scenario has no attempt %d for slice %s", d.Attempt, d.Slice),
		})
	}

	resultData, err := os.ReadFile(filepath.Join(attemptDir, "result.json"))
	if err != nil {
		return fmt.Errorf("session/fake: read scenario result.json: %w", err)
	}
	var res map[string]any
	if err := json.Unmarshal(resultData, &res); err != nil {
		return fmt.Errorf("session/fake: parse scenario result.json: %w", err)
	}

	patchPath := filepath.Join(attemptDir, "patch.diff")
	if fi, err := os.Stat(patchPath); err == nil && fi.Size() > 0 {
		if err := applyFakePatch(d, patchPath, res); err != nil {
			return err
		}
	}

	if outc, _ := res["outcome"].(string); outc == outcome.Green {
		commit, _ := res["commit"].(string)
		if commit == "" || commit == "@HEAD" {
			sha, err := gitx.RevParse(d.Worktree, "HEAD")
			if err != nil {
				return fmt.Errorf("session/fake: resolve HEAD sha: %w", err)
			}
			res["commit"] = sha
		}
	}

	out, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("session/fake: marshal result: %w", err)
	}
	return writeResultBytes(d.ResultJSON, out)
}

// runGate plays back a gate-review dispatch: it copies
// <scenario>/gate/round-<n>/review-result.json verbatim into d.ResultJSON.
// Missing scenario coverage is an error, never a silent clean result - a
// gate round the scenario forgot to script must fail loudly rather than be
// misread as "nothing to find". The worktree is never touched.
func (b *fakeBackend) runGate(d Dispatch) error {
	path := filepath.Join(b.scenarioDir, "gate", fmt.Sprintf("round-%d", d.Attempt), "review-result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("session/fake: scenario has no gate round %d review-result.json", d.Attempt)
	}
	return writeResultBytes(d.ResultJSON, data)
}

// applyFakePatch applies patchPath in d.Worktree, stages everything, and
// commits with the pinned fixture identity and a message built from the
// first line of the scenario result's summary.
func applyFakePatch(d Dispatch, patchPath string, res map[string]any) error {
	absPatch, err := filepath.Abs(patchPath)
	if err != nil {
		return fmt.Errorf("session/fake: resolve patch path: %w", err)
	}
	if _, err := gitx.Run(d.Worktree, "apply", absPatch); err != nil {
		return fmt.Errorf("session/fake: git apply: %w", err)
	}
	if _, err := gitx.Run(d.Worktree, "add", "-A"); err != nil {
		return fmt.Errorf("session/fake: git add: %w", err)
	}
	summary, _ := res["summary"].(string)
	msg := fmt.Sprintf("%s %s: %s", d.Ticket, d.Slice, firstLine(summary))
	if _, err := gitx.RunEnv(d.Worktree, fakeGitEnv, "commit", "-m", msg); err != nil {
		return fmt.Errorf("session/fake: git commit: %w", err)
	}
	return nil
}

// firstLine returns s up to its first line break, or all of s if it has
// none.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		return s[:i]
	}
	return s
}
