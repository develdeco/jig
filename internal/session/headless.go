package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/outcome"
)

// headlessBackend drives a local `claude -p` subprocess. It is
// compile-checked only: unit tests never invoke a real claude binary.
type headlessBackend struct{}

func newHeadlessBackend(opts Options) Backend {
	return &headlessBackend{}
}

// Run shells out to `claude -p <prompt> --output-format stream-json
// --model <model> --add-dir <worktree> --settings <generated settings>`
// with cwd=worktree, then reads d.ResultJSON; if the agent didn't write it,
// the collected stdout is scanned via outcome.ParseText and the result is
// written in its place.
func (b *headlessBackend) Run(d Dispatch) error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return &axi.Error{
			Msg:  "claude binary not found on PATH; install the Claude Code CLI to use the headless backend",
			Code: "CLAUDE_NOT_FOUND",
			Help: []string{"Install `claude` and ensure it is on PATH, or use `--backend fake --scenario <dir>` for CI."},
		}
	}

	settingsPath, err := writeHeadlessSettings(d)
	if err != nil {
		return fmt.Errorf("session/headless: write settings: %w", err)
	}

	args := []string{
		"-p", d.Prompt,
		"--output-format", "stream-json",
		"--model", d.Model,
		"--add-dir", d.Worktree,
		"--settings", settingsPath,
	}
	cmd := exec.Command(claudePath, args...)
	cmd.Dir = d.Worktree
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	// The exit status alone doesn't determine the outcome: ResultJSON (or,
	// failing that, the transcript) does. A non-zero exit still gets
	// routed through the same read-result path.
	_ = cmd.Run()

	if _, err := os.Stat(d.ResultJSON); err == nil {
		return nil
	}

	res := outcome.ParseText("slice", out.String())
	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("session/headless: marshal fallback result: %w", err)
	}
	return writeResultBytes(d.ResultJSON, data)
}

// writeHeadlessSettings generates a Claude Code settings JSON for the run,
// registering a PreToolUse hook that shells back into this same jig binary
// (`<abs path> _screen`) for best-effort command/tool screening when
// d.Screen is set.
func writeHeadlessSettings(d Dispatch) (string, error) {
	settings := map[string]any{}
	if d.Screen {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve current executable: %w", err)
		}
		settings["hooks"] = map[string]any{
			"PreToolUse": []map[string]any{
				{
					"matcher": "*",
					"hooks": []map[string]any{
						{"type": "command", "command": exe + " _screen"},
					},
				},
			},
		}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(d.Worktree, ".jig", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
