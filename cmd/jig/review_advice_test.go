package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestAdviceToSpawnSaysHowToWait: the advice that tells a session to spawn a
// reviewer asks for the foreground where the Agent tool takes it
// (`run_in_background: false`, honoured by Claude Code 2.1.232, 2.1.309 and
// 2.1.317 headless, top level and nested) and says how to wait by what the call
// returns, never by a claim about the harness the session cannot check: the
// subagent's reply holds the verdict; a launch notice means it runs in the
// background, and then a top-level session waits in the same turn with
// TaskOutput where its tools have it, or, where they have none (2.1.277 removed
// it), at the harness's notice, ending the turn as waiting, not done, since the
// notice resumes an unattended `claude -p` run too; while a subagent, whose
// turn's end is its hand-back and whose reviewer's notice goes to the session
// above it, hands back at once with the round open, never claiming the ticket
// reviewed, and the session above reads the verdict from the store. Never a
// wakeup or a stalling tool. It says so for a session that built the ticket and
// for one jig cannot tell, never forbids ending the turn outright (a session
// with no TaskOutput could not then wait at all), and never asks for the
// background. A fresh session is told to review itself, with no subagent to
// wait for.
func TestAdviceToSpawnSaysHowToWait(t *testing.T) {
	t.Parallel()
	rule := []string{
		"`run_in_background: false` where the tool takes it",
		"If the call's own result is the subagent's reply, that result holds the verdict",
		"If the call returns without the reply (a launch notice and an agent id), the subagent runs in the background",
		"A top-level session waits for its verdict in this turn with TaskOutput (`block: true`", "search for it with ToolSearch when they do not list it",
		"where they have none", "the harness's notice that the subagent finished is the wait",
		"end the turn as waiting, not done", "resumes an unattended `claude -p` run too",
		"A subagent (a chart run's build subagent) cannot wait: its turn's end is its hand-back",
		"hand back at once, saying the round is open and giving the reviewer's agent id, never that SHOP-1 is reviewed",
		"the session above reads the verdict with `jig status SHOP-1`",
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
			if strings.Contains(text, "run_in_background: true") {
				t.Errorf("advice asks for the background, where the foreground gives the verdict in the call's result:\n%s", text)
			}
			if strings.Contains(text, "Do not end the turn") {
				t.Errorf("advice forbids ending the turn, the only wait a Claude Code without TaskOutput has:\n%s", text)
			}
		})
	}
}
