package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// Round is one gate round's content, whether played back by a fake source
// (tests) or produced by a real reviewer session. Review is nil for the
// scripted source's old fix-slices/clean shape; non-nil for a real
// reviewer round (see review.go).
type Round struct {
	FindingsMD string
	FixSlices  []store.Slice
	Receipts   map[string][]byte
	Review     *Review
}

// RoundInput is what Gate knows about a round when it asks a source for
// it.
type RoundInput struct {
	Store     *store.Store
	Ticket    string
	Round     int
	LeaseDir  string // gate lease, checked out at the branch under review
	RepoName  string
	Target    string // target branch, e.g. "main"
	Model     string // the gate model (staircase.Disjoint)
	BriefPath string // absolute; see Brief path in review.go
	Manifest  manifest.Manifest
}

// GateSource supplies one gate round's content. Round(in) returns
// ok=false for a clean round (no round directory, or an empty one); the
// real reviewer source always returns ok=true when the review ran (a
// clean review still carries closures, summary and reviewed_sha).
type GateSource interface {
	Round(in RoundInput) (Round, bool, error)
}

// fakeGateSource reads gate rounds from a materialized fixture scenario
// tree (scenarioDir/gate/round-<n>/).
type fakeGateSource struct {
	scenarioDir string
}

// NewFakeGateSource returns a GateSource that reads scenario rounds from
// scenarioDir/gate/round-<n>/: findings.md, fix-slices.yaml (a
// store.SliceFile), and receipts/* (any files, keyed by base name).
func NewFakeGateSource(scenarioDir string) GateSource {
	return &fakeGateSource{scenarioDir: scenarioDir}
}

func (f *fakeGateSource) Round(in RoundInput) (Round, bool, error) {
	n := in.Round
	dir := filepath.Join(f.scenarioDir, "gate", fmt.Sprintf("round-%d", n))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Round{}, false, nil
		}
		return Round{}, false, fmt.Errorf("verifydeliver: read scenario round %d: %w", n, err)
	}
	if len(entries) == 0 {
		return Round{}, false, nil
	}

	findings, err := os.ReadFile(filepath.Join(dir, "findings.md"))
	if err != nil {
		return Round{}, false, fmt.Errorf("verifydeliver: read scenario round %d findings.md: %w", n, err)
	}

	var fixSlices []store.Slice
	if data, err := os.ReadFile(filepath.Join(dir, "fix-slices.yaml")); err == nil {
		var sf store.SliceFile
		if err := yaml.Unmarshal(data, &sf); err != nil {
			return Round{}, false, fmt.Errorf("verifydeliver: parse scenario round %d fix-slices.yaml: %w", n, err)
		}
		fixSlices = sf.Slices
	} else if !os.IsNotExist(err) {
		return Round{}, false, err
	}

	receipts := map[string][]byte{}
	if recEntries, err := os.ReadDir(filepath.Join(dir, "receipts")); err == nil {
		for _, e := range recEntries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, "receipts", e.Name()))
			if err != nil {
				return Round{}, false, err
			}
			receipts[e.Name()] = data
		}
	}

	return Round{FindingsMD: string(findings), FixSlices: fixSlices, Receipts: receipts}, true, nil
}

// GateOpts configures one Gate invocation.
type GateOpts struct {
	Ticket   string
	Early    bool
	Branch   string // validate this branch instead of jig/<ticket>
	BriefDoc string // spec-axis input when Branch is set
	PRMode   bool
	// Triage receives a real reviewer round's open findings (only called
	// when there is at least one) and returns the kept subset, matched
	// back by ID; every open finding not returned becomes dismissed. nil
	// keeps all. Never used by the scripted source. No IO happens inside
	// verifydeliver; cmd wires the interactive prompt, tests wire
	// identity/dismisser stubs.
	Triage func([]Finding) []Finding
}

// GateReport is Gate's result. Scope, Findings and FixSlices are set only
// for a real reviewer round; the scripted source leaves them zero.
type GateReport struct {
	Round       int
	Verdict     string // clean|fix-slices
	TargetSHA   map[string]string
	Model       string
	Scope       string            // full|delta, reviewer rounds only
	Findings    []Finding         // final statuses, reviewer rounds only
	FixSlices   []string          // appended fix-slice ids, reviewer rounds only
	ReviewedSHA map[string]string // repoName -> HeadSHA, reviewer rounds only
}

// reportYAML is gate/round-<n>/report.yaml's exact on-disk shape.
type reportYAML struct {
	Round       int               `yaml:"round"`
	Verdict     string            `yaml:"verdict"`
	Model       string            `yaml:"model"`
	TargetSHA   map[string]string `yaml:"target_sha"`
	ReviewedSHA map[string]string `yaml:"reviewed_sha,omitempty"`
}

// Gate runs one gate round for ticket: it re-verifies every manifest
// oracle in a fresh gate lease checked out to the ticket's branch, then
// asks src for this round's review content.
func Gate(d Deps, src GateSource, o GateOpts) (GateReport, error) {
	if o.PRMode {
		return GateReport{}, &axi.Error{Msg: "gate pr-mode ships in v0.2", Code: "NOT_IMPLEMENTED"}
	}

	ticket := o.Ticket
	if err := d.Store.Sync(); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: sync store: %w", err)
	}

	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: read slices: %w", err)
	}
	if err := checkFrontier(d, ticket, slices, o.Early); err != nil {
		return GateReport{}, err
	}

	repo, repoName, target := primaryRepo(d.Cfg)
	branch := ticketBranch(ticket)
	// --branch: validate a hand-written branch fetched from origin instead
	// of the ticket's own jig/<ticket>. Its spec axis reads opts.BriefDoc
	// instead of the brief; report.yaml's shape stays fixed by contract to
	// {round,verdict,model,target_sha} on a scripted round (a reviewer round
	// also carries reviewed_sha; BriefDoc is not recorded there either way),
	// but the doc's content is copied into this round's own
	// gate/round-<n>/spec-input.md so the spec-axis-input swap is real
	// rather than an accepted, no-op flag.
	var briefDocContent []byte
	// briefPath is the review dispatch's brief_path (D1): the ticket's own
	// brief.md normally, or the --doc file itself in --branch --doc mode.
	// Pointing at the round's spec-input.md instead would leave a partial
	// round directory behind whenever the reviewer fails, since that file
	// is written only after the round completes.
	briefPath := absPath(filepath.Join(d.Store.TicketDir(ticket), "brief.md"))
	if o.Branch != "" {
		branch = o.Branch
		if o.BriefDoc != "" {
			data, err := os.ReadFile(o.BriefDoc)
			if err != nil {
				return GateReport{}, &axi.Error{
					Msg:  fmt.Sprintf("brief doc %q is set but unreadable: %v", o.BriefDoc, err),
					Code: "BRIEF_DOC_MISSING",
				}
			}
			briefDocContent = data
			briefPath = absPath(o.BriefDoc)
		}
	}
	leaseKey := ticket + "-gate"
	// Restore an existing gate lease pristine at its current HEAD before
	// Acquire ever touches it. A reviewer that outlived a killed jig, or an
	// oracle rewrite left over from an earlier attempt, can leave tracked
	// dirt in the lease; if the ticket branch later advances past whatever
	// file that dirt touched, Acquire's own `git checkout` (and, in normal
	// mode, fetchTicketBranchFromBuildLease's checkout right after) refuses
	// with "local changes ... would be overwritten" before the restore below
	// ever runs, wedging every later attempt at the same point. This restore
	// is best-effort: if the pool dir cannot be resolved, or the lease does
	// not exist yet, Acquire runs unchanged and surfaces its own error.
	if poolDir, perr := home.PoolDir(); perr == nil {
		leaseDir := filepath.Join(poolDir, repoName, leaseKey)
		if isGitLeaseDir(leaseDir) {
			if err := resetLeasePristine(leaseDir, "HEAD"); err != nil {
				return GateReport{}, fmt.Errorf("verifydeliver: gate: restore existing lease before acquire: %w", err)
			}
		}
	}
	lease, err := pool.Acquire(repoName, repo.Remote, target, branch, leaseKey)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: acquire lease: %w", err)
	}

	// Restore the gate lease to a pristine, known-correct head right after
	// acquire and before any oracle runs. pool.Acquire never resets an
	// existing local branch (a deliberate rule so a same-run slice's commits
	// on it survive later acquires), so without this, a reviewer or an
	// oracle that left the lease dirty or ahead on an earlier, killed jig
	// (NM2) has its leftovers reviewed by this round's own oracles, or in
	// --branch mode makes this gate review the stale local copy instead of
	// origin's current branch tip.
	if o.Branch == "" {
		if err := fetchTicketBranchFromBuildLease(lease.Dir, repoName, ticket); err != nil {
			return GateReport{}, err
		}
		if err := resetLeasePristine(lease.Dir, "HEAD"); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: restore lease before oracles: %w", err)
		}
	} else {
		// pool.Acquire just fetched origin, so refs/remotes/origin/<branch>
		// is current. The gate lease never commits (reviewers and oracles are
		// always undone), so it must always equal origin/<branch> exactly.
		if _, err := gitx.RevParse(lease.Dir, "refs/remotes/origin/"+branch); err != nil {
			return GateReport{}, &axi.Error{
				Msg:  fmt.Sprintf("branch %q does not exist on origin", branch),
				Code: "BRANCH_NOT_FOUND",
			}
		}
		if err := resetLeasePristine(lease.Dir, "origin/"+branch); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: restore lease to origin/%s: %w", branch, err)
		}
	}

	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: read journal: %w", err)
	}
	model := staircase.Disjoint(d.Rungs, journal.BuilderModels(lines))
	if err := journal.Append(d.Store, ticket, journal.Line{Slice: "", Event: "gate-open", Model: model}); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: journal gate-open: %w", err)
	}

	man, err := manifest.Resolve(lease.Dir)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: resolve manifest: %w", err)
	}

	if err := runGateOracles(d, ticket, lease.Dir, man, slices); err != nil {
		return GateReport{}, err
	}

	n, err := existingGateRounds(d.Store, ticket)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: count rounds: %w", err)
	}
	n++
	roundDir := gateRoundDir(d.Store, ticket, n)
	if _, err := os.Stat(roundDir); err == nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: round %d already exists", n)
	}

	round, ok, err := src.Round(RoundInput{
		Store:     d.Store,
		Ticket:    ticket,
		Round:     n,
		LeaseDir:  lease.Dir,
		RepoName:  repoName,
		Target:    target,
		Model:     model,
		BriefPath: briefPath,
		Manifest:  man,
	})
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: read round %d: %w", n, err)
	}

	targetSHA, err := gitx.RevParse(lease.Dir, "origin/"+target)
	if err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: resolve origin/%s: %w", target, err)
	}

	report := GateReport{Round: n, Model: model, TargetSHA: map[string]string{repoName: targetSHA}}
	switch {
	case !ok:
		// Old scripted-clean path: unchanged.
		report.Verdict = "clean"
		if err := writeCleanRound(d, ticket, n, report); err != nil {
			return GateReport{}, err
		}
	case round.Review == nil:
		// Old scripted-fix-slices path: unchanged.
		report.Verdict = "fix-slices"
		if err := writeFixRound(d, ticket, n, report, round); err != nil {
			return GateReport{}, err
		}
	default:
		// Real reviewer round: triage the open findings, synthesize fix
		// slices from the kept ones only, then render the round files.
		rv := round.Review
		finalFindings, kept := triageOpenFindings(rv.Findings, o.Triage)
		fixSlices, err := synthesizeFixSlices(n, kept, man)
		if err != nil {
			return GateReport{}, err
		}
		if len(fixSlices) > 0 {
			report.Verdict = "fix-slices"
		} else {
			report.Verdict = "clean"
		}
		report.Scope = rv.Scope
		report.Findings = finalFindings
		report.ReviewedSHA = rv.ReviewedSHA
		for _, fs := range fixSlices {
			report.FixSlices = append(report.FixSlices, fs.ID)
		}
		round.FixSlices = fixSlices
		if err := writeReviewerRound(d, ticket, n, report, rv, finalFindings); err != nil {
			return GateReport{}, err
		}
	}

	if report.Verdict == "clean" {
		if err := journal.Append(d.Store, ticket, journal.Line{Event: "gate-clean", Attempt: n}); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: journal gate-clean: %w", err)
		}
	} else {
		if err := journal.Append(d.Store, ticket, journal.Line{Event: "gate-round", Attempt: n}); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: journal gate-round: %w", err)
		}
		for _, fs := range round.FixSlices {
			if fs.FromGate == 0 {
				fs.FromGate = n
			}
			if err := d.Store.AppendSlices(ticket, []store.Slice{fs}); err != nil {
				return GateReport{}, fmt.Errorf("verifydeliver: gate: append fix slice %s: %w", fs.ID, err)
			}
			if err := d.Store.WriteSliceState(ticket, fs.ID, store.SliceState{State: "queued"}); err != nil {
				return GateReport{}, fmt.Errorf("verifydeliver: gate: init fix slice %s state: %w", fs.ID, err)
			}
			if err := journal.Append(d.Store, ticket, journal.Line{Slice: fs.ID, Event: "fix-slice", Attempt: n}); err != nil {
				return GateReport{}, fmt.Errorf("verifydeliver: gate: journal fix-slice %s: %w", fs.ID, err)
			}
		}
	}

	if briefDocContent != nil {
		if err := os.WriteFile(filepath.Join(roundDir, "spec-input.md"), briefDocContent, 0o644); err != nil {
			return GateReport{}, fmt.Errorf("verifydeliver: gate: write spec-input.md: %w", err)
		}
	}

	if err := d.Store.Push(fmt.Sprintf("%s: gate round %d %s", ticket, n, report.Verdict)); err != nil {
		return GateReport{}, fmt.Errorf("verifydeliver: gate: push store: %w", err)
	}

	return report, nil
}

// checkFrontier errors when any slice - including a fix slice raised by an
// earlier gate round - is not yet green and early is false: a slice left
// queued, stalled, needs-input, env-blocked or otherwise unfinished means
// the delivery is incomplete, so gating (or publishing) now would review or
// ship less than the whole ticket. --early skips the check entirely, for a
// deliberate mid-ticket milestone batch.
func checkFrontier(d Deps, ticket string, slices []store.Slice, early bool) error {
	if early {
		return nil
	}
	for _, s := range slices {
		st, err := d.Store.ReadSliceState(ticket, s.ID)
		if err != nil {
			return fmt.Errorf("verifydeliver: gate: read slice %s state: %w", s.ID, err)
		}
		if st.State != "green" {
			return &axi.Error{
				Msg:  fmt.Sprintf("slice %s is %s, not green; run `jig run %s` first or pass --early", s.ID, st.State, ticket),
				Code: "GATE_NOT_GREEN",
				Help: []string{fmt.Sprintf("Run `jig run %s` to finish the frontier, or `jig gate %s --early` to review anyway.", ticket, ticket)},
			}
		}
	}
	return nil
}

// fetchTicketBranchFromBuildLease points the lease's jig/<ticket> branch at
// the build lease's copy: gate and publish leases are separate clones, and
// the ticket branch exists only in the build lease until publish pushes it.
func fetchTicketBranchFromBuildLease(leaseDir, repoName, ticket string) error {
	poolDir, err := home.PoolDir()
	if err != nil {
		return fmt.Errorf("verifydeliver: resolve pool dir: %w", err)
	}
	buildLeaseDir := filepath.Join(poolDir, repoName, ticket)
	branch := ticketBranch(ticket)
	refspec := fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branch, branch)
	// Fetching into refs/heads/<branch> fails while that branch is the
	// lease's own checked-out HEAD, so step off it first.
	if _, err := gitx.Run(leaseDir, "checkout", "--detach", "HEAD"); err != nil {
		return fmt.Errorf("verifydeliver: detach HEAD before fetch: %w", err)
	}
	if _, err := gitx.Run(leaseDir, "fetch", buildLeaseDir, refspec); err != nil {
		return fmt.Errorf("verifydeliver: fetch %s from build lease: %w", branch, err)
	}
	if _, err := gitx.Run(leaseDir, "checkout", branch); err != nil {
		return fmt.Errorf("verifydeliver: checkout %s: %w", branch, err)
	}
	return nil
}

// runGateOracles brings up every env class referenced by a slice, runs
// every manifest oracle across every workspace, then tears the env classes
// back down. An unavailable env with a defer-ci policy journals and skips;
// one with no policy pauses the gate with a NEEDS_INPUT error.
func runGateOracles(d Deps, ticket, dir string, man manifest.Manifest, slices []store.Slice) error {
	var handles []*envrun.Handle
	defer func() {
		for _, h := range handles {
			_ = h.Down()
		}
	}()

	seen := map[string]bool{}
	for _, s := range slices {
		if s.Env == "" || seen[s.Env] {
			continue
		}
		seen[s.Env] = true
		ec, ok := man.Envs[s.Env]
		if !ok {
			continue
		}
		h, err := envrun.Up(ec, ticket, dir)
		if err != nil {
			var un *envrun.Unavailable
			if errors.As(err, &un) {
				if un.Policy == "defer-ci" {
					if jerr := journal.Append(d.Store, ticket, journal.Line{Event: "env-unavailable", Outcome: "defer-ci"}); jerr != nil {
						return fmt.Errorf("verifydeliver: gate: journal env-unavailable: %w", jerr)
					}
					continue
				}
				return &axi.Error{
					Msg:  fmt.Sprintf("env class %q is unavailable and needs input: %v", s.Env, err),
					Code: axi.NeedsInput,
				}
			}
			return fmt.Errorf("verifydeliver: gate: bring up env %q: %w", s.Env, err)
		}
		handles = append(handles, h)
	}

	return runOracleSuite(dir, man)
}

// runOracleSuite runs every manifest oracle across every manifest
// workspace in dir.
func runOracleSuite(dir string, man manifest.Manifest) error {
	for _, ws := range man.Workspaces {
		for name := range man.Oracles {
			cmd := shortenQuotedPath(man.OracleCmd(name, ws))
			if err := envrun.Shell(cmd, dir); err != nil {
				return &axi.Error{
					Msg:  fmt.Sprintf("oracle %q failed in workspace %s: %v", name, ws.ID, err),
					Code: "GATE_ORACLE_FAILED",
				}
			}
		}
	}
	return nil
}

// writeCleanRound writes a clean round's files: findings.md declares it
// clean, report.yaml records the verdict, and diff-changelog.md is the
// pure journal render of what landed since the previous round.
func writeCleanRound(d Deps, ticket string, n int, report GateReport) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	findings := fmt.Sprintf("# Gate round %d\n\nclean\n", n)
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(findings), 0o644); err != nil {
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

// writeFixRound writes a fix-slices round's files: the reviewer's
// findings, report.yaml, diff-changelog.md, and receipts under the
// ticket's evidence tree.
func writeFixRound(d Deps, ticket string, n int, report GateReport, round Round) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	findings := round.FindingsMD
	if findings == "" {
		findings = fmt.Sprintf("# Gate round %d\n", n)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(findings), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}
	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	if err := writeDiffChangelog(d, ticket, dir, n); err != nil {
		return err
	}
	if len(round.Receipts) > 0 {
		evDir := evidenceDir(d.Store, ticket, n)
		if err := os.MkdirAll(evDir, 0o755); err != nil {
			return fmt.Errorf("verifydeliver: gate: create evidence dir: %w", err)
		}
		for name, data := range round.Receipts {
			if err := os.WriteFile(filepath.Join(evDir, name), data, 0o644); err != nil {
				return fmt.Errorf("verifydeliver: gate: write receipt %s: %w", name, err)
			}
		}
	}
	return nil
}

func writeReportYAML(dir string, report GateReport) error {
	out, err := yaml.Marshal(reportYAML{
		Round:       report.Round,
		Verdict:     report.Verdict,
		Model:       report.Model,
		TargetSHA:   report.TargetSHA,
		ReviewedSHA: report.ReviewedSHA,
	})
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: marshal report.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), out, 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write report.yaml: %w", err)
	}
	return nil
}

func writeDiffChangelog(d Deps, ticket, dir string, n int) error {
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: read journal for diff changelog: %w", err)
	}
	content := journal.RenderDiffChangelog(lines, n)
	if err := os.WriteFile(filepath.Join(dir, "diff-changelog.md"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write diff-changelog.md: %w", err)
	}
	return nil
}

// isGitLeaseDir reports whether dir looks like an existing pool lease
// checkout (a git working copy), as opposed to a key never acquired yet.
func isGitLeaseDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
