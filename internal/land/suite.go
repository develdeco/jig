package land

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
)

// The codes of an issue's suite precondition.
const (
	// SuiteNotRun: the repo declares a suite and no covering run is recorded
	// at the tip of the issue's branch.
	SuiteNotRun = "LAND_SUITE_NOT_RUN"
	// SuiteFailed: a covering run at the tip failed.
	SuiteFailed = "LAND_SUITE_FAILED"
)

// The standings of one repo's suite at the tip of an issue's branch.
const (
	SuitePass       = "pass"
	SuiteFail       = "fail"
	SuiteWaived     = "waived"
	SuiteUndeclared = "undeclared"
	SuiteUnrun      = "unrun"
)

// RepoSuite is where one repo's suite stands for an issue: the tip of the
// issue's branch read from the clone's origin, whether the repo's manifest
// there declares a suite, and the run or the waiver recorded at that exact tip.
type RepoSuite struct {
	Repo string // the repo's name in the project
	Tip  string // origin/<branch>'s sha, "" when the clone holds no copy
	// Standing is one of the Suite standings above.
	Standing string
	Command  string       // the suite command as the manifest at the tip gives it
	Run      journal.Line // the covering run, when Standing is pass or fail
	Waiver   journal.Line // the waiver, when Standing is waived
	Why      string       // for an unrun suite, what is missing
	Unmet    *Unmet       // the precondition's answer, nil when met
}

// JournalRepo is the repo name a journal line of the project carries: none in
// a project of one repo.
func JournalRepo(d Deps, repo string) string {
	if len(d.Cfg.Repos) < 2 {
		return ""
	}
	return repo
}

// SuiteOf says where the suite of issue c stands in each repo it builds in. It
// reads the manifest at the tip of origin/<branch> and the issue's journal at
// exactly that tip: a run or a waiver at an earlier tip stands for nothing. A
// repo with no clone on this machine is not read, and has no standing row.
func SuiteOf(d Deps, c Container) ([]RepoSuite, error) {
	if c.Kind != store.KindIssue || d.Store == nil {
		return nil, nil
	}
	lines, err := journal.Read(d.Store, c.ID)
	if err != nil {
		return nil, err
	}
	var out []RepoSuite
	for _, repo := range c.Repos {
		dir := d.Clones[repo]
		if dir == "" {
			continue
		}
		rs := RepoSuite{Repo: repo}
		tip, terr := gitx.RevParse(dir, "origin/"+c.Branch)
		if terr != nil || tip == "" {
			rs.Standing = SuiteUnrun
			rs.Why = fmt.Sprintf("%s holds no copy of origin/%s: `git fetch` it, then run the suite at its tip", repo, c.Branch)
			rs.Unmet = &Unmet{Code: SuiteNotRun, Why: rs.Why, Prompt: suitePrompt(c, repo)}
			out = append(out, rs)
			continue
		}
		rs.Tip = tip
		m, err := manifest.ResolveAt(dir, tip)
		if err != nil {
			return nil, fmt.Errorf("the manifest of %s at %s: %w", repo, short(tip), err)
		}
		rs.Command = strings.TrimSpace(m.OracleCmd("suite", rootWorkspace(m)))
		if _, ok := m.Oracles["suite"]; !ok || rs.Command == "" {
			rs.Standing, rs.Command = SuiteUndeclared, ""
			out = append(out, rs)
			continue
		}
		jr := JournalRepo(d, repo)
		run, hasRun := journal.FinalRunAt(lines, jr, tip)
		waiver, waived := journal.SuiteWaivedAt(lines, jr, tip)
		switch {
		case hasRun && run.Outcome == "pass":
			rs.Standing, rs.Run = SuitePass, run
		case waived:
			rs.Standing, rs.Waiver = SuiteWaived, waiver
		case hasRun:
			rs.Standing, rs.Run = SuiteFail, run
			rs.Why = fmt.Sprintf("the suite of %s failed at %s in %d s: %s", repo, short(tip), run.Seconds, run.Signature)
			rs.Unmet = &Unmet{Code: SuiteFailed, Why: rs.Why, Prompt: "Add a fix task for " + c.ID}
		default:
			rs.Standing = SuiteUnrun
			rs.Why = fmt.Sprintf("the suite of %s has no run at %s, the tip of origin/%s", repo, short(tip), c.Branch)
			rs.Unmet = &Unmet{Code: SuiteNotRun, Why: rs.Why, Prompt: suitePrompt(c, repo)}
		}
		out = append(out, rs)
	}
	return out, nil
}

// suitePrompt is what a session does to run the suite of repo: `jig verify
// <issue> --final` in a worktree of the repo on the issue's branch.
func suitePrompt(c Container, repo string) string {
	return fmt.Sprintf("Run `jig verify %s --final` in a worktree of %s on %s at its tip", c.ID, repo, c.Branch)
}

// rootWorkspace is the root workspace of m: the one `{path}` in the suite
// command resolves against.
func rootWorkspace(m manifest.Manifest) manifest.Workspace {
	if ws, ok := m.Workspace("root"); ok {
		return ws
	}
	return manifest.Workspace{ID: "root", Path: "."}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// suitePrecondition is the entry of Preconditions for an issue's suite: every
// repo is met, with a pass or a waiver at the tip or no suite declared, or it
// names what is unmet. It asks nothing of a multi-task. The caller asks it only
// once every child has landed.
func suitePrecondition(c Container) ([]Unmet, error) {
	if c.Kind != store.KindIssue {
		return nil, nil
	}
	suites, err := SuiteOf(c.Deps, c)
	if err != nil {
		return nil, err
	}
	var out []Unmet
	for _, s := range suites {
		if s.Unmet != nil {
			out = append(out, *s.Unmet)
		}
	}
	return out, nil
}

// suiteWaived are the waivers among suites in words, one per waived repo, as
// `jig next` shows them: the repo, the tip, who said it and why.
func suiteWaived(suites []RepoSuite) []string {
	var out []string
	for _, s := range suites {
		if s.Standing == SuiteWaived {
			out = append(out, fmt.Sprintf("suite of %s waived at %s by %s: %s", s.Repo, short(s.Tip), s.Waiver.Provenance, s.Waiver.Summary))
		}
	}
	return out
}

// VerificationLines are the lines of an issue's pull request body about its
// suite, one per repo, in the words ADR 0053's final run uses: what ran at the
// issue's head and how it ended, that it was waived and why, that the repo
// declares none, or that only an earlier tip has a run. A project of several
// repos names each line's repo.
func VerificationLines(suites []RepoSuite) []string {
	var out []string
	for _, s := range suites {
		var line string
		switch s.Standing {
		case SuitePass:
			line = fmt.Sprintf("Suite at the issue's head %s: pass in %d s: `%s`", short(s.Tip), s.Run.Seconds, s.Command)
		case SuiteFail:
			line = fmt.Sprintf("Suite at the issue's head %s failed", short(s.Tip))
		case SuiteWaived:
			line = fmt.Sprintf("Suite waived at %s: %s", short(s.Tip), s.Waiver.Summary)
		case SuiteUndeclared:
			line = "No suite declared in " + s.Repo
		default:
			line = fmt.Sprintf("No suite run at the issue's head %s", short(s.Tip))
		}
		if len(suites) > 1 {
			line = s.Repo + ": " + line
		}
		out = append(out, line)
	}
	return out
}
