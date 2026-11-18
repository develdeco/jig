package verify

import (
	"strings"

	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// SliceTrailerKey is the commit trailer that names the slice a commit is for.
const SliceTrailerKey = "Jig-Slice"

// BranchCommits reads the branch past start in the work tree dir with one git
// log: the commits reachable from rev and not from start whose message carries
// a slice trailer, oldest first, each with the slice it names (the last trailer
// wins; a merge commit is not one), and every commit of the range with its
// parents, so descent over the branch is answered with no git process more.
func BranchCommits(dir, start, rev string) ([]phase.CommitRef, map[string][]string, error) {
	out, err := gitx.Run(dir, "log", "--reverse",
		"--format=%H%x1f%P%x1f%(trailers:key="+SliceTrailerKey+",valueonly,separator=%x1f)%x1e", start+".."+rev)
	if err != nil {
		return nil, nil, err
	}
	var refs []phase.CommitRef
	parents := map[string][]string{}
	for _, rec := range strings.Split(out, "\x1e") {
		parts := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		ps := strings.Fields(parts[1])
		parents[parts[0]] = ps
		if len(ps) > 1 {
			continue
		}
		slice := ""
		for _, p := range parts[2:] {
			if p = strings.TrimSpace(p); p != "" {
				slice = p
			}
		}
		if slice != "" {
			refs = append(refs, phase.CommitRef{SHA: parts[0], Slice: slice})
		}
	}
	return refs, parents, nil
}

// StartSHA is the start sha the ticket recorded in repo, "" when it has none.
func StartSHA(st *store.Store, ticket, repo string) string {
	if repo == "" {
		return ""
	}
	data, err := fileshare.ReadFile(st.StartSHAPath(ticket, repo))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Truth is what the readers of a ticket's verifications share: the journal
// and, when the branch is known, one git log.
type Truth struct {
	Ticket string
	// Lines are the ticket's journal lines.
	Lines []journal.Line
	// Slices are the ids of the ticket's slices: a commit whose trailer names
	// another slice is not the ticket's.
	Slices []string
	// Commits are the branch's commits with a slice trailer and BranchKnown says
	// the branch was read (BranchCommits); without it only what the journal
	// shows is judged.
	Commits     []phase.CommitRef
	BranchKnown bool
	// Parents holds every commit of the branch past the start sha with its
	// parents, trailer or not, merge or not (BranchCommits).
	Parents map[string][]string
	// BranchSlices are the slices that build in a repo whose branch was read,
	// in a project of several repos where only some branches are. nil says
	// every slice's branch was read (when BranchKnown).
	BranchSlices map[string]bool
}

// AtOrAfter reports whether at is commit or a commit that has it as an
// ancestor, over parents (every commit of the branch past the start sha, with
// its parents, as BranchCommits reads them). A run or a verified line at such
// a commit covers a tree that holds commit, whether the commit between them
// carries a trailer, is a merge of the target branch or is neither. It is the
// one rule a slice's last run (does it cover the slice's last commit), the
// Stop hook and `verification_states` share.
func AtOrAfter(parents map[string][]string, commit, at string) bool {
	if commit == "" || at == "" {
		return false
	}
	seen := map[string]bool{}
	stack := []string{at}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c == commit {
			return true
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		stack = append(stack, parents[c]...)
	}
	return false
}

// History reports whether commit of slice is history, not something still to
// be given a result: the slice has a later commit of its own on the branch
// (the command that clears a commit runs at HEAD, so a test commit before the
// code is history once the code exists), or the slice has a verified line at a
// commit after it (slices verified one after the other at HEAD). Without the
// branch nothing is history.
func (t Truth) History(commit, slice string) bool {
	latest := ""
	for _, c := range t.Commits { // oldest first
		if c.Slice == slice {
			latest = c.SHA
		}
	}
	if latest != "" && latest != commit {
		return true
	}
	for _, l := range t.Lines {
		if l.Event == journal.EventVerified && l.Slice == slice && l.Commit != "" && l.Commit != commit && AtOrAfter(t.Parents, commit, l.Commit) {
			return true
		}
	}
	return false
}

// Derive judges every (commit, slice) as phase.DeriveVerifications does, over
// the commits of the ticket's own slices.
func (t Truth) Derive() []phase.CommitVerification {
	in := phase.VerifyInput{Ticket: t.Ticket, Lines: t.Lines, BranchKnown: t.BranchKnown}
	own := map[string]bool{}
	for _, s := range t.Slices {
		own[s] = true
	}
	for _, c := range t.Commits {
		if own[c.Slice] {
			in.Commits = append(in.Commits, c)
		}
	}
	return phase.DeriveVerifications(in)
}

// Outstanding is Derive's rows that are not a pass and not history: the
// commits a reader should name. A slice's building tip with no recorded
// result is one.
func (t Truth) Outstanding() []phase.CommitVerification {
	var out []phase.CommitVerification
	for _, r := range t.Derive() {
		if r.State.Unclear() && !t.History(r.Commit, r.Slice) {
			out = append(out, r)
		}
	}
	return out
}
