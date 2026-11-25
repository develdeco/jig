package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// DriftLine is one jig-owned field this sync found edited on GitHub: GitHub's
// current value differed from what the last sync itself wrote,
// so jig overwrote it with the store's value
// again.
type DriftLine struct {
	// What is the ticket id, or "charts/<name>" for a chart.
	What string
	// Issue is the issue's number.
	Issue int
	// Field is the jig-owned field that drifted: "title", "body", "state",
	// "sub-issue", "status" or "store_id".
	Field string
}

// UpdatedIssue is one issue Sync brought up to date: its title, body,
// open/closed state or close reason changed to match the store.
type UpdatedIssue struct {
	What   string
	Fields []string
}

// normalizeBody puts s in the form issue bodies compare and hash in:
// line endings become \n and outer whitespace is trimmed.
func normalizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

// bodyHash is the sha256 (hex) of s's normalized form, what synced.body_sha256
// keeps.
func bodyHash(s string) string {
	sum := sha256.Sum256([]byte(normalizeBody(s)))
	return hex.EncodeToString(sum[:])
}

// openState renders open the way synced.state spells it.
func openState(open bool) string {
	if open {
		return "OPEN"
	}
	return "CLOSED"
}

// closeReason is the reason an issue held closed with status takes.
func closeReason(status string) string {
	if status == StatusNotPlanned {
		return github.CloseReasonNotPlanned
	}
	return github.CloseReasonCompleted
}

// contradictsReason reports whether reason, GitHub's spelling of why a closed
// issue was closed, says otherwise than status. An issue closed with no reason
// reads as completed on GitHub, and one closed as a duplicate as not planned,
// so those two stand where they agree.
func contradictsReason(status, reason string) bool {
	if status == StatusNotPlanned {
		return reason == "COMPLETED" || reason == ""
	}
	return reason == "NOT_PLANNED" || reason == "DUPLICATE"
}

// updateIssues is the update step of a sync: for every
// ticket and chart that already has a record (including one this same Sync
// just created, with only its title and footer as a body), it renders the
// full title and body - now that every ticket has an issue number any
// citation of it can resolve - derives its Status, and brings GitHub's
// title, body and open/closed state to match, reporting drift when GitHub's
// current value differs from what the last sync itself wrote.
// An issue gone from GitHub is recreated rather than failing the sync.
//
// Before any write, it scans the rendered title and body when public is true
// or dryRun is true (every title and body is
// scanned before it is written, on every sync, not only the one that creates
// the issue): a hit is reported in report.Skipped and that item's what() is
// added to the returned skip set, so GitHub keeps what it had and
// linkParentsAndBlockers/placeBoardItems make no write for it either.
//
// dryRun withholds every mutation (UpdateIssue, CreateIssue on a recreation,
// ReopenIssue, CloseIssueCompleted, CloseIssueNotPlanned, SetCloseReason, the
// record write) while still reading GitHub (FetchIssues, PullRequestsByHeads)
// to report what would change.
//
// An issue whose status is Done is closed as completed and one whose status is
// Not planned is closed as not planned (a ticket closed as covered, superseded
// or not planned, `jig close`); one that is closed already stays closed, but a
// close reason that says otherwise is set to the store's, and a ticket reopened
// gets its issue back open. This step makes every write of an issue's state, so
// it is done before the board step moves any card: a card never reads Done or
// Not planned for an issue this sync does not hold closed.
//
// It returns, for later steps to reuse rather than querying GitHub again:
// curByWhat (each item's current GitHub state, fetched before this step's
// own writes), cardByWhat (the Status each item's card shows, charts
// included), prsByTicket (every ticket's own pull requests, the board step
// places) and skip (every item a publish-safety hit took out of this sync).
func updateIssues(ctx context.Context, st *store.Store, cfg project.Config, client github.Client, items []item, report *SyncReport, terms []string, public, dryRun bool) (curByWhat map[string]github.IssueState, cardByWhat map[string]string, prsByTicket map[string][]PRRef, skip map[string]bool, err error) {
	curByWhat = map[string]github.IssueState{}
	cardByWhat = map[string]string{}
	prsByTicket = map[string][]PRRef{}
	skip = map[string]bool{}

	target := "main"
	if len(cfg.Repos) > 0 {
		target = cfg.Repos[0].TargetBranch()
	}

	// Every record is read first, so what the loop below compares against
	// - each ticket's pull requests and each issue's current state - comes
	// from a few batched reads rather than a round trip per item, which is
	// what outgrew the sync's bound once a store held a few dozen tickets.
	type pending struct {
		it  item
		rec githubRecord
		// lookup is this ticket's index in lookups; -1 for a chart.
		lookup int
		// issue is a ticket of kind issue, whose card follows its chart's
		// tickets while it is unfinished (issueCard).
		issue bool
	}
	var todo []pending
	var lookups []prLookup
	var nodeIDs []string
	for _, it := range items {
		if !it.ref.has() {
			continue
		}
		rec, rerr := readRecord(st, it)
		if rerr != nil {
			return nil, nil, nil, nil, rerr
		}
		p := pending{it: it, rec: rec, lookup: -1}
		if it.kind == "ticket" {
			tk, terr := st.ReadTicket(it.id)
			if terr != nil {
				return nil, nil, nil, nil, terr
			}
			p.issue = tk.KindOrDefault() == store.KindIssue
			branch, berr := st.TicketBranch(it.id, target)
			if berr != nil {
				return nil, nil, nil, nil, berr
			}
			recorded := make([]PRRef, len(rec.PRs))
			for i, pr := range rec.PRs {
				owner, repo, _ := splitRepo(pr.Repo)
				recorded[i] = PRRef{Owner: owner, Repo: repo, Number: pr.Number, NodeID: pr.NodeID}
			}
			p.lookup = len(lookups)
			lookups = append(lookups, prLookup{branch: branch, recorded: recorded})
		}
		nodeIDs = append(nodeIDs, rec.NodeID)
		todo = append(todo, p)
	}
	found, ferr := findPullRequests(ctx, client, cfg, lookups)
	if ferr != nil {
		return nil, nil, nil, nil, fmt.Errorf("mirror: find the tickets' pull requests: %w", ferr)
	}
	curByNodeID, ferr := client.FetchIssues(ctx, nodeIDs)
	if ferr != nil {
		return nil, nil, nil, nil, fmt.Errorf("mirror: fetch the issues: %w", ferr)
	}
	// Every ticket's own Status comes first, since a chart's and an issue's
	// card read the Statuses of the tickets a chart lists.
	statusByTicket := map[string]string{}
	for _, p := range todo {
		if p.it.kind != "ticket" {
			continue
		}
		statusByTicket[p.it.id], err = TicketStatus(st, p.it.id, found[p.lookup].prs)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}

	for _, p := range todo {
		it, rec := p.it, p.rec
		what := it.what()

		var title, body, status string
		// card is the Status the board shows: the item's own, but for an
		// unfinished issue ticket its own pull request's and its chart's
		// (issueCard). The issue's open or closed state and its close reason
		// follow status, the item's own.
		var card string
		if it.kind == "ticket" {
			prs, gone := found[p.lookup].prs, found[p.lookup].gone
			prsByTicket[it.id] = prs
			for _, g := range gone {
				report.GoneRecordedPRs = append(report.GoneRecordedPRs, GoneRecordedPR{Owner: g.Owner, Repo: g.Repo, Number: g.Number, Ticket: it.id})
			}

			status = statusByTicket[it.id]
			card = status
			if p.issue {
				card, err = issueCard(st, it.id, status, statusByTicket)
				if err != nil {
					return nil, nil, nil, nil, err
				}
			}
			title, _, err = ResolveTicketTitleBody(st, it.id)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			body, err = RenderTicketBody(st, cfg, it.id, prs)
			if err != nil {
				return nil, nil, nil, nil, err
			}
		} else {
			status, err = chartStatusOf(st, it.id, "", statusByTicket)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			card = status
			title, err = chartTitle(st, it.id)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			body, err = RenderChartBody(st, cfg, it.id)
			if err != nil {
				return nil, nil, nil, nil, err
			}
		}

		cardByWhat[what] = card

		if dryRun || public {
			if hits := ScanTitleAndBody(title, body, terms); len(hits) > 0 {
				report.Skipped = append(report.Skipped, SkippedItem{What: what, Line: hits[0].Line, Text: hits[0].Text})
				skip[what] = true
				continue
			}
		}

		if rec.NodeID == "" {
			// FetchIssues leaves an empty id out, as it would a gone issue's, so
			// this record would open a second issue; GitHub refused the empty id
			// itself before reads were batched. A skipped item never got this
			// far, then or now.
			return nil, nil, nil, nil, fmt.Errorf("mirror: %s's record names no issue node_id", what)
		}
		cur, onGitHub := curByNodeID[rec.NodeID]
		if !onGitHub {
			owner, repo, serr := splitRepo(rec.Repo)
			if serr != nil {
				return nil, nil, nil, nil, serr
			}
			number := 0
			if !dryRun {
				issue, cerr := client.CreateIssue(ctx, owner, repo, title, body)
				if cerr != nil {
					return nil, nil, nil, nil, fmt.Errorf("mirror: recreate %s's issue: %w", what, cerr)
				}
				rec.Issue, rec.NodeID = issue.Number, issue.NodeID
				rec.Synced = nil
				number = issue.Number
			}
			report.Recreated = append(report.Recreated, CreatedIssue{What: what, Owner: owner, Repo: repo, Number: number})
			cur = github.IssueState{Title: title, Body: body, Open: true}
		}
		curByWhat[what] = cur

		synced := rec.Synced
		if synced == nil {
			synced = &syncedFields{}
		}

		var changed []string
		newHash := bodyHash(body)
		titleChanged := cur.Title != title
		bodyChanged := bodyHash(cur.Body) != newHash
		if titleChanged {
			if synced.Title != "" && cur.Title != synced.Title {
				report.Drift = append(report.Drift, DriftLine{What: what, Issue: rec.Issue, Field: "title"})
			}
			changed = append(changed, "title")
		}
		if bodyChanged {
			if synced.BodySHA256 != "" && bodyHash(cur.Body) != synced.BodySHA256 {
				report.Drift = append(report.Drift, DriftLine{What: what, Issue: rec.Issue, Field: "body"})
			}
			changed = append(changed, "body")
		}
		if titleChanged || bodyChanged {
			if !dryRun {
				if uerr := client.UpdateIssue(ctx, rec.NodeID, title, body); uerr != nil {
					return nil, nil, nil, nil, fmt.Errorf("mirror: update %s's issue: %w", what, uerr)
				}
			}
		}
		synced.Title = title
		synced.BodySHA256 = newHash

		wantOpen := IsOpen(status)
		curState := openState(cur.Open)
		switch {
		case cur.Open != wantOpen:
			if synced.State != "" && curState != synced.State {
				report.Drift = append(report.Drift, DriftLine{What: what, Issue: rec.Issue, Field: "state"})
			}
			if !dryRun {
				var operr error
				switch {
				case wantOpen:
					operr = client.ReopenIssue(ctx, rec.NodeID)
				case status == StatusNotPlanned:
					operr = client.CloseIssueNotPlanned(ctx, rec.NodeID)
				default:
					operr = client.CloseIssueCompleted(ctx, rec.NodeID)
				}
				if operr != nil {
					return nil, nil, nil, nil, fmt.Errorf("mirror: set %s's issue open state: %w", what, operr)
				}
			}
			changed = append(changed, "state")
			synced.State = openState(wantOpen)
		case !wantOpen && contradictsReason(status, cur.StateReason):
			// Closed as the store holds it, but under another reason: whoever
			// closed it, the reason is the store's. No drift line: the record
			// keeps no reason to compare against.
			synced.State = curState
			if !dryRun {
				owner, repo, serr := splitRepo(rec.Repo)
				if serr != nil {
					return nil, nil, nil, nil, serr
				}
				if rerr := client.SetCloseReason(ctx, owner, repo, rec.Issue, closeReason(status)); rerr != nil {
					return nil, nil, nil, nil, fmt.Errorf("mirror: set %s's issue close reason: %w", what, rerr)
				}
			}
			changed = append(changed, "close reason")
		default:
			// GitHub already holds the store's value: nothing is overwritten,
			// so nothing is drift. Record it so the next sync has a baseline.
			synced.State = curState
		}

		if !dryRun {
			rec.Synced = synced
			if werr := writeRecord(st, it, rec); werr != nil {
				return nil, nil, nil, nil, werr
			}
		}
		if len(changed) > 0 {
			report.Updated = append(report.Updated, UpdatedIssue{What: what, Fields: changed})
		}
	}
	return curByWhat, cardByWhat, prsByTicket, skip, nil
}
