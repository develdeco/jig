package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/develdeco/jig/internal/outcome"
)

// herdrBackend drives a remote coding agent through herdr: natively off
// Windows, inside a WSL login shell on Windows. goos is runtime.GOOS except in
// tests.
type herdrBackend struct {
	goos string
}

func newHerdrBackend(opts Options) Backend {
	return &herdrBackend{goos: runtime.GOOS}
}

// jigWSLDistroEnv picks the WSL distro herdr runs in on Windows; unset uses
// WSL's default.
const jigWSLDistroEnv = "JIG_WSL_DISTRO"

// WSLPath mechanically converts a Windows path (e.g. `C:\Users\x`) to its
// WSL mount equivalent (`/mnt/c/Users/x`): lowercase the drive letter, drop
// the colon, and flip backslashes to slashes. No wslpath subprocess is
// needed for this shape of path. This is pure string manipulation rather
// than filepath.ToSlash, which is a no-op on any OS other than Windows and
// would leave the backslashes untouched when jig is built on Linux (e.g. in
// CI, where this helper's own test still runs). Only called on the Windows
// (WSL) branch; off Windows, herdr sees the worktree path unchanged.
//
// Exported for other packages that need jig's own notion of the WSL mount
// spelling herdr hands a session on Windows: verifydeliver checks a
// pick's summary, titles and captions for it, among the other spellings jig
// itself handed the session, before they reach a published pull request body
// (the owner's decision on r1-f13, DECISIONS.md).
func WSLPath(winPath string) string {
	p := strings.ReplaceAll(winPath, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(p[:1])
		return "/mnt/" + drive + p[2:]
	}
	return p
}

// herdrCommand returns the process for one herdr control command. Off
// Windows that is herdr itself with args. On Windows it is
// `wsl [-d <distro>] -e bash -lc '<herdr command line>'`.
func herdrCommand(goos string, distro string, args []string) (name string, argv []string) {
	if goos != "windows" {
		return "herdr", args
	}
	cmdline := "herdr " + strings.Join(quoteHerdrArgs(args), " ")
	var wslArgs []string
	if distro != "" {
		wslArgs = append(wslArgs, "-d", distro)
	}
	wslArgs = append(wslArgs, "-e", "bash", "-lc", cmdline)
	return "wsl", wslArgs
}

// runHerdr runs one herdr control command and parses its JSON response. sub
// names the command, "noun verb" ("agent prompt"), and args are its operands.
// An error names the command and never its operands: the operands of `agent
// prompt` are the whole prompt, and every path of the dispatch in it (on
// Windows, spelled as WSL mounts), and those reach the operator's terminal
// and the store's history through whatever records the error. herdr's own
// stderr is kept, where it has said something.
func (b *herdrBackend) runHerdr(sub string, args ...string) (map[string]any, error) {
	name, argv := herdrCommand(b.goos, os.Getenv(jigWSLDistroEnv), append(strings.Fields(sub), args...))
	out, err := exec.Command(name, argv...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
			return nil, fmt.Errorf("session/herdr: herdr %s: %w: %s", sub, err, outputTail(string(exitErr.Stderr), ""))
		}
		return nil, fmt.Errorf("session/herdr: herdr %s: %w", sub, err)
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("session/herdr: parse the response of herdr %s: %w", sub, err)
	}
	return res, nil
}

// quoteHerdrArgs single-quotes each arg for the inner bash -lc command
// line, escaping any embedded single quotes.
func quoteHerdrArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return out
}

func herdrResult(res map[string]any) map[string]any {
	if r, ok := res["result"].(map[string]any); ok {
		return r
	}
	return map[string]any{}
}

// Run drives one dispatch through herdr: create a workspace in the
// dispatch's worktree, start a claude agent in it, send the prompt and
// wait, then read the result the agent was told to write. A blocked agent
// (stuck at an approval dialog) becomes a failed result rather than an
// infrastructure error. The workspace is closed on success and left open
// (logged) on failure, for jump-in. On Windows the session runs in WSL, so
// the workspace is created at the worktree's WSL mount and the prompt's own
// mentions of every path of the dispatch are spelled the same way
// (respellMentions); the files jig itself wrote keep the host spelling of
// whatever paths they hold. jig reads the result at its host path.
// d.NoSessionPersistence is accepted and ignored: the claude agent herdr
// starts is an interactive session that jig starts without flags, so Claude
// Code keeps its transcript whatever the dispatch asked.
func (b *herdrBackend) Run(d Dispatch) error {
	label := fmt.Sprintf("jig-%s-%s", d.Ticket, d.Slice)
	cwd, prompt := d.Worktree, d.Prompt
	if b.goos == "windows" {
		cwd = WSLPath(d.Worktree)
		prompt = respellMentions(d.Prompt, d.paths(), WSLPath)
	}
	ws, err := b.runHerdr("workspace create", "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return err
	}
	wsResult := herdrResult(ws)
	workspaceID, _ := wsResult["workspace"].(string)
	paneID, _ := paneIDOf(wsResult)

	agentName := fmt.Sprintf("jig-%s-%s-a%d", d.Ticket, d.Slice, d.Attempt)
	if _, err := b.runHerdr("agent start", agentName, "--kind", "claude", "--pane", paneID, "--timeout", "60000"); err != nil {
		b.leaveOpen(workspaceID)
		return err
	}

	promptRes, err := b.runHerdr("agent prompt", agentName, prompt, "--wait")
	if err != nil {
		b.leaveOpen(workspaceID)
		return err
	}
	state, _ := herdrResult(promptRes)["state"].(string)

	if state == "blocked" {
		if err := writeJSONResult(d.ResultJSON, map[string]any{
			"outcome": outcome.Failed,
			"summary": "agent blocked at dialog",
		}); err != nil {
			return err
		}
		b.leaveOpen(workspaceID)
		return nil
	}

	if _, err := os.Stat(d.ResultJSON); err != nil {
		readRes, err := b.runHerdr("agent read", agentName)
		if err != nil {
			b.leaveOpen(workspaceID)
			return err
		}
		text, _ := herdrResult(readRes)["text"].(string)
		res := outcome.ParseText("slice", text)
		data, err := json.Marshal(res)
		if err != nil {
			return fmt.Errorf("session/herdr: marshal fallback result: %w", err)
		}
		if err := writeResultBytes(d.ResultJSON, data); err != nil {
			return err
		}
	}

	if _, err := b.runHerdr("workspace close", workspaceID); err != nil {
		b.leaveOpen(workspaceID)
	}
	return nil
}

// paneIDOf extracts the workspace's root pane id from a `workspace create`
// result (`.result.root_pane.pane_id`).
func paneIDOf(result map[string]any) (string, bool) {
	rp, ok := result["root_pane"].(map[string]any)
	if !ok {
		return "", false
	}
	id, ok := rp["pane_id"].(string)
	return id, ok
}

// leaveOpen logs a workspace jig is deliberately not closing, so a human
// can jump in.
func (b *herdrBackend) leaveOpen(workspaceID string) {
	if workspaceID == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "session/herdr: left workspace %s open for inspection\n", workspaceID)
}
