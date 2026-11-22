package mirror

import (
	"context"
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

// BindOpts is what BindItem needs beyond the store, the project and the
// client (Deps).
type BindOpts struct {
	// Ref is the item to bind.
	Ref ItemRef
	// Chart is the new chart's name, and Heading its title ("" takes the
	// item's title, trimmed to one line).
	Chart   string
	Heading string
	// Key is the key to mint the issue under ("" is the project's only key).
	Key string
	// ChartTaken refuses a chart name that cannot be used: the caller's
	// own rule, run again inside the claim on the tree as it is then.
	ChartTaken func() error
}

// BindResult is what a bind made.
type BindResult struct {
	// Issue is the id of the issue ticket minted from the item.
	Issue string
	Chart string
	// Heading is the chart's title.
	Heading string
	Item    github.ItemDetail
	// Advisories are shown, never refused: the item is closed, or has linked
	// pull requests.
	Advisories []string
}

// BindItem makes an issue ticket and its chart from an item that exists on
// the tracker, and binds the item to the issue as the issue's own record, in
// one claim. The item is read once, outside the claim's retried write. After
// the bind the store owns the item's text: the mirror stays one-way and the
// first sync updates the item in place, so no second issue is opened.
//
// The write of the claim, on each attempt, reloads the tracker tree and
// refuses an entry another ticket or chart holds before it mints, then mints
// the issue with the item's title and body, reserves the entry in writers
// mode, makes the chart, and writes the record and its link. It returns every
// path it touched as it touches it, so a failure at any step leaves no
// ticket folder, chart folder or record behind.
func BindItem(ctx context.Context, d Deps, o BindOpts) (BindResult, error) {
	if err := CheckItemRepo(o.Ref, d.Cfg); err != nil {
		return BindResult{}, err
	}
	st := d.Store
	repo := d.Cfg.GitHub.Repo
	owner, name, err := splitRepo(repo)
	if err != nil {
		return BindResult{}, err
	}
	key, err := d.Cfg.ResolveKey(o.Key)
	if err != nil {
		return BindResult{}, err
	}
	entry := githubEntry(repo, o.Ref.Number)
	// A second bind is refused here, offline and before the read.
	st.ReloadTrackerTree()
	if err := checkEntryUnbound(st, entry, o.Ref.Number); err != nil {
		return BindResult{}, err
	}
	client, err := d.client()
	if err != nil {
		return BindResult{}, err
	}
	detail, err := client.ItemByNumber(ctx, owner, name, o.Ref.Number)
	if err != nil {
		return BindResult{}, err
	}

	heading := strings.TrimSpace(o.Heading)
	if heading == "" {
		heading = oneLine(detail.Title)
	}
	res := BindResult{Chart: o.Chart, Heading: heading, Item: detail}
	rec := githubRecord{Repo: repo, Issue: detail.Number, NodeID: detail.NodeID}

	_, err = st.Claim(
		func() (string, []string, error) {
			// The tree as it is now: the pull after a rejected push can bring
			// in another clone's bind of the same item.
			st.ReloadTrackerTree()
			if err := checkEntryUnbound(st, entry, o.Ref.Number); err != nil {
				return "", nil, err
			}
			if o.ChartTaken != nil {
				if err := o.ChartTaken(); err != nil {
					return "", nil, err
				}
			}
			id, err := st.Mint(key, store.Ticket{Title: detail.Title, Body: detail.Body, Slug: store.Slug(detail.Title), Kind: store.KindIssue})
			if err != nil {
				return "", nil, err
			}
			res.Issue = id
			paths := []string{st.TicketRelDir(id)}
			if err := st.ReserveEntry(entry, id); err != nil {
				return "", paths, err
			}
			if err := st.CreateChartWithIssue(o.Chart, heading, id); err != nil {
				// A refused or failed chart write removes what it made itself, and
				// a folder that was there before is not this claim's to undo.
				return "", paths, err
			}
			paths = append(paths, st.ChartRelDir(o.Chart))
			bound := item{kind: "ticket", id: id, ref: &recordRef{}}
			// The entry's folder holds the record and the link: reporting it
			// undoes both with a failure, and the folder with them.
			paths = append(paths, st.TrackerRelPath(entry))
			if err := writeRecord(st, bound, rec); err != nil {
				return "", paths, err
			}
			if _, err := st.LinkTrackerEntry(entry, bound.owner()); err != nil {
				return "", paths, err
			}
			return id, paths, nil
		},
		func(string) string {
			return fmt.Sprintf("chart %s: new chart (issue %s binds #%d)", o.Chart, res.Issue, detail.Number)
		},
	)
	if err != nil {
		return BindResult{}, err
	}

	if !detail.Open {
		res.Advisories = append(res.Advisories, fmt.Sprintf("%d is closed; the first sync reopens it", detail.Number))
	}
	if detail.LinkedPullRequests > 0 {
		res.Advisories = append(res.Advisories, fmt.Sprintf("%d has %d linked pull request(s); the issue does not take them over", detail.Number, detail.LinkedPullRequests))
	}
	return res, nil
}

// checkEntryUnbound refuses TRACKER_ENTRY_TAKEN for an entry whose record the
// tree already holds, naming its owner: the issue and its chart when the owner
// is an issue a chart plans.
func checkEntryUnbound(st *store.Store, entry store.TrackerEntry, number int) error {
	rec, ok, err := st.TrackerRecordAt(entry)
	if err != nil || !ok {
		return err
	}
	who := rec.Ticket
	switch {
	case rec.Chart != "":
		who = "chart " + rec.Chart
	case rec.Ticket != "":
		names, cerr := st.ChartNames()
		if cerr != nil {
			return cerr
		}
		for _, n := range names {
			if got, ierr := st.ChartIssue(n); ierr == nil && got == rec.Ticket {
				who = fmt.Sprintf("%s (chart %s)", rec.Ticket, n)
				break
			}
		}
	default:
		who = rec.Rel
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("%d is already bound to %s", number, who),
		Code: "TRACKER_ENTRY_TAKEN",
		Help: []string{fmt.Sprintf("Work on %s instead: an item is bound to one issue", who)},
	}
}
