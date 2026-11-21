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
	// PRs is repo -> the pull request recorded or opened there, for a ticket
	// whose publish names its repos (a project of several): PR is the latest.
	PRs map[string]string `json:"prs,omitempty"`
	// PRHead and PRHeads are the head each pull request line recorded: the
	// latest, and repo -> the latest in that repo. They are what a pull request
	// was opened or updated for, which the branch may have moved past.
	PRHead  string            `json:"pr_head,omitempty"`
	PRHeads map[string]string `json:"pr_heads,omitempty"`
	// Issues is, for a shared task, the publish state of each issue it is
	// published into, in chart order; the fields above are then empty.
	Issues []IssueStatus `json:"issues,omitempty"`
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
//
// A shared task (two issues list it) has its publish state in Issues, one for
// each issue, and the flat fields empty; ReadStatusFor reads one issue's into
// the flat fields.
func ReadStatus(st *store.Store, ticket string) (Status, error) {
	return ReadStatusFor(st, ticket, "")
}

// ReadStatusFor is ReadStatus with the publish fields read for one issue.
func ReadStatusFor(st *store.Store, ticket, issue string) (Status, error) {
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
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return Status{}, fmt.Errorf("verifydeliver: status: read ticket: %w", err)
	}
	containers, err := st.ContainersOf(ticket, rec)
	if err != nil {
		return Status{}, fmt.Errorf("verifydeliver: status: containers: %w", err)
	}
	if issue == "" && len(containers) > 1 {
		// A shared task has one publish state for each issue and none of its
		// own.
		for _, c := range containers {
			lines, err := journal.ReadFor(st, ticket, c)
			if err != nil {
				return Status{}, fmt.Errorf("verifydeliver: status: read journal: %w", err)
			}
			is := publishStatus(lines)
			is.Issue = c
			s.Issues = append(s.Issues, is)
		}
		return s, nil
	}
	lines, err := journal.ReadFor(st, ticket, issue)
	if err != nil {
		return Status{}, fmt.Errorf("verifydeliver: status: read journal: %w", err)
	}
	is := publishStatus(lines)
	s.BodyFor, s.ApprovedFor, s.PR, s.PRs, s.Published = is.BodyFor, is.ApprovedFor, is.PR, is.PRs, is.Published
	s.PRHead, s.PRHeads = is.PRHead, is.PRHeads
	return s, nil
}

// IssueStatus is what the journal says of the publish of a shared task into
// one issue: the fields of Status that a publish records, for that issue's
// pull requests alone.
type IssueStatus struct {
	Issue       string            `json:"issue"`
	BodyFor     string            `json:"body_for,omitempty"`
	ApprovedFor string            `json:"approved_for,omitempty"`
	PR          string            `json:"pr,omitempty"`
	PRs         map[string]string `json:"prs,omitempty"`
	// PRHead is the head the latest pull request line recorded: the head that
	// issue's pull request was opened or updated for.
	PRHead    string            `json:"pr_head,omitempty"`
	PRHeads   map[string]string `json:"pr_heads,omitempty"`
	Published bool              `json:"published,omitempty"`
}

// publishStatus reads the publish fields from journal lines already read for
// one issue (or the whole ticket's).
func publishStatus(lines []journal.Line) IssueStatus {
	var s IssueStatus
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
				s.PRHead = l.Commit
				if l.Repo != "" {
					if s.PRHeads == nil {
						s.PRHeads = map[string]string{}
					}
					s.PRHeads[l.Repo] = l.Commit
				}
				if l.Repo != "" {
					if s.PRs == nil {
						s.PRs = map[string]string{}
					}
					s.PRs[l.Repo] = l.URL
				}
			}
		case journal.EventPublishDone:
			s.Published = true
		}
	}
	return s
}
