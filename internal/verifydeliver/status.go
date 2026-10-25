package verifydeliver

import (
	"fmt"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// Status is how a ticket stands in the gate and the publish: the latest
// round's verdict and who reviewed it, the round open now, if any, and what a
// publish has recorded of the pull request. It reads the store and nothing
// else, so `jig here` can show it anywhere.
type Status struct {
	Ticket string `json:"ticket"`
	// Rounds is how many gate rounds are recorded.
	Rounds int `json:"rounds"`
	// Verdict, Independent and Reviewer are the latest recorded round's:
	// clean, fix-slices or not-independent; whether its reviewer was
	// independent of the builders ("" for a scripted or launched round); and
	// who the reviewer was.
	Verdict     string       `json:"verdict,omitempty"`
	Independent Independence `json:"independent,omitempty"`
	Reviewer    *Provenance  `json:"reviewer,omitempty"`
	// ReviewedSHA is repo -> the head the latest round reviewed.
	ReviewedSHA map[string]string `json:"reviewed_sha,omitempty"`
	// Open is the round opened and not yet recorded.
	Open *OpenStatus `json:"open,omitempty"`
	// BodyFor, ApprovedFor, PR and Published are what the journal says of the
	// publish: the head the pull request body was last rendered for, the head
	// a person approved, the pull request recorded or opened, and whether the
	// publish finished.
	BodyFor     string `json:"body_for,omitempty"`
	ApprovedFor string `json:"approved_for,omitempty"`
	PR          string `json:"pr,omitempty"`
	Published   bool   `json:"published,omitempty"`
}

// OpenStatus is a round that is open: opened by `jig gate` or `jig review
// context`, and waiting for `jig review done`. A round over the one repo of a
// project of one has its head and copy here; a round in a project of several
// repos has them in Repos, one entry for each repo it covers.
type OpenStatus struct {
	Round    int              `json:"round"`
	Head     string           `json:"head,omitempty"`
	Copy     string           `json:"copy,omitempty"`
	Repos    []OpenRepoStatus `json:"repos,omitempty"`
	Drafts   int              `json:"drafts"`
	Reviewer Provenance       `json:"reviewer,omitempty"`
}

// OpenRepoStatus is one repo of an open round: the commit under review there
// and whose copy of the branch it is.
type OpenRepoStatus struct {
	Repo string `json:"repo"`
	Head string `json:"head"`
	Copy string `json:"copy"`
}

// ReadStatus reads ticket's gate and publish state from the store.
func ReadStatus(st *store.Store, ticket string) (Status, error) {
	s := Status{Ticket: ticket}
	n, err := existingGateRounds(st, ticket)
	if err != nil {
		return Status{}, fmt.Errorf("verifydeliver: status: count rounds: %w", err)
	}
	s.Rounds = n
	if n > 0 {
		rep, err := latestGateReport(st, ticket)
		if err == nil {
			s.Verdict, s.Independent, s.ReviewedSHA = rep.Verdict, rep.Independent, rep.ReviewedSHA
			s.Reviewer = rep.Reviewer
		}
	}
	if of, ok, err := readOpenFile(st, ticket, n+1); err != nil {
		return Status{}, err
	} else if ok {
		p := preparedOf("", st, of, ReviewRequest{})
		drafts, err := ReadDrafts(p)
		if err != nil {
			return Status{}, err
		}
		s.Open = &OpenStatus{Round: of.Round, Head: of.Head, Copy: of.Copy, Drafts: len(drafts), Reviewer: of.Reviewer}
		for _, r := range of.Repos {
			s.Open.Repos = append(s.Open.Repos, OpenRepoStatus{Repo: r.Repo, Head: r.Head, Copy: r.Copy})
		}
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return Status{}, fmt.Errorf("verifydeliver: status: read journal: %w", err)
	}
	for _, l := range lines {
		switch l.Event {
		case journal.EventPrBody:
			s.BodyFor = l.Commit
		case journal.EventPublishApproved:
			if l.Provenance == "person" {
				s.ApprovedFor = l.Commit
			}
		case journal.EventPr:
			if l.URL != "" {
				s.PR = l.URL
			}
		case journal.EventPublishDone:
			s.Published = true
		}
	}
	return s, nil
}
