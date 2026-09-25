package revieweval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
	"gopkg.in/yaml.v3"
)

// identityEnv pins the git author/committer identity and date for every
// commit this package makes, so an eval repo's shas are stable across runs
// of the same corpus.
var identityEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// baseManifestYAML is the eval repo's base commit .claude/jig.yaml: one
// root workspace and one oracle, so every case's manifest resolves the
// same way whatever its diff touches - the base commit holds no go.mod, so
// without a declared manifest, verifydeliver would see no oracle at all.
const baseManifestYAML = "workspaces:\n  - id: root\n    path: .\noracles:\n  test: go test ./...\n"

// evalRepoName and evalTarget name the eval repo the way RoundInput and
// report.yaml's reviewed_sha need: a repo id and its target branch.
const (
	evalRepoName = "eval"
	evalTarget   = "main"
)

// alwaysNotGreen is ApplyRound's sliceGreen: this package never builds a
// fix slice, so no existing slice is ever green, and every re-report is
// scored as a fresh occurrence rather than a recurrence that a finished
// (but unresolving) fix slice would explain.
func alwaysNotGreen(string) (bool, error) { return false, nil }

// dismissedFold returns known's dismissed findings, sorted by id for
// determinism: match.go's fourth candidate source.
func dismissedFold(known map[string]verifydeliver.Finding) []verifydeliver.Finding {
	var out []verifydeliver.Finding
	for _, f := range known {
		if f.Status == verifydeliver.StatusDismissed {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// missedRoundScore builds the RoundScore for a round that never produced a
// scoreable result: every seeded gold finding counts as missed, never left
// out of the denominator.
func missedRoundScore(n int, gold Gold, decisions []Decision, reason string) RoundScore {
	rs := RoundScore{Round: n, Reason: reason, FalsePositiveGold: falsePositiveGold(gold, decisions)}
	for _, g := range gold.Findings {
		rs.Missed = append(rs.Missed, g.ID)
	}
	return rs
}

// reviewerRoundFailure turns a Round() error into a refused or failed
// RoundScore when it is a REVIEW_INVALID/REVIEW_FAILED *axi.Error, or
// reports ok=false for any other (infrastructure) error, which the caller
// returns instead of scoring.
func reviewerRoundFailure(n int, gold Gold, decisions []Decision, err error) (RoundScore, bool) {
	var ae *axi.Error
	if !errors.As(err, &ae) {
		return RoundScore{}, false
	}
	rs := missedRoundScore(n, gold, decisions, ae.Msg)
	switch ae.Code {
	case "REVIEW_INVALID":
		rs.Refused = true
	case "REVIEW_FAILED":
		rs.Failed = true
	default:
		return RoundScore{}, false
	}
	return rs, true
}

// initEvalStore builds workDir/store: project.yaml, and the ticket dir
// (named after the case) holding a copy of its brief.md, an empty
// slices.yaml and an empty journal.ndjson - the minimum store.Open and
// verifydeliver's own paths (TicketDir, gate/round-N/...) need.
func initEvalStore(workDir string, c Case) (*store.Store, error) {
	storeRoot := filepath.Join(workDir, "store")
	if err := os.MkdirAll(storeRoot, 0o755); err != nil {
		return nil, fmt.Errorf("revieweval: create store: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storeRoot, "project.yaml"), []byte("# revieweval store\n"), 0o644); err != nil {
		return nil, fmt.Errorf("revieweval: write project.yaml: %w", err)
	}

	ticketDir := filepath.Join(storeRoot, c.Name)
	if err := os.MkdirAll(ticketDir, 0o755); err != nil {
		return nil, fmt.Errorf("revieweval: create ticket dir: %w", err)
	}
	briefData, err := os.ReadFile(c.BriefPath)
	if err != nil {
		return nil, fmt.Errorf("revieweval: read %s: %w", c.BriefPath, err)
	}
	if err := os.WriteFile(filepath.Join(ticketDir, "brief.md"), briefData, 0o644); err != nil {
		return nil, fmt.Errorf("revieweval: write brief.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(ticketDir, "slices.yaml"), []byte("slices: []\n"), 0o644); err != nil {
		return nil, fmt.Errorf("revieweval: write slices.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(ticketDir, "journal.ndjson"), nil, 0o644); err != nil {
		return nil, fmt.Errorf("revieweval: write journal.ndjson: %w", err)
	}

	return store.Open(storeRoot)
}

// initEvalRepo builds workDir/repo: an empty git repo whose base commit
// holds only .claude/jig.yaml, with origin/main pointed at that same base
// so round 1's scope resolves as a full diff from it (verifydeliver's own
// resolveScopeBase).
func initEvalRepo(workDir string) (repoDir, base string, err error) {
	repoDir = filepath.Join(workDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return "", "", fmt.Errorf("revieweval: create repo dir: %w", err)
	}
	if _, err := gitx.Run(repoDir, "init", "-q", "-b", "main"); err != nil {
		return "", "", fmt.Errorf("revieweval: git init: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, ".claude"), 0o755); err != nil {
		return "", "", fmt.Errorf("revieweval: create .claude: %w", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".claude", "jig.yaml"), []byte(baseManifestYAML), 0o644); err != nil {
		return "", "", fmt.Errorf("revieweval: write jig.yaml: %w", err)
	}
	if _, err := gitx.Run(repoDir, "add", "-A"); err != nil {
		return "", "", fmt.Errorf("revieweval: git add base: %w", err)
	}
	if _, err := gitx.RunEnv(repoDir, identityEnv, "commit", "-q", "-m", "revieweval: base"); err != nil {
		return "", "", fmt.Errorf("revieweval: base commit: %w", err)
	}
	base, err = gitx.RevParse(repoDir, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("revieweval: resolve base sha: %w", err)
	}
	if _, err := gitx.Run(repoDir, "update-ref", "refs/remotes/origin/main", base); err != nil {
		return "", "", fmt.Errorf("revieweval: set origin/main: %w", err)
	}
	return repoDir, base, nil
}

// seedRoundHistory copies prev's recorded findings.yaml (round N-1's own
// ground truth, what the case says jig recorded) into
// store/<case>/gate/round-(N-1)/findings.yaml, and writes that round's
// report.yaml, so verifydeliver.FoldBefore for round N reads exactly the
// case's own recorded history - never this run's live result - the
// teacher-forcing the package doc describes.
func seedRoundHistory(st *store.Store, caseName string, prev Round, prevHead string) error {
	gateDir := filepath.Join(st.TicketDir(caseName), "gate", fmt.Sprintf("round-%d", prev.N))
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		return fmt.Errorf("revieweval: create round %d gate dir: %w", prev.N, err)
	}
	data, err := os.ReadFile(prev.FindingsPath)
	if err != nil {
		return fmt.Errorf("revieweval: read round %d findings.yaml: %w", prev.N, err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "findings.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("revieweval: write round %d findings.yaml: %w", prev.N, err)
	}

	report := struct {
		Round       int               `yaml:"round"`
		ReviewedSHA map[string]string `yaml:"reviewed_sha"`
	}{Round: prev.N, ReviewedSHA: map[string]string{evalRepoName: prevHead}}
	reportData, err := yaml.Marshal(report)
	if err != nil {
		return fmt.Errorf("revieweval: marshal round %d report.yaml: %w", prev.N, err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "report.yaml"), reportData, 0o644); err != nil {
		return fmt.Errorf("revieweval: write round %d report.yaml: %w", prev.N, err)
	}
	return nil
}

// RunCase runs every round of c in order, under workDir: a store (once)
// and a repo (once), then per round, applies that round's patch, seeds the
// store with the previous round's recorded history, dispatches the real
// reviewer round, applies findings bookkeeping (verifydeliver.ApplyRound,
// verifydeliver.ClearingAfterTriage), and scores it (match.go, score.go).
//
// A round's own REVIEW_INVALID/REVIEW_FAILED result scores that round
// refused/failed (every seeded gold finding missed) but does not stop the
// case: teacher-forcing means the next round's fold comes from the case's
// recorded history, never from this run's own result, so it is unaffected.
// Any other error is infrastructure and is returned.
func RunCase(workDir string, c Case, backend session.Backend, judge Judge, model string) (CaseScore, error) {
	st, err := initEvalStore(workDir, c)
	if err != nil {
		return CaseScore{}, err
	}
	repoDir, _, err := initEvalRepo(workDir)
	if err != nil {
		return CaseScore{}, err
	}
	briefPath := filepath.Join(st.TicketDir(c.Name), "brief.md")

	cs := CaseScore{Name: c.Name, Passed: true}
	var prevHead string
	for idx, r := range c.Rounds {
		n := r.N

		absPatch, err := filepath.Abs(r.PatchPath)
		if err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: resolve patch path: %w", c.Name, n, err)
		}
		if _, err := gitx.Run(repoDir, "apply", absPatch); err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: apply patch: %w", c.Name, n, err)
		}
		if _, err := gitx.Run(repoDir, "add", "-A"); err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: git add: %w", c.Name, n, err)
		}
		if _, err := gitx.RunEnv(repoDir, identityEnv, "commit", "-q", "-m", fmt.Sprintf("revieweval: round %d", n)); err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: commit: %w", c.Name, n, err)
		}
		head, err := gitx.RevParse(repoDir, "HEAD")
		if err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: resolve head: %w", c.Name, n, err)
		}

		if n > 1 {
			if err := seedRoundHistory(st, c.Name, c.Rounds[idx-1], prevHead); err != nil {
				return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: %w", c.Name, n, err)
			}
		}

		fold, err := verifydeliver.FoldBefore(st, c.Name, n)
		if err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: fold: %w", c.Name, n, err)
		}
		man, err := manifest.Resolve(repoDir)
		if err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: resolve manifest: %w", c.Name, n, err)
		}

		rnd, ok, rerr := verifydeliver.NewReviewerGateSource(backend).Round(verifydeliver.RoundInput{
			Store: st, Ticket: c.Name, Round: n, LeaseDir: repoDir, RepoName: evalRepoName, Target: evalTarget,
			Model: model, BriefPath: briefPath, Manifest: man, Open: fold.Open, Dismissed: fold.Dismissed,
		})
		if rerr != nil {
			rs, handled := reviewerRoundFailure(n, r.Gold, r.Decisions, rerr)
			if !handled {
				return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: dispatch round: %w", c.Name, n, rerr)
			}
			cs.Rounds = append(cs.Rounds, rs)
			cs.Passed = false
			prevHead = head
			continue
		}
		if !ok || rnd.Review == nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: reviewer source returned no review content", c.Name, n)
		}
		result := rnd.Review.Result

		reported, err := verifydeliver.ApplyRound(n, fold.Known, result, nil, alwaysNotGreen, man)
		if err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: apply round: %w", c.Name, n, err)
		}
		existsAtHead := func(file string) (bool, error) { return gitx.FileExistsAtRev(repoDir, head, file) }
		cleared, err := verifydeliver.ClearingAfterTriage(fold.Known, reported, result.ReviewedPaths, existsAtHead)
		if err != nil {
			return CaseScore{}, fmt.Errorf("revieweval: case %s round %d: clearing: %w", c.Name, n, err)
		}

		judgeWorkDir := filepath.Join(workDir, "judge", fmt.Sprintf("round-%d", n))
		match, err := MatchRound(c.Name, n, repoDir, judgeWorkDir, r.Gold, r.Decisions, dismissedFold(fold.Known), result.Findings, reported, judge)
		if err != nil {
			rs := missedRoundScore(n, r.Gold, r.Decisions, err.Error())
			rs.Failed = true
			cs.Rounds = append(cs.Rounds, rs)
			cs.Passed = false
			prevHead = head
			continue
		}

		rs := ScoreRound(n, r.Gold, r.Decisions, result.Findings, reported, cleared, match)
		if !rs.Passed {
			cs.Passed = false
		}
		cs.Rounds = append(cs.Rounds, rs)
		prevHead = head
	}

	return cs, nil
}

// RunCorpus runs every case in cases under its own subdirectory of
// workRoot and returns each case's score, in corpus order.
func RunCorpus(workRoot string, cases []Case, backend session.Backend, judge Judge, model string) ([]CaseScore, error) {
	scores := make([]CaseScore, 0, len(cases))
	for _, c := range cases {
		dir := filepath.Join(workRoot, c.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("revieweval: create work dir for %s: %w", c.Name, err)
		}
		sc, err := RunCase(dir, c, backend, judge, model)
		if err != nil {
			return nil, fmt.Errorf("revieweval: run case %s: %w", c.Name, err)
		}
		scores = append(scores, sc)
	}
	return scores, nil
}
