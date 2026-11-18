package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// lastRunRig is statesRig with the ticket's own branch (the one `jig next`
// names) at the clone's HEAD, so `jig status` reads the commits on it, and the
// store opened.
func lastRunRig(t *testing.T) (*fixture.Fixture, env, *store.Store, func()) {
	t.Helper()
	fx, e := statesRig(t)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	next, code := runMain(t, e, "", "next", fx.Ticket)
	b := regexp.MustCompile(`(?m)^\s*branch:\s*(\S+)`).FindStringSubmatch(next)
	if code != 0 || b == nil {
		t.Fatalf("next: exit %d, no branch in %q", code, next)
	}
	moveBranch := func() {
		t.Helper()
		if _, err := gitx.Run(fx.RepoDir, "branch", "-f", b[1], "HEAD"); err != nil {
			t.Fatal(err)
		}
	}
	moveBranch()
	return fx, e, st, moveBranch
}

func appendLine(t *testing.T, st *store.Store, ticket string, l journal.Line) {
	t.Helper()
	if err := journal.Append(st, ticket, l); err != nil {
		t.Fatal(err)
	}
}

// TestStatusLastRunsShowResultAndCovers: the last_runs table tells each
// slice's last witnessed run with its result and whether it covers the
// slice's last commit: yes at that commit, the commit that is the last when a
// later one was made, and "not checked" when the branch cannot be read.
func TestStatusLastRunsShowResultAndCovers(t *testing.T) {
	t.Parallel()
	fx, e, st, moveBranch := lastRunRig(t)
	first := commitWithTrailer(t, fx, "one.txt", "a")
	moveBranch()
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "a", Commit: first, Outcome: "fail", Attempt: 1, Seconds: 9, Summary: "FAIL TestX"})

	row := func(out string) string {
		t.Helper()
		got := block(out, "last_runs[")
		if len(got) != 2 {
			t.Fatalf("last_runs block = %v\n%s", got, out)
		}
		return strings.TrimSpace(got[1])
	}
	out, code := runMain(t, e, "", "status", fx.Ticket)
	if code != 0 {
		t.Fatalf("jig status: exit %d, output %q", code, out)
	}
	if got, want := row(out), "a,"+first[:12]+",fail,yes,9,"; !strings.HasPrefix(got, want) {
		t.Errorf("last run of a failing run at the slice's last commit = %q, want it to start %q", got, want)
	}
	if !strings.Contains(strings.Join(block(out, "last_runs["), "\n"), "{slice,commit,result,covers,seconds,cpu_seconds,peak_memory,basis}") {
		t.Errorf("the table header lacks result and covers after commit:\n%s", out)
	}

	// A commit after the run: the run covers the commit before it.
	second := commitWithTrailer(t, fx, "two.txt", "a")
	moveBranch()
	out, _ = runMain(t, e, "", "status", fx.Ticket)
	if got, want := row(out), "a,"+first[:12]+",fail,\"no: slice's last commit is "+second[:12]+"\",9,"; !strings.HasPrefix(got, want) {
		t.Errorf("last run before a later commit = %q, want it to start %q", got, want)
	}

	// With no start sha there is no branch to read.
	if err := os.Remove(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "start.fixture-repo.sha")); err != nil {
		t.Fatal(err)
	}
	out, _ = runMain(t, e, "", "status", fx.Ticket)
	if got, want := row(out), "a,"+first[:12]+",fail,not checked,9,"; !strings.HasPrefix(got, want) {
		t.Errorf("last run with no branch read = %q, want it to start %q", got, want)
	}
}

// TestAnUncoveredRunIsTheLastRunAndCoversNothing: a run on a dirty tree is the
// last thing witnessed, so it is the slice's last run and says why it covers
// nothing, and the commit's state stays what the covering run before it made.
func TestAnUncoveredRunIsTheLastRunAndCoversNothing(t *testing.T) {
	t.Parallel()
	fx, e, st, moveBranch := lastRunRig(t)
	sha := commitWithTrailer(t, fx, "one.txt", "a")
	moveBranch()
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "a", Commit: sha, Outcome: "fail", Attempt: 1, Seconds: 4, Summary: "FAIL TestX"})
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "a", Commit: sha, Outcome: "pass", Attempt: 2, Seconds: 3, Uncovered: journal.UncoveredDirtyBefore})

	out, code := runMain(t, e, "", "status", fx.Ticket)
	if code != 0 {
		t.Fatalf("jig status: exit %d, output %q", code, out)
	}
	runs := block(out, "last_runs[")
	if len(runs) != 2 || !strings.HasPrefix(strings.TrimSpace(runs[1]), "a,"+sha[:12]+",pass,\"no: tree dirty before\",3,") {
		t.Errorf("last_runs = %v, want the uncovered pass as a's last run with its reason", runs)
	}
	states := strings.Join(block(out, "verification_states["), "\n")
	if !strings.Contains(states, "failed: FAIL TestX") {
		t.Errorf("the commit's state changed with an uncovered pass:\n%s", out)
	}
}

// TestHereJSONSlicesCarryTheirLastRun: each entry of the slices has a last_run
// with the commit, result, covers and seconds when the slice has an oracle
// line, and none when it has not; the text table shows covers beside the state.
func TestHereJSONSlicesCarryTheirLastRun(t *testing.T) {
	t.Parallel()
	fx, e, st, _ := lastRunRig(t)
	first := commitWithTrailer(t, fx, "one.txt", "a")
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "a", Commit: first, Outcome: "pass", Attempt: 1, Seconds: 12})
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "b", Commit: first, Outcome: "fail", Attempt: 1, Cause: journal.CauseTimeout, Seconds: 30})
	second := commitWithTrailer(t, fx, "two.txt", "b")

	out, code := runMain(t, e, "", "here", "--json")
	if code != 0 {
		t.Fatalf("jig here --json: exit %d, output %q", code, out)
	}
	var got struct {
		State struct {
			Slices []struct {
				ID      string `json:"id"`
				LastRun *struct {
					Commit  string `json:"commit"`
					Result  string `json:"result"`
					Covers  string `json:"covers"`
					Seconds int    `json:"seconds"`
				} `json:"last_run"`
			} `json:"slices"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	by := map[string]int{}
	for i, s := range got.State.Slices {
		by[s.ID] = i
	}
	a, b, c := got.State.Slices[by["a"]], got.State.Slices[by["b"]], got.State.Slices[by["c"]]
	if a.LastRun == nil || a.LastRun.Commit != first || a.LastRun.Result != "pass" || a.LastRun.Covers != "yes" || a.LastRun.Seconds != 12 {
		t.Errorf("a's last_run = %+v, want a pass of %s that covers", a.LastRun, first)
	}
	if b.LastRun == nil || b.LastRun.Result != "timed out" || b.LastRun.Covers != "no: slice's last commit is "+second[:12] {
		t.Errorf("b's last_run = %+v, want a timed out run of an earlier commit than %s", b.LastRun, second[:12])
	}
	if c.LastRun != nil {
		t.Errorf("c has no oracle line but last_run = %+v", c.LastRun)
	}

	text, _ := runMain(t, e, "", "here")
	if !strings.Contains(text, "{id,state,covers,attempts,blocked_by,question}") || !strings.Contains(text, ",yes,") {
		t.Errorf("the text slices table does not show covers beside the state:\n%s", text)
	}
}

// TestHereLastRunsCostOneGitLog: laying every slice's last run on `jig here`
// adds no git process however many slices have one: the branch is read once.
func TestHereLastRunsCostOneGitLog(t *testing.T) {
	fx, e := startPathRig(t)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	startPath := st.StartSHAPath(fx.Ticket, "fixture-repo")
	_ = os.Remove(startPath)
	run := func() int {
		processes, _ := measureWork(t, func() {
			if out, code := runMain(t, e, "", "here"); code != 0 {
				t.Fatalf("jig here: exit %d, output %q", code, out)
			}
		})
		return processes
	}
	without := run()

	start, err := gitx.RevParse(fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStartSHA(fx.Ticket, "fixture-repo", start); err != nil {
		t.Fatal(err)
	}
	for _, slice := range []string{"a", "b", "c", "d"} {
		sha := commitWithTrailer(t, fx, slice+".txt", slice)
		appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: slice, Commit: sha, Outcome: "pass", Attempt: 1, Seconds: 1})
	}
	with := run()
	t.Logf("jig here: %d git processes without a branch to read, %d with four slices that ran", without, with)
	if with > without+1 {
		t.Errorf("reading the last runs of four slices cost %d more git processes, want at most one", with-without)
	}
}
