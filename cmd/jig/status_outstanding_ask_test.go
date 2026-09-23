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
