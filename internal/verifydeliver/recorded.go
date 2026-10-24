package verifydeliver

// A recorded gate round: jig starts no reviewer. `jig gate` opens a round
// (GatePrepare) and writes what a reviewer needs; an agent that jig did not
// start, in a fresh context, reads it (`jig review context`), reports
// findings as it goes (`jig finding`) and says it is done (`jig review done`,
// GateFinish), which checks the report as strictly as a launched reviewer's
// and records the round the way every round is recorded. While a round is open
// its state lives in the store's work directory, never in a gate/round-N
// directory: existingGateRounds counts those, and a half-recorded round must
// neither block the next number nor be counted as a round.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// openRoundFile is work/gate.round-N.open.yaml: what jig settled when it
// opened round N, and what `review done` needs to close it in another
// process.
type openRoundFile struct {
	Round  int    `yaml:"round"`
	Ticket string `yaml:"ticket"`
	Repo   string `yaml:"repo"`
	Branch string `yaml:"branch"`
	// Copy says whose copy of the branch the round reviews.
	Copy      string `yaml:"copy"`
	Head      string `yaml:"head"`
	Base      string `yaml:"base"`
	Scope     string `yaml:"scope"`
	Target    string `yaml:"target"`
	TargetSHA string `yaml:"target_sha"`
	// Lease is the gate lease the round's head is checked out in: the
	// pristine worktree the reviewer reads. It, IntentExcerpt and Recordings
	// are paths under the jig home, written relative to it: the record is in
	// the store, which is shared and must not name this machine's directories.
	Lease   string           `yaml:"lease"`
	Adopted bool             `yaml:"adopted,omitempty"`
	Intent  reportIntentYAML `yaml:"intent"`
	// IntentExcerpt is the excerpt of the author's own agent session the
	// request points the reviewer at, when it does; IntentNote is why it does
	// not when no intent is stated either.
	IntentExcerpt string `yaml:"intent_excerpt,omitempty"`
	IntentNote    string `yaml:"intent_note,omitempty"`
	// Recordings is the list of the build's recordings under the jig home.
	Recordings string `yaml:"recordings,omitempty"`
	// Request is the reviewer's copy of the request under the jig home.
	Request string `yaml:"request,omitempty"`
	// Yes is `jig gate --yes`.
	Yes    bool   `yaml:"yes,omitempty"`
	Opened string `yaml:"opened"`
	// Reviewer is what the reviewer said of itself when it read the round
	// (`jig review context --session --model --effort`).
	Reviewer Provenance `yaml:"reviewer,omitempty"`
}

// Prepared is an open round: what GatePrepare settled, and the request a
// reviewer is handed. GateFinish closes it.
type Prepared struct {
	Ticket string
	Round  int
	Repo   string
	Branch string
	// Copy names whose copy of the branch the round reviews: "the clone's",
	// "origin's" or "the build lease's".
	Copy string
	// Head is the commit under review; Base and Scope say what it is reviewed
	// against.
	Head, Base, Scope string
	Target, TargetSHA string
	// Lease is the pristine worktree of Head the reviewer reads.
	Lease        string
	Adopted      bool
	Intent       Intent
	IntentSHA256 string
	IntentNote   string
	Yes          bool
	Opened       time.Time
	// Reviewer is what the reviewer told jig of itself, if it did.
	Reviewer Provenance
	// Request is the review.json at RequestPath.
	Request ReviewRequest
	// RequestPath is the reviewer's copy of the request, under the jig home:
	// the worktree, the intent excerpt and the recordings list included.
	RequestPath string
	// ReviewPath, OpenPath and DraftsPath are the round's files under the
	// store's work directory; the review.json there names no directory of
	// this machine.
	ReviewPath, OpenPath, DraftsPath string
}

// openRoundPath, findingsDraftsPath: one round's open record and finding
// drafts, store-side, next to its review.json.
func openRoundPath(st *store.Store, ticket string, n int) string {
	return filepath.Join(gateWorkDir(st, ticket), fmt.Sprintf("gate.round-%d.open.yaml", n))
}

func findingsDraftsPath(st *store.Store, ticket string, n int) string {
	return filepath.Join(gateWorkDir(st, ticket), fmt.Sprintf("gate.round-%d.findings.ndjson", n))
}

// readOpenFile reads round n's open record; ok is false when the round was
// never opened.
func readOpenFile(st *store.Store, ticket string, n int) (openRoundFile, bool, error) {
	data, err := os.ReadFile(openRoundPath(st, ticket, n))
	if err != nil {
		if isNotExist(err) {
			return openRoundFile{}, false, nil
		}
		return openRoundFile{}, false, fmt.Errorf("verifydeliver: read the open round: %w", err)
	}
	var of openRoundFile
	if err := yaml.Unmarshal(data, &of); err != nil {
		return openRoundFile{}, false, fmt.Errorf("verifydeliver: parse the open round: %w", err)
	}
	return of, true, nil
}

func writeOpenFile(st *store.Store, of openRoundFile) error {
	data, err := yaml.Marshal(of)
	if err != nil {
		return fmt.Errorf("verifydeliver: marshal the open round: %w", err)
	}
	path := openRoundPath(st, of.Ticket, of.Round)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("verifydeliver: create work dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("verifydeliver: write the open round: %w", err)
	}
	return nil
}

// homeRel is path as a path relative to the jig home root, slash-separated:
// the form a path of this machine takes in the store. A path that is not under
// the jig home is returned as it is.
func homeRel(jigHome, path string) string {
	if path == "" || jigHome == "" {
		return path
	}
	rel, err := filepath.Rel(absPath(jigHome), absPath(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(rel)
}

// homeAbs is homeRel's inverse: the absolute path under the jig home that rel
// names, or rel itself when it is already absolute or empty.
func homeAbs(jigHome, rel string) string {
	if rel == "" || jigHome == "" || filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(absPath(jigHome), filepath.FromSlash(rel))
}

// preparedOf joins an open record with its request, spelling out the paths the
// store keeps relative to the jig home jigHome (the lease, the excerpt, the
// recordings list): the request a reviewer reads names them as they are on
// this machine.
func preparedOf(jigHome string, st *store.Store, of openRoundFile, req ReviewRequest) Prepared {
	opened, _ := time.Parse(time.RFC3339Nano, of.Opened)
	req.Worktree = homeAbs(jigHome, of.Lease)
	req.IntentExcerpt = homeAbs(jigHome, of.IntentExcerpt)
	req.Recordings = homeAbs(jigHome, of.Recordings)
	requestPath := homeAbs(jigHome, of.Request)
	if requestPath == "" {
		requestPath = reviewJSONPath(st, of.Ticket, of.Round)
	}
	return Prepared{
		Ticket: of.Ticket, Round: of.Round, Repo: of.Repo, Branch: of.Branch, Copy: of.Copy,
		Head: of.Head, Base: of.Base, Scope: of.Scope, Target: of.Target, TargetSHA: of.TargetSHA,
		Lease: req.Worktree, Adopted: of.Adopted,
		Intent: req.Intent, IntentSHA256: of.Intent.SHA256, IntentNote: of.IntentNote,
		Yes: of.Yes, Opened: opened, Reviewer: of.Reviewer, Request: req,
		RequestPath: requestPath,
		ReviewPath:  reviewJSONPath(st, of.Ticket, of.Round),
		OpenPath:    openRoundPath(st, of.Ticket, of.Round),
		DraftsPath:  findingsDraftsPath(st, of.Ticket, of.Round),
	}
}

// GatePrepare opens a gate round for ticket, or re-opens the one already open
// (its number is the next one until `review done` records it): the same
// preconditions, copy of the branch, intent, journaled gate-open line and
// oracles as any gate round (gatePrepare), and then, in place of a reviewer, a
// review.json for one that jig does not start (PrepareReviewRequest) and the
// open record next to it (gate.round-N.open.yaml). It never creates
// gate/round-N: until `review done` there is no round, only an open one.
//
// Re-opening a round whose head has not moved keeps the findings drafted so
// far and the reviewer's self-report; one whose head moved starts again, since
// the drafts were about code that is no longer the branch's.
func GatePrepare(d Deps, o GateOpts) (p Prepared, err error) {
	var run *gateRun
	defer func() { run.pushFailure(err) }()
	if run, err = gatePrepare(d, o); err != nil {
		return Prepared{}, err
	}
	in := run.roundInput()
	in.Recorded = true
	prep, err := PrepareReviewRequest(in)
	if err != nil {
		return Prepared{}, err
	}

	prev, hadPrev, err := readOpenFile(d.Store, run.ticket, run.n)
	if err != nil {
		return Prepared{}, err
	}
	of := openRoundFile{
		Round: run.n, Ticket: run.ticket, Repo: run.repoName, Branch: run.branch, Copy: string(run.copy),
		Head: prep.Head, Base: prep.Base, Scope: prep.Scope, Target: run.target,
		Lease: homeRel(d.Home, absPath(run.lease.Dir)), Adopted: run.gb.Adopted,
		Intent:        reportIntentYAML{Source: run.intent.Source, SHA256: run.intentSHA256},
		IntentExcerpt: homeRel(d.Home, prep.Request.IntentExcerpt), IntentNote: prep.IntentNote,
		Recordings: homeRel(d.Home, prep.RecordingsPath), Request: homeRel(d.Home, prep.RequestPath), Yes: o.Yes,
		Opened: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if of.TargetSHA, err = originTip(run.lease.Dir, run.target); err != nil {
		return Prepared{}, err
	}
	if hadPrev && prev.Head == prep.Head {
		of.Reviewer = prev.Reviewer
	} else if err := os.Remove(findingsDraftsPath(d.Store, run.ticket, run.n)); err != nil && !isNotExist(err) {
		return Prepared{}, fmt.Errorf("verifydeliver: clear the drafts of an earlier head: %w", err)
	}
	if err := writeOpenFile(d.Store, of); err != nil {
		return Prepared{}, err
	}
	if err := d.Store.Push(fmt.Sprintf("%s: gate round %d opened", run.ticket, run.n)); err != nil {
		return Prepared{}, fmt.Errorf("verifydeliver: gate: push store: %w", err)
	}
	return preparedOf(d.Home, d.Store, of, prep.Request), nil
}

// CurrentRound returns the round open for ticket: the next round, once it has
// been opened (GatePrepare) and not yet recorded (GateFinish). ok is false when
// none is open.
func CurrentRound(d Deps, ticket string) (Prepared, bool, error) {
	n, err := existingGateRounds(d.Store, ticket)
	if err != nil {
		return Prepared{}, false, fmt.Errorf("verifydeliver: count rounds: %w", err)
	}
	of, ok, err := readOpenFile(d.Store, ticket, n+1)
	if err != nil || !ok {
		return Prepared{}, false, err
	}
	data, err := os.ReadFile(reviewJSONPath(d.Store, ticket, of.Round))
	if err != nil {
		return Prepared{}, false, fmt.Errorf("verifydeliver: read the open round's review.json: %w", err)
	}
	var req ReviewRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return Prepared{}, false, fmt.Errorf("verifydeliver: parse the open round's review.json: %w", err)
	}
	return preparedOf(d.Home, d.Store, of, req), true, nil
}

// BranchMoved reports the commit the clone's branch is at now when the open
// round reviewed the clone's copy and the branch has moved since: the round's
// head is then not the branch's, and a verdict on it would be about code
// nobody ships. "" means the branch has not moved, or the round did not read
// the clone's copy, which jig cannot cheaply look at again.
func BranchMoved(d Deps, p Prepared) (string, error) {
	if p.Copy != string(clonesCopy) {
		return "", nil
	}
	clone := operatorClone(d, p.Repo)
	if clone == "" {
		return "", nil
	}
	tip, err := branchTipIn(clone, p.Branch)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: %w", err)
	}
	if tip == "" || tip == p.Head {
		return "", nil
	}
	return tip, nil
}

// RecordReviewer notes what the reviewer said of itself in the open round's
// record: the fields given replace what was there, the rest are kept.
func RecordReviewer(d Deps, p Prepared, who Provenance) (Prepared, error) {
	of, ok, err := readOpenFile(d.Store, p.Ticket, p.Round)
	if err != nil {
		return Prepared{}, err
	}
	if !ok {
		return Prepared{}, errNoOpenRound(p.Ticket)
	}
	if who.Session != "" && (who.Session != of.Reviewer.Session || who.Agent != of.Reviewer.Agent) {
		of.Reviewer = Provenance{Session: who.Session, Agent: who.Agent, How: who.How}
	}
	if who.Model != "" {
		of.Reviewer.Model = who.Model
	}
	if who.Effort != "" {
		of.Reviewer.Effort = who.Effort
	}
	if err := writeOpenFile(d.Store, of); err != nil {
		return Prepared{}, err
	}
	p.Reviewer = of.Reviewer
	return p, nil
}

// originTip is the sha of origin/<branch> as the repository at dir last
// fetched it.
func originTip(dir, branch string) (string, error) {
	tip, err := originsTip(dir, branch)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: %w", err)
	}
	return tip, nil
}

// Draft is one entry of a round's findings drafts: a finding the reviewer
// reports, or an earlier finding it confirms is still present. A round's
// drafts are its ReviewResult, said a piece at a time.
type Draft struct {
	Finding      *ResultFinding     `json:"finding,omitempty"`
	StillPresent *StillPresentEntry `json:"still_present,omitempty"`
}

// AppendDraft checks d against the open round and appends it to the round's
// findings drafts. What it checks is what needs no other draft: the fields
// ParseReviewResult checks of a finding, an oracle that is one of the
// manifest's, a prior that names a finding under open or dismissed. That the
// file exists at the head, that no two drafts share a prior and that the
// reviewed paths cover must_review are checked when the round is done. It
// returns how many drafts the round has now.
func AppendDraft(d Deps, p Prepared, dr Draft) (int, error) {
	if (dr.Finding == nil) == (dr.StillPresent == nil) {
		return 0, &axi.Error{Msg: "a draft is a finding or a still_present entry, not both and not neither", Code: "VALIDATION_ERROR"}
	}
	existing, err := ReadDrafts(p)
	if err != nil {
		return 0, err
	}
	known := map[string]bool{}
	for _, f := range p.Request.Open {
		known[f.ID] = true
	}
	for _, f := range p.Request.Dismissed {
		known[f.ID] = true
	}
	switch {
	case dr.Finding != nil:
		f := *dr.Finding
		if err := checkResultFinding(len(existing), f); err != nil {
			return 0, err
		}
		if f.Oracle != "" && !inStrings(p.Request.Oracles, f.Oracle) {
			return 0, reviewInvalid(fmt.Sprintf("oracle %q is not a manifest oracle (%s)", f.Oracle, strings.Join(p.Request.Oracles, ", ")))
		}
		if f.Oracle == "" && len(p.Request.Oracles) > 1 && (f.Action == ActionFix || f.Action == ActionAsk) {
			return 0, reviewInvalid(fmt.Sprintf("a finding with action %q must name one of the manifest's %d oracles (%s)", f.Action, len(p.Request.Oracles), strings.Join(p.Request.Oracles, ", ")))
		}
		if f.Prior != "" && !known[f.Prior] {
			return 0, reviewInvalid(fmt.Sprintf("prior %q does not name a finding under open or dismissed", f.Prior))
		}
	default:
		sp := *dr.StillPresent
		if err := checkStillPresent(len(existing), sp); err != nil {
			return 0, err
		}
		if !known[sp.Prior] {
			return 0, reviewInvalid(fmt.Sprintf("still_present %q does not name a finding under open or dismissed", sp.Prior))
		}
	}
	data, err := json.Marshal(dr)
	if err != nil {
		return 0, fmt.Errorf("verifydeliver: marshal a draft: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p.DraftsPath), 0o755); err != nil {
		return 0, fmt.Errorf("verifydeliver: create work dir: %w", err)
	}
	f, err := os.OpenFile(p.DraftsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("verifydeliver: open the findings drafts: %w", err)
	}
	_, werr := f.Write(append(data, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return 0, fmt.Errorf("verifydeliver: append a draft: %w", werr)
	}
	return len(existing) + 1, nil
}

// ReadDrafts reads the round's findings drafts, in the order they were made;
// none is an empty list.
func ReadDrafts(p Prepared) ([]Draft, error) {
	data, err := os.ReadFile(p.DraftsPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("verifydeliver: read the findings drafts: %w", err)
	}
	var out []Draft
	for i, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var dr Draft
		if err := json.Unmarshal(line, &dr); err != nil {
			return nil, fmt.Errorf("verifydeliver: findings draft %d is not valid JSON: %w", i+1, err)
		}
		out = append(out, dr)
	}
	return out, nil
}

// DraftResult is the ReviewResult the drafts, the paths the reviewer read and
// its summary say together: what `review done` reports when the reviewer
// reported by piece and not with one result file.
func DraftResult(drafts []Draft, reviewed []string, summary string) ReviewResult {
	res := ReviewResult{Findings: []ResultFinding{}, StillPresent: []StillPresentEntry{}, ReviewedPaths: []string{}, Summary: summary}
	for _, dr := range drafts {
		if dr.Finding != nil {
			res.Findings = append(res.Findings, *dr.Finding)
		}
		if dr.StillPresent != nil {
			res.StillPresent = append(res.StillPresent, *dr.StillPresent)
		}
	}
	res.ReviewedPaths = append(res.ReviewedPaths, reviewed...)
	return res
}

// marshalResult renders a ReviewResult as result.json, with the three lists as
// [] and never null (ParseReviewResult refuses null).
func marshalResult(r ReviewResult) ([]byte, error) {
	if r.Findings == nil {
		r.Findings = []ResultFinding{}
	}
	if r.StillPresent == nil {
		r.StillPresent = []StillPresentEntry{}
	}
	if r.ReviewedPaths == nil {
		r.ReviewedPaths = []string{}
	}
	return json.MarshalIndent(r, "", "  ")
}

func inStrings(list []string, s string) bool {
	for _, have := range list {
		if have == s {
			return true
		}
	}
	return false
}

// GateFinish closes the open round p with the reviewer's report rr, which the
// caller parsed strictly (ParseReviewResult, or DraftResult): it checks it the
// way a launched reviewer's is checked, and records the round exactly as every
// round is recorded (finish) - findings bookkeeping, routing, the round's
// files, fix slices, the journal and the push.
//
// What replaces the guard that kept a launched reviewer out of the lease is
// checked first: the lease is still at the round's head with no tracked file
// changed, and the clone's branch has not moved on (REVIEW_STALE_HEAD), so the
// round is a verdict on the branch as it is. prov is who the caller says
// reported (a zero Session: jig looks for them, LedgerReviewer, and otherwise
// says it does not know); whether they built what they reviewed decides
// independent, and a reviewer who did makes the round not clean
// (REVIEW_NOT_INDEPENDENT, returned with the report of the round that was
// recorded).
func GateFinish(d Deps, p Prepared, rr ReviewResult, prov Provenance) (report GateReport, err error) {
	var run *gateRun
	defer func() { run.pushFailure(err) }()

	if err := d.Store.Sync(); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: sync store: %w", err)
	}
	n, err := existingGateRounds(d.Store, p.Ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: count rounds: %w", err)
	}
	if p.Round != n+1 {
		return GateReport{}, errNoOpenRound(p.Ticket)
	}

	if !pool.Usable(p.Lease) {
		return GateReport{}, &axi.Error{
			Msg:  fmt.Sprintf("the gate lease of round %d is gone", p.Round),
			Code: "REVIEW_NOT_OPEN",
			Help: []string{fmt.Sprintf("Open the round again with `jig gate %s`", p.Ticket)},
		}
	}
	changed, err := leaseChanged(p.Lease, p.Head)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: %w", err)
	}
	if changed {
		return GateReport{}, &axi.Error{
			Msg:  "the gate lease changed since the round opened; a reviewer reads it and edits nothing",
			Code: "REVIEW_INVALID",
			Help: []string{fmt.Sprintf("Open the round again with `jig gate %s`", p.Ticket)},
		}
	}
	if tip, err := BranchMoved(d, p); err != nil {
		return GateReport{}, err
	} else if tip != "" {
		return GateReport{}, &axi.Error{
			Msg:  fmt.Sprintf("branch %s moved to %s since round %d opened on %s", p.Branch, shortSHA(tip), p.Round, shortSHA(p.Head)),
			Code: "REVIEW_STALE_HEAD",
			Help: []string{fmt.Sprintf("A verdict on %s says nothing of the branch as it is now: run `jig gate %s` to review that", shortSHA(p.Head), p.Ticket)},
		}
	}

	slices, err := d.Store.ReadSlices(p.Ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: read slices: %w", err)
	}
	man, err := manifest.Resolve(p.Lease)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: resolve manifest: %w", err)
	}
	fold, err := FoldBefore(d.Store, p.Ticket, p.Round)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: fold findings: %w", err)
	}
	var openFiles []string
	for _, f := range fold.Open {
		if f.Status == StatusOpen || f.Status == StatusAsked {
			openFiles = append(openFiles, f.File)
		}
	}
	diff, err := computeScopeDiff(p.Lease, p.Base, p.Head, openFiles)
	if err != nil {
		return GateReport{}, err
	}
	in := RoundInput{Store: d.Store, Ticket: p.Ticket, Round: p.Round, LeaseDir: p.Lease, Home: d.Home}
	rr, err = checkReviewResult(p.Request, rr, in, diff.Deleted)
	if err != nil {
		return GateReport{}, err
	}

	// Who reported, and whether they built what they reviewed.
	if !prov.Known() {
		prov.Session, prov.Agent, prov.How = p.Reviewer.Session, p.Reviewer.Agent, p.Reviewer.How
	}
	if !prov.Known() || (prov.How == ReviewerByEnv && prov.Agent == "") {
		// A session from the environment is the harness session, which a
		// subagent shares with its parent and so with builders it never was:
		// the ledger's record of who read the round, when it is in that
		// session, says which agent did.
		if found, ok, err := LedgerReviewer(d, p.Ticket, p.Opened); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: review done: read the ledger: %w", err)
		} else if ok && !prov.Known() {
			prov.Session, prov.Agent, prov.How = found.Session, found.Agent, found.How
		} else if ok && found.Session == prov.Session {
			prov.Agent = found.Agent
		}
	}
	if prov.Model == "" {
		prov.Model = p.Reviewer.Model
	}
	if prov.Effort == "" {
		prov.Effort = p.Reviewer.Effort
	}
	independent, err := JudgeIndependence(d, p.Ticket, p.Branch, prov)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: judge independence: %w", err)
	}

	// The reviewer's result is kept in the store's work directory beside the
	// request, as a launched reviewer's was: it is what the round was
	// recorded from.
	resultData, err := marshalResult(rr)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: marshal result.json: %w", err)
	}
	run = &gateRun{
		d: d, o: GateOpts{Ticket: p.Ticket, Yes: p.Yes}, ticket: p.Ticket, slices: slices,
		repoName: p.Repo, target: p.Target, gb: gateBranch{Name: p.Branch, Adopted: p.Adopted}, branch: p.Branch,
		lease: pool.Lease{Dir: p.Lease, Repo: p.Repo, Branch: p.Branch}, copy: branchCopy(p.Copy),
		intent: p.Intent, intentSHA256: p.IntentSHA256,
		n: p.Round, man: man, fold: fold, journaled: true,
	}
	if err := os.WriteFile(reviewResultJSONPath(d.Store, p.Ticket, p.Round), append(resultData, '\n'), 0o644); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: write result.json: %w", err)
	}
	review := &Review{
		Scope: p.Scope, BaseSHA: p.Base, HeadSHA: p.Head,
		Changed: diff.Changed, Deleted: diff.Deleted, MustReview: p.Request.MustReview,
		Result: rr, Intent: p.Intent, IntentSHA256: p.IntentSHA256, IntentNote: p.IntentNote,
		Effort: prov.Effort,
	}
	return run.finish(Round{Review: review}, true, finishInfo{
		Triage: recordedTriage(d, p.Ticket, p.Yes),
		Model:  prov.Model, Effort: prov.Effort,
		Reviewer: prov, Independent: independent,
		OracleNames: p.Request.Oracles,
	})
}

// recordedTriage is the Triage of a recorded round. Nobody is asked anything
// while it closes: a fix is kept, as DefaultTriage keeps it, and an ask is
// decided by what a person recorded with `jig triage` (a journal triage line
// for its finding id, the last one standing), else, when the round was
// opened with --yes, by DefaultTriage's rule, else left undecided for a
// person. A decision recorded after the round that raised the ask applies to
// the next round, whose fold offers the ask again.
func recordedTriage(d Deps, ticket string, yes bool) Triage {
	return func(in TriageInput) TriageResult {
		decisions := map[string]journal.Line{}
		if lines, err := journal.Read(d.Store, ticket); err == nil {
			for _, l := range lines {
				if l.Event == journal.EventTriage && l.Ref != "" {
					decisions[l.Ref] = l
				}
			}
		}
		res := TriageResult{Asks: map[string]AskOutcome{}}
		fallback := DefaultTriage(in)
		for _, f := range in.Asks {
			if l, ok := decisions[f.ID]; ok {
				ws, oracle := triageTarget(l.Command)
				res.Asks[f.ID] = AskOutcome{
					Keep: l.Outcome == TriageKeep, Decision: l.Summary,
					Workspace: ws, Oracle: oracle, Human: l.Provenance == "person",
				}
				continue
			}
			if yes {
				if out, ok := fallback.Asks[f.ID]; ok {
					res.Asks[f.ID] = out
				}
			}
		}
		return res
	}
}

// The outcomes of a `jig triage` line, in its Outcome.
const (
	TriageKeep    = "keep"
	TriageDismiss = "dismiss"
)

// TriageTarget encodes the build target a kept ask lacked (the workspace its
// file lies in, the oracle that proves the fix), the person's choice, in the
// triage line's Command.
func TriageTarget(workspace, oracle string) string {
	var parts []string
	if workspace != "" {
		parts = append(parts, "workspace="+workspace)
	}
	if oracle != "" {
		parts = append(parts, "oracle="+oracle)
	}
	return strings.Join(parts, " ")
}

// triageTarget decodes TriageTarget.
func triageTarget(command string) (workspace, oracle string) {
	for _, part := range strings.Fields(command) {
		if v, ok := strings.CutPrefix(part, "workspace="); ok {
			workspace = v
		}
		if v, ok := strings.CutPrefix(part, "oracle="); ok {
			oracle = v
		}
	}
	return workspace, oracle
}
