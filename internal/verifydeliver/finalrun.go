package verifydeliver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// The outcomes of FinalRunReport: the final run `jig verify --final` recorded
// at the reviewed head passed or failed, or there is none.
const (
	FinalRunPass = "pass"
	FinalRunFail = "fail"
	FinalRunNone = "none"
)

// FinalRunReport is what the journal says of the final run at a repo's
// reviewed head: the run `jig verify --final` recorded at exactly that commit,
// a pass or a failure, or none. Publish says it and enforces nothing: a head
// with no final run publishes all the same.
type FinalRunReport struct {
	Repo string
	// Head is the reviewed head the run is looked for at.
	Head string
	// Outcome is FinalRunPass, FinalRunFail or FinalRunNone.
	Outcome string
	// Seconds and Command are the run's own, for a pass or a failure.
	Seconds int
	Command string
}

// finalRunAt reads the final run at head for the repo called name: lineRepo is
// the name its journal lines carry in a project of several repos, "" in a
// project of one. Only the exact head counts: a run at another commit is none.
func finalRunAt(lines []journal.Line, name, lineRepo, head string) FinalRunReport {
	r := FinalRunReport{Repo: name, Head: head, Outcome: FinalRunNone}
	line, ok := journal.FinalRunAt(lines, lineRepo, head)
	if !ok {
		return r
	}
	// A command on several lines (a union with a comment in it) cannot be shown
	// on one line without becoming another command, so it is left out, as one
	// that cannot sit in a code span is.
	if !strings.Contains(line.Command, "\n") {
		r.Command = strings.Join(strings.Fields(line.Command), " ")
	}
	r.Seconds = line.Seconds
	if line.Outcome == "pass" {
		r.Outcome = FinalRunPass
	} else {
		r.Outcome = FinalRunFail
	}
	return r
}

// finalRunsOf is the final run at each repo's reviewed head, in repo order:
// the heads in reviewed when the round recorded some, else head, the head the
// body is rendered for, in repo (a scripted round records none). qualified
// says whether the journal's lines name their repo.
func finalRunsOf(lines []journal.Line, qualified bool, reviewed map[string]string, repo, head string) []FinalRunReport {
	lineRepo := func(name string) string {
		if qualified {
			return name
		}
		return ""
	}
	if len(reviewed) == 0 {
		return []FinalRunReport{finalRunAt(lines, repo, lineRepo(repo), head)}
	}
	names := make([]string, 0, len(reviewed))
	for name := range reviewed {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]FinalRunReport, len(names))
	for i, name := range names {
		out[i] = finalRunAt(lines, name, lineRepo(name), reviewed[name])
	}
	return out
}

// ownFinalRun is the final run of the repo among finals: the one the repo's
// own head has, or the only one there is.
func ownFinalRun(finals []FinalRunReport, repo string) FinalRunReport {
	for _, f := range finals {
		if f.Repo == repo {
			return f
		}
	}
	if len(finals) > 0 {
		return finals[0]
	}
	return FinalRunReport{Repo: repo, Outcome: FinalRunNone}
}

// Words is the run as the PR body's Verification section and the publish
// report say it: what the reviewed head has, with the command in a code span
// (a command that cannot sit in one is left out, and the line stands without
// it).
func (r FinalRunReport) Words() string {
	head := shortSHA(r.Head)
	// The command follows its colon; one that cannot sit in a code span is left
	// out with the colon, so the sentence ends where it should.
	tail := ""
	if r.Command != "" && !strings.Contains(r.Command, "`") {
		tail = ": `" + r.Command + "`"
	}
	switch r.Outcome {
	case FinalRunPass:
		return fmt.Sprintf("Final run at the reviewed head %s: pass in %d s%s", head, r.Seconds, tail)
	case FinalRunFail:
		return fmt.Sprintf("Final run at the reviewed head %s failed%s", head, tail)
	}
	return "No final run at the reviewed head " + head
}

// RunHint is the line that gives the reviewed head a final run: the command to
// run in the worktree at that head.
func (r FinalRunReport) RunHint(ticket string) string {
	return fmt.Sprintf("jig verify %s --final", ticket)
}
