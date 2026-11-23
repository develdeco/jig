package main

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// suiteRowsOf are the rows of the suite table in a status's text, one string
// each, as printed.
func suiteRowsOf(t *testing.T, text string) []string {
	t.Helper()
	var rows []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "suite["):
			in = true
		case in && strings.HasPrefix(line, "  "):
			rows = append(rows, strings.TrimSpace(line))
		default:
			in = false
		}
	}
	return rows
}

// TestStatusOfAContainerCarriesItsNeed: the status of an issue and of a
// multi-task prints the ticket's body whole, in the text and in the object the
// text is made from; a multi-task prints no suite; a task's status has no need.
func TestStatusOfAContainerCarriesItsNeed(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	r.put(t, "tickets/TK-1/ticket.yaml", "schema_version: 1\ntitle: Ticket TK-1\nkind: issue\nbody: |\n  The first line of the need.\n  The second, with a colon: here.\n")
	r.put(t, "tickets/TK-4/ticket.yaml", "schema_version: 1\ntitle: Ticket TK-4\nkind: multi-task\nbody: |\n  What the multi-task is for.\n")
	for id, want := range map[string]string{
		"TK-1": `need: "The first line of the need.\nThe second, with a colon: here."`,
		"TK-4": `need: What the multi-task is for.`,
	} {
		out, code := r.run(t, "status", id)
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("jig status %s: exit %d, want the line %q\n%s", id, code, want, out)
		}
	}
	if out, _ := r.run(t, "status", "TK-4"); strings.Contains(out, "suite[") {
		t.Errorf("a multi-task prints a suite:\n%s", out)
	}
	if out, _ := r.run(t, "status", "TK-2"); strings.Contains(out, "need:") {
		t.Errorf("a task's status prints a need:\n%s", out)
	}

	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := readTicketData(r.st, "TK-1")
	if err != nil {
		t.Fatal(err)
	}
	n, err := nextNotATask(r.st, data, store.KindIssue).withPlanFacts(r.st, cfg, project.MachineProject{}, data)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Need string `json:"need"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Need != "The first line of the need.\nThe second, with a colon: here." {
		t.Errorf("the object's need = %q, %v", got.Need, err)
	}
	// jig next carries neither.
	if out, _ := r.run(t, "next", "TK-1", "--json"); strings.Contains(out, `"need"`) || strings.Contains(out, `"suite"`) {
		t.Errorf("jig next --json names the plan's facts:\n%s", out)
	}
}

// TestStatusOfAnIssueCarriesItsSuiteByRepo: each standing of a repo's suite is a
// row of the status, equal to what land.SuiteOf reads at the same tip, and a
// repo with no clone on this machine reads unrun with its sentence.
func TestStatusOfAnIssueCarriesItsSuiteByRepo(t *testing.T) {
	check := func(t *testing.T, s *suitePublishRig, standing string) {
		t.Helper()
		out, code := s.run(t, "status", "TK-1")
		if code != 0 {
			t.Fatalf("jig status TK-1: exit %d\n%s", code, out)
		}
		d := land.Deps{Store: s.st, Cfg: mustConfig(t, s.root), Clones: map[string]string{"app": s.clone}}
		c, err := land.ContainerOf(d, "TK-1")
		if err != nil {
			t.Fatal(err)
		}
		want, err := land.SuiteOf(d, c)
		if err != nil || len(want) != 1 {
			t.Fatalf("land.SuiteOf = %+v, %v", want, err)
		}
		if want[0].Standing != standing {
			t.Fatalf("the rig stands at %q, want %q", want[0].Standing, standing)
		}
		rows := suiteRowsOf(t, out)
		if len(rows) != 1 {
			t.Fatalf("jig status TK-1 prints %d suite rows, want one:\n%s", len(rows), out)
		}
		row := strings.Split(rows[0], ",")
		w := want[0]
		if row[0] != w.Repo || row[1] != w.Standing || row[3] != orDash(w.Tip) || row[2] != orDash(w.Command) {
			t.Errorf("row %v differs from land.SuiteOf %+v", row, w)
		}
		switch standing {
		case land.SuitePass, land.SuiteFail:
			if row[4] != strconv.Itoa(w.Run.Seconds) || row[5] != axi.Quote(orDash(w.Run.Signature)) {
				t.Errorf("row %v does not carry the run %+v", row, w.Run)
			}
		case land.SuiteWaived:
			if row[6] != orDash(w.Waiver.Summary) {
				t.Errorf("row %v does not carry the waiver %+v", row, w.Waiver)
			}
		}
	}

	t.Run("pass", func(t *testing.T) {
		s := newSuitePublishRig(t, suiteManifestYAML)
		s.record(journal.Line{Event: journal.EventFinalOracle, Commit: s.tip(), Outcome: "pass", Seconds: 41, Command: "go test ./..."})
		check(t, s, land.SuitePass)
	})
	t.Run("fail", func(t *testing.T) {
		s := newSuitePublishRig(t, suiteManifestYAML)
		s.record(journal.Line{Event: journal.EventFinalOracle, Commit: s.tip(), Outcome: "fail", Seconds: 9, Signature: "final:boom", Command: "go test ./..."})
		check(t, s, land.SuiteFail)
	})
	t.Run("waived", func(t *testing.T) {
		s := newSuitePublishRig(t, suiteManifestYAML)
		s.record(journal.Line{Event: journal.EventSuiteWaived, Commit: s.tip(), Summary: "ci runs it", Provenance: "person"})
		check(t, s, land.SuiteWaived)
	})
	t.Run("undeclared", func(t *testing.T) {
		s := newSuitePublishRig(t, "")
		check(t, s, land.SuiteUndeclared)
	})
	t.Run("unrun", func(t *testing.T) {
		s := newSuitePublishRig(t, suiteManifestYAML)
		check(t, s, land.SuiteUnrun)
	})
	t.Run("no clone", func(t *testing.T) {
		r := containerRig(t)
		out, code := r.run(t, "status", "TK-1")
		rows := suiteRowsOf(t, out)
		if code != 0 || len(rows) != 1 || !strings.HasPrefix(rows[0], "app,unrun,") || !strings.Contains(rows[0], "has no clone on this machine") {
			t.Errorf("jig status TK-1 (no clone): exit %d, rows %q, want one unrun row with its sentence\n%s", code, rows, out)
		}
	})
}

func mustConfig(t *testing.T, root string) project.Config {
	t.Helper()
	cfg, err := project.LoadStoreConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestSuiteRowNamesTheTasksAddedAfterAFailedRun: a task begun after a failed
// final run is a fix of that run, named on its row; one begun before names none;
// a task with no journal line is a fix of the last run when that failed; a later
// pass at the new tip keeps the failed run as a row of its own tip.
func TestSuiteRowNamesTheTasksAddedAfterAFailedRun(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	base := time.Now().UTC().Add(time.Hour)
	at := func(min int) string { return base.Add(time.Duration(min) * time.Minute).Format(time.RFC3339Nano) }

	first := s.tip()
	s.record(journal.Line{TS: at(0), Event: journal.EventFinalOracle, Commit: first, Outcome: "fail", Seconds: 9, Signature: "final:boom", Command: "go test ./..."})

	// TK-3 and TK-4 are graduated after the failure, TK-4 with nothing begun.
	s.ticket(t, "TK-3", "", false)
	s.ticket(t, "TK-4", "", false)
	s.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-3\n    title: Three\n  - id: TK-4\n    title: Four\n", "")
	if err := journal.Append(s.st, "TK-3", journal.Line{TS: at(2), Event: journal.EventClaim, Slice: "a"}); err != nil {
		t.Fatal(err)
	}

	rows := func() []nextSuiteRow {
		t.Helper()
		cfg := mustConfig(t, s.root)
		rec, err := s.st.ReadTicket("TK-1")
		if err != nil {
			t.Fatal(err)
		}
		got, err := suiteRows(s.st, cfg, project.MachineProject{Clones: map[string]string{"app": s.clone}}, "TK-1", rec)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := rows()
	if len(got) != 1 || got[0].Standing != land.SuiteFail || !reflect.DeepEqual(got[0].Fixes, []string{"TK-3", "TK-4"}) {
		t.Fatalf("rows after the failed run = %+v, want one failed row with fixes TK-3 and TK-4 (TK-2 began before the run)", got)
	}
	out, _ := s.run(t, "status", "TK-1")
	if r := suiteRowsOf(t, out); len(r) != 1 || !strings.Contains(r[0], ",TK-3+TK-4,") {
		t.Errorf("the status rows = %q, want the fixes TK-3+TK-4", r)
	}

	// The fix landed: the tip moved and the suite passed there. The failed run
	// stays as the row of its own tip, naming the task begun before the pass;
	// the pass names none, and TK-4, begun nothing, is not a fix of a pass.
	second := s.moveTip("")
	s.record(journal.Line{TS: at(5), Event: journal.EventFinalOracle, Commit: second, Outcome: "pass", Seconds: 11, Command: "go test ./..."})
	got = rows()
	if len(got) != 2 {
		t.Fatalf("rows after the pass = %+v, want the failed run and the pass", got)
	}
	if got[0].Standing != land.SuiteFail || got[0].Tip != first || !reflect.DeepEqual(got[0].Fixes, []string{"TK-3"}) || got[0].Signature != "final:boom" {
		t.Errorf("the earlier row = %+v, want the failed run at %s with the fix TK-3", got[0], first)
	}
	if got[1].Standing != land.SuitePass || got[1].Tip != second || len(got[1].Fixes) != 0 {
		t.Errorf("the standing row = %+v, want the pass at %s with no fixes", got[1], second)
	}

	// A pass at an earlier tip is no row: only a failure is history.
	third := s.moveTip("")
	s.record(journal.Line{TS: at(8), Event: journal.EventFinalOracle, Commit: third, Outcome: "pass", Seconds: 12, Command: "go test ./..."})
	got = rows()
	if len(got) != 2 || got[0].Tip != first || got[1].Tip != third {
		t.Errorf("rows after a second pass = %+v, want the failed run at %s and the pass at %s", got, first, third)
	}
}

// TestContainerDigestCarriesItsOwnPullRequest: an issue's and a multi-task's own
// pull request is a row of its digest in the roster, the issue's into the
// target and the multi-task's into the issue's branch, in the state the merge
// facts give.
func TestContainerDigestCarriesItsOwnPullRequest(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	const issuePR, multiPR = "https://github.com/acme/app/pull/31", "https://github.com/acme/app/pull/32"
	for id, url := range map[string]string{"TK-1": issuePR, "TK-4": multiPR} {
		if err := journal.Append(r.st, id, journal.Line{Event: journal.EventPr, Outcome: "opened", URL: url}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := mustConfig(t, r.root)
	fleet, err := nextForStore(r.st, cfg, project.MachineProject{}, issueMerges{"TK-1": openInto(issuePR), "TK-4": mergedInto(multiPR)})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]nextDigest{}
	for _, row := range fleet.Tickets {
		byID[row.ID] = *row.Digest
	}
	rec, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	issueBranch, err := r.st.ResolveTicketBranch("TK-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := []nextDigestPR{{Repo: "app", Number: 31, URL: issuePR, State: "open", Target: "main"}}; !reflect.DeepEqual(byID["TK-1"].PRs, want) {
		t.Errorf("the issue's pull requests = %+v, want %+v", byID["TK-1"].PRs, want)
	}
	if want := []nextDigestPR{{Repo: "app", Number: 32, URL: multiPR, State: "merged", Target: issueBranch}}; !reflect.DeepEqual(byID["TK-4"].PRs, want) {
		t.Errorf("the multi-task's pull requests = %+v, want %+v", byID["TK-4"].PRs, want)
	}
	if got := byID["TK-6"].PRs; got == nil || len(got) != 0 {
		t.Errorf("a ticket with none has %+v, want an empty list", got)
	}
}
