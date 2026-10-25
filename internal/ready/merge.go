package ready

import (
	"fmt"
	"strings"
	"time"
)

// How the work of a ticket was found to have merged in a repo.
const (
	// ByPR: the pull request recorded for the repo is merged, as the repo host
	// says.
	ByPR = "pr"
	// ByAncestry: the ticket's branch tip is in the target branch of the repo
	// on its origin. It is positive evidence only: a branch that was squashed
	// or rebased into the target has a different tip and is never found this
	// way.
	ByAncestry = "ancestry"
)

// The states of a pull request as the repo host spells them (GitHub's own).
const (
	PROpen   = "OPEN"
	PRClosed = "CLOSED"
	PRMerged = "MERGED"
)

// Evidence is what is known of one repo's share of a ticket's work, to be
// judged by JudgeRepo. The caller reads it (the journal, the repo host, the
// clone); judging it reads nothing.
type Evidence struct {
	// Ticket is the ticket whose work it is, named in the command that records
	// its pull request.
	Ticket string
	// Repo names the repo in a project of several repos, "" in a project of
	// one.
	Repo string
	// PR is the pull request recorded for the repo: the latest `publish --pr`.
	// "" when none is.
	PR string
	// State is what the host said of PR: PROpen, PRClosed or PRMerged, or ""
	// when it could not be read.
	State string
	// AsOf is when State was read, when this run did not read it itself (it is
	// the host's answer from earlier, kept on this machine); the zero time when
	// it was read now. Offline says the host could not be asked, so State is
	// the last answer it gave.
	AsOf    time.Time
	Offline bool
	// Ancestor says the ticket's branch tip is an ancestor of the repo's
	// target branch on its origin (the clone's origin/<target>, as of its last
	// fetch). False when it is not, and when that could not be checked.
	Ancestor bool
}

// RepoMerge is whether the work of a ticket has merged in one repo, and how
// that is known.
type RepoMerge struct {
	Repo string
	Met  bool
	// By is how a met repo was found: ByPR or ByAncestry.
	By string
	// PR is the pull request the fact rests on, "" when none is recorded, and
	// State what the host said of it (PROpen, PRClosed or PRMerged), "" when
	// it could not be read.
	PR    string
	State string
	// Why is, for a repo not met, what is missing, in words; it ends with the
	// time of the host's answer when that answer is not from this run.
	Why string
	// AsOf is when the host's answer behind a repo not met was read, when it
	// was not read in this run; the zero time otherwise.
	AsOf time.Time
}

// JudgeRepo says whether the work in e has merged. The order is the recorded
// pull request, then ancestry, then nothing:
//
//   - a recorded pull request the host answered for decides: merged is met,
//     closed is not met (it was closed without merging), open is not met;
//   - with no recorded pull request, or one the host could not be asked about,
//     the ticket's branch tip being an ancestor of the target is met, by
//     ancestry; that is positive evidence only, and nothing else (a patch
//     that is equivalent, say) stands for it;
//   - otherwise it is not met, and the reason names the command that records
//     the pull request.
func JudgeRepo(e Evidence) RepoMerge {
	out := RepoMerge{Repo: e.Repo, PR: e.PR, State: e.State}
	note := asOfNote(e)
	switch {
	case e.PR != "" && e.State == PRMerged:
		out.Met, out.By = true, ByPR
	case e.PR != "" && e.State == PRClosed:
		out.Why = "its PR was closed without merging (" + e.PR + ")" + note
	case e.PR != "" && e.State == PROpen:
		out.Why = "its PR is open (" + e.PR + ")" + note
	case e.Ancestor:
		out.Met, out.By = true, ByAncestry
	case e.PR != "":
		out.Why = "its PR could not be read from the host (" + e.PR + ")"
	default:
		out.Why = fmt.Sprintf("no merge on record: record its PR with `jig publish %s --pr <url>`", e.Ticket)
	}
	if !out.Met && e.State != "" {
		out.AsOf = e.AsOf
	}
	return out
}

// asOfNote is the clause that dates a host answer which this run did not read:
// "" for one it did.
func asOfNote(e Evidence) string {
	if e.AsOf.IsZero() {
		return ""
	}
	at := e.AsOf.UTC().Format("2006-01-02 15:04 UTC")
	if e.Offline {
		return " (the host did not answer; as of " + at + ")"
	}
	return " (as of " + at + ")"
}

// Merge is whether the work of one ticket has merged, in every repo it builds
// in. A ticket that has not published has no merge to look for.
type Merge struct {
	// Published says the ticket's journal has a publish-done line. A ticket
	// that did not publish is never met, whatever its branch holds.
	Published bool
	// Repos is one fact per repo the ticket builds in, in the project's order.
	Repos []RepoMerge
}

// Met reports whether the work has merged in every repo of the ticket: a
// multi-repo ticket is met when each repo's pull request is merged (or its
// branch tip is in the repo's target).
func (m Merge) Met() bool {
	if !m.Published || len(m.Repos) == 0 {
		return false
	}
	for _, r := range m.Repos {
		if !r.Met {
			return false
		}
	}
	return true
}

// NotPublished is what a ticket that has not published is waiting for.
const NotPublished = "it has not published"

// Why is what the ticket's merge is missing, in words; "" when it is met. A
// project of several repos names the repo before each reason.
func (m Merge) Why() string {
	switch {
	case m.Met():
		return ""
	case !m.Published:
		return NotPublished
	case len(m.Repos) == 0:
		return "it builds in no repo that is known"
	}
	var parts []string
	for _, r := range m.Repos {
		if r.Met {
			continue
		}
		if r.Repo != "" {
			parts = append(parts, r.Repo+": "+r.Why)
		} else {
			parts = append(parts, r.Why)
		}
	}
	return strings.Join(parts, "; ")
}

// AsOf is the oldest time a host answer behind a missing repo was read, when
// one was not read in this run; the zero time when every answer is fresh.
func (m Merge) AsOf() time.Time {
	var oldest time.Time
	for _, r := range m.Repos {
		if r.Met || r.AsOf.IsZero() {
			continue
		}
		if oldest.IsZero() || r.AsOf.Before(oldest) {
			oldest = r.AsOf
		}
	}
	return oldest
}

// Via is how a met merge is known: ByPR when every repo's pull request is
// merged, ByAncestry when every repo was found by its branch tip, and both
// joined by "+" when it is mixed. "" when the merge is not met.
func (m Merge) Via() string {
	if !m.Met() {
		return ""
	}
	var by []string
	seen := map[string]bool{}
	for _, r := range m.Repos {
		if !seen[r.By] {
			seen[r.By] = true
			by = append(by, r.By)
		}
	}
	return strings.Join(by, "+")
}
