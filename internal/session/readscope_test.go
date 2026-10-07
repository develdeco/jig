package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/screen"
)

// TestAHeadlessSessionReadsEvidenceUnderTheJigHomeWithoutAGrant pins why the
// gate reviewer needs no read grant for the build's recordings (ADR 0029): a
// headless session's read tools are not scoped to its worktree. The settings
// carry path-scoped rules for edits alone, and the screen, which grants
// Read, Glob and Grep, names only credential locations, so a recordings.json
// and the recordings it lists, under the jig home's evidence directory outside
// the lease, are read like review.json and the journal already are. A change
// that scopes reads to the lease would break this test, and with it the
// reviewer's access, before it broke a review.
func TestAHeadlessSessionReadsEvidenceUnderTheJigHomeWithoutAGrant(t *testing.T) {
	t.Parallel()
	jigHome := filepath.Join(t.TempDir(), ".config", "jig")
	evidence := filepath.Join(jigHome, "evidence", "0123456789abcdef", "T-1")
	list := filepath.Join(evidence, "reviews", "round-1", "recordings.json")
	recording := filepath.Join(evidence, "recordings", "abc123", "a-a1-f0", "login.svg")
	for _, p := range []string{list, recording} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}
	d := missingDispatch(t, true)
	d.Slice = "gate"
	raw, err := b.settings(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	var got struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	for _, rule := range got.Permissions.Allow {
		if !strings.HasPrefix(rule, "Edit(") {
			t.Errorf("allow rule %q is not an edit rule: the only path-scoped grants are edits", rule)
		}
	}

	for _, call := range []struct {
		tool  string
		input map[string]any
	}{
		{"Read", map[string]any{"file_path": list}},
		{"Read", map[string]any{"file_path": recording}},
		{"Glob", map[string]any{"pattern": "**/*.svg", "path": filepath.Dir(recording)}},
		{"Grep", map[string]any{"pattern": "<text", "path": recording}},
	} {
		if reason, allowed := screen.ToolCall(call.tool, call.input); !allowed {
			t.Errorf("the screen denies %s %v: %s", call.tool, call.input, reason)
		}
		// With the screen attached, the hook's allow is the only grant a read
		// tool has: a call it passes still needs the tool to be one it grants.
		if !screen.Grants(call.tool) {
			t.Errorf("a passing %s call gets no allow from the screen: %s is not in screen.Granted", call.tool, call.tool)
		}
	}
}
