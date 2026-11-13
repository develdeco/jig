package verify

import (
	"strings"
	"time"

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

// Truth is what the readers of a ticket's verifications share: the journal,
// the ledger and, when the branch is known, one git log.
type Truth struct {
	JigHome string
	Ticket  string
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
	Now         time.Time
	// Alive is the worker liveness check; nil is the operating system's.
	Alive func(pid int, notBefore time.Time) bool
}

// Derive judges every (commit, slice) as phase.DeriveVerifications does, with
// the spawned lines of the pending ones read from the ledger once.
func (t Truth) Derive() []phase.CommitVerification {
	in := phase.VerifyInput{Ticket: t.Ticket, Lines: t.Lines, BranchKnown: t.BranchKnown, Now: t.Now, Alive: t.Alive}
	if in.Alive == nil {
		in.Alive = Alive
	}
	own := map[string]bool{}
	for _, s := range t.Slices {
		own[s] = true
	}
	for _, c := range t.Commits {
		if own[c.Slice] {
			in.Commits = append(in.Commits, c)
		}
	}
	var pending []phase.CommitRef
	for _, p := range journal.Pending(t.Lines) {
		pending = append(pending, phase.CommitRef{SHA: p.Commit, Slice: p.Slice})
	}
	in.Spawned, _ = LatestSpawned(t.JigHome, t.Ticket, pending)
	return phase.DeriveVerifications(in)
}
