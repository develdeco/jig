package e2e

// fakeHarness plays the part of an agent harness (Claude Code, say) for the
// digester e2e tests. The digester never starts a session; a harness runs
// beside it, works in the operator clone, runs jig and git, and fires hooks
// at jig observe. This fake does just those things, scripted: it commits a
// scenario patches with the trailer a real session leaves, builds the hook
// payloads Claude Code sends, runs jig observe on them, and waits for or kills
// the detached verification workers a commit starts. It adds no model and no
// network.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// verifierWait is how long WaitVerifiers gives the detached verification
// workers a harness started to finish before it kills them.
const verifierWait = 60 * time.Second

// sliceTrailerKey is the commit trailer that names the slice a commit is for.
const sliceTrailerKey = "Jig-Slice"

// fakeHarness is one agent session working in the fixture clone.
type fakeHarness struct {
	t   *testing.T
	fx  *fixture.Fixture
	env jigEnv

	// Clone is the operator clone the harness works in (the fixture RepoDir).
	Clone string
	// Session is the harness session id, in every payload and in JIG_SESSION
	// for the jig commands the harness runs.
	Session string
	// Agent is the id of the subagent speaking, "" for the main agent; it is
	// the payloads agent_id when set.
	Agent string
	// Transcript is the payloads transcript_path. The file need not exist.
	Transcript string
	// VerifierWait is how long WaitVerifiers waits before it kills.
	VerifierWait time.Duration

	// reportf is how WaitVerifiers reports a worker it had to kill: it fails
	// the test, except in the test of WaitVerifiers itself.
	reportf func(format string, args ...any)
}

// newHarness returns a harness for session working in the clone of fx,
// running jig and git with env. Detached verification workers it left
// running are killed when the test ends.
func newHarness(t *testing.T, fx *fixture.Fixture, env jigEnv, session string) *fakeHarness {
	t.Helper()
	h := &fakeHarness{
		t:            t,
		fx:           fx,
		env:          env,
		Clone:        fx.RepoDir,
		Session:      session,
		Transcript:   filepath.Join(fx.Dir, "transcripts", session+".jsonl"),
		VerifierWait: verifierWait,
		reportf:      t.Errorf,
	}
	t.Cleanup(h.killVerifiers)
	return h
}

// gitEnv is the whole environment the harness runs git and its commands in:
// the jig subprocess environment (so the global git config of a test, from
// withGitGlobal, applies, and a variable the test left out stays out) with
// the pinned identity and dates laid over it.
func (h *fakeHarness) gitEnv() []string {
	return replaceEnv(h.env.environ(), pinnedIdentityEnv)
}

// git runs git in the clone and fails the test on an error.
func (h *fakeHarness) git(args ...string) string {
	h.t.Helper()
	out, err := gitx.RunReplaceEnv(h.Clone, h.gitEnv(), args...)
	if err != nil {
		h.t.Fatalf("harness git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// Branch checks out the branch name in the clone, creating it at the current
// HEAD when it does not exist.
func (h *fakeHarness) Branch(name string) {
	h.t.Helper()
	if _, err := gitx.RunReplaceEnv(h.Clone, h.gitEnv(), "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		h.git("checkout", name)
		return
	}
	h.git("checkout", "-b", name)
}

// CommitScenario applies the patch the fixture scenario scripts for the
// attempt of a slice (slices/SLICE/attempt-N/patch.diff in the fixture
// scenario directory) to the clone and commits it, as the fake session
// backend does: the message is "TICKET SLICE: first line of the attempt
// summary", followed by the trailer "Jig-Slice: SLICE". It returns the new
// commit sha.
func (h *fakeHarness) CommitScenario(slice string, attempt int) string {
	h.t.Helper()
	dir := filepath.Join(h.fx.ScenarioDir, "slices", slice, fmt.Sprintf("attempt-%d", attempt))
	patch := filepath.Join(dir, "patch.diff")
	if _, err := os.Stat(patch); err != nil {
		h.t.Fatalf("CommitScenario(%q, %d): the scenario has no patch: %v", slice, attempt, err)
	}
	msg := fmt.Sprintf("%s %s: %s", h.fx.Ticket, slice, firstLine(scenarioResultSummary(h.t, h.fx.ScenarioDir, slice, strconv.Itoa(attempt))))
	h.git("apply", patch)
	h.git("add", "-A")
	h.git("commit", "-m", msg, "-m", sliceTrailerKey+": "+slice)
	return h.git("rev-parse", "HEAD")
}

// Commit writes files (clone-relative path to content) into the clone and
// commits them with msg. A non-empty slice adds the Jig-Slice trailer for it.
// It returns the new commit sha.
func (h *fakeHarness) Commit(slice, msg string, files map[string]string) string {
	h.t.Helper()
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		full := filepath.Join(h.Clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			h.t.Fatalf("harness Commit: mkdir for %s: %v", rel, err)
		}
		writeFileOrFatal(h.t, full, []byte(files[rel]))
	}
	h.git(append([]string{"add", "--"}, paths...)...)
	args := []string{"commit", "-m", msg}
	if slice != "" {
		args = append(args, "-m", sliceTrailerKey+": "+slice)
	}
	h.git(args...)
	return h.git("rev-parse", "HEAD")
}

// Payload builds the JSON a Claude Code hook sends on stdin for event: the
// session_id, transcript_path, cwd and hook_event_name every event carries,
// and the agent_id of a subagent. fields are laid over that: a key is a
// top-level payload key (tool_name, tool_response, stop_hook_active, prompt
// and so on) with one shorthand, "command", which sets tool_input.command.
func (h *fakeHarness) Payload(event string, fields map[string]any) []byte {
	h.t.Helper()
	payload := map[string]any{
		"session_id":      h.Session,
		"transcript_path": h.Transcript,
		"cwd":             h.Clone,
		"hook_event_name": event,
	}
	if h.Agent != "" {
		payload["agent_id"] = h.Agent
	}
	for key, value := range fields {
		if key != "command" {
			payload[key] = value
		}
	}
	if cmd, ok := fields["command"]; ok {
		input, _ := payload["tool_input"].(map[string]any)
		merged := map[string]any{}
		for k, v := range input {
			merged[k] = v
		}
		merged["command"] = cmd
		payload["tool_input"] = merged
	}
	data, err := json.Marshal(payload)
	if err != nil {
		h.t.Fatalf("harness Payload(%s): %v", event, err)
	}
	return data
}

// Hook fires event at jig observe --harness claude, as an installed hook
// would: Payload(event, fields) on stdin, run from the clone. It returns what
// the hook printed; a hook never fails the harness, so the caller reads the
// result.
func (h *fakeHarness) Hook(event string, fields map[string]any) jigResult {
	h.t.Helper()
	return runJigIn(h.t, h.env, h.Clone, h.Payload(event, fields), "observe", "--harness", "claude")
}

// bashResult is what Bash returns: the output and exit code of the command, and
// what the PostToolUse hook that followed it printed.
type bashResult struct {
	jigResult
	Hook jigResult
}

// Bash runs cmd in the clone as the agent Bash tool would, then fires the
// PostToolUse hook for it with the command and its output. cmd is split into
// words the way a shell splits plain words and quotes; there is no pipe,
// redirection, expansion or chaining. A command that fails is a result, not a
// test failure; one that cannot start fails the test.
func (h *fakeHarness) Bash(cmd string) bashResult {
	h.t.Helper()
	argv, err := splitCommand(cmd)
	if err != nil {
		h.t.Fatalf("harness Bash(%q): %v", cmd, err)
	}
	if len(argv) == 0 {
		h.t.Fatalf("harness Bash(%q): no command", cmd)
	}
	res := h.run(argv)
	hook := h.Hook("PostToolUse", map[string]any{
		"tool_name": "Bash",
		"command":   cmd,
		"tool_response": map[string]any{
			"stdout":      res.Stdout,
			"stderr":      res.Stderr,
			"interrupted": false,
		},
	})
	return bashResult{jigResult: res, Hook: hook}
}

// run executes argv in the clone with the harness environment. git goes
// through gitx, the only place git is started from.
func (h *fakeHarness) run(argv []string) jigResult {
	h.t.Helper()
	if argv[0] == "git" {
		out, err := gitx.RunReplaceEnv(h.Clone, h.gitEnv(), argv[1:]...)
		return jigResult{Stdout: out, Stderr: errText(err), Code: exitCodeOfErr(h.t, err)}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = h.Clone
	cmd.Env = h.gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := exitCodeOfErr(h.t, cmd.Run())
	return jigResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}

// errText is the text of err, or "" for none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// exitCodeOfErr is the exit code an error from running a command stands for:
// 0 for none, the exit code of the process for a non-zero exit. A command that could not
// start fails the test.
func exitCodeOfErr(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("harness: run command: %v", err)
	return -1
}

// The bytes splitCommand looks for.
const (
	singleQuote = 0x27
	doubleQuote = 0x22
	backslash   = 0x5c
)

// splitCommand splits s into words: runs of non-space characters, with single
// quotes (everything literal) and double quotes (a backslash escapes only a
// quote or another backslash) joining what they enclose into the word. A
// backslash outside quotes is an ordinary character, so Windows paths pass.
func splitCommand(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == singleQuote:
			end := strings.IndexByte(s[i+1:], singleQuote)
			if end < 0 {
				return nil, fmt.Errorf("unterminated single quote in %q", s)
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == doubleQuote:
			inWord = true
			closed := false
			for i++; i < len(s); i++ {
				if s[i] == backslash && i+1 < len(s) && (s[i+1] == doubleQuote || s[i+1] == backslash) {
					i++
					cur.WriteByte(s[i])
					continue
				}
				if s[i] == doubleQuote {
					closed = true
					break
				}
				cur.WriteByte(s[i])
			}
			if !closed {
				return nil, fmt.Errorf("unterminated double quote in %q", s)
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// Jig runs jig with args from the clone as this session: JIG_SESSION names it.
func (h *fakeHarness) Jig(args ...string) jigResult {
	h.t.Helper()
	return runJig(h.t, h.env.with("JIG_SESSION="+h.Session), h.Clone, args...)
}

// Push pushes branch from the clone to its origin.
func (h *fakeHarness) Push(branch string) {
	h.t.Helper()
	h.git("push", "origin", branch)
}

// verifierPIDs lists the pids of the detached verification workers the ledger
// says were spawned.
func (h *fakeHarness) verifierPIDs() ([]spawnedWorker, error) {
	lines, err := ledger.Read(h.env.home)
	if err != nil {
		return nil, err
	}
	var workers []spawnedWorker
	for _, l := range lines {
		if l.Event != journal.EventVerifySpawned || l.PID <= 0 {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, l.TS)
		if err != nil || slices.ContainsFunc(workers, func(w spawnedWorker) bool { return w.pid == l.PID }) {
			continue
		}
		workers = append(workers, spawnedWorker{pid: l.PID, at: at})
	}
	return workers, nil
}

// spawnedWorker is a verification worker as its verify-spawned line names
// it: its pid and when the line was written, which is after the worker
// started.
type spawnedWorker struct {
	pid int
	at  time.Time
}

// running reports whether w is still running: a live process under its pid
// that started no later than its line was written. Windows hands a pid out
// again as soon as it is free, so a later process under the same pid is
// another test's jig, git or go, and must never be waited for or killed in
// the worker's place (a force-kill ends it with exit status 1 and no
// output, which is how it showed).
func (w spawnedWorker) running() bool { return startedBy(w.pid, w.at) }

// WaitVerifiers waits for the detached verification workers the ledger
// verify-spawned lines name to exit, up to VerifierWait, and kills (with the
// tree under them) any still running then, reporting each as a test failure:
// a test must not leave a worker behind. Workers spawned later are not seen;
// call it again.
func (h *fakeHarness) WaitVerifiers() {
	h.t.Helper()
	workers, err := h.verifierPIDs()
	if err != nil {
		h.t.Fatalf("WaitVerifiers: read the ledger: %v", err)
	}
	if pollUntil(h.VerifierWait, func() bool { return !slices.ContainsFunc(workers, spawnedWorker.running) }) {
		return
	}
	for _, w := range workers {
		if w.running() {
			h.reportf("verification worker %d was still running %s after the test waited for it; killed", w.pid, h.VerifierWait)
			killTree(w.pid)
		}
	}
}

// killVerifiers kills, without waiting, every verification worker still
// running: the cleanup of a test that did not wait for them.
func (h *fakeHarness) killVerifiers() {
	workers, _ := h.verifierPIDs()
	for _, w := range workers {
		if w.running() {
			killTree(w.pid)
		}
	}
}

// killTree ends the process pid and the processes it started.
func killTree(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = envrun.KillTree(&exec.Cmd{Process: proc})
}
