package verifydeliver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/journal"
	"github.com/develdeco/jig/manifest"
	"github.com/develdeco/jig/pool"
	"github.com/develdeco/jig/store"
	"github.com/develdeco/jig/tracker"
)

// PublishOpts configures one Publish invocation.
type PublishOpts struct {
	Ticket string
	Yes    bool
}

// PublishReport is Publish's result.
type PublishReport struct {
	Tier     string            // none|oracles-only
	Squashed map[string]string // repo -> squash commit sha
	PRBody   map[string]string // repo -> store-relative pr body path
}

// stdinConfirm reads one line from stdin for the interactive publish
// confirmation. It is a func var so tests can stub it without a real
// terminal.
var stdinConfirm = func() (string, error) {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}

// Publish reconciles, re-validates, documents, squashes, and routes one
// ticket's delivery. v0.1 handles a single repo.
func Publish(d Deps, o PublishOpts) (PublishReport, error) {
	ticket := o.Ticket
	if err := d.Store.Sync(); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: sync store: %w", err)
	}

	repo, repoName, target := primaryRepo(d.Cfg)
	branch := ticketBranch(ticket)
	lease, err := pool.Acquire(repoName, repo.Remote, target, branch, ticket+"-publish")
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: acquire lease: %w", err)
	}
	if err := fetchTicketBranchFromBuildLease(lease.Dir, repoName, ticket); err != nil {
		return PublishReport{}, err
	}
	if _, err := gitx.Run(lease.Dir, "fetch", "origin"); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: fetch origin: %w", err)
	}

	// Step 1: reconcile.
	policy, err := reconcile(lease.Dir, ticket, target)
	if err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "reconcile", Outcome: policy}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal reconcile: %w", err)
	}

	man, err := manifest.Resolve(lease.Dir)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: resolve manifest: %w", err)
	}

	// Step 2: re-validate.
	tier, err := revalidate(d, ticket, repoName, target, lease.Dir, man)
	if err != nil {
		return PublishReport{}, err
	}

	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read slices: %w", err)
	}
	questions, err := d.Store.ReadQuestions(ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read questions: %w", err)
	}

	// Step 3: docs.
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	if err := writeMemorize(lease.Dir, ticket, slices, lines, questions); err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "memorize"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal memorize: %w", err)
	}

	if err := writeChangelogs(d.Store, ticket, slices, lines); err != nil {
		return PublishReport{}, err
	}
	title := consolidatedTitle(ticket, slices)
	if err := appendLedgerEntry(d.Store, ticket, title, slices, questions); err != nil {
		return PublishReport{}, err
	}
	oracleNames := make([]string, 0, len(man.Oracles))
	for name := range man.Oracles {
		oracleNames = append(oracleNames, name)
	}
	if err := appendContractIndexEntry(d.Store, d.Cfg.Platform, ticket, slices, oracleNames); err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "changelog"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal changelog: %w", err)
	}

	lastRound, err := existingGateRounds(d.Store, ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: count gate rounds: %w", err)
	}

	// Step 4: evidence.
	if err := writeEvidence(d.Store, ticket, lastRound); err != nil {
		return PublishReport{}, err
	}

	// Step 5: PR (squash + confirm + push).
	sha, err := squash(lease.Dir, target, ticket, title)
	if err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "squash", Commit: sha}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal squash: %w", err)
	}

	consolidated := journal.RenderConsolidated(lines)
	prPath, err := writePRBody(d.Store, ticket, repoName, consolidated)
	if err != nil {
		return PublishReport{}, err
	}

	if !o.Yes {
		fmt.Printf("Push %s and open a PR for %s? [y/N] ", branch, ticket)
		answer, _ := stdinConfirm()
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			return PublishReport{}, &axi.Error{Msg: "publish declined at confirmation", Code: "PUBLISH_DECLINED"}
		}
	}

	if err := gitx.GuardedPush(lease.Dir, "origin", branch, true); err != nil {
		return PublishReport{}, err
	}

	adapter, err := tracker.New(d.Cfg, d.Store)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: build tracker adapter: %w", err)
	}
	// NOTE: for a github-tracker project, the contract calls for `gh pr
	// create` here; tracker.Adapter (Mint/Project/Comment) has no
	// PR-creation method to shell that through, so for every tracker kind
	// the PR body file above remains the artifact of record, same as
	// local/command. Closest working version pending an Adapter extension.
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "pr"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal pr: %w", err)
	}

	// Step 6: route.
	if err := route(d.Store, adapter, ticket, consolidated, slices, lastRound); err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "route"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal route: %w", err)
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "publish-done"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal publish-done: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: publish", ticket)); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: push store: %w", err)
	}

	return PublishReport{
		Tier:     tier,
		Squashed: map[string]string{repoName: sha},
		PRBody:   map[string]string{repoName: prPath},
	}, nil
}

// latestGateReport reads the highest-numbered gate round's report.yaml.
func latestGateReport(st *store.Store, ticket string) (reportYAML, error) {
	n, err := existingGateRounds(st, ticket)
	if err != nil {
		return reportYAML{}, err
	}
	if n == 0 {
		return reportYAML{}, fmt.Errorf("verifydeliver: publish: no gate rounds recorded for %s", ticket)
	}
	path := filepath.Join(gateRoundDir(st, ticket, n), "report.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return reportYAML{}, fmt.Errorf("verifydeliver: publish: read %s: %w", path, err)
	}
	var rep reportYAML
	if err := yaml.Unmarshal(data, &rep); err != nil {
		return reportYAML{}, fmt.Errorf("verifydeliver: publish: parse %s: %w", path, err)
	}
	return rep, nil
}

// revalidate compares the latest gate round's recorded target sha against
// the current origin/target: unmoved keeps the gate verdict standing
// ("none"); moved re-runs every oracle in the publish lease before
// standing behind it ("oracles-only").
func revalidate(d Deps, ticket, repoName, target, leaseDir string, man manifest.Manifest) (string, error) {
	rep, err := latestGateReport(d.Store, ticket)
	if err != nil {
		return "", err
	}
	current, err := gitx.RevParse(leaseDir, "origin/"+target)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: publish: resolve origin/%s: %w", target, err)
	}

	if rep.TargetSHA[repoName] == current {
		if err := journal.Append(d.Store, ticket, journal.Line{Event: "revalidate", Outcome: "none:target-unmoved"}); err != nil {
			return "", fmt.Errorf("verifydeliver: publish: journal revalidate: %w", err)
		}
		return "none", nil
	}

	if err := runOracleSuite(leaseDir, man); err != nil {
		return "", err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "revalidate", Outcome: "oracles-only:target-moved"}); err != nil {
		return "", fmt.Errorf("verifydeliver: publish: journal revalidate: %w", err)
	}
	return "oracles-only", nil
}

// squash refuses to publish a range that already reached a remote branch,
// then collapses start..HEAD into one commit on the ticket branch. The
// squash base is merge-base(origin/target, HEAD) computed here at squash
// time, not the ticket's recorded start sha: while the target is unmoved
// the two are equal, but after a reconcile rebase the recorded start sha
// is stale (it would wrongly pull the target's own new history into the
// range), while the merge-base tracks the rebase's new fork point.
func squash(leaseDir, target, ticket, title string) (string, error) {
	start, err := gitx.MergeBase(leaseDir, "origin/"+target, "HEAD")
	if err != nil {
		return "", fmt.Errorf("verifydeliver: squash: merge-base origin/%s HEAD: %w", target, err)
	}

	commits, err := gitx.CommitsIn(leaseDir, start+"..HEAD")
	if err != nil {
		return "", fmt.Errorf("verifydeliver: squash: list %s..HEAD: %w", start, err)
	}
	for _, c := range commits {
		onRemote, err := gitx.CommitOnAnyRemote(leaseDir, c)
		if err != nil {
			return "", fmt.Errorf("verifydeliver: squash: check remote reachability of %s: %w", c, err)
		}
		if onRemote {
			return "", &axi.Error{
				Msg:  fmt.Sprintf("commit %s in the squash range %s..HEAD is already on a remote branch", c, start),
				Code: "PUSHED_RANGE",
			}
		}
	}

	if _, err := gitx.Run(leaseDir, "reset", "--soft", start); err != nil {
		return "", fmt.Errorf("verifydeliver: squash: reset --soft %s: %w", start, err)
	}
	msg := fmt.Sprintf("%s: %s", ticket, title)
	if _, err := runGitEnv(leaseDir, pinnedGitEnv, "commit", "-m", msg); err != nil {
		return "", fmt.Errorf("verifydeliver: squash: commit: %w", err)
	}
	return gitx.RevParse(leaseDir, "HEAD")
}

// route projects the ticket's final state onto its tracker adapter: a
// description, one subtask per slice with its blocking links and current
// state, and a comment trail (the consolidated changelog, then each gate
// round's diff changelog).
func route(st *store.Store, adapter tracker.Adapter, ticket, consolidated string, slices []store.Slice, lastRound int) error {
	subtasks := make([]tracker.Subtask, 0, len(slices))
	for _, s := range slices {
		state, err := st.ReadSliceState(ticket, s.ID)
		if err != nil {
			return fmt.Errorf("verifydeliver: route: read slice %s state: %w", s.ID, err)
		}
		subtasks = append(subtasks, tracker.Subtask{
			ID:        s.ID,
			Title:     s.Goal,
			State:     state.State,
			BlockedBy: s.BlockedBy,
		})
	}
	sort.Slice(subtasks, func(i, j int) bool { return subtasks[i].ID < subtasks[j].ID })

	comments := []string{consolidated}
	for n := 1; n <= lastRound; n++ {
		path := filepath.Join(gateRoundDir(st, ticket, n), "diff-changelog.md")
		if data, err := os.ReadFile(path); err == nil {
			comments = append(comments, string(data))
		}
	}

	return adapter.Project(ticket, tracker.Projection{
		Description: consolidated,
		Subtasks:    subtasks,
		Comments:    comments,
	})
}
