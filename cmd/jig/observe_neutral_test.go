package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

func TestObserveBuildsTheEventFromFlagsForAScript(t *testing.T) {
	t.Parallel()
	fx, e, _ := observeRig(t)
	out, code := runMain(t, e, "", "observe", "--event", "session-start", "--session", "crew-1", "--cwd", fx.RepoDir, "--model", "m")
	if code != 0 {
		t.Fatalf("exit %d, output %q", code, out)
	}
	var neutral struct {
		Context string `json:"context"`
	}
	if err := jsonUnmarshal(out, &neutral); err != nil || !strings.Contains(neutral.Context, "ticket: "+fx.Ticket) {
		t.Errorf("output = %q (%v), want the neutral JSON with the context", out, err)
	}
	if out, code := runMain(t, e, "", "observe", "--event", "tool", "--session", "crew-1", "--cwd", fx.RepoDir, "--tool", "Bash", "--command", "ls", "--exit", "0", "--files", "a.go, b.go"); out != "" || code != 0 {
		t.Fatalf("tool: exit %d, output %q", code, out)
	}
	tools := ledgerEvents(t, fx.Home, journal.EventTool)
	if len(tools) != 1 || tools[0].Session != "crew-1" || tools[0].Harness != "neutral" || tools[0].Exit == nil || len(tools[0].Files) != 2 || tools[0].Files[1] != "b.go" {
		t.Errorf("tool lines = %+v, want the call as the flags gave it", tools)
	}
}
