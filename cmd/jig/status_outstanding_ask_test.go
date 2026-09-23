package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// writeAskedFindingsYAML records one round's findings.yaml holding a
// single asked finding, the on-disk state `jig status` folds to decide
// what is outstanding.
func writeAskedFindingsYAML(t *testing.T, storeDir, ticket string) {
	t.Helper()
	dir := filepath.Join(storeDir, ticket, "gate", "round-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	body := "scope: diff\n" +
		"reviewed_paths:\n" +
		"  - go.mod\n" +
		"findings:\n" +
		"  - id: r1-f1\n" +
		"    file: go.mod\n" +
		"    line: 1\n" +
		"    title: toolchain bumped\n" +
		"    detail: the toolchain line moved\n" +
		"    action: ask\n" +
		"    risk: medium\n" +
		"    risk_rationale: it changes what every build runs on\n" +
		"    status: asked\n" +
		"    recurrences: 0\n" +
		"summary: round 1\n"
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write findings.yaml: %v", err)
	}
}

// TestRenderStatusOutstandingAskWithQueuedFrontier pins that status never
// prints a command the frontier check would refuse. An outstanding ask is
// decided by `jig gate <ticket>` at a terminal, but that gate is refused
// while any slice is short of green - the ordinary case, since a round
// that leaves an ask undecided usually queues fix slices too. So the
// deciding command is not a per-row cell: it is named in the help, after
// the ticket's own next step, the order `jig gate`'s report hint prints.
func TestRenderStatusOutstandingAskWithQueuedFrontier(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// a is green; b, c and d stay queued, so the frontier is not green
	// and `jig gate` would refuse with GATE_NOT_GREEN.
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state a: %v", err)
	}
	writeAskedFindingsYAML(t, fx.StoreDir, fx.Ticket)

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	wantTable := "outstanding_asks[1]{id,risk,file:line,title}:\n" +
		"  r1-f1,medium,\"go.mod:1\",toolchain bumped\n"
	if !strings.Contains(got, wantTable) {
		t.Errorf("status is missing the outstanding_asks table %q:\n%s", wantTable, got)
	}

	wantHelp := "help[2]:\n" +
		"  Run `jig run JIG-1` to work the frontier\n" +
		"  Then run `jig gate JIG-1` at a terminal to decide the outstanding asks\n"
	if !strings.HasSuffix(got, wantHelp) {
		t.Errorf("status help does not name the frontier-first ordering:\n--- got ---\n%s--- want suffix ---\n%s", got, wantHelp)
	}

	// The refused command must not be printed as a standalone step: every
	// `jig gate` the ticket's status prints while the frontier is short of
	// green is the second half of the ordering above.
	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "jig gate ") {
			continue
		}
		if !strings.Contains(line, "Then run") {
			t.Errorf("status prints `jig gate` as a step the frontier check would refuse: %q", line)
		}
	}
}

// TestRenderStatusOutstandingAskWithGreenFrontier is the other half: once
// every slice is green the gate runs, so the help names it directly.
func TestRenderStatusOutstandingAskWithGreenFrontier(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := st.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatalf("write slice state %s: %v", id, err)
		}
	}
	writeAskedFindingsYAML(t, fx.StoreDir, fx.Ticket)

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	wantHelp := "help[1]:\n" +
		"  Run `jig gate JIG-1` at a terminal to decide the outstanding asks\n"
	if !strings.HasSuffix(got, wantHelp) {
		t.Errorf("status help does not name the deciding command:\n--- got ---\n%s--- want suffix ---\n%s", got, wantHelp)
	}
}

// TestRenderStatusSurvivesAnUnreadableFindingsFile pins that one corrupt
// gate round does not take down `jig status`. Status is the command a
// person runs when something is already wrong, so it renders the ticket,
// its slices and its questions regardless, names the round it could not
// read, and never leaks an internal package name into user-facing text.
// A gate round itself still refuses to run on bookkeeping it cannot read;
// only this read-only view degrades.
func TestRenderStatusSurvivesAnUnreadableFindingsFile(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state a: %v", err)
	}
	// Round 1 holds a real outstanding ask; round 2's file is corrupt.
	writeAskedFindingsYAML(t, fx.StoreDir, fx.Ticket)
	corrupt := filepath.Join(fx.StoreDir, fx.Ticket, "gate", "round-2")
	if err := os.MkdirAll(corrupt, 0o755); err != nil {
		t.Fatalf("mkdir round 2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corrupt, "findings.yaml"), []byte("{not: valid: yaml"), 0o644); err != nil {
		t.Fatalf("write corrupt findings.yaml: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v, want a rendered status despite the corrupt round", err)
	}
	if !strings.Contains(got, "slices[4]") {
		t.Errorf("status did not render the slices table:\n%s", got)
	}
	if !strings.Contains(got, "unreadable_gate_rounds: 2 (any asks they recorded are not listed below)") {
		t.Errorf("status did not name the round it could not read:\n%s", got)
	}
	if !strings.Contains(got, "r1-f1") {
		t.Errorf("status dropped the ask recorded by the round it could read:\n%s", got)
	}
	if strings.Contains(got, "verifydeliver") {
		t.Errorf("status leaked an internal package name into user-facing text:\n%s", got)
	}
}

// TestRenderStatusSurvivesAnUnreadableReportFile is the other per-round
// file status reads. A corrupt report.yaml used to fail the whole command
// through the next-step hint, the same way a corrupt findings.yaml did.
// The verdict it could not read is treated as "not clean", so the hint
// points at the frontier rather than at publish: the safe direction when
// jig cannot tell.
func TestRenderStatusSurvivesAnUnreadableReportFile(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := st.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatalf("write slice state %s: %v", id, err)
		}
	}
	dir := filepath.Join(fx.StoreDir, fx.Ticket, "gate", "round-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round 1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte("{verdict: [unterminated"), 0o644); err != nil {
		t.Fatalf("write corrupt report.yaml: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v, want a rendered status despite the corrupt report", err)
	}
	if !strings.Contains(got, "unreadable_gate_rounds: 1") {
		t.Errorf("status did not name the round it could not read:\n%s", got)
	}
	if strings.Contains(got, "jig publish") {
		t.Errorf("status pointed at publish on a verdict it could not read:\n%s", got)
	}
	if strings.Contains(got, "verifydeliver") {
		t.Errorf("status leaked an internal package name:\n%s", got)
	}
}
