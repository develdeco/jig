package verifydeliver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
	"gopkg.in/yaml.v3"
)

// Finding action vocabulary (design 4.2): who acts on a gate finding. fix
// means jig queues a fix slice, ask means a human decides, note means it is
// recorded only.
const (
	ActionFix  = "fix"
	ActionAsk  = "ask"
	ActionNote = "note"
)

// Finding risk vocabulary (design 4.2): how much harm follows if this part
// of the change is wrong.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// OpenFinding is one entry of review.json's "open" list: an earlier round's
// finding the reviewer needs to see again (design 4.1). Building this list
// from the cumulative findings state across rounds is findings bookkeeping
// (design 5); RoundInput carries it as an input here.
type OpenFinding struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	Action      string `json:"action"`
	Recurrences int    `json:"recurrences"`
}

// DismissedFinding is one entry of review.json's "dismissed" list: a
// finding the human already dismissed, so the reviewer never raises it
// again.
type DismissedFinding struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// ReviewRequest is review.json's exact wire shape (design 4.1), the input
// jig writes for one gate reviewer round.
type ReviewRequest struct {
	Ticket      string             `json:"ticket"`
	Round       int                `json:"round"`
	Scope       string             `json:"scope"` // full|delta
	BaseSHA     string             `json:"base_sha"`
	HeadSHA     string             `json:"head_sha"`
	BriefPath   string             `json:"brief_path"`
	SlicesPath  string             `json:"slices_path"`
	JournalPath string             `json:"journal_path"`
	Oracles     []string           `json:"oracles"`
	Open        []OpenFinding      `json:"open"`
	Dismissed   []DismissedFinding `json:"dismissed"`
	MustReview  []string           `json:"must_review"`
}

// ResultFinding is one finding as the reviewer session reports it in
// result.json (design 4.2), before jig assigns it an id (findings
// bookkeeping, design 5).
type ResultFinding struct {
	File          string `json:"file"`
	Line          int    `json:"line"`
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	Action        string `json:"action"`
	Risk          string `json:"risk"`
	RiskRationale string `json:"risk_rationale"`
	Oracle        string `json:"oracle"`
	Prior         string `json:"prior"`
}

// ReviewResult is result.json's exact wire shape (design 4.2), the
// reviewer session's output.
type ReviewResult struct {
	Findings      []ResultFinding `json:"findings"`
	ReviewedPaths []string        `json:"reviewed_paths"`
	Summary       string          `json:"summary"`
}

// MarshalReviewRequest renders req as indented JSON, with nil Oracles,
// Open, Dismissed and MustReview marshaled as [] rather than null.
func MarshalReviewRequest(req ReviewRequest) ([]byte, error) {
	if req.Oracles == nil {
		req.Oracles = []string{}
	}
	if req.Open == nil {
		req.Open = []OpenFinding{}
	}
	if req.Dismissed == nil {
		req.Dismissed = []DismissedFinding{}
	}
	if req.MustReview == nil {
		req.MustReview = []string{}
	}
	return json.MarshalIndent(req, "", "  ")
}

// reviewInvalid wraps msg as the *axi.Error ParseReviewResult and the
// reviewer round return for a malformed or out-of-contract result: the
// round fails loudly (design 4.4), never a silent default.
func reviewInvalid(msg string) error {
	return &axi.Error{Msg: "gate reviewer result.json is invalid: " + msg, Code: "REVIEW_INVALID"}
}

// ParseReviewResult parses result.json strictly and checks the rules that
// need no context beyond the result itself (design 4.4): exactly one JSON
// object, a known action and risk on every finding, non-empty title and
// risk_rationale, a non-negative line, and repo-relative file syntax (Q5).
// The rules that need the request or the lease's head - oracle membership,
// prior identity, reviewed_paths coverage, file existence - are checked
// separately by validateReviewResult, once the caller has that context.
// Errors are *axi.Error{Code: "REVIEW_INVALID"} naming the rule; nothing
// here is silently accepted.
func ParseReviewResult(data []byte) (ReviewResult, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var res ReviewResult
	if err := dec.Decode(&res); err != nil {
		return ReviewResult{}, reviewInvalid(fmt.Sprintf("not valid JSON: %v", err))
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return ReviewResult{}, reviewInvalid("must contain exactly one JSON object")
	}

	for i, f := range res.Findings {
		switch f.Action {
		case ActionFix, ActionAsk, ActionNote:
		default:
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("finding %d action %q is not %q, %q or %q", i, f.Action, ActionFix, ActionAsk, ActionNote))
		}
		switch f.Risk {
		case RiskLow, RiskMedium, RiskHigh:
		default:
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("finding %d risk %q is not %q, %q or %q", i, f.Risk, RiskLow, RiskMedium, RiskHigh))
		}
		if strings.TrimSpace(f.Title) == "" {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("finding %d has an empty title", i))
		}
		if strings.TrimSpace(f.RiskRationale) == "" {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("finding %d has an empty risk_rationale", i))
		}
		if f.Line < 0 {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("finding %d line %d is negative", i, f.Line))
		}
		if _, err := normalizeRepoRelPath(f.File); err != nil {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("finding %d file %q: %v", i, f.File, err))
		}
	}
	return res, nil
}

// normalizeRepoRelPath normalizes p for comparison (Q5): backslash to
// slash, a leading "./" stripped, then rejects an empty path, an absolute
// path (a leading "/" or a Windows drive letter), and any ".." segment.
func normalizeRepoRelPath(p string) (string, error) {
	q := strings.ReplaceAll(p, "\\", "/")
	q = strings.TrimPrefix(q, "./")
	if q == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(q, "/") {
		return "", fmt.Errorf("path %q is absolute", p)
	}
	if len(q) >= 2 && q[1] == ':' && ((q[0] >= 'A' && q[0] <= 'Z') || (q[0] >= 'a' && q[0] <= 'z')) {
		return "", fmt.Errorf("path %q is absolute", p)
	}
	for _, seg := range strings.Split(q, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path %q has a \"..\" segment", p)
		}
	}
	return q, nil
}

// validateReviewResult checks result against req and the lease's head, the
// rules ParseReviewResult cannot check on its own (design 4.4): a finding's
// file exists at head or was deleted in the scope diff; an oracle, when
// given, names a manifest oracle, and is required when the manifest has
// more than one and the finding is fix or ask (Q4, no silent fallback); a
// prior names an id under open or dismissed, and no two findings share one;
// reviewed_paths covers every must_review path. atHead reports whether a
// (normalized) path exists in the lease at head.
func validateReviewResult(req ReviewRequest, result ReviewResult, atHead func(path string) (bool, error), deleted map[string]bool, oracleNames []string) error {
	openIDs := make(map[string]bool, len(req.Open))
	for _, f := range req.Open {
		openIDs[f.ID] = true
	}
	dismissedIDs := make(map[string]bool, len(req.Dismissed))
	for _, f := range req.Dismissed {
		dismissedIDs[f.ID] = true
	}
	oracleSet := make(map[string]bool, len(oracleNames))
	for _, o := range oracleNames {
		oracleSet[o] = true
	}

	seenPrior := map[string]bool{}
	for i, f := range result.Findings {
		norm, err := normalizeRepoRelPath(f.File)
		if err != nil {
			// ParseReviewResult already rejects this; defensive only.
			return reviewInvalid(fmt.Sprintf("finding %d file %q: %v", i, f.File, err))
		}
		if !deleted[norm] {
			present, err := atHead(norm)
			if err != nil {
				return fmt.Errorf("verifydeliver: review: check %q at head: %w", norm, err)
			}
			if !present {
				return reviewInvalid(fmt.Sprintf("finding %d file %q is neither present at head nor deleted in the scope diff", i, f.File))
			}
		}

		if f.Oracle != "" {
			if !oracleSet[f.Oracle] {
				return reviewInvalid(fmt.Sprintf("finding %d oracle %q is not a manifest oracle", i, f.Oracle))
			}
		} else if len(oracleNames) > 1 && (f.Action == ActionFix || f.Action == ActionAsk) {
			return reviewInvalid(fmt.Sprintf("finding %d (action %q) must name one of the manifest's %d oracles", i, f.Action, len(oracleNames)))
		}

		if f.Prior != "" {
			if !openIDs[f.Prior] && !dismissedIDs[f.Prior] {
				return reviewInvalid(fmt.Sprintf("finding %d prior %q does not name a finding under open or dismissed", i, f.Prior))
			}
			if seenPrior[f.Prior] {
				return reviewInvalid(fmt.Sprintf("two findings share prior %q", f.Prior))
			}
			seenPrior[f.Prior] = true
		}
	}

	reviewed := map[string]bool{}
	for _, p := range result.ReviewedPaths {
		norm, err := normalizeRepoRelPath(p)
		if err != nil {
			return reviewInvalid(fmt.Sprintf("reviewed_paths entry %q: %v", p, err))
		}
		reviewed[norm] = true
	}
	for _, mr := range req.MustReview {
		if !reviewed[mr] {
			return reviewInvalid(fmt.Sprintf("reviewed_paths is missing must_review path %q", mr))
		}
	}
	return nil
}

// reviewPromptTemplate is the exact prompt rendered (via fmt.Sprintf) for
// every gate reviewer dispatch (design 4.3): it states the job and the
// output contract, and says what jig will verify. It never lists kinds of
// problems, coaches behavior, or patches a past model mistake.
const reviewPromptTemplate = `You are reviewing round %d of ticket %s. Your inputs are in review.json at %s.
Review the %s diff %s..%s in this worktree against the brief; the brief says what was asked for. Do not edit files, commit, or push.
Report every problem you find in the files you review, as they are now, including problems already listed as open. For each, give file, line (0 if unknown), title, detail, action, risk, risk_rationale and oracle, plus prior when it is a finding listed under open or dismissed. The human dismissed the findings listed under dismissed.
action: "fix" when the fix is objective and does not change what the brief asks for; "ask" when resolving it needs a decision only the human can make; "note" when nothing needs to change but a human reviewer should know it.
risk: "low", "medium" or "high": how much harm follows if this part of the change is wrong.
oracle: the manifest oracle from review.json that best proves the fix.
reviewed_paths: every file you read. jig rejects a result that does not include every path in must_review.
When finished, write result.json at %s with exactly one JSON object: %s`

// reviewResultSchema is the {schema} filled into reviewPromptTemplate: the
// literal shape of one result.json.
const reviewResultSchema = `{"findings": [{"file": "...", "line": 0, "title": "...", "detail": "...", "action": "fix|ask|note", "risk": "low|medium|high", "risk_rationale": "...", "oracle": "...", "prior": "r1-f2"}], "reviewed_paths": ["..."], "summary": "..."}`

// RenderReviewPrompt fills reviewPromptTemplate for one review dispatch.
func RenderReviewPrompt(req ReviewRequest, reviewPath, resultPath string) string {
	return fmt.Sprintf(reviewPromptTemplate, req.Round, req.Ticket, reviewPath, req.Scope, req.BaseSHA, req.HeadSHA, resultPath, reviewResultSchema)
}

// gateWorkDir is <ticket>/work under the store, mirroring frontier's own
// work/ contract (built independently here: verifydeliver never imports
// frontier).
func gateWorkDir(st *store.Store, ticket string) string {
	return filepath.Join(st.TicketDir(ticket), "work")
}

// reviewJSONPath and reviewResultJSONPath are one round's review dispatch
// input/output paths, store-side.
func reviewJSONPath(st *store.Store, ticket string, n int) string {
	return filepath.Join(gateWorkDir(st, ticket), fmt.Sprintf("gate.round-%d.review.json", n))
}

func reviewResultJSONPath(st *store.Store, ticket string, n int) string {
	return filepath.Join(gateWorkDir(st, ticket), fmt.Sprintf("gate.round-%d.result.json", n))
}

// absPath returns p resolved to an absolute path, or p itself when that
// fails (matching frontier's resolveBriefSections fallback).
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// sortedOracleNames returns man's oracle names in sorted order.
func sortedOracleNames(man manifest.Manifest) []string {
	names := make([]string, 0, len(man.Oracles))
	for name := range man.Oracles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// priorReviewedSHA reads round-n's report.yaml and returns its
// reviewed_sha[repoName], or "" when the round has no report.yaml (a
// scripted round, or one that hasn't happened yet) or no reviewed_sha for
// repoName.
func priorReviewedSHA(st *store.Store, ticket string, round int, repoName string) (string, error) {
	data, err := os.ReadFile(filepath.Join(gateRoundDir(st, ticket, round), "report.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var rep reportYAML
	if err := yaml.Unmarshal(data, &rep); err != nil {
		return "", err
	}
	return rep.ReviewedSHA[repoName], nil
}

// resolveScopeBase resolves one round's scope and base sha (design 4.1,
// Q5): delta from the previous reviewer round's reviewed_sha when it is an
// ancestor of head, else full anchored at merge-base(origin/target, HEAD),
// falling back to the ticket's recorded start sha only when the merge-base
// call itself fails (e.g. no such ref).
func resolveScopeBase(st *store.Store, ticket, leaseDir, repoName, target string, round int, head string) (scope, base string, err error) {
	if round > 1 {
		prior, perr := priorReviewedSHA(st, ticket, round-1, repoName)
		if perr != nil {
			return "", "", fmt.Errorf("verifydeliver: review: read round %d report.yaml: %w", round-1, perr)
		}
		if prior != "" {
			if isAnc, ancErr := gitx.IsAncestor(leaseDir, prior, head); ancErr == nil && isAnc {
				return "delta", prior, nil
			}
		}
	}

	mergeBase, mbErr := gitx.MergeBase(leaseDir, "origin/"+target, head)
	if mbErr == nil {
		return "full", mergeBase, nil
	}

	startPath := filepath.Join(st.TicketDir(ticket), fmt.Sprintf("start.%s.sha", repoName))
	data, rerr := os.ReadFile(startPath)
	if rerr != nil {
		return "", "", fmt.Errorf("verifydeliver: review: resolve scope base: merge-base origin/%s failed (%v) and no start sha: %w", target, mbErr, rerr)
	}
	return "full", strings.TrimSpace(string(data)), nil
}

// scopeDiff is the scope diff's coverage lists (design 4.1, Q5): Changed is
// every file base..head adds, modifies or type-changes (a rename counts as
// its new path); Deleted is every file it removes; MustReview is their
// sorted, deduplicated union with the files of open findings that still
// exist at head.
type scopeDiff struct {
	Changed    []string
	Deleted    []string
	MustReview []string
}

// computeScopeDiff runs the scope diff and builds must_review (Q5). openFiles
// are the open-finding files findings bookkeeping (design 5, S2) supplies;
// S1 has no bookkeeping yet, so callers before S2 pass nil.
func computeScopeDiff(leaseDir, base, head string, openFiles []string) (scopeDiff, error) {
	changed, err := gitx.DiffNameOnly(leaseDir, base, head, "AMT")
	if err != nil {
		return scopeDiff{}, fmt.Errorf("verifydeliver: review: diff changed files: %w", err)
	}
	deleted, err := gitx.DiffNameOnly(leaseDir, base, head, "D")
	if err != nil {
		return scopeDiff{}, fmt.Errorf("verifydeliver: review: diff deleted files: %w", err)
	}
	sort.Strings(changed)
	sort.Strings(deleted)

	mustReview := map[string]bool{}
	for _, f := range changed {
		mustReview[f] = true
	}
	for _, f := range openFiles {
		norm, err := normalizeRepoRelPath(f)
		if err != nil {
			continue // an open finding's file was already validated when recorded
		}
		exists, err := gitx.FileExistsAtRev(leaseDir, head, norm)
		if err != nil {
			return scopeDiff{}, fmt.Errorf("verifydeliver: review: check open finding file %q at head: %w", f, err)
		}
		if exists {
			mustReview[norm] = true
		}
	}
	out := make([]string, 0, len(mustReview))
	for f := range mustReview {
		out = append(out, f)
	}
	sort.Strings(out)

	return scopeDiff{Changed: changed, Deleted: deleted, MustReview: out}, nil
}

// Review is one validated reviewer round's content: Round.Review carries it
// so a later stage's findings bookkeeping (design 5, S2) can apply it
// without recomputing scope.
type Review struct {
	Scope      string
	BaseSHA    string
	HeadSHA    string
	Changed    []string
	Deleted    []string
	MustReview []string
	Result     ReviewResult
}

// RoundInput is what Gate hands a GateSource for one round. Round(n int)
// from PR #8 grows into this shape so the scripted source's exact behavior
// stays byte for byte (it reads only Round) while the reviewer source has
// what it needs to build review.json and dispatch.
type RoundInput struct {
	Store     *store.Store
	Ticket    string
	Round     int
	LeaseDir  string
	RepoName  string
	Target    string
	Model     string
	BriefPath string
	Manifest  manifest.Manifest
	Open      []OpenFinding      // findings bookkeeping (S2) supplies this; nil until then
	Dismissed []DismissedFinding // findings bookkeeping (S2) supplies this; nil until then
}

// reviewerGateSource dispatches a real, session-driven gate review for
// each round.
type reviewerGateSource struct {
	backend session.Backend
}

// NewReviewerGateSource returns a GateSource that dispatches a real
// reviewer session via b for each round.
func NewReviewerGateSource(b session.Backend) GateSource {
	return &reviewerGateSource{backend: b}
}

// Round writes review.json, dispatches the reviewer session, and validates
// its result.json strictly (design 4). It never routes findings into fix
// slices or applies bookkeeping across rounds (design 5); that is S2/S3's
// job. The lease is restored pristine before dispatch (oracles run just
// before this in Gate, and may have left tracked dirt) and always after,
// success or failure, so a broken reviewer never leaves the lease for a
// later operation to trip over.
func (r *reviewerGateSource) Round(in RoundInput) (rnd Round, ok bool, err error) {
	if err := resetLeasePristine(in.LeaseDir, "HEAD"); err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: restore lease before round: %w", err)
	}

	head, err := gitx.RevParse(in.LeaseDir, "HEAD")
	if err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: resolve HEAD: %w", err)
	}
	// The post-round restore always targets this captured sha, never the
	// literal ref "HEAD": a reviewer that committed during dispatch (which
	// it must never do) moves HEAD itself, so resetting to "HEAD" would be
	// a no-op that leaves the rogue commit in place instead of undoing it.
	defer func() {
		if rerr := resetLeasePristine(in.LeaseDir, head); rerr != nil && err == nil {
			err = fmt.Errorf("verifydeliver: review: restore lease after round: %w", rerr)
			rnd, ok = Round{}, false
		}
	}()

	scope, base, err := resolveScopeBase(in.Store, in.Ticket, in.LeaseDir, in.RepoName, in.Target, in.Round, head)
	if err != nil {
		return Round{}, false, err
	}
	var openFiles []string
	for _, f := range in.Open {
		openFiles = append(openFiles, f.File)
	}
	diff, err := computeScopeDiff(in.LeaseDir, base, head, openFiles)
	if err != nil {
		return Round{}, false, err
	}

	oracleNames := sortedOracleNames(in.Manifest)
	req := ReviewRequest{
		Ticket:      in.Ticket,
		Round:       in.Round,
		Scope:       scope,
		BaseSHA:     base,
		HeadSHA:     head,
		BriefPath:   absPath(in.BriefPath),
		SlicesPath:  absPath(filepath.Join(in.Store.TicketDir(in.Ticket), "slices.yaml")),
		JournalPath: absPath(filepath.Join(in.Store.TicketDir(in.Ticket), "journal.ndjson")),
		Oracles:     oracleNames,
		Open:        in.Open,
		Dismissed:   in.Dismissed,
		MustReview:  diff.MustReview,
	}

	reviewPath := reviewJSONPath(in.Store, in.Ticket, in.Round)
	resultPath := reviewResultJSONPath(in.Store, in.Ticket, in.Round)
	reqData, err := MarshalReviewRequest(req)
	if err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: marshal review.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(reviewPath), 0o755); err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: create work dir: %w", err)
	}
	if err := os.WriteFile(reviewPath, reqData, 0o644); err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: write review.json: %w", err)
	}
	// A failed earlier attempt at this round must never be read back as
	// this attempt's result.
	if err := os.Remove(resultPath); err != nil && !os.IsNotExist(err) {
		return Round{}, false, fmt.Errorf("verifydeliver: review: remove stale result.json: %w", err)
	}

	prompt := RenderReviewPrompt(req, reviewPath, resultPath)
	dispatch := session.Dispatch{
		Ticket:     in.Ticket,
		Slice:      "gate",
		Attempt:    in.Round,
		Worktree:   in.LeaseDir,
		SliceJSON:  reviewPath,
		ResultJSON: resultPath,
		Model:      in.Model,
		Prompt:     prompt,
		Screen:     true,
	}
	if err := r.backend.Run(dispatch); err != nil {
		return Round{}, false, &axi.Error{
			Msg:  fmt.Sprintf("gate reviewer dispatch failed: %v", err),
			Code: "REVIEW_FAILED",
		}
	}
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		return Round{}, false, &axi.Error{
			Msg:  fmt.Sprintf("gate reviewer wrote no result.json at %s", resultPath),
			Code: "REVIEW_FAILED",
		}
	}

	// The read-only guard (design 3): a reviewer edits nothing. HEAD must
	// still be head, and every tracked file must be as it was.
	headAfter, err := gitx.RevParse(in.LeaseDir, "HEAD")
	if err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: resolve HEAD after dispatch: %w", err)
	}
	statusOut, err := gitx.Run(in.LeaseDir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: git status after dispatch: %w", err)
	}
	if headAfter != head || statusOut != "" {
		return Round{}, false, &axi.Error{
			Msg:  "the reviewer changed the gate lease; reviewers never edit",
			Code: "REVIEW_INVALID",
		}
	}

	result, err := ParseReviewResult(resultData)
	if err != nil {
		return Round{}, false, err
	}

	deletedSet := make(map[string]bool, len(diff.Deleted))
	for _, f := range diff.Deleted {
		deletedSet[f] = true
	}
	atHead := func(path string) (bool, error) {
		return gitx.FileExistsAtRev(in.LeaseDir, head, path)
	}
	if err := validateReviewResult(req, result, atHead, deletedSet, oracleNames); err != nil {
		return Round{}, false, err
	}

	return Round{Review: &Review{
		Scope:      scope,
		BaseSHA:    base,
		HeadSHA:    head,
		Changed:    diff.Changed,
		Deleted:    diff.Deleted,
		MustReview: diff.MustReview,
		Result:     result,
	}}, true, nil
}
