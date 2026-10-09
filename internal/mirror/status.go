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

// FindPullRequests returns ticket's pull requests (brief.md#Status and pull
// requests): for each of cfg.Repos whose GitHub owner and repo are known
// (repohost.OwnerRepo), every pull request in any state whose head is
// exactly branch, in the project's repo order and by number, then each of
// recorded that none of those queries turned up (matched by owner, repo and
// number), re-fetched by node id for its current state and repository
// (PullRequestsByIDs) and dropped when GitHub no longer has it - a recorded
// prs: entry carries no state of its own (record.go's prRecord), so a merged
// pull request whose head branch GitHub has since deleted would otherwise
// be listed with no state and never count toward Done. A sync never
// deletes store data it cannot check (the human's r9-f1 decision): a
// recorded entry with no node_id (hand-edited, or written by a bridge
// version that omitted it; brief.md#Records says records the bridge wrote
// are adopted as they are) stays in the result with no State, listed in
// the body and kept in the record exactly as before this re-check existed
// - it is only left out of the PullRequestsByIDs lookup, rather than sent
// as an empty id. An entry PullRequestsByIDs does send but GitHub answers
// NOT_FOUND for (r9-f2: that can mean the token lost access, not that the
// pull request is gone) stays the same way too, with one warning returned
// in gone so the caller can print it and the operator can remove the
// entry by hand if it really is gone.
func FindPullRequests(ctx context.Context, client github.Client, cfg project.Config, branch string, recorded []PRRef) ([]PRRef, []GoneRecordedPR, error) {
	var found []PRRef
	seen := map[string]bool{}
	for _, r := range cfg.Repos {
		owner, repo, ok := repohost.OwnerRepo(r.Remote)
		if !ok {
			continue
		}
		prs, err := client.PullRequestsByHead(ctx, owner, repo, branch)
		if err != nil {
			return nil, nil, fmt.Errorf("mirror: find pull requests in %s/%s: %w", owner, repo, err)
		}
		sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })
		for _, pr := range prs {
			found = append(found, PRRef{Owner: owner, Repo: repo, Number: pr.Number, NodeID: pr.NodeID, State: pr.State})
			seen[prKey(owner, repo, pr.Number)] = true
		}
	}

	var missingIDs []string
	var missing []PRRef
	for _, r := range recorded {
		if seen[prKey(r.Owner, r.Repo, r.Number)] {
			continue
		}
		if r.NodeID == "" {
			// A recorded entry with no node_id (hand-edited, or written by a
			// bridge version that omitted it) cannot be re-checked by id. It
			// stays listed and recorded with no State, exactly as before this
			// re-check existed; only the PullRequestsByIDs lookup leaves it
			// out, rather than sending GitHub an empty id and failing the
			// whole sync over one malformed record.
			found = append(found, PRRef{Owner: r.Owner, Repo: r.Repo, Number: r.Number})
			continue
		}
		missingIDs = append(missingIDs, r.NodeID)
		missing = append(missing, r)
	}
	if len(missingIDs) == 0 {
		return found, nil, nil
	}
	current, err := client.PullRequestsByIDs(ctx, missingIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("mirror: fetch recorded pull requests: %w", err)
	}
	byNodeID := map[string]github.PullRequest{}
	for _, pr := range current {
		byNodeID[pr.NodeID] = pr
	}
	var gone []GoneRecordedPR
	for _, r := range missing {
		pr, ok := byNodeID[r.NodeID]
		if !ok {
			// GitHub answered NOT_FOUND for this id, which can mean the token
			// lost access rather than that the pull request is truly gone
			// (r9-f2). It stays listed and recorded with no State, and the
			// caller is told so it can warn the operator, who can remove the
			// entry by hand if it really is gone.
			found = append(found, PRRef{Owner: r.Owner, Repo: r.Repo, Number: r.Number, NodeID: r.NodeID})
			gone = append(gone, GoneRecordedPR{Owner: r.Owner, Repo: r.Repo, Number: r.Number})
			continue
		}
		found = append(found, PRRef{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number, NodeID: pr.NodeID, State: pr.State})
	}
	return found, gone, nil
}

// GoneRecordedPR is one recorded pull request PullRequestsByIDs could not
// find (GitHub answered NOT_FOUND for its node id). FindPullRequests keeps
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

// TicketStatus derives ticket's Status (brief.md#Status and pull requests):
// Done when any of prs merged; In review when one is open; In progress when
// its journal is not empty or any slice has left queued; Briefed when it
// has a brief.md; Backlog otherwise.
func TicketStatus(st *store.Store, ticket string, prs []PRRef) (string, error) {
	for _, pr := range prs {
		if strings.EqualFold(pr.State, "MERGED") {
			return "Done", nil
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
// one is Done; In progress when any is In progress, In review or Done;
// Backlog otherwise.
func ChartStatus(ticketStatuses []string) string {
	if len(ticketStatuses) == 0 {
		return "Backlog"
	}
	allDone := true
	anyAdvanced := false
	for _, s := range ticketStatuses {
		if s != "Done" {
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
// (brief.md#Status and pull requests): every status but Done is open.
func IsOpen(status string) bool {
	return status != "Done"
}
