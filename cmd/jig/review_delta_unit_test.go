package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// deltaPrepared is a delta round 2 over a/a.go with a recordings list and an
// excerpt.
func deltaPrepared(intent string) verifydeliver.Prepared {
	return verifydeliver.Prepared{
		Ticket: "T-1", Round: 2, Branch: "T-1-b", Copy: "the clone's", Head: "bbbb", Base: "aaaa", Scope: "delta",
		Intent: verifydeliver.Intent{Source: intent},
		Request: verifydeliver.ReviewRequest{
			Recordings:    "/rec/recordings.json",
			IntentExcerpt: "/ex/excerpt.md",
			MustReview:    []string{"a/a.go"},
		},
	}
}

// TestADeltaRoundDropsWhatRound1Read: recordings and the intent excerpt of a
// stated intent are not printed; the excerpt of intent none is.
func TestADeltaRoundDropsWhatRound1Read(t *testing.T) {
	t.Parallel()
	diffs := []deltaDiff{{Base: "aaaa", Head: "bbbb", Files: []string{"a/a.go"}, Lines: 1, Text: "diff\n"}}
	out := strings.Join(roundBlocks(deltaPrepared(verifydeliver.IntentSourceBrief), nil, true, diffs), "\n")
	for _, unwanted := range []string{"recordings:", "intent_excerpt"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a delta round prints %q\n%s", unwanted, out)
		}
	}
	none := strings.Join(roundBlocks(deltaPrepared(verifydeliver.IntentSourceNone), nil, true, diffs), "\n")
	if !strings.Contains(none, "intent_excerpt: /ex/excerpt.md") {
		t.Errorf("a delta round with no stated intent drops the excerpt, its only hint\n%s", none)
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

// TestAMultiRepoDeltaNamesItsFilesWithTheirRepoThroughTheDiff: the files of a
// repo's delta are read from git and prefixed with the repo by
// computeDeltaDiffs, and the repo that did not move has none.
func TestAMultiRepoDeltaNamesItsFilesWithTheirRepoThroughTheDiff(t *testing.T) {
	t.Parallel()
	mk := func(name string) (dir, base, head string) {
		dir = t.TempDir()
		gitMust(t, dir, "init", "-q")
		if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitBuilder(t).Commit(dir, name+" base")
		base = gitMust(t, dir, "rev-parse", "HEAD")
		return dir, base, base
	}
	apiDir, apiBase, apiHead := mk("api")
	webDir, webBase, _ := mk("web")
	if err := os.WriteFile(filepath.Join(webDir, "f.go"), []byte("package x\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, webDir, "commit", "-q", "-am", "web change")
	webHead := gitMust(t, webDir, "rev-parse", "HEAD")

	p := deltaPrepared(verifydeliver.IntentSourceBrief)
	p.RequestPath = filepath.Join(t.TempDir(), "review.json")
	p.Repos = []verifydeliver.PreparedRepo{
		{Repo: "api", Base: apiBase, Head: apiHead, Scope: "delta"},
		{Repo: "web", Base: webBase, Head: webHead, Scope: "delta"},
	}
	diffs, err := computeDeltaDiffs(p, map[string]string{"api": apiDir, "web": webDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 2 || len(diffs[0].Files) != 0 || !reflect.DeepEqual(diffs[1].Files, []string{"web/f.go"}) {
		t.Fatalf("delta files = %+v, want none for api and web/f.go for web", diffs)
	}
}
