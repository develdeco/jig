package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// deltaPrepared is a delta round 2 over web/ui/page.go with a recordings list,
// an excerpt, and hunks of the whole change in two files.
func deltaPrepared(intent string) verifydeliver.Prepared {
	return verifydeliver.Prepared{
		Ticket: "T-1", Round: 2, Branch: "T-1-b", Copy: "the clone's", Head: "bbbb", Base: "aaaa", Scope: "delta", Lease: "/lease",
		Intent: verifydeliver.Intent{Source: intent},
		Request: verifydeliver.ReviewRequest{
			Recordings:    "/rec/recordings.json",
			IntentExcerpt: "/ex/excerpt.md",
			MustReview:    []string{"a/a.go"},
			Hunks: []verifydeliver.HunkEntry{
				{File: "a/a.go", From: 2, To: 4},
				{File: "a/other.go", From: 6, To: 8},
			},
		},
	}
}

// TestADeltaRoundDropsWhatRound1Read (GATE-17): recordings, the whole
// change's hunks count, unprobed hunks outside the delta and the intent
// excerpt of a stated intent are not printed; the excerpt of intent none is.
func TestADeltaRoundDropsWhatRound1Read(t *testing.T) {
	t.Parallel()
	diffs := []deltaDiff{{Base: "aaaa", Head: "bbbb", Files: []string{"a/a.go"}, Lines: 1, Text: "diff\n"}}
	out := strings.Join(roundBlocks(deltaPrepared(verifydeliver.IntentSourceBrief), nil, true, diffs), "\n")
	for _, unwanted := range []string{"recordings:", "hunks:", "intent_excerpt", "a/other.go"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a delta round prints %q\n%s", unwanted, out)
		}
	}
	for _, want := range []string{"unprobed: 1", "unprobed[1]{hunk}:\n  \"a/a.go:2-4\""} {
		if !strings.Contains(out, want) {
			t.Errorf("a delta round lacks %q\n%s", want, out)
		}
	}
	none := strings.Join(roundBlocks(deltaPrepared(verifydeliver.IntentSourceNone), nil, true, diffs), "\n")
	if !strings.Contains(none, "intent_excerpt: /ex/excerpt.md") {
		t.Errorf("a delta round with no stated intent drops the excerpt, its only hint\n%s", none)
	}
}

// TestAMultiRepoDeltaListsItsUnprobedHunksWithTheirRepoPrefix: each repo's
// delta files are named <repo>/<path>, as the hunks are, so the work list
// holds the web's hunk and not the api's, which did not move.
func TestAMultiRepoDeltaListsItsUnprobedHunksWithTheirRepoPrefix(t *testing.T) {
	t.Parallel()
	p := deltaPrepared(verifydeliver.IntentSourceBrief)
	p.Repos = []verifydeliver.PreparedRepo{
		{Repo: "api", Head: "1", Base: "1", Scope: "delta"},
		{Repo: "web", Head: "3", Base: "2", Scope: "delta"},
	}
	p.Request.Hunks = []verifydeliver.HunkEntry{
		{File: "api/server/handler.go", From: 1, To: 5},
		{File: "web/ui/page.go", From: 1, To: 4},
	}
	diffs := []deltaDiff{
		{Repo: "api", Base: "1", Head: "1"},
		{Repo: "web", Base: "2", Head: "3", Files: []string{"web/ui/page.go"}, Lines: 1, Text: "d\n"},
	}
	out := strings.Join(roundBlocks(p, nil, true, diffs), "\n")
	if !strings.Contains(out, "unprobed[1]{hunk}:\n  \"web/ui/page.go:1-4\"") || strings.Contains(out, "handler.go") {
		t.Errorf("the multi-repo delta's work list is not the web's hunk alone\n%s", out)
	}
}

// TestAnswersHoldTheFindingsADeltaAnswers: open and asked findings with their
// id, and a noted finding only when a person kept it; a noted finding with no
// decision or a pending dismiss is left out.
func TestAnswersHoldTheFindingsADeltaAnswers(t *testing.T) {
	t.Parallel()
	open := []verifydeliver.OpenFinding{
		{ID: "r1-f1", Status: verifydeliver.StatusOpen, File: "a.go", Line: 1, Action: "fix", Risk: "high", Title: "open one"},
		{ID: "r1-f2", Status: verifydeliver.StatusAsked, File: "a.go", Line: 2, Action: "ask", Risk: "medium", Title: "asked one"},
		{ID: "r1-f3", Status: verifydeliver.StatusNoted, File: "a.go", Line: 3, Action: "note", Risk: "low", Title: "undecided note"},
		{ID: "r1-f4", Status: verifydeliver.StatusNoted, File: "a.go", Line: 4, Action: "note", Risk: "low", Title: "kept note", Decision: verifydeliver.TriageKeep, DecidedBy: "person"},
		{ID: "r1-f5", Status: verifydeliver.StatusNoted, File: "a.go", Line: 5, Action: "note", Risk: "low", Title: "dismissed note", Decision: verifydeliver.TriageDismiss, DecidedBy: "person"},
	}
	var ids []string
	for _, row := range answers(open) {
		ids = append(ids, row[0])
	}
	if got := strings.Join(ids, ","); got != "r1-f1,r1-f2,r1-f4" {
		t.Errorf("answers = %s, want r1-f1,r1-f2,r1-f4", got)
	}
}
