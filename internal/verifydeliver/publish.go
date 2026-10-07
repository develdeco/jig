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
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// PublishOpts configures one Publish invocation.
type PublishOpts struct {
	Ticket string
	Yes    bool
	// Backend runs the short session that picks, from the recordings the
	// build made, the ones the pull request shows (ADR 0029). nil means no
	// session can be dispatched: a ticket with recordings then has its pick
	// refused, and the gate's demo, if there is one, stands in. A ticket with
	// no recordings never dispatches one.
	Backend session.Backend
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
	PRURL  map[string]string // repo -> the PR url, opened or updated; empty when the repo has no pull-request host
	// PRUpdated is repo -> true when the branch already had an open pull
	// request, which publish updated instead of opening another.
	PRUpdated map[string]bool
	// PRNote is repo -> why PRURL[repo] is empty - today always "no
	// pull-request host" (the journal's own "none:no-host" outcome), the one
	// way PRURL ends up empty - so a repo with no pull-request host still
	// reports why, rather than a silent, unexplained gap where its row would
	// otherwise be.
	PRNote map[string]string
	// Picks is what publish did with the build's recordings: the zero value
	// when the build recorded nothing that could be offered.
	Picks PicksReport
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

// Publish reconciles, re-validates, documents, squashes, and routes one
// ticket's delivery. Only history not yet on origin is squashed: a branch
// already there is pushed as it is. v0.1 handles a single repo.
func Publish(d Deps, o PublishOpts) (report PublishReport, err error) {
	ticket := o.Ticket

	// Use Deps fields if set, otherwise use default implementations.
	// The confirm default covers the whole interactive seam - both the
	// fmt.Printf prompt and the stdin read - so a test that hands its own
	// never touches the real terminal: stubbing only the read half would
	// still print the literal prompt text to the test's real stdout. openPR
	// is the URL of the pull request the branch already has open, or "", and
	// hasHost says whether the repo has a pull-request host at all: the
	// question names which of the things it is agreeing to - opening a pull
	// request, updating that one, or pushing alone when there is no host.
	confirmFn := d.Confirm
	if confirmFn == nil {
		confirmFn = func(branch, ticket, openPR string, hasHost bool) bool {
			if !hasHost {
				fmt.Printf("Push %s for %s? [y/N] ", branch, ticket)
			} else if openPR != "" {
				fmt.Printf("Push %s and update its open PR %s for %s? [y/N] ", branch, openPR, ticket)
			} else {
				fmt.Printf("Push %s and open a PR for %s? [y/N] ", branch, ticket)
			}
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			answer := strings.ToLower(strings.TrimSpace(line))
			return answer == "y" || answer == "yes"
		}
	}
	guardedPushFn := d.GuardedPush
	if guardedPushFn == nil {
		guardedPushFn = gitx.GuardedPush
	}
	fetchOriginFn := d.FetchOrigin
	if fetchOriginFn == nil {
		fetchOriginFn = func(dir string) error {
			_, err := gitx.Run(dir, "fetch", "origin")
			return err
		}
	}
	warnFn := d.Warn
	if warnFn == nil {
		warnFn = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format, args...)
		}
	}

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
	// A branch the ticket adopted is on origin by definition, so one that is
	// not there is refused (BRANCH_NOT_FOUND) instead of being cut from the
	// target: publish would ship a branch that lacks the author's code. The
	// commits jig built on the branch, whichever it is, are the journal's
	// verified lines.
	var acquireOpts []pool.Option
	if rec.Adopted() {
		acquireOpts = append(acquireOpts, pool.MustExistOnOrigin())
	}
	jlines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	built := journal.BuiltCommits(jlines)
	lease, err := pool.Acquire(d.Home, repoName, repo.Remote, target, branch, ticket, pool.Publish, acquireOpts...)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: acquire lease: %w", err)
	}
	// Resolve the operator's identity once, before the first commit
	// (reconcile, memorize and squash all use it).
	identityEnv, err := gitx.IdentityEnvWithBase(identityDir(d, repoName, lease.Dir), d.GitEnv)
	if err != nil {
		return PublishReport{}, err
	}
	// The lease is pointed at the copy of the branch the gate reviewed, by the
	// same function the gate uses: an adopted branch jig built nothing on as
	// origin has it, or else whichever copy holds jig's commits - the build
	// lease's while origin has no copy of the branch.
	shipping, err := pointAtTicketBranch(d, lease.Dir, repoName, ticket, branch, rec.Adopted(), built, "publish")
	if err != nil {
		return PublishReport{}, err
	}
	if err := fetchOriginFn(lease.Dir); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: fetch origin: %w", err)
	}

	// Publish ships what was reviewed. The head it would ship, before reconcile
	// adds anything to it, must be the head the last clean round reviewed
	// (when that round recorded heads): a commit that landed on the branch since
	// would go out under a verdict that never saw it. Checked ahead of
	// everything that writes, so a refusal leaves the store untouched.
	shipHead, err := gitx.RevParse(lease.Dir, "HEAD")
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: resolve the head to ship: %w", err)
	}
	if err := checkReviewedHead(gateRep, repoName, shipHead, ticket); err != nil {
		return PublishReport{}, err
	}
	if err := checkReviewedIntent(d.Store, ticket, gateRep); err != nil {
		return PublishReport{}, err
	}
	// Publish never rewrites what is on origin and never forces: a branch that
	// is already there must be a fast-forward from where it stands. Checked
	// here for the same reason as above - git would refuse the push, but only
	// after publish had written the store and made its commits.
	buildDir, err := pool.Dir(d.Home, repoName, ticket, pool.Build)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: resolve build lease: %w", err)
	}
	if err := requireFastForward(lease.Dir, branch, buildDir, ticket, shipping); err != nil {
		return PublishReport{}, err
	}
	// Build the repo host from the repo's remote (or take the one d hands
	// Publish). It may be nil if the remote is not a pull-request host
	// (e.g., a local path).
	host, err := d.publishHost(repo.Remote)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: build repo host: %w", err)
	}

	// If the host exists, look for an open pull request ahead of the first
	// store write: a host that cannot be reached, or cannot say, refuses the
	// publish before it has written or pushed anything, not after the branch
	// is on origin.
	openPR := ""
	if host != nil {
		if openPR, err = host.FindOpenPR(branch, target); err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: look for an open pull request from %s: %w", branch, err)
		}
	}

	// Step 1: reconcile.
	policy, err := reconcile(lease.Dir, branch, target, identityEnv)
	if err != nil {
		return PublishReport{}, err
	}
	// touchedFiles is read once, right after reconcile and before the
	// memorize commit adds the retrieval notes on top of it, so it is the
	// change's own files - jig's bookkeeping never counts as "touched" -
	// for both the divergence count below and review-notes' Coverage
	// section later.
	touchedFiles, err := diffFiles(lease.Dir, target)
	if err != nil {
		return PublishReport{}, err
	}
	// recordAndCheckDivergence's own journal line is this function's
	// earliest tracked write to the store, appended unconditionally -
	// including on its own PUBLISH_NO_DIVERGENCE error path, before that
	// check runs - so journaled is set here rather than derived from its
	// result. If the append itself fails before anything actually lands,
	// the deferred push above still fires but commits nothing: Store.Push
	// skips its own commit when nothing is staged (it still pushes and
	// runs maintenance, same as always).
	journaled = true
	if err := recordAndCheckDivergence(d, ticket, policy, touchedFiles); err != nil {
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
	oracleNames := SortedOracleNames(man)
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

	commits := lastGreenCommits(lines)
	// An adopted ticket's own commits, from the merge base with target up to
	// the start sha recorded at adoption: the author's work, listed ahead of
	// jig's in What changed. Read after reconcile and squash (an adopted
	// branch is always on origin, so it is never squashed) so the lease's
	// HEAD still has startSHA as an ancestor.
	var authorCommits []authorCommit
	if rec.Adopted() {
		startData, err := os.ReadFile(d.Store.StartSHAPath(ticket, repoName))
		if err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: read start sha: %w", err)
		}
		authorCommits, err = adoptedAuthorCommits(lease.Dir, target, strings.TrimSpace(string(startData)))
		if err != nil {
			return PublishReport{}, err
		}
	}
	outcomes, reviewedPaths, lastSummary, err := collectFindingsWithOutcomes(d.Store.TicketDir(ticket), lastRound, slices)
	if err != nil {
		return PublishReport{}, err
	}
	// The recordings the build made, picked for the pull request (ADR 0029).
	// A pick that stands renders the ## Demo section; one that does not, or
	// none to make, leaves it to the gate's demo as it always was. The pick is
	// made for the head the gate reviewed (shipHead, before reconcile and
	// squash rewrote anything), whose history still holds the recorded commits.
	picksReport, picked, err := publishPicks(picksStep{
		d: d, backend: o.Backend, ticket: ticket, repoName: repoName,
		leaseDir: lease.Dir, head: shipHead, lines: lines, warn: warnFn,
	})
	if err != nil {
		return PublishReport{}, err
	}
	prPath, omittedBriefIntent, demoResult, err := writePRBody(d.Store, ticket, repoName, slices, gateRep, tier, commits, authorCommits, oracleNames, outcomes, d, lastRound, picked)
	if err != nil {
		return PublishReport{}, err
	}

	// The media directory and file names for attachment, when a demo has
	// verified files: demoResult.MediaDir is the evidence directory
	// renderDemoSection already resolved and verified these very files
	// against, for the head the gate reviewed - never recomputed here from
	// head, which by this point is the post-squash tip and names no
	// evidence directory that exists. A pick's files are staged in a
	// directory of their own (picksStep.stage), and are handed over the same
	// way.
	mediaDir := demoResult.MediaDir
	mediaFiles := make([]string, 0, len(demoResult.MediaFiles))
	for _, f := range demoResult.MediaFiles {
		mediaFiles = append(mediaFiles, f.Name)
	}
	// A brief that bound as the intent source but has no "## " section with
	// text to publish as one - no such heading anywhere, or a first section
	// with nothing under it, which renderIntentSection reads the same way -
	// leaves the body with no ## Intent section at all, which is the owner's
	// rule (DECISIONS.md, renderIntentSection) and so never an error - but it
	// is the one way a published body loses the section that says why the
	// change exists, and no other part of a run says it happened: the brief
	// binds whatever bytes it has (resolveIntent), the report records nothing
	// about the body's sections, and `jig validate` counts sections with
	// store.BriefSectionHashes, which counts the ones a code fence quotes too.
	// So the operator is told here, before the confirmation prompt below,
	// while fixing the brief and gating again is still cheaper than editing a
	// published pull request. The warning says "with text" because both briefs
	// reach it: naming only the missing heading would point the operator at an
	// edit that does not fix the brief that has one and nothing under it.
	if omittedBriefIntent {
		warnFn("jig: the pull request body for %s has no ## Intent section: %s has no \"## \" section with text to publish as one\n",
			ticket, intentFilePath(d.Store, ticket, IntentSourceBrief))
	}

	// Report demo status: no demo recorded, a demo refused outright, every
	// file of a recorded demo failing verification, or some files omitted
	// from an otherwise rendered section. The three ways a published body
	// ends up with no ## Demo section (no demo at all, a demo refused
	// outright, or one whose media all failed verification) are told apart
	// here, which half of the pipeline to blame.
	switch {
	case demoResult.NoDemo:
		warnFn("jig: no demo recorded for %s\n", ticket)
	case demoResult.DemoRefused:
		warnFn("jig: the demo recorded for %s was refused: %s\n", ticket, demoResult.RefusalReason)
	case demoResult.AllMediaFailed:
		warnFn("jig: the pull request body for %s has no ## Demo section: all media files from the recorded demo failed verification: %v\n",
			ticket, demoResult.Omitted)
	case len(demoResult.Omitted) > 0:
		warnFn("jig: media files omitted from the pull request body for %s: %v (missing or changed)\n",
			ticket, demoResult.Omitted)
	}

	// Report any summary or caption left out of the rendered section because
	// it named one of jig's own directories (the owner's decision on r1-f13,
	// DECISIONS.md): independent of the switch above, since a section can be
	// otherwise rendered in full.
	if demoResult.ScrubbedSummary {
		warnFn("jig: the demo summary for %s named one of jig's own directories and was left out of the pull request body\n", ticket)
	}
	if len(demoResult.ScrubbedCaptions) > 0 {
		warnFn("jig: captions left out of the pull request body for %s because they named one of jig's own directories: %v\n",
			ticket, demoResult.ScrubbedCaptions)
	}

	if err := writeReviewNotes(d.Store, ticket, lastRound, lastSummary, outcomes, reviewedPaths, touchedFiles); err != nil {
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
		if !confirmFn(branch, ticket, openPR, host != nil) {
			return PublishReport{}, &axi.Error{Msg: "publish declined at confirmation", Code: "PUBLISH_DECLINED"}
		}
		confirmed = true
	}

	if err := guardedPushFn(lease.Dir, "origin", branch, confirmed); err != nil {
		return PublishReport{}, err
	}

	// The pull request: the one the branch already has open into the target
	// is updated, its body replaced with this publish's, and none is opened
	// beside it; a branch with none gets one, when the host supports pull
	// requests. A closed or merged pull request, or one into another base, is
	// not that one (FindOpenPR): the operator asked to publish, so one is
	// opened, and the others are left as they are.
	prURL, prOutcome := "", ""

	// The files a pick staged are attached by name from their directory, and
	// the session and the confirmation have run since they were copied: check
	// them once more, right before the host reads them. A pick's media are
	// never uploaded unchecked; a change here refuses the publish, and the
	// next one stages them afresh.
	if picked != nil && host != nil {
		if err := verifyStaged(absPath(filepath.Join(d.Home, "evidence")), picked.MediaDir, picked.MediaFiles); err != nil {
			return PublishReport{}, &axi.Error{
				Msg:  fmt.Sprintf("the recordings staged for %s changed before they were attached: %v", ticket, err),
				Code: "PUBLISH_PICKS_CHANGED",
				Help: []string{fmt.Sprintf("Run `jig publish %s` again: it stages the picked recordings afresh.", ticket)},
			}
		}
	}

	if host != nil {
		switch {
		case openPR != "":
			// Try to update with media if supported
			if len(mediaFiles) > 0 {
				attached, err := host.UpdatePRWithMedia(openPR, filepath.Join(d.Store.Root, prPath), mediaDir, mediaFiles)
				if err != nil {
					return PublishReport{}, fmt.Errorf("verifydeliver: publish: update PR %s with media: %w", openPR, err)
				}
				if !attached {
					warnFn("jig: the installed gh does not support --attach; the pull request updated for %s carries no media\n", ticket)
				}
			} else {
				if err := host.UpdatePR(openPR, filepath.Join(d.Store.Root, prPath)); err != nil {
					return PublishReport{}, fmt.Errorf("verifydeliver: publish: update PR %s: %w", openPR, err)
				}
			}
			prURL, prOutcome = openPR, "updated"
		default:
			// Try to create with media if supported
			if len(mediaFiles) > 0 {
				url, attached, err := host.CreatePRWithMedia(branch, target, title, filepath.Join(d.Store.Root, prPath), mediaDir, mediaFiles)
				if err != nil {
					return PublishReport{}, fmt.Errorf("verifydeliver: publish: create PR with media: %w", err)
				}
				if !attached {
					warnFn("jig: the installed gh does not support --attach; the pull request opened for %s carries no media\n", ticket)
				}
				prURL, prOutcome = url, "opened"
			} else {
				url, err := host.CreatePR(branch, target, title, filepath.Join(d.Store.Root, prPath))
				if err != nil {
					return PublishReport{}, fmt.Errorf("verifydeliver: publish: create PR: %w", err)
				}
				prURL, prOutcome = url, "opened"
			}
		}
	} else {
		prOutcome = "none:no-host"
	}

	if err := journal.Append(d.Store, ticket, journal.Line{Event: "pr", Outcome: prOutcome, Commit: head}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal pr: %w", err)
	}

	// Check for unrewritten demo references if media was attached. A failed
	// read-back is a warning, not silence: media were just uploaded, and the
	// read-back is the one signal that says whether the references actually
	// point at them now. gh rewrites an image reference in place but leaves
	// other forms as they were - a bare video path among them, "Videos",
	// DECISIONS.md - so a reference still unrewritten is patched here, not
	// merely reported: the URL gh appended for that file (recorded in the very
	// body just read back) moves to where the reference stands, and the pull
	// request gets a second edit with the fix. Only a name gh appended no URL
	// for at all - the read-back carries no record of it - is left as it was
	// and named in the warning, the one case nothing here can fix.
	if prURL != "" && len(mediaFiles) > 0 && host != nil {
		body, err := host.ReadPRBody(prURL)
		if err != nil {
			warnFn("jig: read back the pull request body for %s to check for unrewritten media references: %v\n", ticket, err)
		} else if unrewritten := checkUnrewrittenReferences(body, demoResult.MediaFiles); len(unrewritten) > 0 {
			patched, changed, stillUnrewritten := rewriteUnrewrittenReferences(body, unrewritten)
			if changed {
				bodyPath := filepath.Join(d.Store.Root, prPath)
				if err := os.WriteFile(bodyPath, []byte(patched), 0o644); err != nil {
					warnFn("jig: write the patched pull request body for %s: %v\n", ticket, err)
				} else if err := host.UpdatePR(prURL, bodyPath); err != nil {
					warnFn("jig: rewrite unrewritten media references in the pull request body for %s: %v\n", ticket, err)
				}
			}
			if len(stillUnrewritten) > 0 {
				warnFn("jig: unrewritten media references in the pull request body for %s: %v\n", ticket, stillUnrewritten)
			}
		}
	}

	// Post the review notes as a comment on the pull request, if there is one.
	// A failed post is a soft failure, not a publish failure: the pull
	// request stands, and the file is kept in the store for a manual post.
	if prURL != "" && host != nil {
		reviewNotesPath := filepath.Join(d.Store.Root, ticket, "pr", "review-notes.md")
		if err := host.CommentPR(prURL, reviewNotesPath); err != nil {
			warnFn("jig: post %s as a comment on %s: %v\n", reviewNotesPath, prURL, err)
		}
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
	prNote := map[string]string{}
	if host == nil {
		prNote[repoName] = "no pull-request host"
	}
	return PublishReport{
		Tier:      tier,
		Squashed:  squashed,
		Head:      map[string]string{repoName: head},
		PRBody:    map[string]string{repoName: prPath},
		PRURL:     map[string]string{repoName: prURL},
		PRUpdated: map[string]bool{repoName: prOutcome == "updated"},
		PRNote:    prNote,
		Picks:     picksReport,
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

	if _, err := RunOracleSuite(leaseDir, man); err != nil {
		return "", err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: "revalidate", Outcome: "oracles-only:target-moved"}); err != nil {
		return "", fmt.Errorf("verifydeliver: publish: journal revalidate: %w", err)
	}
	return "oracles-only", nil
}

// diffFiles returns the repo-relative paths that differ between
// origin/target and HEAD, using the triple-dot form (relative to their
// merge base) so a merge-policy reconcile is measured the same way as a
// rebase one. Shared by recordAndCheckDivergence's own file count and
// review-notes' Coverage section (the files the change touched), computed
// once by Publish right after reconcile.
func diffFiles(dir, target string) ([]string, error) {
	out, err := gitx.Run(dir, "diff", "--name-only", "origin/"+target+"...HEAD")
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: publish: diff origin/%s...HEAD: %w", target, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// recordAndCheckDivergence journals the reconcile step's outcome as
// "<policy>:<n>-files" and refuses with PUBLISH_NO_DIVERGENCE when the
// reconciled branch has no diff against the target at all: a conflict-free
// integration that changes nothing is an ownership-aware divergence
// failure - the target already carries the same content, so publishing
// would silently keep whatever this ticket's slices actually changed.
func recordAndCheckDivergence(d Deps, ticket, policy string, files []string) error {
	n := len(files)
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

// checkReviewedHead refuses, with PUBLISH_UNREVIEWED_HEAD, a head publish
// would ship that is not the head the latest clean gate round reviewed: rep is
// that round's report (a clean verdict is checked before this is reached), and
// head the ticket branch's head before reconcile. A reviewer round records the
// heads it reviewed (reviewed_sha), one per repo; a scripted round records
// none at all, and there is nothing to hold the head to, so it is let through
// as it always has been. A round that recorded heads, but none for this repo,
// reviewed something else - a repo renamed since, say - and is refused like a
// different head: what was reviewed is not what would ship.
func checkReviewedHead(rep reportYAML, repoName, head, ticket string) error {
	if len(rep.ReviewedSHA) == 0 {
		return nil
	}
	help := []string{fmt.Sprintf("Run `jig gate %s` to review the branch as it is now, then publish again.", ticket)}
	reviewed := rep.ReviewedSHA[repoName]
	if reviewed == "" {
		repos := make([]string, 0, len(rep.ReviewedSHA))
		for name := range rep.ReviewedSHA {
			repos = append(repos, name)
		}
		sort.Strings(repos)
		return &axi.Error{
			Msg:  fmt.Sprintf("the last clean gate round recorded the head it reviewed for %s, but none for %s, the repo publish ships", strings.Join(repos, ", "), repoName),
			Code: "PUBLISH_UNREVIEWED_HEAD",
			Help: help,
		}
	}
	if reviewed == head {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the head publish would ship, %s, is not the head the last clean gate round reviewed, %s", head, reviewed),
		Code: "PUBLISH_UNREVIEWED_HEAD",
		Help: help,
	}
}

// checkReviewedIntent refuses, with PUBLISH_UNREVIEWED_INTENT, publishing an
// intent the last clean gate round never reviewed: rep is that round's
// report, which pins the sha256 of the exact bytes resolveIntent read
// (reportIntentYAML.SHA256) for a binding source, brief or explicit.
// Re-reading the same path now and hashing it the same way
// (intentSHA256) catches a brief.md or intent.md edited after the round -
// the file renderIntentSection would otherwise render as if it had been
// reviewed. An inferred or absent intent has nothing pinned to check:
// neither ever reaches the published body.
func checkReviewedIntent(st *store.Store, ticket string, rep reportYAML) error {
	if rep.Intent.Source != IntentSourceBrief && rep.Intent.Source != IntentSourceExplicit {
		return nil
	}
	intentPath := intentFilePath(st, ticket, rep.Intent.Source)
	data, err := os.ReadFile(intentPath)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: read %s: %w", intentPath, err)
	}
	if intentSHA256(rep.Intent.Source, string(data)) == rep.Intent.SHA256 {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("%s has changed since the last clean gate round for %s reviewed it", intentPath, ticket),
		Code: "PUBLISH_UNREVIEWED_INTENT",
		Help: []string{fmt.Sprintf("Run `jig gate %s` to review the intent as it is now, then publish again.", ticket)},
	}
}

// requireFastForward refuses, with PUBLISH_NOT_FAST_FORWARD, a push of branch
// from dir that would not fast-forward origin's copy: origin has commits the
// branch lacks, whether or not the branch has some origin lacks in turn. dir's
// origin/<branch> is as of the fetch just made. A branch origin does not have
// is a new branch, which has nothing to fast-forward. Publish pushes without
// force, so git would refuse such a push in the end; this is the refusal made
// early, with both sides counted, before publish writes to the store.
//
// The copy publish ships was compared with origin's when the lease was pointed
// at it (pointAtTicketBranch), and is refused there when neither holds the
// other; what this catches is a push since, between the acquire's fetch and the
// one publish makes right after.
//
// What to do about it depends on whose copy publish would ship (shipping).
// The build lease's, at buildDir, is where jig's commits wait, so that is where
// the two are integrated: jig merges nothing that is not its own. Origin's own
// copy has nothing to integrate in anywhere - origin moved since the round, by
// a push between publish's two fetches - and the way on is a round over the
// branch as it is now.
func requireFastForward(dir, branch, buildDir, ticket string, shipping branchCopy) error {
	remoteRef := "refs/remotes/origin/" + branch
	tip, err := originsTip(dir, branch)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: %w", err)
	}
	if tip == "" {
		return nil
	}
	ok, err := gitx.IsAncestor(dir, remoteRef, "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: compare %s with origin/%s: %w", branch, branch, err)
	}
	if ok {
		return nil
	}
	theirs, err := gitx.CommitsIn(dir, "refs/heads/"+branch+".."+remoteRef)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: list the commits of origin/%s the copy lacks: %w", branch, err)
	}
	ours, err := gitx.CommitsIn(dir, remoteRef+"..refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: list the commits of %s origin lacks: %w", branch, err)
	}
	next := fmt.Sprintf("Origin's %s moved since the round: run `jig gate %s` to review it as it is now, then publish again", branch, ticket)
	if shipping == buildLeasesCopy {
		next = fmt.Sprintf("Integrate origin's copy in the build lease (for example `git -C %s fetch origin && git -C %s merge origin/%s`), run `jig gate %s`, then publish again", buildDir, buildDir, branch, ticket)
	}
	return &axi.Error{
		Msg: fmt.Sprintf("pushing %s would not be a fast-forward: origin/%s has %d commit(s) the branch lacks, and the branch has %d commit(s) origin lacks",
			branch, branch, len(theirs), len(ours)),
		Code: "PUBLISH_NOT_FAST_FORWARD",
		Help: []string{
			"publish pushes only a fast-forward and never forces: it rewrites nothing that is on origin",
			next,
		},
	}
}
