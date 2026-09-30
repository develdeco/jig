package verifydeliver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tracker"
)

// PublishOpts configures one Publish invocation.
type PublishOpts struct {
	Ticket string
	Yes    bool
}

// PublishReport is Publish's result.
type PublishReport struct {
	Tier string // none|oracles-only
	// Squashed is repo -> the squash commit's sha, for a repo whose branch was
	// squashed: one that was not on origin. A branch already on origin is
	// pushed as it is and has no entry (Squash says which).
	Squashed map[string]string
	// Head is repo -> the head the push left on the branch.
	Head   map[string]string
	PRBody map[string]string // repo -> store-relative pr body path
	PRURL  map[string]string // repo -> opened PR url; empty when the tracker adapter has no PRCreator
}

// NotSquashed is what the publish report says of a repo whose branch was
// already on origin: published history is never rewritten, so the branch is
// pushed as it is, with what publish adds on top.
const NotSquashed = "not squashed (branch already on origin)"

// Squash says what publish did with repo's history: "squashed", or
// NotSquashed.
func (r PublishReport) Squash(repo string) string {
	if r.Squashed[repo] != "" {
		return "squashed"
	}
	return NotSquashed
}

// confirm asks the interactive "Push ... ? [y/N]" question and reports
// whether the operator agreed. It is a func var covering the whole seam -
// both the fmt.Printf prompt and the stdin read - so a test can replace it
// outright and never touch the real terminal: stubbing only the read half
// would still print the literal prompt text to the test's real stdout.
var confirm = func(branch, ticket string) bool {
	fmt.Printf("Push %s and open a PR for %s? [y/N] ", branch, ticket)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// guardedPush pushes branch to origin, refusing a non-local remote without
// confirmation. It is a func var (defaulting to gitx.GuardedPush) so tests
// can intercept the call and assert what confirmation value Publish
// actually threads through, without needing a real non-local remote.
var guardedPush = gitx.GuardedPush

// Publish reconciles, re-validates, documents, squashes, and routes one
// ticket's delivery. Only history not yet on origin is squashed: a branch
// already there is pushed as it is. v0.1 handles a single repo.
func Publish(d Deps, o PublishOpts) (report PublishReport, err error) {
	ticket := o.Ticket
	// journaled backs the deferred best-effort push below: once Publish's
	// first tracked write to the store's working copy has landed (the
	// journaled reconcile outcome, recordAndCheckDivergence below - the
	// earliest store write in this whole function), any later error -
	// PUBLISH_NO_DIVERGENCE, PUSHED_RANGE, an oracle failure in revalidate,
	// a declined confirmation, a push or PR-creation failure - would
	// otherwise leave that journal line (and whatever changelog, ledger,
	// evidence or PR-body content landed after it) uncommitted until
	// whatever later command next calls Store.Sync, on this ticket or any
	// other (see failureCode for why this push runs here rather than
	// waiting on Sync; Gate runs the same pattern on its own failures) -
	// reachable simply by declining at this function's own confirmation
	// prompt, not only by an outage.
	var journaled bool
	defer func() {
		if err == nil || !journaled {
			return
		}
		// Best-effort: if this push itself fails, the original error is
		// still the one that reaches the caller; there is nothing more to
		// do here but try.
		_ = d.Store.Push(fmt.Sprintf("%s: publish failed: %s", ticket, failureCode(err)))
	}()

	if err := d.Store.Sync(); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: sync store: %w", err)
	}

	repo, repoName, target := primaryRepo(d.Cfg)
	// The ticket's record is read once, here, before publish writes anything
	// to the store, and both the branch and the PR title come from that one
	// read: a record that cannot be read fails now rather than after the
	// memorize commit, and one that changes while publish runs cannot give
	// the two different snapshots.
	rec, err := d.Store.ReadTicket(ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read ticket record: %w", err)
	}
	// Resolved once: the lease, the fetch, the reconcile, the confirm prompt,
	// the push and the PR all name this one branch, so a ticket.yaml that
	// changes while publish runs cannot split them across two.
	branch, err := d.Store.ResolveTicketBranch(ticket, rec, target)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: resolve ticket branch: %w", err)
	}
	// A ticket that adopted a branch is refused before anything is written
	// or acquired, and ahead of the checks below, which it would only fail
	// for a reason that is not the one to fix first.
	if rec.Adopted() {
		return PublishReport{}, adoptedBranchRefusal(d, ticket, repoName, branch)
	}

	// Precondition: every slice must be green (the same frontier check gate
	// applies before it will even review), and the latest gate round must
	// have returned a clean verdict. Without this, publish can squash and
	// push a ticket whose last review found must-fix work still open.
	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read slices: %w", err)
	}
	if err := checkFrontier(d, ticket, slices, false); err != nil {
		return PublishReport{}, err
	}
	gateRep, err := latestGateReport(d.Store, ticket)
	if err != nil {
		return PublishReport{}, err
	}
	if gateRep.Verdict != "clean" {
		return PublishReport{}, &axi.Error{
			Msg:  fmt.Sprintf("latest gate round for %s returned %q, not clean", ticket, gateRep.Verdict),
			Code: "PUBLISH_NOT_CLEAN",
			Help: []string{fmt.Sprintf("Run `jig gate %s` until it reports clean before publishing.", ticket)},
		}
	}

	// The publish lease is re-pointed at the copy publish ships right after
	// the acquire below, so its own copy of the branch is disposable: one left
	// by an earlier attempt must not refuse the acquire (BRANCH_DIVERGED) over
	// commits nobody keeps, which it does once the branch has reached origin
	// another way.
	if err := restoreLeaseBeforeAcquire(d.Home, repoName, ticket, pool.Publish, branch); err != nil {
		return PublishReport{}, err
	}
	lease, err := pool.Acquire(d.Home, repoName, repo.Remote, target, branch, ticket, pool.Publish)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: acquire lease: %w", err)
	}
	// Resolve the operator's identity once, before the first commit
	// (reconcile, memorize and squash all use it).
	identityEnv, err := gitx.IdentityEnv(identityDir(d, repoName, lease.Dir))
	if err != nil {
		return PublishReport{}, err
	}
	if err := fetchTicketBranchFromBuildLease(d.Home, lease.Dir, repoName, ticket, branch); err != nil {
		return PublishReport{}, err
	}
	if _, err := gitx.Run(lease.Dir, "fetch", "origin"); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: fetch origin: %w", err)
	}

	// Step 1: reconcile.
	policy, err := reconcile(lease.Dir, branch, target, identityEnv)
	if err != nil {
		return PublishReport{}, err
	}
	// recordAndCheckDivergence's own journal line is this function's
	// earliest tracked write to the store, appended unconditionally -
	// including on its own PUBLISH_NO_DIVERGENCE error path, before that
	// check runs - so journaled is set here rather than derived from its
	// result. If divergenceFileCount or the append itself fails before
	// anything actually lands, the deferred push above still fires but
	// commits nothing: Store.Push skips its own commit when nothing is
	// staged (it still pushes and runs maintenance, same as always).
	journaled = true
	if err := recordAndCheckDivergence(d, ticket, lease.Dir, target, policy); err != nil {
		return PublishReport{}, err
	}
	if err := checkNonEmptyRange(lease.Dir, target); err != nil {
		return PublishReport{}, err
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

	questions, err := d.Store.ReadQuestions(ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read questions: %w", err)
	}

	// Step 3: docs.
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	if err := writeMemorize(lease.Dir, ticket, slices, lines, questions, identityEnv); err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "memorize"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal memorize: %w", err)
	}

	if err := writeChangelogs(d.Store, ticket, slices, lines); err != nil {
		return PublishReport{}, err
	}
	title := consolidatedTitle(rec.Title, ticket, slices)
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
	//
	// Only unpushed history is squashed. A branch that is already on origin
	// (the policy reconcile just chose from its own ls-remote, the one answer
	// to that question) has published history, which is never rewritten: it is
	// pushed as it is, the commits already there with their shas and what
	// publish added (the merge of the target, the memorize commit) on top. A
	// branch that was never pushed squashes as it always has.
	var sha string
	if policy == policyLocalRebase {
		sha, err = squash(lease.Dir, target, ticket, title, identityEnv)
		if err != nil {
			return PublishReport{}, err
		}
		if err := journal.Append(d.Store, ticket, journal.Line{Event: "squash", Commit: sha}); err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal squash: %w", err)
		}
	} else if err := journal.Append(d.Store, ticket, journal.Line{Event: "squash", Outcome: "none:branch-on-origin"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal squash: %w", err)
	}
	head, err := gitx.RevParse(lease.Dir, "HEAD")
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: resolve the head to push: %w", err)
	}

	consolidated := journal.RenderConsolidated(lines)
	prPath, err := writePRBody(d.Store, ticket, repoName, consolidated)
	if err != nil {
		return PublishReport{}, err
	}

	// confirmed tracks whether an actual publish confirmation ran: --yes
	// stands in for it, or an interactive "y"/"yes" answer does. A decline
	// stops cleanly here: the ticket branch itself is never pushed (the
	// deferred push above still runs and pushes the store's own record of
	// the attempt - only guardedPush's push of the ticket branch below is
	// skipped). confirmed is the only value ever passed to guardedPush; it
	// is never hardcoded to true, so a non-local remote with no
	// confirmation is refused by the gitx guard downstream.
	confirmed := o.Yes
	if !o.Yes {
		if !confirm(branch, ticket) {
			return PublishReport{}, &axi.Error{Msg: "publish declined at confirmation", Code: "PUBLISH_DECLINED"}
		}
		confirmed = true
	}

	if err := guardedPush(lease.Dir, "origin", branch, confirmed); err != nil {
		return PublishReport{}, err
	}

	adapter, err := tracker.New(d.Cfg, d.Store)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: build tracker adapter: %w", err)
	}
	prURL := ""
	if creator, ok := adapter.(tracker.PRCreator); ok {
		url, err := creator.CreatePR(branch, target, title, filepath.Join(d.Store.Root, prPath))
		if err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: create PR: %w", err)
		}
		prURL = url
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "pr"}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal pr: %w", err)
	}

	// Step 6: route.
	if err := route(d.Cfg, d.Store, adapter, ticket, slices); err != nil {
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

	squashed := map[string]string{}
	if sha != "" {
		squashed[repoName] = sha
	}
	return PublishReport{
		Tier:     tier,
		Squashed: squashed,
		Head:     map[string]string{repoName: head},
		PRBody:   map[string]string{repoName: prPath},
		PRURL:    map[string]string{repoName: prURL},
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

// divergenceFileCount returns how many files differ between HEAD and
// origin/target after reconcile, using the triple-dot form (relative to
// their merge base) so a merge-policy reconcile is measured the same way as
// a rebase one.
func divergenceFileCount(dir, target string) (int, error) {
	out, err := gitx.Run(dir, "diff", "--name-only", "origin/"+target+"...HEAD")
	if err != nil {
		return 0, fmt.Errorf("verifydeliver: publish: diff origin/%s...HEAD: %w", target, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, nil
	}
	return len(strings.Split(out, "\n")), nil
}

// recordAndCheckDivergence journals the reconcile step's outcome as
// "<policy>:<n>-files" and refuses with PUBLISH_NO_DIVERGENCE when the
// reconciled branch has no diff against the target at all: a conflict-free
// integration that changes nothing is an ownership-aware divergence
// failure - the target already carries the same content, so publishing
// would silently keep whatever this ticket's slices actually changed.
func recordAndCheckDivergence(d Deps, ticket, dir, target, policy string) error {
	n, err := divergenceFileCount(dir, target)
	if err != nil {
		return err
	}
	outcome := fmt.Sprintf("%s:%d-files", policy, n)
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "reconcile", Outcome: outcome}); err != nil {
		return fmt.Errorf("verifydeliver: publish: journal reconcile: %w", err)
	}
	if n == 0 {
		return &axi.Error{
			Msg:  "integration produced no change against the target - stale-overwrite suspicion",
			Code: "PUBLISH_NO_DIVERGENCE",
		}
	}
	return nil
}

// checkNonEmptyRange refuses to publish when the reconciled branch has no
// commits beyond origin/target: an all-green ticket whose slices never
// landed a commit, or a re-publish after the branch already squashed onto
// the target, has nothing left to squash. Checked up front, before any of
// the doc/evidence store writes further down Publish.
func checkNonEmptyRange(dir, target string) error {
	start, err := gitx.MergeBase(dir, "origin/"+target, "HEAD")
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: merge-base origin/%s HEAD: %w", target, err)
	}
	commits, err := gitx.CommitsIn(dir, start+"..HEAD")
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: list %s..HEAD: %w", start, err)
	}
	if len(commits) == 0 {
		return &axi.Error{
			Msg:  fmt.Sprintf("nothing to publish: no commits beyond origin/%s", target),
			Code: "NOTHING_TO_PUBLISH",
		}
	}
	return nil
}

// squash refuses to publish a range that already reached a remote branch,
// then collapses start..HEAD into one commit on the ticket branch. Publish
// calls it only for a branch that is not on origin under its own name (a
// branch that is there is pushed as it is); the refusal is for commits that
// reached a remote under another. The squash base is
// merge-base(origin/target, HEAD) computed here at squash time, not the
// ticket's recorded start sha: while the target is unmoved the two are
// equal, but after a reconcile rebase the recorded start sha is stale (it
// would wrongly pull the target's own new history into the range), while
// the merge-base tracks the rebase's new fork point.
func squash(leaseDir, target, ticket, title string, identityEnv []string) (string, error) {
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
	if _, err := gitx.RunEnv(leaseDir, identityEnv, "commit", "-m", msg); err != nil {
		return "", fmt.Errorf("verifydeliver: squash: commit: %w", err)
	}
	return gitx.RevParse(leaseDir, "HEAD")
}

// defaultRoutes is jig's default routing map, used for any key cfg's
// own project.yaml routes: block does not declare: pr.description is just
// the consolidated changelog; pr.comments and ticket.comments are the
// consolidated changelog followed by every gate round's diff changelog.
// jig v0.1's Adapter.Project has a single Description/Comments projection
// (no separate PR-vs-ticket sink), so pr.description/pr.comments are what
// actually drive Project's call; ticket.comments is accepted and defaulted
// the same way for forward compatibility with a future adapter split.
var defaultRoutes = map[string][]string{
	"pr.description":  {"changelog/consolidated.md"},
	"pr.comments":     {"changelog/consolidated.md", "gate/round-*/diff-changelog.md"},
	"ticket.comments": {"changelog/consolidated.md", "gate/round-*/diff-changelog.md"},
}

// resolveRoutes returns cfg's declared routing map overlaid onto
// defaultRoutes: an absent cfg.Routes (or an absent individual key) falls
// back to this default for that key.
func resolveRoutes(cfg project.Config) map[string][]string {
	out := make(map[string][]string, len(defaultRoutes))
	for k, v := range defaultRoutes {
		out[k] = v
	}
	for k, v := range cfg.Routes {
		out[k] = v
	}
	return out
}

// routeFiles glob-expands each of globs (store-relative to the ticket's
// folder) and returns the content of every match, in glob order and then
// lexical match order, skipping any pattern that matches nothing.
func routeFiles(st *store.Store, ticket string, globs []string) []string {
	var out []string
	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(st.TicketDir(ticket), g))
		if err != nil {
			continue
		}
		for _, m := range matches {
			if data, err := os.ReadFile(m); err == nil {
				out = append(out, string(data))
			}
		}
	}
	return out
}

// route projects the ticket's final state onto its tracker adapter: a
// description and comment trail assembled from cfg's routing map (the
// spec's defaults when it declares none), plus one subtask per slice with
// its blocking links and current state.
func route(cfg project.Config, st *store.Store, adapter tracker.Adapter, ticket string, slices []store.Slice) error {
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

	routes := resolveRoutes(cfg)
	description := strings.Join(routeFiles(st, ticket, routes["pr.description"]), "\n")
	comments := routeFiles(st, ticket, routes["pr.comments"])

	return adapter.Project(ticket, tracker.Projection{
		Description: description,
		Subtasks:    subtasks,
		Comments:    comments,
	})
}

// adoptedBranchRefusal is Publish's refusal of a ticket that adopted a
// branch. Publishing an adopted branch is not built: publish squashes
// everything the branch holds beyond the target into one commit and refuses a
// range already on a remote (PUSHED_RANGE), and the author's commits are on
// origin. It would fail there, after writing the ticket's changelog, ledger
// and journal entries for a publish that never happened, and it would fail
// earlier still, on a machine with no build lease, at the fetch from one.
// The refusal comes first and names the gap, so nothing is written for it,
// and says what to do instead (PublishByHand): the commits jig built, when it
// built any, are not on origin, so the pull request the human opens needs
// them pushed first.
func adoptedBranchRefusal(d Deps, ticket, repoName, branch string) error {
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	built := len(journal.BuiltCommits(lines)) > 0
	leaseDir := ""
	if built {
		if leaseDir, err = pool.Dir(d.Home, repoName, ticket, pool.Build); err != nil {
			return fmt.Errorf("verifydeliver: publish: resolve build lease: %w", err)
		}
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("ticket %s adopted branch %s, and jig publish cannot ship an adopted branch yet", ticket, branch),
		Code: "PUBLISH_ADOPTED_BRANCH",
		Help: []string{"Until it can: " + PublishByHand(branch, built, leaseDir)},
	}
}
