package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// finalLine is a covering final-oracle line at commit.
func finalLine(commit, outcome string, seconds int, command string) journal.Line {
	return journal.Line{Event: journal.EventFinalOracle, Commit: commit, Outcome: outcome, Seconds: seconds, Command: command}
}

func TestPureRuleFinalRunAtIsTheRunAtExactlyTheReviewedHead(t *testing.T) {
	t.Parallel()
	const head = "aaaa"
	uncovered := finalLine(head, "pass", 3, "ran-uncovered")
	uncovered.Uncovered = journal.UncoveredDirtyAfter
	multiline := finalLine(head, "pass", 5, "one\ntwo")
	for _, c := range []struct {
		name  string
		lines []journal.Line
		want  FinalRunReport
	}{
		{"no line", nil, FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunNone}},
		{"a pass names its seconds and command", []journal.Line{finalLine(head, "pass", 9, "(the-command) && (another)")},
			FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunPass, Seconds: 9, Command: "(the-command) && (another)"}},
		{"a failure names its command", []journal.Line{finalLine(head, "fail", 4, "the-failing-command")},
			FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunFail, Seconds: 4, Command: "the-failing-command"}},
		{"a run at the base is none", []journal.Line{finalLine("bbbb", "pass", 9, "ran-at-the-base")}, FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunNone}},
		{"a run that covers nothing is none", []journal.Line{uncovered}, FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunNone}},
		{"a command on several lines is left out", []journal.Line{multiline}, FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunPass, Seconds: 5}},
		{"spaces in a command are one space", []journal.Line{finalLine(head, "pass", 1, "go   test\t./a")},
			FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunPass, Seconds: 1, Command: "go test ./a"}},
		{"the latest run at the head decides", []journal.Line{finalLine(head, "fail", 4, "first"), finalLine(head, "pass", 6, "second")},
			FinalRunReport{Repo: "r", Head: head, Outcome: FinalRunPass, Seconds: 6, Command: "second"}},
	} {
		if got := finalRunAt(c.lines, "r", "", head); got != c.want {
			t.Errorf("%s: finalRunAt = %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := finalRunAt([]journal.Line{finalLine("", "pass", 1, "x")}, "r", "", ""); got.Outcome != FinalRunNone {
		t.Errorf("a run at no head: finalRunAt = %+v, want none", got)
	}
}
