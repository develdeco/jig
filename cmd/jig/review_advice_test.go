package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestAdviceToSpawnSaysHowToWait: the advice that tells a session to spawn a
// reviewer says how to wait for it by what the session sees, never by a claim
// about the harness it cannot check: the Agent call's own result where that is
// the subagent's reply (background tasks off, as in a headless session jig
// starts); else, the call having returned an agent id, TaskOutput in the same
// turn where the session's tools have it, or, where they have none (Claude
// Code 2.1.277 removed it), the harness's notice that the subagent finished,
// ending the turn as waiting, not done, since the notice resumes an unattended
// `claude -p` run too; never scheduling a wakeup or stalling with another
// tool. It says so for a session that built the ticket and for one jig cannot
// tell, and never forbids ending the turn outright (a session with no
// TaskOutput could not then wait at all). It names no `run_in_background`:
// Claude Code 2.1.232 ignored a foreground ask (DF-1), and where a harness
// honours it the call's result is the reply, the first case. A fresh session
// is told to review itself, with no subagent to wait for.
func TestAdviceToSpawnSaysHowToWait(t *testing.T) {
	t.Parallel()
	rule := []string{
		"Where the call's own result is the subagent's reply", "that result holds the verdict",
		"Otherwise the call returns an agent id and the subagent runs in the background", "TaskOutput (`block: true`", "where your tools have it", "search for it with ToolSearch when they do not list it",
		"Where they have none", "the harness's notice that the subagent finished is the wait",
		"end the turn as waiting, not done", "resumes an unattended `claude -p` run too",
		"Never schedule a wakeup or call another tool to stall",
	}
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
				t.Errorf("advice names run_in_background, which Claude Code 2.1.232 ignored and whose foreground result the first case covers:\n%s", text)
			}
			if strings.Contains(text, "Do not end the turn") {
				t.Errorf("advice forbids ending the turn, the only wait a Claude Code without TaskOutput has:\n%s", text)
			}
		})
	}
}
