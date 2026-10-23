package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// hookPayload is the JSON Claude Code sends a hook, for the fixture clone.
func hookPayload(t *testing.T, fx *fixture.Fixture, event string, extra map[string]any) string {
	t.Helper()
	p := map[string]any{"session_id": "s1", "cwd": fx.RepoDir, "hook_event_name": event, "transcript_path": filepath.Join(fx.Dir, "t.jsonl")}
	for k, v := range extra {
		p[k] = v
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// observeRig is a fixture clone on the ticket's branch and an env that runs in
// it with a spawner that starts nothing.
func observeRig(t *testing.T) (*fixture.Fixture, env, *spawnRecorder) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	if _, err := gitx.Run(fx.RepoDir, "checkout", "-q", "-b", "jig/"+fx.Ticket); err != nil {
		t.Fatal(err)
	}
	rec := &spawnRecorder{PID: 777}
	e := testEnv(fx.Home).inDir(fx.RepoDir).withSpawn(rec.Spawn).withExecutable(filepath.Join(fx.Dir, "bin", "jig"))
	return fx, e, rec
}

func TestObserveSessionStartAnswersWithTheHereContextAsClaudeReadsIt(t *testing.T) {
	t.Parallel()
	fx, e, _ := observeRig(t)
	out, code := runMain(t, e, hookPayload(t, fx, "SessionStart", map[string]any{"source": "startup", "model": "m"}), "observe", "--harness", "claude")
	if code != 0 {
		t.Fatalf("exit %d, output %q", code, out)
	}
	var got struct {
		Specific struct {
			Name    string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not the one JSON object Claude Code reads: %q: %v", out, err)
	}
	if got.Specific.Name != "SessionStart" || !strings.Contains(got.Specific.Context, "ticket: "+fx.Ticket) || !strings.Contains(got.Specific.Context, "slices[") ||
		!strings.Contains(got.Specific.Context, "session: s1") {
		t.Errorf("additionalContext = %q, want jig here for %s as session s1", got.Specific.Context, fx.Ticket)
	}
	if starts := ledgerEvents(t, fx.Home, journal.EventSessionStart); len(starts) != 1 || starts[0].Session != "s1" || starts[0].Model != "m" {
		t.Errorf("session-start lines = %+v, want exactly s1 (jig here must not mint a second session)", starts)
	}
}

func ledgerEvents(t *testing.T, root, event string) []journal.Line {
	t.Helper()
	lines, err := ledger.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range lines {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

func TestObserveToolStartsTheVerificationThroughTheEnvsSpawner(t *testing.T) {
	t.Parallel()
	fx, e, rec := observeRig(t)
	runHook := func(event string, extra map[string]any) (string, int) {
		t.Helper()
		return runMain(t, e, hookPayload(t, fx, event, extra), "observe", "--harness", "claude")
	}
	if _, code := runHook("SessionStart", nil); code != 0 {
		t.Fatalf("SessionStart exit %d", code)
	}
	if err := os.WriteFile(filepath.Join(fx.RepoDir, "n.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"add", "--", "n.txt"}, {"commit", "-q", "-m", "work", "-m", "Jig-Slice: a"}} {
		if _, err := gitx.Run(fx.RepoDir, a...); err != nil {
			t.Fatal(err)
		}
	}
	if out, code := runHook("PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "git commit -m work"}}); code != 0 || out != "" {
		t.Fatalf("PostToolUse: exit %d, output %q, want a silent success", code, out)
	}

	specs := rec.Specs()
	if len(specs) != 1 || filepath.Base(specs[0].Path) != "jig" || specs[0].Args[0] != "verify" || specs[0].Args[1] != "--worker" {
		t.Fatalf("spawned %+v, want one jig verify --worker", specs)
	}
	if spawned := ledgerEvents(t, fx.Home, journal.EventVerifySpawned); len(spawned) != 1 || spawned[0].PID != 777 || spawned[0].Slice != "a" {
		t.Errorf("verify-spawned lines = %+v, want one with pid 777 for slice a", spawned)
	}
	if tools := ledgerEvents(t, fx.Home, journal.EventTool); len(tools) != 1 || tools[0].Command != "git commit -m work" {
		t.Errorf("tool lines = %+v", tools)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
