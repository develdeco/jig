package revieweval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// identityEnv pins the git author/committer identity and date for every
// commit this package makes, matching the values internal/fixture and the
// fake session backend use elsewhere, so an eval repo's shas are stable
// across runs of the same corpus.
var identityEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// validateReviewTemplate checks that a case's review.json parses as a
// verifydeliver.ReviewRequest and declares scope "full", the only scope a
// corpus case uses.
func validateReviewTemplate(caseName, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("revieweval: case %s: read review.json: %w", caseName, err)
	}
	var req verifydeliver.ReviewRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("revieweval: case %s: parse review.json: %w", caseName, err)
	}
	if req.Scope != "full" {
		return fmt.Errorf("revieweval: case %s: review.json scope is %q, want \"full\"", caseName, req.Scope)
	}
	return nil
}

// materializeCaseRepo builds a throwaway git repo for c under
// workDir/repo: an empty base commit, then c's patch.diff applied and
// committed. It returns the repo dir and the base/head shas.
func materializeCaseRepo(workDir string, c Case) (repoDir, base, head string, err error) {
	repoDir = filepath.Join(workDir, "repo")
	if err = os.MkdirAll(repoDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("revieweval: create repo dir: %w", err)
	}
	if _, err = gitx.Run(repoDir, "init", "-b", "main"); err != nil {
		return "", "", "", fmt.Errorf("revieweval: git init: %w", err)
	}
	if _, err = gitx.RunEnv(repoDir, identityEnv, "commit", "--allow-empty", "-m", "revieweval: base"); err != nil {
		return "", "", "", fmt.Errorf("revieweval: base commit: %w", err)
	}
	base, err = gitx.RevParse(repoDir, "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("revieweval: resolve base sha: %w", err)
	}

	absPatch, err := filepath.Abs(c.PatchPath)
	if err != nil {
		return "", "", "", fmt.Errorf("revieweval: resolve patch path: %w", err)
	}
	if _, err = gitx.Run(repoDir, "apply", absPatch); err != nil {
		return "", "", "", fmt.Errorf("revieweval: apply %s: %w", c.PatchPath, err)
	}
	if _, err = gitx.Run(repoDir, "add", "-A"); err != nil {
		return "", "", "", fmt.Errorf("revieweval: git add: %w", err)
	}
	if _, err = gitx.RunEnv(repoDir, identityEnv, "commit", "-m", "revieweval: case diff"); err != nil {
		return "", "", "", fmt.Errorf("revieweval: diff commit: %w", err)
	}
	head, err = gitx.RevParse(repoDir, "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("revieweval: resolve head sha: %w", err)
	}
	return repoDir, base, head, nil
}

// absPath returns p resolved to an absolute path, or p itself when that
// fails.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// failedScore builds the CaseScore for a case that never produced a
// scoreable result: every gold finding counts as missed (not left at zero),
// so a run with dispatch or parse failures does not inflate recall by
// dropping those findings from the denominator.
func failedScore(c Case, reason string) CaseScore {
	sc := CaseScore{Name: c.Name, Reason: reason}
	for _, g := range c.Gold.Findings {
		sc.Missed = append(sc.Missed, g.ID)
	}
	return sc
}

// RunCase materializes c's repo and a work dir (both under workDir),
// dispatches one reviewer round through backend using the exact
// verifydeliver prompt/parse contract, and scores the result against c's
// gold. Screen is false on purpose: the "_screen" PreToolUse hook re-execs
// os.Executable(), which inside `go test` is the test binary, not jig; the
// eval repo is a throwaway temp dir with no remote, so screening buys
// nothing here.
//
// A failed dispatch or an invalid result.json fails the case with the
// error as its Reason rather than returning an error: only infrastructure
// trouble (workDir itself unusable) is returned as an error.
func RunCase(workDir string, c Case, backend session.Backend, model string) (CaseScore, error) {
	repoDir, base, head, err := materializeCaseRepo(workDir, c)
	if err != nil {
		return CaseScore{}, err
	}

	outDir := filepath.Join(workDir, "work")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: create work dir: %w", err)
	}

	briefData, err := os.ReadFile(c.BriefPath)
	if err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: read %s: %w", c.BriefPath, err)
	}
	briefPath := filepath.Join(outDir, "brief.md")
	if err := os.WriteFile(briefPath, briefData, 0o644); err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: write brief.md: %w", err)
	}
	slicesPath := filepath.Join(outDir, "slices.yaml")
	if err := os.WriteFile(slicesPath, []byte("slices: []\n"), 0o644); err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: write slices.yaml: %w", err)
	}
	journalPath := filepath.Join(outDir, "journal.ndjson")
	if err := os.WriteFile(journalPath, nil, 0o644); err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: write journal.ndjson: %w", err)
	}

	templateData, err := os.ReadFile(c.ReviewPath)
	if err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: read %s: %w", c.ReviewPath, err)
	}
	var req verifydeliver.ReviewRequest
	if err := json.Unmarshal(templateData, &req); err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: parse %s: %w", c.ReviewPath, err)
	}
	req.BaseSHA = base
	req.HeadSHA = head
	req.BriefPath = absPath(briefPath)
	req.SlicesPath = absPath(slicesPath)
	req.JournalPath = absPath(journalPath)

	reqData, err := verifydeliver.MarshalReviewRequest(req)
	if err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: marshal review.json: %w", err)
	}
	reviewPath := filepath.Join(outDir, "review.json")
	if err := os.WriteFile(reviewPath, reqData, 0o644); err != nil {
		return CaseScore{}, fmt.Errorf("revieweval: write review.json: %w", err)
	}
	resultPath := filepath.Join(outDir, "result.json")

	prompt := verifydeliver.RenderReviewPrompt(req, reviewPath, resultPath)
	dispatch := session.Dispatch{
		Ticket:     c.Name,
		Slice:      "gate",
		Attempt:    1,
		Worktree:   repoDir,
		SliceJSON:  reviewPath,
		ResultJSON: resultPath,
		Model:      model,
		Prompt:     prompt,
		Screen:     false,
	}
	if err := backend.Run(dispatch); err != nil {
		return failedScore(c, err.Error()), nil
	}

	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		return failedScore(c, fmt.Sprintf("no result.json written at %s", resultPath)), nil
	}
	result, err := verifydeliver.ParseReviewResult(resultData)
	if err != nil {
		return failedScore(c, err.Error()), nil
	}
	return scoreCase(c, result), nil
}

// RunCorpus runs every case in cases under its own subdirectory of
// workRoot and returns each case's score, in corpus order.
func RunCorpus(workRoot string, cases []Case, backend session.Backend, model string) ([]CaseScore, error) {
	scores := make([]CaseScore, 0, len(cases))
	for _, c := range cases {
		caseDir := filepath.Join(workRoot, c.Name)
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return nil, fmt.Errorf("revieweval: create case dir for %s: %w", c.Name, err)
		}
		score, err := RunCase(caseDir, c, backend, model)
		if err != nil {
			return nil, fmt.Errorf("revieweval: run case %s: %w", c.Name, err)
		}
		scores = append(scores, score)
	}
	return scores, nil
}
