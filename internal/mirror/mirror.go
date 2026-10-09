// Package mirror is jig's own GitHub mirror: after every store checkpoint,
// in the same process, it syncs the store onto GitHub issues (and, later
// slices, a GitHub Project). internal/mirror holds what any tracker
// provider would share; internal/mirror/github is the GitHub client this
// one tracker uses today. The mirror depends on the store, never the
// reverse: internal/store offers the checkpoint hook (Store.AfterCheckpoint)
// this package's Sync is meant to be wired into.
//
// This slice ("tracer") is narrow by design: it creates the issue for a
// ticket or chart that has none yet, with its title and footer, and writes
// the record the bridge used to keep. Updating an existing issue, the board,
// links and drift are later slices' own work. The "safety" slice scans a
// title and body for every ticket or chart Sync is about to create a new
// issue for, before writing it, when the issue repo is public: a hit skips
// that one ticket or chart (brief.md#Publish safety).
package mirror

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// syncTimeout bounds one sync as a whole, not any one GitHub call within it,
// for a sync that runs from inside a command (opts.Unbounded false): one
// context, built once at the top of doSync and threaded through every call
// it makes, so a store with many items cannot spend syncTimeout per item
// (brief.md#Syncing at every checkpoint: "stops after 2 minutes, with a
// warning"). `jig trackers sync` runs with opts.Unbounded true instead: a
// first sync of a sizeable store needs more than syncTimeout at one mutation
// per second (one per ticket is 5 or more: createIssue, updateIssue, up to
// two link mutations, three board mutations), so a sync bounded inside every
// command would never finish it; the owner's call is to keep the bound only
// where it protects a command's own latency, and let the on-demand command
// run to completion instead (gate finding r2-f5).
const syncTimeout = 2 * time.Minute

// Deps bundles what Sync needs beyond the store and project config, per
// #84: a test hands its own Client (and need touch no network), production
// leaves it nil to build the real one against api.github.com with the
// token `gh auth token` prints.
type Deps struct {
	Store *store.Store
	Cfg   project.Config
	// Home is the jig home root (JIG_HOME for the command). Unused by this
	// slice; carried so a later one (publish-terms.txt) does not need to
	// widen Deps again.
	Home string
	// Client talks to GitHub. nil builds github.New against
	// https://api.github.com/graphql, authenticated by GhAuthToken.
	Client github.Client
	// SyncTimeout overrides syncTimeout when non-zero (a test shrinks it to
	// exercise the bound without waiting out the real 2 minutes). Unused
	// when SyncOpts.Unbounded is set.
	SyncTimeout time.Duration
}

// SyncOpts are jig trackers sync's own flags.
type SyncOpts struct {
	// DryRun reads everything (the store, and GitHub for anything that
	// already has a record) and reports what would change, writing to
	// neither (brief.md#jig trackers sync, brief.md#Cutover). A store whose
	// items are all new reaches no client, since there is nothing on GitHub
	// yet for it to read.
	DryRun bool
	// Unbounded lifts syncTimeout: this sync runs until it is done rather
	// than stopping after 2 minutes. Set by `jig trackers sync`, which is
	// run on demand rather than from inside another command, so it has no
	// reason to share that bound (gate finding r2-f5); a sync from inside a
	// command (the checkpoint hook) leaves this false.
	Unbounded bool
}

// SkippedItem is one ticket or chart the publish-safety scanner skipped:
// the first line of its title or body that hit, named by its line number
// within that text (the title is line 1) and the line itself
// (brief.md#Publish safety).
type SkippedItem struct {
	What string
	Line int
	Text string
}

// CreatedIssue is one issue Sync created (or, in a dry run, would create).
type CreatedIssue struct {
	// What is the ticket id, or "charts/<name>" for a chart.
	What        string
	Owner, Repo string
	// Number is the issue's number. Zero in a dry run, where no issue was
	// actually opened.
	Number int
}

// SyncReport is what one Sync call did, or would do.
type SyncReport struct {
	// NoTracker reports that cfg declared no trackers: github: entry, so
	// nothing else in the report is populated.
	NoTracker bool
	Created   []CreatedIssue
	// Updated is every issue Sync brought up to date: its title, body or
	// open/closed state changed to match the store (brief.md#Syncing at
	// every checkpoint, step 4).
	Updated []UpdatedIssue
	// Recreated is every issue Sync opened again because its recorded one was
	// gone from GitHub (brief.md#Adoption).
	Recreated []CreatedIssue
	// Drift is every jig-owned field this sync found edited on GitHub and
	// overwrote with the store's value again (brief.md#Ownership and drift).
	Drift []DriftLine
	// LinkedParents is every ticket Sync made (or found already) a
	// sub-issue of its chart's issue (brief.md#Links). A dry run previews
	// this the same way, without writing the link.
	LinkedParents []LinkedParent
	// LinkedBlockedBy is every blocked_by edge Sync turned into a native
	// blocked-by link, previewed the same way in a dry run.
	LinkedBlockedBy []LinkedBlockedBy
	// NotLinked is every blocked_by edge Sync could not link because its
	// blocker has no issue yet (brief.md#Links).
	NotLinked []NotLinked
	// Skipped is every ticket or chart Sync did not create an issue for
	// because its title or body tripped the publish-safety scanner
	// (brief.md#Publish safety). jig trackers sync exits non-zero when this
	// is non-empty.
	Skipped []SkippedItem
	// Placed is every issue (ticket or chart) Sync placed on the project
	// board, with the Status it set (brief.md#The board), previewed the
	// same way in a dry run.
	Placed []PlacedItem
	// PlacedPRs is every open or merged pull request Sync placed on the
	// project board (brief.md#The board), previewed the same way in a dry
	// run.
	PlacedPRs []PlacedPR
	// GoneRecordedPRs is every recorded pull request nodes(ids:) answered
	// NOT_FOUND for (r9-f2): kept in the record and listed with no state,
	// since NOT_FOUND can mean the token lost access rather than that the
	// pull request is gone. One warning per entry, naming the ticket, the
	// repo and the number, so the operator can remove it by hand if it
	// really is gone.
	GoneRecordedPRs []GoneRecordedPR
}

// Sync runs one full sync of d.Cfg's trackers: github entry against d.Store:
// every ticket (in id order) and chart (in name order, after every ticket)
// with no tracker/github.yaml (or charts/<name>/github.yaml) record yet gets
// a new issue, titled and footed per brief.md#What an issue shows, claimed
// on the store's origin like an id (brief.md#Syncing at every checkpoint). A
// config with no github entry is a no-op, reported as such rather than
// refused, since every checkpoint of every store calls this whether or not
// it configured a tracker.
//
// Sync itself only adds the bound (and, on timeout, names how many tickets
// or charts still have no GitHub issue and points at `jig trackers sync`)
// around doSync, which does the actual work.
func Sync(d Deps, opts SyncOpts) (SyncReport, error) {
	report, items, err := doSync(d, opts)
	if err != nil && !opts.Unbounded && errors.Is(err, context.DeadlineExceeded) {
		timeout := syncTimeout
		if d.SyncTimeout > 0 {
			timeout = d.SyncTimeout
		}
		left := itemsWithNoRecord(items)
		if left > 0 {
			err = fmt.Errorf("mirror: sync ran out of its %s bound with %d ticket(s) or chart(s) still having no GitHub issue; run `jig trackers sync` to finish them: %w", timeout, left, err)
		} else {
			err = fmt.Errorf("mirror: sync ran out of its %s bound; run `jig trackers sync` to finish it: %w", timeout, err)
		}
	}
	return report, err
}

// itemsWithNoRecord counts items with no tracker/github.yaml (or
// charts/<name>/github.yaml) record yet: on a first sync of a sizeable
// store, these are exactly the items a timeout during issue creation left
// behind, since doSync creates every item's issue before touching updates,
// links or the board. A record-read failure counts as 0 rather than
// failing again on top of the timeout already being reported.
func itemsWithNoRecord(items []item) int {
	n := 0
	for _, it := range items {
		has, err := hasRecord(it.recordAbs)
		if err == nil && !has {
			n++
		}
	}
	return n
}

// doSync runs Sync's actual work; see Sync for the bound wrapped around it.
func doSync(d Deps, opts SyncOpts) (SyncReport, []item, error) {
	if d.Cfg.GitHub == nil {
		return SyncReport{NoTracker: true}, nil, nil
	}
	owner, repo, err := splitRepo(d.Cfg.GitHub.Repo)
	if err != nil {
		return SyncReport{}, nil, err
	}

	items, err := collectItems(d.Store)
	if err != nil {
		return SyncReport{}, nil, err
	}
	terms, err := LoadPublishTerms(d.Home)
	if err != nil {
		return SyncReport{}, items, fmt.Errorf("mirror: read publish-terms.txt: %w", err)
	}

	ctx := context.Background()
	if !opts.Unbounded {
		timeout := syncTimeout
		if d.SyncTimeout > 0 {
			timeout = d.SyncTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var report SyncReport
	var client github.Client
	var publicChecked, public bool
	ensureClient := func() error {
		if client != nil {
			return nil
		}
		client, err = d.client()
		return err
	}
	ensurePublic := func() error {
		if publicChecked {
			return nil
		}
		if err := ensureClient(); err != nil {
			return err
		}
		p, err := client.RepositoryIsPublic(ctx, owner, repo)
		if err != nil {
			return fmt.Errorf("mirror: check %s/%s's visibility: %w", owner, repo, err)
		}
		public, publicChecked = p, true
		return nil
	}

	target := "main"
	if len(d.Cfg.Repos) > 0 {
		target = d.Cfg.Repos[0].TargetBranch()
	}
	// newItemPRs returns it's own pull requests, for previewCreateRender to
	// render ahead of a create, so the create-time scan (dry run or live)
	// covers the same `## Pull requests` block updateIssues will later
	// render and scan (brief.md#Status and pull requests; r4-f1's narrower
	// seam: a term matching a pull request's repo must refuse the create
	// too, not just updateIssues' later skip). A chart has no pull
	// requests. When trackers: has no repos: entries, FindPullRequests
	// queries nothing, so no client is ensured either - keeping the dry
	// run with no new items' pull requests to find needing none (r1-f3's
	// guarantee for a store with no repos: configured).
	newItemPRs := func(it item) ([]PRRef, error) {
		if it.kind != "ticket" || len(d.Cfg.Repos) == 0 {
			return nil, nil
		}
		if err := ensureClient(); err != nil {
			return nil, err
		}
		branch, err := d.Store.TicketBranch(it.id, target)
		if err != nil {
			return nil, err
		}
		prs, _, err := FindPullRequests(ctx, client, d.Cfg, branch, nil)
		return prs, err
	}

	anyHasRecord := false
	for _, it := range items {
		has, err := hasRecord(it.recordAbs)
		if err != nil {
			return report, items, fmt.Errorf("mirror: check %s's record: %w", it.what(), err)
		}
		if has {
			rec, err := readRecord(it.recordAbs)
			if err != nil {
				return report, items, fmt.Errorf("mirror: read %s's record: %w", it.what(), err)
			}
			if !strings.EqualFold(rec.Repo, d.Cfg.GitHub.Repo) {
				return report, items, &axi.Error{
					Msg:  fmt.Sprintf("%s's record names %s, but trackers: github: repo: is now %s", it.what(), rec.Repo, d.Cfg.GitHub.Repo),
					Code: "MIRROR_REPO_CHANGED",
					Help: []string{"moving issues to a new repo is T-30"},
				}
			}
			anyHasRecord = true
			continue
		}
		if opts.DryRun {
			// Creating an issue for real writes only the marker as its body
			// (brief.md#Syncing at every checkpoint, step 3) and lets
			// updateIssues render the full title and body once the record
			// claimIssue wrote mid-sync lands it in the same run (step 4).
			// A dry run claims nothing, so an item with no record never
			// reaches updateIssues; it previews (and scans) the full render
			// here instead, so a store with no records yet still gets a
			// meaningful preview and publish-safety check (r1-f3). It
			// cannot learn the issue repo's visibility without a client it
			// otherwise has no need for yet, so it scans unconditionally,
			// previewing every hit it might otherwise miss. Its pull
			// requests are found the same way a live sync would (r4-f1),
			// since a dry run reads GitHub too (brief.md#jig trackers sync).
			prs, err := newItemPRs(it)
			if err != nil {
				return report, items, fmt.Errorf("mirror: find %s's pull requests: %w", it.what(), err)
			}
			title, body, err := previewCreateRender(d.Store, d.Cfg, it, prs)
			if err != nil {
				return report, items, fmt.Errorf("mirror: render %s: %w", it.what(), err)
			}
			if hits := ScanTitleAndBody(title, body, terms); len(hits) > 0 {
				report.Skipped = append(report.Skipped, SkippedItem{What: it.what(), Line: hits[0].Line, Text: hits[0].Text})
				continue
			}
			report.Created = append(report.Created, CreatedIssue{What: it.what(), Owner: owner, Repo: repo})
			continue
		}

		title, body, err := it.render(d.Store)
		if err != nil {
			return report, items, fmt.Errorf("mirror: render %s: %w", it.what(), err)
		}
		if err := ensurePublic(); err != nil {
			return report, items, err
		}
		if public {
			// Scan the full title and body previewCreateRender would render
			// (brief.md#Publish safety), not just the footer-only body
			// CreateIssue is about to write: a hit anywhere in the item's
			// real body must refuse the create, not just a hit in the
			// marker footer (r4-f1). The issue number does not exist yet
			// either way, and changes no text the scan matches, so scanning
			// before it exists (step 3, ahead of step 4's full render) is
			// sound. Its pull requests are included too (r4-f1's narrower
			// seam): a term matching a pull request's repo must refuse the
			// create, the same as updateIssues would later skip the update
			// on it.
			prs, err := newItemPRs(it)
			if err != nil {
				return report, items, fmt.Errorf("mirror: find %s's pull requests: %w", it.what(), err)
			}
			fullTitle, fullBody, err := previewCreateRender(d.Store, d.Cfg, it, prs)
			if err != nil {
				return report, items, fmt.Errorf("mirror: render %s: %w", it.what(), err)
			}
			if hits := ScanTitleAndBody(fullTitle, fullBody, terms); len(hits) > 0 {
				report.Skipped = append(report.Skipped, SkippedItem{What: it.what(), Line: hits[0].Line, Text: hits[0].Text})
				continue
			}
		}

		issue, err := client.CreateIssue(ctx, owner, repo, title, body)
		if err != nil {
			return report, items, fmt.Errorf("mirror: create issue for %s: %w", it.what(), err)
		}

		rec := githubRecord{Repo: d.Cfg.GitHub.Repo, Issue: issue.Number, NodeID: issue.NodeID}
		what := it.what()
		msg := fmt.Sprintf("%s: github issue %s/%s#%d", what, owner, repo, issue.Number)
		adopted, err := claimIssue(ctx, d.Store, client, it, issue, rec, msg)
		if err != nil {
			return report, items, fmt.Errorf("mirror: record %s's issue %s/%s#%d: %w", what, owner, repo, issue.Number, err)
		}

		number := issue.Number
		if adopted != 0 {
			number = adopted
		}
		report.Created = append(report.Created, CreatedIssue{What: what, Owner: owner, Repo: repo, Number: number})
	}

	if opts.DryRun && !anyHasRecord {
		// Nothing has a record to update, link or place, so a dry run that
		// only ever previewed creations needs no further client calls
		// (brief.md#jig trackers sync: "touching neither GitHub nor the
		// store"); newItemPRs above already ensured one if any new ticket
		// had pull requests to look up.
		return report, items, nil
	}
	if err := ensureClient(); err != nil {
		return report, items, err
	}
	if !opts.DryRun {
		if err := ensurePublic(); err != nil {
			return report, items, err
		}
	}
	curByWhat, statusByTicket, prsByTicket, skip, err := updateIssues(ctx, d.Store, d.Cfg, client, items, &report, terms, public, opts.DryRun)
	if err != nil {
		return report, items, fmt.Errorf("mirror: update issues: %w", err)
	}
	if err := linkParentsAndBlockers(ctx, d.Store, client, items, curByWhat, skip, &report, opts.DryRun); err != nil {
		return report, items, fmt.Errorf("mirror: link parents and blockers: %w", err)
	}
	if err := placeBoardItems(ctx, d.Store, d.Cfg, client, items, statusByTicket, prsByTicket, skip, &report, opts.DryRun); err != nil {
		return report, items, fmt.Errorf("mirror: place board items: %w", err)
	}
	return report, items, nil
}

// claimIssue lands issue's record on the store's origin like an id
// (Store.Claim): a commit of its own record alone, pushed at once. When the
// origin rejects that push, Claim undoes it, pulls and calls write again; if
// that pull brought in a record for the same ticket or chart, another
// clone's push won the race first, so issue is closed as not planned with a
// comment naming the other, and this clone drops its own record and adopts
// the other's instead of retrying (brief.md#Syncing at every checkpoint).
// Otherwise Claim's own retry (up to maxClaimAttempts, as L1 tries for an
// id) runs as it does for a plain id claim. It returns the other issue's
// number when it adopted one, 0 when issue's own record was claimed.
func claimIssue(ctx context.Context, st *store.Store, client github.Client, it item, issue github.Issue, rec githubRecord, msg string) (adopted int, err error) {
	what := it.what()
	closed := false
	_, err = st.Claim(
		func() (string, []string, error) {
			has, herr := hasRecord(it.recordAbs)
			if herr != nil {
				return "", nil, herr
			}
			if has {
				if !closed {
					other, rerr := readRecord(it.recordAbs)
					if rerr != nil {
						return "", nil, rerr
					}
					if cerr := closeDuplicateIssue(ctx, client, issue, other.Issue); cerr != nil {
						return "", nil, cerr
					}
					closed = true
					adopted = other.Issue
				}
				return what, nil, nil
			}
			if werr := writeRecord(it.recordAbs, rec); werr != nil {
				return "", nil, werr
			}
			return what, []string{it.recordRel}, nil
		},
		func(string) string { return msg },
	)
	return adopted, err
}

// closeDuplicateIssue closes issue as not planned and comments naming
// otherNumber, the issue a pull just revealed another clone pushed first for
// the same ticket or chart (brief.md#Syncing at every checkpoint).
func closeDuplicateIssue(ctx context.Context, client github.Client, issue github.Issue, otherNumber int) error {
	if err := client.CloseIssueNotPlanned(ctx, issue.NodeID); err != nil {
		return err
	}
	return client.AddComment(ctx, issue.NodeID, fmt.Sprintf("Duplicate of #%d", otherNumber))
}

// client returns d.Client, or the real GitHub client built against
// api.github.com with the token `gh auth token` prints, per #84 (no
// environment variable redirects either).
func (d Deps) client() (github.Client, error) {
	if d.Client != nil {
		return d.Client, nil
	}
	token, err := ghAuthToken()
	if err != nil {
		return nil, err
	}
	return github.New("https://api.github.com/graphql", token), nil
}

// splitRepo splits an "owner/name" repo spec, the shape project.yaml's
// trackers: github: repo: requires.
func splitRepo(spec string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(spec, "/")
	if !ok || owner == "" || name == "" {
		return "", "", fmt.Errorf("mirror: trackers: github: repo: %q is not owner/name", spec)
	}
	return owner, name, nil
}

// item is one ticket or chart Sync considers, with the record path it
// checks and, on creation, writes.
type item struct {
	kind      string // "ticket" or "chart"
	id        string // the ticket id, or the chart's name
	recordRel string // relative to the store root, for Store.Claim
	recordAbs string
}

// what is item's identity the way Sync's report, commit message and footer
// name it: the ticket id itself, or "charts/<name>" for a chart.
func (it item) what() string {
	if it.kind == "chart" {
		return "charts/" + it.id
	}
	return it.id
}

// render returns item's title and body for a brand-new issue.
func (it item) render(st *store.Store) (title, body string, err error) {
	if it.kind == "chart" {
		title, err = chartTitle(st, it.id)
		if err != nil {
			return "", "", err
		}
		return title, chartFooter(it.id), nil
	}
	title, err = ticketTitle(st, it.id)
	if err != nil {
		return "", "", err
	}
	return title, ticketFooter(it.id), nil
}

// previewCreateRender returns item's full title and body, as updateIssues
// would render them once the issue exists, for a dry run's preview and
// publish-safety scan of an item that has no record yet (r1-f3), and for the
// live create step's own scan ahead of CreateIssue (r4-f1). prs is it's own
// pull requests (newItemPRs), nil for a chart, which has none.
func previewCreateRender(st *store.Store, cfg project.Config, it item, prs []PRRef) (title, body string, err error) {
	if it.kind == "chart" {
		title, err = chartTitle(st, it.id)
		if err != nil {
			return "", "", err
		}
		body, err = RenderChartBody(st, cfg, it.id)
		return title, body, err
	}
	title, _, err = ResolveTicketTitleBody(st, it.id)
	if err != nil {
		return "", "", err
	}
	body, err = RenderTicketBody(st, cfg, it.id, prs)
	return title, body, err
}

// collectItems lists every ticket (key, then number order) then every chart
// (name order), the order brief.md#What an issue shows syncs them in.
func collectItems(st *store.Store) ([]item, error) {
	tickets, err := st.TicketIDs()
	if err != nil {
		return nil, err
	}
	charts, err := st.ChartNames()
	if err != nil {
		return nil, err
	}

	items := make([]item, 0, len(tickets)+len(charts))
	for _, id := range tickets {
		rel, abs := ticketRecordPath(st, id)
		items = append(items, item{kind: "ticket", id: id, recordRel: rel, recordAbs: abs})
	}
	for _, name := range charts {
		rel, abs := chartRecordPath(st, name)
		items = append(items, item{kind: "chart", id: name, recordRel: rel, recordAbs: abs})
	}
	return items, nil
}
