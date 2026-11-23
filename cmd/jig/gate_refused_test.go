package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// ticketJournal returns the fixture ticket's journal lines.
func ticketJournal(t *testing.T, fx *fixture.Fixture) []journal.Line {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// TestGateRefusedLine: a gate refused on a precondition leaves one
// gate-refused line, from `jig gate` and from `jig review context` (which
// opens a round when none is open), and no gate-open line; once the cause is
// gone the gate opens round 1.
func TestGateRefusedLine(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	e := testEnv(fx.Home)

	wantLast := func(t *testing.T, code string, total int) {
		t.Helper()
		lines := ticketJournal(t, fx)
		n := 0
		for _, l := range lines {
			switch l.Event {
			case journal.EventGateOpen:
				t.Fatalf("a gate-open line after a refusal: %+v", lines)
			case journal.EventGateRefused:
				n++
			}
		}
		last := lines[len(lines)-1]
		if n != total || last.Event != journal.EventGateRefused || last.Code != code || last.Summary == "" {
			t.Fatalf("gate-refused lines = %d (want %d), last = %+v, want one with code %s and a summary", n, total, last, code)
		}
	}

	t.Run("jig gate with a slice unbuilt is GATE_NOT_GREEN", func(t *testing.T) {
		out, code := runMain(t, e, "", "gate", fx.Ticket, "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
		if code != 1 || !strings.Contains(out, "code: GATE_NOT_GREEN") {
			t.Fatalf("gate: exit = %d, want 1 with GATE_NOT_GREEN\n%s", code, out)
		}
		wantLast(t, "GATE_NOT_GREEN", 1)
	})
	t.Run("jig review context journals the same refusal", func(t *testing.T) {
		out, code := runMain(t, e, "", "review", "context", fx.Ticket, "--store", fx.StoreDir)
		if code == 0 || !strings.Contains(out, "GATE_NOT_GREEN") {
			t.Fatalf("review context: exit = %d, want a GATE_NOT_GREEN refusal\n%s", code, out)
		}
		wantLast(t, "GATE_NOT_GREEN", 2)
	})

	buildFixtureTicket(t, fx)

	t.Run("--intent on a ticket with a brief is INTENT_CONFLICT", func(t *testing.T) {
		out, code := runMain(t, e, "", "gate", fx.Ticket, "--intent", "a", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
		if code != 1 || !strings.Contains(out, "code: INTENT_CONFLICT") {
			t.Fatalf("gate --intent: exit = %d, want 1 with INTENT_CONFLICT\n%s", code, out)
		}
		wantLast(t, "INTENT_CONFLICT", 3)
	})
	t.Run("the rerun opens round 1", func(t *testing.T) {
		if out, code := runMain(t, e, "", "gate", fx.Ticket, "--scenario", fx.ScenarioDir, "--store", fx.StoreDir); code != 0 {
			t.Fatalf("gate: exit = %d, want 0\n%s", code, out)
		}
		if got := gateOpens(t, fx); got != 1 {
			t.Fatalf("gate-open lines = %d, want 1", got)
		}
	})
}
