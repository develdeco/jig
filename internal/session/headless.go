package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
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
// permission model (docs/adr/0008-headless-permission-model.md): `dontAsk`
// mode denies anything not granted, but granting through the screen is not
// by itself fail-closed - see verifyScreen for why jig proves the screen
// before it relies on it. Its hermetic tests run a stub `claude`; the
// opt-in contract test in headless_live_test.go runs the real CLI against a
// local mock API.
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

// defaultHeadlessTimeout bounds one headless session. It is generous: a
// real slice can take a long time, and the bound exists for a session or a
// hook that has stopped making progress at all, not to hurry one along.
const defaultHeadlessTimeout = 90 * time.Minute

// headlessTimeout is defaultHeadlessTimeout, or the Go duration in
// JIG_HEADLESS_TIMEOUT. A value that does not parse, or is not positive, is
// refused rather than ignored: an operator who set a bound and got the
// default silently would only find out by waiting for it.
func headlessTimeout() (time.Duration, error) {
	raw := os.Getenv("JIG_HEADLESS_TIMEOUT")
	if raw == "" {
		return defaultHeadlessTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, &axi.Error{
			Msg:  fmt.Sprintf("JIG_HEADLESS_TIMEOUT is %q, which is not a positive Go duration", raw),
			Code: "BAD_TIMEOUT",
			Help: []string{"Set it to a duration such as 45m or 3h, or unset it for the default."},
		}
	}
	return d, nil
}

// screenProbe is the tool call verifyScreen sends the hook: a push, which
// the command screen always denies.
var screenProbe = []byte(`{"tool_name":"Bash","tool_input":{"command":"git push"}}`)

// hookDecision is the part of the hook's stdout verifyScreen reads.
type hookDecision struct {
	HookSpecificOutput struct {
		PermissionDecision string `json:"permissionDecision"`
	} `json:"hookSpecificOutput"`
}

// verifyScreen proves the screen hook answers before a screened session
// starts. Claude Code skips a hook it cannot launch, and its own read-only
// classifier still grants part of the shell, so a hook that is missing,
// exits non-zero, or prints nothing would leave a session running with no
// screen in front of it and nothing saying so. One probe per dispatch
// settles it: the hook must come back denying a push.
func (b *headlessBackend) verifyScreen() error {
	bin, err := b.hookBinary()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "_screen")
	cmd.Stdin = bytes.NewReader(screenProbe)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	unusable := func(why string) error {
		return &axi.Error{
			Msg:  fmt.Sprintf("the screen hook %q is unusable (%s), so a screened session would run unscreened", bin, why),
			Code: "SCREEN_UNAVAILABLE",
			Help: []string{"Check that the jig binary the hook names exists and runs `jig _screen`, then rerun."},
		}
	}
	if ctx.Err() != nil {
		return unusable("it did not answer within 30s")
	}
	if runErr != nil {
		return unusable(fmt.Sprintf("it failed: %s: %s", exitStatus(runErr), outputTail(stderr.String(), stdout.String())))
	}
	var decision hookDecision
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &decision); err != nil {
		return unusable("its answer was not the hook's JSON decision")
	}
	if decision.HookSpecificOutput.PermissionDecision != "deny" {
		return unusable(fmt.Sprintf("it answered %q to a call it must deny", decision.HookSpecificOutput.PermissionDecision))
	}
	return nil
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

	// Parsed before verifyScreen spawns the probe subprocess: a bound that
	// does not parse should cost nothing and fail as BAD_TIMEOUT, not spend
	// the probe's budget and come back as SCREEN_UNAVAILABLE (or a working
	// screen's few hundred milliseconds) before anyone finds out the bound
	// itself was bad.
	bound, err := headlessTimeout()
	if err != nil {
		return err
	}

	if d.Screen {
		if err := b.verifyScreen(); err != nil {
			return err
		}
	}

	args, cleanup, err := b.args(d)
	if err != nil {
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	cmd := exec.CommandContext(ctx, claudePath, args...)
	cmd.Dir = d.Worktree
	newProcessGroup(cmd)
	// A session spawns children - a shell per Bash call, a test runner,
	// whatever those start - and they inherit these pipes. Killing the CLI
	// alone would leave Wait blocked on a pipe a surviving grandchild still
	// holds, so the bound would not bound anything: Cancel ends the whole
	// tree, and WaitDelay stops waiting on the pipes regardless.
	cmd.Cancel = func() error { return killTree(cmd) }
	cmd.WaitDelay = sessionWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// A session that wrote its result honored the disk contract, whatever
	// happened to the process afterwards - including a timeout while it was
	// shutting down - so the result is read before the bound is reported.
	if _, err := os.Stat(d.ResultJSON); err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return sessionTimeoutError(bound)
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

// args renders the full `claude` argv for d, plus a cleanup func the caller
// must run once the session is done with it (it removes the lease-memory
// temp file args may have created; a no-op when there was none to make).
// The prompt goes last, after "--", so no prompt text can ever parse as a
// flag.
func (b *headlessBackend) args(d Dispatch) (argv []string, cleanup func(), err error) {
	noop := func() {}
	settings, err := b.settings(d)
	if err != nil {
		return nil, noop, fmt.Errorf("session/headless: render settings: %w", err)
	}
	args := []string{"-p", "--output-format", "json"}
	if d.Model != "" {
		args = append(args, "--model", d.Model)
	}
	args = append(args,
		"--permission-mode", "dontAsk",
		"--tools", strings.Join(headlessTools(), ","),
		"--strict-mcp-config",
		// Only the operator's own settings load on top of this session's
		// own --settings. Project and local settings live inside the lease
		// worktree, which is the content under review: a `.claude/
		// settings.json` committed on the ticket branch would otherwise run
		// its own PreToolUse hook on this machine, and a
		// `.claude/settings.local.json` could grant edits outside the
		// lease.
		"--setting-sources", "user",
	)
	// Dropping the project source also drops the lease's CLAUDE.md, which
	// the CLI discovers through it, so jig carries that file itself. The
	// distinction is capability against instructions: a settings file grants
	// what a session may do and must not come from the code under review,
	// while CLAUDE.md only tells the session how this repo works, which is
	// the repo's job. Imports inside it are not resolved, since this passes
	// the file's own text.
	memory, memoryCleanup, err := leaseMemory(d.Worktree)
	if err != nil {
		return nil, noop, err
	}
	if memory != "" {
		args = append(args, "--append-system-prompt-file", memory)
	}
	args = append(args,
		"--settings", settings,
		"--", d.Prompt,
	)
	return args, memoryCleanup, nil
}

// settings renders the session's `--settings` JSON. Its permission rules
// grant the edit tools inside the lease worktree and on d.ResultJSON
// itself, nowhere else. With d.Screen set, a PreToolUse hook runs
// `<hookBinary> _screen` (exec form, so no shell parses the path) on every
// tool call, and its allow is this settings object's only grant for the
// screen.Granted tools - the operator's own user settings, loaded on top,
// can still grant more. Without d.Screen those tools get plain allow rules
// instead, unscreened.
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

// leaseMemoryCap bounds the lease's CLAUDE.md as carried into a headless
// session's system prompt. Exceeding it fails the dispatch with the
// LEASE_MEMORY_TOO_LARGE error (see leaseMemory) rather than sending a
// truncated file or paying an unbounded cost silently. 64 KiB is generous
// for hand-written project memory - this repo's own CLAUDE.md/AGENTS.md
// pair sits under 4 KiB - while keeping one dispatch's request body from
// growing by megabytes on every call just because a branch happened to
// carry a huge one. A lease that genuinely needs more should point the
// session at a file it reads for itself instead of paying the cost on
// every dispatch.
const leaseMemoryCap = 64 * 1024

// sessionWaitDelay is cmd.WaitDelay: how long a dispatch keeps reading a
// session's stdout/stderr pipes after its context is done, for a child the
// CLI leaves running and still holding them. It is also named in
// sessionTimeoutError, since a bound alone understates how long a timed-out
// dispatch can actually take.
const sessionWaitDelay = 10 * time.Second

// sessionTimeoutError renders the SESSION_TIMEOUT error for a dispatch that
// did not finish within bound. It names both halves of the ceiling jig
// actually enforced: the bound, and the drain it can then spend waiting for
// a child that outlived the CLI to let go of the pipes. A message that only
// says the bound reads like a bug the first time someone times a 3s bound
// returning at 10s.
func sessionTimeoutError(bound time.Duration) *axi.Error {
	return &axi.Error{
		Msg: fmt.Sprintf(
			"the headless session did not finish within %s (plus up to %s to drain its output, if a child it started was still holding the pipes) and was stopped, with no result written",
			bound, sessionWaitDelay,
		),
		Code: "SESSION_TIMEOUT",
		Help: []string{fmt.Sprintf("Raise the bound with JIG_HEADLESS_TIMEOUT (a Go duration, currently %s), or rerun.", bound)},
	}
}

// leaseMemory resolves worktree's committed CLAUDE.md, if it has one, into a
// jig-owned temp file (mode 0600, as os.CreateTemp makes it) ready for
// --append-system-prompt-file, and returns its path plus a cleanup func that
// removes it. cleanup is always safe to call, including when path is "".
//
// The content comes from worktree's HEAD tree through gitx, never from the
// working directory: the lease is the code under review, it can write
// anything through Edit/Write including a symlink or a hard link, and a
// filesystem read of "CLAUDE.md" would follow either one into whatever file
// it names, screen or no screen, since jig would be the one reading it, not
// a tool call the screen ever sees. Only a regular-file blob (git mode
// 100644 or 100755) counts, matched by the exact name "CLAUDE.md" - git's
// own pathspec is case-sensitive on every platform, so a committed
// "claude.md" is not found even on a case-insensitive filesystem. A symlink
// entry (mode 120000), no CLAUDE.md at HEAD, or a worktree that is not a
// git repository (or has no commit yet) all mean nothing to append - the
// same as a lease with no CLAUDE.md at all, not an error; headTreeEntry
// folds a transient git failure into the same silent skip. An oversized
// blob refuses the dispatch outright (LEASE_MEMORY_TOO_LARGE): refusing
// loudly beats silently sending a truncated or absent memory file. Reading
// or writing the temp file can also fail, and is reported as an error too.
func leaseMemory(worktree string) (path string, cleanup func(), err error) {
	noop := func() {}
	mode, hash, size, ok := headTreeEntry(worktree, "CLAUDE.md")
	if !ok || (mode != "100644" && mode != "100755") {
		return "", noop, nil
	}
	if size > leaseMemoryCap {
		return "", noop, &axi.Error{
			Msg:  fmt.Sprintf("the lease's CLAUDE.md is %d bytes, over the %d-byte cap, so the dispatch was refused rather than sending it anyway", size, leaseMemoryCap),
			Code: "LEASE_MEMORY_TOO_LARGE",
			Help: []string{"Trim CLAUDE.md below the cap, or move the excess into a file the session reads for itself."},
		}
	}
	// RunRaw, not Run: Run trims stdout, which would drop meaningful
	// leading or trailing bytes of the file's own content.
	content, err := gitx.RunRaw(worktree, "cat-file", "-p", hash)
	if err != nil {
		return "", noop, fmt.Errorf("session/headless: read the lease's CLAUDE.md blob: %w", err)
	}
	f, err := os.CreateTemp("", "jig-lease-memory-*.md")
	if err != nil {
		return "", noop, fmt.Errorf("session/headless: create a temp file for the lease's CLAUDE.md: %w", err)
	}
	remove := func() { _ = os.Remove(f.Name()) }
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		remove()
		return "", noop, fmt.Errorf("session/headless: write the lease memory temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", noop, fmt.Errorf("session/headless: close the lease memory temp file: %w", err)
	}
	return f.Name(), remove, nil
}

// headTreeEntry looks up path in worktree's HEAD tree via `git ls-tree -l`
// and returns its mode, blob hash and byte size. ok is false for every shape
// of "nothing there" a caller following the disk contract needs to treat as
// a silent skip rather than a hard failure: worktree is not a git
// repository, HEAD has no commit yet, or HEAD's tree simply has no entry at
// path. None of those is distinguished from the others; the caller does not
// need to.
func headTreeEntry(worktree, path string) (mode, hash string, size int64, ok bool) {
	out, err := gitx.Run(worktree, "ls-tree", "-l", "HEAD", "--", path)
	if err != nil || out == "" {
		return "", "", 0, false
	}
	// "<mode> SP <type> SP <hash> SP <size>\t<path>"; only the part before
	// the tab is field data, and the size column is space-padded for
	// alignment, so Fields (not a fixed split) is what parses it.
	head, _, cut := strings.Cut(out, "\t")
	if !cut {
		return "", "", 0, false
	}
	fields := strings.Fields(head)
	if len(fields) != 4 {
		return "", "", 0, false
	}
	n, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return "", "", 0, false
	}
	return fields[0], fields[2], n, true
}
