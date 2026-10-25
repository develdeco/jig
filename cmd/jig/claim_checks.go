package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// The codes of the conditions a claim is checked against.
const (
	// claimBlockedMerged: a blocker of kind merged has not published, or its
	// pull request is not merged where the host can tell.
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
	// merged says whether the pull request at url is merged; known is false
	// when the host cannot tell (no gh, no network, not a pull request).
	merged func(url string) (merged, known bool)
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
			p.Help += promptHint(blockerPrompt(st, blocker, map[string]bool{ticket: true}))
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
// kind (merged, or none) by publishing, and by the pull request being merged
// when it has one and the host can tell.
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
	lines, err := journal.Read(st, blocker)
	if err != nil {
		return nil, err
	}
	published, prURL := false, ""
	for _, l := range lines {
		switch l.Event {
		case journal.EventPublishDone:
			published = true
		case journal.EventPr:
			if l.URL != "" {
				prURL = l.URL
			}
		}
	}
	switch {
	case !published:
		return &claimProblem{
			Code:    claimBlockedMerged,
			Message: fmt.Sprintf("%s waits for %s, which has not published", ticket, blocker),
			Help:    fmt.Sprintf("Publish and merge %s first; `jig next` shows what is ready", blocker),
		}, nil
	case prURL != "" && d.merged != nil:
		if merged, known := d.merged(prURL); known && !merged {
			return &claimProblem{
				Code:    claimBlockedMerged,
				Message: fmt.Sprintf("%s waits for %s, whose pull request %s is not merged yet", ticket, blocker, prURL),
				Help:    fmt.Sprintf("Merge %s, then claim again", prURL),
			}, nil
		}
	}
	return nil, nil
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
