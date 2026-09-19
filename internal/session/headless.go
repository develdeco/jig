package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/screen"
)

// jigMainPackage is the jig binary's main package path as Go's build info
// records it. A test binary records "<package>.test" instead.
const jigMainPackage = "github.com/develdeco/jig/cmd/jig"

// headlessEditTools are the file-edit tools a headless session can see.
// Their grant is path-scoped by the session's permission rules (see
// settings), never by the screen hook.
var headlessEditTools = []string{"Edit", "Write", "NotebookEdit"}

// headlessBackend drives a local `claude -p` subprocess under jig's
// fail-closed permission model (docs/adr/0008-headless-permission-model.md).
// Its hermetic tests run a stub `claude`; the opt-in contract test in
// headless_live_test.go runs the real CLI against a local mock API.
type headlessBackend struct {
	goos         string // runtime.GOOS, injectable so rule paths are testable per OS
	screenBinary string // Options.ScreenBinary; see hookBinary
}

func newHeadlessBackend(opts Options) Backend {
	return &headlessBackend{goos: runtime.GOOS, screenBinary: opts.ScreenBinary}
}

// hookBinary returns the jig binary a screened dispatch's PreToolUse hook
// runs: Options.ScreenBinary when set, else this process's own executable.
// It is resolved per screened dispatch, so an unscreened one never needs it.
func (b *headlessBackend) hookBinary() (string, error) {
	if b.screenBinary != "" {
		return b.screenBinary, nil
	}
	return selfJigBinary()
}

// selfJigBinary returns this process's executable when this process is the
// jig binary. Any other program - a test binary above all - would register
// itself as the screen hook and rerun itself on every tool call.
func selfJigBinary() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Path != jigMainPackage {
		self := "a binary without build info"
		if ok {
			self = bi.Path
		}
		return "", fmt.Errorf("session/headless: the screen hook runs `jig _screen`, but this process is %s, not jig; set Options.ScreenBinary to a built jig binary", self)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("session/headless: resolve the jig executable for the screen hook: %w", err)
	}
	return exe, nil
}

// headlessTools is a headless session's whole tool surface (`--tools`): the
// tools the screen grants plus the edit tools. Web access, subagents,
// skills, scheduling and MCP tools are absent from the session entirely.
func headlessTools() []string {
	return append(append([]string{}, screen.Granted...), headlessEditTools...)
}

// cliResult is the final result object `claude -p --output-format json`
// prints: whether the session ended in error, its final message, and the
// tool calls the permission system denied.
type cliResult struct {
	Type              string      `json:"type"`
	Subtype           string      `json:"subtype"`
	IsError           bool        `json:"is_error"`
	Result            string      `json:"result"`
	PermissionDenials []cliDenial `json:"permission_denials"`
}

type cliDenial struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// Run runs one session: `claude -p` in the lease worktree, in dontAsk
// permission mode, with the tool surface and grants args describes. A
// session that wrote d.ResultJSON has honored the disk contract, whatever
// its exit status. Otherwise the CLI's final result object decides: a
// missing one (the CLI never ran a session, e.g. a rejected flag) or an
// error one (authentication, the API, a budget) is an infrastructure error
// returned to the caller with the CLI's own message, while a completed
// session's final message is parsed via outcome.ParseText and written to
// d.ResultJSON in its place.
func (b *headlessBackend) Run(d Dispatch) error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return &axi.Error{
			Msg:  "claude binary not found on PATH; install the Claude Code CLI to use the headless backend",
			Code: "CLAUDE_NOT_FOUND",
			Help: []string{"Install `claude` and ensure it is on PATH, or use `--backend fake --scenario <dir>` for CI."},
		}
	}

	args, err := b.args(d)
	if err != nil {
		return err
	}
	cmd := exec.Command(claudePath, args...)
	cmd.Dir = d.Worktree
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if _, err := os.Stat(d.ResultJSON); err == nil {
		return nil
	}

	res, ok := parseCLIResult(stdout.Bytes())
	if !ok {
		return fmt.Errorf("session/headless: claude ran no session (%s): %s", exitStatus(runErr), outputTail(stderr.String(), stdout.String()))
	}
	if res.IsError {
		msg := res.Result
		if msg == "" {
			msg = res.Subtype
		}
		return fmt.Errorf("session/headless: claude session ended in error: %s", msg)
	}

	parsed := outcome.ParseText("slice", res.Result)
	if parsed.Outcome == outcome.Failed && len(res.PermissionDenials) > 0 {
		parsed.Summary += "; " + describeDenials(res.PermissionDenials)
	}
	data, err := json.Marshal(parsed)
	if err != nil {
		return fmt.Errorf("session/headless: marshal fallback result: %w", err)
	}
	return writeResultBytes(d.ResultJSON, data)
}

// args renders the full `claude` argv for d. The prompt goes last, after
// "--", so no prompt text can ever parse as a flag.
func (b *headlessBackend) args(d Dispatch) ([]string, error) {
	settings, err := b.settings(d)
	if err != nil {
		return nil, fmt.Errorf("session/headless: render settings: %w", err)
	}
	args := []string{"-p", "--output-format", "json"}
	if d.Model != "" {
		args = append(args, "--model", d.Model)
	}
	args = append(args,
		"--permission-mode", "dontAsk",
		"--tools", strings.Join(headlessTools(), ","),
		"--strict-mcp-config",
		"--settings", settings,
		"--", d.Prompt,
	)
	return args, nil
}

// settings renders the session's `--settings` JSON. Its permission rules
// grant the edit tools inside the lease worktree and on d.ResultJSON
// itself, nowhere else. With d.Screen set, a PreToolUse hook runs
// `<hookBinary> _screen` (exec form, so no shell parses the path) on every
// tool call, and its allow is the only grant for the screen.Granted tools.
// Without d.Screen those tools get plain allow rules instead, unscreened.
func (b *headlessBackend) settings(d Dispatch) (string, error) {
	worktree, err := filepath.Abs(d.Worktree)
	if err != nil {
		return "", err
	}
	result, err := filepath.Abs(d.ResultJSON)
	if err != nil {
		return "", err
	}
	var allow []string
	for _, p := range pathForms(worktree) {
		allow = append(allow, "Edit("+rulePath(b.goos, p)+"/**)")
	}
	for _, p := range pathForms(result) {
		allow = append(allow, "Edit("+rulePath(b.goos, p)+")")
	}

	settings := map[string]any{}
	if d.Screen {
		bin, err := b.hookBinary()
		if err != nil {
			return "", err
		}
		settings["hooks"] = map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{"type": "command", "command": bin, "args": []string{"_screen"}},
					},
				},
			},
		}
	} else {
		allow = append(allow, screen.Granted...)
	}
	settings["permissions"] = map[string]any{"allow": allow}
	data, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// pathForms returns p plus its symlink-resolved form when that differs, so
// a rule matches whichever form Claude Code checks (a macOS temp dir under
// /var is under /private/var once resolved). p itself need not exist yet:
// only its parent is resolved then.
func pathForms(p string) []string {
	forms := []string{p}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		dir, derr := filepath.EvalSymlinks(filepath.Dir(p))
		if derr != nil {
			return forms
		}
		resolved = filepath.Join(dir, filepath.Base(p))
	}
	if resolved != p {
		forms = append(forms, resolved)
	}
	return forms
}

// rulePath renders an absolute path as a Claude Code permission-rule path:
// "//" anchors it at the filesystem root, a Windows drive path takes the
// POSIX form Claude Code normalizes paths to before matching (C:\a\b is
// //c/a/b), and gitignore pattern characters are escaped so the rule
// matches only the literal path.
func rulePath(goos, p string) string {
	if goos == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
		if len(p) >= 2 && p[1] == ':' {
			p = "/" + strings.ToLower(p[:1]) + p[2:]
		}
	}
	var b strings.Builder
	b.WriteString("/")
	for _, r := range p {
		switch r {
		case '\\', '*', '?', '[', ']':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// parseCLIResult finds the CLI's final result object in its stdout: the
// whole output, or failing that its last line that is one (tolerating any
// stray output before it).
func parseCLIResult(out []byte) (cliResult, bool) {
	var res cliResult
	if err := json.Unmarshal(bytes.TrimSpace(out), &res); err == nil && res.Type == "result" {
		return res, true
	}
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var r cliResult
		if err := json.Unmarshal([]byte(line), &r); err == nil && r.Type == "result" {
			return r, true
		}
	}
	return cliResult{}, false
}

// exitStatus describes how the CLI process ended.
func exitStatus(err error) string {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return "exit 0"
	case errors.As(err, &exitErr):
		return fmt.Sprintf("exit %d", exitErr.ExitCode())
	default:
		return err.Error()
	}
}

// outputTail returns the last 600 characters of stderr, or of stdout when
// stderr is empty, as the diagnostic for a CLI that ran no session.
func outputTail(stderr, stdout string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		s = strings.TrimSpace(stdout)
	}
	if s == "" {
		return "no output"
	}
	if r := []rune(s); len(r) > 600 {
		s = "..." + string(r[len(r)-600:])
	}
	return s
}

// describeDenials names the tool calls the permission system denied, so a
// session that could not finish its disk contract says why: at most five,
// each by tool name and its path or command.
func describeDenials(denials []cliDenial) string {
	const limit = 5
	var parts []string
	for i, dn := range denials {
		if i == limit {
			parts = append(parts, fmt.Sprintf("and %d more", len(denials)-limit))
			break
		}
		part := dn.ToolName
		for _, key := range []string{"file_path", "notebook_path", "path", "command"} {
			if v, ok := dn.ToolInput[key].(string); ok && v != "" {
				if r := []rune(v); len(r) > 120 {
					v = string(r[:120]) + "..."
				}
				part += " " + v
				break
			}
		}
		parts = append(parts, part)
	}
	return "denied tool calls: " + strings.Join(parts, ", ")
}
