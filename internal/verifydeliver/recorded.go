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
	"maps"
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

// openRepoFile is what an open round settled of one repo of a project of
// several: the openRoundFile's own repo, copy, head, base, scope, target,
// target_sha and lease, for each repo the round covers.
type openRepoFile struct {
	Repo      string `yaml:"repo"`
	Copy      string `yaml:"copy"`
	Head      string `yaml:"head"`
	Base      string `yaml:"base"`
	Scope     string `yaml:"scope"`
	Target    string `yaml:"target"`
	TargetSHA string `yaml:"target_sha"`
	Lease     string `yaml:"lease"`
}

// openRoundFile is work/gate.round-N.open.yaml: what jig settled when it
// opened round N, and what `review done` needs to close it in another
// process.
//
// A round over the one repo of a project of one writes that repo's settings
// here, at the top. A round in a project of several writes them in Repos, one
// entry for each repo the ticket's slices build in, and leaves the top out.
type openRoundFile struct {
	Round  int    `yaml:"round"`
	Ticket string `yaml:"ticket"`
	Repo   string `yaml:"repo,omitempty"`
	Branch string `yaml:"branch"`
	// Copy says whose copy of the branch the round reviews.
	Copy      string `yaml:"copy,omitempty"`
	Head      string `yaml:"head,omitempty"`
	Base      string `yaml:"base,omitempty"`
	Scope     string `yaml:"scope,omitempty"`
	Target    string `yaml:"target,omitempty"`
	TargetSHA string `yaml:"target_sha,omitempty"`
	// Lease is the gate lease the round's head is checked out in: the
	// pristine worktree the reviewer reads. It, IntentExcerpt and Recordings
	// are paths under the jig home, written relative to it: the record is in
	// the store, which is shared and must not name this machine's directories.
	Lease   string           `yaml:"lease,omitempty"`
	Repos   []openRepoFile   `yaml:"repos,omitempty"`
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

// PreparedRepo is what an open round settled of one repo of a project of
// several: Prepared's own Repo, Copy, Head, Base, Scope, Target, TargetSHA and
// Lease.
type PreparedRepo struct {
	Repo              string
	Copy              string
	Head, Base, Scope string
	Target, TargetSHA string
	Lease             string
}

// Prepared is an open round: what GatePrepare settled, and the request a
// reviewer is handed. GateFinish closes it.
//
// A round in a project of one repo has that repo's settings in Repo, Copy,
// Head, Base, Scope, Target, TargetSHA and Lease. A round in a project of
// several has them in Repos, one entry for each repo the ticket's slices
// build in, and leaves those fields empty (Covered gives either as a list).
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
	Lease string
	// Repos are the repos of a round in a project of several.
	Repos        []PreparedRepo
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
	var repos []PreparedRepo
	if len(of.Repos) > 0 {
		req.Repos = append([]RepoRequest(nil), req.Repos...)
		leases := map[string]string{}
		for _, r := range of.Repos {
			lease := homeAbs(jigHome, r.Lease)
			leases[r.Repo] = lease
			repos = append(repos, PreparedRepo{
				Repo: r.Repo, Copy: r.Copy, Head: r.Head, Base: r.Base, Scope: r.Scope,
				Target: r.Target, TargetSHA: r.TargetSHA, Lease: lease,
			})
		}
		for i := range req.Repos {
			req.Repos[i].Worktree = leases[req.Repos[i].Repo]
		}
	}
	return Prepared{
		Ticket: of.Ticket, Round: of.Round, Repo: of.Repo, Branch: of.Branch, Copy: of.Copy,
		Head: of.Head, Base: of.Base, Scope: of.Scope, Target: of.Target, TargetSHA: of.TargetSHA,
		Lease: req.Worktree, Repos: repos, Adopted: of.Adopted,
		Intent: req.Intent, IntentSHA256: of.Intent.SHA256, IntentNote: of.IntentNote,
		Yes: of.Yes, Opened: opened, Reviewer: of.Reviewer, Request: req,
		RequestPath: requestPath,
		ReviewPath:  reviewJSONPath(st, of.Ticket, of.Round),
		OpenPath:    openRoundPath(st, of.Ticket, of.Round),
		DraftsPath:  findingsDraftsPath(st, of.Ticket, of.Round),
	}
}

// heads is the commit under review in each repo of the round, by repo name.
func (of openRoundFile) heads() map[string]string {
	if len(of.Repos) == 0 {
		return map[string]string{of.Repo: of.Head}
	}
	heads := make(map[string]string, len(of.Repos))
	for _, r := range of.Repos {
		heads[r.Repo] = r.Head
	}
	return heads
}

// qualified reports whether the round is over the repos of a project of
// several, whose files are named "<repo>/<path>".
func (p Prepared) qualified() bool { return len(p.Repos) > 0 }

// Covered lists the repos the round covers: Repos, or for a round over the one
// repo of a project of one, that repo.
func (p Prepared) Covered() []PreparedRepo {
	if p.qualified() {
		return p.Repos
	}
	return []PreparedRepo{{
		Repo: p.Repo, Copy: p.Copy, Head: p.Head, Base: p.Base, Scope: p.Scope,
		Target: p.Target, TargetSHA: p.TargetSHA, Lease: p.Lease,
	}}
}

// roundInput is the input of the round as far as its files are checked: its
// leases and where its request is kept.
func (p Prepared) roundInput(d Deps) RoundInput {
	in := RoundInput{Store: d.Store, Ticket: p.Ticket, Round: p.Round, Home: d.Home, Branch: p.Branch}
	if !p.qualified() {
		in.LeaseDir, in.RepoName, in.Target = p.Lease, p.Repo, p.Target
		return in
	}
	for _, r := range p.Repos {
		in.Repos = append(in.Repos, RoundRepo{Name: r.Repo, Target: r.Target, LeaseDir: r.Lease})
	}
	return in
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
		Round: run.n, Ticket: run.ticket, Branch: run.branch, Adopted: run.gb.Adopted,
		Intent:        reportIntentYAML{Source: run.intent.Source, SHA256: run.intentSHA256},
		IntentExcerpt: homeRel(d.Home, prep.Request.IntentExcerpt), IntentNote: prep.IntentNote,
		Recordings: homeRel(d.Home, prep.RecordingsPath), Request: homeRel(d.Home, prep.RequestPath), Yes: o.Yes,
		Opened: time.Now().UTC().Format(time.RFC3339Nano),
	}
	for i, gr := range run.repos {
		rp := prep.Repos[i]
		tip, err := originTip(gr.lease.Dir, gr.target)
		if err != nil {
			return Prepared{}, inRepo(run.qualified, gr.name, err)
		}
		repo := openRepoFile{
			Repo: gr.name, Copy: string(gr.copy), Head: rp.Head, Base: rp.Base, Scope: rp.Scope,
			Target: gr.target, TargetSHA: tip, Lease: homeRel(d.Home, absPath(gr.lease.Dir)),
		}
		if run.qualified {
			of.Repos = append(of.Repos, repo)
			continue
		}
		of.Repo, of.Copy, of.Head, of.Base, of.Scope = repo.Repo, repo.Copy, repo.Head, repo.Base, repo.Scope
		of.Target, of.TargetSHA, of.Lease = repo.Target, repo.TargetSHA, repo.Lease
	}
	if hadPrev && maps.Equal(prev.heads(), of.heads()) {
		// The round is the same and so is its reviewer, which the journal
		// says again after this gate-open, so a reader of the journal alone
		// still knows who is reading the round. It opened when it first did:
		// the reads of it since then (the ledger's records of review context,
		// which tell a subagent reviewer from its session) still count.
		of.Reviewer, of.Opened = prev.Reviewer, prev.Opened
		if of.Reviewer.Known() {
			if err := journalReviewer(d, run.ticket, run.n, of.Reviewer); err != nil {
				return Prepared{}, err
			}
		}
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

// RepoTip is a repo of a round and the commit its branch is at.
type RepoTip struct {
	Repo string
	Tip  string
}

// MovedRepos reports, for each repo whose clone's branch has moved since the
// open round reviewed the clone's copy of it, the commit the branch is at now:
// the round's head is then not the branch's, and a verdict on it would be about
// code nobody ships. A repo whose branch has not moved, or whose copy the round
// did not read from the clone, which jig cannot cheaply look at again, is left
// out.
func MovedRepos(d Deps, p Prepared) ([]RepoTip, error) {
	var moved []RepoTip
	for _, r := range p.Covered() {
		if r.Copy != string(clonesCopy) {
			continue
		}
		clone := operatorClone(d, r.Repo)
		if clone == "" {
			continue
		}
		tip, err := branchTipIn(clone, p.Branch)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: %w", err)
		}
		if tip != "" && tip != r.Head {
			moved = append(moved, RepoTip{Repo: r.Repo, Tip: tip})
		}
	}
	return moved, nil
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
	// The journal says who is reading the round as soon as jig knows, so a
	// watcher (jig tui) shows the round being reviewed rather than waiting
	// for a reviewer until it closes. Whether to say it is the journal's
	// own question, not the open record's: a reopen that failed after its
	// gate-open, or an append refused before the record was written, leaves
	// the record naming a reviewer the journal has not named since, and the
	// next review context says it then.
	said, err := journaledReviewer(d, p.Ticket, p.Round)
	if err != nil {
		return Prepared{}, err
	}
	named := of.Reviewer.Known() && (said.Session != of.Reviewer.Session || said.Agent != of.Reviewer.Agent)
	if named {
		if err := journalReviewer(d, p.Ticket, p.Round, of.Reviewer); err != nil {
			return Prepared{}, err
		}
	}
	if err := writeOpenFile(d.Store, of); err != nil {
		return Prepared{}, err
	}
	p.Reviewer = of.Reviewer
	if named {
		// Best effort: the round's close pushes the line if this one could not.
		_ = d.Store.Push(fmt.Sprintf("%s: gate round %d is being reviewed", p.Ticket, p.Round))
	}
	return p, nil
}

// journaledReviewer is who the journal last said is reading round n of
// ticket since the round last opened: the latest gate-reviewer line after
// the latest gate-open, or no one.
func journaledReviewer(d Deps, ticket string, n int) (Provenance, error) {
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return Provenance{}, fmt.Errorf("verifydeliver: read the journal: %w", err)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		switch l := lines[i]; {
		case l.Event == journal.EventGateOpen:
			return Provenance{}, nil
		case l.Event == journal.EventGateReviewer && l.Attempt == n:
			return Provenance{Session: l.Session, Agent: l.Agent}, nil
		}
	}
	return Provenance{}, nil
}

// journalReviewer writes the gate-reviewer line: who is reading round n of
// ticket, and what they said they run on.
func journalReviewer(d Deps, ticket string, n int, who Provenance) error {
	line := journal.Line{
		Event: journal.EventGateReviewer, Attempt: n,
		Session: who.Session, Agent: who.Agent, Model: who.Model, Effort: who.Effort,
	}
	if err := journal.Append(d.Store, ticket, line); err != nil {
		return fmt.Errorf("verifydeliver: journal gate-reviewer: %w", err)
	}
	return nil
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
// reports, an earlier finding it confirms is still present, or a probe or a
// skip (ADR 0036). A round's drafts are its ReviewResult, said a piece at a
// time.
type Draft struct {
	Finding      *ResultFinding     `json:"finding,omitempty"`
	StillPresent *StillPresentEntry `json:"still_present,omitempty"`
	Probe        *ResultProbe       `json:"probe,omitempty"`
	Skip         *ResultSkip        `json:"skip,omitempty"`
}

// kinds counts the parts of the draft that are set: exactly one is.
func (dr Draft) kinds() int {
	n := 0
	for _, set := range []bool{dr.Finding != nil, dr.StillPresent != nil, dr.Probe != nil, dr.Skip != nil} {
		if set {
			n++
		}
	}
	return n
}

// AppendDraft checks d against the open round and appends it to the round's
// findings drafts. What it checks is what needs no other draft: the fields
// ParseReviewResult checks of a finding, a probe or a skip, an oracle that is
// one of the manifest's, a prior that names a finding under open or
// dismissed, and a file of a probe or a skip in a repo of the round. That the
// file exists at the head, that no two drafts share a prior and that the
// reviewed paths cover must_review are checked when the round is done. It
// returns how many drafts the round has now.
func AppendDraft(d Deps, p Prepared, dr Draft) (int, error) {
	if dr.kinds() != 1 {
		return 0, &axi.Error{Msg: "a draft is one of a finding, a still_present entry, a probe or a skip", Code: "VALIDATION_ERROR"}
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
		// A round over several repos names a file "<repo>/<path>", and the
		// oracles a finding can name are those of its repo.
		rf := p.Request.files()
		file, err := normalizeRepoRelPath(f.File)
		if err != nil {
			return 0, reviewInvalid(fmt.Sprintf("file %q: %v", f.File, err))
		}
		if _, _, ok := rf.of(file); !ok {
			return 0, reviewInvalid(rf.unknownRepoFile(f.File))
		}
		oracles := rf.oracles(file)
		if f.Oracle != "" && !inStrings(oracles, f.Oracle) {
			return 0, reviewInvalid(fmt.Sprintf("oracle %q is not a manifest oracle (%s)", f.Oracle, strings.Join(oracles, ", ")))
		}
		if f.Oracle == "" && len(oracles) > 1 && (f.Action == ActionFix || f.Action == ActionAsk) {
			return 0, reviewInvalid(fmt.Sprintf("a finding with action %q must name one of the manifest's %d oracles (%s)", f.Action, len(oracles), strings.Join(oracles, ", ")))
		}
		if f.Prior != "" && !known[f.Prior] {
			return 0, reviewInvalid(fmt.Sprintf("prior %q does not name a finding under open or dismissed", f.Prior))
		}
	case dr.Probe != nil:
		if err := checkResultProbe(len(existing), *dr.Probe); err != nil {
			return 0, err
		}
		if err := p.roundInput(d).requestFiles(p.Request).placeLedgerFile(dr.Probe.File); err != nil {
			return 0, err
		}
	case dr.Skip != nil:
		if err := checkResultSkip(len(existing), *dr.Skip); err != nil {
			return 0, err
		}
		if err := p.roundInput(d).requestFiles(p.Request).placeLedgerFile(dr.Skip.File); err != nil {
			return 0, err
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
		if dr.Probe != nil {
			res.Probes = append(res.Probes, *dr.Probe)
		}
		if dr.Skip != nil {
			res.Skipped = append(res.Skipped, *dr.Skip)
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
// round is a verdict on the branch as it is. A round over several repos checks
// each repo's lease and branch. prov is who the caller says reported (a zero
// Session: jig looks for them, LedgerReviewer, and otherwise says it does not
// know); whether they built what they reviewed decides independent - over the
// builders of every repo the ticket's slices build in, since the ledger and the
// journal name a builder by the ticket and its branch - and a reviewer who did
// makes the round not clean (REVIEW_NOT_INDEPENDENT, returned with the report
// of the round that was recorded).
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

	qualified := p.qualified()
	for _, r := range p.Covered() {
		if !pool.Usable(r.Lease) {
			return GateReport{}, inRepo(qualified, r.Repo, &axi.Error{
				Msg:  fmt.Sprintf("the gate lease of round %d is gone", p.Round),
				Code: "REVIEW_NOT_OPEN",
				Help: []string{fmt.Sprintf("Open the round again with `jig gate %s`", p.Ticket)},
			})
		}
		changed, err := leaseChanged(r.Lease, r.Head)
		if err != nil {
			return GateReport{}, inRepo(qualified, r.Repo, fmt.Errorf("verifydeliver: review done: %w", err))
		}
		if changed {
			return GateReport{}, inRepo(qualified, r.Repo, &axi.Error{
				Msg:  "the gate lease changed since the round opened; a reviewer reads it and edits nothing",
				Code: "REVIEW_INVALID",
				Help: []string{fmt.Sprintf("Open the round again with `jig gate %s`", p.Ticket)},
			})
		}
	}
	if moved, err := MovedRepos(d, p); err != nil {
		return GateReport{}, err
	} else if len(moved) > 0 {
		r := p.reposByName()[moved[0].Repo]
		return GateReport{}, inRepo(qualified, r.Repo, &axi.Error{
			Msg:  fmt.Sprintf("branch %s moved to %s since round %d opened on %s", p.Branch, shortSHA(moved[0].Tip), p.Round, shortSHA(r.Head)),
			Code: "REVIEW_STALE_HEAD",
			Help: []string{fmt.Sprintf("A verdict on %s says nothing of the branch as it is now: run `jig gate %s` to review that", shortSHA(r.Head), p.Ticket)},
		})
	}

	slices, err := d.Store.ReadSlices(p.Ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: read slices: %w", err)
	}
	fold, err := FoldBefore(d.Store, p.Ticket, p.Round)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: fold findings: %w", err)
	}
	in := p.roundInput(d)
	rf := in.requestFiles(p.Request)
	var openFiles []string
	for _, f := range fold.Open {
		if f.Status == StatusOpen || f.Status == StatusAsked {
			openFiles = append(openFiles, f.File)
		}
	}
	openByRepo := rf.byRepo(openFiles)
	covered := p.Covered()
	repos := make([]*gateRepo, len(covered))
	var diff scopeDiff
	for i, r := range covered {
		man, err := manifest.Resolve(r.Lease)
		if err != nil {
			return GateReport{}, inRepo(qualified, r.Repo, fmt.Errorf("verifydeliver: review done: resolve manifest: %w", err))
		}
		repos[i] = &gateRepo{
			name: r.Repo, target: r.Target, man: man, copy: branchCopy(r.Copy),
			lease: pool.Lease{Dir: r.Lease, Repo: r.Repo, Branch: p.Branch},
		}
		one, err := computeScopeDiff(r.Lease, r.Base, r.Head, openByRepo[r.Repo])
		if err != nil {
			return GateReport{}, inRepo(qualified, r.Repo, err)
		}
		if !qualified {
			diff = one
			continue
		}
		diff.Changed = append(diff.Changed, rf.qualifyAll(r.Repo, one.Changed)...)
		diff.Deleted = append(diff.Deleted, rf.qualifyAll(r.Repo, one.Deleted)...)
	}
	rr, err = checkReviewResult(p.Request, rr, in, diff.Deleted)
	if err != nil {
		return GateReport{}, err
	}

	// Who reported, and whether they built what they reviewed.
	if !prov.Known() {
		prov.Session, prov.Agent, prov.How = p.Reviewer.Session, p.Reviewer.Agent, p.Reviewer.How
	}
	if !prov.Known() {
		if found, ok, err := LedgerReviewer(d, p.Ticket, p.Opened); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: review done: read the ledger: %w", err)
		} else if ok {
			prov.Session, prov.Agent, prov.How = found.Session, found.Agent, found.How
		}
	}
	if prov.How == ReviewerByEnv && prov.Agent == "" {
		// A session from the environment is the harness session, which a
		// subagent shares with its parent and so with builders it never was:
		// the ledger says which agent of that session read the round.
		if agent, ok, err := LedgerReviewerAgent(d, p.Ticket, p.Opened, prov.Session); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: review done: read the ledger: %w", err)
		} else if ok {
			prov.Agent = agent
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
		repos: repos, qualified: qualified, gb: gateBranch{Name: p.Branch, Adopted: p.Adopted}, branch: p.Branch,
		intent: p.Intent, intentSHA256: p.IntentSHA256,
		n: p.Round, fold: fold, journaled: true,
	}
	if err := os.WriteFile(reviewResultJSONPath(d.Store, p.Ticket, p.Round), append(resultData, '\n'), 0o644); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: review done: write result.json: %w", err)
	}
	review := &Review{
		Changed: diff.Changed, Deleted: diff.Deleted, MustReview: p.Request.MustReview,
		Result: rr, Intent: p.Intent, IntentSHA256: p.IntentSHA256, IntentNote: p.IntentNote,
		Effort: prov.Effort, Hunks: p.Request.Hunks,
	}
	if qualified {
		for _, r := range covered {
			review.Repos = append(review.Repos, ReviewedRepo{Repo: r.Repo, Scope: r.Scope, BaseSHA: r.Base, HeadSHA: r.Head})
		}
	} else {
		review.Scope, review.BaseSHA, review.HeadSHA = p.Scope, p.Base, p.Head
	}
	return run.finish(Round{Review: review}, true, finishInfo{
		Triage: recordedTriage(d, p.Ticket, p.Yes),
		Model:  prov.Model, Effort: prov.Effort,
		Reviewer: prov, Independent: independent,
		OracleNames: p.Request.Oracles,
	})
}

// reposByName indexes the repos the round covers by name.
func (p Prepared) reposByName() map[string]PreparedRepo {
	by := map[string]PreparedRepo{}
	for _, r := range p.Covered() {
		by[r.Repo] = r
	}
	return by
}

// recordedTriage is the Triage of a recorded round. Nobody is asked anything
// while it closes: a fix is kept, as DefaultTriage keeps it, and an ask is
// decided by what a person recorded with `jig triage` (a journal triage line
// for its finding id, the last one standing, TriageDecisions), else, when the
// round was opened with --yes, by DefaultTriage's rule, else left undecided
// for a person. A decision recorded after the round that raised the ask applies
// to the next round, whose fold offers the ask again.
//
// The line is taken whatever round it was recorded before: a triage line is
// never marked used, so an ask that a later round raises again silently takes
// its old decision (TriageDecision.Pending is how `jig status` tells the two
// apart, and shows only a pending one as decided).
func recordedTriage(d Deps, ticket string, yes bool) Triage {
	return func(in TriageInput) TriageResult {
		decisions, _ := ReadTriageDecisions(d.Store, ticket)
		res := TriageResult{Asks: map[string]AskOutcome{}}
		fallback := DefaultTriage(in)
		for _, f := range in.Asks {
			if l, ok := decisions[f.ID]; ok {
				res.Asks[f.ID] = AskOutcome{
					Keep: l.Outcome == TriageKeep, Decision: l.Reason,
					Workspace: l.Workspace, Oracle: l.Oracle, Human: l.Human(),
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
