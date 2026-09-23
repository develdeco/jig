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

// Finding action vocabulary: who acts on a gate finding. fix means jig
// queues a fix slice, ask means a human decides, note means it is recorded
// only.
const (
	ActionFix  = "fix"
	ActionAsk  = "ask"
	ActionNote = "note"
)

// Finding risk vocabulary: how much harm follows if this part of the change
// is wrong.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// OpenFinding is one entry of review.json's "open" list: an earlier round's
// finding the reviewer needs to see again. Building this list from the
// cumulative findings state across rounds is findings bookkeeping;
// RoundInput carries it as an input here.
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

// ReviewRequest is review.json's exact wire shape, the input jig writes
// for one gate reviewer round.
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
// result.json, before jig assigns it an id (findings bookkeeping).
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

// ReviewResult is result.json's exact wire shape, the reviewer session's
// output.
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

// reviewInvalidHelp is REVIEW_INVALID and REVIEW_FAILED's shared help line:
// the round already committed and pushed its own gate-open journal line
// (Gate's best-effort push on any error after that line), so a plain rerun
// works without any manual store cleanup.
var reviewInvalidHelp = []string{"Fix the reviewer result (or the backend) and rerun `jig gate` for this ticket."}

// reviewInvalid wraps msg as the *axi.Error ParseReviewResult and the
// reviewer round return for a malformed or out-of-contract result: the
// round fails loudly, never a silent default.
func reviewInvalid(msg string) error {
	return &axi.Error{Msg: "gate reviewer result.json is invalid: " + msg, Code: "REVIEW_INVALID", Help: reviewInvalidHelp}
}

// reviewResultWire is ParseReviewResult's strict decode target, used only
// once key presence, non-null and no-duplicate checks below have already
// passed for "findings" and "reviewed_paths": both keys must be present,
// and a JSON null does not count as the empty list it requires - there is
// nothing for jig to fall back to when a reviewer session simply never
// wrote either key, or wrote one as null instead of a real list; summary
// stays optional.
type reviewResultWire struct {
	Findings      []ResultFinding `json:"findings"`
	ReviewedPaths []string        `json:"reviewed_paths"`
	Summary       string          `json:"summary"`
}

// ParseReviewResult parses result.json strictly and checks the rules that
// need no context beyond the result itself: exactly one JSON object (a
// bare JSON null, or any other non-object top level, is
// rejected), no key repeated - exactly or only by case - anywhere in the
// document, every key at the top level and inside each finding an exact,
// case-sensitive match of a recognized field name (a lone case variant with
// no duplicate to catch, such as "Oracle" with no plain "oracle" beside it,
// is rejected the same as an outright unknown key - encoding/json's own
// struct decode below would otherwise match it case-insensitively and
// accept it silently), the "findings" and "reviewed_paths" keys present and
// not JSON null, a known action and risk on every finding, non-empty title
// and risk_rationale, a non-negative line, and repo-relative file syntax.
// The rules that need the request or the lease's head - oracle membership,
// prior identity, reviewed_paths coverage, file existence - are checked
// separately by validateReviewResult, once the caller has that context.
// Errors are *axi.Error{Code: "REVIEW_INVALID"} naming the rule; nothing
// here is silently accepted.
func ParseReviewResult(data []byte) (ReviewResult, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ReviewResult{}, reviewInvalid("must contain exactly one JSON object")
	}

	// encoding/json's struct decode below matches an object key to a field
	// case-insensitively, and when two keys in the same object both match
	// one field (an exact duplicate, or a case variant such as "FINDINGS"
	// alongside "findings"), the last one silently overwrites the others'
	// value with no error. A token-level walk of the whole document, top
	// level and every finding, catches this before that decode ever runs.
	if dup, derr := duplicateObjectKey(data); derr != nil {
		return ReviewResult{}, reviewInvalid(fmt.Sprintf("not valid JSON: %v", derr))
	} else if dup != "" {
		return ReviewResult{}, reviewInvalid(fmt.Sprintf("key %q repeats an earlier key in the same object", dup))
	}

	// encoding/json's struct decode also matches an object key to a field
	// case-insensitively when no exact match exists, so a lone case variant
	// with no duplicate to catch (an "Oracle" with no plain "oracle" beside
	// it) would otherwise decode silently instead of being rejected as
	// unknown. This is a separate check from the duplicate scan above: it
	// requires every key, at the top level and inside each finding, to match
	// one of reviewResultWire's or ResultFinding's own JSON tags exactly.
	if bad, kerr := unknownCaseVariantKey(data); kerr == nil && bad != "" {
		return ReviewResult{}, reviewInvalid(fmt.Sprintf("key %q is not a recognized field", bad))
	}

	// Key presence and nullness are checked separately from the strict,
	// typed decode below, which cannot tell "the key was never written" or
	// "written as null" apart from "written as an empty list" once decoded
	// into a plain slice.
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return ReviewResult{}, reviewInvalid(fmt.Sprintf("not valid JSON: %v", err))
	}
	for _, key := range []string{"findings", "reviewed_paths"} {
		raw, ok := present[key]
		if !ok {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("missing %q", key))
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("%q must be a list, not null", key))
		}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var wire reviewResultWire
	if err := dec.Decode(&wire); err != nil {
		return ReviewResult{}, reviewInvalid(fmt.Sprintf("not valid JSON: %v", err))
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return ReviewResult{}, reviewInvalid("must contain exactly one JSON object")
	}
	res := ReviewResult{Findings: wire.Findings, ReviewedPaths: wire.ReviewedPaths, Summary: wire.Summary}

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

// resultTopLevelKeys and resultFindingKeys are the exact key names
// reviewResultWire and ResultFinding's own JSON tags declare - the "exact
// key names are required" half of strict parsing.
var resultTopLevelKeys = map[string]bool{"findings": true, "reviewed_paths": true, "summary": true}
var resultFindingKeys = map[string]bool{
	"file": true, "line": true, "title": true, "detail": true, "action": true,
	"risk": true, "risk_rationale": true, "oracle": true, "prior": true,
}

// unknownCaseVariantKey returns the first key, at the top level of data or
// inside one of its "findings" elements, that is not exactly one of
// resultTopLevelKeys or resultFindingKeys - a case variant like "FINDINGS"
// or "Oracle" included. "" means every key matched exactly. A structurally
// unexpected shape (findings not an array of objects, say) reports its
// decode error instead of a key name; ParseReviewResult's own strict typed
// decode right after this call produces the actual error for that case, so
// this check's error is only ever used to skip it, never surfaced on its
// own.
func unknownCaseVariantKey(data []byte) (string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return "", err
	}
	for key := range top {
		if !resultTopLevelKeys[key] {
			return key, nil
		}
	}
	raw, ok := top["findings"]
	if !ok {
		return "", nil
	}
	var findings []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &findings); err != nil {
		return "", err
	}
	for _, f := range findings {
		for key := range f {
			if !resultFindingKeys[key] {
				return key, nil
			}
		}
	}
	return "", nil
}

// duplicateObjectKey walks data's JSON structure - the top-level object and
// every object nested inside it, findings included - and returns the first
// key that repeats an earlier key of the same object, exactly or only by
// case (a token-level check: it inspects keys, not values). "" means the
// whole document parsed with no such repeat. A malformed document surfaces
// its own decode error instead.
func duplicateObjectKey(data []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	return walkValueForDuplicateKey(dec, tok)
}

// walkValueForDuplicateKey inspects one already-read token: an object or
// array delimiter recurses into it, anything else (a scalar) has no keys of
// its own.
func walkValueForDuplicateKey(dec *json.Decoder, tok json.Token) (string, error) {
	delim, ok := tok.(json.Delim)
	if !ok {
		return "", nil
	}
	switch delim {
	case '{':
		return walkObjectForDuplicateKey(dec)
	case '[':
		return walkArrayForDuplicateKey(dec)
	default:
		return "", nil
	}
}

// walkObjectForDuplicateKey reads dec's current object (dec positioned
// right after its opening '{') member by member, checking each key against
// this object's own earlier keys only - a duplicate nested inside a
// different object is that object's own concern, caught when the walk
// recurses into it - and recursing into every member's value.
func walkObjectForDuplicateKey(dec *json.Decoder) (string, error) {
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", err
		}
		key, _ := keyTok.(string)
		folded := strings.ToLower(key)
		if seen[folded] {
			return key, nil
		}
		seen[folded] = true

		valTok, err := dec.Token()
		if err != nil {
			return "", err
		}
		if dup, err := walkValueForDuplicateKey(dec, valTok); err != nil || dup != "" {
			return dup, err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing '}'
		return "", err
	}
	return "", nil
}

// walkArrayForDuplicateKey reads dec's current array (dec positioned right
// after its opening '[') element by element, recursing into each.
func walkArrayForDuplicateKey(dec *json.Decoder) (string, error) {
	for dec.More() {
		valTok, err := dec.Token()
		if err != nil {
			return "", err
		}
		if dup, err := walkValueForDuplicateKey(dec, valTok); err != nil || dup != "" {
			return dup, err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing ']'
		return "", err
	}
	return "", nil
}

// normalizeRepoRelPath normalizes p for comparison: backslash to slash, a
// leading "./" stripped, then rejects an empty path, an
// absolute path (a leading "/" or a Windows drive letter), any ".."
// segment, and a trailing "/" (a directory, never a file a finding can
// name or a reviewer can read).
func normalizeRepoRelPath(p string) (string, error) {
	q := strings.ReplaceAll(p, "\\", "/")
	q = strings.TrimPrefix(q, "./")
	if q == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasSuffix(q, "/") {
		return "", fmt.Errorf("path %q is a directory, not a file", p)
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

// relativizeReviewedPath resolves one reviewed_paths entry for coverage. A
// plain repo-relative path normalizes as usual. review.json hands the
// reviewer several absolute paths to read - review.json itself, brief_path,
// slices_path, journal_path - and the prompt asks for "every file you
// read", so an absolute path inside the lease worktree is relativized to it
// rather than rejected: strict parsing has no rule against a reviewed_paths
// entry beyond coverage, only against a finding's file.
// Any other entry that still cannot be normalized (absolute outside the
// worktree, a ".." segment) is ignored rather than failing the round: it
// can never match a must_review path either way, so rejecting the whole
// result over it would fail a reviewer that followed the prompt exactly.
func relativizeReviewedPath(leaseDir, p string) (string, bool) {
	if norm, err := normalizeRepoRelPath(p); err == nil {
		return norm, true
	}
	if !filepath.IsAbs(p) {
		return "", false
	}
	absLease, err := filepath.Abs(leaseDir)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absLease, p)
	if err != nil {
		return "", false
	}
	norm, err := normalizeRepoRelPath(filepath.ToSlash(rel))
	if err != nil {
		return "", false
	}
	return norm, true
}

// normalizeReviewedPaths relativizes and normalizes every reviewed_paths
// entry exactly once, right after validateReviewResult has accepted the
// result. Coverage, clearing and persistence all read this one normalized
// list afterward, so an absolute in-lease path that counted as coverage
// also counts as clearing evidence and is never written to findings.yaml
// as a host path. An entry that cannot be normalized was already ignored
// by validateReviewResult's coverage check, so it is dropped here too.
func normalizeReviewedPaths(leaseDir string, paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		norm, ok := relativizeReviewedPath(leaseDir, p)
		if !ok || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}

// validateReviewResult checks result against req and the lease's head, the
// rules ParseReviewResult cannot check on its own: a finding's file exists
// at head or was deleted in the scope diff; an oracle, when
// given, names a manifest oracle, and is required when the manifest has
// more than one and the finding is fix or ask (no silent fallback); a
// prior names an id under open or dismissed, and no two findings share one;
// reviewed_paths covers every must_review path. atHead reports whether a
// (normalized) path exists in the lease at head. leaseDir is the lease
// worktree, used only to relativize an absolute reviewed_paths entry.
func validateReviewResult(req ReviewRequest, result ReviewResult, leaseDir string, atHead func(path string) (bool, error), deleted map[string]bool, oracleNames []string) error {
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
		if norm, ok := relativizeReviewedPath(leaseDir, p); ok {
			reviewed[norm] = true
		}
	}
	for _, mr := range req.MustReview {
		if !reviewed[mr] {
			return reviewInvalid(fmt.Sprintf("reviewed_paths is missing must_review path %q", mr))
		}
	}
	return nil
}

// reviewPromptTemplate is the exact prompt rendered (via fmt.Sprintf) for
// every gate reviewer dispatch: it states the job and the output contract,
// and says what jig will verify. It never lists kinds of problems, coaches
// behavior, or patches a past model mistake.
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

// SortedOracleNames returns man's oracle names in sorted order. Exported so
// cmd/jig's terminal oracle prompt can list the same names route.go's own
// oracle resolution checks against, rather than deriving its own list.
func SortedOracleNames(man manifest.Manifest) []string {
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

// resolveScopeBase resolves one round's scope and base sha: delta from the
// previous reviewer round's reviewed_sha when it is an
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

// scopeDiff is the scope diff's coverage lists: Changed is every file
// base..head adds, modifies or type-changes (a rename counts as its new
// path); Deleted is every file it removes; MustReview is their sorted,
// deduplicated union with the files of open findings that still exist at
// head.
type scopeDiff struct {
	Changed    []string
	Deleted    []string
	MustReview []string
}

// computeScopeDiff runs the scope diff and builds must_review. openFiles
// are the open-finding files findings bookkeeping (findings.go) supplies;
// a round with no open findings yet (round 1, or every round
// before the first one that reports any) passes nil. Only the reviewer
// source (below) ever calls this; the scripted source never does.
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
// so Gate's findings bookkeeping (findings.go) can apply it without
// recomputing scope.
type Review struct {
	Scope      string
	BaseSHA    string
	HeadSHA    string
	Changed    []string
	Deleted    []string
	MustReview []string
	Result     ReviewResult
}

// RoundInput is what Gate hands a GateSource for one round: the scripted
// source reads only its own Round field (fakeGateSource.Round, gate.go) and
// ignores the rest, while the reviewer source below uses the rest to build
// review.json and dispatch.
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
	Open      []OpenFinding      // findings bookkeeping's cumulative fold, projected (findings.go)
	Dismissed []DismissedFinding // findings bookkeeping's cumulative fold, projected (findings.go)
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
// its result.json strictly, or - when nothing is outstanding and the scope
// diff changes nothing - skips dispatch entirely. It never applies findings
// bookkeeping across rounds or routes findings into fix slices; Gate does
// both, using the Review this returns (findings.go's ApplyRound and
// route.go's routeRound). The
// lease is restored pristine before dispatch (oracles run just before this
// in Gate, and may have left tracked dirt) and always after, success or
// failure, so a broken reviewer never leaves the lease for a later
// operation to trip over.
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
	// in.Open (findings.go's openAndNotedFindingsList) carries a noted
	// finding too, so it stays a citable prior target, but a noted finding
	// is not outstanding work: it must not force its file into must_review
	// coverage, and it must not by itself keep this round from taking the
	// clean-without-dispatch shortcut below. openOutstanding is in.Open
	// filtered back down to open/asked for exactly those two uses.
	var openFiles []string
	openOutstanding := 0
	for _, f := range in.Open {
		if f.Action == ActionNote {
			continue
		}
		openFiles = append(openFiles, f.File)
		openOutstanding++
	}
	diff, err := computeScopeDiff(in.LeaseDir, base, head, openFiles)
	if err != nil {
		return Round{}, false, err
	}

	// Clean without dispatch: the scope diff changes no file at all (nothing
	// added, modified, type-changed or deleted) and nothing is
	// outstanding - the previous review already covers head, so there is
	// nothing for a reviewer session to do. must_review alone cannot stand
	// in for this: it never lists a deleted file, so a deletion-only diff
	// would otherwise look empty and skip review on a round that has never
	// been reviewed at all.
	if len(diff.Changed) == 0 && len(diff.Deleted) == 0 && openOutstanding == 0 {
		return Round{Review: &Review{
			Scope:      scope,
			BaseSHA:    base,
			HeadSHA:    head,
			Changed:    diff.Changed,
			Deleted:    diff.Deleted,
			MustReview: diff.MustReview,
			Result:     ReviewResult{ReviewedPaths: []string{}},
		}}, true, nil
	}

	oracleNames := SortedOracleNames(in.Manifest)
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
			Help: reviewInvalidHelp,
		}
	}
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		return Round{}, false, &axi.Error{
			Msg:  fmt.Sprintf("gate reviewer wrote no result.json at %s", resultPath),
			Code: "REVIEW_FAILED",
			Help: reviewInvalidHelp,
		}
	}

	// The read-only guard: a reviewer edits nothing. HEAD must still be
	// head, and every tracked file must be as it was.
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
			Help: reviewInvalidHelp,
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
	if err := validateReviewResult(req, result, in.LeaseDir, atHead, deletedSet, oracleNames); err != nil {
		return Round{}, false, err
	}
	result.ReviewedPaths = normalizeReviewedPaths(in.LeaseDir, result.ReviewedPaths)

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
