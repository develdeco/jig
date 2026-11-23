package finalrun

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

func final(repo, commit, outcome string, secs int) journal.Line {
	return journal.Line{Event: journal.EventFinalOracle, Repo: repo, Commit: commit, Outcome: outcome, Seconds: secs, Command: "go test ./..."}
}

// TestOfReadsTheRunAtEachReviewedHead: the run at the exact head of each repo,
// none for a head with no run, and the repo's name on a line only in a project
// of several.
func TestOfReadsTheRunAtEachReviewedHead(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{final("api", "aaaaaaa1", "fail", 30), final("api", "aaaaaaa1", "pass", 40), final("web", "bbbbbbb0", "pass", 5)}
	got := Of(lines, true, map[string]string{"web": "bbbbbbb1", "api": "aaaaaaa1"}, "", "")
	if len(got) != 2 || got[0].Repo != "api" || got[0].Outcome != Pass || got[0].Seconds != 40 || got[1].Repo != "web" || got[1].Outcome != None {
		t.Errorf("Of = %+v, want api's latest pass in 40 s and none at web's head", got)
	}
	one := Of([]journal.Line{final("", "ccccccc1", "fail", 9)}, false, nil, "app", "ccccccc1")
	if len(one) != 1 || one[0].Outcome != Fail || !strings.Contains(one[0].Words(), "failed") {
		t.Errorf("one repo: %+v", one)
	}
	if own := Own(got, "web"); own.Repo != "web" {
		t.Errorf("Own = %+v", own)
	}
}
