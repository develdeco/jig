package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// DriftLine is one jig-owned field this sync found edited on GitHub: GitHub's
// current value differed from what the last sync itself wrote
// (brief.md#Ownership and drift), so jig overwrote it with the store's value
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

// String renders d the way brief.md#Ownership and drift says every drift
// line prints: "jig prints `drift, overwritten: <what>` naming the ticket or
// chart, the issue number and the field".
func (d DriftLine) String() string {
	return fmt.Sprintf("drift, overwritten: %s (issue #%d): %s", d.What, d.Issue, d.Field)
}

// UpdatedIssue is one issue Sync brought up to date: its title, body or
// open/closed state changed to match the store (brief.md#Syncing at every
// checkpoint, step 4).
type UpdatedIssue struct {
	What   string
	Fields []string
}

// normalizeBody puts s in the form issue bodies compare and hash in
// (brief.md#What an issue shows: "Bodies compare after line endings become
// \n and outer whitespace is trimmed").
func normalizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

// bodyHash is the sha256 (hex) of s's normalized form, what synced.body_sha256
// keeps (brief.md#Records).
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

// updateIssues is brief.md#Syncing at every checkpoint's step 4: for every
// ticket and chart that already has a record (including one this same Sync
// just created, with only its title and footer as a body), it renders the
// full title and body - now that every ticket has an issue number any
// citation of it can resolve - derives its Status, and brings GitHub's
// title, body and open/closed state to match, reporting drift when GitHub's
// current value differs from what the last sync itself wrote
// (brief.md#Ownership and drift). An issue gone from GitHub is recreated
// (brief.md#Adoption) rather than failing the sync.
//
// Before any write, it scans the rendered title and body when public is true
// or dryRun is true (brief.md#Publish safety: every title and body is
// scanned before it is written, on every sync, not only the one that creates
// the issue): a hit is reported in report.Skipped and that item's what() is
// added to the returned skip set, so GitHub keeps what it had and
// linkParentsAndBlockers/placeBoardItems make no write for it either.
//
// dryRun withholds every mutation (UpdateIssue, CreateIssue on a recreation,
// ReopenIssue/CloseIssueCompleted, the record write) while still reading
// GitHub (FetchIssue, PullRequestsByHead) to report what would change
// (brief.md#jig trackers sync, brief.md#Cutover).
//
// It returns, for later steps to reuse rather than querying GitHub again:
// curByWhat (each item's current GitHub state, fetched before this step's
// own writes), statusByTicket (every ticket's derived Status, chart Status
// needs), prsByTicket (every ticket's own pull requests, the board step
// places) and skip (every item a publish-safety hit took out of this sync).
func updateIssues(ctx context.Context, st *store.Store, cfg project.Config, client github.Client, items []item, report *SyncReport, terms []string, public, dryRun bool) (curByWhat map[string]github.IssueState, statusByTicket map[string]string, prsByTicket map[string][]PRRef, skip map[string]bool, err error) {
	curByWhat = map[string]github.IssueState{}
	statusByTicket = map[string]string{}
	prsByTicket = map[string][]PRRef{}
	skip = map[string]bool{}

	target := "main"
	if len(cfg.Repos) > 0 {
		target = cfg.Repos[0].TargetBranch()
	}

	for _, it := range items {
		has, herr := hasRecord(it.recordAbs)
		if herr != nil {
			return nil, nil, nil, nil, herr
		}
		if !has {
			continue
		}
		rec, rerr := readRecord(it.recordAbs)
		if rerr != nil {
			return nil, nil, nil, nil, rerr
		}
		what := it.what()

		var title, body, status string
		if it.kind == "ticket" {
			branch, berr := st.TicketBranch(it.id, target)
			if berr != nil {
				return nil, nil, nil, nil, berr
			}
			recorded := make([]PRRef, len(rec.PRs))
			for i, p := range rec.PRs {
				owner, repo, _ := splitRepo(p.Repo)
				recorded[i] = PRRef{Owner: owner, Repo: repo, Number: p.Number, NodeID: p.NodeID}
			}
			prs, gone, perr := FindPullRequests(ctx, client, cfg, branch, recorded)
			if perr != nil {
				return nil, nil, nil, nil, fmt.Errorf("mirror: find %s's pull requests: %w", what, perr)
			}
			prsByTicket[it.id] = prs
			for _, g := range gone {
				report.GoneRecordedPRs = append(report.GoneRecordedPRs, GoneRecordedPR{Owner: g.Owner, Repo: g.Repo, Number: g.Number, Ticket: it.id})
			}

			status, err = TicketStatus(st, it.id, prs)
			if err != nil {
				return nil, nil, nil, nil, err
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
			statuses, cerr := chartTicketStatuses(st, it.id, statusByTicket)
			if cerr != nil {
				return nil, nil, nil, nil, cerr
			}
			status = ChartStatus(statuses)
			title, err = chartTitle(st, it.id)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			body, err = RenderChartBody(st, cfg, it.id)
			if err != nil {
				return nil, nil, nil, nil, err
			}
		}

		if it.kind == "ticket" {
			statusByTicket[it.id] = status
		}

		if dryRun || public {
			if hits := ScanTitleAndBody(title, body, terms); len(hits) > 0 {
				report.Skipped = append(report.Skipped, SkippedItem{What: what, Line: hits[0].Line, Text: hits[0].Text})
				skip[what] = true
				continue
			}
		}

		cur, ferr := client.FetchIssue(ctx, rec.NodeID)
		if errors.Is(ferr, github.ErrIssueNotFound) {
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
			ferr = nil
		}
		if ferr != nil {
			return nil, nil, nil, nil, fmt.Errorf("mirror: fetch %s's issue: %w", what, ferr)
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
		if cur.Open != wantOpen {
			if synced.State != "" && curState != synced.State {
				report.Drift = append(report.Drift, DriftLine{What: what, Issue: rec.Issue, Field: "state"})
			}
			if !dryRun {
				var operr error
				if wantOpen {
					operr = client.ReopenIssue(ctx, rec.NodeID)
				} else {
					operr = client.CloseIssueCompleted(ctx, rec.NodeID)
				}
				if operr != nil {
					return nil, nil, nil, nil, fmt.Errorf("mirror: set %s's issue open state: %w", what, operr)
				}
			}
			changed = append(changed, "state")
			synced.State = openState(wantOpen)
		} else {
			if synced.State != "" && curState != synced.State {
				report.Drift = append(report.Drift, DriftLine{What: what, Issue: rec.Issue, Field: "state"})
			}
			synced.State = curState
		}

		if !dryRun {
			rec.Synced = synced
			if werr := writeRecord(it.recordAbs, rec); werr != nil {
				return nil, nil, nil, nil, werr
			}
		}
		if len(changed) > 0 {
			report.Updated = append(report.Updated, UpdatedIssue{What: what, Fields: changed})
		}
	}
	return curByWhat, statusByTicket, prsByTicket, skip, nil
}
