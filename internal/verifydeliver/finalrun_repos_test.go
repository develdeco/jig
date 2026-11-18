package verifydeliver

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestFinalRunsOfAProjectOfSeveralRepos: each repo's reviewed head has its own
// final run, found by the repo its line names, and the body says a line for
// every repo the round reviewed, not only the one it is rendered for; a
// project of one repo looks its lines up with no repo and a scripted round
// with no reviewed head asks for the head it renders.
func TestFinalRunsOfAProjectOfSeveralRepos(t *testing.T) {
	t.Parallel()
	const apiHead, webHead = "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"
	line := func(repo, head, outcome string) journal.Line {
		return journal.Line{Event: journal.EventFinalOracle, Repo: repo, Commit: head, HeadEnd: head, Outcome: outcome, Command: "go test ./" + repo, Seconds: 5}
	}
	lines := []journal.Line{line("api", apiHead, "pass"), line("web", webHead, "fail")}
	reviewed := map[string]string{"web": webHead, "api": apiHead}

	got := finalRunsOf(lines, true, reviewed, "api", apiHead)
	want := []FinalRunReport{
		{Repo: "api", Head: apiHead, Outcome: FinalRunPass, Seconds: 5, Command: "go test ./api"},
		{Repo: "web", Head: webHead, Outcome: FinalRunFail, Seconds: 5, Command: "go test ./web"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("finalRunsOf = %+v, want one report for each repo, by name: %+v", got, want)
	}
	if own := ownFinalRun(got, "web"); own.Outcome != FinalRunFail {
		t.Errorf("ownFinalRun(web) = %+v, want web's own", own)
	}

	// A line of one repo says nothing of another repo's head.
	if got := finalRunsOf(lines[:1], true, reviewed, "api", apiHead); got[1].Outcome != FinalRunNone {
		t.Errorf("web's final run = %+v, want none: only api's line is in the journal", got[1])
	}

	// A command on several lines (a union holding a comment) is left out of the
	// words, never flattened into another command.
	multi := journal.Line{Event: journal.EventFinalOracle, Commit: apiHead, Outcome: "pass", Command: "(\necho one # note\n) && (echo two)", Seconds: 2}
	flat := finalRunsOf([]journal.Line{multi}, false, nil, "fixture-repo", apiHead)[0]
	if flat.Command != "" || strings.Contains(flat.Words(), "echo") || !strings.HasSuffix(flat.Words(), "pass in 2 s") {
		t.Errorf("a command on several lines is shown as %+v / %q, want it left out", flat, flat.Words())
	}

	// A project of one repo: its lines name no repo, and its report is its own.
	plain := journal.Line{Event: journal.EventFinalOracle, Commit: apiHead, Outcome: "pass", Command: "go test ./...", Seconds: 2}
	one := finalRunsOf([]journal.Line{plain}, false, map[string]string{"fixture-repo": apiHead}, "fixture-repo", apiHead)
	if len(one) != 1 || one[0].Outcome != FinalRunPass {
		t.Errorf("finalRunsOf in a project of one repo = %+v, want its pass", one)
	}
	// A round that recorded no reviewed head asks for the head it renders.
	scripted := finalRunsOf([]journal.Line{plain}, false, nil, "fixture-repo", apiHead)
	if len(scripted) != 1 || scripted[0].Head != apiHead || scripted[0].Outcome != FinalRunPass || !strings.Contains(scripted[0].Words(), "pass in 2 s") {
		t.Errorf("finalRunsOf with no reviewed head = %+v, want the rendered head's pass", scripted)
	}
}
