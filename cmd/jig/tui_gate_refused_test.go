package main

import (
	"strings"
	"testing"
)

// TestTUIPipeGateRefused: the frame of a ticket whose gate was refused on its
// preconditions reads the refusal on the review step, and reads the round
// once the slices are built and the gate opens one.
func TestTUIPipeGateRefused(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)

	out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 1 || !strings.Contains(out, "code: GATE_NOT_GREEN") {
		t.Fatalf("gate: exit = %d, want 1 with GATE_NOT_GREEN\n%s", code, out)
	}
	keys, frames, wait := startTUI(t, e, fx.Ticket)
	frames.until(t, "gate refused: slice ")
	if strings.Contains(frames.String(), "round 1 open") {
		t.Errorf("a ticket with no open round reads a round open:\n%s", frames.String())
	}
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, frames.String())
	}

	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("gate after the build: exit = %d\n%s", code, out)
	}
	keys, frames, wait = startTUI(t, e, fx.Ticket)
	frames.until(t, "round 1 open")
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, frames.String())
	}
}
