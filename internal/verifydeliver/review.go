package verifydeliver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// Finding class and status vocabulary (D2).
const (
	ClassMechanical = "mechanical"
	ClassIntent     = "intent"
	StatusOpen      = "open"
	StatusDismissed = "dismissed"
)

// Finding is one entry of gate/round-<n>/findings.yaml. Oracle is a
// synthesis-only input (the reviewer-suggested oracle name); it is never
// persisted.
type Finding struct {
	ID        string `yaml:"id"`
	Class     string `yaml:"class"`
	Title     string `yaml:"title"`
	Detail    string `yaml:"detail"`
	Workspace string `yaml:"workspace"`
	Oracle    string `yaml:"-"`
	Status    string `yaml:"status"`
}

// Closure records one prior-round finding's disposition after a later
// round's diff.
type Closure struct {
	ID     string `yaml:"id" json:"id"`
	Status string `yaml:"status" json:"status"` // closed|still-open
	Note   string `yaml:"note" json:"note"`
}

// findingsFile is gate/round-<n>/findings.yaml's on-disk shape.
type findingsFile struct {
	Findings []Finding `yaml:"findings"`
	Closures []Closure `yaml:"closures"`
}

// Review is a real reviewer round's content (Round.Review), as opposed to
// the scripted source's FindingsMD/FixSlices/Receipts shape.
type Review struct {
	Scope       string // full|delta
	BaseSHA     string
	HeadSHA     string
	Findings    []Finding // jig ids assigned; Status open, or dismissed when auto-dismissed
	Closures    []Closure
	Summary     string
	ReviewedSHA map[string]string // repoName -> HeadSHA
}

// PriorFinding is one prior-round open finding as review.json reports it.
type PriorFinding struct {
	ID     string `json:"id"`
	Class  string `json:"class"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// DismissedFinding is one cumulative dismissed finding as review.json
// reports it.
type DismissedFinding struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ReviewRequest is review.json's exact wire shape (D1).
type ReviewRequest struct {
	Ticket        string             `json:"ticket"`
	Round         int                `json:"round"`
	Scope         string             `json:"scope"`
	BaseSHA       string             `json:"base_sha"`
	HeadSHA       string             `json:"head_sha"`
	BriefPath     string             `json:"brief_path"`
	SlicesPath    string             `json:"slices_path"`
	JournalPath   string             `json:"journal_path"`
	PriorFindings []PriorFinding     `json:"prior_findings"`
	Dismissed     []DismissedFinding `json:"dismissed"`
}

// ResultFinding is one finding as the reviewer session reports it in
// result.json, before jig assigns it a jig id.
type ResultFinding struct {
	ID        string `json:"id"`
	Class     string `json:"class"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	Workspace string `json:"workspace"`
	Oracle    string `json:"oracle"`
}

// ReviewResult is result.json's exact wire shape (D1).
type ReviewResult struct {
	Verdict  string          `json:"verdict"` // clean|findings
	Findings []ResultFinding `json:"findings"`
	Closures []Closure       `json:"closures"`
	Summary  string          `json:"summary"`
}

// MarshalReviewRequest renders req as indented JSON, with nil
// PriorFindings/Dismissed marshaled as [] rather than null.
func MarshalReviewRequest(req ReviewRequest) ([]byte, error) {
	if req.PriorFindings == nil {
		req.PriorFindings = []PriorFinding{}
	}
	if req.Dismissed == nil {
		req.Dismissed = []DismissedFinding{}
	}
	return json.MarshalIndent(req, "", "  ")
}

// reviewInvalid wraps msg as the *axi.Error ParseReviewResult and the
// reviewer flow return for a malformed or out-of-contract result.
func reviewInvalid(msg string) error {
	return &axi.Error{Msg: "gate reviewer result.json is invalid: " + msg, Code: "REVIEW_INVALID"}
}

// ParseReviewResult parses result.json strictly: it never silently accepts
// garbage. Errors are *axi.Error{Code: "REVIEW_INVALID"} naming the
// problem.
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

	switch res.Verdict {
	case "clean":
		if len(res.Findings) != 0 {
			return ReviewResult{}, reviewInvalid("verdict \"clean\" must have zero findings")
		}
	case "findings":
		if len(res.Findings) == 0 {
			return ReviewResult{}, reviewInvalid("verdict \"findings\" must have at least one finding")
		}
	default:
		return ReviewResult{}, reviewInvalid(fmt.Sprintf("verdict %q is not \"clean\" or \"findings\"", res.Verdict))
	}

	for _, f := range res.Findings {
		if f.Class != ClassMechanical && f.Class != ClassIntent {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("finding class %q is not %q or %q", f.Class, ClassMechanical, ClassIntent))
		}
		if strings.TrimSpace(f.Title) == "" {
			return ReviewResult{}, reviewInvalid("a finding has an empty title")
		}
	}
	for _, c := range res.Closures {
		if strings.TrimSpace(c.ID) == "" {
			return ReviewResult{}, reviewInvalid("a closure has an empty id")
		}
		if c.Status != "closed" && c.Status != "still-open" {
			return ReviewResult{}, reviewInvalid(fmt.Sprintf("closure %q status %q is not \"closed\" or \"still-open\"", c.ID, c.Status))
		}
	}
	return res, nil
}

// reviewPromptTemplate is the exact prompt rendered (via fmt.Sprintf) for
// every gate reviewer dispatch.
const reviewPromptTemplate = "You are a jig gate reviewer for round %d of ticket %s.\n" +
	"Read your inputs from review.json at %s: the brief, slices and journal paths, the prior open findings, and the dismissed findings.\n" +
	"Review the %s diff %s..%s in this worktree against the brief. Do NOT edit files, commit, or push: report findings only.\n" +
	"Classify each finding as \"mechanical\" (typo, dead code, doc gap, formatting, other lint-grade fixes) or \"intent\" (behavior, correctness, security, or design). Name the file, and the line when known, in each finding's detail. Never raise a dismissed finding again. For every prior finding add a closure: \"closed\" when the diff resolves it, else \"still-open\" (jig carries a still-open finding forward into this round; do not raise it again).\n" +
	"When finished write result.json at %s with exactly one JSON object: {\"verdict\": \"clean|findings\", \"findings\": [{\"id\": \"f1\", \"class\": \"mechanical|intent\", \"title\": \"...\", \"detail\": \"...\", \"workspace\": \"<workspace id>\", \"oracle\": \"<manifest oracle name>\"}], \"closures\": [{\"id\": \"r1-f2\", \"status\": \"closed|still-open\", \"note\": \"...\"}], \"summary\": \"...\"}"

// RenderReviewPrompt fills reviewPromptTemplate for one review dispatch.
func RenderReviewPrompt(req ReviewRequest, reviewPath, resultPath string) string {
	return fmt.Sprintf(reviewPromptTemplate, req.Round, req.Ticket, reviewPath, req.Scope, req.BaseSHA, req.HeadSHA, resultPath)
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

// readFindingsFile reads one round's findings.yaml. A round with no
// findings.yaml (a scripted round) reads as ok=false, contributing
// nothing.
func readFindingsFile(st *store.Store, ticket string, round int) (findingsFile, bool, error) {
	path := filepath.Join(gateRoundDir(st, ticket, round), "findings.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return findingsFile{}, false, nil
		}
		return findingsFile{}, false, err
	}
	var ff findingsFile
	if err := yaml.Unmarshal(data, &ff); err != nil {
		return findingsFile{}, false, err
	}
	return ff, true, nil
}

// priorReviewedSHA reads round-1's report.yaml and returns its
// reviewed_sha[repoName], or "" when the round has no report.yaml (a
// scripted round) or no reviewed_sha for repoName.
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

// priorFindingsAndDismissed walks rounds 1..n-1's findings.yaml files (a
// round without one, i.e. a scripted round, contributes nothing) and
// returns review.json's prior_findings and dismissed lists: prior_findings
// is every finding with status open that no later round's closure closed
// (in first-seen order); dismissed is every finding with status dismissed,
// cumulative.
func priorFindingsAndDismissed(st *store.Store, ticket string, n int) ([]PriorFinding, []DismissedFinding, error) {
	var openOrder []Finding
	openIndex := map[string]int{}
	var dismissed []DismissedFinding
	seenDismissed := map[string]bool{}

	for r := 1; r < n; r++ {
		ff, ok, err := readFindingsFile(st, ticket, r)
		if err != nil {
			return nil, nil, fmt.Errorf("verifydeliver: review: read round %d findings.yaml: %w", r, err)
		}
		if !ok {
			continue
		}
		for _, f := range ff.Findings {
			switch f.Status {
			case StatusOpen:
				openIndex[f.ID] = len(openOrder)
				openOrder = append(openOrder, f)
			case StatusDismissed:
				if !seenDismissed[f.ID] {
					seenDismissed[f.ID] = true
					dismissed = append(dismissed, DismissedFinding{ID: f.ID, Title: f.Title})
				}
			}
		}
		for _, c := range ff.Closures {
			// Any closure - "closed" or "still-open" - removes the id from
			// future prior_findings: "closed" because the diff resolved it,
			// "still-open" because jig carries it forward under a new id in
			// the closing round (see the carry-forward step in Round), so
			// the original id must not also linger as open forever.
			if idx, ok := openIndex[c.ID]; ok {
				openOrder[idx].Status = "" // mark removed
				delete(openIndex, c.ID)
			}
		}
	}

	var prior []PriorFinding
	for _, f := range openOrder {
		if f.Status != StatusOpen {
			continue
		}
		prior = append(prior, PriorFinding{ID: f.ID, Class: f.Class, Title: f.Title, Status: StatusOpen})
	}
	return prior, dismissed, nil
}

// normalizeTitle lowercases s, collapses whitespace, and trims it, so
// dismissals match a finding regardless of case or spacing drift.
func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// collapseTitle collapses every run of whitespace, including newlines, in a
// reviewer-supplied title down to single spaces and trims it: a multi-line
// title would otherwise produce an unindented continuation line in
// findings.md and a broken bullet in a mechanical bundle's goal.
func collapseTitle(s string) string {
	return strings.Join(strings.Fields(s), " ")
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

// Round implements the reviewer source's one-round algorithm: resolve
// scope, gather prior findings/dismissals, write review.json, dispatch,
// enforce the forward-only guard, parse strictly, validate against the
// manifest, assign jig ids, and auto-dismiss.
func (r *reviewerGateSource) Round(in RoundInput) (Round, bool, error) {
	head, err := gitx.RevParse(in.LeaseDir, "HEAD")
	if err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: resolve HEAD: %w", err)
	}

	// Restore the lease to a pristine head before writing review.json or
	// dispatching. Gate runs every manifest oracle in this same lease just
	// before Round is called, and an oracle can rewrite a tracked file (a
	// formatter, a lockfile refresh); without this, the post-dispatch guard
	// below would blame that pre-existing dirt on the reviewer. It also
	// wipes any leftover untracked files from an earlier failed attempt on
	// the same lease.
	if err := resetLeasePristine(in.LeaseDir, head); err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: review: restore lease before dispatch: %w", err)
	}

	scope, base, err := reviewScope(in, head)
	if err != nil {
		return Round{}, false, err
	}

	priorFindings, dismissed, err := priorFindingsAndDismissed(in.Store, in.Ticket, in.Round)
	if err != nil {
		return Round{}, false, err
	}

	req := ReviewRequest{
		Ticket:        in.Ticket,
		Round:         in.Round,
		Scope:         scope,
		BaseSHA:       base,
		HeadSHA:       head,
		BriefPath:     absPath(in.BriefPath),
		SlicesPath:    absPath(filepath.Join(in.Store.TicketDir(in.Ticket), "slices.yaml")),
		JournalPath:   absPath(filepath.Join(in.Store.TicketDir(in.Ticket), "journal.ndjson")),
		PriorFindings: priorFindings,
		Dismissed:     dismissed,
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
	// After dispatch, whatever happens, the lease must end up back at a
	// pristine head: this undoes any reviewer edit or commit (and, in
	// --branch mode, moves the branch ref itself back), so a later round -
	// or the oracles gate.go runs before it - never sees reviewer-written
	// content. Runs on every return path from here, success or error.
	defer func() {
		_ = resetLeasePristine(in.LeaseDir, head)
	}()

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

	// Every prior open finding needs exactly one closure: missing or
	// duplicate is invalid, same as an unknown id. Otherwise an unclosed id
	// (or a contradictory double closure) would silently linger in
	// prior_findings forever, or a clean verdict with no closures at all
	// would pass the gate with open findings the reviewer never spoke to.
	priorIDs := make(map[string]bool, len(req.PriorFindings))
	for _, pf := range req.PriorFindings {
		priorIDs[pf.ID] = true
	}
	closureCount := map[string]int{}
	for _, c := range result.Closures {
		if !priorIDs[c.ID] {
			return Round{}, false, &axi.Error{
				Msg:  fmt.Sprintf("gate reviewer result.json closure %q does not name a prior open finding", c.ID),
				Code: "REVIEW_INVALID",
			}
		}
		closureCount[c.ID]++
		if closureCount[c.ID] > 1 {
			return Round{}, false, &axi.Error{
				Msg:  fmt.Sprintf("gate reviewer result.json has more than one closure for %q", c.ID),
				Code: "REVIEW_INVALID",
			}
		}
	}
	for _, pf := range req.PriorFindings {
		if closureCount[pf.ID] == 0 {
			return Round{}, false, &axi.Error{
				Msg:  fmt.Sprintf("gate reviewer result.json is missing a closure for prior finding %q", pf.ID),
				Code: "REVIEW_INVALID",
			}
		}
	}

	findings := make([]Finding, 0, len(result.Findings))
	for i, rf := range result.Findings {
		ws := rf.Workspace
		if ws == "" {
			if len(in.Manifest.Workspaces) != 1 {
				return Round{}, false, &axi.Error{
					Msg:  fmt.Sprintf("gate reviewer result.json finding %q has no workspace and the manifest has %d workspaces", rf.Title, len(in.Manifest.Workspaces)),
					Code: "REVIEW_INVALID",
				}
			}
			ws = in.Manifest.Workspaces[0].ID
		} else if _, known := in.Manifest.Workspace(ws); !known {
			return Round{}, false, &axi.Error{
				Msg:  fmt.Sprintf("gate reviewer result.json finding %q names unknown workspace %q", rf.Title, ws),
				Code: "REVIEW_INVALID",
			}
		}
		findings = append(findings, Finding{
			ID:        fmt.Sprintf("r%d-f%d", in.Round, i+1),
			Class:     rf.Class,
			Title:     collapseTitle(rf.Title),
			Detail:    rf.Detail,
			Workspace: ws,
			Oracle:    rf.Oracle,
			Status:    StatusOpen,
		})
	}

	// Carry every "still-open" closure forward as a jig-side finding in this
	// round, instead of relying on the reviewer to re-raise it (the prompt
	// now tells it not to). It keeps the prior finding's class/title/
	// workspace/detail, extends the detail with the closure's note, inherits
	// the oracle of the fix slice the prior finding produced, and goes
	// through auto-dismiss and triage exactly like a reviewer-raised
	// finding.
	closureByID := make(map[string]Closure, len(result.Closures))
	for _, c := range result.Closures {
		closureByID[c.ID] = c
	}
	nextSeq := len(findings) + 1
	for _, pf := range req.PriorFindings {
		c, ok := closureByID[pf.ID]
		if !ok || c.Status != "still-open" {
			continue
		}
		prior, found, ferr := findFindingByID(in.Store, in.Ticket, pf.ID)
		if ferr != nil {
			return Round{}, false, fmt.Errorf("verifydeliver: review: read carried finding %s: %w", pf.ID, ferr)
		}
		if !found {
			return Round{}, false, fmt.Errorf("verifydeliver: review: prior finding %s not found in its round's findings.yaml", pf.ID)
		}
		detail := prior.Detail
		if note := strings.TrimSpace(c.Note); note != "" {
			detail = strings.TrimRight(detail, "\n")
			if detail != "" {
				detail += "\n\n"
			}
			detail += fmt.Sprintf("Still open in round %d: %s", in.Round, note)
		}
		oracle, oerr := carriedFindingOracle(in.Store, in.Ticket, pf.ID, prior)
		if oerr != nil {
			return Round{}, false, fmt.Errorf("verifydeliver: review: resolve carried finding %s oracle: %w", pf.ID, oerr)
		}
		findings = append(findings, Finding{
			ID:        fmt.Sprintf("r%d-f%d", in.Round, nextSeq),
			Class:     prior.Class,
			Title:     prior.Title,
			Detail:    detail,
			Workspace: prior.Workspace,
			Oracle:    oracle,
			Status:    StatusOpen,
		})
		nextSeq++
	}

	dismissedTitles := make(map[string]bool, len(dismissed))
	for _, df := range dismissed {
		dismissedTitles[normalizeTitle(df.Title)] = true
	}
	for i := range findings {
		if dismissedTitles[normalizeTitle(findings[i].Title)] {
			findings[i].Status = StatusDismissed
		}
	}

	return Round{Review: &Review{
		Scope:       scope,
		BaseSHA:     base,
		HeadSHA:     head,
		Findings:    findings,
		Closures:    result.Closures,
		Summary:     result.Summary,
		ReviewedSHA: map[string]string{in.RepoName: head},
	}}, true, nil
}

// findingRound parses the round number out of a jig finding id
// "r<round>-f<k>". Ids are always assigned this way (see Round), so the id
// alone says which round's findings.yaml to read.
func findingRound(id string) (int, bool) {
	if !strings.HasPrefix(id, "r") {
		return 0, false
	}
	idx := strings.Index(id, "-f")
	if idx < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(id[1:idx])
	if err != nil {
		return 0, false
	}
	return n, true
}

// findFindingByID looks up a jig finding id in the findings.yaml of the
// round its id names.
func findFindingByID(st *store.Store, ticket, id string) (Finding, bool, error) {
	round, ok := findingRound(id)
	if !ok {
		return Finding{}, false, nil
	}
	ff, ok, err := readFindingsFile(st, ticket, round)
	if err != nil {
		return Finding{}, false, err
	}
	if !ok {
		return Finding{}, false, nil
	}
	for _, f := range ff.Findings {
		if f.ID == id {
			return f, true, nil
		}
	}
	return Finding{}, false, nil
}

// carriedFindingOracle resolves the oracle a "still-open" closure's
// jig-carried finding should synthesize with: the oracle of the fix slice
// prior's own finding produced (intent: fix-<round>-<k>; mechanical: that
// round's bundle for prior's workspace, singular or per-workspace id), or ""
// (synthesis then falls back to the manifest's first oracle) when that slice
// cannot be found.
func carriedFindingOracle(st *store.Store, ticket, id string, prior Finding) (string, error) {
	round, ok := findingRound(id)
	if !ok {
		return "", nil
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	var wantIDs []string
	switch prior.Class {
	case ClassIntent:
		wantIDs = []string{fmt.Sprintf("fix-%d-%s", round, findingSeq(id))}
	case ClassMechanical:
		wantIDs = []string{
			fmt.Sprintf("fix-%d-mech", round),
			fmt.Sprintf("fix-%d-mech-%s", round, sanitizeWorkspaceID(prior.Workspace)),
		}
	default:
		return "", nil
	}
	for _, s := range slices {
		for _, want := range wantIDs {
			if s.ID == want {
				return s.Oracle, nil
			}
		}
	}
	return "", nil
}

// resetLeasePristine hard-resets leaseDir to head and removes every
// untracked file and directory. It never uses `clean -x`, so ignored build
// caches (e.g. node_modules) survive; only content git itself would track or
// that a reviewer left behind is wiped.
func resetLeasePristine(leaseDir, head string) error {
	if _, err := gitx.Run(leaseDir, "reset", "--hard", head); err != nil {
		return err
	}
	if _, err := gitx.Run(leaseDir, "clean", "-fd"); err != nil {
		return err
	}
	return nil
}

// reviewScope resolves one round's scope and base sha (D3): round 1 or a
// missing/non-ancestor prior reviewed_sha (including an IsAncestor error,
// e.g. an unknown object after a rebase) falls back to a full review.
//
// Full scope's base is the merge base of origin/<target> and HEAD. After a
// rebase onto a newer target, or for a --branch branch cut from a newer
// main, the ticket's own start sha is still an ancestor of head (the
// ancestor check alone cannot tell), so using it as the base would include
// every unrelated target commit between the old start and the real fork
// point. The start sha is used only when the merge-base lookup itself
// fails.
func reviewScope(in RoundInput, head string) (scope, base string, err error) {
	if in.Round > 1 {
		prior, perr := priorReviewedSHA(in.Store, in.Ticket, in.Round-1, in.RepoName)
		if perr != nil {
			return "", "", fmt.Errorf("verifydeliver: review: read round %d report.yaml: %w", in.Round-1, perr)
		}
		if prior != "" {
			if isAnc, ancErr := gitx.IsAncestor(in.LeaseDir, prior, head); ancErr == nil && isAnc {
				return "delta", prior, nil
			}
		}
	}

	if mb, mbErr := gitx.MergeBase(in.LeaseDir, "origin/"+in.Target, "HEAD"); mbErr == nil {
		return "full", mb, nil
	}

	startPath := filepath.Join(in.Store.TicketDir(in.Ticket), fmt.Sprintf("start.%s.sha", in.RepoName))
	if data, rerr := os.ReadFile(startPath); rerr == nil {
		startSHA := strings.TrimSpace(string(data))
		if isAnc, ancErr := gitx.IsAncestor(in.LeaseDir, startSHA, head); ancErr == nil && isAnc {
			return "full", startSHA, nil
		}
	}

	return "", "", fmt.Errorf("verifydeliver: review: resolve full-scope base: merge-base of origin/%s and HEAD failed and no usable start sha", in.Target)
}

// triageOpenFindings runs triage over findings' open subset (only called
// when there is at least one open finding) and returns the final findings
// slice (statuses updated: every open finding not kept becomes dismissed)
// and the kept subset, in original order.
func triageOpenFindings(findings []Finding, triage func([]Finding) []Finding) (final, kept []Finding) {
	var open []Finding
	for _, f := range findings {
		if f.Status == StatusOpen {
			open = append(open, f)
		}
	}

	keptIDs := map[string]bool{}
	if len(open) > 0 {
		if triage == nil {
			for _, f := range open {
				keptIDs[f.ID] = true
			}
		} else {
			for _, f := range triage(open) {
				keptIDs[f.ID] = true
			}
		}
	}

	final = make([]Finding, len(findings))
	copy(final, findings)
	for i := range final {
		if final[i].Status == StatusOpen && !keptIDs[final[i].ID] {
			final[i].Status = StatusDismissed
		}
	}
	for _, f := range final {
		if f.Status == StatusOpen {
			kept = append(kept, f)
		}
	}
	return final, kept
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

// resolveOracle returns name when it names a manifest oracle, else the
// first manifest oracle name in sorted order (manifest oracles are global,
// not per workspace). A manifest with no oracles is GATE_NO_ORACLE.
func resolveOracle(name string, oracleNames []string) (string, error) {
	if len(oracleNames) == 0 {
		return "", &axi.Error{Msg: "manifest has no oracles; cannot resolve a fix-slice oracle", Code: "GATE_NO_ORACLE"}
	}
	for _, o := range oracleNames {
		if o == name {
			return name, nil
		}
	}
	return oracleNames[0], nil
}

// findingSeq extracts the "<k>" suffix from a jig finding id "r<n>-f<k>".
func findingSeq(id string) string {
	idx := strings.LastIndex(id, "-f")
	if idx < 0 {
		return id
	}
	return id[idx+2:]
}

// sanitizeWorkspaceID replaces every character outside [A-Za-z0-9._-] with
// "-". A manifest workspace id can legally contain a character such as "/",
// "\" or ":" that is not safe in a `fix-<n>-mech-<workspace>` slice id,
// since that id becomes part of slices/<id>.state and work/<id>.attempt-N.*
// paths (nesting directories, or invalid on Windows).
func sanitizeWorkspaceID(ws string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, ws)
}

// synthesizeFixSlices turns kept findings into fix slices (D4, jig-side,
// from approved findings only): one slice per intent finding, one bundle
// per workspace for mechanical findings (pinned to the cheapest rung).
// Order: intent slices in finding order, then mechanical bundles.
func synthesizeFixSlices(round int, kept []Finding, man manifest.Manifest) ([]store.Slice, error) {
	oracleNames := sortedOracleNames(man)

	var slices []store.Slice
	var mechOrder []string
	mechGroups := map[string][]Finding{}

	for _, f := range kept {
		switch f.Class {
		case ClassIntent:
			oracle, err := resolveOracle(f.Oracle, oracleNames)
			if err != nil {
				return nil, err
			}
			goal := f.Title
			if f.Detail != "" {
				goal = f.Title + "\n\n" + f.Detail
			}
			slices = append(slices, store.Slice{
				ID:        fmt.Sprintf("fix-%d-%s", round, findingSeq(f.ID)),
				Workspace: f.Workspace,
				Goal:      goal,
				Oracle:    oracle,
				FromGate:  round,
			})
		case ClassMechanical:
			if _, ok := mechGroups[f.Workspace]; !ok {
				mechOrder = append(mechOrder, f.Workspace)
			}
			mechGroups[f.Workspace] = append(mechGroups[f.Workspace], f)
		}
	}

	singleWorkspace := len(mechOrder) <= 1
	for _, ws := range mechOrder {
		group := mechGroups[ws]
		oracle, err := resolveOracle(group[0].Oracle, oracleNames)
		if err != nil {
			return nil, err
		}
		var goal strings.Builder
		goal.WriteString("Fix these mechanical gate findings:")
		for _, f := range group {
			goal.WriteString("\n- " + f.Title)
			if f.Detail != "" {
				goal.WriteString(": " + strings.ReplaceAll(f.Detail, "\n", " "))
			}
		}
		id := fmt.Sprintf("fix-%d-mech", round)
		if !singleWorkspace {
			id = fmt.Sprintf("fix-%d-mech-%s", round, sanitizeWorkspaceID(ws))
		}
		slices = append(slices, store.Slice{
			ID:        id,
			Workspace: ws,
			Goal:      goal.String(),
			Oracle:    oracle,
			FromGate:  round,
			Rung:      staircase.RungCheapest,
		})
	}
	return slices, nil
}

// renderFindingsMD deterministically renders a reviewer round's
// findings.md from its structured findings (D2): no shas, a trailing
// newline, "none" when there are no findings, and Closures/Summary
// sections only when non-empty.
func renderFindingsMD(round int, scope, verdict string, findings []Finding, closures []Closure, summary string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Gate round %d\n\n", round)
	fmt.Fprintf(&b, "scope: %s\n", scope)
	fmt.Fprintf(&b, "verdict: %s\n\n", verdict)
	b.WriteString("## Findings\n\n")
	if len(findings) == 0 {
		b.WriteString("none\n")
	} else {
		for _, f := range findings {
			fmt.Fprintf(&b, "- %s (%s, %s) %s: %s\n", f.ID, f.Class, f.Workspace, f.Status, f.Title)
			if f.Detail != "" {
				for _, line := range strings.Split(f.Detail, "\n") {
					fmt.Fprintf(&b, "  %s\n", line)
				}
			}
		}
	}
	if len(closures) > 0 {
		b.WriteString("\n## Closures\n\n")
		for _, c := range closures {
			fmt.Fprintf(&b, "- %s %s: %s\n", c.ID, c.Status, c.Note)
		}
	}
	if strings.TrimSpace(summary) != "" {
		b.WriteString("\n## Summary\n\n")
		b.WriteString(summary)
		if !strings.HasSuffix(summary, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// writeReviewerRound writes a real reviewer round's files: findings.yaml
// (structured), findings.md (rendered from it), report.yaml (with
// reviewed_sha), and diff-changelog.md. Reviewer rounds carry no receipts.
func writeReviewerRound(d Deps, ticket string, n int, report GateReport, rv *Review, findings []Finding) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}

	ffOut, err := yaml.Marshal(findingsFile{Findings: findings, Closures: rv.Closures})
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: marshal findings.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), ffOut, 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.yaml: %w", err)
	}

	md := renderFindingsMD(n, rv.Scope, report.Verdict, findings, rv.Closures, rv.Summary)
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(md), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}

	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	if err := writeDiffChangelog(d, ticket, dir, n); err != nil {
		return err
	}
	return nil
}
