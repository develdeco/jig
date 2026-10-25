package verifydeliver

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// TestFreshnessIsWhatASessionDidToATicket: a session is fresh for a ticket
// when its id carries no journal line of the ticket but the gate's, triage and
// publish lines, and no ledger line of it names the ticket with a changed file
// or a commit. Each kind of work makes it not fresh and is named in words; a
// session jig has no record of is not known; an empty one is not either.
func TestFreshnessIsWhatASessionDidToATicket(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	ticket := fx.Ticket
	const sha = "d7101d0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	for _, l := range []journal.Line{
		{Event: journal.EventClaim, Session: "s-claimer", Slice: "round"},
		{Event: journal.EventClaim, Session: "s-claimer", Slice: "round"},
		{Event: journal.EventVerified, Session: "s-claimer", Slice: "round", Commit: sha},
		{Event: journal.EventApproval, Session: "s-approver"},
		{Event: journal.EventQuestion, Session: "s-asker", Ref: "q-001"},
		{Event: journal.EventNote, Session: "s-noter"},
		{Event: journal.EventGateOpen, Session: "s-reviewer"},
		{Event: journal.EventGateReviewer, Session: "s-reviewer"},
		{Event: journal.EventGateRound, Session: "s-reviewer"},
		{Event: journal.EventTriage, Session: "s-reviewer", Ref: "r1-f1"},
		{Event: journal.EventPrBody, Session: "s-reviewer"},
		{Event: journal.EventPublishApproved, Session: "s-reviewer"},
		{Event: journal.EventSpend, Session: "s-reviewer"},
	} {
		if err := journal.Append(d.Store, ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []journal.Line{
		{Event: journal.EventSessionStart, Session: "s-reviewer"},
		{Event: journal.EventTool, Session: "s-reviewer", Ticket: ticket, Command: "jig next " + ticket},
		{Event: journal.EventTool, Session: "s-reviewer", Ticket: ticket, Command: "git log --oneline -3"},
		{Event: journal.EventTool, Session: "s-reviewer", Ticket: ticket, Command: "git -C repo log --grep commit"},
		{Event: journal.EventTool, Session: "s-editor", Ticket: ticket, Files: []string{"alpha/alpha.go"}},
		{Event: journal.EventTool, Session: "s-elsewhere-editor", Ticket: "JIG-9", Files: []string{"x.go"}},
		{Event: journal.EventTool, Session: "s-shell", Ticket: ticket, Command: `"C:\Program Files\Git\bin\git.exe" -C clone commit -m x`},
		{Event: journal.EventGitCommit, Session: "s-committer", Ticket: ticket, Commit: sha},
		{Event: journal.EventSessionStart, Session: "s-elsewhere-editor"},
	} {
		if err := ledger.Append(d.Home, l); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name, session string
		want          Fresh
	}{
		{"claims and verification", "s-claimer", Fresh{Known: true, Why: []string{"claimed slice round", "verified d7101d0"}}},
		{"approval", "s-approver", Fresh{Known: true, Why: []string{"approved the brief"}}},
		{"question", "s-asker", Fresh{Known: true, Why: []string{"asked question q-001"}}},
		{"note", "s-noter", Fresh{Known: true, Why: []string{"left a note"}}},
		{"a reviewer: gate, triage and publish lines, and tools that only read", "s-reviewer", Fresh{Known: true, Fresh: true}},
		{"a changed file in the ledger", "s-editor", Fresh{Known: true, Why: []string{"changed alpha/alpha.go"}}},
		{"a commit a shell made", "s-shell", Fresh{Known: true, Why: []string{"ran git commit"}}},
		{"a commit in the ledger", "s-committer", Fresh{Known: true, Why: []string{"made commit d7101d0"}}},
		{"work on another ticket", "s-elsewhere-editor", Fresh{Known: true, Fresh: true}},
		{"a session jig has no record of", "s-nobody", Fresh{}},
		{"no session", "", Fresh{}},
	} {
		got, err := Freshness(d, ticket, tc.session)
		if err != nil {
			t.Fatalf("%s: Freshness: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: Freshness(%q) = %+v, want %+v", tc.name, tc.session, got, tc.want)
		}
	}
}

// TestFreshnessKnowsASessionByItsJournalLinesAlone: a harness with no hooks
// writes no ledger line, but the commands its session ran leave journal lines
// carrying its id, and those say it is not fresh; with no home to read, a
// session with no journal line is unknown rather than fresh.
func TestFreshnessKnowsASessionByItsJournalLinesAlone(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	if err := journal.Append(d.Store, fx.Ticket, journal.Line{Event: journal.EventClaim, Session: "s-hand", Slice: "a"}); err != nil {
		t.Fatal(err)
	}
	d.Home = ""
	got, err := Freshness(d, fx.Ticket, "s-hand")
	if want := (Fresh{Known: true, Why: []string{"claimed slice a"}}); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Freshness(s-hand) = %+v, %v, want %+v", got, err, want)
	}
	if got, err := Freshness(d, fx.Ticket, "s-other"); err != nil || !reflect.DeepEqual(got, Fresh{}) {
		t.Errorf("Freshness(s-other) = %+v, %v, want a session jig cannot tell", got, err)
	}
}

// TestCommitsCodeReadsTheGitCommandNotItsWords: `git commit` is a commit with
// options before it, a path to git, or a chain; a word "commit" elsewhere is
// not.
func TestCommitsCodeReadsTheGitCommandNotItsWords(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]bool{
		`git commit -m "x"`:                        true,
		`git -C clone -c user.name=x commit -m x`:  true,
		`/usr/bin/git commit`:                      true,
		`git add . && git commit -m x`:             true,
		`git log --grep commit`:                    false,
		`git status && echo commit`:                false,
		`echo git commit`:                          true, // a word run is not told from a command; the advice errs toward a subagent
		`jig verify JIG-1 abc --slice commit`:      false,
		`git`:                                      false,
		``:                                         false,
		`git --git-dir x.git rev-parse HEAD`:       false,
		`git --git-dir x.git commit --allow-empty`: true,
	} {
		if got := commitsCode(cmd); got != want {
			t.Errorf("commitsCode(%q) = %v, want %v", cmd, got, want)
		}
	}
}
