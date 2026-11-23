package verifydeliver

import (
	"github.com/develdeco/jig/internal/finalrun"
	"github.com/develdeco/jig/internal/journal"
)

// The final run at a reviewed head is read by internal/finalrun, which the
// screens read it by too; these are its names here.
const (
	FinalRunPass = finalrun.Pass
	FinalRunFail = finalrun.Fail
	FinalRunNone = finalrun.None
)

// FinalRunReport is what the journal says of the final run at a repo's
// reviewed head (finalrun.Report).
type FinalRunReport = finalrun.Report

func finalRunAt(lines []journal.Line, name, lineRepo, head string) FinalRunReport {
	return finalrun.At(lines, name, lineRepo, head)
}

func finalRunsOf(lines []journal.Line, qualified bool, reviewed map[string]string, repo, head string) []FinalRunReport {
	return finalrun.Of(lines, qualified, reviewed, repo, head)
}

func ownFinalRun(finals []FinalRunReport, repo string) FinalRunReport {
	return finalrun.Own(finals, repo)
}
