package revieweval

import (
	"crypto/sha256"
	"encoding/hex"
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

// runID returns case name's opaque run id: "c-" plus the first 8 hex
// characters of a sha256 of the name. Stable (the same name always yields
// the same id) and reveals nothing about the name itself - this id, never
// the case name, is what the store ticket, the reviewer's own dispatch
// ticket (RoundInput.Ticket, which verifydeliver's review prompt embeds
// verbatim) and every path under a run's own work root are named after, so
// nothing the reviewer sees can prime it with which case this is
// (CaseScore.Name and the reports still carry the real name - a person
// reading a report needs it, a reviewer session reading its own dispatch
// must never see it).
func runID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "c-" + hex.EncodeToString(sum[:])[:8]
}

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

// scoredFindingsNoMatch builds RoundScore.Findings for a round that reached
// a live reviewer result but never finished matching (a judge error, or the
// judge changing the case repo): every reported finding, with jig's own
// status, but no gold match and no fate - matching never ran far enough to
// say either, and RenderJSON should still show what the reviewer said
// rather than nothing at all.
func scoredFindingsNoMatch(findings []verifydeliver.ResultFinding, reported []verifydeliver.Finding) []ScoredFinding {
	out := make([]ScoredFinding, 0, len(findings))
	for j, f := range findings {
		out = append(out, ScoredFinding{
			File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
			Action: f.Action, Risk: f.Risk, Prior: f.Prior, Status: reported[j].Status,
		})
	}
	return out
}

// failedRoundScore builds the RoundScore for a round whose reviewer result
// was live but whose matching never finished (a judge error, or the judge
// changing the case repo): every seeded gold finding counts as missed, the
// round is Failed with reason, and the reviewer's own findings are
// preserved (scoredFindingsNoMatch) so RenderJSON still shows what it
// said.
func failedRoundScore(n int, gold Gold, decisions []Decision, findings []verifydeliver.ResultFinding, reported []verifydeliver.Finding, reason string) RoundScore {
	rs := missedRoundScore(n, gold, decisions, reason)
	rs.Failed = true
	rs.Findings = scoredFindingsNoMatch(findings, reported)
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

// checkJudgeReadOnly reports whether the judge violated its read-only rule
// - repoDir's HEAD is no longer head, or a tracked file changed since -
// the same read-only guard the reviewer's own dispatch is held to
// (verifydeliver/review.go's "the reviewer changed the gate lease" check),
// extended to the judge's dispatch, since a judge session's Worktree is
// this same case repo. A git command itself failing is a different thing
// from the judge having changed anything, so it comes back as err
// (infrastructure, for the caller to return as a real error), never folded
// into violated - only an actual violation may fail the round with reason
// "the judge changed the case repo".
func checkJudgeReadOnly(repoDir, head string) (violated bool, err error) {
	headAfter, err := gitx.RevParse(repoDir, "HEAD")
	if err != nil {
		return false, fmt.Errorf("revieweval: check judge read-only: resolve HEAD: %w", err)
	}
	statusOut, err := gitx.Run(repoDir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, fmt.Errorf("revieweval: check judge read-only: git status: %w", err)
	}
	return headAfter != head || statusOut != "", nil
}

// restoreCaseRepo hard-resets repoDir to head and removes every untracked
// file and directory, the same restoration verifydeliver's own
// resetLeasePristine performs on the reviewer's lease - mirrored here
// rather than imported (that helper is package-private), since the next
// round's own patch apply must start from a pristine head whatever a judge
// dispatch left behind, tracked or not.
func restoreCaseRepo(repoDir, head string) error {
	if _, err := gitx.Run(repoDir, "reset", "--hard", head); err != nil {
		return fmt.Errorf("revieweval: restore case repo: reset --hard: %w", err)
	}
	if _, err := gitx.Run(repoDir, "clean", "-fd"); err != nil {
		return fmt.Errorf("revieweval: restore case repo: clean -fd: %w", err)
	}
	return nil
}

// initEvalStore builds workDir/store: project.yaml, and the ticket dir -
// named after id, the case's own opaque run id, never c.Name - holding
// a copy of its brief.md, an empty slices.yaml and an empty journal.ndjson,
// the minimum store.Open and verifydeliver's own paths (TicketDir,
// gate/round-N/...) need.
func initEvalStore(workDir string, c Case, id string) (*store.Store, error) {
	storeRoot := filepath.Join(workDir, "store")
	if err := os.MkdirAll(storeRoot, 0o755); err != nil {
		return nil, fmt.Errorf("revieweval: create store: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storeRoot, "project.yaml"), []byte("# revieweval store\n"), 0o644); err != nil {
		return nil, fmt.Errorf("revieweval: write project.yaml: %w", err)
	}

	ticketDir := filepath.Join(storeRoot, id)
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
	// The commit message names no run-record kind: nothing the reviewer or
	// judge inspects with `git log` in this repo may say this is an
	// evaluation, let alone which case.
	if _, err := gitx.RunEnv(repoDir, identityEnv, "commit", "-q", "-m", "base"); err != nil {
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
// store/<ticket>/gate/round-(N-1)/findings.yaml, and writes that round's
// report.yaml, so verifydeliver.FoldBefore for round N reads exactly the
// case's own recorded history - never this run's live result - the
// teacher-forcing the package doc describes. ticket is the case's own
// opaque run id, the same one initEvalStore filed brief.md, etc.
// under.
func seedRoundHistory(st *store.Store, ticket string, prev Round, prevHead string) error {
	gateDir := filepath.Join(st.TicketDir(ticket), "gate", fmt.Sprintf("round-%d", prev.N))
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

// caseWorkDir is <ticket>/work under the eval store: verifydeliver's own
// gateWorkDir convention (internal/verifydeliver/review.go, package-
// private there), reconstructed here rather than imported since it is a
// store-side path convention, not a call. Every review dispatch's
// review.json and result.json for a case live there, one pair per round,
// filenames never reused across rounds.
func caseWorkDir(st *store.Store, ticket string) string {
	return filepath.Join(st.TicketDir(ticket), "work")
}

// retireRoundWork deletes the case's store-side work dir and this round's
// own judge scratch dir, once round n is fully scored: the JSON
// report already keeps every finding a round produced, so nothing is
// lost, and deleting rather than archiving them means no later round's
// dispatch (reviewer or judge), and no later session poking around under
// the work root, can find an earlier round's live
// review.json/result.json/judge.json/verdicts.json anywhere - not merely
// moved aside - and read a result that disagrees with the case's own
// recorded history, teacher-forcing's whole point. A round with nothing
// dispatched yet (an early infrastructure error before any file was
// written) leaves nothing to delete, which is not an error. The store
// itself recreates the work dir on demand (os.MkdirAll before it writes
// review.json), so an empty store-side work dir for the next round is
// never a problem.
func retireRoundWork(st *store.Store, ticket string, n int, judgeWorkDir string) error {
	if err := os.RemoveAll(caseWorkDir(st, ticket)); err != nil {
		return fmt.Errorf("revieweval: delete case work dir for round %d: %w", n, err)
	}
	if err := os.RemoveAll(judgeWorkDir); err != nil {
		return fmt.Errorf("revieweval: delete round %d judge dir: %w", n, err)
	}
	return nil
}

// runRound runs c.Rounds[idx] (whose number is n) against the case's
// already-initialized store and repo: applies the round's patch, seeds the
// store with the previous round's recorded history (n > 1), folds,
// dispatches the reviewer, applies findings bookkeeping, matches and
// scores. ticket is the case's own opaque run id - the store ticket
// and RoundInput.Ticket - never c.Name, which stays for error text and
// MatchRound's own caseName parameter only. recordedLinks is every earlier
// round's decisions keyed by the finding id each one's "recorded" names;
// prevHead is the previous round's own head, "" for round 1.
//
// It always retires the round's store-side work dir and this round's own
// judge scratch dir before returning (deferred so every return path runs
// it), and, once a live result exists to match at all, always restores
// the case repo to this round's own head before returning, since a later
// round's git apply must never see anything a live reviewer or judge
// dispatch left behind.
func runRound(workDir string, st *store.Store, c Case, ticket string, idx int, repoDir, briefPath string, backend session.Backend, judge Judge, model, prevHead string, recordedLinks map[string]Decision) (rs RoundScore, head string, err error) {
	r := c.Rounds[idx]
	n := r.N

	absPatch, perr := filepath.Abs(r.PatchPath)
	if perr != nil {
		return RoundScore{}, "", fmt.Errorf("revieweval: case %s round %d: resolve patch path: %w", c.Name, n, perr)
	}
	if _, aerr := gitx.Run(repoDir, "apply", absPatch); aerr != nil {
		return RoundScore{}, "", fmt.Errorf("revieweval: case %s round %d: apply patch: %w", c.Name, n, aerr)
	}
	if _, aerr := gitx.Run(repoDir, "add", "-A"); aerr != nil {
		return RoundScore{}, "", fmt.Errorf("revieweval: case %s round %d: git add: %w", c.Name, n, aerr)
	}
	if _, cerr := gitx.RunEnv(repoDir, identityEnv, "commit", "-q", "-m", fmt.Sprintf("round %d", n)); cerr != nil {
		return RoundScore{}, "", fmt.Errorf("revieweval: case %s round %d: commit: %w", c.Name, n, cerr)
	}
	head, herr := gitx.RevParse(repoDir, "HEAD")
	if herr != nil {
		return RoundScore{}, "", fmt.Errorf("revieweval: case %s round %d: resolve head: %w", c.Name, n, herr)
	}

	judgeWorkDir := filepath.Join(workDir, "judge", fmt.Sprintf("round-%d", n))
	defer func() {
		if rerr := retireRoundWork(st, ticket, n, judgeWorkDir); rerr != nil && err == nil {
			err = fmt.Errorf("revieweval: case %s round %d: %w", c.Name, n, rerr)
		}
	}()

	if n > 1 {
		if serr := seedRoundHistory(st, ticket, c.Rounds[idx-1], prevHead); serr != nil {
			return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: %w", c.Name, n, serr)
		}
	}

	fold, ferr := verifydeliver.FoldBefore(st, ticket, n)
	if ferr != nil {
		return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: fold: %w", c.Name, n, ferr)
	}
	man, merr := manifest.Resolve(repoDir)
	if merr != nil {
		return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: resolve manifest: %w", c.Name, n, merr)
	}

	rnd, ok, rerr := verifydeliver.NewReviewerGateSource(backend).Round(verifydeliver.RoundInput{
		Store: st, Ticket: ticket, Round: n, LeaseDir: repoDir, RepoName: evalRepoName, Target: evalTarget,
		Model: model, BriefPath: briefPath, Manifest: man, Open: fold.Open, Dismissed: fold.Dismissed,
	})
	if rerr != nil {
		rs, handled := reviewerRoundFailure(n, r.Gold, r.Decisions, rerr)
		if !handled {
			return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: dispatch round: %w", c.Name, n, rerr)
		}
		return rs, head, nil
	}
	if !ok || rnd.Review == nil {
		return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: reviewer source returned no review content", c.Name, n)
	}
	result := rnd.Review.Result

	reported, aerr := verifydeliver.ApplyRound(n, fold.Known, result, nil, alwaysNotGreen, man)
	if aerr != nil {
		return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: apply round: %w", c.Name, n, aerr)
	}
	existsAtHead := func(file string) (bool, error) { return gitx.FileExistsAtRev(repoDir, head, file) }
	cleared, clerr := verifydeliver.ClearingAfterTriage(fold.Known, reported, result.ReviewedPaths, existsAtHead)
	if clerr != nil {
		return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: clearing: %w", c.Name, n, clerr)
	}

	match, matchErr := MatchRound(c.Name, n, repoDir, judgeWorkDir, r.Gold, r.Decisions, recordedLinks, dismissedFold(fold.Known), result.Findings, reported, judge)

	// Whatever MatchRound did, the judge (if any) dispatched a session
	// against this same case repo - check it changed nothing, then restore
	// it to this round's head regardless, so a later round's patch apply
	// never sees anything a live judge session left behind, tracked or not.
	// checkJudgeReadOnly's own error (a git command failing) is
	// infrastructure, returned as a real error, never scored as "the judge
	// changed the case repo" - only violated=true, a genuine
	// difference from head, is that.
	violated, roErr := checkJudgeReadOnly(repoDir, head)
	if restoreErr := restoreCaseRepo(repoDir, head); restoreErr != nil {
		return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: %w", c.Name, n, restoreErr)
	}
	if roErr != nil {
		return RoundScore{}, head, fmt.Errorf("revieweval: case %s round %d: %w", c.Name, n, roErr)
	}
	if violated {
		return failedRoundScore(n, r.Gold, r.Decisions, result.Findings, reported, "the judge changed the case repo"), head, nil
	}
	if matchErr != nil {
		return failedRoundScore(n, r.Gold, r.Decisions, result.Findings, reported, matchErr.Error()), head, nil
	}

	return ScoreRound(n, r.Gold, r.Decisions, result.Findings, reported, cleared, match), head, nil
}

// RunCase runs every round of c in order, under workDir: a store (once)
// and a repo (once), then per round (runRound), applies that round's
// patch, seeds the store with the previous round's recorded history,
// dispatches the real reviewer round, applies findings bookkeeping
// (verifydeliver.ApplyRound, verifydeliver.ClearingAfterTriage), and scores
// it (match.go, score.go).
//
// A round's own REVIEW_INVALID/REVIEW_FAILED result, or a judge failure (a
// judge error, or the judge changing the case repo), scores that round
// refused/failed (every seeded gold finding missed) but does not stop the
// case: teacher-forcing means the next round's fold comes from the case's
// recorded history, never from this run's own result, so it is
// unaffected. Any other error is infrastructure and is returned.
func RunCase(workDir string, c Case, backend session.Backend, judge Judge, model string) (CaseScore, error) {
	ticket := runID(c.Name)
	st, err := initEvalStore(workDir, c, ticket)
	if err != nil {
		return CaseScore{}, err
	}
	repoDir, _, err := initEvalRepo(workDir)
	if err != nil {
		return CaseScore{}, err
	}
	briefPath := filepath.Join(st.TicketDir(ticket), "brief.md")

	cs := CaseScore{Name: c.Name, Passed: true}
	var prevHead string
	recordedLinks := map[string]Decision{}
	for idx, r := range c.Rounds {
		rs, head, err := runRound(workDir, st, c, ticket, idx, repoDir, briefPath, backend, judge, model, prevHead, recordedLinks)
		if err != nil {
			return CaseScore{}, err
		}
		if !rs.Passed {
			cs.Passed = false
		}
		cs.Rounds = append(cs.Rounds, rs)
		for _, d := range r.Decisions {
			if d.Recorded != "" {
				recordedLinks[d.Recorded] = d
			}
		}
		prevHead = head
	}

	return cs, nil
}

// RunCorpus runs every case in cases under its own subdirectory of
// workRoot - named after the case's own opaque run id, never its
// name - and returns each case's score, in corpus order.
func RunCorpus(workRoot string, cases []Case, backend session.Backend, judge Judge, model string) ([]CaseScore, error) {
	scores := make([]CaseScore, 0, len(cases))
	for _, c := range cases {
		dir := filepath.Join(workRoot, runID(c.Name))
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
