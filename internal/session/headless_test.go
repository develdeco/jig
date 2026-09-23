package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	got, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
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
	got, err = b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
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
	wantArgs, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
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

// TestHeadlessTimeoutBound pins that a session runs under a deadline and
// that JIG_HEADLESS_TIMEOUT sets it, so a hung CLI or hook cannot wedge an
// unattended run forever.
func TestHeadlessTimeoutBound(t *testing.T) {
	if got := headlessTimeout(); got != defaultHeadlessTimeout {
		t.Errorf("headlessTimeout() = %v, want the default %v", got, defaultHeadlessTimeout)
	}
	t.Setenv("JIG_HEADLESS_TIMEOUT", "45m")
	if got := headlessTimeout(); got != 45*time.Minute {
		t.Errorf("headlessTimeout() with an override = %v, want 45m", got)
	}
	for _, bad := range []string{"nonsense", "-5m", "0"} {
		t.Setenv("JIG_HEADLESS_TIMEOUT", bad)
		if got := headlessTimeout(); got != defaultHeadlessTimeout {
			t.Errorf("headlessTimeout() with %q = %v, want the default", bad, got)
		}
	}
}
