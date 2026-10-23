package e2e

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
)

// TestObserveMalformedPayloadExitsZero: a hook must not fail the harness, and
// Claude Code takes a hook's stdout for the model's context and a non-zero
// status for a refusal, so whatever `jig observe` is handed - garbage, a
// payload of the wrong shape, a harness it has never heard of, a directory
// that is no project - it exits 0 and prints nothing. The reason is in the log.
func TestObserveMalformedPayloadExitsZero(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	elsewhere := t.TempDir()
	huge := make([]byte, 5<<20)
	rand.New(rand.NewSource(1)).Read(huge)
	truncated := []byte(`{"session_id":"s","cwd":"` + strings.ReplaceAll(fx.RepoDir, `\`, `\`) + `","hook_event_name":"PostToolUse","tool_input":{"command":"git comm`)

	payloads := map[string][]byte{
		"empty":               nil,
		"whitespace":          []byte("  \n\t "),
		"text":                []byte("not json at all"),
		"binary":              {0x00, 0xff, 0xfe, 0x01},
		"json array":          []byte(`[1, 2, 3]`),
		"json string":         []byte(`"Stop"`),
		"json null":           []byte(`null`),
		"empty object":        []byte(`{}`),
		"truncated":           truncated,
		"wrong types":         []byte(`{"hook_event_name":7,"session_id":["x"],"cwd":{"a":1},"tool_input":"str","stop_hook_active":"yes"}`),
		"unknown event":       []byte(`{"hook_event_name":"FutureEvent","session_id":"s","cwd":"x"}`),
		"a directory missing": []byte(`{"hook_event_name":"Stop","session_id":"s","cwd":"` + strings.ReplaceAll(filepath.Join(elsewhere, "gone"), `\`, `\`) + `"}`),
		"outside a project":   []byte(`{"hook_event_name":"SessionStart","session_id":"s","cwd":"` + strings.ReplaceAll(elsewhere, `\`, `\`) + `"}`),
		"huge noise":          huge,
	}
	harnesses := []string{"claude", "codex", "gemini", "cursor", "copilot", "opencode", "neutral"}
	for name, payload := range payloads {
		for _, harness := range harnesses {
			res := runJigIn(t, env, fx.RepoDir, payload, "observe", "--harness", harness)
			if res.Code != 0 || res.Stdout != "" {
				t.Errorf("%s payload as %s: exit %d, stdout %q, want exit 0 and silence\nstderr:\n%s", name, harness, res.Code, res.Stdout, res.Stderr)
			}
		}
	}
	// A call that is wrong in its flags is just as quiet.
	for _, args := range [][]string{
		{"observe"},
		{"observe", "--harness", "emacs"},
		{"observe", "--harness", "claude", "--no-such-flag"},
		{"observe", "--event", "nonsense"},
		{"observe", "--event", "tool", "--exit", "not-a-number"},
	} {
		if res := runJigIn(t, env, fx.RepoDir, []byte(`{}`), args...); res.Code != 0 || res.Stdout != "" {
			t.Errorf("jig %s: exit %d, stdout %q, want exit 0 and silence", strings.Join(args, " "), res.Code, res.Stdout)
		}
	}

	if got := readLedger(t, env); len(got) != 0 {
		t.Errorf("malformed and out-of-project calls wrote ledger lines: %+v", got)
	}
	if log, err := os.ReadFile(filepath.Join(home.LogsDir(env.home), "observe.log")); err != nil || !bytes.Contains(log, []byte("observe:")) {
		t.Errorf("observe.log = %q, %v, want the reasons", log, err)
	}
}
