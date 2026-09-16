package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/develdeco/jig/outcome"
)

// herdrBackend drives a remote coding agent through herdr, itself reached
// inside a WSL login shell. It is compile-checked only: unit tests never
// invoke wsl or a real herdr instance.
type herdrBackend struct{}

func newHerdrBackend(opts Options) Backend {
	return &herdrBackend{}
}

// wslDistro is the WSL distribution herdr runs in.
const wslDistro = "Ubuntu-24.04"

// wslPath mechanically converts a Windows path (e.g. `C:\Users\x`) to its
// WSL mount equivalent (`/mnt/c/Users/x`): lowercase the drive letter, drop
// the colon, and flip backslashes to slashes. No wslpath subprocess is
// needed for this shape of path. This is pure string manipulation rather
// than filepath.ToSlash, which is a no-op on any OS other than Windows and
// would leave the backslashes untouched when jig is built on Linux (e.g. in
// CI, where this helper's own test still runs).
func wslPath(winPath string) string {
	p := strings.ReplaceAll(winPath, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(p[:1])
		return "/mnt/" + drive + p[2:]
	}
	return p
}

// runHerdr runs one herdr control command inside the WSL login shell
// (`wsl -d Ubuntu-24.04 -e bash -lc '<cmd>'`) and parses its JSON response.
func runHerdr(args ...string) (map[string]any, error) {
	cmdline := "herdr " + strings.Join(quoteHerdrArgs(args), " ")
	cmd := exec.Command("wsl", "-d", wslDistro, "-e", "bash", "-lc", cmdline)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("session/herdr: %s: %w", cmdline, err)
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("session/herdr: parse response for %q: %w", cmdline, err)
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
// (logged) on failure, for jump-in.
func (b *herdrBackend) Run(d Dispatch) error {
	label := fmt.Sprintf("jig-%s-%s", d.Ticket, d.Slice)
	ws, err := runHerdr("workspace", "create", "--cwd", wslPath(d.Worktree), "--label", label, "--no-focus")
	if err != nil {
		return err
	}
	wsResult := herdrResult(ws)
	workspaceID, _ := wsResult["workspace"].(string)
	paneID, _ := paneIDOf(wsResult)

	agentName := fmt.Sprintf("jig-%s-%s-a%d", d.Ticket, d.Slice, d.Attempt)
	if _, err := runHerdr("agent", "start", agentName, "--kind", "claude", "--pane", paneID, "--timeout", "60000"); err != nil {
		b.leaveOpen(workspaceID)
		return err
	}

	promptRes, err := runHerdr("agent", "prompt", agentName, d.Prompt, "--wait")
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
		readRes, err := runHerdr("agent", "read", agentName)
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

	if _, err := runHerdr("workspace", "close", workspaceID); err != nil {
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
