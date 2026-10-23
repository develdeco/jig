package e2e

// Tests of the e2e scaffold itself: the helpers and the fake harness the
// digester tests are built on. They run against the same jig binary and the
// same fixtures, so a helper that stops doing what its doc comment says is
// found here and not in a ticket test that depends on it.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// envValue is the value a KEY=VALUE environment holds for name, and whether it
// holds the name at all.
func envValue(environ []string, name string) (string, bool) {
	for _, kv := range environ {
		if sameEnvName(envName(kv), name) {
			return strings.TrimPrefix(kv, envName(kv)+"="), true
		}
	}
	return "", false
}

// countEnv is how many entries of environ are named name.
func countEnv(environ []string, name string) int {
	n := 0
	for _, kv := range environ {
		if sameEnvName(envName(kv), name) {
			n++
		}
	}
	return n
}

// TestJigEnvWithAndWithout pins the value semantics and layering of with and
// without: extra entries replace earlier ones of their name (home included),
// an unset name is gone whatever else says it, and neither changes the env
// it was called on.
func TestJigEnvWithAndWithout(t *testing.T) {
	t.Parallel()

	base := jigEnv{home: "/h", userHome: "/u"}
	e := base.with("E2E_A=1", "E2E_B=2").with("E2E_A=3")
	if got, _ := envValue(e.environ(), "E2E_A"); got != "3" || countEnv(e.environ(), "E2E_A") != 1 {
		t.Errorf("E2E_A = %q (%d entries), want one entry, 3: a later with replaces", got, countEnv(e.environ(), "E2E_A"))
	}
	if got, _ := envValue(e.environ(), "E2E_B"); got != "2" {
		t.Errorf("E2E_B = %q, want 2", got)
	}
	if len(base.extra) != 0 || len(base.unset) != 0 {
		t.Errorf("with changed the env it was called on: %+v", base)
	}

	other := base.with("HOME=/elsewhere")
	if got, _ := envValue(other.environ(), "HOME"); got != "/elsewhere" || countEnv(other.environ(), "HOME") != 1 {
		t.Errorf("HOME = %q (%d entries), want one entry, /elsewhere: extra replaces userHome", got, countEnv(other.environ(), "HOME"))
	}
	if got, _ := envValue(other.environ(), "USERPROFILE"); got != "/u" {
		t.Errorf("USERPROFILE = %q, want /u: only HOME was replaced", got)
	}

	gone := e.without("E2E_A", "PATH")
	if _, ok := envValue(gone.environ(), "E2E_A"); ok {
		t.Errorf("E2E_A is still set after without")
	}
	if _, ok := envValue(gone.environ(), "PATH"); ok {
		t.Errorf("PATH is still set after without: a variable the process has must be removable")
	}
	if got, _ := envValue(gone.environ(), "E2E_B"); got != "2" {
		t.Errorf("E2E_B = %q after an unrelated without, want 2", got)
	}
	if _, ok := envValue(e.environ(), "PATH"); !ok {
		t.Errorf("without changed the env it was called on")
	}

	back := gone.with("E2E_A=again")
	if got, _ := envValue(back.environ(), "E2E_A"); got != "again" {
		t.Errorf("E2E_A = %q after with following without, want it set again", got)
	}
	if _, ok := envValue(gone.with("PATH=/x").environ(), "PATH"); !ok {
		t.Errorf("PATH stayed unset after with named it")
	}

	defer func() {
		if recover() == nil {
			t.Errorf("with accepted an entry that is not KEY=VALUE")
		}
	}()
	base.with("NOEQUALS")
}

// TestWithGitGlobalIsPerTest pins that withGitGlobal gives a test a global git
// config of its own that still includes the hermetic one: a setting written
// with git config --global lands in the test file and nowhere shared.
func TestWithGitGlobalIsPerTest(t *testing.T) {
	t.Parallel()

	hermetic := os.Getenv("GIT_CONFIG_GLOBAL")
	env := jigEnv{home: t.TempDir()}.withGitGlobal(t)
	own, _ := envValue(env.environ(), "GIT_CONFIG_GLOBAL")
	if own == "" || own == hermetic {
		t.Fatalf("GIT_CONFIG_GLOBAL = %q, want a file of the test own, not the shared %q", own, hermetic)
	}

	dir := t.TempDir()
	if _, err := gitx.RunReplaceEnv(dir, env.environ(), "config", "--global", "core.hooksPath", "/some/hooks"); err != nil {
		t.Fatalf("git config --global: %v", err)
	}
	if got, err := gitx.RunReplaceEnv(dir, env.environ(), "config", "--global", "--get", "core.hooksPath"); err != nil || got != "/some/hooks" {
		t.Errorf("core.hooksPath = %q, %v, want /some/hooks in the test file", got, err)
	}
	if got, err := gitx.RunReplaceEnv(dir, env.environ(), "config", "--global", "--includes", "--get", "maintenance.auto"); err != nil || got != "false" {
		t.Errorf("maintenance.auto = %q, %v, want false through the include of the hermetic config", got, err)
	}
	shared := readFileOrFatal(t, hermetic)
	if strings.Contains(string(shared), "hooksPath") {
		t.Errorf("the shared hermetic config took the test setting:\n%s", shared)
	}
	if _, err := gitx.RunReplaceEnv(dir, jigEnv{home: t.TempDir()}.environ(), "config", "--global", "--get", "core.hooksPath"); err == nil {
		t.Errorf("another test sees the core.hooksPath this test installed")
	}
}

// TestWithoutIdentityLetsConfigDecideTheAuthor pins that withoutIdentity drops
// the pinned author and committer names and emails and keeps the dates, so a
// commit takes the identity from git config.
func TestWithoutIdentityLetsConfigDecideTheAuthor(t *testing.T) {
	t.Parallel()

	env := jigEnv{home: t.TempDir()}
	pinned, _ := envValue(env.environ(), "GIT_AUTHOR_DATE")
	if pinned == "" {
		t.Fatal("the test binary pins no GIT_AUTHOR_DATE: TestMain must call gittest.PinIdentity")
	}
	bare := env.withoutIdentity()
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		if _, ok := envValue(bare.environ(), name); ok {
			t.Errorf("%s is still set after withoutIdentity", name)
		}
	}
	for _, name := range []string{"GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
		if _, ok := envValue(bare.environ(), name); !ok {
			t.Errorf("%s was dropped by withoutIdentity, want the dates kept", name)
		}
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Store Person"},
		{"config", "user.email", "store.person@example.invalid"},
		{"commit", "--allow-empty", "-m", "by config"},
	} {
		if _, err := gitx.RunReplaceEnv(dir, bare.environ(), args...); err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
	if got, err := gitx.Run(dir, "log", "-1", "--format=%an <%ae>"); err != nil || got != "Store Person <store.person@example.invalid>" {
		t.Errorf("author = %q, %v, want the configured identity", got, err)
	}
}

// TestHelpersStdinAndStart pins the two ways a test feeds a jig process
// input: runJigIn hands a payload to a process that reads stdin to the end,
// and startJig keeps a process running that the test writes to and reads
// lines from. Both are tried against the hook verb observe, which reads a
// harness's payload on stdin: given a SessionStart for a session in the
// fixture's clone it answers with the context to start the session with, and
// given nothing it prints nothing.
func TestHelpersStdinAndStart(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dir := t.TempDir()

	payload, err := json.Marshal(map[string]any{
		"session_id": "s-probe", "cwd": fx.RepoDir, "hook_event_name": "SessionStart", "source": "startup",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := runJigIn(t, env, dir, payload, "observe", "--harness", "claude")
	if res.Code != 0 || !strings.Contains(res.Stdout, `"hookEventName":"SessionStart"`) || !strings.Contains(res.Stdout, "s-probe") {
		t.Errorf("runJigIn(observe, a SessionStart) = exit %d, %q, want exit 0 and the session's context", res.Code, res.Stdout)
	}
	if res := runJigIn(t, env, dir, nil, "observe", "--harness", "claude"); res.Code != 0 || res.Stdout != "" {
		t.Errorf("runJigIn(observe) with no stdin = exit %d, %q, want exit 0 and nothing printed", res.Code, res.Stdout)
	}

	// A process that runs to completion: its first line, then the rest, then
	// nothing more.
	version := startJig(t, env, dir, "version")
	first, ok := version.Line(30 * time.Second)
	if !ok || first != "version:" {
		t.Errorf("startJig(version) first line = %q, %v, want %q", first, ok, "version:")
	}
	if line, ok := version.Line(30 * time.Second); !ok || !strings.HasPrefix(line, "  version: ") {
		t.Errorf("startJig(version) second line = %q, %v, want the version row", line, ok)
	}
	got := version.Stop()
	if got.Code != 0 || !strings.HasPrefix(got.Stdout, "version:") || !strings.Contains(got.Stdout, "help[1]:") {
		t.Errorf("Stop() = exit %d, %q, want exit 0 and all of the output, the lines already read included", got.Code, got.Stdout)
	}
	if again := version.Stop(); again != got {
		t.Errorf("a second Stop() = %+v, want the first result %+v", again, got)
	}

	// A process that reads its stdin: written to, then closed, it answers.
	hook := startJig(t, env, dir, "observe", "--harness", "claude")
	hook.Write(payload)
	hook.CloseStdin()
	line, ok := hook.Line(30 * time.Second)
	if !ok || !strings.Contains(line, `"hookEventName":"SessionStart"`) {
		t.Errorf("startJig(observe) line = %q, %v, want the session's context", line, ok)
	}
	if _, ok := hook.Line(30 * time.Second); ok {
		t.Errorf("startJig(observe) had a second line, want the output to end")
	}
	if res := hook.Stop(); res.Code != 0 {
		t.Errorf("startJig(observe).Stop() exit = %d, want 0", res.Code)
	}

	// A process still waiting for input is ended by Stop: it closes stdin and
	// the process leaves with nothing to say.
	idle := startJig(t, env, dir, "observe", "--harness", "claude")
	if line, ok := idle.Line(100 * time.Millisecond); ok {
		t.Errorf("an idle startJig(observe) had a line %q", line)
	}
	if res := idle.Stop(); res.Code != 0 || res.Stdout != "" {
		t.Errorf("idle Stop() = exit %d, %q, want exit 0 and no output", res.Code, res.Stdout)
	}
}

// TestWaitHelpers pins the polling helpers: waitFor returns once its
// condition turns true, pollUntil reports a condition that never does, and
// waitJournal finds a line that exists already and one appended while it
// waits.
func TestWaitHelpers(t *testing.T) {
	t.Parallel()
	fx, _ := newFixture(t, fixture.Opts{})

	var ready atomic.Bool
	go func() {
		time.Sleep(120 * time.Millisecond)
		ready.Store(true)
	}()
	start := time.Now()
	waitFor(t, 30*time.Second, "the flag", ready.Load)
	if time.Since(start) < 100*time.Millisecond {
		t.Errorf("waitFor returned before its condition held")
	}
	if pollUntil(80*time.Millisecond, func() bool { return false }) {
		t.Errorf("pollUntil reported a condition that never held")
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventNote, Summary: "first"}); err != nil {
		t.Fatalf("journal.Append: %v", err)
	}
	isNote := func(text string) func(journal.Line) bool {
		return func(l journal.Line) bool { return l.Event == journal.EventNote && l.Summary == text }
	}
	if got := waitJournal(t, fx, fx.Ticket, 30*time.Second, isNote("first")); got.Summary != "first" {
		t.Errorf("waitJournal found %+v, want the first note", got)
	}
	go func() {
		time.Sleep(120 * time.Millisecond)
		if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventNote, Summary: "later"}); err != nil {
			t.Errorf("journal.Append: %v", err)
		}
	}()
	if got := waitJournal(t, fx, fx.Ticket, 30*time.Second, isNote("later")); got.Summary != "later" {
		t.Errorf("waitJournal found %+v, want the note appended while it waited", got)
	}
}

// TestReadLedgerReadsTheEnvsHome pins that readLedger reads the ledger under
// the jig home of the env it is given, and that an empty ledger reads as none.
func TestReadLedgerReadsTheEnvsHome(t *testing.T) {
	t.Parallel()
	_, env := newFixture(t, fixture.Opts{})
	if got := readLedger(t, env); len(got) != 0 {
		t.Fatalf("a fresh jig home has ledger lines %+v", got)
	}
	for _, id := range []string{"s-1", "s-2"} {
		if err := ledger.Append(env.home, journal.Line{Event: journal.EventSessionStart, Session: id}); err != nil {
			t.Fatalf("ledger.Append: %v", err)
		}
	}
	got := readLedger(t, env)
	if len(got) != 2 || got[0].Session != "s-1" || got[1].Event != journal.EventSessionStart {
		t.Errorf("readLedger = %+v, want the two session-start lines in order", got)
	}
}

// trailerOf is the Jig-Slice trailer value of the commit sha in dir.
func trailerOf(t *testing.T, dir, sha string) string {
	t.Helper()
	return gitLog(t, dir, "log", "-1", "--format=%(trailers:key=Jig-Slice,valueonly)", sha)
}

// TestHarnessCommitHasTrailer pins what the fake harness leaves in the clone:
// a scenario patch committed under the message the fake backend would write
// and the Jig-Slice trailer, a hand commit with or without a trailer, branch
// switching that keeps the commits of a branch, and a push that reaches the
// origin.
func TestHarnessCommitHasTrailer(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-harness")
	branch := "jig/" + fx.Ticket

	h.Branch(branch)
	if got := gitLog(t, fx.RepoDir, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Fatalf("after Branch the clone is on %q, want %q", got, branch)
	}
	base := gitLog(t, fx.RepoDir, "rev-parse", "HEAD")

	sha := h.CommitScenario("a", 1)
	if head := gitLog(t, fx.RepoDir, "rev-parse", "HEAD"); head != sha {
		t.Errorf("CommitScenario returned %s, HEAD is %s", sha, head)
	}
	if parent := gitLog(t, fx.RepoDir, "rev-parse", sha+"^"); parent != base {
		t.Errorf("the scenario commit sits on %s, want the branch base %s", parent, base)
	}
	if got := trailerOf(t, fx.RepoDir, sha); got != "a" {
		t.Errorf("Jig-Slice trailer = %q, want a", got)
	}
	wantSubject := fx.Ticket + " a: " + firstLine(scenarioResultSummary(t, fx.ScenarioDir, "a", "1"))
	if got := gitLog(t, fx.RepoDir, "log", "-1", "--format=%s", sha); got != wantSubject {
		t.Errorf("subject = %q, want the fake backend message %q", got, wantSubject)
	}
	if alpha := readFileOrFatal(t, filepath.Join(fx.RepoDir, "alpha", "alpha.go")); !strings.Contains(string(alpha), "if v > hi") {
		t.Errorf("the scenario patch is not in the working tree:\n%s", alpha)
	}
	if author := gitLog(t, fx.RepoDir, "log", "-1", "--format=%an", sha); author != "e2e-script" {
		t.Errorf("author = %q, want the pinned e2e-script identity", author)
	}

	byHand := h.Commit("b", "b: by hand", map[string]string{"notes/b.txt": "hand written"})
	if got := trailerOf(t, fx.RepoDir, byHand); got != "b" {
		t.Errorf("hand commit trailer = %q, want b", got)
	}
	noSlice := h.Commit("", "no slice", map[string]string{"notes/c.txt": "other"})
	if got := trailerOf(t, fx.RepoDir, noSlice); got != "" {
		t.Errorf("a commit for no slice has trailer %q, want none", got)
	}

	h.Branch("elsewhere")
	h.Branch(branch)
	if head := gitLog(t, fx.RepoDir, "rev-parse", "HEAD"); head != noSlice {
		t.Errorf("returning to %s found HEAD %s, want its commits kept (%s)", branch, head, noSlice)
	}
	h.Push(branch)
	if got := gitLog(t, fx.RepoRemote, "rev-parse", branch); got != noSlice {
		t.Errorf("origin %s = %s after Push, want %s", branch, got, noSlice)
	}

	if res := h.Jig("version"); res.Code != 0 || !strings.HasPrefix(res.Stdout, "version:") {
		t.Errorf("Jig(version) = exit %d, %q, want its output", res.Code, res.Stdout)
	}
}

// TestHarnessPayloadShape pins the JSON the harness hands a hook: the fields
// every Claude Code event carries, the agent id only for a subagent, and the
// command shorthand merging into tool_input.
func TestHarnessPayloadShape(t *testing.T) {
	t.Parallel()
	h := &fakeHarness{t: t, Clone: "/the/clone", Session: "s-1", Transcript: "/the/transcript.jsonl"}
	decode := func(data []byte) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("payload is not JSON: %v: %s", err, data)
		}
		return m
	}

	p := decode(h.Payload("PostToolUse", map[string]any{
		"tool_name":        "Bash",
		"command":          "git commit -m x",
		"tool_response":    map[string]any{"stdout": "done"},
		"stop_hook_active": true,
	}))
	for key, want := range map[string]any{
		"session_id":       "s-1",
		"transcript_path":  "/the/transcript.jsonl",
		"cwd":              "/the/clone",
		"hook_event_name":  "PostToolUse",
		"tool_name":        "Bash",
		"stop_hook_active": true,
	} {
		if p[key] != want {
			t.Errorf("payload %s = %v, want %v", key, p[key], want)
		}
	}
	if input, _ := p["tool_input"].(map[string]any); input["command"] != "git commit -m x" {
		t.Errorf("payload tool_input = %v, want the command in it", p["tool_input"])
	}
	if resp, _ := p["tool_response"].(map[string]any); resp["stdout"] != "done" {
		t.Errorf("payload tool_response = %v, want it passed through", p["tool_response"])
	}
	if _, ok := p["agent_id"]; ok {
		t.Errorf("the main agent payload has agent_id %v, want none", p["agent_id"])
	}

	h.Agent = "agent-7"
	p = decode(h.Payload("Stop", map[string]any{
		"tool_input": map[string]any{"description": "d"},
		"command":    "x",
	}))
	if p["agent_id"] != "agent-7" {
		t.Errorf("subagent payload agent_id = %v, want agent-7", p["agent_id"])
	}
	if input, _ := p["tool_input"].(map[string]any); input["description"] != "d" || input["command"] != "x" {
		t.Errorf("tool_input = %v, want the given map with the command merged in", p["tool_input"])
	}
}

// TestSplitCommand pins how Bash splits a command into words.
func TestSplitCommand(t *testing.T) {
	t.Parallel()
	bs := string(rune(92)) // a backslash
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"git status", []string{"git", "status"}},
		{"  git   status  ", []string{"git", "status"}},
		{`git commit -m "two words"`, []string{"git", "commit", "-m", "two words"}},
		{"git commit -m 'it is $HOME'", []string{"git", "commit", "-m", "it is $HOME"}},
		{`echo a"b c"d`, []string{"echo", "ab cd"}},
		{`echo "say ` + bs + `"hi` + bs + `""`, []string{"echo", `say "hi"`}},
		{"echo C:" + bs + "dir" + bs + "file", []string{"echo", "C:" + bs + "dir" + bs + "file"}},
		{`echo ""`, []string{"echo", ""}},
		{"", nil},
	} {
		got, err := splitCommand(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("splitCommand(%q) = %q, %v, want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{`echo "open`, "echo 'open"} {
		if got, err := splitCommand(bad); err == nil {
			t.Errorf("splitCommand(%q) = %q, want an unterminated-quote error", bad, got)
		}
	}
}

// TestHarnessBashRunsTheCommandInTheClone pins that Bash runs the command in
// the clone with the harness environment, reports its output and exit code
// without failing the test, and goes through the same hook path as Hook.
func TestHarnessBashRunsTheCommandInTheClone(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-bash")

	head := gitLog(t, fx.RepoDir, "rev-parse", "HEAD")
	if res := h.Bash("git rev-parse HEAD"); res.Code != 0 || res.Stdout != head {
		t.Errorf("Bash(git rev-parse HEAD) = exit %d, %q, want 0 and %s", res.Code, res.Stdout, head)
	}
	if res := h.Bash(`git commit --allow-empty -m "two words"`); res.Code != 0 {
		t.Errorf("Bash(git commit) exit = %d, want 0: %s", res.Code, res.Stderr)
	}
	if got := gitLog(t, fx.RepoDir, "log", "-1", "--format=%s"); got != "two words" {
		t.Errorf("the quoted message became %q, want it kept as one argument", got)
	}
	if res := h.Bash("git no-such-subcommand"); res.Code == 0 || res.Stderr == "" {
		t.Errorf("Bash(git no-such-subcommand) = exit %d, %q, want a failure that says why", res.Code, res.Stderr)
	}
	if res := h.Bash("go version"); res.Code != 0 || !strings.HasPrefix(res.Stdout, "go version") {
		t.Errorf("Bash(go version) = exit %d, %q, want the go toolchain answered", res.Code, res.Stdout)
	}
}

// longLived starts a process that stays up until it is killed, and returns it
// with a channel closed once it has exited and been reaped.
func longLived(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "-n", "120", "127.0.0.1")
	} else {
		cmd = exec.Command("sleep", "120")
	}
	return startReaped(t, cmd)
}

// shortLived starts a process that exits at once, and returns it with a
// channel closed once it has exited and been reaped.
func shortLived(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", "exit", "0")
	} else {
		cmd = exec.Command("true")
	}
	return startReaped(t, cmd)
}

func startReaped(t *testing.T, cmd *exec.Cmd) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", cmd.Args, err)
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-done
	})
	return cmd, done
}

// TestHarnessWaitVerifiers pins WaitVerifiers against the ledger: a worker
// that has exited is waited for without complaint, one still running when
// the wait is out is reported and killed, and nothing is left to report on a
// second call.
func TestHarnessWaitVerifiers(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-wait")
	h.VerifierWait = 400 * time.Millisecond
	var reports []string
	h.reportf = func(format string, args ...any) { reports = append(reports, fmt.Sprintf(format, args...)) }

	h.WaitVerifiers() // no ledger at all
	if len(reports) != 0 {
		t.Fatalf("WaitVerifiers with no workers reported %q", reports)
	}

	quick, quickDone := shortLived(t)
	stuck, stuckDone := longLived(t)
	<-quickDone
	for _, pid := range []int{quick.Process.Pid, stuck.Process.Pid} {
		if err := ledger.Append(env.home, journal.Line{Event: journal.EventVerifySpawned, PID: pid}); err != nil {
			t.Fatalf("ledger.Append: %v", err)
		}
	}

	h.WaitVerifiers()
	if len(reports) != 1 || !strings.Contains(reports[0], strconv.Itoa(stuck.Process.Pid)) {
		t.Fatalf("WaitVerifiers reported %q, want exactly the worker still running (%d)", reports, stuck.Process.Pid)
	}
	select {
	case <-stuckDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the worker still running was not killed")
	}

	h.WaitVerifiers()
	if len(reports) != 1 {
		t.Errorf("a second WaitVerifiers reported again: %q", reports)
	}
}

// TestHarnessNeverKillsAProcessStartedAfterTheWorkerRecord: a verify-spawned
// line names a pid, and the pid can belong to another process by the time a
// test ends (Windows reuses them at once). A process that started after the
// line was written is not the worker: the harness neither waits for it nor
// kills it.
func TestHarnessNeverKillsAProcessStartedAfterTheWorkerRecord(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-reused")
	h.VerifierWait = 400 * time.Millisecond
	var reports []string
	h.reportf = func(format string, args ...any) { reports = append(reports, fmt.Sprintf(format, args...)) }

	other, otherDone := longLived(t)
	earlier := time.Now().Add(-time.Minute)
	if err := ledger.Append(env.home, journal.Line{TS: ledger.Stamp(earlier), Event: journal.EventVerifySpawned, PID: other.Process.Pid}); err != nil {
		t.Fatalf("ledger.Append: %v", err)
	}

	h.WaitVerifiers()
	h.killVerifiers()
	if len(reports) != 0 {
		t.Errorf("WaitVerifiers reported %q for a process that started after the worker's line", reports)
	}
	select {
	case <-otherDone:
		t.Fatal("the harness killed a process that started after the worker's line")
	case <-time.After(500 * time.Millisecond):
	}
	if !pidAlive(other.Process.Pid) {
		t.Fatal("the later process is gone")
	}
}
