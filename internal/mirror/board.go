package mirror

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// PlacedItem is one issue (ticket or chart) Sync placed on the project
// board, with the Status it set (brief.md#The board).
type PlacedItem struct {
	What, Status string
}

// PlacedPR is one pull request Sync placed on the project board
// (brief.md#The board: "each open or merged pull request... is an item").
type PlacedPR struct {
	Ticket      string
	Owner, Repo string
	Number      int
	Status      string
}

// projectURLRE matches a trackers: github: project: URL
// (brief.md#The trackers entry): "users/<login>" or "orgs/<org>", which says
// what kind of owner it has.
var projectURLRE = regexp.MustCompile(`^https://github\.com/(users|orgs)/([^/]+)/projects/(\d+)$`)

// parseProjectURL splits raw into the owner login, whether that owner is an
// org, and the project's number.
func parseProjectURL(raw string) (ownerLogin string, isOrg bool, number int, err error) {
	m := projectURLRE.FindStringSubmatch(raw)
	if m == nil {
		return "", false, 0, fmt.Errorf("mirror: trackers: github: project: %q is not a GitHub Project URL (users/<login>/projects/<n> or orgs/<org>/projects/<n>)", raw)
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return "", false, 0, fmt.Errorf("mirror: trackers: github: project: %q: %w", raw, err)
	}
	return m[2], m[1] == "orgs", n, nil
}

// placeBoardItems is brief.md#The board's own work: it resolves the
// project, creating any missing field or option and linking the issue repo
// when it is not linked, then places every item that already has a record
// (every ticket and chart Sync just created or found, in items' own order)
// with its Status and Store ID (statusByTicket and chartTicketStatuses,
// which updateIssues already derived), and each of a ticket's open or merged
// pull requests (prsByTicket, which updateIssues already found) alongside
// it. An item already on the board (rec.Item set from an earlier sync) has
// its current Status and Store ID read back first, so an edit made on
// GitHub reports as drift before jig overwrites it again
// (brief.md#Ownership and drift); when they already match what this sync
// would write, PlaceItem is skipped entirely - brief.md#Seams's critical
// path, "a store whose records match GitHub syncs with no mutation" -
// rather than re-sent on every checkpoint regardless. skip names every item
// updateIssues took out of this sync on a publish-safety hit: it gets no
// board write either. dryRun previews Placed/PlacedPR and the drift this
// step would find, without calling EnsureProject, PlaceItem or writing a
// record: it resolves the project read-only (LookupProject), which cannot
// create a missing field or link the issue repo the way EnsureProject does,
// so a project that does not yet exist refuses the same way a live sync
// would (brief.md#jig trackers sync, brief.md#Cutover).
func placeBoardItems(ctx context.Context, st *store.Store, cfg project.Config, client github.Client, items []item, statusByTicket map[string]string, prsByTicket map[string][]PRRef, skip map[string]bool, report *SyncReport, dryRun bool) error {
	ownerLogin, isOrg, number, err := parseProjectURL(cfg.GitHub.Project)
	if err != nil {
		return err
	}
	repoOwner, repoName, err := splitRepo(cfg.GitHub.Repo)
	if err != nil {
		return err
	}

	var proj github.ProjectV2
	if dryRun {
		found, ferr := client.LookupProject(ctx, ownerLogin, isOrg, number)
		if ferr != nil {
			return ferr
		}
		if !found {
			return &axi.Error{
				Msg:  fmt.Sprintf("github project for %s (number %d) was not found, or this token lacks the project scope", ownerLogin, number),
				Code: "MIRROR_PROJECT_NOT_FOUND",
				Help: []string{"gh auth refresh -s project"},
			}
		}
	} else {
		proj, err = client.EnsureProject(ctx, ownerLogin, isOrg, number, repoOwner, repoName)
		if err != nil {
			return err
		}
	}

	for _, it := range items {
		has, err := hasRecord(it.recordAbs)
		if err != nil {
			return err
		}
		if !has || skip[it.what()] {
			continue
		}
		rec, err := readRecord(it.recordAbs)
		if err != nil {
			return err
		}

		var status string
		if it.kind == "ticket" {
			status = statusByTicket[it.id]
			if err := placeTicketPRs(ctx, client, proj, it.id, prsByTicket[it.id], &rec, report, dryRun); err != nil {
				return err
			}
		} else {
			statuses, err := chartTicketStatuses(st, it.id, statusByTicket)
			if err != nil {
				return err
			}
			status = ChartStatus(statuses)
		}

		alreadyPlaced := false
		if rec.Item != "" {
			curStatus, curStoreID, ferr := client.ItemFieldValues(ctx, rec.Item)
			if ferr != nil {
				return fmt.Errorf("read %s's board item: %w", it.what(), ferr)
			}
			synced := rec.Synced
			if synced != nil {
				if synced.Status != "" && curStatus != synced.Status {
					report.Drift = append(report.Drift, DriftLine{What: it.what(), Issue: rec.Issue, Field: "status"})
				}
				if synced.StoreID != "" && curStoreID != synced.StoreID {
					report.Drift = append(report.Drift, DriftLine{What: it.what(), Issue: rec.Issue, Field: "store_id"})
				}
			}
			alreadyPlaced = curStatus == status && curStoreID == it.what()
		}

		if !alreadyPlaced {
			if !dryRun {
				itemID, err := client.PlaceItem(ctx, proj, rec.NodeID, status, it.what())
				if err != nil {
					return fmt.Errorf("place %s on the board: %w", it.what(), err)
				}
				rec.Item = itemID
			}
			report.Placed = append(report.Placed, PlacedItem{What: it.what(), Status: status})
		}
		if !dryRun {
			if rec.Synced == nil {
				rec.Synced = &syncedFields{}
			}
			rec.Synced.Status = status
			rec.Synced.StoreID = it.what()
			if err := writeRecord(it.recordAbs, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

// placeTicketPRs places each of ticket's own prs that is open or merged on
// the board under its Store ID, writing them into rec's prs:
// (brief.md#Status and pull requests, brief.md#The board: "a closed one is
// listed but not placed"). A pull request already placed with this same
// Status and Store ID (rec's own prior record for it) is left alone, the
// same no-mutation-when-matching rule placeBoardItems applies to a ticket's
// or chart's own item. dryRun previews PlacedPRs without calling PlaceItem
// or writing rec.PRs.
func placeTicketPRs(ctx context.Context, client github.Client, proj github.ProjectV2, ticket string, prs []PRRef, rec *githubRecord, report *SyncReport, dryRun bool) error {
	existing := make(map[string]prRecord, len(rec.PRs))
	for _, p := range rec.PRs {
		existing[p.NodeID] = p
	}

	var prRecs []prRecord
	for _, pr := range prs {
		var prStatus string
		placeable := true
		switch {
		case strings.EqualFold(pr.State, "MERGED"):
			prStatus = "Done"
		case strings.EqualFold(pr.State, "OPEN"):
			prStatus = "In review"
		case pr.State == "":
			// A recorded entry FindPullRequests could not re-check by id
			// (r9-f1/r9-f2: no node_id, or GitHub answered NOT_FOUND) carries
			// no State at all. It is listed in the body but never placed,
			// and is kept in the record below rather than dropped: a sync
			// must never delete store data it cannot check.
			placeable = false
		default:
			continue // closed: listed in the body, dropped from the record
		}

		itemID := ""
		if ex, ok := existing[pr.NodeID]; ok {
			itemID = ex.Item
		}
		if placeable {
			alreadyPlaced := false
			if itemID != "" {
				curStatus, curStoreID, err := client.ItemFieldValues(ctx, itemID)
				if err != nil {
					return fmt.Errorf("read %s/%s#%d's board item: %w", pr.Owner, pr.Repo, pr.Number, err)
				}
				alreadyPlaced = curStatus == prStatus && curStoreID == ticket
			}
			if !alreadyPlaced {
				if !dryRun {
					newItemID, err := client.PlaceItem(ctx, proj, pr.NodeID, prStatus, ticket)
					if err != nil {
						return fmt.Errorf("place %s/%s#%d on the board: %w", pr.Owner, pr.Repo, pr.Number, err)
					}
					itemID = newItemID
				}
				report.PlacedPRs = append(report.PlacedPRs, PlacedPR{Ticket: ticket, Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number, Status: prStatus})
			}
		}
		prRecs = append(prRecs, prRecord{Repo: pr.Owner + "/" + pr.Repo, Number: pr.Number, NodeID: pr.NodeID, Item: itemID})
	}
	if !dryRun {
		rec.PRs = prRecs
	}
	return nil
}

// chartTicketStatuses returns the Status already computed (in ticketStatus)
// for each of chart's own tickets, in its tickets.yaml order, the input
// ChartStatus derives a chart's own Status from.
func chartTicketStatuses(st *store.Store, chart string, ticketStatus map[string]string) ([]string, error) {
	entries, err := st.ReadChart(chart)
	if err != nil {
		return nil, err
	}
	var statuses []string
	for _, e := range entries {
		if s, ok := ticketStatus[e.ID]; ok {
			statuses = append(statuses, s)
		}
	}
	return statuses, nil
}
