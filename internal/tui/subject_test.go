package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// TestCommitWordsNameTheSubjectFromTheOracleLine: jig verify writes a commit's
// subject on its oracle line, and the screens name the commit with it, take it
// as the slice's latest commit, and list a run that covers nothing without
// letting it decide the commit's state.
func TestCommitWordsNameTheSubjectFromTheOracleLine(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	env := func(l journal.Line) events.Envelope { return events.Envelope{Src: events.SrcJournal, Line: l} }
	tk := &ticket{id: "JIG-5", lines: []events.Envelope{
		env(journal.Line{Event: journal.EventOracle, Slice: "a", Commit: sha, Outcome: "pass", Subject: "fix Clamp"}),
		env(journal.Line{Event: journal.EventOracle, Slice: "a", Commit: sha, Outcome: "fail", Uncovered: journal.UncoveredDirtyBefore}),
	}}
	if got := commitWords(tk, sha); !strings.Contains(got, "“fix Clamp”") || !strings.HasPrefix(got, sha[:7]) {
		t.Errorf("commitWords = %q, want the short id and the subject", got)
	}
	if got := tk.lastCommit("a"); !strings.Contains(got, "fix Clamp") {
		t.Errorf("lastCommit = %q, want the commit an oracle line was of", got)
	}
	if _, failed := tk.lastRunFailed("a"); failed {
		t.Error("an uncovered failure was taken for the slice's latest word")
	}
	recs := tk.commits("a")
	if len(recs) != 1 || recs[0].Subject != "fix Clamp" || recs[0].State != StateBuilding || len(recs[0].Runs) != 2 {
		t.Errorf("commits = %+v, want one commit with its subject, both runs and the state the covered pass gave", recs)
	}
}
