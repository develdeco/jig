package main

import (
	"strings"
	"testing"
)

// refusedTicket is the fixture ticket, built, whose gate refused
// INTENT_CONFLICT (an --intent over its brief).
func refusedTicket(t *testing.T) (string, env, string) {
	t.Helper()
	fx, _, e := workFixture(t)
	buildFixtureTicket(t, fx)
	out, code := runMain(t, e, "", "gate", fx.Ticket, "--intent", "x", "--store", fx.StoreDir)
	if code != 1 || !strings.Contains(out, "code: INTENT_CONFLICT") {
		t.Fatalf("gate --intent: exit = %d, want 1 with INTENT_CONFLICT\n%s", code, out)
	}
	return fx.Ticket, e, fx.StoreDir
}

// TestNextSaysTheGateRefused: while the refusal is the review's word, plain
// jig next says it as the reason, and its help says what the code asks, not
// only "run jig gate".
func TestNextSaysTheGateRefused(t *testing.T) {
	t.Parallel()
	ticket, e, store := refusedTicket(t)
	out, code := runMain(t, e, "", "next", ticket, "--store", store)
	if code != 0 || !strings.Contains(out, "reason: \"gate refused: ticket "+ticket+" already has a brief.md") {
		t.Fatalf("next: exit = %d, want the refusal as its reason\n%s", code, out)
	}
	if !strings.Contains(out, "without --intent or --doc") || strings.Contains(out, "to open a gate round") {
		t.Errorf("next help does not say what the refusal asks:\n%s", out)
	}
	// A gate that then opens a round supersedes it.
	if out, code := runMain(t, e, "", "gate", ticket, "--store", store); code != 0 {
		t.Fatalf("gate: exit = %d\n%s", code, out)
	}
	if out, _ := runMain(t, e, "", "next", ticket, "--store", store); strings.Contains(out, "gate refused") {
		t.Errorf("next still says the refusal after a round opened:\n%s", out)
	}
}

// TestHereSaysTheGateRefused: jig here on the ticket prints the same line.
func TestHereSaysTheGateRefused(t *testing.T) {
	t.Parallel()
	ticket, e, store := refusedTicket(t)
	out, code := runMain(t, e, "", "here", ticket, "--store", store)
	if code != 0 || !strings.Contains(out, "review: \"gate refused: ticket "+ticket+" already has a brief.md") {
		t.Fatalf("here: exit = %d, want the refusal line\n%s", code, out)
	}
}
