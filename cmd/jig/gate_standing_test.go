package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// gateOpens counts the gate-open lines of the fixture ticket's journal.
func gateOpens(t *testing.T, fx *fixture.Fixture) int {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range lines {
		if l.Event == journal.EventGateOpen {
			n++
		}
	}
	return n
}

// TestSecondGateReportsTheStandingRound: a gate run again at a branch head a
// round already stands at opens no round and writes no gate-open line; it
// reports that round, open or recorded.
func TestSecondGateReportsTheStandingRound(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)

	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 || strings.Contains(out, "stands at its head") {
		t.Fatalf("first gate: exit = %d, want 0 and an ordinary report\n%s", code, out)
	}
	before := gateOpens(t, fx)

	out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "round: 1 stands at its head (not opened again)") || !strings.Contains(out, "verdict: open") {
		t.Fatalf("second gate over an open round: exit = %d, want 0 and the standing round\n%s", code, out)
	}
	if after := gateOpens(t, fx); after != before {
		t.Errorf("gate-open lines = %d, want %d: the second gate opened a round", after, before)
	}

	// Record the round, then gate again at the same head.
	ctx, code := runMain(t, e, "", "review", "context", fx.Ticket, "--json", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("review context: exit = %d\n%s", code, ctx)
	}
	var req struct {
		MustReview []string `json:"must_review"`
	}
	if err := json.Unmarshal([]byte(ctx), &req); err != nil {
		t.Fatalf("review context --json: %v\n%s", err, ctx)
	}
	result, err := json.Marshal(map[string]any{
		"findings": []any{}, "still_present": []any{}, "reviewed_paths": req.MustReview, "summary": "nothing found",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clean.json")
	if err := os.WriteFile(path, result, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "review", "done", fx.Ticket, "--result", path, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("review done: exit = %d, want a clean verdict\n%s", code, out)
	}
	before = gateOpens(t, fx)
	out, code = runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "round: 1 stands at its head (not opened again)") || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("gate over a recorded round: exit = %d, want 0 and the recorded round\n%s", code, out)
	}
	if after := gateOpens(t, fx); after != before {
		t.Errorf("gate-open lines = %d, want %d: the gate opened a round over a recorded one", after, before)
	}
}

// TestSecondGateWithRoundShapingFlagsGoesOnAsToday: a gate given a flag that
// shapes the round does not report the standing round, which would drop the
// flag; it goes on as it would with none standing.
func TestSecondGateWithRoundShapingFlagsGoesOnAsToday(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("first gate: exit = %d\n%s", code, out)
	}
	for _, flag := range []string{"--yes", "--early"} {
		out, code := runMain(t, e, "", "gate", fx.Ticket, flag, "--store", fx.StoreDir)
		if code != 0 || strings.Contains(out, "stands at its head") {
			t.Fatalf("gate with %s: exit = %d, want 0 and an ordinary report\n%s", flag, code, out)
		}
	}
}

// TestTwoGatesAtOnceOpenOneRound: two gates of one ticket started together
// wait for nothing, both succeed, and the ticket ends with one open round.
func TestTwoGatesAtOnceOpenOneRound(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)

	var wg sync.WaitGroup
	outs := make([]string, 2)
	codes := make([]int, 2)
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], codes[i] = runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
		}()
	}
	wg.Wait()
	for i := range outs {
		if codes[i] != 0 {
			t.Fatalf("gate %d: exit = %d, want 0\n%s", i, codes[i], outs[i])
		}
	}
	var found []string
	_ = filepath.WalkDir(fx.StoreDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "gate.round-") && strings.HasSuffix(d.Name(), ".open.yaml") {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 1 || !strings.HasSuffix(found[0], "gate.round-1.open.yaml") {
		t.Errorf("open records = %v, want exactly round 1's", found)
	}
}

// TestReviewContextOpensARoundWithNoGateRunning: review context opens a round
// when none is open, without a gate having run first.
func TestReviewContextOpensARoundWithNoGateRunning(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "review", "context", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("review context: exit = %d, want 0\n%s", code, out)
	}
	if n := gateOpens(t, fx); n != 1 {
		t.Errorf("gate-open lines = %d, want 1", n)
	}
}
