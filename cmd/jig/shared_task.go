package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// A shared task is a ticket two or more model charts list: it has one branch
// and a pull request into each issue's branch. This file reads where it stands
// for one issue, and for all of them (the rows `jig next` and `jig status`
// print), from the journal's per-issue publish lines (journal.ReadFor) and the
// per-issue merge fact (mergefacts.Reader.OfFor).

// issueMergeSource is a mergeSource that can also say whether a shared task
// merged into one issue: *mergefacts.Reader.
type issueMergeSource interface {
	OfFor(ticket, issue string) (ready.Merge, error)
}

// sharedIssues are the issues of ticket when it is a shared task, in chart
// order; nil for any other ticket, or one that cannot be read.
func sharedIssues(st *store.Store, ticket string) []string {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return nil
	}
	cs, err := st.ContainersOf(ticket, rec)
	if err != nil || len(cs) < 2 {
		return nil
	}
	return cs
}

// dependentIssue is the issue, of those a shared blocker is listed by, that
// the dependent belongs to: the issue of its chart, or of the multi-task's it
// sits under. "" when it belongs to none of them (it is not in their charts, or
// is itself shared): the blocker is then read for every issue.
func dependentIssue(st *store.Store, dependent string, issues []string) string {
	id := dependent
	for range 3 {
		rec, err := st.ReadTicket(id)
		if err != nil {
			return ""
		}
		cs, err := st.ContainersOf(id, rec)
		if err != nil || len(cs) != 1 {
			return ""
		}
		if slices.Contains(issues, cs[0]) {
			return cs[0]
		}
		id = cs[0]
	}
	return ""
}

// blockerIssue is the issue a blocker is read for from the dependent: issue
// when given (the dependent's chart is known), else the dependent's own. "" for
// a blocker that is not a shared task, or that no issue of the dependent
// decides.
func blockerIssue(st *store.Store, dependent, blocker, issue string) string {
	issues := sharedIssues(st, blocker)
	if issues == nil {
		return ""
	}
	if issue == "" {
		issue = dependentIssue(st, dependent, issues)
	}
	if slices.Contains(issues, issue) {
		return issue
	}
	return ""
}

// mergeFor is the merge fact of blocker as the dependent sees it: a shared
// blocker is read for the dependent's own issue, so a task merged into one
// issue does not release a dependent of the other.
func mergeFor(st *store.Store, merges mergeSource, dependent, blocker, issue string) (ready.Merge, error) {
	if is := blockerIssue(st, dependent, blocker, issue); is != "" {
		if f, ok := merges.(issueMergeSource); ok {
			return f.OfFor(blocker, is)
		}
	}
	return merges.Of(blocker)
}

// publishedFor is whether blocker published, as the dependent sees it: into the
// dependent's issue when blocker is a shared task, else at all.
func publishedFor(st *store.Store, dependent, blocker, issue string) (bool, error) {
	is := blockerIssue(st, dependent, blocker, issue)
	if is == "" {
		return isPublished(st, blocker)
	}
	lines, err := journal.ReadFor(st, blocker, is)
	if err != nil {
		return false, err
	}
	return hasPublishDone(lines), nil
}

// publishedAll is whether the ticket whose whole journal is lines has published:
// a publish-done line, and for a shared task one into each of its issues. A
// journal whose publish-done lines name no issue (written before the ticket
// could be shared, or of a ticket with no container) is published by any one,
// and no chart is read for it.
func publishedAll(st *store.Store, ticket string, lines []journal.Line) bool {
	done := false
	into := map[string]bool{}
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			done = true
			if l.Into != "" {
				into[l.Into] = true
			}
		}
	}
	if !done || len(into) == 0 {
		return done
	}
	for _, issue := range sharedIssues(st, ticket) {
		if !into[issue] {
			return false
		}
	}
	return true
}

func hasPublishDone(lines []journal.Line) bool {
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			return true
		}
	}
	return false
}

// nextIssueRow is one repo of one issue of a shared task, as `jig next` and
// `jig status` print it: the issue, the branch its pull request goes into, the
// pull request and where it stands. Stale says the task moved on after the
// issue merged it.
type nextIssueRow struct {
	Issue  string `json:"issue"`
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch"`
	PR     string `json:"pr"`
	State  string `json:"state"`
	Stale  string `json:"stale,omitempty"`
	// Land says where the issue's landing stands: landed, merged on the host and
	// not recorded, or waits for a merge; "" before the task publishes into it.
	Land string `json:"land,omitempty"`
}

// The states of an issue's pull request of a shared task.
const (
	issueUnpublished = "not published"
	issueOpen        = "open"
	issueMerged      = "merged"
	issueClosed      = "closed"
	issueUnknown     = "unknown"
)

// sharedRows are the rows of the shared task id, one for each issue and repo,
// and the issues that have no pull request yet. Nil for any other ticket. merges
// may be nil (a pull request is then only recorded, its merge unknown).
func sharedRows(st *store.Store, input stack.Input, merges mergeSource, id string) ([]nextIssueRow, []string, error) {
	issues := sharedIssues(st, id)
	if issues == nil {
		return nil, nil, nil
	}
	plan, err := stack.Read(input, id)
	if err != nil {
		return nil, nil, err
	}
	var rows []nextIssueRow
	var unpublished []string
	for _, issue := range issues {
		status, err := verifydeliver.ReadStatusFor(st, id, issue)
		if err != nil {
			return nil, nil, err
		}
		var merge ready.Merge
		if f, ok := merges.(issueMergeSource); ok && status.PR != "" {
			if merge, err = f.OfFor(id, issue); err != nil {
				return nil, nil, err
			}
		}
		irec, _ := st.ReadTicket(issue)
		repos := plan.Repos
		if len(repos) == 0 {
			repos = []stack.Repo{{}}
		}
		any := false
		for _, r := range repos {
			row := nextIssueRow{Issue: issue, Repo: r.Repo, PR: "-", State: issueUnpublished}
			for _, c := range r.Containers {
				if c.Container == issue {
					row.Branch = c.Branch
				}
			}
			if row.Branch == "" {
				row.Branch, _ = st.ResolveTicketBranch(issue, irec, r.Target)
			}
			url, head := status.PRs[r.Repo], status.PRHeads[r.Repo]
			if url == "" && len(repos) == 1 {
				url, head = status.PR, status.PRHead
			}
			if url != "" {
				any = true
				row.PR, row.State = url, prState(merge, r.Repo, len(repos) > 1)
				row.Land = landWords(st, id, issue, row.State)
				if row.State == issueMerged && head != "" && r.Head != "" && head != r.Head {
					row.Stale = fmt.Sprintf("stale: merged at %s, the task is at %s", shortSHA(head), shortSHA(r.Head))
				}
			}
			rows = append(rows, row)
		}
		if !any || !status.Published {
			unpublished = append(unpublished, issue)
		}
	}
	return rows, unpublished, nil
}

// prState is the state of a pull request recorded for a repo, from the merge
// fact read for its issue.
func prState(m ready.Merge, repo string, several bool) string {
	for i, r := range m.Repos {
		if several && r.Repo != repo && !(i == 0 && r.Repo == "") {
			continue
		}
		switch {
		case r.Met:
			return issueMerged
		case r.State == ready.PROpen:
			return issueOpen
		case r.State == ready.PRClosed:
			return issueClosed
		}
		return issueUnknown
	}
	return issueUnknown
}

// sharedBlocks are the table of rows and one note for each stale row.
func sharedBlocks(rows []nextIssueRow) []string {
	if len(rows) == 0 {
		return nil
	}
	table := make([][]string, len(rows))
	for i, r := range rows {
		table[i] = []string{r.Issue, dashed(r.Repo), r.Branch, r.PR, r.State}
	}
	blocks := []string{axi.Table("shared", []string{"issue", "repo", "branch", "pr", "state"}, table)}
	for _, r := range rows {
		if r.Stale != "" {
			blocks = append(blocks, "note: "+r.Issue+": "+r.Stale)
		}
	}
	return blocks
}

// sharedStatusSection is the section of `jig status` for a shared task: one
// row for each issue and repo, and a note for each issue that has no pull
// request yet or merged an older head of the task. Any other ticket has none.
func sharedStatusSection(cfg project.Config, mp project.MachineProject, merges mergeSource) statusSection {
	return statusSection{Name: "shared", Order: 230, Blocks: func(st *store.Store, ticket string) ([]string, error) {
		rows, unpublished, err := sharedRows(st, stackInput(st, cfg, mp, merges), merges, ticket)
		if err != nil || len(rows) == 0 {
			return nil, err
		}
		blocks := sharedBlocks(rows)
		for _, h := range sharedHelp(ticket, rows, unpublished) {
			blocks = append(blocks, "note: "+h)
		}
		return blocks, nil
	}}
}

// sharedHelp is the advice for a shared task: one publish for each issue that
// has none, and for each stale issue the fix as a new task of its chart, since
// the pull request merged. An issue whose pull request is open takes a push to
// the same branch.
func sharedHelp(ticket string, rows []nextIssueRow, unpublished []string) []string {
	var help []string
	for _, issue := range unpublished {
		help = append(help, fmt.Sprintf("run `jig publish %s --issue %s` to open its pull request into %s", ticket, issue, issue))
	}
	// One landing for each issue that has published and not landed: the
	// person's word lands it, one issue at a time.
	landIssues := map[string]bool{}
	for _, r := range rows {
		if r.Land == "" || r.Land == landedWords || landIssues[r.Issue] {
			continue
		}
		landIssues[r.Issue] = true
		help = append(help, fmt.Sprintf("%s into %s %s: at the person's word run `jig land %s --issue %s`", ticket, r.Issue, r.Land, ticket, r.Issue))
	}
	staleIssues := map[string]bool{}
	for _, r := range rows {
		if r.Stale == "" || staleIssues[r.Issue] {
			continue
		}
		staleIssues[r.Issue] = true
		help = append(help, fmt.Sprintf("%s changed after %s merged it (%s): the fix for %s is a new task in its chart, not a push to a merged branch; the pull requests still open take the push",
			ticket, r.Issue, strings.TrimPrefix(r.Stale, "stale: "), r.Issue))
	}
	return help
}
