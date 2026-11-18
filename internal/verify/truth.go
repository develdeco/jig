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

// BranchCommits lists the commits reachable from rev and not from start in the
// work tree dir whose message carries a slice trailer, oldest first, each with
// the slice it names (the last trailer wins). It runs one git log however many
// commits there are.
func BranchCommits(dir, start, rev string) ([]phase.CommitRef, error) {
	out, err := gitx.Run(dir, "log", "--no-merges", "--reverse",
		"--format=%H%x1f%(trailers:key="+SliceTrailerKey+",valueonly,separator=%x1f)%x1e", start+".."+rev)
	if err != nil {
		return nil, err
	}
	var refs []phase.CommitRef
	for _, rec := range strings.Split(out, "\x1e") {
		parts := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(parts) < 2 {
			continue
		}
		slice := ""
		for _, p := range parts[1:] {
			if p = strings.TrimSpace(p); p != "" {
				slice = p
			}
		}
		if slice != "" {
			refs = append(refs, phase.CommitRef{SHA: parts[0], Slice: slice})
		}
	}
	return refs, nil
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
	// BranchSlices are the slices that build in a repo whose branch was read,
	// in a project of several repos where only some branches are. nil says
	// every slice's branch was read (when BranchKnown).
	BranchSlices map[string]bool
}

// AtOrAfter reports whether at is commit or a commit made after it on the
// branch: commits is the branch's log, oldest first. A run or a verified line
// at such a commit covers a tree that holds commit. It is the one rule the
// Stop hook (a commit is history once its slice is verified at a later one)
// and a slice's last run (does it cover the slice's last commit) share.
func AtOrAfter(commits []phase.CommitRef, commit, at string) bool {
	if commit == "" || at == "" {
		return false
	}
	if commit == at {
		return true
	}
	seen := false
	for _, c := range commits {
		if c.SHA == commit {
			seen = true
			continue
		}
		if seen && c.SHA == at {
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
