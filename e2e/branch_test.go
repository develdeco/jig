package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestEnvPauseDeferCI exercises a slice whose env class fails to come up
// with an "unavailable: defer-ci" policy: jig run should pause (exit 2) and
// journal the env-unavailable event with that policy as its outcome.
func TestEnvPauseDeferCI(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{EnvFail: true})

	r := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r.Code != 2 {
		t.Fatalf("jig run exit = %d, want 2 (paused)\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	lines := readJournal(t, fx, fx.Ticket)
	l, ok := findJournalLine(lines, "env-unavailable", "d", "")
	if !ok {
		t.Fatalf("no env-unavailable journal line for slice d; journal:\n%+v", lines)
	}
	if l.Outcome != "defer-ci" {
		t.Fatalf("env-unavailable outcome = %q, want %q", l.Outcome, "defer-ci")
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ds, err := st.ReadSliceState(fx.Ticket, "d")
	if err != nil {
		t.Fatalf("ReadSliceState(d): %v", err)
	}
	if ds.State != "env-blocked" {
		t.Fatalf("slice d state = %q, want env-blocked", ds.State)
	}
	if ds.Reason != "env-up-failed" {
		t.Fatalf("slice d reason = %q, want env-up-failed", ds.Reason)
	}
}

// TestOracleWrong drives the oracle-wrong branch: slice a's first attempt is
// judged oracle-wrong (the fixture repro was actually correct), and its
// second attempt goes green. The ticket still pauses once for slice c's
// scripted question, exactly like the default scenario; answering it drives
// the ticket to fully green.
func TestOracleWrong(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{ScenarioBranch: "oracle-wrong"})

	r1 := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("first jig run exit = %d, want 2 (paused at slice c)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}

	lines := readJournal(t, fx, fx.Ticket)
	if _, ok := findJournalLine(lines, "result", "a", "oracle-wrong"); !ok {
		t.Fatalf("no result journal line for slice a with outcome oracle-wrong; journal:\n%+v", lines)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	as, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	if as.State != "green" {
		t.Fatalf("slice a state = %q, want green (oracle-wrong recovered on attempt 2)", as.State)
	}

	r2 := runJig(t, fx.StoreDir, "run", fx.Ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code != 0 {
		t.Fatalf("second jig run exit = %d, want 0 (all green)\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}
	for _, slice := range []string{"a", "b", "c", "d"} {
		s, err := st.ReadSliceState(fx.Ticket, slice)
		if err != nil {
			t.Fatalf("ReadSliceState(%s): %v", slice, err)
		}
		if s.State != "green" {
			t.Fatalf("slice %s state = %q, want green after answering", slice, s.State)
		}
	}
}

// TestFlawedBriefRequeue drives the flawed-brief branch: slice c's attempt
// is judged flawed-brief, the operator amends brief.md, `jig requeue
// --from-brief-diff` re-queues exactly the touched slice, and re-running
// resolves it green.
func TestFlawedBriefRequeue(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{ScenarioBranch: "flawed-brief"})

	r1 := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("first jig run exit = %d, want 2 (paused: flawed-brief question)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cs, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	if cs.State != "needs-input" || cs.Reason != "flawed-brief" {
		t.Fatalf("slice c state/reason = %q/%q, want needs-input/flawed-brief", cs.State, cs.Reason)
	}

	// Amend the brief: overlaying the branch's brief-amended.md, materialized
	// alongside the scenario tree by fixture.Generate, over the store's
	// brief.md.
	amended := readFileOrFatal(t, joinPath(fx.ScenarioDir, "brief-amended.md"))
	writeFileOrFatal(t, joinPath(fx.StoreDir, fx.Ticket, "brief.md"), amended)

	r2 := runJig(t, fx.StoreDir, "requeue", fx.Ticket, "--from-brief-diff")
	if r2.Code != 0 {
		t.Fatalf("jig requeue exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}
	assertOnlySlicesListed(t, r2.Stdout, []string{"c"})

	// NOTE (known gap, not this suite's bug): frontier.Requeue clears the
	// touched slice's SliceState.Question field but never marks the store's
	// questions/<id>.md record itself answered/closed, and
	// frontier.Run's buildReport treats "the first open question record" as
	// PendingQuestion regardless of whether its slice has since resolved.
	// So after a flawed-brief episode is remediated purely through
	// --from-brief-diff (the contract's own prescribed remediation, with no
	// mention of also answering the question), the orphaned open question
	// keeps reporting exit 2 on every subsequent run forever, even once
	// every slice is green. This assertion encodes the contractually
	// intended behavior (exit 0 once the requeue+run resolves the ticket)
	// and will fail until that is fixed in package frontier (either Requeue
	// closes the question(s) tied to its touched slices, or buildReport
	// only counts an open question whose slice is still needs-input).
	r3 := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r3.Code != 0 {
		t.Fatalf("second jig run exit = %d, want 0 (all green)\nstdout:\n%s\nstderr:\n%s", r3.Code, r3.Stdout, r3.Stderr)
	}
	for _, slice := range []string{"a", "b", "c", "d"} {
		s, err := st.ReadSliceState(fx.Ticket, slice)
		if err != nil {
			t.Fatalf("ReadSliceState(%s): %v", slice, err)
		}
		if s.State != "green" {
			t.Fatalf("slice %s state = %q, want green after requeue+run", slice, s.State)
		}
	}
}

// TestStallStops drives the stall branch: slice a returns the identical
// code-bug signature on its first two attempts, which must stop the run
// with a framed message pointing at requeue/answer as the way out.
//
// NOTE: slice c's scripted question also fires in this same run (the stall
// branch only overlays slice a's attempts). cmd/jig's exit-code priority
// checks PendingQuestion before Stopped (confirmed by reading
// cmd/jig/run.go's printRunReport), so the process exit is 2 here, not 1;
// the stall itself is still asserted directly, via the stopped block in
// stdout and the slice's on-disk state/reason/journal line.
func TestStallStops(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{ScenarioBranch: "stall"})

	r := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r.Code != 2 {
		t.Fatalf("jig run exit = %d, want 2 (slice c's question outranks the stall for exit-code purposes)\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "returned code-bug twice with no progress") {
		t.Fatalf("stdout missing framed stall message:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "amend the brief") {
		t.Fatalf("stdout missing stall remediation hint:\n%s", r.Stdout)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	as, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	if as.State != "stalled" || as.Reason != "stall" {
		t.Fatalf("slice a state/reason = %q/%q, want stalled/stall", as.State, as.Reason)
	}

	lines := readJournal(t, fx, fx.Ticket)
	if _, ok := findJournalLine(lines, "stall", "a", ""); !ok {
		t.Fatalf("no stall journal line for slice a; journal:\n%+v", lines)
	}
}

// TestCapExhaustion drives the cap branch: slice a returns three distinct
// code-bug summaries (no repeated signature, so no stall), exhausting the
// default 3-attempt cap. Status surfaces the attempt-cap reason for slice a.
//
// NOTE: as in TestStallStops, slice c's scripted question outranks the
// attempt-cap stop for exit-code purposes (see cmd/jig/run.go's
// printRunReport), so the process exits 2, not 1; the cap exhaustion itself
// is asserted directly via slice a's on-disk state/reason/attempts.
func TestCapExhaustion(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{ScenarioBranch: "cap"})

	r := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r.Code != 2 {
		t.Fatalf("jig run exit = %d, want 2 (slice c's question outranks the attempt-cap stop for exit-code purposes)\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	as, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	if as.State != "stalled" || as.Reason != "attempt-cap" {
		t.Fatalf("slice a state/reason = %q/%q, want stalled/attempt-cap", as.State, as.Reason)
	}
	if as.Attempts != 3 {
		t.Fatalf("slice a attempts = %d, want 3", as.Attempts)
	}

	sr := runJig(t, fx.StoreDir, "status", fx.Ticket)
	if sr.Code != 0 {
		t.Fatalf("jig status exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", sr.Code, sr.Stdout, sr.Stderr)
	}
	var row string
	for _, line := range strings.Split(sr.Stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "a,") {
			row = trimmed
			break
		}
	}
	if row == "" {
		t.Fatalf("no status row for slice a in:\n%s", sr.Stdout)
	}
	if !strings.Contains(row, "stalled") {
		t.Fatalf("slice a status row = %q, want it to contain state stalled", row)
	}
	// NOTE: cmd/jig's RenderStatus (status.go) only ever puts ss.Question in
	// the 5th column; it never surfaces ss.Reason there, so "attempt-cap"
	// does not currently appear in `jig status` output at all (the contract's
	// status format table has no reason column either). The Reason value
	// itself is fully covered above via store.ReadSliceState, which is the
	// authoritative source RenderStatus would need to start reading from to
	// satisfy the DoD wording literally; flagged as a follow-up rather than
	// asserted here against code this suite does not own.
}
