package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// The codes of the conditions a claim is checked against.
const (
	// claimBlockedMerged: a blocker of kind merged has not published, or its
	// work has not merged: its recorded pull request is open or was closed
	// unmerged, or there is neither a merged pull request nor its branch in the
	// target.
	claimBlockedMerged = "CLAIM_BLOCKED_MERGED"
	// claimStackedParentNotClean: a blocker of kind stacked has no clean gate
	// round yet.
	claimStackedParentNotClean = "CLAIM_STACKED_PARENT_NOT_CLEAN"
	// claimNotApproved: the brief has no approval, or was edited after it.
	claimNotApproved = "CLAIM_NOT_APPROVED"
)

// claimProblem is one condition a claim does not meet, with the way to meet
// it.
type claimProblem struct {
	Code    string
	Message string
	Help    string
}

// claimDeps are what the checks ask of the world beyond the store.
type claimDeps struct {
	// merges says whether the work of a blocker has merged (decision 43). nil
	// judges a blocker by its journal alone: it is met once it published.
	merges mergeSource
}

// mergeSource says whether the work of a ticket has merged:
// *mergefacts.Reader.
type mergeSource interface {
	Of(ticket string) (ready.Merge, error)
}

// claimProblems checks the conditions under which ticket may be built, in the
// order of the codes above, and returns each one it does not meet. Who is
// building decides what follows: a claim made unattended is refused on any
// of them, one made with a person watching is warned.
func claimProblems(st *store.Store, ticket string, d claimDeps) ([]claimProblem, error) {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return nil, err
	}
	var out []claimProblem
	for _, b := range rec.BlockedBy {
		blocker := b.Ticket
		if resolved, err := st.ResolveTicket(b.Ticket); err == nil {
			blocker = resolved
		}
		p, err := blockerProblem(st, ticket, blocker, b.Kind, d)
		if err != nil {
			return nil, err
		}
		if p != nil {
			// What makes the ticket ready is the blocker getting on: the prompt
			// of the phase the blocker is at.
			p.Help += promptHint(blockerPrompt(st, blocker, map[string]bool{ticket: true}, d.merges))
			out = append(out, *p)
		}
	}
	approval, err := approvalProblem(st, ticket)
	if err != nil {
		return nil, err
	}
	if approval != nil {
		out = append(out, *approval)
	}
	return out, nil
}

// blockerProblem is the problem blocker of the given kind is for ticket, nil
// when it is met. A stacked blocker is met by a clean gate round; any other
// kind (merged, or none) by its work having merged: it published, and its
// recorded pull request is merged or, with none recorded, its branch is in the
// target (d.merges). With nothing to read that from, publishing is enough.
func blockerProblem(st *store.Store, ticket, blocker, kind string, d claimDeps) (*claimProblem, error) {
	if kind == "stacked" {
		rounds, verdict, _, err := latestGateRound(st, blocker)
		if err != nil {
			return nil, err
		}
		if rounds > 0 && verdict == "clean" {
			return nil, nil
		}
		return &claimProblem{
			Code:    claimStackedParentNotClean,
			Message: fmt.Sprintf("%s is stacked on %s, whose gate has no clean round yet", ticket, blocker),
			Help:    fmt.Sprintf("Run `jig gate %s` until a round is clean", blocker),
		}, nil
	}
	var m ready.Merge
	if d.merges != nil {
		var err error
		if m, err = d.merges.Of(blocker); err != nil {
			return nil, err
		}
	} else {
		published, err := isPublished(st, blocker)
		if err != nil {
			return nil, err
		}
		m = ready.Merge{Published: published, Repos: []ready.RepoMerge{{Met: published}}}
	}
	switch {
	case m.Met():
		return nil, nil
	case !m.Published:
		return &claimProblem{
			Code:    claimBlockedMerged,
			Message: fmt.Sprintf("%s waits for %s, which has not published", ticket, blocker),
			Help:    fmt.Sprintf("Publish and merge %s first; `jig next` shows what is ready", blocker),
		}, nil
	}
	return &claimProblem{
		Code:    claimBlockedMerged,
		Message: fmt.Sprintf("%s waits for %s: %s", ticket, blocker, m.Why()),
		Help:    mergeHelp(blocker, m),
	}, nil
}

// mergeHelp is what meets a blocker that published but whose work has not
// merged: for each repo that is missing, merge its open pull request, publish
// again after one closed unmerged, check the host when it could not be asked,
// or record the pull request when none is on record.
func mergeHelp(blocker string, m ready.Merge) string {
	var out []string
	for _, r := range m.Repos {
		if r.Met {
			continue
		}
		var h string
		switch {
		case r.State == ready.PROpen:
			h = fmt.Sprintf("Merge %s, then try again", r.PR)
		case r.State == ready.PRClosed:
			h = fmt.Sprintf("Its pull request %s was closed without merging: open the one that lands %s and record it with `jig publish %s --pr <url>`", r.PR, blocker, blocker)
		case r.PR != "":
			h = fmt.Sprintf("Check `gh auth status` and the network so %s can be read, then try again", r.PR)
		default:
			h = fmt.Sprintf("Merge the pull request of %s and record it with `jig publish %s --pr <url>`; if it merged already, fetch the repo so its origin has the merge", blocker, blocker)
		}
		if r.Repo != "" {
			h = r.Repo + ": " + h
		}
		out = append(out, h)
	}
	return strings.Join(out, "; ")
}

// approvalProblem is the problem when the brief of ticket is not approved: no
// approval on record, or one made of a brief that has changed since. A ticket
// with no brief has nothing to approve.
func approvalProblem(st *store.Store, ticket string) (*claimProblem, error) {
	sum, err := st.BriefSHA256(ticket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	approval, ok, err := st.ReadApproval(ticket)
	if err != nil {
		return nil, err
	}
	switch {
	case !ok:
		return &claimProblem{
			Code:    claimNotApproved,
			Message: fmt.Sprintf("the brief of %s is not approved", ticket),
			Help:    fmt.Sprintf("Run `jig validate %s`: a valid brief is approved there", ticket) + promptHint(phase.Prompt(phase.Brief, ticket)) + " (approve its brief)",
		}, nil
	case !strings.EqualFold(approval.BriefSHA256, sum):
		return &claimProblem{
			Code:    claimNotApproved,
			Message: fmt.Sprintf("the brief of %s changed after it was approved", ticket),
			Help:    fmt.Sprintf("Run `jig validate %s` to approve it as it is now", ticket) + promptHint(phase.Prompt(phase.Brief, ticket)) + " (approve its brief)",
		}, nil
	}
	return nil, nil
}
