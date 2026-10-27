package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestAdviceToSpawnSaysHowToWait: the advice that tells a session to spawn a
// reviewer says how to wait for it as Claude Code runs it, in the background
// whatever the session asks: in the same turn with TaskOutput, neither ending
// the turn nor scheduling a wakeup, and never stalling with another tool. It says so for a session that built the
// ticket and for one jig cannot tell, and never asks for a foreground run the
// harness does not give. A fresh session is told to review itself, with no
// subagent to wait for.
func TestAdviceToSpawnSaysHowToWait(t *testing.T) {
	t.Parallel()
	rule := []string{"TaskOutput (`block: true`", "load the tool with ToolSearch", "Do not end the turn or schedule a wakeup while it runs", "never call another tool to stall"}
	cases := []struct {
		name  string
		fresh verifydeliver.Fresh
		state string
		spawn bool
	}{
		{"built it", verifydeliver.Fresh{Known: true, Why: []string{"claimed slice a"}}, freshNo, true},
		{"unknown", verifydeliver.Fresh{}, freshUnknown, true},
		{"fresh", verifydeliver.Fresh{Known: true, Fresh: true}, freshYes, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := adviceFor(tc.fresh, "SHOP-1", "/work/shop")
			if a.State != tc.state {
				t.Errorf("state = %q, want %q", a.State, tc.state)
			}
			text := strings.Join(a.Lines, "\n")
			for _, want := range rule {
				if got := strings.Contains(text, want); got != tc.spawn {
					t.Errorf("advice has %q = %v, want %v:\n%s", want, got, tc.spawn, text)
				}
			}
			if strings.Contains(text, "run_in_background") {
				t.Errorf("advice asks for a foreground run, which Claude Code does not give:\n%s", text)
			}
		})
	}
}
