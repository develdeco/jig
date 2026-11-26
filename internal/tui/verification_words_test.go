package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// verifyStatus is a status of JIG-1 whose verification_states table holds what
// internal/phase tells of each commit the lines name, as `jig status` prints it:
// the state cell carries a failed run's FAIL line or a could-not-run summary.
func verifyStatus(t *testing.T, envs []events.Envelope, shas ...string) (string, []phase.CommitVerification) {
	t.Helper()
	var lines []journal.Line
	var commits []phase.CommitRef
	for _, e := range envs {
		lines = append(lines, e.Line)
	}
	for _, sha := range shas {
		commits = append(commits, phase.CommitRef{SHA: sha, Slice: "a"})
	}
	rows := phase.DeriveVerifications(phase.VerifyInput{Ticket: "JIG-1", Lines: lines, Commits: commits, BranchKnown: true})
	var table [][]string
	for _, r := range rows {
		if !r.State.Unclear() {
			continue
		}
		told := r.Told()
		switch {
		case r.State == phase.VerifyFailed && r.FailLine != "" && told == string(r.State):
			told = "failed: " + r.FailLine
		case r.State == phase.VerifyCouldNotRun && r.Error != "":
			told = "could not run: " + r.Error
		}
		table = append(table, []string{r.Slice, r.Commit[:12], told, r.Clear})
	}
	status := slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"}) +
		axi.Table("verification_states", []string{"slice", "commit", "state", "clear"}, table)
	return status, rows
}

// told is how phase words a commit's state in the status's table.
func told(r phase.CommitVerification) string {
	switch {
	case r.State == phase.VerifyFailed && r.FailLine != "" && r.Told() == string(r.State):
		return "failed: " + r.FailLine
	case r.State == phase.VerifyCouldNotRun:
		return "could not run: " + r.Error
	}
	return r.Told()
}

// TestCommitStatesReadAsThePhaseWritesThem: the build screen and the slice
// screen word each commit's verification state as internal/phase does, with the
// command that clears it, and draw it in the slice's icons.
func TestCommitStatesReadAsThePhaseWritesThem(t *testing.T) {
	t.Parallel()
	a, b, c, d, e := sha('a'), sha('b'), sha('c'), sha('d'), sha('e')
	envs := []events.Envelope{
		jl(at(0, 1), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", a, "", "pending", "", "")),
		jl(at(0, 2), "JIG-1", "a", journal.EventOracle, with("", b, "fail", "--- FAIL: TestGreeting (0.00s)", "", "")),
		jl(at(0, 3), "JIG-1", "a", journal.EventOracle, with("", c, "fail", "signal: interrupt", "", ""), func(l *journal.Line) { l.Cause = journal.CauseStopped }),
		jl(at(0, 4), "JIG-1", "a", journal.EventOracle, with("", d, "fail", "killed", "", ""), func(l *journal.Line) { l.Cause = journal.CauseTimeout }),
		jl(at(0, 5), "JIG-1", "a", journal.EventVerifyError, with("", e, "", "no worktree", "", "")),
	}
	// A screen lists the latest three commits of a slice, so each group is a
	// ticket of its own, with the lines of its commits.
	for _, group := range []struct {
		shas  []string
		icons map[string]string
	}{
		{[]string{a, b, c}, map[string]string{a: StateStalled, b: StateFail, c: StateQueued}},
		{[]string{d, e}, map[string]string{d: StateQueued, e: StateQueued}},
	} {
		var mine []events.Envelope
		for _, env := range envs {
			for _, sha := range group.shas {
				if env.Line.Commit == sha {
					mine = append(mine, env)
				}
			}
		}
		status, rows := verifyStatus(t, mine, group.shas...)
		if len(rows) != len(group.shas) {
			t.Fatalf("phase judged %d commits, want %d", len(rows), len(group.shas))
		}

		g := newRig(t, "JIG-1")
		g.r.answer("status JIG-1 --store /s", status, 0)
		g.feed(mine...).press("enter")
		if g.m.Screen() != "build" {
			t.Fatalf("on %s, want build", g.m.Screen())
		}
		build := pageText(g)
		g.press("enter")
		if g.m.Screen() != "slice" {
			t.Fatalf("on %s, want slice:\n%s", g.m.Screen(), build)
		}
		slice := pageText(g)

		for name, text := range map[string]string{"build": build, "slice": slice} {
			for _, r := range rows {
				for _, want := range []string{told(r), r.Clear} {
					if !strings.Contains(text, want) {
						t.Errorf("the %s screen lacks phase's %q for %s:\n%s", name, want, short(r.Commit), text)
					}
				}
			}
		}
		// The icons are the slice's: held is stalled, failed is failed, the
		// rest are drawn back.
		for sha, icon := range group.icons {
			if want := lookOf(icon).Icon + " " + short(sha); !strings.Contains(build, want) {
				t.Errorf("the build screen lacks %q:\n%s", want, build)
			}
		}
	}
}

// TestAStoppedRunIsNotShownAsAFailure: a run jig was told to stop, or a timeout
// killed, is worded as one that judged nothing, and neither it nor its commit is
// drawn as the code failing; one that failed still is. Before the status is
// read the commit says reading.
func TestAStoppedRunIsNotShownAsAFailure(t *testing.T) {
	t.Parallel()
	b, c := sha('b'), sha('c')
	envs := []events.Envelope{
		jl(at(0, 2), "JIG-1", "a", journal.EventOracle, with("", b, "fail", "--- FAIL: TestGreeting (0.00s)", "", "")),
		jl(at(0, 3), "JIG-1", "a", journal.EventOracle, with("", c, "fail", "signal: interrupt", "", ""), func(l *journal.Line) { l.Cause = journal.CauseStopped }),
	}
	status, _ := verifyStatus(t, envs, b, c)

	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", status, 0)
	g.feed(envs...).press("enter")
	text := pageText(g)
	if !strings.Contains(text, "stopped, not counted") {
		t.Errorf("a stopped run is not worded as one that judged nothing:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "stopped, not counted") && strings.Contains(line, lookOf(StateStalled).Icon) {
			t.Errorf("a stopped run is drawn as a failure: %q", line)
		}
		if strings.Contains(line, "failed in") && strings.Contains(line, short(c)) {
			t.Errorf("the stopped run reads as failed: %q", line)
		}
	}
	if !strings.Contains(text, "failed: --- FAIL: TestGreeting (0.00s)") {
		t.Errorf("a failed run is not worded as failing with its line:\n%s", text)
	}

	// Before the status is read, the records say reading and word no state.
	quiet := newRig(t, "JIG-1")
	quiet.feed(envs...)
	tk := quiet.m.w.tickets["JIG-1"]
	tk.status = nil
	p := page{lines: quiet.m.detailLines(quiet.m.buildDetail(tk, sliceRow{ID: "a"}, statusSlice{}), 80)}
	if got := pagesText(p); !strings.Contains(got, readingText) || strings.Contains(got, "failed:") {
		t.Errorf("records before the status is read:\n%s", got)
	}
}
