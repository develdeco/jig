package e2e

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installedHookCommand reads the command a Claude Code settings file runs for
// event: the one hook of the first group of that event.
func installedHookCommand(t *testing.T, settingsPath, event string) string {
	t.Helper()
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(readFileOrFatal(t, settingsPath), &settings); err != nil {
		t.Fatalf("%s is not settings JSON: %v", settingsPath, err)
	}
	groups := settings.Hooks[event]
	if len(groups) == 0 || len(groups[0].Hooks) == 0 || groups[0].Hooks[0].Type != "command" {
		t.Fatalf("%s installs no command for %s:\n%s", settingsPath, event, readFileOrFatal(t, settingsPath))
	}
	return groups[0].Hooks[0].Command
}

// runHookCommand runs a hook command line as a harness does: the words of the
// command (quoted program included), the payload on stdin, in cwd, in env.
func runHookCommand(t *testing.T, env jigEnv, cwd, command string, payload []byte) jigResult {
	t.Helper()
	argv, err := splitCommand(command)
	if err != nil || len(argv) == 0 {
		t.Fatalf("hook command %q: %v", command, err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = env.environ()
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := exitCodeOf(t, cmd.Run())
	return jigResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}

// hookOutput is what a Claude Code hook printed, decoded.
type hookOutput struct {
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	SystemMessage string `json:"systemMessage"`
	Specific      struct {
		Name    string `json:"hookEventName"`
		Context string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// decodeHook decodes the one JSON object a hook printed, failing the test on
// anything else, since Claude Code takes stray output for the model's context.
func decodeHook(t *testing.T, res jigResult) hookOutput {
	t.Helper()
	var out hookOutput
	if res.Code != 0 {
		t.Fatalf("hook exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil || strings.Count(strings.TrimSpace(res.Stdout), "\n") != 0 {
		t.Fatalf("hook stdout is not one JSON object: %q (%v)", res.Stdout, err)
	}
	return out
}

// claudeSettings is the settings file `jig hooks --global` writes under the
// test's user home.
func claudeSettings(env jigEnv) string {
	return filepath.Join(env.userHome, ".claude", "settings.json")
}
