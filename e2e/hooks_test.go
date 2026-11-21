package e2e

// End-to-end tests of the hooks: what `jig hooks --global` and `jig githook
// --global` install, run by a scripted harness the way a real one runs them,
// drive `jig observe` through a session on the fixture ticket. The commits are
// real; a commit starts nothing, and the session proves each one by running
// `jig verify` itself, recorded in the journal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// spawnedFor counts the ledger lines that say a worker was started for sha: a
// commit starts none, so a test that saw a commit expects 0.
func spawnedFor(lines []journal.Line, sha string) int {
	n := 0
	for _, l := range lines {
		if l.Event == journal.EventVerifySpawned && l.Commit == sha {
			n++
		}
	}
	return n
}

// TestHooksDriveVerify is the digester's loop with the hooks installed and
// nothing else: SessionStart tells the session its ticket and slices, a commit
// made through git's hook starts nothing however many hooks see it, Stop says
// the commit has no recorded result, the session's own `jig verify` records
// it, the outcome reaches the session at its next prompt, and SessionEnd
// closes the session.
func TestHooksDriveVerify(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.withGitGlobal(t)
	h := newHarness(t, fx, env, "s-hooks")
	branch := fx.Branch
	h.Branch(branch)

	// Install both hooks into this test's own home and git config.
	if res := runJig(t, env, fx.RepoDir, "hooks", "--global", "--harness", "claude", "--yes"); res.Code != 0 {
		t.Fatalf("jig hooks --global exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if res := runJig(t, env, fx.RepoDir, "githook", "--global"); res.Code != 0 {
		t.Fatalf("jig githook --global exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}

	// SessionStart, run from the settings file as Claude Code runs it.
	start := installedHookCommand(t, claudeSettings(env), "SessionStart")
	if !strings.HasSuffix(start, "observe --harness claude") {
		t.Fatalf("the installed SessionStart command = %q, want jig observe --harness claude", start)
	}
	// Claude Code names a file of the session's, in a directory named after
	// its id, that it sources before each Bash command: the hook puts the
	// session id there for the jig commands the session runs.
	sessionDir := filepath.Join(t.TempDir(), "s-hooks")
	if err := os.Mkdir(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(sessionDir, "sessionstart-hook-0.sh")
	got := decodeHook(t, runHookCommand(t, env.with("CLAUDE_ENV_FILE="+envFile), fx.RepoDir, start, h.Payload("SessionStart", map[string]any{"source": "startup", "model": "claude-test"})))
	if got.Specific.Name != "SessionStart" || !strings.Contains(got.Specific.Context, fx.Ticket) || !strings.Contains(got.Specific.Context, "slices[4]{id,state,covers,attempts,blocked_by,question}") ||
		!strings.Contains(got.Specific.Context, "session: s-hooks") {
		t.Fatalf("SessionStart context = %q, want %s, its slice table and the session id", got.Specific.Context, fx.Ticket)
	}
	if exports := string(readFileOrFatal(t, envFile)); exports != "export JIG_SESSION='s-hooks'\n" {
		t.Errorf("CLAUDE_ENV_FILE = %q, want the export of JIG_SESSION", exports)
	}
	if strings.Contains(got.Specific.Context, "JIG_SESSION") {
		t.Errorf("SessionStart context = %q, asks for JIG_SESSION though the harness set it", got.Specific.Context)
	}

	// The session claims slice a, which records where the ticket's work
	// started, then commits it through git: its global post-commit hook
	// records the commit and starts nothing.
	if res := h.Jig("claim", fx.Ticket, "--slice", "a"); res.Code != 0 {
		t.Fatalf("jig claim exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	sha := h.CommitScenario("a", 1)
	if n := spawnedFor(readLedger(t, env), sha); n != 0 {
		t.Fatalf("%d workers were started for %s, want none: a commit starts nothing", n, sha[:12])
	}

	// Stop right after: the commit has no recorded result, and the message
	// names the command that records one.
	stop := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if want := "commit " + sha[:12] + " for slice a has no recorded result"; !strings.Contains(stop.SystemMessage, want) || stop.Decision != "" {
		t.Errorf("Stop = %+v, want a message saying %q and no refusal", stop, want)
	}
	if res := h.Verify(fx.Ticket, "a"); res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
		t.Fatalf("jig verify exit = %d, want a pass\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}

	// The agent commits through its Bash tool: git's hook records the commit,
	// then the PostToolUse hook sees it, and neither starts anything. The
	// hook hands the session the outcome of slice a's run, which it has not
	// heard yet.
	bash := h.Bash("git commit --allow-empty -m 'c: nothing yet' -m 'Jig-Slice: c'")
	if bash.Code != 0 || bash.Hook.Code != 0 || !strings.Contains(bash.Hook.Stdout, "verified "+sha[:12]+" for slice a") {
		t.Fatalf("Bash commit: exit %d, hook exit %d, hook stdout %q, want the outcome of slice a", bash.Code, bash.Hook.Code, bash.Hook.Stdout)
	}
	sha2 := h.git("rev-parse", "HEAD")
	if lines := readLedger(t, env); spawnedFor(lines, sha) != 0 || spawnedFor(lines, sha2) != 0 {
		t.Errorf("verify-spawned lines per commit = %d and %d, want none: the hooks that see a commit start nothing", spawnedFor(lines, sha), spawnedFor(lines, sha2))
	}
	var tools []journal.Line
	for _, l := range readLedger(t, env) {
		if l.Event == journal.EventTool {
			tools = append(tools, l)
		}
	}
	if len(tools) != 1 || tools[0].Session != "s-hooks" || tools[0].Harness != "claude" || tools[0].Tool != "Bash" || !strings.Contains(tools[0].Command, "git commit") || tools[0].Commit != sha2 {
		t.Errorf("tool lines = %+v, want the one Bash call, at the new commit", tools)
	}

	// The session verifies the second commit too, and the journal says so.
	if res := h.Verify(fx.Ticket, "c"); res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
		t.Fatalf("jig verify exit = %d, want a pass\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}

	// The outcome reaches the session at its next prompt, once.
	prompt := decodeHook(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "carry on"}))
	if want := "verified " + sha2[:12] + " for slice c"; !strings.Contains(prompt.Specific.Context, want) {
		t.Errorf("prompt context = %q, want %q", prompt.Specific.Context, want)
	}
	if again := h.Hook("UserPromptSubmit", map[string]any{"prompt": "and again"}); again.Code != 0 || again.Stdout != "" {
		t.Errorf("a second prompt = exit %d, %q, want silence: the session was told", again.Code, again.Stdout)
	}
	// Nothing is outstanding now.
	if res := h.Hook("Stop", map[string]any{"stop_hook_active": false}); res.Code != 0 || res.Stdout != "" {
		t.Errorf("Stop with everything verified = exit %d, %q, want silence", res.Code, res.Stdout)
	}

	// SessionEnd closes the session.
	if res := h.Hook("SessionEnd", map[string]any{"reason": "other"}); res.Code != 0 || res.Stdout != "" {
		t.Errorf("SessionEnd = exit %d, %q, want silence", res.Code, res.Stdout)
	}
	sessions, err := ledger.Sessions(env.home)
	if err != nil {
		t.Fatal(err)
	}
	if s := sessions["s-hooks"]; s.Live() || s.Ticket != fx.Ticket || s.Harness != "claude" || s.Model != "claude-test" {
		t.Errorf("session s-hooks = %+v, want ended, on %s, of claude, model claude-test", s, fx.Ticket)
	}
}
