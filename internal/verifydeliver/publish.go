package verifydeliver

// Publishing a ticket's reviewed branch. The work is in the operator's clone
// and the reviewed head is what ships: publish reconciles nothing, squashes
// nothing and commits nothing on the person's branch, so their branch never
// diverges from origin's because of it. It comes in four forms, each a step
// of the same hand-off between jig and an agent that does the git and gh work:
//
//   - PublishBody renders the pull request body and the review notes for the
//     reviewed head, and changes no lease and no branch;
//   - PublishRecordPR records a pull request somebody opened, after checking
//     it is the ticket's branch;
//   - PublishApprove records that a person approves the reviewed head;
//   - Publish pushes the reviewed head as it is and opens or updates the PR.

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
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// PublishOpts configures one publish, in any of its forms.
type PublishOpts struct {
	Ticket string
	// Repo names the repo to publish, by its name in the project: `jig
	// publish --repo`. "" means the project's first.
	Repo string
	// Yes stands in for the confirmation bare publish asks for.
	Yes bool
	// Picks is the path of a picks file: the recordings an agent chose for
	// the pull request's demo, in the pick result's shape (ParsePicksResult).
	// "" means the pick an earlier publish of the same head made, if any.
	Picks string
}

// PublishReport is Publish's result.
type PublishReport struct {
	Tier string // none
	// Head is repo -> the head the push left on the branch: the head the gate
	// reviewed, unchanged.
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

// publishTarget is what every form of publish settles before it does
// anything: the repo and branch, the ticket's slices, and the latest gate
// round, which must be clean.
type publishTarget struct {
	ticket    string
	repo      project.Repo
	repoName  string
	target    string
	rec       store.Ticket
	branch    string
	slices    []store.Slice
	gateRep   reportYAML
	lastRound int
}

// reviewedHead is the head the last clean gate round reviewed for the repo:
// "" for a scripted round, which recorded none.
func (t *publishTarget) reviewedHead() string { return t.gateRep.ReviewedSHA[t.repoName] }

// selectRepo returns the repo named by name in cfg, or the first when name is
// empty.
func selectRepo(cfg project.Config, name string) (project.Repo, string, string, error) {
	if name == "" {
		repo, n, target := primaryRepo(cfg)
		return repo, n, target, nil
	}
	var names []string
	for _, r := range cfg.Repos {
		if r.Name() == name {
			return r, r.Name(), r.TargetBranch(), nil
		}
		names = append(names, r.Name())
	}
	return project.Repo{}, "", "", &axi.Error{
		Msg:  fmt.Sprintf("the project has no repo %q (it has %s)", name, strings.Join(names, ", ")),
		Code: "VALIDATION_ERROR",
	}
}

// readyToPublish checks what every form of publish needs and reads what it
// works from: every slice is green (the same frontier check gate applies
// before it will even review), the latest gate round is clean and was not
// reviewed by one of the builders, and the intent it pinned is the intent on
// disk. A refusal here leaves nothing written.
func readyToPublish(d Deps, ticket, repoFlag string) (*publishTarget, error) {
	if err := d.Store.Sync(); err != nil {
		return nil, fmt.Errorf("verifydeliver: publish: sync store: %w", err)
	}
	repo, repoName, target, err := selectRepo(d.Cfg, repoFlag)
	if err != nil {
		return nil, err
	}
	// The ticket's record is read once, here, before publish writes anything
	// to the store, and both the branch and the PR title come from that one
	// read: a record that cannot be read fails now rather than after the
	// first write, and one that changes while publish runs cannot give the
	// two different snapshots.
	rec, err := d.Store.ReadTicket(ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: publish: read ticket record: %w", err)
	}
	branch, err := d.Store.ResolveTicketBranch(ticket, rec, target)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: publish: resolve ticket branch: %w", err)
	}
	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: publish: read slices: %w", err)
	}
	if err := checkOneRepo(d, ticket, slices); err != nil {
		return nil, err
	}
	if err := checkFrontier(d, ticket, slices, false); err != nil {
		return nil, err
	}
	gateRep, err := latestGateReport(d.Store, ticket)
	if err != nil {
		return nil, err
	}
	if gateRep.Verdict != "clean" {
		return nil, &axi.Error{
			Msg:  fmt.Sprintf("latest gate round for %s returned %q, not clean", ticket, gateRep.Verdict),
			Code: "PUBLISH_NOT_CLEAN",
			Help: []string{fmt.Sprintf("Run `jig gate %s` until it reports clean before publishing.", ticket)},
		}
	}
	if gateRep.Independent == IndependentNo {
		return nil, &axi.Error{
			Msg:  fmt.Sprintf("the latest gate round for %s was reviewed by an agent that built part of it", ticket),
			Code: "PUBLISH_NOT_INDEPENDENT",
			Help: []string{fmt.Sprintf("Have a fresh-context reviewer review it: `jig gate %s`", ticket)},
		}
	}
	if err := checkReviewedIntent(d.Store, ticket, gateRep); err != nil {
		return nil, err
	}
	lastRound, err := existingGateRounds(d.Store, ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: publish: count gate rounds: %w", err)
	}
	return &publishTarget{
		ticket: ticket, repo: repo, repoName: repoName, target: target, rec: rec, branch: branch,
		slices: slices, gateRep: gateRep, lastRound: lastRound,
	}, nil
}

// publishFuncs are the seams a publish form takes from Deps, each with its
// production default.
type publishFuncs struct {
	confirm     func(branch, ticket, openPR string, hasHost bool) bool
	guardedPush func(dir, remote, branch string, confirmed bool) error
	fetchOrigin func(dir string) error
	warn        func(format string, args ...any)
}

func (d Deps) publishFuncs() publishFuncs {
	f := publishFuncs{confirm: d.Confirm, guardedPush: d.GuardedPush, fetchOrigin: d.FetchOrigin, warn: d.Warn}
	// The confirm default covers the whole interactive seam - both the
	// fmt.Printf prompt and the stdin read - so a test that hands its own
	// never touches the real terminal: stubbing only the read half would
	// still print the literal prompt text to the test's real stdout. openPR
	// is the URL of the pull request the branch already has open, or "", and
	// hasHost says whether the repo has a pull-request host at all: the
	// question names which of the things it is agreeing to - opening a pull
	// request, updating that one, or pushing alone when there is no host.
	if f.confirm == nil {
		f.confirm = func(branch, ticket, openPR string, hasHost bool) bool {
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
	if f.guardedPush == nil {
		f.guardedPush = gitx.GuardedPush
	}
	if f.fetchOrigin == nil {
		f.fetchOrigin = func(dir string) error {
			_, err := gitx.Run(dir, "fetch", "origin")
			return err
		}
	}
	if f.warn == nil {
		f.warn = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format, args...)
		}
	}
	return f
}

// Publish pushes the reviewed head of the ticket's branch as it is and opens
// or updates its pull request. It reconciles nothing, squashes nothing and
// commits nothing on the branch: the head it pushes is the head the last
// clean gate round reviewed, taken from the operator's clone (or origin's
// copy, when that is the newer), so the person's branch never diverges from
// origin's because of a publish. The pull request body is rendered from the
// store's records (PublishBody renders the same), the recordings the demo
// shows come from the picks file or an earlier pick (o.Picks), and the store
// gets the ticket's changelogs, ledger entry and evidence.
func Publish(d Deps, o PublishOpts) (report PublishReport, err error) {
	ticket := o.Ticket
	fn := d.publishFuncs()

	// journaled backs the deferred best-effort push below: once Publish's
	// first tracked write to the store's working copy has landed, any later
	// error - an unreviewed head, a declined confirmation, a push or
	// PR-creation failure - would otherwise leave that write uncommitted
	// until whatever later command next calls Store.Sync, on this ticket or
	// any other (see failureCode for why this push runs here rather than
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

	pt, err := readyToPublish(d, ticket, o.Repo)
	if err != nil {
		return PublishReport{}, err
	}
	branch, repoName, target := pt.branch, pt.repoName, pt.target
	picks, err := loadPicksFile(o.Picks)
	if err != nil {
		return PublishReport{}, err
	}

	// The publish lease is re-pointed at the copy publish ships right after
	// the acquire below, so its own copy of the branch is disposable: one left
	// by an earlier attempt must not refuse the acquire (BRANCH_DIVERGED) over
	// commits nobody keeps, which it does once the branch has reached origin
	// another way.
	if err := restoreLeaseBeforeAcquire(d.Home, repoName, ticket, pool.Publish, branch); err != nil {
		return PublishReport{}, err
	}
	jlines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	built := journal.BuiltCommits(jlines)
	lease, err := pool.Acquire(d.Home, repoName, pt.repo.Remote, target, branch, ticket, pool.Publish)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: acquire lease: %w", err)
	}
	// The lease is pointed at the copy of the branch the gate reviewed, by the
	// same function the gate uses: the clone's when it has the newer, origin's
	// otherwise.
	shipping, err := pointAtBranchCopy(d, lease.Dir, repoName, ticket, branch, pt.rec.Adopted(), built, "publish")
	if err != nil {
		return PublishReport{}, err
	}
	if err := fn.fetchOrigin(lease.Dir); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: fetch origin: %w", err)
	}

	// Publish ships what was reviewed: the head it would ship must be the head
	// the last clean round reviewed (when that round recorded heads), because a
	// commit that landed on the branch since would go out under a verdict that
	// never saw it. Checked ahead of everything that writes, so a refusal
	// leaves the store untouched.
	shipHead, err := gitx.RevParse(lease.Dir, "HEAD")
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: resolve the head to ship: %w", err)
	}
	if err := checkReviewedHead(pt.gateRep, repoName, shipHead, ticket); err != nil {
		return PublishReport{}, err
	}
	// Publish never rewrites what is on origin and never forces: a branch that
	// is already there must be a fast-forward from where it stands. Checked
	// here for the same reason as above - git would refuse the push, but only
	// after publish had written the store.
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
	host, err := d.publishHost(pt.repo.Remote)
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

	touchedFiles, err := diffFiles(lease.Dir, target, "HEAD")
	if err != nil {
		return PublishReport{}, err
	}
	if len(touchedFiles) == 0 {
		return PublishReport{}, &axi.Error{
			Msg:  "the branch changes nothing against the target - stale-overwrite suspicion",
			Code: "PUBLISH_NO_DIVERGENCE",
		}
	}
	if err := checkNonEmptyRange(lease.Dir, target, "HEAD"); err != nil {
		return PublishReport{}, err
	}

	man, err := manifest.Resolve(lease.Dir)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: resolve manifest: %w", err)
	}
	oracleNames := pt.oracleNames(man)

	// Everything from here to the push writes the store's working copy.
	journaled = true
	pub, err := renderPublication(d, pt, renderInput{
		dir: lease.Dir, head: shipHead, touched: touchedFiles, oracleNames: oracleNames,
		picks: picks, warn: fn.warn,
	})
	if err != nil {
		return PublishReport{}, err
	}

	// confirmed tracks whether an actual publish confirmation ran: --yes
	// stands in for it, a person's recorded approval of this head does too,
	// or an interactive "y"/"yes" answer does. A decline stops cleanly here:
	// the ticket branch itself is never pushed (the deferred push above still
	// runs and pushes the store's own record of the attempt - only
	// guardedPush's push of the ticket branch below is skipped). confirmed is
	// the only value ever passed to guardedPush; it is never hardcoded to
	// true, so a non-local remote with no confirmation is refused by the gitx
	// guard downstream.
	confirmed := o.Yes || approvedByPerson(jlines, shipHead)
	if !confirmed {
		if !fn.confirm(branch, ticket, openPR, host != nil) {
			return PublishReport{}, &axi.Error{Msg: "publish declined at confirmation", Code: "PUBLISH_DECLINED"}
		}
		confirmed = true
	}

	// The files a pick staged are attached by name from their directory, and
	// the confirmation has run since they were copied: check them once more
	// before anything is pushed. A pick's media are never uploaded unchecked,
	// and a change here refuses the publish with origin untouched: the next
	// publish reuses the journaled pick (the head and the recordings are the
	// same) and stages the files afresh, with no gate round. What is left
	// after this is the push and the host's own read of the files, which a
	// check made by jig cannot close, since the host opens them by name.
	picked := pub.picked
	if picked != nil && host != nil {
		if err := verifyStaged(absPath(filepath.Join(d.Home, "evidence")), picked.MediaDir, picked.MediaFiles); err != nil {
			return PublishReport{}, &axi.Error{
				Msg:  fmt.Sprintf("the recordings staged for %s changed before they were attached: %v", ticket, err),
				Code: "PUBLISH_PICKS_CHANGED",
				Help: []string{fmt.Sprintf("Nothing was pushed. Run `jig publish %s` again: it reuses the pick and stages the recordings afresh.", ticket)},
			}
		}
	}

	if err := fn.guardedPush(lease.Dir, "origin", branch, confirmed); err != nil {
		return PublishReport{}, err
	}

	prURL, prOutcome, err := openOrUpdatePR(d, host, pt, pub, openPR, fn.warn)
	if err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventPr, Outcome: prOutcome, Commit: shipHead, URL: prURL, Branch: branch}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal pr: %w", err)
	}
	postPublishChecks(d, host, ticket, prURL, pub, fn.warn)

	// What a publish leaves in the store about the ticket is written once the
	// branch is out: a publish that is declined or that fails before the push
	// leaves no changelog or ledger entry behind to be written again by the
	// one that follows.
	if err := writeStoreDocs(d, pt, oracleNames); err != nil {
		return PublishReport{}, err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventPublishDone}); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal publish-done: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: publish", ticket)); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: push store: %w", err)
	}

	prNote := map[string]string{}
	if host == nil {
		prNote[repoName] = "no pull-request host"
	}
	return PublishReport{
		Tier:      "none",
		Head:      map[string]string{repoName: shipHead},
		PRBody:    map[string]string{repoName: pub.prPath},
		PRURL:     map[string]string{repoName: prURL},
		PRUpdated: map[string]bool{repoName: prOutcome == "updated"},
		PRNote:    prNote,
		Picks:     pub.picks,
	}, nil
}

// oracleNames are the manifest's oracle names the last clean round recorded
// (report.yaml's oracle_names), or, for a round that recorded none, the
// names man has.
func (t *publishTarget) oracleNames(man manifest.Manifest) []string {
	if len(t.gateRep.OracleNames) > 0 {
		return append([]string(nil), t.gateRep.OracleNames...)
	}
	return SortedOracleNames(man)
}

// approvedByPerson reports whether a person approved publishing head
// (`jig publish --approve`): the last publish-approved line for that head
// speaks for a person.
func approvedByPerson(lines []journal.Line, head string) bool {
	approved := false
	for _, l := range lines {
		if l.Event == journal.EventPublishApproved && l.Commit == head {
			approved = l.Provenance == "person"
		}
	}
	return approved
}

// writeStoreDocs writes what a publish leaves in the store about the ticket:
// its changelogs, its entry in the project ledger and contract index, and the
// evidence list. None of it touches a branch.
func writeStoreDocs(d Deps, t *publishTarget, oracleNames []string) error {
	ticket := t.ticket
	questions, err := d.Store.ReadQuestions(ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: read questions: %w", err)
	}
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	if err := writeChangelogs(d.Store, ticket, t.slices, lines); err != nil {
		return err
	}
	title := consolidatedTitle(t.rec.Title, ticket, t.slices)
	if err := appendLedgerEntry(d.Store, ticket, title, t.slices, questions); err != nil {
		return err
	}
	if err := appendContractIndexEntry(d.Store, d.Cfg.Platform, ticket, t.slices, oracleNames); err != nil {
		return err
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventChangelog}); err != nil {
		return fmt.Errorf("verifydeliver: publish: journal changelog: %w", err)
	}
	return writeEvidence(d.Store, ticket, t.lastRound)
}

// renderInput is what rendering a publication reads: the repository to read
// commits from and the head the gate reviewed, the files the change touched,
// the oracle names, and the picks file.
type renderInput struct {
	dir         string
	head        string
	touched     []string
	oracleNames []string
	picks       *PicksResult
	warn        func(format string, args ...any)
}

// publication is what renderPublication wrote and decided: the pull request
// body at prPath (store-relative), the media a pick staged, and the pick's
// report.
type publication struct {
	prPath     string
	title      string
	picks      PicksReport
	picked     *DemoRenderResult
	demoResult DemoRenderResult
	mediaDir   string
	mediaFiles []string
}

// renderPublication renders the pull request body and the review notes for
// the reviewed head from the store's records: no model is asked, no lease is
// written, and no branch is touched. The recordings the demo shows are the
// picks file's, or an earlier pick of the same head and recordings.
func renderPublication(d Deps, t *publishTarget, in renderInput) (publication, error) {
	ticket := t.ticket
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return publication{}, fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	title := consolidatedTitle(t.rec.Title, ticket, t.slices)
	commits := lastGreenCommits(lines)
	// An adopted ticket's own commits, from the merge base with target up to
	// the start sha recorded at adoption: the author's work, listed ahead of
	// jig's in What changed.
	var authorCommits []authorCommit
	if t.rec.Adopted() {
		startData, err := os.ReadFile(d.Store.StartSHAPath(ticket, t.repoName))
		if err != nil {
			return publication{}, fmt.Errorf("verifydeliver: publish: read start sha: %w", err)
		}
		authorCommits, err = adoptedAuthorCommits(in.dir, t.target, strings.TrimSpace(string(startData)))
		if err != nil {
			return publication{}, err
		}
	}
	outcomes, reviewedPaths, lastSummary, err := collectFindingsWithOutcomes(d.Store.TicketDir(ticket), t.lastRound, t.slices)
	if err != nil {
		return publication{}, err
	}
	// The recordings the build made, picked for the pull request (ADR 0029).
	// A pick that stands renders the ## Demo section; one that does not, or
	// none to make, leaves the pull request without one. The pick is made for
	// the head the gate reviewed, whose history holds the recorded commits.
	picksReport, picked, err := publishPicks(picksStep{
		d: d, result: in.picks, ticket: ticket, repoName: t.repoName,
		leaseDir: in.dir, head: in.head, lines: lines, warn: in.warn,
	})
	if err != nil {
		return publication{}, err
	}
	prPath, omittedBriefIntent, demoResult, err := writePRBody(d.Store, ticket, t.repoName, t.slices, t.gateRep, "none", commits, authorCommits, in.oracleNames, outcomes, picked)
	if err != nil {
		return publication{}, err
	}

	// The media directory and file names for attachment, when a pick staged
	// files: demoResult.MediaDir is the staging directory (picksStep.stage)
	// the pick copied and checked them in, for the head the gate reviewed.
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
	// So the operator is told here, before anything is pushed, while fixing the
	// brief and gating again is still cheaper than editing a published pull
	// request. The warning says "with text" because both briefs reach it:
	// naming only the missing heading would point the operator at an edit that
	// does not fix the brief that has one and nothing under it.
	if omittedBriefIntent {
		in.warn("jig: the pull request body for %s has no ## Intent section: %s has no \"## \" section with text to publish as one\n",
			ticket, intentFilePath(d.Store, ticket, IntentSourceBrief))
	}

	// A pull request has a ## Demo section only from a pick. One that was
	// refused or failed has been said where it was (publishPicks); a ticket
	// with nothing to pick from has none, and is told so here. When the build
	// did record, the recordings left out of the choice have been named
	// (publishPicks), so this says none is left rather than none was made.
	if picked == nil && picksReport.Status == "" {
		if len(picksReport.Dropped) > 0 {
			in.warn("jig: no demo for %s: no recordings are left to show\n", ticket)
		} else {
			in.warn("jig: no demo for %s: there are no recordings to show\n", ticket)
		}
	}

	// Report any summary or caption left out of the rendered section because
	// it named one of jig's own directories (the owner's decision on r1-f13,
	// DECISIONS.md): independent of whether a pick stands, since a section can
	// be otherwise rendered in full.
	if demoResult.ScrubbedSummary {
		in.warn("jig: the demo summary for %s named one of jig's own directories and was left out of the pull request body\n", ticket)
	}
	if len(demoResult.ScrubbedCaptions) > 0 {
		in.warn("jig: captions left out of the pull request body for %s because they named one of jig's own directories: %v\n",
			ticket, demoResult.ScrubbedCaptions)
	}

	if err := writeReviewNotes(d.Store, ticket, t.lastRound, lastSummary, outcomes, reviewedPaths, in.touched); err != nil {
		return publication{}, err
	}
	return publication{
		prPath: prPath, title: title, picks: picksReport, picked: picked, demoResult: demoResult,
		mediaDir: demoResult.MediaDir, mediaFiles: mediaFiles,
	}, nil
}

// openOrUpdatePR opens the branch's pull request, or updates the one it
// already has open into the target with this publish's body: none is opened
// beside an open one. A closed or merged pull request, or one into another
// base, is not that one (FindOpenPR): the operator asked to publish, so one is
// opened, and the others are left as they are. A repo with no pull-request
// host gets neither, and the outcome says so.
func openOrUpdatePR(d Deps, host repohost.Host, t *publishTarget, r publication, openPR string, warn func(format string, args ...any)) (url, outcome string, err error) {
	if host == nil {
		return "", "none:no-host", nil
	}
	bodyFile := filepath.Join(d.Store.Root, r.prPath)
	if openPR != "" {
		if len(r.mediaFiles) > 0 {
			attached, err := host.UpdatePRWithMedia(openPR, bodyFile, r.mediaDir, r.mediaFiles)
			if err != nil {
				return "", "", fmt.Errorf("verifydeliver: publish: update PR %s with media: %w", openPR, err)
			}
			if !attached {
				warn("jig: the installed gh does not support --attach; the pull request updated for %s carries no media\n", t.ticket)
			}
		} else if err := host.UpdatePR(openPR, bodyFile); err != nil {
			return "", "", fmt.Errorf("verifydeliver: publish: update PR %s: %w", openPR, err)
		}
		return openPR, "updated", nil
	}
	if len(r.mediaFiles) > 0 {
		url, attached, err := host.CreatePRWithMedia(t.branch, t.target, r.title, bodyFile, r.mediaDir, r.mediaFiles)
		if err != nil {
			return "", "", fmt.Errorf("verifydeliver: publish: create PR with media: %w", err)
		}
		if !attached {
			warn("jig: the installed gh does not support --attach; the pull request opened for %s carries no media\n", t.ticket)
		}
		return url, "opened", nil
	}
	url, err = host.CreatePR(t.branch, t.target, r.title, bodyFile)
	if err != nil {
		return "", "", fmt.Errorf("verifydeliver: publish: create PR: %w", err)
	}
	return url, "opened", nil
}

// postPublishChecks does what follows a pull request that now exists: reads
// its body back to check the media references were rewritten, and posts the
// review notes as its first comment. Both are soft: a failure is a warning,
// and the pull request stands.
func postPublishChecks(d Deps, host repohost.Host, ticket, prURL string, r publication, warn func(format string, args ...any)) {
	if prURL == "" || host == nil {
		return
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
	if len(r.mediaFiles) > 0 {
		body, err := host.ReadPRBody(prURL)
		if err != nil {
			warn("jig: read back the pull request body for %s to check for unrewritten media references: %v\n", ticket, err)
		} else if unrewritten := checkUnrewrittenReferences(body, r.demoResult.MediaFiles); len(unrewritten) > 0 {
			patched, changed, stillUnrewritten := rewriteUnrewrittenReferences(body, unrewritten)
			if changed {
				bodyPath := filepath.Join(d.Store.Root, r.prPath)
				if err := os.WriteFile(bodyPath, []byte(patched), 0o644); err != nil {
					warn("jig: write the patched pull request body for %s: %v\n", ticket, err)
				} else if err := host.UpdatePR(prURL, bodyPath); err != nil {
					warn("jig: rewrite unrewritten media references in the pull request body for %s: %v\n", ticket, err)
				}
			}
			if len(stillUnrewritten) > 0 {
				warn("jig: unrewritten media references in the pull request body for %s: %v\n", ticket, stillUnrewritten)
			}
		}
	}

	// Post the review notes as a comment on the pull request. A failed post is
	// a soft failure, not a publish failure: the pull request stands, and the
	// file is kept in the store for a manual post.
	reviewNotesPath := filepath.Join(d.Store.TicketDir(ticket), "pr", "review-notes.md")
	if err := host.CommentPR(prURL, reviewNotesPath); err != nil {
		warn("jig: post %s as a comment on %s: %v\n", reviewNotesPath, prURL, err)
	}
}

// BodyReport is PublishBody's result.
type BodyReport struct {
	Repo string
	// Branch is the ticket's branch and Target the branch its pull request
	// goes into.
	Branch, Target string
	// Head is the reviewed head the body is for.
	Head string
	// PRBody is the store-relative path of the body, and ReviewNotes of the
	// notes the pull request's first comment carries.
	PRBody      string
	ReviewNotes string
	Title       string
	Picks       PicksReport
}

// PublishBody renders the pull request body (pr/<repo>.md) and the review
// notes for the head the last clean gate round reviewed, and journals a
// pr-body line: the first step of publishing when an agent does the pushing.
// It changes no lease and no branch, runs nothing, and needs the reviewed head
// to be the tip of the clone's branch or of origin's: a body for a head that
// is neither describes a pull request nobody will open.
func PublishBody(d Deps, o PublishOpts) (BodyReport, error) {
	ticket := o.Ticket
	fn := d.publishFuncs()
	pt, err := readyToPublish(d, ticket, o.Repo)
	if err != nil {
		return BodyReport{}, err
	}
	picks, err := loadPicksFile(o.Picks)
	if err != nil {
		return BodyReport{}, err
	}
	dir, err := readerDir(d, pt)
	if err != nil {
		return BodyReport{}, err
	}
	head := pt.reviewedHead()
	if err := checkBodyHead(d, pt, dir); err != nil {
		return BodyReport{}, err
	}
	rev := head
	if rev == "" {
		rev = "HEAD"
	}
	touched, err := diffFiles(dir, pt.target, rev)
	if err != nil {
		return BodyReport{}, err
	}
	man, err := manifest.Resolve(dir)
	if err != nil {
		return BodyReport{}, fmt.Errorf("verifydeliver: publish: resolve manifest: %w", err)
	}
	r, err := renderPublication(d, pt, renderInput{
		dir: dir, head: rev, touched: touched, oracleNames: pt.oracleNames(man), picks: picks, warn: fn.warn,
	})
	if err != nil {
		return BodyReport{}, err
	}
	line := journal.Line{Event: journal.EventPrBody, Commit: head, Branch: pt.branch, Ref: r.prPath}
	if err := journal.Append(d.Store, ticket, line); err != nil {
		return BodyReport{}, fmt.Errorf("verifydeliver: publish: journal pr-body: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: pr body", ticket)); err != nil {
		return BodyReport{}, fmt.Errorf("verifydeliver: publish: push store: %w", err)
	}
	return BodyReport{
		Repo: pt.repoName, Branch: pt.branch, Target: pt.target, Head: head, PRBody: r.prPath, Title: r.title, Picks: r.picks,
		ReviewNotes: filepath.ToSlash(filepath.Join(d.Store.TicketRelDir(ticket), "pr", "review-notes.md")),
	}, nil
}

// readerDir is a repository to read the reviewed head from without changing
// it: the gate lease the last round reviewed in, else the operator's clone.
func readerDir(d Deps, t *publishTarget) (string, error) {
	if dir, err := pool.Dir(d.Home, t.repoName, t.ticket, pool.Gate); err == nil && pool.Usable(dir) {
		return dir, nil
	}
	if clone := operatorClone(d, t.repoName); clone != "" {
		return clone, nil
	}
	return "", &axi.Error{
		Msg:  fmt.Sprintf("no checkout of %s holds the reviewed head: the gate lease is gone and no clone is mapped", t.repoName),
		Code: "PUBLISH_NO_CHECKOUT",
		Help: []string{fmt.Sprintf("Run `jig gate %s` again, or map the clone with `jig map %s=<path>`", t.ticket, t.repoName)},
	}
}

// branchTips are the commits the ticket's branch is at in the places a
// reviewed head can be found: the operator's clone (its branch, and its last
// view of origin's), the repository dir reads from (its view of origin's) and,
// for work an earlier jig built, the build lease.
func branchTips(d Deps, t *publishTarget, dir string) []string {
	var tips []string
	add := func(repo, ref string) {
		if repo == "" {
			return
		}
		if sha, err := gitx.RevParse(repo, ref); err == nil {
			tips = append(tips, sha)
		}
	}
	clone := operatorClone(d, t.repoName)
	add(clone, "refs/heads/"+t.branch)
	add(clone, "refs/remotes/origin/"+t.branch)
	add(dir, "refs/remotes/origin/"+t.branch)
	if buildDir, err := pool.Dir(d.Home, t.repoName, t.ticket, pool.Build); err == nil && pool.Usable(buildDir) {
		add(buildDir, "refs/heads/"+t.branch)
	}
	return tips
}

// checkBodyHead refuses, with PUBLISH_UNREVIEWED_HEAD, a body for a head that
// is not where the branch is: the reviewed head must be the tip of the clone's
// branch or of origin's. A scripted round recorded no head and is let through.
func checkBodyHead(d Deps, t *publishTarget, dir string) error {
	reviewed := t.reviewedHead()
	if len(t.gateRep.ReviewedSHA) == 0 {
		return nil
	}
	help := []string{fmt.Sprintf("Run `jig gate %s` to review the branch as it is now, then render the body again.", t.ticket)}
	if reviewed == "" {
		return checkReviewedHead(t.gateRep, t.repoName, "", t.ticket)
	}
	for _, tip := range branchTips(d, t, dir) {
		if tip == reviewed {
			return nil
		}
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the head the last clean gate round reviewed, %s, is not where branch %s is, in the clone or on origin", shortSHA(reviewed), t.branch),
		Code: "PUBLISH_UNREVIEWED_HEAD",
		Help: help,
	}
}

// RecordReport is PublishRecordPR's result.
type RecordReport struct {
	Repo   string
	URL    string
	Branch string
	Commit string
	// Verified is true when the host confirmed the pull request is from the
	// ticket's branch; false when the repo has no pull-request host to ask,
	// and the pull request was recorded as given.
	Verified bool
}

// PublishRecordPR records the pull request at prURL, which somebody other than
// jig opened, as the ticket's: after checking, when the repo has a
// pull-request host, that it is from the ticket's branch and, when the host
// says which commit, from the reviewed head. It journals a pr line and the
// publish-done line, writes what a publish leaves in the store (changelogs,
// ledger entry, evidence), and pushes the store. It changes no lease and no
// branch and pushes nothing of the repo.
func PublishRecordPR(d Deps, o PublishOpts, prURL string, warn func(format string, args ...any)) (RecordReport, error) {
	ticket := o.Ticket
	if warn == nil {
		warn = d.publishFuncs().warn
	}
	ref, err := repohost.ParsePRURL(prURL)
	if err != nil {
		return RecordReport{}, err
	}
	pt, err := readyToPublish(d, ticket, o.Repo)
	if err != nil {
		return RecordReport{}, err
	}
	host, err := d.publishHost(pt.repo.Remote)
	if err != nil {
		return RecordReport{}, fmt.Errorf("verifydeliver: publish: build repo host: %w", err)
	}
	reviewed := pt.reviewedHead()
	commit := reviewed
	verified := false
	if host == nil {
		warn("jig: %s has no pull-request host to ask: recorded pull request %s for %s without checking it is from %s\n", pt.repoName, prURL, ticket, pt.branch)
	} else {
		head, err := host.PRHead(prURL)
		if err != nil {
			return RecordReport{}, fmt.Errorf("verifydeliver: publish: read the head of %s: %w", prURL, err)
		}
		if head.Branch != pt.branch {
			return RecordReport{}, &axi.Error{
				Msg:  fmt.Sprintf("pull request #%d is from branch %s, not %s, the branch of %s", ref.Number, head.Branch, pt.branch, ticket),
				Code: "PR_HEAD_MISMATCH",
				Help: []string{fmt.Sprintf("Open the pull request from %s, or record the one that is", pt.branch)},
			}
		}
		if reviewed != "" && head.SHA != "" && head.SHA != reviewed {
			return RecordReport{}, &axi.Error{
				Msg:  fmt.Sprintf("pull request #%d is at %s, not %s, the head the last clean gate round reviewed", ref.Number, shortSHA(head.SHA), shortSHA(reviewed)),
				Code: "PUBLISH_UNREVIEWED_HEAD",
				Help: []string{fmt.Sprintf("Run `jig gate %s` to review the branch as it is now, then record the pull request again.", ticket)},
			}
		}
		if head.SHA != "" {
			commit = head.SHA
		}
		verified = true
	}

	oracleNames := pt.gateRep.OracleNames
	if len(oracleNames) == 0 {
		if dir, err := readerDir(d, pt); err == nil {
			if man, err := manifest.Resolve(dir); err == nil {
				oracleNames = SortedOracleNames(man)
			}
		}
	}
	if err := writeStoreDocs(d, pt, oracleNames); err != nil {
		return RecordReport{}, err
	}
	outcome := "recorded"
	if !verified {
		outcome = "recorded:unverified"
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventPr, Outcome: outcome, Commit: commit, URL: prURL, Branch: pt.branch}); err != nil {
		return RecordReport{}, fmt.Errorf("verifydeliver: publish: journal pr: %w", err)
	}
	if err := journal.Append(d.Store, ticket, journal.Line{Event: journal.EventPublishDone}); err != nil {
		return RecordReport{}, fmt.Errorf("verifydeliver: publish: journal publish-done: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: publish", ticket)); err != nil {
		return RecordReport{}, fmt.Errorf("verifydeliver: publish: push store: %w", err)
	}
	return RecordReport{Repo: pt.repoName, URL: prURL, Branch: pt.branch, Commit: commit, Verified: verified}, nil
}

// ApproveReport is PublishApprove's result.
type ApproveReport struct {
	Repo   string
	Head   string
	Branch string
	// Provenance is who the approval speaks for.
	Provenance string
}

// PublishApprove records, as a publish-approved line, that provenance (a
// person at a terminal, or an agent) approves publishing the head the last
// clean gate round reviewed. Only a person's approval stands in for the
// confirmation a bare publish asks for.
func PublishApprove(d Deps, o PublishOpts, provenance string) (ApproveReport, error) {
	pt, err := readyToPublish(d, o.Ticket, o.Repo)
	if err != nil {
		return ApproveReport{}, err
	}
	head := pt.reviewedHead()
	if head == "" {
		return ApproveReport{}, &axi.Error{
			Msg:  fmt.Sprintf("the latest gate round for %s recorded no reviewed head to approve", o.Ticket),
			Code: "PUBLISH_UNREVIEWED_HEAD",
			Help: []string{fmt.Sprintf("Run `jig gate %s` and have it reviewed", o.Ticket)},
		}
	}
	line := journal.Line{Event: journal.EventPublishApproved, Commit: head, Branch: pt.branch, Provenance: provenance}
	if err := journal.Append(d.Store, o.Ticket, line); err != nil {
		return ApproveReport{}, fmt.Errorf("verifydeliver: publish: journal publish-approved: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: publish approved", o.Ticket)); err != nil {
		return ApproveReport{}, fmt.Errorf("verifydeliver: publish: push store: %w", err)
	}
	return ApproveReport{Repo: pt.repoName, Head: head, Branch: pt.branch, Provenance: provenance}, nil
}

// loadPicksFile reads and strictly parses the picks file `jig publish --picks`
// names, or returns nil for no file.
func loadPicksFile(path string) (*PicksResult, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &axi.Error{Msg: fmt.Sprintf("the picks file is unreadable: %v", err), Code: "PICKS_UNREADABLE"}
	}
	res, err := ParsePicksResult(data)
	if err != nil {
		return nil, &axi.Error{Msg: err.Error(), Code: "PICKS_INVALID"}
	}
	return &res, nil
}

// diffFiles returns the repo-relative paths that differ between origin/target
// and rev, using the triple-dot form (relative to their merge base). It is the
// change's own files for the divergence check and review-notes' Coverage
// section.
func diffFiles(dir, target, rev string) ([]string, error) {
	out, err := gitx.Run(dir, "diff", "--name-only", "origin/"+target+"..."+rev)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: publish: diff origin/%s...%s: %w", target, rev, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// checkNonEmptyRange refuses to publish when rev has no commits beyond
// origin/target: an all-green ticket whose slices never landed a commit, or a
// re-publish after the branch was merged into the target, has nothing to
// publish. Checked up front, before any of the doc/evidence store writes
// further down Publish.
func checkNonEmptyRange(dir, target, rev string) error {
	start, err := gitx.MergeBase(dir, "origin/"+target, rev)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: merge-base origin/%s %s: %w", target, rev, err)
	}
	commits, err := gitx.CommitsIn(dir, start+".."+rev)
	if err != nil {
		return fmt.Errorf("verifydeliver: publish: list %s..%s: %w", start, rev, err)
	}
	if len(commits) == 0 {
		return &axi.Error{
			Msg:  fmt.Sprintf("nothing to publish: no commits beyond origin/%s", target),
			Code: "NOTHING_TO_PUBLISH",
		}
	}
	return nil
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
