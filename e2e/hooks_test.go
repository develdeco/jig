package e2e

// End-to-end tests of the hooks: what `jig hooks --global` and `jig githook
// --global` install, run by a scripted harness the way a real one runs them,
// drive `jig observe` through a session on the fixture ticket. The commits are
// real, so the verifications are too: a detached worker per commit, in a
// worktree of its own, recorded in the journal.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// spawnedFor counts the ledger lines that say a worker was started for sha.
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
// started by git's hook is verified by a detached worker once, however many
// hooks see it, Stop shows the verification while it runs, the outcome reaches
// the session at its next prompt, and SessionEnd closes the session.
func TestHooksDriveVerify(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.withGitGlobal(t)
	h := newHarness(t, fx, env, "s-hooks")
	branch := "jig/" + fx.Ticket
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
	// Claude Code names a file it sources before each Bash command of the
	// session: the hook puts the session id there for the jig commands the
	// session runs.
	envFile := filepath.Join(t.TempDir(), "sessionstart-hook-0.sh")
	got := decodeHook(t, runHookCommand(t, env.with("CLAUDE_ENV_FILE="+envFile), fx.RepoDir, start, h.Payload("SessionStart", map[string]any{"source": "startup", "model": "claude-test"})))
	if got.Specific.Name != "SessionStart" || !strings.Contains(got.Specific.Context, fx.Ticket) || !strings.Contains(got.Specific.Context, "slices[4]{id,state,attempts,blocked_by,question}") ||
		!strings.Contains(got.Specific.Context, "session: s-hooks") {
		t.Fatalf("SessionStart context = %q, want %s, its slice table and the session id", got.Specific.Context, fx.Ticket)
	}
	if exports := string(readFileOrFatal(t, envFile)); exports != "export JIG_SESSION='s-hooks'\n" {
		t.Errorf("CLAUDE_ENV_FILE = %q, want the export of JIG_SESSION", exports)
	}
	if strings.Contains(got.Specific.Context, "JIG_SESSION") {
		t.Errorf("SessionStart context = %q, asks for JIG_SESSION though the harness set it", got.Specific.Context)
	}

	// A commit made by git: its global post-commit hook starts the worker.
	sha := h.CommitScenario("a", 1)
	waitFor(t, 10*time.Second, "the worker git's hook started", func() bool { return spawnedFor(readLedger(t, env), sha) == 1 })

	// Stop right after: the verification is still running.
	stop := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if want := "verification of " + sha[:12] + " for slice a is still running"; !strings.Contains(stop.SystemMessage, want) || stop.Decision != "" {
		t.Errorf("Stop = %+v, want a message saying %q and no refusal", stop, want)
	}

	// The agent commits through its Bash tool: git's hook starts the worker,
	// then the PostToolUse hook sees the commit and starts nothing more.
	bash := h.Bash("git commit --allow-empty -m 'c: nothing yet' -m 'Jig-Slice: c'")
	if bash.Code != 0 || bash.Hook.Code != 0 || bash.Hook.Stdout != "" {
		t.Fatalf("Bash commit: exit %d, hook exit %d, hook stdout %q, want a silent hook", bash.Code, bash.Hook.Code, bash.Hook.Stdout)
	}
	sha2 := h.git("rev-parse", "HEAD")
	if lines := readLedger(t, env); spawnedFor(lines, sha) != 1 || spawnedFor(lines, sha2) != 1 {
		t.Errorf("verify-spawned lines per commit = %d and %d, want 1 and 1: the hooks that see a commit after the first start nothing", spawnedFor(lines, sha), spawnedFor(lines, sha2))
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

	// Both verify, and the journal says so.
	for _, c := range []struct{ sha, slice string }{{sha, "a"}, {sha2, "c"}} {
		waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
			return l.Event == journal.EventVerified && l.Commit == c.sha && l.Slice == c.slice
		})
	}
	h.WaitVerifiers()

	// The outcome reaches the session at its next prompt, once.
	prompt := decodeHook(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "carry on"}))
	for _, want := range []string{"verified " + sha[:12] + " for slice a", "verified " + sha2[:12] + " for slice c"} {
		if !strings.Contains(prompt.Specific.Context, want) {
			t.Errorf("prompt context = %q, want %q", prompt.Specific.Context, want)
		}
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
