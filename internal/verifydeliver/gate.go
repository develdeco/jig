package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// Round is one gate round's content, whether played back by a fake source
// (tests) or produced by a real reviewer session. Review is set only by the
// reviewer source (review.go): its validated result plus scope data, which
// Gate applies through findings bookkeeping (findings.go) to decide clean
// vs fix-slices and to persist findings.yaml/md, then routes into fix
// slices (route.go) - those are appended directly from routing's own
// return value, never stored back onto this struct. FixSlices
// is the scripted source's own field instead: fakeGateSource.Round reads it
// straight from a scenario's fix-slices.yaml, and Gate appends it unchanged
// for that source.
type Round struct {
	FindingsMD string
	FixSlices  []store.Slice
	Receipts   map[string][]byte
	Review     *Review
}

// GateSource supplies one gate round's content. Round returns ok=false for
// a clean round (no round directory, or an empty one, for the scripted
// source; findings bookkeeping decides this for the reviewer source).
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
	Ticket string
	Early  bool
	// Branch is `jig gate --branch`: review this branch, built outside jig, and
	// adopt it as the ticket's own. The first round that names it records it in
	// the ticket's record, with the branch's tip as the ticket's start sha; every
	// later round works on the recorded branch, and naming another one is
	// refused (BRANCH_MISMATCH). Empty: the ticket's own branch, the recorded
	// one or the default (<ticket>-<slug>).
	Branch string
	// Intent and IntentDoc are `jig gate --intent`/`--doc`: at most one is
	// ever set - refused when both are, whether by the CLI or by a caller
	// going straight to GateOpts - valid in every mode, and write the
	// ticket's intent.md as an explicit, binding intent - refused with
	// INTENT_CONFLICT when the ticket already has a brief.md. Intent is the
	// literal text; IntentDoc is a file whose content becomes that text.
	Intent    string
	IntentDoc string
	// Yes is `jig gate --yes`: the round keeps every finding jig can route on
	// its own when it is recorded, without waiting for a person's decision on
	// each ask (DefaultTriage). A recorded round carries it from the opening
	// to the `review done` that closes it.
	Yes bool
	// Triage is the seam for a reviewer round's fix batch and ask
	// findings (route.go). nil means DefaultTriage: every fix is
	// kept, every ask with a full build target is kept, one missing part
	// of it stays undecided. Used by Gate only: a recorded round decides its
	// asks from the decisions `jig triage` journaled (recordedTriage).
	Triage Triage
}

// GateReport is Gate's result.
type GateReport struct {
	Round     int
	Verdict   string // clean|fix-slices|not-independent
	TargetSHA map[string]string
	// Model and Effort are what the reviewer said it ran on: self-reported,
	// "" when it said nothing or no reviewer ran. jig chooses no model.
	Model  string
	Effort string
	// Reviewer is who reported a recorded round, and Independent whether they
	// are independent of the agents that built what they reviewed. Both are
	// the zero value for a scripted or launched round.
	Reviewer    Provenance
	Independent Independence
	// OracleNames are the manifest's oracle names at this round.
	OracleNames []string
	// Copy says whose copy of the branch the round reviewed: the clone's,
	// origin's or (for work an earlier jig built) the build lease's.
	Copy string
	// ReviewedSHA is repoName -> the head sha a reviewer round reviewed, on
	// every reviewer round, clean or not - the anchor the next round's
	// scope resolves against (review.go's resolveScopeBase). nil for a
	// scripted round.
	ReviewedSHA map[string]string
	// Scope is this round's scope diff kind (full|delta), "" for a
	// scripted round.
	Scope string
	// Findings is every finding this round reported, after routing and
	// triage set each one's final Status/Triage/Decision/
	// RoutedAs. nil for a scripted round.
	Findings []Finding
	// FixSlices is the ids of the fix slices this round appended, in the
	// order they were built.
	FixSlices []string
	// NeedsHuman is every asked finding still undecided after this round
	// (across every round, not only this one's own): the "needs a human"
	// list, the exit-2 signal. Empty when nothing is waiting on a person.
	NeedsHuman []Finding
	// Branch is the branch the ticket adopted, "" for a ticket working on
	// its own default branch.
	Branch string
	// Intent is this round's resolved intent binding (resolveIntent), read
	// once before the round's source runs; a reviewer round reports the one
	// it handed the reviewer instead (Review.Intent), which is an
	// inference the source ran during the round (review.go's inferIntent)
	// when nothing was resolved.
	Intent Intent
	// IntentSHA256 is the sha256 hex of the exact bytes of the file at
	// Intent.Path, "" for Intent.Source "none".
	IntentSHA256 string
	// IntentNote is a one-line reason this round's intent stayed "none"
	// after an inference attempt (round.Review.IntentNote) - empty when
	// inference was not attempted or it succeeded.
	IntentNote string
	// BudgetParked is how many findings this round parked under the fix
	// budget (status asked, RoutedWhyBudget): 0 for a round that parked
	// none, a scripted round (the budget never forces one of its findings),
	// or a round with nothing new to route at all. BudgetUsed and
	// BudgetLimit are this round's own view of the fix budget (the
	// ticket's earlier fix-appending rounds, and gate.fix_rounds),
	// meaningful only alongside BudgetParked > 0.
	BudgetParked            int
	BudgetUsed, BudgetLimit int
	// Probes is how many probes a reviewer round recorded, and Unprobed how
	// many hunks of the whole change it left with no current probe inside and
	// no current skip over them (ADR 0036): 0 for a scripted round.
	Probes, Unprobed int
}

// reportYAML is gate/round-<n>/report.yaml's exact on-disk shape.
type reportYAML struct {
	Round       int               `yaml:"round"`
	Verdict     string            `yaml:"verdict"`
	Model       string            `yaml:"model,omitempty"`
	Effort      string            `yaml:"effort,omitempty"`
	TargetSHA   map[string]string `yaml:"target_sha"`
	ReviewedSHA map[string]string `yaml:"reviewed_sha,omitempty"`
	Intent      reportIntentYAML  `yaml:"intent"`
	// Independent says whether the reviewer of a recorded round is
	// independent of the builders (true, false or unknown); absent for a
	// scripted or launched round. Publish refuses a round whose reviewer
	// built what it reviewed.
	Independent Independence `yaml:"independent,omitempty"`
	// Reviewer is who reported the round, as the reviewer's session and
	// agent ids and how jig learned them.
	Reviewer *Provenance `yaml:"reviewer,omitempty"`
	// OracleNames are the manifest's oracle names, kept so the pull request
	// body can name the oracles a publish rendered without a checkout.
	OracleNames []string `yaml:"oracle_names,omitempty"`
	// FixSlices is the ids of the fix slices this round appended, whoever
	// kept them (a reviewer round's own routing, or a scripted round's
	// scenario fix slices): the fix budget's own used-rounds count
	// (UsedFixBudget) is a round whose list here is non-empty.
	FixSlices []string `yaml:"fix_slices,omitempty"`
}

// reportIntentYAML is report.yaml's own "intent" block: the source
// resolveIntent bound this round to, and the sha256 hex of the exact bytes
// of the file at that source's path ("" for source "none").
type reportIntentYAML struct {
	Source string `yaml:"source"`
	SHA256 string `yaml:"sha256"`
}

// UsedFixBudget returns how many of ticket's gate rounds before round
// appended at least one fix slice (report.yaml's fix_slices, written for a
// reviewer round's own routing and for a scripted round's scenario fix
// slices alike): the count "The fix budget" compares against
// gate.fix_rounds to decide whether a round still queues fix findings or
// parks them. unreadable names any round in that range whose report.yaml
// could not be read or parsed, skipped rather than counted either way -
// the same tolerance OutstandingAsks and `jig status`'s own round-reading
// already apply, since one corrupt earlier round is not reason enough to
// treat every ticket's budget as exhausted or to refuse every later
// round. Gate and `jig status` share this one reading, so neither can
// disagree with the other about how much of the budget is used.
func UsedFixBudget(st *store.Store, ticket string, round int) (used int, unreadable []int) {
	for r := 1; r < round; r++ {
		data, err := os.ReadFile(filepath.Join(gateRoundDir(st, ticket, r), "report.yaml"))
		if err != nil {
			if !os.IsNotExist(err) {
				unreadable = append(unreadable, r)
			}
			continue
		}
		var rep reportYAML
		if err := yaml.Unmarshal(data, &rep); err != nil {
			unreadable = append(unreadable, r)
			continue
		}
		if len(rep.FixSlices) > 0 {
			used++
		}
	}
	return used, unreadable
}

// checkFrontier errors when any slice - including a fix slice raised by an
// earlier gate round - is not yet green and early is false: a slice left
// queued, stalled, needs-input, env-blocked or otherwise unfinished means
// the delivery is incomplete, so gating (or publishing) now would review or
// ship less than the whole ticket. The slices of every repo count, and in a
// project of several repos the refusal names the repo of the slice it stops
// at. --early skips the check entirely, for a deliberate mid-ticket milestone
// batch.
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
			where := ""
			if len(d.Cfg.Repos) > 1 {
				if repo := ready.Place(ready.ProjectRepos(d.Cfg, d.Machine.Clones), slices).Repo[s.Workspace]; repo != "" && repo != ready.Unplaced {
					where = " of repo " + repo
				}
			}
			help := []string{fmt.Sprintf("Run `jig next %s` to see what is left to build, or `jig gate %s --early` to review anyway.", ticket, ticket)}
			if d.Prompt != nil {
				if p := d.Prompt(ticket); p != "" {
					help = append(help, "ask: "+p)
				}
			}
			return &axi.Error{
				Msg:  fmt.Sprintf("slice %s%s is %s, not green; finish it first or pass --early", s.ID, where, st.State),
				Code: "GATE_NOT_GREEN",
				Help: help,
			}
		}
	}
	return nil
}

// fetchTicketBranchFromBuildLease points the lease's ticket branch, branch
// (the name the caller resolved for the ticket once), at the build lease's
// copy, in the pool under the jig home root jigHome: gate and publish leases
// are separate clones, and the ticket branch exists only in the build lease
// until publish pushes it.
func fetchTicketBranchFromBuildLease(jigHome, leaseDir, repoName, ticket, branch string) error {
	buildLeaseDir, err := pool.Dir(jigHome, repoName, ticket, pool.Build)
	if err != nil {
		return fmt.Errorf("verifydeliver: resolve build lease: %w", err)
	}
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

// runGateOracles runs every manifest oracle across every workspace and
// returns the runs, all passed. A run already passed on this head's tree, as
// jig recorded it at a slice's green (reusablePasses), is reused, not run
// again (ADR 0021). For the runs left, it brings up every env class
// referenced by a slice, runs them, then tears the env classes back down; it
// brings up none when no run is left. An unavailable env with a defer-ci
// policy journals and skips; one with no policy pauses the gate with a
// NEEDS_INPUT error.
func runGateOracles(d Deps, ticket, repo, dir string, man manifest.Manifest, slices []store.Slice) ([]OracleRun, error) {
	reused, err := reusablePasses(d, ticket, dir, man, slices)
	if err != nil {
		return nil, err
	}
	if coversSuite(man, reused) {
		runs, err := oracleSuiteRuns(dir, man, reused, d.oracle())
		return withSlices(runs, repo, man, slices), err
	}

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
						return nil, fmt.Errorf("verifydeliver: gate: journal env-unavailable: %w", jerr)
					}
					continue
				}
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("env class %q is unavailable and needs input: %v", s.Env, err),
					Code: axi.NeedsInput,
				}
			}
			return nil, fmt.Errorf("verifydeliver: gate: bring up env %q: %w", s.Env, err)
		}
		handles = append(handles, h)
	}

	runs, err := oracleSuiteRuns(dir, man, reused, d.oracle())
	return withSlices(runs, repo, man, slices), err
}

// withSlices names, on each run, the slices whose oracle it is in its
// workspace (ADR 0036): which code the oracle was run for. slices are the
// slices that build in repo, each workspace read as a slice writes it
// (manifest.WorkspaceIn): <repo>/<id> in a project of several repos, the bare
// id, which may hold a slash, in a project of one.
func withSlices(runs []OracleRun, repo string, man manifest.Manifest, slices []store.Slice) []OracleRun {
	for i, run := range runs {
		for _, s := range slices {
			if ws, ok := man.WorkspaceIn(repo, s.Workspace); ok && s.Oracle == run.Oracle && ws.ID == run.Workspace {
				runs[i].Slices = append(runs[i].Slices, s.ID)
			}
		}
	}
	return runs
}

// reusedPass is a pass jig recorded at a slice's green that stands for the
// gate's own run: the commit it ran at and its wall time in seconds.
type reusedPass struct {
	Commit  string
	Seconds int
}

// reusablePasses returns, by exact command, the oracle pass jig recorded at
// a slice's green (ADR 0020) that stands for the gate's own run of that
// command on dir's HEAD (ADR 0021). A pass stands only when:
//   - it carries a commit, which frontier records only when the lease's
//     tracked and untracked files were exactly that commit;
//   - that commit's tree is HEAD's tree;
//   - the env class that was up for it is the whole set of env classes the
//     gate brings up for this ticket (gateEnvs);
//   - no failed run of the same command is recorded on that tree, so a
//     flaky oracle is sampled again.
//
// Every oracle runs from the repo root, so the command alone names the run.
func reusablePasses(d Deps, ticket, dir string, man manifest.Manifest, slices []store.Slice) (map[string]reusedPass, error) {
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: gate: read journal for oracle passes: %w", err)
	}
	headTree, err := gitx.RevParse(dir, "HEAD^{tree}")
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: gate: resolve HEAD's tree: %w", err)
	}
	envs := gateEnvs(man, slices)
	sameTree := func(commit string) bool {
		tree, found, _ := gitx.Resolve(dir, commit+"^{tree}")
		return found && tree == headTree
	}
	passed, failed := map[string]reusedPass{}, map[string]bool{}
	for _, l := range lines {
		if l.Event != "oracle" || l.Commit == "" || l.Command == "" || !sameTree(l.Commit) {
			continue
		}
		switch l.Outcome {
		case "fail":
			failed[l.Command] = true
		case "pass":
			if sameEnvs(l.Env, envs) {
				passed[l.Command] = reusedPass{Commit: l.Commit, Seconds: l.Seconds}
			}
		}
	}
	for cmd := range failed {
		delete(passed, cmd)
	}
	return passed, nil
}

// gateEnvs is the set of env classes runGateOracles brings up: every one a
// slice names that the manifest declares.
func gateEnvs(man manifest.Manifest, slices []store.Slice) map[string]bool {
	envs := map[string]bool{}
	for _, s := range slices {
		if _, ok := man.Envs[s.Env]; s.Env != "" && ok {
			envs[s.Env] = true
		}
	}
	return envs
}

// sameEnvs reports whether a run with env class env up ("" for none) had
// exactly the env classes in envs up.
func sameEnvs(env string, envs map[string]bool) bool {
	if env == "" {
		return len(envs) == 0
	}
	return len(envs) == 1 && envs[env]
}

// coversSuite reports whether reused holds every run of man's suite.
func coversSuite(man manifest.Manifest, reused map[string]reusedPass) bool {
	for _, ws := range man.Workspaces {
		for name := range man.Oracles {
			if _, ok := reused[man.OracleCmd(name, ws)]; !ok {
				return false
			}
		}
	}
	return true
}

// oracleSuiteRuns is RunOracleSuite with the runs in reused (by command)
// taken from there instead of run again, and each other run made by run and
// timed.
func oracleSuiteRuns(dir string, man manifest.Manifest, reused map[string]reusedPass, run oracleFunc) ([]OracleRun, error) {
	var runs []OracleRun
	for _, ws := range man.Workspaces {
		for _, name := range SortedOracleNames(man) {
			cmd := man.OracleCmd(name, ws)
			if pass, ok := reused[cmd]; ok {
				runs = append(runs, OracleRun{Oracle: name, Workspace: ws.ID, Command: cmd, ReusedFrom: pass.Commit, Seconds: pass.Seconds})
				continue
			}
			start := time.Now()
			if err := runOracle(run, cmd, dir); err != nil {
				return nil, &axi.Error{
					Msg:  fmt.Sprintf("oracle %q failed in workspace %s: %v", name, ws.ID, err),
					Code: "GATE_ORACLE_FAILED",
				}
			}
			runs = append(runs, OracleRun{Oracle: name, Workspace: ws.ID, Command: cmd, Seconds: wholeSeconds(time.Since(start))})
		}
	}
	return runs, nil
}

// wholeSeconds is d rounded up to whole seconds, so any run takes at least 1:
// the rule `jig verify` times an oracle by (ADR 0024).
func wholeSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		return 1
	}
	return s
}

// oracleLimit bounds one oracle run of the gate or of publish's
// revalidation: the same bound jig gives a builder's shell command and its
// own run at a slice's green, so a hung oracle cannot hold the gate.
const oracleLimit = 30 * time.Minute

// oracleFunc runs one oracle command in a directory and returns its
// combined output: Deps.Oracle's type.
type oracleFunc = func(cmd, dir string) (string, error)

// shellOracle is the real oracle run: cmd in dir within oracleLimit, its
// process tree killed past it.
func shellOracle(cmd, dir string) (string, error) {
	return envrun.ShellOutput(envrun.ShortenQuotedPath(cmd), dir, oracleLimit)
}

// runOracle runs one oracle command in dir through run. A failure carries
// the end of the command's output, as the stderr Shell reported once did.
func runOracle(run oracleFunc, cmd, dir string) error {
	out, err := run(cmd, dir)
	if err == nil {
		return nil
	}
	out = strings.TrimSpace(out)
	if len(out) > 4000 {
		out = out[len(out)-4000:]
	}
	if out == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, out)
}

// RunOracleSuite runs every manifest oracle across every manifest
// workspace in dir, workspaces in manifest order and oracles by name, and
// returns the runs it made. Any failure stops the suite, so a nil error
// means every returned run passed. Exported for internal/revieweval, whose
// reviewer gets the runs the way Gate's does.
func RunOracleSuite(dir string, man manifest.Manifest) ([]OracleRun, error) {
	return oracleSuiteRuns(dir, man, nil, shellOracle)
}

// writeNoRound writes the files for a round that never ran while something
// was still outstanding: the source had nothing to review for this round
// number, but the fold still holds an open or asked finding. It records
// what is outstanding rather than the word "clean", so the stored round
// agrees with the report and with `jig status`.
func writeNoRound(d Deps, ticket string, n int, report GateReport) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Gate round %d\n", n)
	fmt.Fprintf(&b, "\nNo review ran this round. Still outstanding from earlier rounds:\n\n")
	for _, f := range report.Findings {
		fmt.Fprintf(&b, "- %s (%s) %s:%d %s\n", f.ID, f.Status, f.File, f.Line, f.Title)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.md"), []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}
	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	if err := writeDiffChangelog(d, ticket, dir, n); err != nil {
		return err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "gate-round", Attempt: n}); err != nil {
		return fmt.Errorf("verifydeliver: gate: journal gate-round: %w", err)
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

// appendFixSlices appends every fix slice a round routed, initializing
// each to queued and journaling it. It is shared by the scripted source's
// own fix-slices (round.FixSlices) and a reviewer round's routing output
// (routeRound's slices, passed by Gate).
func appendFixSlices(d Deps, ticket string, n int, slices []store.Slice) error {
	for _, fs := range slices {
		if fs.FromGate == 0 {
			fs.FromGate = n
		}
		if err := d.Store.AppendSlices(ticket, []store.Slice{fs}); err != nil {
			return fmt.Errorf("verifydeliver: gate: append fix slice %s: %w", fs.ID, err)
		}
		if err := d.Store.WriteSliceState(ticket, fs.ID, store.SliceState{State: "queued"}); err != nil {
			return fmt.Errorf("verifydeliver: gate: init fix slice %s state: %w", fs.ID, err)
		}
		if err := journal.Append(d.Store, ticket, journal.Line{Slice: fs.ID, Event: "fix-slice", Attempt: n}); err != nil {
			return fmt.Errorf("verifydeliver: gate: journal fix-slice %s: %w", fs.ID, err)
		}
	}
	return nil
}

// writeReviewerRound writes a reviewer round's files: findings.yaml (with the
// round's probes and skips), findings.md rendered from it, report.yaml (with
// ReviewedSHA), and diff-changelog.md.
func writeReviewerRound(d Deps, ticket string, n int, report GateReport, scope string, reviewedPaths []string, findings []Finding, cleared []string, summary string, probes []ProbeRecord, skips []SkipRecord) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	yamlData, err := marshalFindingsYAML(scope, reviewedPaths, findings, cleared, summary, probes, skips)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: marshal findings.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), yamlData, 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.yaml: %w", err)
	}
	md := renderFindingsMD(n, report.Verdict, summary, findings, cleared)
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

func writeReportYAML(dir string, report GateReport) error {
	rep := reportYAML{
		Round:       report.Round,
		Verdict:     report.Verdict,
		Model:       report.Model,
		Effort:      report.Effort,
		TargetSHA:   report.TargetSHA,
		ReviewedSHA: report.ReviewedSHA,
		Intent:      reportIntentYAML{Source: report.Intent.Source, SHA256: report.IntentSHA256},
		FixSlices:   report.FixSlices,
		Independent: report.Independent,
		OracleNames: report.OracleNames,
	}
	if report.Independent != "" {
		reviewer := report.Reviewer
		rep.Reviewer = &reviewer
	}
	out, err := yaml.Marshal(rep)
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

// restoreLeaseBeforeAcquire readies the ticket's gate or publish lease, if
// there is one, for the Acquire that follows: it restores the lease pristine
// at its current HEAD and forgets its own copy of branch (dropLeaseBranch).
// Both roles re-point the lease at the copy they work on right after
// acquiring it, so neither keeps anything of its own on the branch.
//
// The restore comes first. A reviewer that outlived a killed jig, or an oracle
// rewrite left over from an earlier attempt, can leave tracked dirt in the
// lease; if the ticket branch later advances past whatever file that dirt
// touched, Acquire's own `git checkout` (and, in normal mode,
// fetchTicketBranchFromBuildLease's checkout right after) refuses with "local
// changes ... would be overwritten" before any restore after the acquire ever
// runs, wedging every later attempt at the same point. It is best-effort and
// runs only in a pool.Usable lease: a reset where git resolves an enclosing
// repository would discard that repository's work, and one on an unborn HEAD
// would fail. Anything else is left to Acquire, which refuses a bad ticket id,
// clones where there is no lease yet, and moves aside anything git shows is
// not a repository of its own.
func restoreLeaseBeforeAcquire(jigHome, repoName, ticket string, role pool.Role, branch string) error {
	leaseDir, err := pool.Dir(jigHome, repoName, ticket, role)
	if err != nil || !pool.Usable(leaseDir) {
		return nil
	}
	if err := resetLeasePristine(leaseDir, "HEAD"); err != nil {
		return fmt.Errorf("verifydeliver: %s: restore existing lease before acquire: %w", role, err)
	}
	if err := dropLeaseBranch(leaseDir, branch); err != nil {
		return fmt.Errorf("verifydeliver: %s: drop the lease's own %s before acquire: %w", role, branch, err)
	}
	return nil
}

// dropLeaseBranch forgets a gate or publish lease's own local copy of branch,
// leaving its HEAD detached. Neither lease keeps commits of its own on the
// branch - Gate and Publish each re-point the lease at the copy they work on
// right after acquiring it - so its copy is disposable; left in place, one
// that fell behind or diverged from origin's since the last round would fail
// Acquire's sync with the branch (BRANCH_DIVERGED) before the command could
// replace it, over commits that are not the lease's to keep.
func dropLeaseBranch(leaseDir, branch string) error {
	if _, err := gitx.Run(leaseDir, "checkout", "--detach", "HEAD"); err != nil {
		return fmt.Errorf("detach HEAD: %w", err)
	}
	if _, found, _ := gitx.Resolve(leaseDir, "refs/heads/"+branch); !found {
		return nil // no local copy
	}
	if _, err := gitx.Run(leaseDir, "branch", "-D", branch); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}
	return nil
}
