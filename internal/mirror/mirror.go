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
	"path/filepath"
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
	// MovedRecords is every record this sync moved from a ticket's or chart's
	// own folder into the tracker tree. Empty in a dry run, which moves
	// nothing.
	MovedRecords []MovedRecord
}

// MovedRecord is one record Sync moved into the tracker tree.
type MovedRecord struct {
	// What is the ticket id, or "charts/<name>" for a chart.
	What string
	// From is the path the record left, and To the one it is at now, both
	// relative to the store root and with slashes.
	From, To string
}

// Sync runs one full sync of d.Cfg's trackers: github entry against d.Store:
// every ticket (in id order) and chart (in name order, after every ticket)
// with no record in the tracker tree yet gets a new issue, titled and footed
// per brief.md#What an issue shows, claimed on the store's origin like an id
// (brief.md#Syncing at every checkpoint). A record still in a ticket's or
// chart's own folder is moved into the tree first, and the issue is updated in
// place, never created again; and every sync that is not a dry run makes the
// tree's links again from its records once it has written them. A config with
// no github entry is a no-op as far as GitHub goes, reported as such rather
// than refused, since every checkpoint of every store calls this whether or
// not it configured a tracker; it still makes the links, which need no
// provider.
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

// itemsWithNoRecord counts items with no record yet: on a first sync of a
// sizeable store, these are exactly the items a timeout during issue creation
// left behind, since doSync creates every item's issue before touching
// updates, links or the board.
func itemsWithNoRecord(items []item) int {
	n := 0
	for _, it := range items {
		if !it.ref.has() {
			n++
		}
	}
	return n
}

// doSync runs Sync's actual work; see Sync for the bound wrapped around it.
func doSync(d Deps, opts SyncOpts) (SyncReport, []item, error) {
	// The tree as it is on disk now: another process may have changed it
	// since this Store last read it.
	d.Store.ReloadTrackerTree()
	if d.Cfg.GitHub == nil {
		if !opts.DryRun {
			if _, err := d.Store.RebuildTrackerLinks(); err != nil {
				return SyncReport{NoTracker: true}, nil, fmt.Errorf("mirror: rebuild the tracker tree's links: %w", err)
			}
		}
		return SyncReport{NoTracker: true}, nil, nil
	}
	owner, repo, err := splitRepo(d.Cfg.GitHub.Repo)
	if err != nil {
		return SyncReport{}, nil, err
	}

	if err := refuseUnreadableRecords(d.Store); err != nil {
		return SyncReport{}, nil, err
	}
	items, err := collectItems(d.Store, d.Cfg.GitHub.Repo)
	if err != nil {
		return SyncReport{}, nil, err
	}
	terms, err := LoadPublishTerms(d.Home)
	if err != nil {
		return SyncReport{}, items, fmt.Errorf("mirror: read publish-terms.txt: %w", err)
	}
	var report SyncReport
	if !opts.DryRun {
		report.MovedRecords, err = moveLegacyRecords(d.Store, items, d.Cfg.GitHub.Repo)
		if err != nil {
			return report, items, err
		}
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
		if it.ref.has() {
			rec, err := readRecord(d.Store, it)
			if err != nil {
				return report, items, fmt.Errorf("mirror: read %s's record: %w", it.what(), err)
			}
			if !strings.EqualFold(rec.Repo, d.Cfg.GitHub.Repo) {
				return report, items, repoChangedError(it, rec.Repo, d.Cfg.GitHub.Repo)
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
		adopted, err := claimIssue(ctx, d.Store, client, it, d.Cfg.GitHub.Repo, issue, rec, msg)
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
	if !opts.DryRun {
		if _, err := d.Store.RebuildTrackerLinks(); err != nil {
			return report, items, fmt.Errorf("mirror: rebuild the tracker tree's links: %w", err)
		}
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
func claimIssue(ctx context.Context, st *store.Store, client github.Client, it item, repo string, issue github.Issue, rec githubRecord, msg string) (adopted int, err error) {
	what := it.what()
	closed := false
	_, err = st.Claim(
		func() (string, []string, error) {
			// The tree as it is now, not as this clone last read it: the pull
			// that follows a rejected push brings in another clone's record,
			// and a record another process wrote since counts as well.
			st.ReloadTrackerTree()
			other, ferr := findRecord(st, it.owner(), repo)
			if ferr != nil {
				return "", nil, ferr
			}
			if other.has() {
				if !closed {
					theirs, rerr := readRecord(st, item{kind: it.kind, id: it.id, ref: other})
					if rerr != nil {
						return "", nil, rerr
					}
					if cerr := closeDuplicateIssue(ctx, client, issue, theirs.Issue); cerr != nil {
						return "", nil, cerr
					}
					closed = true
					adopted = theirs.Issue
				}
				*it.ref = *other
				return what, nil, nil
			}
			// A first attempt's record is gone once its push was undone.
			*it.ref = recordRef{}
			if werr := writeRecord(st, it, rec); werr != nil {
				return "", nil, werr
			}
			paths := []string{st.TrackerRecordRelPath(it.ref.entry)}
			// The link goes in the same commit as its record, so the committed
			// tree is whole at every step.
			made, lerr := st.LinkTrackerEntry(it.ref.entry, it.owner())
			if lerr != nil {
				return "", nil, lerr
			}
			if made == store.LinkMade || made == store.LinkReplaced {
				paths = append(paths, st.TrackerRelPath(it.ref.entry))
			}
			return what, paths, nil
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

// item is one ticket or chart Sync considers, with where its record sits.
type item struct {
	kind string // "ticket" or "chart"
	id   string // the ticket id, or the chart's name
	ref  *recordRef
}

// owner is the ticket or chart the item's record names.
func (it item) owner() store.TrackerOwner {
	if it.kind == "chart" {
		return store.TrackerOwner{Chart: it.id}
	}
	return store.TrackerOwner{Ticket: it.id}
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
// (name order), the order brief.md#What an issue shows syncs them in, each
// with where its record is (findRecord, for the repository repo).
func collectItems(st *store.Store, repo string) ([]item, error) {
	tickets, err := st.TicketIDs()
	if err != nil {
		return nil, err
	}
	charts, err := st.ChartNames()
	if err != nil {
		return nil, err
	}

	items := make([]item, 0, len(tickets)+len(charts))
	for _, o := range append(ticketOwners(tickets), chartOwners(charts)...) {
		it := item{kind: "ticket", id: o.Ticket}
		if o.Chart != "" {
			it = item{kind: "chart", id: o.Chart}
		}
		ref, err := findRecord(st, o, repo)
		if err != nil {
			return nil, err
		}
		it.ref = ref
		items = append(items, it)
	}
	return items, nil
}

// ticketOwners lists ids as the owners of ticket records.
func ticketOwners(ids []string) []store.TrackerOwner {
	out := make([]store.TrackerOwner, len(ids))
	for i, id := range ids {
		out[i] = store.TrackerOwner{Ticket: id}
	}
	return out
}

// chartOwners lists names as the owners of chart records.
func chartOwners(names []string) []store.TrackerOwner {
	out := make([]store.TrackerOwner, len(names))
	for i, name := range names {
		out[i] = store.TrackerOwner{Chart: name}
	}
	return out
}

// refuseUnreadableRecords refuses a sync while a record of the tracker tree
// cannot be read: it names no ticket or chart that can be told, so the sync
// would take its ticket for one with no issue and open a second.
func refuseUnreadableRecords(st *store.Store) error {
	recs, err := st.TrackerRecords()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.Err == nil {
			continue
		}
		rel := filepath.ToSlash(st.TrackerRecordRelPath(rec.Entry))
		return &axi.Error{
			Msg:  fmt.Sprintf("%s could not be read: %v", rel, rec.Err),
			Code: "MIRROR_RECORD_INVALID",
			Help: []string{fmt.Sprintf("Restore %s from the store's history, or remove it and let the sync adopt the issue again", rel)},
		}
	}
	return nil
}

// repoChangedError refuses a sync whose item has a record for another
// repository than the one trackers: github: repo: names now.
func repoChangedError(it item, recorded, configured string) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("%s's record names %s, but trackers: github: repo: is now %s", it.what(), recorded, configured),
		Code: "MIRROR_REPO_CHANGED",
		Help: []string{"moving issues to a new repo is T-30"},
	}
}

// moveLegacyRecords moves every record still in its ticket's or chart's own
// folder into the tracker tree, adding the key that names its owner, and
// returns what it moved. It is the first thing a sync does, before any record
// is read for its issue, so the issue is found by its record and updated in
// place, never created again. The moves are working-tree changes, committed
// with the next checkpoint like every record change but a creation. A record
// for another repository than repo is refused before anything moves, as the
// sync would refuse it later.
func moveLegacyRecords(st *store.Store, items []item, repo string) ([]MovedRecord, error) {
	type move struct {
		it    item
		entry store.TrackerEntry
	}
	var moves []move
	for _, it := range items {
		if it.ref.legacy == nil {
			continue
		}
		rec, err := readRecord(st, it)
		if err != nil {
			return nil, fmt.Errorf("mirror: read %s's record: %w", it.what(), err)
		}
		if !strings.EqualFold(rec.Repo, repo) {
			return nil, repoChangedError(it, rec.Repo, repo)
		}
		if rec.Issue < 1 {
			return nil, &axi.Error{
				Msg:  fmt.Sprintf("%s's record in %s names no issue", it.what(), filepath.ToSlash(it.ref.legacy.Rel)),
				Code: "MIRROR_RECORD_INVALID",
				Help: []string{fmt.Sprintf("Restore it from the store's history, or remove %s and let the sync adopt the issue again", filepath.ToSlash(it.ref.legacy.Rel))},
			}
		}
		moves = append(moves, move{it, githubEntry(rec.Repo, rec.Issue)})
	}
	var moved []MovedRecord
	for _, m := range moves {
		if err := st.MoveLegacyTrackerRecord(*m.it.ref.legacy, m.entry, recordHeader); err != nil {
			return moved, fmt.Errorf("mirror: move %s's record into the tracker tree: %w", m.it.what(), err)
		}
		moved = append(moved, MovedRecord{What: m.it.what(), From: filepath.ToSlash(m.it.ref.legacy.Rel), To: filepath.ToSlash(st.TrackerRecordRelPath(m.entry))})
		m.it.ref.entry, m.it.ref.inTree, m.it.ref.legacy = m.entry, true, nil
	}
	return moved, nil
}
