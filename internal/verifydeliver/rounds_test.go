package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// roundStore is a ticket's store with no round recorded, and a way to record
// one by its files.
func roundStore(t *testing.T) (*store.Store, string, func(n int, report, findings string)) {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	record := func(n int, report, findings string) {
		t.Helper()
		dir := gateRoundDir(st, fx.Ticket, n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
		if findings != "" {
			if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), []byte(findings), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return st, fx.Ticket, record
}

func sha40(d string) string { return strings.Repeat(d, 40) }

const roundFindings = `scope: full
reviewed_paths: [a.go, b.go]
findings:
  - {id: r1-f1, file: a.go, line: 3, title: low one, action: note, risk: low, status: noted}
  - {id: r1-f2, file: b.go, line: 4, title: high one, action: fix, risk: high, status: open}
  - {id: r1-f3, file: b.go, line: 9, title: asked, action: ask, risk: medium, status: asked}
`

// TestReadRoundsJoinsTheReportTheFindingsAndTheJournal: a round's reviewer,
// model, independence, range and paths come from its files, the harness from
// the journal's lines of the reviewer's session, and its counts from the
// findings the round raised, high risk first.
func TestReadRoundsJoinsTheReportTheFindingsAndTheJournal(t *testing.T) {
	t.Parallel()
	st, ticket, record := roundStore(t)
	record(1, "round: 1\nverdict: fix-slices\nmodel: claude-opus-5\neffort: high\nreviewed_sha: {repo: "+sha40("2")+"}\nindependent: true\n"+
		"reviewer: {session: s-rev, how: env}\nfix_slices: [fix-1]\n", roundFindings)
	// Round 2 records the decision on r1-f3, which round 1 raised: not its finding.
	record(2, "round: 2\nverdict: clean\nreviewed_sha: {repo: "+sha40("3")+"}\nindependent: unknown\nreviewer: {session: s-rev}\n",
		"scope: delta\nreviewed_paths: [b.go]\nfindings:\n  - {id: r1-f3, file: b.go, line: 9, title: asked, action: ask, risk: medium, status: dismissed}\n")
	if err := journal.Append(st, ticket, journal.Line{Event: journal.EventNote, Session: "s-rev", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}

	got, err := ReadRounds(st, ticket)
	if err != nil {
		t.Fatalf("ReadRounds: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rounds = %+v, want two", got)
	}
	r1 := got[0]
	if r1.Round != 1 || r1.Verdict != RoundFindings || r1.Reviewer.Session != "s-rev" || r1.Model != "claude-opus-5" || r1.Effort != "high" ||
		r1.Harness != "claude" || r1.Independent != IndependentYes || r1.Scope != "full" || r1.Files != 2 || r1.Head != sha40("2") ||
		r1.Base != "" || !reflect.DeepEqual(r1.FixSlices, []string{"fix-1"}) {
		t.Errorf("round 1 = %+v", r1)
	}
	if r1.Fix != 1 || r1.Ask != 1 || r1.Note != 1 {
		t.Errorf("round 1 counts = %d fix, %d ask, %d note, want one each", r1.Fix, r1.Ask, r1.Note)
	}
	if ids := []string{r1.Findings[0].ID, r1.Findings[1].ID, r1.Findings[2].ID}; !reflect.DeepEqual(ids, []string{"r1-f2", "r1-f3", "r1-f1"}) {
		t.Errorf("round 1 findings are %v, want them high risk first", ids)
	}
	r2 := got[1]
	if r2.Verdict != RoundClean || r2.Independent != IndependentUnknown || r2.Scope != "delta" || r2.Base != sha40("2") || r2.Head != sha40("3") {
		t.Errorf("round 2 = %+v, want a clean delta from round 1's head to its own", r2)
	}
	if len(r2.Findings) != 0 || r2.Ask != 0 {
		t.Errorf("round 2 raised %+v, want nothing: r1-f3 is round 1's", r2.Findings)
	}
}

// TestReadRoundsTakesTheBaseFromTheRequestWhileItIsThere: a request in the work
// folder names the base the round was asked to review from, whatever the scope.
func TestReadRoundsTakesTheBaseFromTheRequestWhileItIsThere(t *testing.T) {
	t.Parallel()
	st, ticket, record := roundStore(t)
	record(1, "round: 1\nverdict: clean\nreviewed_sha: {repo: "+sha40("2")+"}\nreviewer: {session: s-rev}\n", "scope: full\nreviewed_paths: []\nfindings: []\n")
	path := reviewJSONPath(st, ticket, 1)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(request string) {
		if err := os.WriteFile(path, []byte(request), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"base_sha":"` + sha40("1") + `","head_sha":"` + sha40("2") + `"}`)
	if got, _ := ReadRounds(st, ticket); got[0].Base != sha40("1") {
		t.Errorf("base = %q, want the request's", got[0].Base)
	}
	// A request for another head, or one that cannot be read, is not this round's.
	write(`{"base_sha":"` + sha40("1") + `","head_sha":"` + sha40("9") + `"}`)
	if got, _ := ReadRounds(st, ticket); got[0].Base != "" {
		t.Errorf("base = %q, want none from a request for another head", got[0].Base)
	}
	write("not json")
	if got, _ := ReadRounds(st, ticket); got[0].Base != "" {
		t.Errorf("base = %q, want none from a request that does not parse", got[0].Base)
	}
}

// TestReadRoundsNamesTheReviewerOfARoundWhoseReportDoesNot: a round that was
// recorded without a reviewer of its own (a scripted one) takes the session,
// model and effort the journal's gate lines for the round name.
func TestReadRoundsNamesTheReviewerOfARoundWhoseReportDoesNot(t *testing.T) {
	t.Parallel()
	st, ticket, record := roundStore(t)
	record(1, "round: 1\nverdict: fix-slices\ntarget_sha: {repo: "+sha40("5")+"}\n", "")
	record(2, "round: 2\nverdict: fix-slices\n", "")
	for _, l := range []journal.Line{
		{Event: journal.EventGateReviewer, Attempt: 1, Session: "s-late", Model: "m-one", Effort: "low"},
		{Event: journal.EventGateRound, Attempt: 1, Session: "s-late", Model: "m-two"},
		{Event: journal.EventGateRound, Attempt: 2},
	} {
		if err := journal.Append(st, ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadRounds(st, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Reviewer.Session != "s-late" || got[0].Model != "m-one" || got[0].Effort != "low" || got[0].Independent != "" || got[0].Head != sha40("5") || got[0].Files != -1 {
		t.Errorf("round 1 = %+v, want the journal's reviewer and the gate's head, with no files", got[0])
	}
	if got[1].Reviewer.Session != "" || got[1].Head != "" || got[1].Verdict != RoundFindings {
		t.Errorf("round 2 = %+v, want no reviewer and no head", got[1])
	}
}

// TestReadRoundsSkipsWhatItCannotRead: one round whose report is gone or is not
// YAML does not hide the others, and a ticket with no round has none.
func TestReadRoundsSkipsWhatItCannotRead(t *testing.T) {
	t.Parallel()
	st, ticket, record := roundStore(t)
	if got, err := ReadRounds(st, ticket); err != nil || got != nil {
		t.Errorf("a ticket with no round reads as %+v, %v", got, err)
	}
	record(1, "round: [", "")
	if err := os.MkdirAll(gateRoundDir(st, ticket, 2), 0o755); err != nil {
		t.Fatal(err)
	}
	record(3, "round: 3\nverdict: clean\nreviewed_sha: {a: "+sha40("1")+", b: "+sha40("2")+"}\n", "scope: full\nreviewed_paths: [x]\nfindings: [")
	got, err := ReadRounds(st, ticket)
	if err != nil || len(got) != 1 || got[0].Round != 3 {
		t.Fatalf("rounds = %+v, %v, want round 3 alone", got, err)
	}
	if got[0].Files != -1 || got[0].Head != "a:"+sha40("1")+"+b:"+sha40("2") {
		t.Errorf("round 3 = %+v, want no files (its findings file is not YAML) and a head per repo", got[0])
	}
}
