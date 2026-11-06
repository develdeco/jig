package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// PRRef is one pull request found for a ticket's branch, or recorded in its
// prs: that the query no longer found (brief.md#Status and pull requests).
type PRRef struct {
	Owner, Repo string
	Number      int
	NodeID      string
	// State is GitHub's own spelling: "OPEN", "CLOSED" or "MERGED".
	State string
}

// prLookup is one ticket's question for findPullRequests: its branch and
// the pull requests its record lists.
type prLookup struct {
	branch   string
	recorded []PRRef
}

// prsFound is one prLookup's answer: the ticket's pull requests, and the
// recorded ones GitHub answered NOT_FOUND for.
type prsFound struct {
	prs  []PRRef
	gone []GoneRecordedPR
}

// findPullRequests returns each lookup's ticket's pull requests
// (brief.md#Status and pull requests): for each of cfg.Repos whose GitHub
// owner and repo are known (repohost.OwnerRepo), every pull request in any
// state whose head is exactly the lookup's branch, in the project's repo
// order and by number, then each of its recorded entries that none of those
// queries turned up (matched by owner, repo and number), re-fetched by node
// id for its current state and repository (PullRequestsByIDs) and dropped
// when GitHub no longer has it - a recorded prs: entry carries no state of
// its own (record.go's prRecord), so a merged pull request whose head branch
// GitHub has since deleted would otherwise be listed with no state and
// never count toward Done. A sync never deletes store data it cannot check
// (the human's r9-f1 decision): a recorded entry with no node_id
// (hand-edited, or written by a bridge version that omitted it;
// brief.md#Records says records the bridge wrote are adopted as they are)
// stays in the result with no State, listed in the body and kept in the
// record exactly as before this re-check existed - it is only left out of
// the PullRequestsByIDs lookup, rather than sent as an empty id. An entry
// PullRequestsByIDs does send but GitHub answers NOT_FOUND for (r9-f2: that
// can mean the token lost access, not that the pull request is gone) stays
// the same way too, with one warning returned in gone so the caller can
// print it and the operator can remove the entry by hand if it really is
// gone.
//
// Every lookup is answered at once, with one PullRequestsByHeads per repo
// and one PullRequestsByIDs for every lookup's unfound recorded entries, so
// a sync's pull request reads stay a few queries however many tickets it
// covers.
func findPullRequests(ctx context.Context, client github.Client, cfg project.Config, lookups []prLookup) ([]prsFound, error) {
	type repoPRs struct {
		owner, repo string
		byHead      map[string][]github.PullRequest
	}
	var repos []repoPRs
	if len(lookups) > 0 {
		branches := make([]string, len(lookups))
		for i, l := range lookups {
			branches[i] = l.branch
		}
		for _, r := range cfg.Repos {
			owner, repo, ok := repohost.OwnerRepo(r.Remote)
			if !ok {
				continue
			}
			byHead, err := client.PullRequestsByHeads(ctx, owner, repo, branches)
			if err != nil {
				return nil, fmt.Errorf("mirror: find pull requests in %s/%s: %w", owner, repo, err)
			}
			repos = append(repos, repoPRs{owner: owner, repo: repo, byHead: byHead})
		}
	}

	out := make([]prsFound, len(lookups))
	missing := make([][]PRRef, len(lookups))
	var missingIDs []string
	for i, l := range lookups {
		seen := map[string]bool{}
		for _, r := range repos {
			prs := r.byHead[l.branch]
			sort.Slice(prs, func(a, b int) bool { return prs[a].Number < prs[b].Number })
			for _, pr := range prs {
				out[i].prs = append(out[i].prs, PRRef{Owner: r.owner, Repo: r.repo, Number: pr.Number, NodeID: pr.NodeID, State: pr.State})
				seen[prKey(r.owner, r.repo, pr.Number)] = true
			}
		}
		for _, rec := range l.recorded {
			if seen[prKey(rec.Owner, rec.Repo, rec.Number)] {
				continue
			}
			if rec.NodeID == "" {
				// A recorded entry with no node_id (hand-edited, or written by a
				// bridge version that omitted it) cannot be re-checked by id. It
				// stays listed and recorded with no State, exactly as before this
				// re-check existed; only the PullRequestsByIDs lookup leaves it
				// out, rather than sending GitHub an empty id and failing the
				// whole sync over one malformed record.
				out[i].prs = append(out[i].prs, PRRef{Owner: rec.Owner, Repo: rec.Repo, Number: rec.Number})
				continue
			}
			missingIDs = append(missingIDs, rec.NodeID)
			missing[i] = append(missing[i], rec)
		}
	}
	if len(missingIDs) == 0 {
		return out, nil
	}
	current, err := client.PullRequestsByIDs(ctx, missingIDs)
	if err != nil {
		return nil, fmt.Errorf("mirror: fetch recorded pull requests: %w", err)
	}
	byNodeID := map[string]github.PullRequest{}
	for _, pr := range current {
		byNodeID[pr.NodeID] = pr
	}
	for i := range lookups {
		for _, rec := range missing[i] {
			pr, ok := byNodeID[rec.NodeID]
			if !ok {
				// GitHub answered NOT_FOUND for this id, which can mean the token
				// lost access rather than that the pull request is truly gone
				// (r9-f2). It stays listed and recorded with no State, and the
				// caller is told so it can warn the operator, who can remove the
				// entry by hand if it really is gone.
				out[i].prs = append(out[i].prs, PRRef{Owner: rec.Owner, Repo: rec.Repo, Number: rec.Number, NodeID: rec.NodeID})
				out[i].gone = append(out[i].gone, GoneRecordedPR{Owner: rec.Owner, Repo: rec.Repo, Number: rec.Number})
				continue
			}
			out[i].prs = append(out[i].prs, PRRef{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number, NodeID: pr.NodeID, State: pr.State})
		}
	}
	return out, nil
}

// GoneRecordedPR is one recorded pull request PullRequestsByIDs could not
// find (GitHub answered NOT_FOUND for its node id). findPullRequests keeps
// it listed and recorded anyway (r9-f2) and reports it here, Ticket left
// for the caller to fill in, so the caller can print one warning naming
// the ticket, Owner, Repo and Number, since NOT_FOUND can mean the token
// lost access rather than that the pull request is gone.
type GoneRecordedPR struct {
	Ticket      string
	Owner, Repo string
	Number      int
}

func prKey(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// The Status of a ticket that was closed without being built (`jig close`).
// A ticket covered by other work is Done, as one whose pull request merged is;
// one that is superseded or not planned has a Status of its own.
const (
	StatusDone       = "Done"
	StatusNotPlanned = "Not planned"
)

// TicketStatus derives ticket's Status (brief.md#Status and pull requests):
// Done when it was closed as covered or any of prs merged; Not planned when it
// was closed as superseded or not planned; In review when one of prs is open;
// In progress when its journal is not empty or any slice has left queued;
// Briefed when it has a brief.md; Backlog otherwise. A closure wins over every
// other state: the store says the ticket is closed.
func TicketStatus(st *store.Store, ticket string, prs []PRRef) (string, error) {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return "", err
	}
	if c := rec.Closed; c != nil {
		if c.Reason == store.CloseCovered {
			return StatusDone, nil
		}
		return StatusNotPlanned, nil
	}
	for _, pr := range prs {
		if strings.EqualFold(pr.State, "MERGED") {
			return StatusDone, nil
		}
	}
	for _, pr := range prs {
		if strings.EqualFold(pr.State, "OPEN") {
			return "In review", nil
		}
	}

	lines, err := journal.Read(st, ticket)
	if err != nil {
		return "", err
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	leftQueued := false
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return "", err
		}
		if ss.State != "queued" {
			leftQueued = true
			break
		}
	}
	if len(lines) > 0 || leftQueued {
		return "In progress", nil
	}
	if hasBrief(st, ticket) {
		return "Briefed", nil
	}
	return "Backlog", nil
}

// hasBrief reports whether ticket has a brief.md in the store.
func hasBrief(st *store.Store, ticket string) bool {
	_, err := os.Stat(filepath.Join(st.TicketDir(ticket), "brief.md"))
	return err == nil
}

// ChartStatus derives a chart's Status from its own tickets' Statuses
// (brief.md#Status and pull requests): Done when it has tickets and every
// one is finished, Done or Not planned (a closed ticket is finished); In
// progress when any is In progress, In review or Done; Backlog otherwise.
func ChartStatus(ticketStatuses []string) string {
	if len(ticketStatuses) == 0 {
		return "Backlog"
	}
	allDone := true
	anyAdvanced := false
	for _, s := range ticketStatuses {
		if s != StatusDone && s != StatusNotPlanned {
			allDone = false
		}
		if s == "In progress" || s == "In review" || s == "Done" {
			anyAdvanced = true
		}
	}
	if allDone {
		return "Done"
	}
	if anyAdvanced {
		return "In progress"
	}
	return "Backlog"
}

// IsOpen reports whether status's issue should be open on GitHub
// (brief.md#Status and pull requests): every status but Done and Not planned
// is open.
func IsOpen(status string) bool {
	return status != StatusDone && status != StatusNotPlanned
}
