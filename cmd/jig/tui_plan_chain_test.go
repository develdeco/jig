package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// frameLineWith is the first line of frame holding part, "" when none does.
func frameLineWith(frame, part string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, part) {
			return l
		}
	}
	return ""
}

// screenWith waits for the last screen the pipe printed to hold want.
func screenWith(t *testing.T, out *tuiOut, want string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if frame := lastScreen(out.String()); strings.Contains(frame, want) {
			return frame
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the last screen never held %q; it was:\n%s", want, lastScreen(out.String()))
	return ""
}

// TestTUIPlanEqualsStatusAndNext: a store shaped as an issue's own run, an issue
// whose four tasks landed into its branch in a repo that declares no suite, its
// pull request into main and its landing recorded, and a second issue whose
// suite failed and whose fix task waits. `jig tui <issue>` on a pipe prints the
// plan, and the four tasks with where each landed, the branch, the suite, the
// pull request by number and target and the landing in its frames are what `jig
// status` and `jig next --json` say of the same store; a task's screen fed c
// prints the plan again.
func TestTUIPlanEqualsStatusAndNext(t *testing.T) {
	s := newSuitePublishRig(t, "")
	branch := s.branch

	// Four tasks, each published into the issue's branch and landed there.
	tasks := []string{"TK-2", "TK-3", "TK-4", "TK-5"}
	pulls := []fixture.GhPull{}
	line := func(id string, l journal.Line) {
		t.Helper()
		if err := journal.Append(s.st, id, l); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range tasks {
		n := 2 + i
		url := "https://github.example/owner/repo/pull/" + strconv.Itoa(n)
		if id != "TK-2" {
			s.ticket(t, id, "", true)
			line(id, journal.Line{Event: journal.EventPr, Into: "TK-1", URL: url})
			line(id, journal.Line{Event: journal.EventPublishDone, Into: "TK-1"})
		}
		s.green(t, id)
		line(id, journal.Line{Event: journal.EventLanded, Into: "TK-1", Base: branch, URL: url, Commit: "c0ffee" + strconv.Itoa(n)})
		pulls = append(pulls, fixture.GhPull{Number: n, Base: branch, State: "closed", Merged: true, MergeCommit: "c0ffee" + strconv.Itoa(n)})
	}
	s.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-3\n    title: Three\n  - id: TK-4\n    title: Four\n  - id: TK-5\n    title: Five\n", "")

	// The issue's own pull request into main, merged and landed.
	const issuePR = "https://github.example/owner/repo/pull/9"
	line("TK-1", journal.Line{Event: journal.EventPr, URL: issuePR})
	line("TK-1", journal.Line{Event: journal.EventPublishDone})
	line("TK-1", journal.Line{Event: journal.EventLanded, Base: "main", URL: issuePR, Commit: "c0ffee9"})
	pulls = append(pulls, fixture.GhPull{Number: 9, Base: "main", State: "closed", Merged: true, MergeCommit: "c0ffee9"})

	// A second issue whose repo declares a suite that failed at its tip, one task
	// landed into its branch, and a fix task nobody has begun.
	s.ticket(t, "TK-10", "kind: issue\nbody: |\n  The second issue's need.\n", false)
	s.ticket(t, "TK-12", "", true)
	s.ticket(t, "TK-11", "", false)
	s.chart(t, "second", "issue: TK-10\ntickets:\n  - id: TK-12\n    title: Twelve\n  - id: TK-11\n    title: Eleven\n", "")
	rec, err := s.st.ReadTicket("TK-10")
	if err != nil {
		t.Fatal(err)
	}
	branch10, err := s.st.ResolveTicketBranch("TK-10", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	s.green(t, "TK-12")
	line("TK-12", journal.Line{Event: journal.EventPr, Into: "TK-10", URL: "https://github.example/owner/repo/pull/12"})
	line("TK-12", journal.Line{Event: journal.EventPublishDone, Into: "TK-10"})
	line("TK-12", journal.Line{Event: journal.EventLanded, Into: "TK-10", Base: branch10, URL: "https://github.example/owner/repo/pull/12", Commit: "c0ffee12"})
	pulls = append(pulls, fixture.GhPull{Number: 12, Base: branch10, State: "closed", Merged: true, MergeCommit: "c0ffee12"})
	if err := os.MkdirAll(filepath.Join(s.clone, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.clone, ".claude", "jig.yaml"), []byte(suiteManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "suite"}} {
		if _, err := gitx.Run(s.clone, args...); err != nil {
			t.Fatal(err)
		}
	}
	tip10, err := gitx.RevParse(s.clone, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(s.clone, "update-ref", "refs/remotes/origin/"+branch10, tip10); err != nil {
		t.Fatal(err)
	}
	line("TK-10", journal.Line{Event: journal.EventFinalOracle, Commit: tip10, Outcome: "fail", Seconds: 9, Signature: "final:boom", Command: "go test ./..."})
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, pulls...))

	e := s.env()
	scope := []string{"--store", s.root}

	// What the verbs say.
	var next struct {
		Issue struct {
			Branch string `json:"branch"`
		} `json:"issue"`
	}
	out, code := runMain(t, e, "", append([]string{"next", "plan", "--json"}, scope...)...)
	if err := json.Unmarshal([]byte(out), &next); code != 0 || err != nil || next.Issue.Branch != branch {
		t.Fatalf("jig next plan --json: exit %d, %v, the issue's branch %q, want %q\n%s", code, err, next.Issue.Branch, branch, out)
	}
	var roster nextStore
	out, code = runMain(t, e, "", append([]string{"next", "--json"}, scope...)...)
	if err := json.Unmarshal([]byte(out), &roster); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
	}
	digests := map[string]*nextDigest{}
	for _, row := range roster.Tickets {
		digests[row.ID] = row.Digest
	}
	if prs := digests["TK-1"].PRs; len(prs) != 1 || prs[0].Number != 9 || prs[0].Target != "main" {
		t.Fatalf("the roster's pull requests of TK-1 = %+v, want #9 into main", prs)
	}
	status, code := runMain(t, e, "", append([]string{"status", "TK-1"}, scope...)...)
	if rows := suiteRowsOf(t, status); code != 0 || len(rows) != 1 || !strings.HasPrefix(rows[0], "app,undeclared,") {
		t.Fatalf("jig status TK-1: exit %d, suite rows %q, want one undeclared\n%s", code, rows, status)
	}
	status10, code := runMain(t, e, "", append([]string{"status", "TK-10"}, scope...)...)
	rows10 := suiteRowsOf(t, status10)
	if code != 0 || len(rows10) != 1 || !strings.HasPrefix(rows10[0], "app,fail,") || !strings.Contains(rows10[0], ",TK-11,") {
		t.Fatalf("jig status TK-10: exit %d, suite rows %q, want one failed with the fix task TK-11\n%s", code, rows10, status10)
	}

	// The plan of the issue whose tasks landed.
	keys, plan, wait := startTUI(t, e, append([]string{"TK-1"}, scope...)...)
	frame := screenWith(t, plan, "landing")
	// The status of the issue is read after the roster: its need and suite come
	// last.
	frame = screenWith(t, plan, "landed · into main at")
	frame = screenWith(t, plan, "none recorded")
	frame = screenWith(t, plan, "the issue's branch at")
	last := -1
	for _, id := range tasks {
		row := frameLineWith(frame, id+" ")
		if row == "" || strings.Index(frame, row) < last || !strings.Contains(row, "landed · into the issue's branch at") {
			t.Errorf("the plan's row of %s is %q, want it in the chart's order, landed into the issue's branch:\n%s", id, row, frame)
		}
		last = strings.Index(frame, row)
	}
	if !regexp.MustCompile(`branch\s+` + regexp.QuoteMeta(next.Issue.Branch)).MatchString(frame) {
		t.Errorf("the plan does not name the branch %s of jig next:\n%s", next.Issue.Branch, frame)
	}
	if !regexp.MustCompile(`suite\s+none recorded`).MatchString(frame) {
		t.Errorf("the plan does not say the suite is none recorded, as jig status says:\n%s", frame)
	}
	if !regexp.MustCompile(`PR\s+PR #9 into main · merged`).MatchString(frame) {
		t.Errorf("the plan does not say the pull request #9 into main, as jig next --json says:\n%s", frame)
	}
	if !regexp.MustCompile(`landing\s+landed · into main at \d\d:\d\d:\d\d`).MatchString(frame) {
		t.Errorf("the plan does not say the landing:\n%s", frame)
	}
	if !strings.Contains(frame, "4 of 4 landed") {
		t.Errorf("the plan does not count the four tasks landed:\n%s", frame)
	}

	// A task's screen, and c from it back to the plan.
	keys.Write([]byte("\r"))
	screenWith(t, plan, "› TK-2")
	keys.Write([]byte("c"))
	if again := screenWith(t, plan, "› plan"); !strings.Contains(again, "Need") || !strings.Contains(again, "Tasks") {
		t.Errorf("c did not print the plan again:\n%s", again)
	}
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui TK-1 exited non-zero\n%s", plan.String())
	}

	// The plan of the issue whose suite failed.
	keys, plan, wait = startTUI(t, e, append([]string{"TK-10"}, scope...)...)
	frame = screenWith(t, plan, "failed at")
	tip7 := strings.Split(rows10[0], ",")[3][:7]
	if !strings.Contains(frame, "failed at "+tip7+": final:boom · fix: TK-11") {
		t.Errorf("the plan does not say the failed suite and its fix task as jig status does:\n%s", frame)
	}
	if row := frameLineWith(frame, "TK-11 "); row == "" {
		t.Errorf("the plan has no row for the fix task:\n%s", frame)
	}
	if !strings.Contains(frame, "added after the failed suite at "+tip7) {
		t.Errorf("the fix task is not marked:\n%s", frame)
	}
	if !strings.Contains(frame, "The second issue's need.") {
		t.Errorf("the plan has not the need jig status says:\n%s", frame)
	}
	if row := frameLineWith(frame, " PR "); !strings.Contains(row, "not opened yet · waits for 1 task") {
		t.Errorf("the pull request of the second issue: %q\n%s", row, frame)
	}
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui TK-10 exited non-zero\n%s", plan.String())
	}
}
