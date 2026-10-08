package mirror

import (
	"context"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

// LinkedParent is one ticket Sync made (or found already) a sub-issue of
// its chart's issue (brief.md#Links).
type LinkedParent struct {
	Ticket, Chart string
}

// LinkedBlockedBy is one blocked_by edge Sync turned into a native
// blocked-by link (brief.md#Links).
type LinkedBlockedBy struct {
	Ticket, Blocker string
}

// NotLinked is one blocked_by edge Sync could not link because its blocker
// has no issue yet (brief.md#Links: "the sync prints that it was not
// linked").
type NotLinked struct {
	Ticket, Blocker string
}

// linkTicket is what linkParentsAndBlockers needs of one ticket item: its
// id, record path and, once read, its record.
type linkTicket struct {
	id        string
	recordAbs string
	rec       githubRecord
	has       bool
}

// linkParentsAndBlockers wires brief.md#Links for every item that already
// has a record (an issue to link): each chart's tickets become sub-issues
// of the chart's issue, in the chart's own tickets.yaml order, and each
// ticket's blocked_by edges become native blocked-by links. A blocker with
// no issue yet is skipped and reported as not linked, rather than failing
// the sync. What each ticket ends up linked to is written back to its own
// record's synced.links (brief.md#Links: "the record keeps the node ids of
// the links it last wrote"), so a later sync only repeats a mutation whose
// link this one did not already write. curByWhat is the current GitHub state
// updateIssues already fetched for every item with a record, this step's own
// read of each chart's sub-issues: a sub-issue found there that is no ticket
// of the chart's is removed again, as drift (brief.md#Links: "a sub-issue
// added on GitHub to a chart's issue that is no ticket's issue is taken out
// again, as drift"). skip names every item updateIssues skipped on a
// publish-safety hit: it is left out of every write below, though it can
// still be linked against as another ticket's blocker (its existing issue
// is untouched, just not re-rendered this sync). dryRun previews every link
// (LinkedParents, LinkedBlockedBy, NotLinked, Drift) without calling
// AddSubIssue, RemoveSubIssue, AddBlockedBy, RemoveBlockedBy or writing a
// record (brief.md#jig trackers sync, brief.md#Cutover).
func linkParentsAndBlockers(ctx context.Context, st *store.Store, client github.Client, items []item, curByWhat map[string]github.IssueState, skip map[string]bool, report *SyncReport, dryRun bool) error {
	tickets := map[string]*linkTicket{}
	charts := map[string]githubRecord{}
	chartsHave := map[string]bool{}

	for _, it := range items {
		has, err := hasRecord(it.recordAbs)
		if err != nil {
			return err
		}
		if it.kind == "chart" {
			if !has {
				continue
			}
			rec, err := readRecord(it.recordAbs)
			if err != nil {
				return err
			}
			charts[it.id] = rec
			chartsHave[it.id] = true
			continue
		}
		lt := &linkTicket{id: it.id, recordAbs: it.recordAbs, has: has}
		if has {
			rec, err := readRecord(it.recordAbs)
			if err != nil {
				return err
			}
			lt.rec = rec
		}
		tickets[it.id] = lt
	}

	names, err := st.ChartNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		if !chartsHave[name] {
			continue
		}
		entries, err := st.ReadChart(name)
		if err != nil {
			return err
		}
		wanted := map[string]bool{}
		for _, entry := range entries {
			if entry.ID == "" {
				continue
			}
			lt, ok := tickets[entry.ID]
			if !ok || !lt.has {
				continue
			}
			wanted[lt.rec.NodeID] = true
			if skip[entry.ID] || skip["charts/"+name] {
				continue
			}
			cur, haveCur := curByWhat[lt.id]
			if err := linkParent(ctx, client, lt, charts[name], name, cur, haveCur, report, dryRun); err != nil {
				return err
			}
		}
		chartRec := charts[name]
		if !skip["charts/"+name] {
			if cur, ok := curByWhat["charts/"+name]; ok {
				for _, subID := range cur.SubIssueIDs {
					if wanted[subID] {
						continue
					}
					if !dryRun {
						if err := client.RemoveSubIssue(ctx, chartRec.NodeID, subID); err != nil {
							return err
						}
					}
					report.Drift = append(report.Drift, DriftLine{What: "charts/" + name, Issue: chartRec.Issue, Field: "sub-issue"})
				}
			}
		}
	}

	for _, it := range items {
		if it.kind != "ticket" {
			continue
		}
		lt := tickets[it.id]
		if !lt.has || skip[it.id] {
			continue
		}
		ticket, err := st.ReadTicket(it.id)
		if err != nil {
			return err
		}
		cur, haveCur := curByWhat[it.id]
		if err := linkBlockedBy(ctx, client, lt, ticket.BlockedBy, tickets, cur, haveCur, report, dryRun); err != nil {
			return err
		}
	}
	return nil
}

// linkParent makes lt's ticket a sub-issue of chartRec's issue, unless
// GitHub already shows it as one (cur.ParentID, when haveCur: either this
// sync's own earlier write, or - for a record adopted from the bridge, no
// mutation at all, the critical path brief.md#Seams names). A parent link
// jig wrote (lt.rec's own synced.links.parent) that GitHub no longer shows
// is restored and reported as drift (brief.md#Ownership and drift);
// otherwise it is a plain new link, reported in LinkedParents. dryRun
// previews the Drift/LinkedParents line without calling AddSubIssue or
// writing the record.
func linkParent(ctx context.Context, client github.Client, lt *linkTicket, chartRec githubRecord, chartName string, cur github.IssueState, haveCur bool, report *SyncReport, dryRun bool) error {
	links := currentLinks(lt.rec)
	wasSynced := links.Parent == chartRec.NodeID

	if haveCur && cur.ParentID == chartRec.NodeID {
		if !wasSynced && !dryRun {
			links.Parent = chartRec.NodeID
			if err := writeRecordLinks(lt.recordAbs, links); err != nil {
				return err
			}
			if lt.rec.Synced == nil {
				lt.rec.Synced = &syncedFields{}
			}
			lt.rec.Synced.Links = links
		}
		return nil
	}

	if !dryRun {
		if err := client.AddSubIssue(ctx, chartRec.NodeID, lt.rec.NodeID); err != nil {
			return err
		}

		links.Parent = chartRec.NodeID
		if err := writeRecordLinks(lt.recordAbs, links); err != nil {
			return err
		}
		if lt.rec.Synced == nil {
			lt.rec.Synced = &syncedFields{}
		}
		lt.rec.Synced.Links = links
	}
	if wasSynced {
		report.Drift = append(report.Drift, DriftLine{What: lt.id, Issue: lt.rec.Issue, Field: "parent"})
	} else {
		report.LinkedParents = append(report.LinkedParents, LinkedParent{Ticket: lt.id, Chart: chartName})
	}
	return nil
}

// linkBlockedBy brings lt's ticket's blocked-by edges to match blockedBy,
// in order: a blocker with a recorded issue not yet linked on GitHub gets
// AddBlockedBy (reported as drift when lt's own record already named it
// linked - brief.md#Ownership and drift - or as a plain new link in
// LinkedBlockedBy otherwise); a blocker with no recorded issue is skipped
// and reported in NotLinked; and, when haveCur, any blocked-by edge GitHub
// shows that blockedBy does not - added by hand, or a blocker the store no
// longer lists - is removed with RemoveBlockedBy and reported as drift,
// the same ownership rule links.go already applies to a chart's extra
// sub-issue. dryRun previews every report line without calling AddBlockedBy,
// RemoveBlockedBy or writing the record.
func linkBlockedBy(ctx context.Context, client github.Client, lt *linkTicket, blockedBy []store.TicketBlockedBy, tickets map[string]*linkTicket, cur github.IssueState, haveCur bool, report *SyncReport, dryRun bool) error {
	priorLinks := currentLinks(lt.rec)
	prevSynced := map[string]bool{}
	for _, nodeID := range priorLinks.BlockedBy {
		prevSynced[nodeID] = true
	}
	curSet := map[string]bool{}
	if haveCur {
		for _, nodeID := range cur.BlockedByIDs {
			curSet[nodeID] = true
		}
	}

	var newBlockedBy []string
	wanted := map[string]bool{}
	changed := false
	for _, b := range blockedBy {
		blocker, ok := tickets[b.Ticket]
		if !ok || !blocker.has {
			report.NotLinked = append(report.NotLinked, NotLinked{Ticket: lt.id, Blocker: b.Ticket})
			continue
		}
		nodeID := blocker.rec.NodeID
		wanted[nodeID] = true
		if haveCur && curSet[nodeID] {
			newBlockedBy = append(newBlockedBy, nodeID)
			continue
		}

		if !dryRun {
			if err := client.AddBlockedBy(ctx, lt.rec.NodeID, nodeID); err != nil {
				return err
			}
		}
		newBlockedBy = append(newBlockedBy, nodeID)
		changed = true
		if prevSynced[nodeID] {
			report.Drift = append(report.Drift, DriftLine{What: lt.id, Issue: lt.rec.Issue, Field: "blocked_by"})
		} else {
			report.LinkedBlockedBy = append(report.LinkedBlockedBy, LinkedBlockedBy{Ticket: lt.id, Blocker: b.Ticket})
		}
	}

	if haveCur {
		for _, nodeID := range cur.BlockedByIDs {
			if wanted[nodeID] {
				continue
			}
			if !dryRun {
				if err := client.RemoveBlockedBy(ctx, lt.rec.NodeID, nodeID); err != nil {
					return err
				}
			}
			report.Drift = append(report.Drift, DriftLine{What: lt.id, Issue: lt.rec.Issue, Field: "blocked_by"})
			changed = true
		}
	}

	if !changed || dryRun {
		return nil
	}
	links := priorLinks
	links.BlockedBy = newBlockedBy
	if err := writeRecordLinks(lt.recordAbs, links); err != nil {
		return err
	}
	if lt.rec.Synced == nil {
		lt.rec.Synced = &syncedFields{}
	}
	lt.rec.Synced.Links = links
	return nil
}

// currentLinks returns rec's own synced.links, or a zero one when rec has
// none recorded yet.
func currentLinks(rec githubRecord) syncedLinks {
	if rec.Synced == nil {
		return syncedLinks{}
	}
	return rec.Synced.Links
}
