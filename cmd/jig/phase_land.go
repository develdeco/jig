package main

import (
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// mergedOnHost says the host reports the work of a published ticket merged
// while no landed line is on record: a merge made on the host, which `jig next`
// names and `jig land` records. It asks the merge source, which keeps what the
// host said, so a ticket that has not published, one already landed, one with no
// pull request and a caller with no merge source ask nothing. issue is the issue
// a shared task is read for.
func mergedOnHost(merges mergeSource, f phase.Facts, lines []journal.Line, issue string) bool {
	if merges == nil || !f.Published || f.Closed != "" {
		return false
	}
	if landed, hasPR := phase.LandedIn(lines); landed || !hasPR {
		return false
	}
	var m ready.Merge
	var err error
	if is, ok := merges.(issueMergeSource); ok && issue != "" {
		m, err = is.OfFor(f.Ticket, issue)
	} else {
		m, err = merges.Of(f.Ticket)
	}
	return err == nil && m.Met()
}

// landAdvice is the advice for a published ticket that has not landed: the
// person's word is what lands it, and `jig land` names the merge and records it.
// "" for a ticket that is not at Land.
func landAdvice(t phase.Ticket, ticket string) string {
	if t.Current != phase.Land {
		return ""
	}
	if t.MergedOnHost() {
		return ticket + " is published and its pull request merged on the host, not recorded: run `jig land " + ticket + "` to record it" + promptHint(t.Prompt)
	}
	return ticket + " is published and waits for a merge: at the person's word run `jig land " + ticket + "`, make the merge it names, then run it again" + promptHint(t.Prompt)
}

// The words of a shared task's landing into one issue.
const (
	landedWords   = "landed"
	mergedWords   = "merged on the host, not recorded"
	waitsForWords = "waits for a merge"
)

// landWords is where a shared task's landing into issue stands, from the state
// of its pull request there: landed when a landed line is on record for the
// issue, merged on the host and not recorded, or waiting for a merge. "" for a
// pull request that was closed or whose state is not known.
func landWords(st *store.Store, ticket, issue, prState string) string {
	if got, err := land.Landed(st, ticket, issue); err == nil && len(got) > 0 {
		return landedWords
	}
	switch prState {
	case issueMerged:
		return mergedWords
	case issueOpen:
		return waitsForWords
	}
	return ""
}
