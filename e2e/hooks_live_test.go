package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/claudetest"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
)

// livePrompt is the one user prompt the scripted model answers, and
// liveSubagentPrompt the task the scripted model hands a subagent: a
// subagent's conversation begins with its own prompt, which the scripted API
// routes to a script of its own.
const (
	livePrompt         = "scripted hooks run: commit the slice"
	liveSubagentPrompt = "scripted hooks run: commit the subagent's slice"
)

// TestHooksLive is the only proof that the hook JSON jig prints is what Claude
// Code reads. The real `claude -p` runs in the fixture clone against a
// scripted stand-in for the Messages API (claudetest), with the hooks that
// `jig hooks --here` installs and nothing else: no git hook. It asserts that
// the SessionStart context, the ticket and its slice table, reaches the API
// request, that the session's Bash commands run with JIG_SESSION set to the
// harness's session id, and that the scripted Bash `git commit` leaves a ledger tool line
// and, since only the PostToolUse hook saw the commit, a journal verify-pending
// line from the worker that hook started. The project defaults to unattended,
// so the Stop hook refuses to let the session end while that verification
// runs, and the reason it gives must come back to the model. A subagent the
// session starts commits a slice of its own: its tool call reaches the ledger
// with its agent id, and its SubagentStop is held back, with a reason that
// comes back to the subagent, while that verification runs.
//
// It is opt-in, like the other tests that need the CLI (set JIG_LIVE_CLAUDE=1,
// with claude on PATH): CI's claude-cli job runs it.
func TestHooksLive(t *testing.T) {
	if os.Getenv("JIG_LIVE_CLAUDE") == "" {
		t.Skip("set JIG_LIVE_CLAUDE=1 to run the hooks against the local claude CLI")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("JIG_LIVE_CLAUDE is set but claude is not on PATH: %v", err)
	}
	// This test stays serial, with no t.Parallel: claudetest.Serve points the
	// claude CLI at its scripted API with t.Setenv, which edits the process
	// environment (and panics in a parallel test). The user home stays the
	// host's, which the CLI may need; the jig home and the CLI's own config
	// directory are this test's.
	env := jigEnv{home: t.TempDir()}
	fx := fixture.Generate(t, fixture.Opts{Home: env.home})
	h := newHarness(t, fx, env, "live")
	branch := fx.Branch
	h.Branch(branch)

	mustExitZero(t, runJig(t, env, fx.RepoDir, "hooks", "--here", "--yes"), "jig hooks --here")
	// The project defaults to unattended, so Stop holds the session back while
	// the verification of its commit is still running.
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(fx.RepoDir, project.MarkerFile), []byte("jig: \"1\"\nproject: "+cfg.Name+"\nunattended: true\n"))

	var (
		mu   sync.Mutex
		seen []string // the text of every message the model was sent
	)
	var script *claudetest.Session
	script = &claudetest.Session{Steps: []claudetest.Step{
		{Name: "commit the slice", Call: claudetest.Bash(
			"git -c user.name=jig-e2e -c user.email=e2e@example.invalid commit -q --allow-empty -m 'a: scripted' -m 'Jig-Slice: a' && git rev-parse HEAD")},
		{Name: "read the session id jig set", Call: claudetest.Bash("printenv JIG_SESSION")},
		{Name: "start a subagent", Call: func([]claudetest.ToolResult) claudetest.ToolCall {
			// The tool is called Task in the CLI's older releases and Agent in
			// the newer; the scripted API is offered whichever there is.
			name := "Agent"
			if script.ToolSchema(name) == nil {
				name = "Task"
			}
			return claudetest.ToolCall{Name: name, Input: map[string]any{
				"description": "commit the subagent's slice", "prompt": liveSubagentPrompt, "subagent_type": "general-purpose",
			}}
		}},
	}}
	subScript := &claudetest.Session{Steps: []claudetest.Step{
		{Name: "commit the subagent's slice", Call: claudetest.Bash(
			"git -c user.name=jig-e2e -c user.email=e2e@example.invalid commit -q --allow-empty -m 'b: scripted by a subagent' -m 'Jig-Slice: b' && git rev-parse HEAD")},
	}}
	claudetest.Serve(t, &claudetest.API{Route: func(p string) *claudetest.Session {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(p, liveSubagentPrompt):
			return subScript
		case strings.Contains(p, livePrompt):
			return script
		}
		return nil
	}, Observe: func(messages []string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, messages...)
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "-p", "--permission-mode", "dontAsk", "--allowedTools", "Bash,Agent,Task", "--no-session-persistence", "--", livePrompt)
	cmd.Dir = fx.RepoDir
	cmd.Env = env.environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude -p: %v\n%s", err, out)
	}
	// What the hooks did, for a failure to be read from.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if log, err := os.ReadFile(filepath.Join(home.LogsDir(env.home), "observe.log")); err == nil {
			t.Logf("observe.log:\n%s", log)
		}
		for _, l := range readLedger(t, env) {
			t.Logf("ledger: %s %s session=%s harness=%s tool=%s command=%q", l.TS, l.Event, l.Session, l.Harness, l.Tool, l.Command)
		}
	})
	script.Check(t)
	// The Bash tool's output can carry the harness's own lines after the
	// command's (on a Windows runner: "Shell cwd was reset to ..."), so the
	// sha is the line that is one.
	content := script.Results()[0].Content
	sha := shaLine.FindString(content)
	if sha == "" {
		t.Fatalf("the scripted commit printed %q, want its sha", content)
	}

	mu.Lock()
	got := strings.Join(seen, "\n--- message ---\n")
	mu.Unlock()
	for _, want := range []string{fx.Ticket, "slices[4]{id,state,attempts,blocked_by,question}"} {
		if !strings.Contains(got, want) {
			t.Errorf("the requests to the API lack the SessionStart context %q:\n%s", want, got)
		}
	}

	// The Stop hook's refusal reached the model as its next message: the JSON
	// {"decision":"block","reason":...} is what Claude Code reads.
	if !strings.Contains(got, "this session is unattended, so it is held back from stopping") {
		t.Errorf("no request to the API carries the reason jig gave for holding the session back:\n%s", got)
	}

	var start, tool journal.Line
	for _, l := range readLedger(t, env) {
		switch l.Event {
		case journal.EventSessionStart:
			start = l
		case journal.EventTool:
			if strings.Contains(l.Command, "Jig-Slice: a") {
				tool = l
			}
		}
	}
	if start.Session == "" || start.Harness != "claude" || start.Ticket != fx.Ticket {
		t.Errorf("session-start line = %+v, want one from claude on %s", start, fx.Ticket)
	}
	if tool.Session != start.Session || tool.Tool != "Bash" || tool.Commit != sha {
		t.Errorf("tool line = %+v, want the scripted Bash commit %s in session %s", tool, sha, start.Session)
	}
	// The SessionStart hook set JIG_SESSION for the session's Bash commands,
	// through the file Claude Code names in CLAUDE_ENV_FILE: a jig command
	// the session runs knows its session without being told.
	if got := script.Results()[1].Content; start.Session == "" || !strings.Contains(got, start.Session) {
		t.Errorf("printenv JIG_SESSION in the session printed %q, want the harness session id %q", got, start.Session)
	}
	pending := waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventVerifyPending && l.Commit == sha
	})
	if pending.Slice != "a" || pending.Session != start.Session {
		t.Errorf("verify-pending line = %+v, want slice a asked for by session %s", pending, start.Session)
	}

	// The subagent: its tool calls carry its agent id (the main session's
	// carry none), and its SubagentStop came back to it as a refusal whose
	// ledger line names it.
	subScript.Check(t)
	var subTool, subHold journal.Line
	for _, l := range readLedger(t, env) {
		switch {
		case l.Event == journal.EventTool && strings.Contains(l.Command, "Jig-Slice: b"):
			subTool = l
		case l.Event == journal.EventNoteDelivered && l.Outcome == "stop-block" && l.Agent != "":
			subHold = l
		}
	}
	if subTool.Agent == "" || subTool.Session != start.Session || subTool.Tool != "Bash" {
		t.Errorf("subagent tool line = %+v, want its Bash commit in session %s with an agent id", subTool, start.Session)
	}
	if subHold.Session != start.Session || subHold.Agent != subTool.Agent {
		t.Errorf("subagent refusal line = %+v, want a stop-block for agent %q in session %s", subHold, subTool.Agent, start.Session)
	}
	if !strings.Contains(got, "this agent's claim is unattended, so it is held back from stopping") {
		t.Errorf("no request to the API carries the reason jig gave for holding the subagent back:\n%s", got)
	}
	h.WaitVerifiers()
}

// mustExitZero fails t now unless r exited 0.
func mustExitZero(t *testing.T, r jigResult, what string) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("%s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", what, r.Code, r.Stdout, r.Stderr)
	}
}

// shaLine is a line that is a full commit sha and nothing else.
var shaLine = regexp.MustCompile(`(?m)^[0-9a-f]{40}\r?$`)
