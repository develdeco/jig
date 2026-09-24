package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/outcome"
)

func TestRulePath(t *testing.T) {
	cases := []struct {
		goos, path, want string
	}{
		{"windows", `C:\Users\a\lease`, "//c/Users/a/lease"},
		{"windows", `D:\DECO\store\T-1\work\a.attempt-1.result.json`, "//d/DECO/store/T-1/work/a.attempt-1.result.json"},
		{"windows", `c:\w [1] (x) y`, `//c/w \[1\] (x) y`},
		{"linux", "/home/a/lease", "//home/a/lease"},
		{"linux", "/tmp/a*b?c[d]", `//tmp/a\*b\?c\[d\]`},
		{"linux", `/tmp/back\slash`, `//tmp/back\\slash`},
		{"darwin", "/private/var/folders/x", "//private/var/folders/x"},
	}
	for _, c := range cases {
		if got := rulePath(c.goos, c.path); got != c.want {
			t.Errorf("rulePath(%s, %q) = %q, want %q", c.goos, c.path, got, c.want)
		}
	}
}

// TestPathFormsSymlink checks that a path reached through a symlink yields
// both the given and the resolved form, for an existing path and for a
// not-yet-written file under a symlinked directory.
func TestPathFormsSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := pathForms(link), []string{link, realResolved}; !reflect.DeepEqual(got, want) {
		t.Errorf("pathForms(link) = %v, want %v", got, want)
	}
	file := filepath.Join(link, "result.json")
	if got, want := pathForms(file), []string{file, filepath.Join(realResolved, "result.json")}; !reflect.DeepEqual(got, want) {
		t.Errorf("pathForms(link/result.json) = %v, want %v", got, want)
	}
}

// missingDispatch returns a dispatch whose worktree and result paths sit
// under a directory that does not exist, so pathForms yields exactly one
// form per path and the rendered settings are exact on every OS.
func missingDispatch(t *testing.T, screen bool) Dispatch {
	root := filepath.Join(t.TempDir(), "missing")
	return Dispatch{
		Ticket:     "T-1",
		Slice:      "a",
		Attempt:    1,
		Worktree:   filepath.Join(root, "lease"),
		SliceJSON:  filepath.Join(root, "store", "T-1", "work", "a.attempt-1.slice.json"),
		ResultJSON: filepath.Join(root, "store", "T-1", "work", "a.attempt-1.result.json"),
		Model:      "claude-haiku-4-5",
		Prompt:     "-do the slice",
		Screen:     screen,
	}
}

// TestHeadlessSettings pins the permission model's settings: edit rules
// for the lease worktree and the result file only, and - when screened -
// the exec-form `jig _screen` hook as the only grant for the shell and read
// tools, which get no allow rule of their own. Unscreened, those tools get
// plain allow rules and there is no hook.
func TestHeadlessSettings(t *testing.T) {
	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}

	d := missingDispatch(t, true)
	edits := []string{
		"Edit(" + rulePath("linux", d.Worktree) + "/**)",
		"Edit(" + rulePath("linux", d.ResultJSON) + ")",
	}
	raw, err := b.settings(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	want := map[string]any{
		"permissions": map[string]any{"allow": toAny(edits)},
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/opt/jig/bin/jig", "args": []any{"_screen"}},
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("screened settings =\n%s\nwant\n%v", raw, want)
	}

	d.Screen = false
	raw, err = b.settings(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	got = nil
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	want = map[string]any{
		"permissions": map[string]any{"allow": toAny(append(edits, "Bash", "Read", "Glob", "Grep"))},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unscreened settings =\n%s\nwant\n%v", raw, want)
	}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// TestHeadlessArgs pins the argv: print mode with the json result format,
// dontAsk permission mode, the restricted tool surface, no MCP servers, the
// generated settings, and the prompt last after "--" (this one starts with
// a dash, which must not parse as a flag).
func TestHeadlessArgs(t *testing.T) {
	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}
	d := missingDispatch(t, true)
	settings, err := b.settings(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	got, cleanup, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	defer cleanup()
	want := []string{
		"-p", "--output-format", "json",
		"--model", "claude-haiku-4-5",
		"--permission-mode", "dontAsk",
		"--tools", "Bash,Read,Glob,Grep,Edit,Write,NotebookEdit",
		"--strict-mcp-config",
		"--setting-sources", "user",
		"--settings", settings,
		"--", "-do the slice",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args =\n%q\nwant\n%q", got, want)
	}

	d.Model = ""
	got, cleanup2, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	defer cleanup2()
	for _, a := range got {
		if a == "--model" {
			t.Errorf("args with no model carry --model: %q", got)
		}
	}
}

// TestHeadlessScreenBinary checks the screen-hook binary resolution. A test
// binary is not jig, so without Options.ScreenBinary a screened dispatch
// refuses to register it as the hook, while an unscreened one - which runs
// no hook - still renders. With Options.ScreenBinary the hook runs exactly
// that binary.
func TestHeadlessScreenBinary(t *testing.T) {
	backend, err := New("headless", Options{})
	if err != nil {
		t.Fatalf("New(headless): %v", err)
	}
	b := backend.(*headlessBackend)
	if _, err := b.settings(missingDispatch(t, true)); err == nil || !strings.Contains(err.Error(), "Options.ScreenBinary") {
		t.Fatalf("screened settings from a test binary = %v, want an error naming Options.ScreenBinary", err)
	}
	if _, err := b.settings(missingDispatch(t, false)); err != nil {
		t.Fatalf("unscreened settings from a test binary: %v", err)
	}

	backend, err = New("headless", Options{ScreenBinary: "/opt/jig/bin/jig"})
	if err != nil {
		t.Fatalf("New(headless, ScreenBinary): %v", err)
	}
	raw, err := backend.(*headlessBackend).settings(missingDispatch(t, true))
	if err != nil {
		t.Fatalf("screened settings with ScreenBinary: %v", err)
	}
	if !strings.Contains(raw, `"args":["_screen"],"command":"/opt/jig/bin/jig"`) {
		t.Errorf("screened settings do not run /opt/jig/bin/jig _screen:\n%s", raw)
	}
}

// claudeStubRun is one headless Run against the claude stub: the dispatch
// it ran and the error Run returned.
type claudeStubRun struct {
	d   Dispatch
	err error
}

// runClaudeStub runs a headless dispatch against testdata/fixture/claudestub
// on PATH, configured by env (CLAUDE_STUB_*), and checks the stub was
// invoked exactly once, in the lease worktree, with the backend's argv.
func runClaudeStub(t *testing.T, env map[string]string) claudeStubRun {
	t.Helper()
	stubDir := buildBinary(t, filepath.Join("testdata", "fixture", "claudestub"), "claude")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "claude.log")
	t.Setenv("CLAUDE_STUB_LOG", logFile)
	for _, k := range []string{"CLAUDE_STUB_WRITE_PATH", "CLAUDE_STUB_WRITE_BODY", "CLAUDE_STUB_STDOUT", "CLAUDE_STUB_STDERR", "CLAUDE_STUB_EXIT"} {
		t.Setenv(k, env[k])
	}

	worktree := t.TempDir()
	work := filepath.Join(t.TempDir(), "T-1", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	d := Dispatch{
		Ticket:     "T-1",
		Slice:      "a",
		Attempt:    1,
		Worktree:   worktree,
		SliceJSON:  filepath.Join(work, "a.attempt-1.slice.json"),
		ResultJSON: filepath.Join(work, "a.attempt-1.result.json"),
		Model:      "claude-haiku-4-5",
		Prompt:     "do the slice",
		Screen:     true,
	}
	if env["CLAUDE_STUB_WRITE_PATH"] == "result" {
		t.Setenv("CLAUDE_STUB_WRITE_PATH", d.ResultJSON)
	}

	b := &headlessBackend{goos: "linux", screenBinary: builtJigBinary(t)}
	runErr := b.Run(d)

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read claude stub log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("claude stub ran %d times, want 1:\n%s", len(lines), data)
	}
	var call struct {
		Argv []string `json:"argv"`
		Cwd  string   `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &call); err != nil {
		t.Fatalf("parse claude stub log: %v", err)
	}
	wantArgs, wantCleanup, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	defer wantCleanup()
	if !reflect.DeepEqual(call.Argv[1:], wantArgs) {
		t.Errorf("claude argv =\n%q\nwant\n%q", call.Argv[1:], wantArgs)
	}
	if !sameDir(t, call.Cwd, worktree) {
		t.Errorf("claude ran in %s, want the lease worktree %s", call.Cwd, worktree)
	}
	return claudeStubRun{d: d, err: runErr}
}

func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(ai, bi)
}

// cliResultJSON renders a `claude -p --output-format json` result object.
func cliResultJSON(t *testing.T, isError bool, result string, denials []cliDenial) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "is_error": isError,
		"result": result, "permission_denials": denials, "session_id": "s-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// TestHeadlessRunSessionWroteResult: a session that wrote result.json has
// honored the disk contract, even with a non-zero exit; the backend leaves
// the file exactly as written.
func TestHeadlessRunSessionWroteResult(t *testing.T) {
	body := `{"outcome":"green","summary":"done","commit":"abc"}`
	run := runClaudeStub(t, map[string]string{
		"CLAUDE_STUB_WRITE_PATH": "result",
		"CLAUDE_STUB_WRITE_BODY": body,
		"CLAUDE_STUB_EXIT":       "1",
	})
	if run.err != nil {
		t.Fatalf("Run: %v", run.err)
	}
	if got, err := os.ReadFile(run.d.ResultJSON); err != nil || string(got) != body {
		t.Errorf("result.json = %q (%v), want the session's own %q", got, err, body)
	}
}

// TestHeadlessRunFallbackFinalMessage: a completed session that wrote no
// result.json gets one parsed from its final message only.
func TestHeadlessRunFallbackFinalMessage(t *testing.T) {
	final := "All done.\n```json\n{\"outcome\":\"green\",\"summary\":\"from the final message\"}\n```"
	run := runClaudeStub(t, map[string]string{
		"CLAUDE_STUB_STDOUT": "some stray line\n" + cliResultJSON(t, false, final, nil),
	})
	if run.err != nil {
		t.Fatalf("Run: %v", run.err)
	}
	res := readResult(t, run.d.ResultJSON)
	if res.Outcome != outcome.Green || res.Summary != "from the final message" {
		t.Errorf("fallback result = %+v, want green from the final message", res)
	}
}

// TestHeadlessRunFallbackNamesDenials: a session that could not write its
// result.json - here because the permission system denied the write - gets
// a failed result whose summary names the denied calls.
func TestHeadlessRunFallbackNamesDenials(t *testing.T) {
	denials := []cliDenial{
		{ToolName: "Write", ToolInput: map[string]any{"file_path": "/store/T-1/work/a.attempt-1.result.json", "content": "{}"}},
		{ToolName: "Bash", ToolInput: map[string]any{"command": "curl example.invalid"}},
	}
	run := runClaudeStub(t, map[string]string{
		"CLAUDE_STUB_STDOUT": cliResultJSON(t, false, "I could not write the result file.", denials),
	})
	if run.err != nil {
		t.Fatalf("Run: %v", run.err)
	}
	res := readResult(t, run.d.ResultJSON)
	want := "no result block; denied tool calls: Write /store/T-1/work/a.attempt-1.result.json, Bash curl example.invalid"
	if res.Outcome != outcome.Failed || res.Summary != want {
		t.Errorf("fallback result = %+v, want failed with summary %q", res, want)
	}
}

// TestHeadlessRunSessionError: a session the CLI ended in error (expired
// credentials, an API failure) is an infrastructure error carrying the
// CLI's message, and no result.json is synthesized.
func TestHeadlessRunSessionError(t *testing.T) {
	run := runClaudeStub(t, map[string]string{
		"CLAUDE_STUB_STDOUT": cliResultJSON(t, true, "Failed to authenticate: OAuth session expired and could not be refreshed", nil),
	})
	if run.err == nil || !strings.Contains(run.err.Error(), "OAuth session expired") {
		t.Fatalf("Run = %v, want an error carrying the CLI's message", run.err)
	}
	if _, err := os.Stat(run.d.ResultJSON); !os.IsNotExist(err) {
		t.Errorf("result.json exists after a session error (stat: %v)", err)
	}
}

// TestHeadlessRunNoSession: a CLI that ran no session at all - it rejected
// a flag and printed no result object - is an infrastructure error carrying
// its exit status and stderr, and no result.json is synthesized.
func TestHeadlessRunNoSession(t *testing.T) {
	run := runClaudeStub(t, map[string]string{
		"CLAUDE_STUB_STDERR": "Error: When using --print, --output-format=stream-json requires --verbose\n",
		"CLAUDE_STUB_EXIT":   "1",
	})
	if run.err == nil {
		t.Fatal("Run = nil, want an error")
	}
	for _, want := range []string{"exit 1", "requires --verbose"} {
		if !strings.Contains(run.err.Error(), want) {
			t.Errorf("Run error %q does not mention %q", run.err, want)
		}
	}
	if _, err := os.Stat(run.d.ResultJSON); !os.IsNotExist(err) {
		t.Errorf("result.json exists after a CLI that ran no session (stat: %v)", err)
	}
}

func readResult(t *testing.T, path string) outcome.Result {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	return outcome.ParseJSON("slice", data)
}

// builtJigBinary builds cmd/jig once per test binary and returns the path
// to it. A screened dispatch runs the real `jig _screen` hook, so the tests
// that drive one exercise the same binary production resolves through build
// info rather than a stand-in.
func builtJigBinary(t *testing.T) string {
	t.Helper()
	dir := buildBinary(t, filepath.Join("cmd", "jig"), "jig")
	name := "jig"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// TestHeadlessScreenProbeRefusesABrokenHook pins the precondition behind
// the model's central claim. Claude Code skips a hook it cannot launch, and
// its own read-only classifier still grants part of the shell, so a screened
// session whose hook is missing, fails, says nothing, or answers "allow" to
// a call the screen must deny would run with no screen and nothing saying
// so. Each broken state must stop the dispatch before the CLI is started.
func TestHeadlessScreenProbeRefusesABrokenHook(t *testing.T) {
	stubDir := buildBinary(t, filepath.Join("testdata", "fixture", "screenstub"), "screenstub")
	stub := filepath.Join(stubDir, "screenstub")
	if runtime.GOOS == "windows" {
		stub += ".exe"
	}

	cases := []struct {
		name string
		bin  string
		mode string
		want string
	}{
		{"missing binary", filepath.Join(t.TempDir(), "not-jig"), "", "unusable"},
		{"exits non-zero", stub, "exit1", "it failed"},
		{"prints no decision", stub, "garbage", "not the hook's JSON decision"},
		{"prints nothing", stub, "silent", "not the hook's JSON decision"},
		{"allows a call it must deny", stub, "allow", `answered "allow"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SCREEN_STUB_MODE", c.mode)
			claudeDir := buildBinary(t, filepath.Join("testdata", "fixture", "claudestub"), "claude")
			t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			logFile := filepath.Join(t.TempDir(), "claude.log")
			t.Setenv("CLAUDE_STUB_LOG", logFile)

			b := &headlessBackend{goos: runtime.GOOS, screenBinary: c.bin}
			err := b.Run(missingDispatch(t, true))
			if err == nil {
				t.Fatal("Run with a broken screen hook succeeded, want a refusal")
			}
			var ax *axi.Error
			if !errors.As(err, &ax) || ax.Code != "SCREEN_UNAVAILABLE" {
				t.Fatalf("Run error = %v, want an axi.Error with code SCREEN_UNAVAILABLE", err)
			}
			if !strings.Contains(ax.Msg, c.want) {
				t.Errorf("Msg = %q, want it to say %q", ax.Msg, c.want)
			}
			if _, statErr := os.Stat(logFile); statErr == nil {
				t.Error("the claude CLI was started even though the screen hook was broken")
			}
		})
	}
}

// realDispatch returns a dispatch whose worktree and work directory exist,
// for the tests that actually start the stub CLI. The directories are not
// t.TempDir's: a case that deliberately leaves a child running would fail
// the test on cleanup, and what that case is about is jig returning inside
// its bound, not the orphan's lifetime.
func realDispatch(t *testing.T, screen bool) Dispatch {
	t.Helper()
	root, err := os.MkdirTemp("", "jig-headless-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	worktree := filepath.Join(root, "lease")
	work := filepath.Join(root, "store", "T-1", "work")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return Dispatch{
		Ticket:     "T-1",
		Slice:      "a",
		Attempt:    1,
		Worktree:   worktree,
		SliceJSON:  filepath.Join(work, "a.attempt-1.slice.json"),
		ResultJSON: filepath.Join(work, "a.attempt-1.result.json"),
		Model:      "claude-haiku-4-5",
		Prompt:     "do the slice",
		Screen:     screen,
	}
}

// TestHeadlessTimeoutParsing pins how the bound is read: the default when
// unset, the operator's value when it parses, and a loud refusal when it
// does not, since silently falling back to 90 minutes would only show up as
// a wait.
func TestHeadlessTimeoutParsing(t *testing.T) {
	got, err := headlessTimeout()
	if err != nil || got != defaultHeadlessTimeout {
		t.Errorf("headlessTimeout() = %v, %v, want the default and no error", got, err)
	}
	t.Setenv("JIG_HEADLESS_TIMEOUT", "45m")
	if got, err := headlessTimeout(); err != nil || got != 45*time.Minute {
		t.Errorf("headlessTimeout() with an override = %v, %v, want 45m", got, err)
	}
	for _, bad := range []string{"nonsense", "-5m", "0", "90"} {
		t.Setenv("JIG_HEADLESS_TIMEOUT", bad)
		got, err := headlessTimeout()
		var ax *axi.Error
		if !errors.As(err, &ax) || ax.Code != "BAD_TIMEOUT" {
			t.Errorf("headlessTimeout() with %q = %v, %v, want a BAD_TIMEOUT error", bad, got, err)
		}
	}
}

// TestSessionTimeoutMessageStatesTheDrain pins the SESSION_TIMEOUT message's
// honesty: it must name both the bound jig enforced and the WaitDelay drain
// that can run past it, not just the bound alone. In
// TestHeadlessTimeoutEndsAWedgedSession's "a child outlives the CLI" case
// the stub exits right away and the WaitDelay timer starts at that exit, so
// Run returns at about 10s regardless of the 3s bound, and a message that
// only says "3s" reads as a bug report waiting to happen.
func TestSessionTimeoutMessageStatesTheDrain(t *testing.T) {
	err := sessionTimeoutError(3 * time.Second)
	if err.Code != "SESSION_TIMEOUT" {
		t.Fatalf("code = %q, want SESSION_TIMEOUT", err.Code)
	}
	if !strings.Contains(err.Msg, "3s") {
		t.Errorf("SESSION_TIMEOUT message %q does not name the 3s bound", err.Msg)
	}
	if !strings.Contains(err.Msg, sessionWaitDelay.String()) {
		t.Errorf("SESSION_TIMEOUT message %q does not name the %s drain", err.Msg, sessionWaitDelay)
	}
}

// TestHeadlessBadTimeoutFailsBeforeTheScreenProbe pins the parse order: a
// JIG_HEADLESS_TIMEOUT that does not parse must fail before verifyScreen
// ever runs, so a broken screen hook cannot mask a bad bound - or spend the
// probe's 30s budget - behind SCREEN_UNAVAILABLE. The screen binary here
// does not exist at all, which would normally surface as SCREEN_UNAVAILABLE;
// getting BAD_TIMEOUT instead proves the probe never ran.
func TestHeadlessBadTimeoutFailsBeforeTheScreenProbe(t *testing.T) {
	t.Setenv("JIG_HEADLESS_TIMEOUT", "nonsense")
	b := &headlessBackend{goos: runtime.GOOS, screenBinary: filepath.Join(t.TempDir(), "not-jig")}
	err := b.Run(missingDispatch(t, true))
	var ax *axi.Error
	if !errors.As(err, &ax) || ax.Code != "BAD_TIMEOUT" {
		t.Fatalf("Run with a bad bound and a broken screen = %v, want a BAD_TIMEOUT error", err)
	}
}

// TestHeadlessTimeoutEndsAWedgedSession drives the bound itself, which is
// the part that has to hold: a CLI that stops making progress, and one that
// stops while a child it started keeps running and keeps jig's pipes open.
// Both must return inside the bound rather than hanging the dispatch.
func TestHeadlessTimeoutEndsAWedgedSession(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"the CLI wedges", map[string]string{"CLAUDE_STUB_HANG": "90s"}},
		{"a child outlives the CLI", map[string]string{"CLAUDE_STUB_CHILD_HANG": "90s"}},
		{"both wedge", map[string]string{"CLAUDE_STUB_HANG": "90s", "CLAUDE_STUB_CHILD_HANG": "90s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubDir := buildBinary(t, filepath.Join("testdata", "fixture", "claudestub"), "claude")
			t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			for _, k := range []string{"CLAUDE_STUB_LOG", "CLAUDE_STUB_WRITE_PATH", "CLAUDE_STUB_WRITE_BODY", "CLAUDE_STUB_STDOUT", "CLAUDE_STUB_STDERR", "CLAUDE_STUB_EXIT", "CLAUDE_STUB_HANG", "CLAUDE_STUB_CHILD_HANG"} {
				t.Setenv(k, c.env[k])
			}
			t.Setenv("JIG_HEADLESS_TIMEOUT", "3s")

			b := &headlessBackend{goos: runtime.GOOS, screenBinary: builtJigBinary(t)}
			start := time.Now()
			err := b.Run(realDispatch(t, true))
			elapsed := time.Since(start)

			var ax *axi.Error
			if !errors.As(err, &ax) || ax.Code != "SESSION_TIMEOUT" {
				t.Fatalf("Run = %v after %v, want a SESSION_TIMEOUT error", err, elapsed)
			}
			if elapsed > 30*time.Second {
				t.Errorf("Run returned after %v, well past the 3s bound: the bound does not bound the call", elapsed)
			}
			// A child the CLI leaves behind after exiting normally is not
			// jig's to kill on every OS; what must hold is that jig stops
			// waiting on it, which the elapsed check above pins.
		})
	}
}

// TestHeadlessTimeoutKillsTheChildTree pins the tree kill itself, not just
// the elapsed-time ceiling TestHeadlessTimeoutEndsAWedgedSession checks: when
// the bound fires while the CLI is still alive, a child it started must be
// dead afterward, not merely disconnected from jig's pipes. Nothing else in
// this file looks at the child's own pid, so replacing killTree's tree walk
// with a plain cmd.Process.Kill would still pass the rest of the suite.
func TestHeadlessTimeoutKillsTheChildTree(t *testing.T) {
	stubDir := buildBinary(t, filepath.Join("testdata", "fixture", "claudestub"), "claude")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	for _, k := range []string{"CLAUDE_STUB_LOG", "CLAUDE_STUB_WRITE_PATH", "CLAUDE_STUB_WRITE_BODY", "CLAUDE_STUB_STDOUT", "CLAUDE_STUB_STDERR", "CLAUDE_STUB_EXIT"} {
		t.Setenv(k, "")
	}
	// Both the CLI and its child must still be alive when the bound fires,
	// so the kill has to reach the tree rather than a child the CLI already
	// let go of (that shape is TestHeadlessTimeoutEndsAWedgedSession's "a
	// child outlives the CLI" case, which WaitDelay covers, not killTree).
	t.Setenv("CLAUDE_STUB_HANG", "90s")
	t.Setenv("CLAUDE_STUB_CHILD_HANG", "90s")
	t.Setenv("CLAUDE_STUB_CHILD_PID_FILE", pidFile)
	t.Setenv("JIG_HEADLESS_TIMEOUT", "3s")

	b := &headlessBackend{goos: runtime.GOOS, screenBinary: builtJigBinary(t)}
	err := b.Run(realDispatch(t, true))
	var ax *axi.Error
	if !errors.As(err, &ax) || ax.Code != "SESSION_TIMEOUT" {
		t.Fatalf("Run = %v, want a SESSION_TIMEOUT error", err)
	}

	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read the child's pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", pidData, err)
	}
	if processAlive(pid) {
		t.Errorf("child pid %d is still running after the timeout, want killTree to have ended the whole tree", pid)
	}
}

// TestHeadlessTimeoutKeepsFinishedWork pins that a session which wrote its
// result is honored even if the process then had to be stopped: the disk
// contract is what decides, not how the process ended.
func TestHeadlessTimeoutKeepsFinishedWork(t *testing.T) {
	stubDir := buildBinary(t, filepath.Join("testdata", "fixture", "claudestub"), "claude")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	work := filepath.Join(t.TempDir(), "T-1", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(work, "a.attempt-1.result.json")
	for _, k := range []string{"CLAUDE_STUB_LOG", "CLAUDE_STUB_STDOUT", "CLAUDE_STUB_STDERR", "CLAUDE_STUB_EXIT", "CLAUDE_STUB_CHILD_HANG"} {
		t.Setenv(k, "")
	}
	t.Setenv("CLAUDE_STUB_WRITE_PATH", result)
	t.Setenv("CLAUDE_STUB_WRITE_BODY", `{"outcome":"green","summary":"finished before the bound"}`)
	t.Setenv("CLAUDE_STUB_HANG", "90s")
	t.Setenv("JIG_HEADLESS_TIMEOUT", "3s")

	d := realDispatch(t, true)
	d.ResultJSON = result
	b := &headlessBackend{goos: runtime.GOOS, screenBinary: builtJigBinary(t)}
	if err := b.Run(d); err != nil {
		t.Fatalf("Run = %v, want the written result to be honored", err)
	}
}

// TestHeadlessCarriesTheLeaseMemory pins that the lease's committed
// CLAUDE.md still reaches the session. Dropping the project setting source
// keeps the code under review from registering hooks or widening
// permissions, and it also drops that file from the CLI's own discovery, so
// jig carries it itself - from the lease's HEAD tree (leaseMemory, tested on
// its own in leasememory_test.go), not from the working directory.
func TestHeadlessCarriesTheLeaseMemory(t *testing.T) {
	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}

	d := realDispatch(t, true)
	initLeaseRepo(t, d.Worktree)

	args, cleanup, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	cleanup()
	if strings.Contains(strings.Join(args, " "), "--append-system-prompt-file") {
		t.Errorf("args carry a memory file when the lease has none:\n%q", args)
	}

	commitLeaseFile(t, d.Worktree, "CLAUDE.md", "# conventions\n")
	args, cleanup, err = b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	defer cleanup()
	var memoryPath string
	for i, a := range args {
		if a == "--append-system-prompt-file" && i+1 < len(args) {
			memoryPath = args[i+1]
		}
	}
	if memoryPath == "" {
		t.Fatalf("args do not carry the lease's CLAUDE.md:\n%q", args)
	}
	got, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read the carried memory file: %v", err)
	}
	if string(got) != "# conventions\n" {
		t.Errorf("carried memory file = %q, want the committed CLAUDE.md content", got)
	}
	for i, a := range args {
		if a == "--setting-sources" && (i+1 >= len(args) || args[i+1] != "user") {
			t.Errorf("args do not keep the lease's own settings out:\n%q", args)
		}
	}
}
